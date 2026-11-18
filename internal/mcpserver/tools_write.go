package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"flock/internal/api"
	"flock/internal/catalog"
	"flock/internal/config"
	"flock/internal/node"
	"flock/internal/php"
	"flock/internal/postgres"
	"flock/internal/project"
)

// additive marks tools that change something but never remove anything.
func additive(idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: ptr(false), IdempotentHint: idempotent, OpenWorldHint: ptr(false)}
}

// schema infers T's input schema and restricts some of its properties to fixed values.
func schema[T any](enums map[string][]any) *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	for name, values := range enums {
		s.Properties[name].Enum = values
	}
	return s
}

func (s *server) addWriteTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "service",
		Description: "Start, stop or install a service (see list_services for the ids). Installs download (or build) and start it; " +
			"when one takes longer than about a minute the tool answers that it is still installing, and list_services shows its progress.",
		Annotations: additive(true),
		InputSchema: schema[serviceIn](map[string][]any{"action": {"start", "stop", "install"}}),
	}, s.service)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "install_version",
		Description: "Install a PHP version (e.g. 8.4: its newest release) or a Node.js version (a major, e.g. 22). " +
			"When the download takes longer than about a minute the tool answers that it is still " +
			"installing; list_php_versions / list_node_versions show the progress.",
		Annotations: additive(true),
		InputSchema: schema[installVersionIn](map[string][]any{"tool": {"php", "node"}}),
	}, s.installVersion)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "link_site",
		Description: "Serve a project folder as a Flock site at https://<name>.test (by default the project Flock was started in, named after its folder).",
		Annotations: additive(true),
	}, s.linkSite)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "proxy_site",
		Description: "Serve a server of its own, e.g. a dashboard, an object store, a Docker container or a Go or Python app, at " +
			"http(s)://<name>.test: every request, WebSocket upgrades included, passes to the upstream with its Host header. " +
			"Proxying a name again changes its upstream. With a command, Flock can start and stop the server " +
			"(proxy_server); without one, start the server yourself.",
		Annotations: additive(true),
	}, s.proxySite)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "proxy_server",
		Description: "Start or stop the server of a proxy that has a command (see proxy_site); its output is the service log the proxy's command names.",
		Annotations: additive(false),
		InputSchema: schema[proxyServerIn](map[string][]any{"action": {"start", "stop"}}),
	}, s.proxyServer)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "update_site",
		Description: "Change a site:\n" +
			"- secure / unsecure: serve it over HTTPS or HTTP.\n" +
			"- isolate (tool, version) / unisolate (tool): use a PHP or Node.js version for the site on this machine only.\n" +
			"- pin (tool, version) / unpin (tool): write the version into the project's .flockrc (committed, so everyone " +
			"working on the project gets it); pin without a version pins the one the site uses now.\n" +
			"- add_domain (domain): an extra domain, a subdomain (\"tenant1\" → tenant1.<site>.test) or an alias (\"my-app-2.test\").\n" +
			"- set_server (server, worker): run PHP through FastCGI (PHP-FPM) or FrankenPHP, optionally in Octane's worker mode.\n" +
			"- set_type (type): serve it as a php site or as a node app, whose dev server (npm run dev) Flock starts on the first visit.\n" +
			"- reverb_on / reverb_off: run or stop the Laravel app's Reverb server, reached at wss://<site>.test/app; site_info shows the .env lines it needs.\n" +
			"- restart: restart a FrankenPHP site's process, e.g. so Octane workers load changed code, or a Node.js app's dev server.\n" +
			"- restart_reverb: restart the site's Reverb server, e.g. so it loads changed code.\n" +
			"- stop: stop a Node.js app's dev server until the site's next request (it also stops after 30 idle minutes).",
		Annotations: additive(false),
		InputSchema: schema[updateSiteIn](map[string][]any{
			"action": {"secure", "unsecure", "isolate", "unisolate", "pin", "unpin", "add_domain", "set_server", "set_type", "reverb_on", "reverb_off", "restart", "restart_reverb", "stop"},
			"type":   {"php", "node"},
			"tool":   {"php", "node"},
			"server": {"fastcgi", "frankenphp"},
			"worker": {"classic", "octane"},
		}),
	}, s.updateSite)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "set_debugging",
		Description: "Turn Xdebug or dump capture on or off. With xdebug on, requests that ask for it (the XDEBUG_SESSION " +
			"cookie or parameter, or a browser extension) run with Xdebug. With dumps on, dump() and dd() output goes to Flock " +
			"(read it with get_dumps) instead of into the page, and Flock records queries, logs, views, jobs and requests " +
			"(read them with get_events).",
		Annotations: additive(true),
		InputSchema: schema[debuggingIn](map[string][]any{"feature": {"xdebug", "dumps"}}),
	}, s.setDebugging)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "postgres_extension",
		Description: "List or install the PostgreSQL extensions Flock offers (PostGIS, pgRouting, pgvector, TimescaleDB), " +
			"per PostgreSQL version (default: the one that runs). Installing makes the extension available to that " +
			"version; the database still needs CREATE EXTENSION (the answer says what to run, e.g. in a migration). " +
			"pgRouting brings PostGIS along; TimescaleDB restarts PostgreSQL to load it. list also shows the user's own " +
			"extensions in that version's extensions folder.",
		Annotations: additive(true),
		InputSchema: schema[postgresExtensionIn](map[string][]any{
			"action":    {"list", "install"},
			"extension": {"postgis", "pgrouting", "pgvector", "timescaledb"},
		}),
	}, s.postgresExtension)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "create_database",
		Description: "Create a database in Flock's PostgreSQL, MySQL or MariaDB (user postgres or root, no password). " +
			"An existing database is left as it is.",
		Annotations: additive(true),
		InputSchema: schema[createDatabaseIn](map[string][]any{"service": {"postgres", "mysql", "mariadb"}}),
	}, s.createDatabase)
}

// actionOut is what a tool that changes something answers.
type actionOut struct {
	// Done is false when an install is still running after the tool stopped waiting.
	Done    bool   `json:"done"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------------------------
// waiting for installs

// step is the state of an install: done, failed (err), or still running (what it is doing,
// and how far along it is: 0..1, or 0 when unknown).
type step struct {
	done     bool
	text     string
	progress float64
}

// await polls check until the install is done or fails, or Wait passes. Meanwhile it sends progress
// notifications when the client asked for them. It reports whether the install finished.
func (s *server) await(ctx context.Context, req *mcp.CallToolRequest, check func(context.Context) (step, error)) (bool, string, error) {
	deadline := time.Now().Add(s.Wait)
	poll := min(time.Second, s.Wait/10)
	var token any
	if req != nil && req.Params != nil {
		token = req.Params.GetProgressToken()
	}
	sent, last := 0.0, ""

	for {
		st, err := check(ctx)
		if err != nil || st.done {
			return st.done, st.text, err
		}
		if token != nil && st.text != last && req.Session != nil {
			sent = max(sent+1, st.progress*100)
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Message: st.text, Progress: sent})
			last = st.text
		}
		if time.Now().After(deadline) {
			return false, st.text, nil
		}
		select {
		case <-ctx.Done():
			return false, st.text, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// background runs installs the daemon only offers as one long request (Node.js, PostgreSQL), so a
// tool can answer before they finish. They end with the session.
type background struct {
	mu   sync.Mutex
	jobs map[string]*bgJob
}

type bgJob struct {
	done bool
	err  error
}

// start runs do as job key, unless that job is already running.
func (b *background) start(key string, do func(context.Context) error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.jobs == nil {
		b.jobs = map[string]*bgJob{}
	}
	if j := b.jobs[key]; j != nil && !j.done {
		return
	}
	j := &bgJob{}
	b.jobs[key] = j
	go func() {
		err := do(context.Background())
		b.mu.Lock()
		j.done, j.err = true, err
		b.mu.Unlock()
	}()
}

// state reports whether job key finished, and how.
func (b *background) state(key string) (done bool, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if j := b.jobs[key]; j != nil {
		return j.done, j.err
	}
	return true, nil
}

// running lists the jobs that haven't finished.
func (b *background) running() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var keys []string
	for k, j := range b.jobs {
		if !j.done {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// stillRunning is the answer when an install outlasts the wait.
func stillRunning(what, doing, check string) actionOut {
	msg := what + " is still installing"
	if doing != "" {
		msg += " (" + doing + ")"
	}
	return actionOut{Message: msg + ". It continues in the background; " + check + " shows when it's done."}
}

// ---------------------------------------------------------------------------------------------
// services

type serviceIn struct {
	Action  string `json:"action" jsonschema:"start, stop or install"`
	Service string `json:"service" jsonschema:"the service's id from list_services, e.g. mysql, postgres, redis, valkey or dumps"`
	Version string `json:"version,omitempty" jsonschema:"install only: the version line, e.g. 8.4 for MySQL or 18 for PostgreSQL (default: the newest)"`
	Port    int    `json:"port,omitempty" jsonschema:"install only: the port (default: the usual one, or the next free one)"`
}

func (s *server) service(ctx context.Context, req *mcp.CallToolRequest, in serviceIn) (*mcp.CallToolResult, actionOut, error) {
	id := strings.ToLower(in.Service)
	switch in.Action {
	case "start", "stop":
		services, err := s.Daemon.SetServiceRunning(ctx, id, in.Action == "start")
		if err != nil {
			return nil, actionOut{}, err
		}
		msg := fmt.Sprintf("%s is %s", id, map[bool]string{true: "on", false: "off"}[in.Action == "start"])
		for _, svc := range services {
			if svc.ID == id {
				msg = fmt.Sprintf("%s is %s", svc.Name, runState(svc))
			}
		}
		return nil, actionOut{Done: true, Message: msg}, nil
	case "install":
		return s.installService(ctx, req, id, in.Version, in.Port)
	}
	return nil, actionOut{}, fmt.Errorf("unknown action %q: use start, stop or install", in.Action)
}

func runState(svc api.Service) string {
	switch {
	case svc.Running:
		state := "running"
		if svc.Port != 0 {
			state += fmt.Sprintf(" on 127.0.0.1:%d", svc.Port)
		}
		return state
	case svc.Enabled && svc.Problem != "":
		return "not running: " + svc.Problem
	case svc.Enabled:
		return "starting"
	}
	return "stopped"
}

func (s *server) installService(ctx context.Context, req *mcp.CallToolRequest, id, version string, port int) (*mcp.CallToolResult, actionOut, error) {
	var check func(context.Context) (step, error)
	var name string

	switch id {
	case "postgres", "postgresql", "pgsql":
		id, name = "postgres", "PostgreSQL"
		major := postgres.DefaultMajor
		if version != "" {
			major = version
			if m := postgres.MajorOf(version); m != "" {
				major = m // a version like "18.6": Flock installs the newest build of its major
			}
		}
		name += " " + major
		key := "postgres " + major
		s.bg.start(key, func(ctx context.Context) error {
			_, err := s.Daemon.InstallPostgres(ctx, major)
			return err
		})
		check = func(ctx context.Context) (step, error) {
			if done, err := s.bg.state(key); done {
				return step{done: true}, err
			}
			text := "installing"
			if v, err := s.Daemon.PostgresVersions(ctx); err == nil {
				for _, m := range v.Majors {
					if m.Major == major && m.Busy != "" {
						text = m.Busy
					}
				}
			}
			return step{text: text}, nil
		}

	default:
		cat, err := s.Daemon.InstallService(ctx, id, version, port)
		if err != nil {
			return nil, actionOut{}, err
		}
		e, _ := cat.Find(id)
		name = strings.TrimSpace(cmpOr(e.Name, id) + " " + version)
		check = func(ctx context.Context) (step, error) {
			c, err := s.Daemon.Catalog(ctx)
			if err != nil {
				return step{}, err
			}
			e, _ := c.Find(id)
			switch {
			case e.Job == nil:
				return step{done: true}, nil
			case e.Job.State == "failed":
				return step{}, fmt.Errorf("installing %s failed: %s (the log is %s)", e.Name, e.Job.Error, catalog.LogFile(s.Root, id+"-install"))
			}
			text, _ := jobState(e.Job.State, e.Job.Phase, e.Job.Progress, "")
			return step{text: text, progress: e.Job.Progress}, nil
		}
	}

	done, doing, err := s.await(ctx, req, check)
	if err != nil {
		return nil, actionOut{}, err
	}
	if !done {
		return nil, stillRunning(name, doing, "list_services"), nil
	}

	msg := name + " is installed"
	if services, err := s.Daemon.Services(ctx); err == nil {
		for _, svc := range services {
			if svc.ID == id && version != "" && svc.Version != version && !strings.HasPrefix(svc.Version, version+".") {
				// Versions install side by side; the one that ran before keeps running.
				msg = fmt.Sprintf("%s is installed next to %s %s, which keeps running. The user can switch versions in Flock's Settings → Services or with `flock services use %s %s`.",
					name, svc.Name, svc.Version, id, version)
			} else if svc.ID == id {
				msg = fmt.Sprintf("%s %s is installed and %s.", svc.Name, svc.Version, runState(svc))
				if svc.Env != "" {
					msg += " To connect a Laravel app, set in .env:\n" + svc.Env
				}
			}
		}
	}
	return nil, actionOut{Done: true, Message: msg}, nil
}

// ---------------------------------------------------------------------------------------------
// PostgreSQL extensions

type postgresExtensionIn struct {
	Action    string `json:"action" jsonschema:"list or install"`
	Extension string `json:"extension,omitempty" jsonschema:"install only: postgis, pgrouting, pgvector or timescaledb"`
	Major     string `json:"major,omitempty" jsonschema:"the PostgreSQL version, e.g. 18 (default: the one that runs)"`
}

func (s *server) postgresExtension(ctx context.Context, req *mcp.CallToolRequest, in postgresExtensionIn) (*mcp.CallToolResult, actionOut, error) {
	major := in.Major
	if major == "" {
		v, err := s.Daemon.PostgresVersions(ctx)
		if err != nil {
			return nil, actionOut{}, err
		}
		if major = v.Active; major == "" {
			return nil, actionOut{}, errors.New("PostgreSQL isn't installed: install it first with the service tool (service postgres)")
		}
	}
	switch in.Action {
	case "list":
		state, err := s.Daemon.PostgresExtensions(ctx, major)
		if err != nil {
			return nil, actionOut{}, err
		}
		return nil, actionOut{Done: true, Message: describeExtensions(state)}, nil
	case "install":
		return s.installPostgresExtension(ctx, req, major, strings.ToLower(in.Extension))
	}
	return nil, actionOut{}, fmt.Errorf("unknown action %q: use list or install", in.Action)
}

func (s *server) installPostgresExtension(ctx context.Context, req *mcp.CallToolRequest, major, name string) (*mcp.CallToolResult, actionOut, error) {
	e, ok := postgres.FindExtension(name)
	if !ok {
		return nil, actionOut{}, fmt.Errorf("unknown extension %q: use postgis, pgrouting, pgvector or timescaledb", name)
	}
	what := fmt.Sprintf("%s for PostgreSQL %s", e.Title, major)
	key := "postgres-extension " + major + "/" + name
	s.bg.start(key, func(ctx context.Context) error {
		_, err := s.Daemon.InstallPostgresExtension(ctx, major, name)
		return err
	})
	done, doing, err := s.await(ctx, req, func(ctx context.Context) (step, error) {
		if done, err := s.bg.state(key); done {
			return step{done: true}, err
		}
		text := "installing"
		if state, err := s.Daemon.PostgresExtensions(ctx, major); err == nil {
			for _, x := range state.Extensions {
				if x.Name == name && x.Busy != "" {
					text = x.Busy // "waiting" behind another operation, or "installing"
				}
			}
		}
		return step{text: text}, nil
	})
	if err != nil {
		return nil, actionOut{}, err
	}
	if !done {
		return nil, stillRunning(what, doing, "postgres_extension list"), nil
	}

	creates := []string{e.Creates}
	for _, r := range e.Requires {
		if required, ok := postgres.FindExtension(r); ok {
			creates = append([]string{required.Creates}, creates...)
		}
	}
	var statements, migration []string
	for _, c := range creates {
		statements = append(statements, "CREATE EXTENSION IF NOT EXISTS "+c+";")
		migration = append(migration, fmt.Sprintf("DB::statement('CREATE EXTENSION IF NOT EXISTS %s');", c))
	}
	msg := what + " is installed."
	if len(e.Requires) > 0 {
		msg = fmt.Sprintf("%s is installed, with the extensions it needs.", what)
	}
	if e.Preload {
		msg += fmt.Sprintf(" PostgreSQL %s restarted to load it.", major)
	}
	msg += " Enable it in each database that uses it: " + strings.Join(statements, " ") +
		" In a Laravel migration: " + strings.Join(migration, " ")
	return nil, actionOut{Done: true, Message: msg}, nil
}

// describeExtensions lists a major's extensions for an agent: Flock's and the user's own.
func describeExtensions(state api.PostgresExtensions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PostgreSQL %s extensions:\n", state.Major)
	for _, e := range state.Extensions {
		switch {
		case e.Busy != "":
			fmt.Fprintf(&b, "- %s: %s\n", e.Name, e.Busy)
		case e.Installed != "":
			line := fmt.Sprintf("- %s %s: installed (CREATE EXTENSION %s)", e.Name, e.Installed, e.Creates)
			if e.Update {
				line += ", update to " + e.Latest + " available"
			}
			b.WriteString(line + "\n")
		case e.Supported:
			fmt.Fprintf(&b, "- %s: not installed (%s %s available)\n", e.Name, e.Title, e.Latest)
		default:
			fmt.Fprintf(&b, "- %s: not available for PostgreSQL %s\n", e.Name, state.Major)
		}
	}
	if len(state.Custom) > 0 {
		b.WriteString("The user's own extensions (" + state.CustomDir + "):\n")
		for _, x := range state.Custom {
			if x.Linked {
				fmt.Fprintf(&b, "- %s: CREATE EXTENSION %s\n", x.Name, x.Name)
			} else {
				fmt.Fprintf(&b, "- %s: can't be used: %s\n", x.Name, x.Problem)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// ---------------------------------------------------------------------------------------------
// versions

type installVersionIn struct {
	Tool    string `json:"tool" jsonschema:"php or node"`
	Version string `json:"version" jsonschema:"PHP: a minor version like 8.4. Node.js: a major like 22."`
}

func (s *server) installVersion(ctx context.Context, req *mcp.CallToolRequest, in installVersionIn) (*mcp.CallToolResult, actionOut, error) {
	switch in.Tool {
	case "php":
		return s.installPHP(ctx, req, in.Version)
	case "node":
		return s.installNode(ctx, req, in.Version)
	}
	return nil, actionOut{}, fmt.Errorf("unknown tool %q: use php or node", in.Tool)
}

func (s *server) installPHP(ctx context.Context, req *mcp.CallToolRequest, version string) (*mcp.CallToolResult, actionOut, error) {
	minor := php.MinorOf(version)
	if minor == "" {
		return nil, actionOut{}, fmt.Errorf("give a PHP version like 8.4, not %q", version)
	}

	versions, err := s.Daemon.PHPVersions(ctx)
	if err != nil {
		return nil, actionOut{}, err
	}
	v, ok := versions.Find(minor)
	if !ok {
		// A version the daemon hasn't looked up yet: fetch the releases first.
		if versions, err = s.Daemon.CheckPHP(ctx); err != nil {
			return nil, actionOut{}, err
		}
		if v, ok = versions.Find(minor); !ok {
			return nil, actionOut{}, fmt.Errorf("there is no PHP %s release Flock can install", minor)
		}
	}
	switch {
	case v.Installed != "" && v.Job == nil:
		return nil, actionOut{Done: true, Message: fmt.Sprintf("PHP %s is already installed (%s).", minor, v.Installed)}, nil
	case v.Latest == "":
		return nil, actionOut{}, fmt.Errorf("no PHP %s build is published for this platform", minor)
	}

	if _, err := s.Daemon.InstallPHP(ctx, minor); err != nil {
		return nil, actionOut{}, err
	}
	done, doing, err := s.await(ctx, req, func(ctx context.Context) (step, error) {
		versions, err := s.Daemon.PHPVersions(ctx)
		if err != nil {
			return step{}, err
		}
		v, _ := versions.Find(minor)
		switch {
		case v.Job == nil && v.Installed != "":
			return step{done: true, text: v.Installed}, nil
		case v.Job == nil:
			return step{}, fmt.Errorf("the PHP %s install was canceled", minor)
		case v.Job.State == "failed":
			return step{}, fmt.Errorf("installing PHP %s failed: %s (the log is %s)", minor, v.Job.Error, php.LogPath(s.Root, minor))
		}
		return step{text: v.Job.Text(minor)}, nil
	})
	if err != nil {
		return nil, actionOut{}, err
	}
	if !done {
		return nil, stillRunning("PHP "+minor, doing, "list_php_versions"), nil
	}
	return nil, actionOut{Done: true, Message: fmt.Sprintf("PHP %s is installed (%s).", minor, doing)}, nil
}

func (s *server) installNode(ctx context.Context, req *mcp.CallToolRequest, version string) (*mcp.CallToolResult, actionOut, error) {
	major := node.MajorOf(version)
	if major == "" {
		major = version
	}
	if err := project.ValidVersion(project.Node, major); err != nil {
		return nil, actionOut{}, fmt.Errorf("give a Node.js major version like 22, not %q", version)
	}
	if cfg, err := s.Daemon.Config(ctx); err == nil {
		for _, v := range cfg.Node {
			if node.MajorOf(v) == major {
				return nil, actionOut{Done: true, Message: fmt.Sprintf("Node.js %s is already installed (%s).", major, v)}, nil
			}
		}
	}

	key := "Node.js " + major
	s.bg.start(key, func(ctx context.Context) error {
		_, err := s.Daemon.InstallNode(ctx, major)
		return err
	})
	done, _, err := s.await(ctx, req, func(context.Context) (step, error) {
		done, err := s.bg.state(key)
		return step{done: done, text: "downloading"}, err
	})
	if err != nil {
		return nil, actionOut{}, err
	}
	if !done {
		return nil, stillRunning(key, "downloading", "list_node_versions"), nil
	}
	return nil, actionOut{Done: true, Message: key + " is installed."}, nil
}

// ---------------------------------------------------------------------------------------------
// sites

type linkSiteIn struct {
	Path string `json:"path,omitempty" jsonschema:"the project folder (absolute); defaults to the folder Flock was started in"`
	Name string `json:"name,omitempty" jsonschema:"the site's name, which makes its domain <name>.test; defaults to the folder's name"`
}

type siteActionOut struct {
	Message string    `json:"message"`
	Site    *siteInfo `json:"site,omitempty"`
}

func (s *server) linkSite(ctx context.Context, _ *mcp.CallToolRequest, in linkSiteIn) (*mcp.CallToolResult, siteActionOut, error) {
	dir := cmpOr(in.Path, s.Dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, siteActionOut{}, errors.New("give the project's folder as an absolute path")
	}
	dir = filepath.Clean(dir)

	if in.Name == "" {
		if st, err := s.Daemon.Status(ctx); err == nil {
			for _, site := range st.Sites {
				if site.Path == dir {
					info := siteInfoOf(site)
					return nil, siteActionOut{Message: fmt.Sprintf("%s is already served at %s.", dir, info.URL), Site: &info}, nil
				}
			}
		}
	}

	st, err := s.Daemon.LinkSite(ctx, dir, in.Name)
	if err != nil {
		return nil, siteActionOut{}, err
	}
	name := config.SiteName(cmpOr(in.Name, filepath.Base(dir)))
	for _, site := range st.Sites {
		if site.Name == name {
			info := siteInfoOf(site)
			msg := fmt.Sprintf("Linked: %s is served at %s.", dir, info.URL)
			if info.Problem != "" {
				msg += " Problem: " + info.Problem
			}
			return nil, siteActionOut{Message: msg, Site: &info}, nil
		}
	}
	return nil, siteActionOut{Message: "Linked " + dir + " as " + name + "."}, nil
}

type proxySiteIn struct {
	Name        string  `json:"name" jsonschema:"the proxy's name, which makes its domain <name>.test"`
	Upstream    string  `json:"upstream" jsonschema:"where requests go: a port on 127.0.0.1 (3000), host:port (localhost:5173) or a URL (https://127.0.0.1:8443)"`
	Secure      *bool   `json:"secure,omitempty" jsonschema:"serve over HTTPS; left out, a new proxy is served over HTTP and an existing one keeps its setting"`
	Command     *string `json:"command,omitempty" jsonschema:"the command that starts the server (one line), so proxy_server can start and stop it; empty removes it; left out, it stays as it is"`
	Dir         string  `json:"dir,omitempty" jsonschema:"the absolute folder the command runs in; left out, the project Flock was started in"`
	StopCommand *string `json:"stop_command,omitempty" jsonschema:"for a command that starts the server in the background and exits (docker compose up -d): the command that stops it"`
}

type proxySiteOut struct {
	Message string           `json:"message"`
	Proxy   *api.ProxyStatus `json:"proxy,omitempty"`
}

func (s *server) proxySite(ctx context.Context, _ *mcp.CallToolRequest, in proxySiteIn) (*mcp.CallToolResult, proxySiteOut, error) {
	if in.Name == "" || in.Upstream == "" {
		return nil, proxySiteOut{}, errors.New("give the proxy's name and its upstream, e.g. name softpack and upstream 3000")
	}
	st, err := s.Daemon.Proxy(ctx, in.Name, in.Upstream, in.Secure)
	if err != nil {
		return nil, proxySiteOut{}, err
	}
	name := config.SiteName(in.Name)
	if in.Command != nil || in.StopCommand != nil || in.Dir != "" {
		change := api.ProxyChange{Command: in.Command, StopCommand: in.StopCommand}
		dir := in.Dir
		if dir == "" && in.Command != nil && *in.Command != "" {
			dir = s.Dir
		}
		if dir != "" {
			change.Dir = &dir
		}
		if st, err = s.Daemon.UpdateProxy(ctx, name, change); err != nil {
			return nil, proxySiteOut{}, err
		}
	}
	for _, p := range st.Proxies {
		if p.Name == name {
			if p.Problem != "" {
				return nil, proxySiteOut{Message: "Proxied, but not served: " + p.Problem, Proxy: &p}, nil
			}
			return nil, proxySiteOut{Message: fmt.Sprintf("Proxied: %s passes to %s.", p.URL, p.Upstream), Proxy: &p}, nil
		}
	}
	return nil, proxySiteOut{Message: "Proxied " + name + "."}, nil
}

type proxyServerIn struct {
	Name   string `json:"name" jsonschema:"the proxy's name"`
	Action string `json:"action" jsonschema:"start or stop"`
}

func (s *server) proxyServer(ctx context.Context, _ *mcp.CallToolRequest, in proxyServerIn) (*mcp.CallToolResult, proxySiteOut, error) {
	var (
		st  api.Status
		err error
	)
	switch in.Action {
	case "start":
		st, err = s.Daemon.StartProxy(ctx, in.Name)
	case "stop":
		st, err = s.Daemon.StopProxy(ctx, in.Name)
	default:
		return nil, proxySiteOut{}, fmt.Errorf("unknown action %q: start or stop", in.Action)
	}
	if err != nil {
		return nil, proxySiteOut{}, err
	}
	for _, p := range st.Proxies {
		if p.Name == in.Name && p.Command != nil {
			msg := fmt.Sprintf("%s's server is %s.", p.Name, p.Command.State)
			if in.Action == "start" {
				msg = fmt.Sprintf("Starting %s's server; its output is the service log %s, and the proxy's command shows when it runs.", p.Name, p.Command.Log)
			}
			return nil, proxySiteOut{Message: msg, Proxy: &p}, nil
		}
	}
	return nil, proxySiteOut{Message: "Done."}, nil
}

type updateSiteIn struct {
	siteArg
	Action  string `json:"action" jsonschema:"what to change; see the tool's description"`
	Tool    string `json:"tool,omitempty" jsonschema:"isolate, unisolate, pin and unpin: php or node"`
	Version string `json:"version,omitempty" jsonschema:"isolate and pin: e.g. 8.4 for PHP or 22 for Node.js"`
	Domain  string `json:"domain,omitempty" jsonschema:"add_domain: a subdomain (tenant1) or an alias (my-app-2.test)"`
	Server  string `json:"server,omitempty" jsonschema:"set_server: fastcgi or frankenphp"`
	Worker  string `json:"worker,omitempty" jsonschema:"set_server with frankenphp: classic (the default) or octane (Laravel Octane's worker mode)"`
	Type    string `json:"type,omitempty" jsonschema:"set_type: php or node"`
}

func (s *server) updateSite(ctx context.Context, _ *mcp.CallToolRequest, in updateSiteIn) (*mcp.CallToolResult, siteActionOut, error) {
	site, err := s.site(ctx, in.Site)
	if err != nil {
		return nil, siteActionOut{}, err
	}
	needTool := func() error {
		if in.Tool != project.PHP && in.Tool != project.Node {
			return fmt.Errorf("%s needs tool: php or node", in.Action)
		}
		return nil
	}

	var st api.Status
	var msg string
	switch in.Action {
	case "secure", "unsecure":
		st, err = s.Daemon.UpdateSite(ctx, site.Name, api.SiteChange{Secured: ptr(in.Action == "secure")})
		msg = fmt.Sprintf("%s is served over %s.", site.Name, map[bool]string{true: "HTTPS", false: "HTTP"}[in.Action == "secure"])
	case "isolate", "unisolate":
		if err := needTool(); err != nil {
			return nil, siteActionOut{}, err
		}
		version := ""
		if in.Action == "isolate" {
			if version = in.Version; version == "" {
				return nil, siteActionOut{}, errors.New("isolate needs version")
			}
		}
		change := api.SiteChange{PHP: &version}
		if in.Tool == project.Node {
			change = api.SiteChange{Node: &version}
		}
		st, err = s.Daemon.UpdateSite(ctx, site.Name, change)
		msg = fmt.Sprintf("%s's %s version is now set for this machine only.", site.Name, in.Tool)
		if in.Action == "unisolate" {
			msg = fmt.Sprintf("%s's %s version comes from the project or Flock's default again.", site.Name, in.Tool)
		}
	case "pin":
		if err := needTool(); err != nil {
			return nil, siteActionOut{}, err
		}
		st, err = s.Daemon.PinSite(ctx, site.Name, in.Tool, in.Version)
		msg = fmt.Sprintf("Pinned %s's %s version in the project.", site.Name, in.Tool)
	case "unpin":
		if err := needTool(); err != nil {
			return nil, siteActionOut{}, err
		}
		st, err = s.Daemon.UnpinSite(ctx, site.Name, in.Tool)
		msg = fmt.Sprintf("Removed the %s version pinned in %s.", in.Tool, site.Name)
	case "add_domain":
		if in.Domain == "" {
			return nil, siteActionOut{}, errors.New("add_domain needs domain")
		}
		st, err = s.Daemon.AddDomain(ctx, site.Name, in.Domain)
		msg = fmt.Sprintf("Added %s to %s.", in.Domain, site.Name)
	case "set_server":
		if in.Server != config.ServerFastCGI && in.Server != config.ServerFrankenPHP {
			return nil, siteActionOut{}, errors.New("set_server needs server: fastcgi or frankenphp")
		}
		change := api.SiteChange{Server: &in.Server}
		switch in.Worker {
		case "octane":
			change.Worker = ptr(config.WorkerOctane)
		case "classic":
			change.Worker = ptr("")
		}
		st, err = s.Daemon.UpdateSite(ctx, site.Name, change)
		msg = fmt.Sprintf("%s now runs on %s.", site.Name, in.Server)
	case "set_type":
		if in.Type != config.TypePHP && in.Type != config.TypeNode {
			return nil, siteActionOut{}, errors.New("set_type needs type: php or node")
		}
		st, err = s.Daemon.UpdateSite(ctx, site.Name, api.SiteChange{Type: &in.Type})
		msg = fmt.Sprintf("%s is served as a %s site.", site.Name, config.TypeName(in.Type))
	case "reverb_on", "reverb_off":
		on := in.Action == "reverb_on"
		st, err = s.Daemon.UpdateSite(ctx, site.Name, api.SiteChange{Reverb: &on})
		msg = fmt.Sprintf("Reverb is %s for %s.", map[bool]string{true: "on", false: "off"}[on], site.Name)
	case "restart":
		st, err = s.Daemon.RestartSite(ctx, site.Name)
		msg = fmt.Sprintf("Restarted %s.", site.Name)
	case "restart_reverb":
		st, err = s.Daemon.RestartReverb(ctx, site.Name)
		msg = fmt.Sprintf("Restarted %s's Reverb server.", site.Name)
	case "stop":
		st, err = s.Daemon.StopSite(ctx, site.Name)
		msg = fmt.Sprintf("Stopped %s's dev server; the next request starts it again.", site.Name)
	default:
		return nil, siteActionOut{}, fmt.Errorf("unknown action %q", in.Action)
	}
	if err != nil {
		return nil, siteActionOut{}, err
	}

	out := siteActionOut{Message: msg}
	for _, updated := range st.Sites {
		if updated.Name == site.Name {
			info := siteInfoOf(updated)
			out.Site = &info
			if info.Problem != "" {
				out.Message += " Problem: " + info.Problem
			}
		}
	}
	return nil, out, nil
}

// ---------------------------------------------------------------------------------------------
// debugging

type debuggingIn struct {
	Feature string `json:"feature" jsonschema:"xdebug or dumps"`
	Enabled bool   `json:"enabled"`
}

func (s *server) setDebugging(ctx context.Context, _ *mcp.CallToolRequest, in debuggingIn) (*mcp.CallToolResult, actionOut, error) {
	var settings api.Settings
	var msg string
	switch in.Feature {
	case "xdebug":
		settings.Xdebug = &in.Enabled
		msg = "Xdebug is off."
		if in.Enabled {
			msg = "Xdebug is on: requests with the XDEBUG_SESSION cookie or parameter (or a browser extension's trigger) run with Xdebug."
		}
	case "dumps":
		settings.Dumps = &in.Enabled
		msg = "Dump capture is off: dump() output shows in the page or console again."
		if in.Enabled {
			msg = "Capture is on: dump() and dd() output goes to Flock (read it with get_dumps), and queries, logs, views, jobs " +
				"and requests are recorded (read them with get_events)."
		}
	default:
		return nil, actionOut{}, fmt.Errorf("unknown feature %q: use xdebug or dumps", in.Feature)
	}
	if _, err := s.Daemon.UpdateSettings(ctx, settings); err != nil {
		return nil, actionOut{}, err
	}
	return nil, actionOut{Done: true, Message: msg}, nil
}

// ---------------------------------------------------------------------------------------------
// databases

type createDatabaseIn struct {
	Name    string `json:"name" jsonschema:"the database's name"`
	Service string `json:"service,omitempty" jsonschema:"postgres, mysql or mariadb; defaults to the one that runs"`
}

var databaseServices = []string{"postgres", "mysql", "mariadb"}

func (s *server) createDatabase(ctx context.Context, _ *mcp.CallToolRequest, in createDatabaseIn) (*mcp.CallToolResult, actionOut, error) {
	if in.Name == "" {
		return nil, actionOut{}, errors.New("give the database's name")
	}
	services, err := s.Daemon.Services(ctx)
	if err != nil {
		return nil, actionOut{}, err
	}

	var svc api.Service
	if in.Service != "" {
		i := slices.IndexFunc(services, func(x api.Service) bool { return x.ID == in.Service })
		if i < 0 || !slices.Contains(databaseServices, in.Service) {
			return nil, actionOut{}, fmt.Errorf("unknown database service %q: use postgres, mysql or mariadb", in.Service)
		}
		svc = services[i]
	} else {
		var running []api.Service
		for _, x := range services {
			if slices.Contains(databaseServices, x.ID) && x.Running {
				running = append(running, x)
			}
		}
		switch len(running) {
		case 0:
			return nil, actionOut{}, errors.New("no database service runs: install or start one with the service tool (postgres, mysql or mariadb)")
		case 1:
			svc = running[0]
		default:
			return nil, actionOut{}, errors.New("several database services run: say which with service (postgres, mysql or mariadb)")
		}
	}
	switch {
	case !svc.Installed:
		return nil, actionOut{}, fmt.Errorf("%s isn't installed: install it with the service tool first", svc.Name)
	case !svc.Running:
		return nil, actionOut{}, fmt.Errorf("%s isn't running: start it with the service tool first", svc.Name)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if svc.ID == "postgres" {
		err = postgres.CreateDatabase(ctx, svc.Port, in.Name)
	} else {
		err = catalog.CreateDatabase(ctx, s.Root, svc.ID, svc.Version, svc.Port, in.Name)
	}
	if err != nil {
		return nil, actionOut{}, fmt.Errorf("creating %s in %s: %w", in.Name, svc.Name, err)
	}

	msg := fmt.Sprintf("The database %s is ready in %s.", in.Name, svc.Name)
	if svc.Env != "" {
		msg += " To connect a Laravel app, set in .env:\n" + strings.Replace(svc.Env, "DB_DATABASE=laravel", "DB_DATABASE="+in.Name, 1)
	}
	return nil, actionOut{Done: true, Message: msg}, nil
}
