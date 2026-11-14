package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/mcpclients"
	"flock/internal/node"
	"flock/internal/worktree"
)

// markWorktreesSince records, the first time a daemon that sets up worktrees runs, that it does
// from now on: worktrees that existed before are served but left as they are.
func (d *daemon) markWorktreesSince() {
	cfg := d.config()
	if cfg == nil || !cfg.WorktreesSince.IsZero() {
		return
	}
	_ = d.Mutate(func(c *config.Config) error {
		if c.WorktreesSince.IsZero() {
			c.WorktreesSince = time.Now()
		}
		return nil
	})
}

// syncWorktrees starts the setup of worktrees that need one, and forgets those that are gone,
// however they went (git, an agent app, deleting the folder), dropping the database Flock made for
// them. It runs after every reload and never waits: a setup saves config.json, which reloads.
func (d *daemon) syncWorktrees(cfg *config.Config) {
	if d.worktrees == nil {
		return
	}
	current := map[string]bool{}
	created := map[string]time.Time{} // when git created each worktree, by path
	scanned := map[string]bool{}      // parents whose worktrees are in created
	for _, site := range cfg.AllSites() {
		if !site.IsWorktree() {
			continue
		}
		current[filepath.Clean(site.Path)] = true
		if d.worktrees.Get(site.Path) != nil {
			continue // set up (or failed) since the daemon started, or running
		}
		if e, known := cfg.WorktreeEntry(site.Path); known {
			if !e.SetUp {
				d.startSetup(site, "") // a restart interrupted it
			}
			continue
		}
		if cfg.WorktreesSince.IsZero() {
			continue
		}
		if !scanned[site.Parent] {
			scanned[site.Parent] = true
			parent, _ := cfg.FindSite(site.Parent)
			for _, wt := range config.Worktrees(parent.Path) {
				created[filepath.Clean(wt.Path)] = wt.Created
			}
		}
		if created[filepath.Clean(site.Path)].Before(cfg.WorktreesSince) {
			continue
		}
		// A flock.yml that can't be read still starts it: the setup fails and says why.
		if parent, ok := cfg.FindSite(site.Parent); ok {
			if settings, err := worktree.Load(parent.Path, site.Path); err == nil && !settings.Auto {
				continue
			}
		}
		d.startSetup(site, "")
	}

	for _, p := range d.worktrees.Paths() {
		if !current[p] {
			go d.worktrees.Forget(p)
		}
	}
	for _, e := range cfg.Worktrees {
		if !current[filepath.Clean(e.Path)] && d.forgetting(e.Path) {
			go d.forgetGone(cfg, e)
		}
	}
}

// forgetting marks the worktree at path as being forgotten, and reports whether it wasn't already:
// reloads come quicker than a database is dropped.
func (d *daemon) forgetting(path string) bool {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	if d.gone == nil {
		d.gone = map[string]bool{}
	}
	if d.gone[filepath.Clean(path)] {
		return false
	}
	d.gone[filepath.Clean(path)] = true
	return true
}

// forgetGone drops the database Flock made for a worktree that is gone and forgets it. When the
// database can't be dropped (its server is stopped), the next reload tries again. It needs nothing
// but the record: the site may be gone too (unlinked).
func (d *daemon) forgetGone(cfg *config.Config, e config.WorktreeEntry) {
	defer func() {
		d.pendingMu.Lock()
		delete(d.gone, filepath.Clean(e.Path))
		d.pendingMu.Unlock()
	}()
	if e.Database != "" {
		job := worktree.Job{Path: e.Path, Database: worktree.Database{Engine: e.Engine, Name: e.Database, Service: e.Service}, Services: d.dbServices(cfg)}
		if err := job.DropDatabase(context.Background(), io.Discard); err != nil {
			d.setWarnings(append(d.currentWarnings(), fmt.Sprintf("worktree %s: %v", e.Path, err)))
			return
		}
	}
	_ = d.Mutate(func(c *config.Config) error {
		c.RemoveWorktreeEntry(e.Path)
		return nil
	})
}

// worktreeJob describes the setup (or removal) of the worktree at path of the site parentName,
// served as site (zero for one whose folder is gone).
func (d *daemon) worktreeJob(cfg *config.Config, parentName, path string, site config.Site) (worktree.Job, error) {
	parent, ok := cfg.FindSite(parentName)
	if !ok {
		return worktree.Job{}, fmt.Errorf("no site named %s", parentName)
	}
	settings, err := worktree.Load(parent.Path, path)
	if err != nil {
		return worktree.Job{}, err
	}
	entry, _ := cfg.WorktreeEntry(path)
	mode := entry.DBMode
	if mode == "" {
		mode = settings.Database
	}
	domain := entry.Slug + "." + cfg.Domain(parent)
	if site.Name != "" {
		domain = cfg.Domain(site)
	}
	return worktree.Job{
		Site: parent.Path, Path: path, Parent: parent.Name, Branch: site.Branch,
		Domain: domain, ParentDomain: cfg.Domain(parent), Secured: parent.Secured, Settings: settings,
		Flock:    mcpclients.Executable(d.root),
		BinDirs:  []string{filepath.Join(d.root, "bin"), node.GlobalBin(node.CurrentGlobalDir(d.root))},
		AsRoot:   runningAsRoot(),
		DBMode:   mode,
		Database: worktree.Database{Engine: entry.Engine, Name: entry.Database, Service: entry.Service},
		Servers:  d.dbServers(cfg),
		Services: d.dbServices(cfg),
		OnDatabase: func(db worktree.Database) {
			_ = d.Mutate(func(c *config.Config) error {
				if e, ok := c.WorktreeEntry(path); ok {
					e.Engine, e.Database, e.Service = db.Engine, db.Name, db.Service
					c.SetWorktreeEntry(e)
				}
				return nil
			})
		},
	}, nil
}

// startSetup sets the worktree up (with the database mode dbMode, when given). A mode asked for
// while it runs (see SetupWorktree) is switched to at its end.
func (d *daemon) startSetup(site config.Site, dbMode string) worktree.Status {
	return d.runJob(site, dbMode, func(ctx context.Context, log io.Writer, step func(string), job worktree.Job) error {
		if err := job.Run(ctx, log, step); err != nil {
			return err
		}
		mode := d.takePendingDB(site.Path)
		if mode == "" || mode == job.DBMode {
			return nil
		}
		err := d.Mutate(func(c *config.Config) error {
			if e, ok := c.WorktreeEntry(site.Path); ok {
				e.DBMode = mode
				c.SetWorktreeEntry(e)
			}
			return nil
		})
		if err != nil {
			return err
		}
		job.DBMode = mode
		return job.SwitchDatabase(ctx, log, step)
	}, func(e *config.WorktreeEntry) { e.SetUp = true })
}

// setPendingDB remembers a database mode asked for while the worktree's setup runs; that setup
// takes it (takePendingDB) when it's done.
func (d *daemon) setPendingDB(path, mode string) {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	if d.pendingDB == nil {
		d.pendingDB = map[string]string{}
	}
	d.pendingDB[filepath.Clean(path)] = mode
}

func (d *daemon) takePendingDB(path string) string {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	mode := d.pendingDB[filepath.Clean(path)]
	delete(d.pendingDB, filepath.Clean(path))
	return mode
}

// runJob runs work as the worktree's job. It records the worktree first (with dbMode, when given):
// so its URL stays when the branch changes, and a setup a restart interrupts runs again. done
// updates the entry once work succeeded.
func (d *daemon) runJob(site config.Site, dbMode string, work func(context.Context, io.Writer, func(string), worktree.Job) error, done func(*config.WorktreeEntry)) worktree.Status {
	path := site.Path
	setup := func(ctx context.Context, log io.Writer, step func(string)) error {
		slug, _, _ := strings.Cut(site.Name, ".")
		var cfg *config.Config
		err := d.Mutate(func(c *config.Config) error {
			e, ok := c.WorktreeEntry(path)
			if !ok {
				e = config.WorktreeEntry{Path: path, Site: site.Parent, Slug: slug}
			}
			if dbMode != "" {
				e.DBMode = dbMode
			}
			c.SetWorktreeEntry(e)
			cfg = c
			return nil
		})
		if err != nil {
			return err
		}
		job, err := d.worktreeJob(cfg, site.Parent, path, site)
		if err != nil {
			return err
		}
		return work(ctx, log, step, job)
	}
	finished := func(err error) {
		d.takePendingDB(path) // asked for too late for this job: the asker switches (see SetupWorktree)
		if err != nil {
			return
		}
		_ = d.Mutate(func(c *config.Config) error {
			if e, ok := c.WorktreeEntry(path); ok {
				done(&e)
				c.SetWorktreeEntry(e)
			}
			return nil
		})
	}
	return d.worktrees.Start(path, site.Name, setup, finished)
}

func (d *daemon) config() *config.Config {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfg
}

// worktreeAt finds the worktree site whose folder is path.
func (d *daemon) worktreeAt(path string) (*config.Config, config.Site, error) {
	cfg := d.config()
	if cfg == nil {
		return nil, config.Site{}, errors.New("the daemon has no configuration yet")
	}
	site, ok := cfg.SiteForDir(path)
	if !ok || !site.IsWorktree() || !config.SamePath(site.Path, path) {
		return cfg, config.Site{}, fmt.Errorf("%w: %s", api.ErrNotWorktree, path)
	}
	return cfg, site, nil
}

// busy reports whether the worktree's setup is queued or running.
func (d *daemon) busy(path string) (worktree.Status, bool) {
	if st := d.worktrees.Get(path); st != nil && (st.State == worktree.Queued || st.State == worktree.Running) {
		return *st, true
	}
	return worktree.Status{}, false
}

func validDBMode(mode string) error {
	switch mode {
	case worktree.Shared, worktree.Copy, worktree.Fresh:
		return nil
	}
	return fmt.Errorf("%w: the database mode is shared, copy or fresh, not %q", api.ErrInvalidRequest, mode)
}

// SetupWorktree implements api.Backend: it starts the worktree's setup, or joins the one that
// runs. A worktree that is set up already is left alone unless again, or switched to dbMode
// when that is another mode. A setup that runs switches to dbMode when it's done.
func (d *daemon) SetupWorktree(path string, again bool, dbMode string) (worktree.Status, error) {
	if dbMode != "" {
		if err := validDBMode(dbMode); err != nil {
			return worktree.Status{}, err
		}
	}
	cfg, site, err := d.worktreeAt(path)
	if err != nil {
		return worktree.Status{}, err
	}
	if st, ok := d.busy(site.Path); ok {
		if dbMode != "" {
			d.setPendingDB(site.Path, dbMode)
		}
		return st, nil
	}
	if !again {
		if st := d.worktreeStatus(cfg, site); st != nil && st.State == worktree.Ready {
			if job, err := d.worktreeJob(cfg, site.Parent, site.Path, site); err == nil && dbMode != "" && dbMode != job.DBMode {
				return d.SwitchWorktreeDatabase(path, dbMode)
			}
			return *st, nil
		}
	}
	return d.startSetup(site, dbMode), nil
}

// CreateWorktree implements api.Backend: it creates a worktree of the site on branch (see
// worktree.Create) and starts its setup.
func (d *daemon) CreateWorktree(ctx context.Context, req api.WorktreeCreate) (api.WorktreeCreated, error) {
	if req.DB != "" {
		if err := validDBMode(req.DB); err != nil {
			return api.WorktreeCreated{}, err
		}
	}
	site, err := d.mainSite(req.Site)
	if err != nil {
		return api.WorktreeCreated{}, err
	}
	if runningAsRoot() {
		return api.WorktreeCreated{}, fmt.Errorf("%w: Flock runs as root here, so it doesn't create worktrees: use `git worktree add`", api.ErrConflict)
	}
	path, branch, err := worktree.Create(ctx, site.Path, site.Name, req.Branch, req.From, req.Path)
	if errors.Is(err, worktree.ErrInvalid) {
		return api.WorktreeCreated{}, fmt.Errorf("%w: %v", api.ErrInvalidRequest, err)
	}
	if err != nil {
		return api.WorktreeCreated{}, err
	}
	created := api.WorktreeCreated{Path: path, Branch: branch}
	if err := d.reload(); err != nil {
		return created, err
	}
	created.Setup, err = d.SetupWorktree(path, false, req.DB)
	return created, err
}

// SiteBranches implements api.Backend: the git branches of a site that worktrees can be made of.
func (d *daemon) SiteBranches(ctx context.Context, name string) (api.WorktreeBranches, error) {
	site, err := d.mainSite(name)
	if err != nil {
		return api.WorktreeBranches{}, err
	}
	branches, err := worktree.ListBranches(ctx, site.Path)
	if errors.Is(err, worktree.ErrInvalid) {
		return branches, fmt.Errorf("%w: %v", api.ErrInvalidRequest, err)
	}
	return branches, err
}

// mainSite finds the site named name that worktrees can be made of: not a worktree itself.
func (d *daemon) mainSite(name string) (config.Site, error) {
	cfg := d.config()
	if cfg == nil {
		return config.Site{}, errors.New("the daemon has no configuration yet")
	}
	site, ok := cfg.FindSite(name)
	if !ok || site.IsWorktree() {
		return config.Site{}, fmt.Errorf("%w: no site named %s that worktrees can be made of", api.ErrInvalidRequest, name)
	}
	return site, nil
}

// SwitchWorktreeDatabase implements api.Backend: as the worktree's job, it gives the worktree a
// database by mode, dropping the one Flock made for it.
func (d *daemon) SwitchWorktreeDatabase(path, mode string) (worktree.Status, error) {
	if err := validDBMode(mode); err != nil {
		return worktree.Status{}, err
	}
	_, site, err := d.worktreeAt(path)
	if err != nil {
		return worktree.Status{}, err
	}
	if _, ok := d.busy(site.Path); ok {
		return worktree.Status{}, fmt.Errorf("%w: %s is being set up — wait for it with `flock worktree setup`", api.ErrConflict, site.Name)
	}
	return d.runJob(site, mode, func(ctx context.Context, log io.Writer, step func(string), job worktree.Job) error {
		return job.SwitchDatabase(ctx, log, step)
	}, func(*config.WorktreeEntry) {}), nil
}

// WorktreeSetup implements api.Backend: the state of the worktree's setup, nil when Flock hasn't
// set it up.
func (d *daemon) WorktreeSetup(path string) (*worktree.Status, error) {
	cfg, site, err := d.worktreeAt(path)
	if err != nil {
		return nil, err
	}
	return d.worktreeStatus(cfg, site), nil
}

func (d *daemon) worktreeStatus(cfg *config.Config, site config.Site) *worktree.Status {
	st := d.worktrees.Get(site.Path)
	e, known := cfg.WorktreeEntry(site.Path)
	if st == nil && known && e.SetUp {
		st = &worktree.Status{State: worktree.Ready, Log: worktree.LogName(site.Name)}
	}
	if st != nil && known {
		st.DBMode, st.Database = e.DBMode, e.Database
	}
	return st
}

// RemoveWorktree implements api.Backend: it stops the worktree's setup, runs its before_delete
// hook, removes it with git and drops the database Flock made for it. It returns what the hook,
// git and the database tools printed.
func (d *daemon) RemoveWorktree(ctx context.Context, path string, force bool) (string, error) {
	cfg, site, err := d.worktreeAt(path)
	if err != nil {
		return "", err
	}
	job, err := d.worktreeJob(cfg, site.Parent, site.Path, site)
	if err != nil {
		return "", err
	}
	d.worktrees.Forget(site.Path)
	var log bytes.Buffer
	if err := job.Remove(ctx, &log, force); err != nil {
		return log.String(), err
	}
	if err := job.DropDatabase(ctx, &log); err != nil {
		// Left in config.json: syncWorktrees tries again.
		return log.String(), fmt.Errorf("the worktree is removed, but its database isn't: %w", err)
	}
	err = d.Mutate(func(c *config.Config) error {
		c.RemoveWorktreeEntry(site.Path)
		return nil
	})
	return log.String(), err
}

// withWorktrees adds the setup state to the worktrees among the statuses. The caller holds d.mu.
func (d *daemon) withWorktrees(sites []api.SiteStatus) []api.SiteStatus {
	if d.worktrees == nil || d.cfg == nil {
		return sites
	}
	for i, s := range sites {
		if s.Parent == "" {
			continue
		}
		sites[i].Worktree = d.worktreeStatus(d.cfg, config.Site{Name: s.Name, Path: s.Path})
	}
	return sites
}
