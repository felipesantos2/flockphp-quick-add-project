// Package daemon runs Flock's long-lived services — DNS, web server, PHP pools,
// mail catcher, databases — and exposes them through the local API.
package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"flock/internal/api"
	"flock/internal/binaries"
	"flock/internal/capture"
	"flock/internal/catalog"
	"flock/internal/certs"
	"flock/internal/config"
	"flock/internal/dns"
	"flock/internal/dumps"
	"flock/internal/frankenphp"
	"flock/internal/hosts"
	"flock/internal/mail"
	"flock/internal/node"
	"flock/internal/paths"
	"flock/internal/php"
	"flock/internal/phpext"
	"flock/internal/ports"
	"flock/internal/postgres"
	"flock/internal/project"
	"flock/internal/share"
	"flock/internal/supervisor"
	"flock/internal/web"
	"flock/internal/worktree"
	"flock/internal/xdebug"
)

// Config controls a daemon instance. Everything else comes from <Root>/config.json.
type Config struct {
	Root    string
	Version string
}

// retryInterval is how often a degraded daemon tries to start what failed.
var retryInterval = 15 * time.Second

// trustInBrowsers adds the CA to the browsers' own trust stores (Linux). Tests replace it: it
// writes to the user's home.
var trustInBrowsers = certs.TrustInBrowsers

// ErrAlreadyRunning is returned when another daemon owns the socket.
var ErrAlreadyRunning = errors.New("flock daemon is already running")

type daemon struct {
	root      string
	version   string
	startedAt time.Time
	procs     *supervisor.Supervisor

	// reloadMu serialises reloads (API requests and the retry loop); mutateMu serialises config edits.
	reloadMu sync.Mutex
	mutateMu sync.Mutex
	dns      *dns.Server // written under reloadMu and mu
	dnsErr   error       // why the DNS server isn't running (Windows, where it is optional)
	mail     *mail.Server
	// collector receives the PHP agent's dumps and events while capture is on.
	collector *capture.Collector
	webUp     bool
	php       *php.Manager
	// binaries is the manifest of prebuilt packages for the configured channel, shared by the managers.
	binaries *binaries.Catalog
	// frankenphp installs FrankenPHP builds; restarts counts RestartSite calls per site (see frankenphp.Site.Restart).
	frankenphp *frankenphp.Manager
	restarts   map[string]int
	// pg manages the PostgreSQL versions, catalog the other services (MySQL, Valkey, Meilisearch, ...).
	pg      *postgres.Manager
	catalog *catalog.Manager
	// shares are the sites shared through tunnels (`flock share`); never kept across restarts.
	shares *share.Manager
	// worktrees sets up the sites' git worktrees (see syncWorktrees); pendingDB are the database
	// modes asked for while their setup ran, by path (see setPendingDB).
	worktrees *worktree.Manager
	pendingMu sync.Mutex
	pendingDB map[string]string
	gone      map[string]bool // worktrees being forgotten, by path (see syncWorktrees)

	// leaves issues certificates for deep subdomains, aliases and unknown names during handshakes.
	leaves *certs.Leaves
	// checking is set while a wildcard check runs (see checkWildcard).
	checking atomic.Bool

	mu       sync.Mutex
	sites    []api.SiteStatus
	proxies  []api.ProxyStatus
	probe    serverProbe // what answers at the proxies' upstreams
	warnings []string
	problems map[string]string // why an installed catalog service isn't running, by id
	// breakpoints finds PhpStorm breakpoints; breakpointSites is what the last reload found, by site path.
	breakpoints     xdebug.Detector
	breakpointSites map[string]bool
	cfg             *config.Config
	wildcard        wildcardState

	defaults defaults // the first run's default PHP and Node.js
}

func (d *daemon) shutdown() {
	// First, while reloads still work: stopping a share reloads the web server.
	if d.shares != nil {
		d.shares.StopAll()
	}
	if d.worktrees != nil {
		d.worktrees.StopAll()
	}
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()

	if d.webUp {
		_ = web.Stop()
	}
	if d.dns != nil {
		_ = d.dns.Shutdown()
	}
	if d.mail != nil {
		_ = d.mail.Shutdown()
	}
	if d.collector != nil {
		_ = d.collector.Close()
	}
	d.procs.StopAll()
}

type tcpAPI struct {
	ln    net.Listener
	token string
}

func (d *daemon) listenTCP() (*tcpAPI, error) {
	d.mu.Lock()
	port := 0
	if d.cfg != nil {
		port = d.cfg.Ports.API
	}
	d.mu.Unlock()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("%s", busy(port, err))
	}

	token, err := api.WriteInfo(d.root, "http://"+ln.Addr().String())
	if err != nil {
		ln.Close()
		return nil, err
	}
	return &tcpAPI{ln: ln, token: token}, nil
}

func (d *daemon) currentWarnings() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.warnings...)
}

func (d *daemon) setWarnings(w []string) {
	d.mu.Lock()
	d.warnings = w
	d.mu.Unlock()
}

func (d *daemon) degraded() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.warnings) > 0
}

// windowsDNSPort is where the DNS server listens on Windows: NRPT rules can only name port 53.
// Tests move it out of the way.
var windowsDNSPort = dns.Port

// ensureDNS (re)starts the DNS server when its address or TLD changed. On Windows it is optional:
// the hosts file still resolves every site's domains when port 53 is taken, so a failure there is
// reported through the wildcard check instead of as a warning.
func (d *daemon) ensureDNS(cfg *config.Config) error {
	port := cfg.Ports.DNS
	if runtime.GOOS == "windows" {
		port = windowsDNSPort
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if d.dns != nil && d.dns.Addr() == addr && d.dns.TLD() == cfg.TLD {
		return nil
	}
	if d.dns != nil {
		_ = d.dns.Shutdown()
		d.setDNS(nil, nil)
	}

	srv := dns.New(addr, cfg.TLD)
	if err := srv.Start(); err != nil {
		err = fmt.Errorf("dns: %s", busy(port, err))
		d.setDNS(nil, err)
		if runtime.GOOS == "windows" {
			return nil
		}
		return err
	}
	d.setDNS(srv, nil)
	return nil
}

func (d *daemon) setDNS(srv *dns.Server, err error) {
	d.mu.Lock()
	d.dns, d.dnsErr = srv, err
	d.mu.Unlock()
}

// ensureMail (re)starts the SMTP catcher when its port changed.
func (d *daemon) ensureMail(cfg *config.Config) error {
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Ports.SMTP)
	if d.mail != nil && d.mail.Addr() == addr {
		return nil
	}
	if d.mail != nil {
		_ = d.mail.Shutdown()
		d.mail = nil
	}

	srv := mail.New(addr, filepath.Join(d.root, "mail"))
	if err := srv.Start(); err != nil {
		return fmt.Errorf("mail: %s", busy(cfg.Ports.SMTP, err))
	}
	d.mail = srv
	return nil
}

// ensureCollector runs the agent's collector while capture is on, on the configured port.
func (d *daemon) ensureCollector(cfg *config.Config) error {
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Ports.Agent)
	if d.collector != nil && (!cfg.Dumps || d.collector.Addr() != addr) {
		_ = d.collector.Close()
		d.collector = nil
	}
	if !cfg.Dumps || d.collector != nil {
		return nil
	}
	c, err := capture.Listen(d.root, addr)
	if err != nil {
		return fmt.Errorf("capture: %s", busy(cfg.Ports.Agent, err))
	}
	d.collector = c
	return nil
}

// busy turns "address already in use" into a message naming the program that holds the port.
// The last two are Windows' wording of EADDRINUSE and EACCES.
func busy(port int, err error) string {
	msg := err.Error()
	if strings.Contains(msg, "address already in use") || strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "Only one usage of each socket address") || strings.Contains(msg, "forbidden by its access permissions") {
		return ports.Describe(port)
	}
	return err.Error()
}

// Run starts all services and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if err := os.MkdirAll(cfg.Root, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", cfg.Root, err)
	}

	socket := paths.Socket(cfg.Root)
	if err := claimSocket(socket); err != nil {
		return err
	}

	d := &daemon{root: cfg.Root, version: cfg.Version, startedAt: time.Now(), procs: supervisor.New(), leaves: certs.NewLeaves(), restarts: map[string]int{}}
	defer d.shutdown()

	d.binaries = binaries.NewCatalog(binaries.DefaultSource(cfg.Root, cfg.Version), d.channel)
	go d.binaries.Run(ctx)
	d.pg = postgres.NewManager(cfg.Root, d.binaries, pgService{d})
	d.php = php.NewManager(cfg.Root, d.binaries)
	d.php.Release = d.ReleasePHP
	d.php.Finished = func(string) {
		_ = d.reload()
		d.ensureDefaults()
	}
	go d.php.Run(ctx)
	d.frankenphp = frankenphp.NewManager(cfg.Root, d.binaries)
	d.frankenphp.Release = d.releaseFrankenPHP
	d.frankenphp.Installed = func(string) { _ = d.reload() }
	go d.frankenphp.Run(ctx)
	d.catalog = catalog.NewManager(cfg.Root, catalog.Kinds(runtime.GOOS), d.binaries, d.Mutate)
	go d.catalog.Run(ctx)
	d.worktrees = worktree.NewManager(cfg.Root)
	d.shares = share.NewManager(cfg.Root, d.binaries)
	d.shares.Changed = func() {
		if err := d.reload(); err != nil {
			d.setWarnings([]string{err.Error()})
		}
	}

	// A broken config or a busy port must not kill the daemon: launchd would restart it in a loop
	// and `flock status` could not explain what is wrong. Problems become warnings instead.
	if err := d.reload(); err != nil {
		d.setWarnings([]string{err.Error()})
	}
	d.markWorktreesSince()
	// What the first reload served, taken before the API answers: a folder parked once a client
	// can see the daemon is a change.
	files, extFiles := d.projectFiles(), d.postgresExtensionFiles()
	// The first run installs the default PHP and Node.js once a manifest arrives (reload already
	// started it from the cached one).
	d.binaries.OnChange(d.ensureDefaults)

	ln, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socket, err)
	}
	defer os.Remove(socket)

	apiServer := &http.Server{
		Handler:           api.NewHandler(d, ""),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- apiServer.Serve(ln) }()

	// The native apps can't speak HTTP over a unix socket with URLSession, so they get a
	// loopback TCP listener guarded by a random token published in api.json (mode 0600).
	if tcp, err := d.listenTCP(); err != nil {
		d.setWarnings(append(d.currentWarnings(), "api: "+err.Error()))
	} else {
		tcpServer := &http.Server{Handler: api.NewHandler(d, tcp.token), ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = tcpServer.Serve(tcp.ln) }()
		defer tcpServer.Close()
		defer os.Remove(api.InfoFile(d.root))
	}

	// Retry while degraded, so e.g. the web server comes up by itself once another program frees port 80.
	retry := time.NewTicker(retryInterval)
	defer retry.Stop()
	// Wildcard DNS can start or stop working without Flock changing anything (a VPN connects,
	// a policy arrives, port 53 frees up), so it is checked again regularly.
	wildcard := time.NewTicker(wildcardInterval)
	defer wildcard.Stop()
	// Follow the projects' version files, so a `git pull` that pins another PHP takes effect,
	// and the parked paths, so a new project folder is served as soon as it exists.
	watch := time.NewTicker(projectWatchInterval)
	defer watch.Stop()
	// Follow PhpStorm breakpoints, so a site switches to Xdebug as soon as one is set.
	breakpoints := time.NewTicker(breakpointInterval)
	defer breakpoints.Stop()

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case err := <-serveErr:
			return fmt.Errorf("api server: %w", err)
		case <-retry.C:
			if d.degraded() {
				if err := d.reload(); err != nil {
					d.setWarnings([]string{err.Error()})
				}
			}
		case <-breakpoints.C:
			if d.breakpointsChanged() {
				if err := d.reload(); err != nil {
					d.setWarnings([]string{err.Error()})
				}
			}
		case <-wildcard.C:
			go d.recheckWildcard()
		case <-watch.C:
			current, ext := d.projectFiles(), d.postgresExtensionFiles()
			if current != files || ext != extFiles {
				files, extFiles = current, ext
				if err := d.reload(); err != nil {
					d.setWarnings([]string{err.Error()})
				}
			}
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return apiServer.Shutdown(shutdownCtx)
}

// postgresExtensionFiles fingerprints the active PostgreSQL major's custom extensions folder, so
// what is added to it is linked into the server without a restart.
func (d *daemon) postgresExtensionFiles() string {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	if cfg == nil || cfg.Services.Postgres == "" {
		return ""
	}
	return postgres.CustomFiles(d.root, postgres.MajorOf(cfg.Services.Postgres))
}

// projectWatchInterval is how often the sites and their project files are checked for changes.
var projectWatchInterval = 3 * time.Second

// projectFiles fingerprints the sites, so folders added to or removed from a parked path count,
// their project files (.flockrc, .nvmrc, .node-version, also in parent directories) by size and
// modification time, and which of the files that decide their document root exist.
func (d *daemon) projectFiles() string {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	if cfg == nil {
		return ""
	}
	var b strings.Builder
	seen := map[string]bool{}
	for _, site := range cfg.AllSites() {
		fmt.Fprintf(&b, "site %s %s\n", site.Name, site.Path)
		for _, file := range project.Files(site.Path) {
			if seen[file] {
				continue
			}
			seen[file] = true
			if info, err := os.Stat(file); err == nil {
				fmt.Fprintf(&b, "%s %d %d\n", file, info.Size(), info.ModTime().UnixNano())
			}
		}
		for _, file := range project.RootFiles(&site) {
			if _, err := os.Stat(file); err == nil {
				fmt.Fprintf(&b, "%s\n", file)
			}
		}
		// The settings files and the Caddy directives they name (see siteHandlers).
		files := project.SettingsFiles(cfg, site)
		if file, _, _ := project.CaddyFile(cfg, site); file != "" {
			files = append(files, file)
		}
		for _, file := range files {
			if info, err := os.Stat(file); err == nil {
				fmt.Fprintf(&b, "%s %d %d\n", file, info.Size(), info.ModTime().UnixNano())
			}
		}
	}
	return b.String()
}

// siteHandlers reads the Caddy directives a site's flock.yml or flock.local.yml names with
// `caddyfile:` (see project.CaddyFile) and checks them in ws, the site as it is served. A problem
// is a warning: the site is served without them, and the other sites aren't affected.
func siteHandlers(cfg *config.Config, site config.Site, ws web.Site) (handlers, problem string) {
	file, from, err := project.CaddyFile(cfg, site)
	if err != nil {
		return "", fmt.Sprintf("caddyfile: %s: %v", site.Name, err)
	}
	if file == "" {
		return "", ""
	}
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Sprintf("caddyfile: %s: %s names %s, which doesn't exist", site.Name, from, file)
	}
	if err != nil {
		return "", fmt.Sprintf("caddyfile: %s: %v", site.Name, err)
	}
	ws.Handlers = string(data)
	if err := web.CheckHandlers(ws, file); err != nil {
		return "", fmt.Sprintf("caddyfile: %s is served without its directives: %v", site.Name, err)
	}
	return ws.Handlers, ""
}

// breakpointInterval is how often the sites' PhpStorm workspaces are checked for breakpoints.
var breakpointInterval = time.Second

// breakpointsChanged reports whether a site gained or lost PhpStorm breakpoints since the last reload.
func (d *daemon) breakpointsChanged() bool {
	d.mu.Lock()
	cfg, sites, found := d.cfg, d.sites, d.breakpointSites
	d.mu.Unlock()
	if cfg == nil || !cfg.Xdebug || !cfg.XdebugBreakpoints {
		return false
	}
	for _, site := range sites {
		if d.breakpoints.Has(site.Path) != found[site.Path] {
			return true
		}
	}
	return false
}

// Reload implements api.Backend.
func (d *daemon) Reload() error { return d.reload() }

// Root implements api.Backend.
func (d *daemon) Root() string { return d.root }

// Mutate implements api.Backend: it edits config.json and applies it.
// Changes are serialised so two UI requests cannot overwrite each other's edits.
func (d *daemon) Mutate(change func(*config.Config) error) error {
	d.mutateMu.Lock()
	defer d.mutateMu.Unlock()

	cfg, err := config.Load(d.root)
	if err != nil {
		return err
	}
	if err := change(cfg); err != nil {
		return err
	}
	if err := cfg.Save(d.root); err != nil {
		return err
	}
	return d.reload()
}

// channel is the channel config.json follows; stable when it names an unknown one.
func (d *daemon) channel() binaries.Channel {
	cfg, err := config.Load(d.root)
	if err != nil {
		return binaries.Stable
	}
	ch, err := binaries.ParseChannel(cfg.Channel)
	if err != nil {
		return binaries.Stable
	}
	return ch
}

// reload re-reads config.json and reconciles every service with it.
// It only returns an error when the config itself is unusable; a service that fails
// to start is reported as a warning and retried later.
func (d *daemon) reload() error {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	// A changed channel (the apps, `flock channel`) takes effect at once.
	if d.binaries != nil {
		d.binaries.ChannelChanged()
	}

	cfg, err := config.Load(d.root)
	if err != nil {
		return err
	}

	ca, err := certs.Ensure(filepath.Join(d.root, "certs"), cfg.TLD)
	if err != nil {
		return fmt.Errorf("certificate authority: %w", err)
	}
	if err := ca.WriteBundle(php.CABundle(d.root), certs.SystemRoots()); err != nil {
		return err
	}

	var warnings []string
	// Linux browsers keep their own trust stores, and one installed since `flock install` lacks the CA.
	if err := trustInBrowsers(ca.CertPath()); err != nil {
		warnings = append(warnings, "browsers don't trust Flock's HTTPS certificates: "+err.Error())
	}
	for _, ensure := range []func(*config.Config) error{d.ensureDNS, d.ensureMail, d.ensureCollector} {
		if err := ensure(cfg); err != nil {
			warnings = append(warnings, err.Error())
		}
	}

	// Global npm packages of the default Node.js version are on the user's PATH via this link.
	if err := node.LinkCurrent(d.root, cfg.DefaultNode); err != nil {
		warnings = append(warnings, "node: "+err.Error())
	}

	var (
		statuses    []api.SiteStatus
		webSites    []web.Site
		waiting     []web.Waiting  // sites whose PHP isn't installed
		rootless    []web.Rootless // sites without a document root
		domains     []string
		keep        = map[string]bool{}
		pools       = map[string][]string{} // version → FastCGI upstreams
		debugPools  = map[string][]string{} // version → upstreams of the pool that loads Xdebug
		breakpoints = map[string]bool{}     // site path → PhpStorm breakpoints found
	)

	// ensurePool starts a pool. The pools that (re)started are waited for together before Caddy
	// gets the new config: it would answer 502 until they listen.
	type startedPool struct {
		upstreams []string
		log       string
	}
	var started []startedPool
	ensurePool := func(pool php.Pool, log string) {
		restarted := false
		for _, spec := range pool.Specs {
			restarted = d.procs.Ensure(spec) || restarted
			keep[spec.Name] = true
		}
		if restarted {
			started = append(started, startedPool{pool.Upstreams, log})
		}
	}

	installed, _ := php.Installed(d.root)

	// With capture on, versions with the agent load it (Sync writes their ini); the others send
	// dumps to the dump server, which the agent unsets the dump server's variables for.
	agent := capture.Settings{On: cfg.Dumps, Addr: fmt.Sprintf("127.0.0.1:%d", cfg.Ports.Agent), Off: cfg.CaptureOff, DumpsInPage: cfg.DumpsInPage}
	if _, err := capture.Sync(d.root, agent); err != nil {
		warnings = append(warnings, "capture: "+err.Error())
	}
	var extraEnv []string
	if cfg.Dumps {
		extraEnv = append(dumps.Env(cfg.Ports.Dumps), agent.Env(d.root)...)
	}
	// pool starts a version's FastCGI pool the first time a site needs it.
	pool := func(version string) ([]string, error) {
		if pools[version] == nil {
			p, err := php.PoolFor(d.root, version, extraEnv...)
			if err != nil {
				return nil, err
			}
			ensurePool(p, php.PoolLog(version))
			pools[version] = p.Upstreams
		}
		return pools[version], nil
	}
	// debugPool starts a version's Xdebug pool; FrankenPHP sites send their Xdebug requests there too.
	debugPool := func(version string) []string {
		if _, done := debugPools[version]; !done {
			debugPools[version] = nil
			if debug, err := php.DebugPoolFor(d.root, version, extraEnv...); err != nil {
				warnings = append(warnings, "xdebug: "+err.Error())
			} else {
				ensurePool(debug, php.DebugPoolLog(version))
				debugPools[version] = debug.Upstreams
			}
		}
		return debugPools[version]
	}

	// shareable are the served sites a share can serve: their document root and PHP version.
	type shareable struct{ root, version string }
	shareables := map[string]shareable{}

	sites := cfg.AllSites()
	extras := cfg.DomainsBySite(sites)
	for _, site := range sites {
		domain := cfg.Domain(site)
		phpVersion, nodeVersion := project.PHPFor(cfg, &site, site.Path), project.NodeFor(cfg, &site, site.Path)
		version := phpVersion.Version
		status := api.SiteStatus{
			Name: site.Name, Domain: domain, Path: site.Path, Secured: site.Secured, Parked: site.Parked,
			Parent: site.Parent, Branch: site.Branch,
			PHP: version, PHPSource: phpVersion.Source, PHPPinned: phpVersion.Pinned, PHPFile: phpVersion.File,
			Node: nodeVersion.Version, NodeSource: nodeVersion.Source, NodePinned: nodeVersion.Pinned, NodeFile: nodeVersion.File,
			NodeShadowed: nodeVersion.Shadowed, Domains: []api.SiteDomain{},
		}
		status.Server, status.Worker, status.OctaneAvailable = config.ServerFastCGI, site.Worker, frankenphp.HasOctane(site.Path)
		if site.FrankenPHP() {
			status.Server = config.ServerFrankenPHP
		}
		scheme := "http"
		if site.Secured {
			scheme = "https"
		}
		var aliases, served []string
		for _, e := range extras[site.Name] {
			sd := api.SiteDomain{Entry: e.Entry, Host: e.Host, Alias: e.Alias, Problem: e.Problem}
			if e.Problem == "" {
				sd.URL = scheme + "://" + e.Host + portSuffix(scheme, cfg)
				served = append(served, e.Host)
				if e.Alias {
					aliases = append(aliases, e.Host)
				}
			}
			status.Domains = append(status.Domains, sd)
		}

		root := project.RootFor(&site)
		status.Root, status.RootSource, status.RootPinned, status.RootFile, status.RootState = root.Rel, root.Source, root.Pinned, root.File, root.State
		if root.State != "" {
			status.RootSuggestions = project.SuggestRoots(site.Path)
			if root.State == project.RootNotFound {
				status.Notice = "No document root found"
			}
		}

		if version == "" || !contains(installed, version) {
			// Its names get a page that says what's missing instead of the site (see web.Waiting).
			w := d.waitingFor(site, phpVersion, domain, aliases)
			status.Problem = waitingProblem(w)
			waiting = append(waiting, w)
			domains = append(append(domains, domain), served...)
			statuses = append(statuses, status)
			continue
		}
		status.Problem = extensionsProblem(d.root, site.Path, version)

		if root.State != "" {
			// Its names get a page that says why and suggests roots instead of the site (see web.Rootless).
			r := web.Rootless{Domain: domain, Aliases: aliases, Name: site.Name, State: root.State, Root: root.Rel,
				Set: root.Source != project.Default, Suggestions: status.RootSuggestions}
			if root.Source == project.Project {
				r.File = filepath.Base(root.File)
			}
			if root.State == project.RootMissing {
				status.Problem = rootProblem(root)
			}
			rootless = append(rootless, r)
			status.URL = scheme + "://" + domain + portSuffix(scheme, cfg)
			domains = append(append(domains, domain), served...)
			statuses = append(statuses, status)
			continue
		}

		ws := web.Site{Domain: domain, Aliases: aliases, Root: root.Dir}
		if cfg.Xdebug {
			ws.DebugFPM = debugPool(version)
		}
		if site.FrankenPHP() {
			fp := d.frankenPHPSite(cfg, site, version, ws.Root, extraEnv)
			status.FrankenPHP = &fp.status
			if fp.spec != nil {
				restarted := d.procs.Ensure(*fp.spec)
				keep[fp.spec.Name] = true
				upstream := fmt.Sprintf("127.0.0.1:%d", site.Port)
				wait := 300 * time.Millisecond
				if restarted {
					wait = 20 * time.Second // Octane boots the app first
				}
				if php.WaitReady([]string{upstream}, wait) {
					fp.status.State, ws.Proxy = api.FrankenPHPRunning, upstream
				} else {
					fp.status.State = api.FrankenPHPFailed
					fp.status.Problem = fmt.Sprintf("FrankenPHP did not start — see `flock logs %s`", fp.spec.Name)
					warnings = append(warnings, fmt.Sprintf("frankenphp: %s did not start — see `flock logs %s`", site.Name, fp.spec.Name))
				}
			}
		}
		if ws.Proxy == "" {
			if ws.FPM, err = pool(version); err != nil {
				return err
			}
		}
		if cfg.Xdebug && cfg.XdebugBreakpoints {
			breakpoints[site.Path] = d.breakpoints.Has(site.Path)
			ws.Debugging = breakpoints[site.Path] && len(ws.DebugFPM) > 0
			status.Debugging = ws.Debugging
		}
		if site.Secured {
			if ws.CertFile, ws.KeyFile, err = ca.SiteCert(domain); err != nil {
				return fmt.Errorf("certificate for %s: %w", domain, err)
			}
		}
		status.URL = scheme + "://" + domain + portSuffix(scheme, cfg)
		shareables[site.Name] = shareable{root: ws.Root, version: version}

		// Checked once the site is complete: its directives adapt in the block it is served with.
		if handlers, problem := siteHandlers(cfg, site, ws); problem != "" {
			warnings = append(warnings, problem)
		} else {
			ws.Handlers = handlers
		}
		webSites = append(webSites, ws)
		domains = append(append(domains, domain), served...)
		statuses = append(statuses, status)
	}

	// Proxied names pass every request to their own server. A site's name wins over a proxy's: they
	// can only clash through a parked directory or a config edited by hand.
	taken := map[string]bool{}
	for _, name := range domains {
		taken[name] = true
	}
	var proxies []api.ProxyStatus
	for _, p := range cfg.Proxies {
		domain := cfg.ProxyDomain(p)
		status := api.ProxyStatus{Name: p.Name, Domain: domain, Upstream: p.Upstream, Secured: p.Secured}
		if taken[domain] {
			status.Problem = domain + " is served by a site of the same name"
			proxies = append(proxies, status)
			continue
		}
		ws := web.Site{Domain: domain, Proxy: p.Upstream}
		scheme := "http"
		if p.Secured {
			scheme = "https"
			if ws.CertFile, ws.KeyFile, err = ca.SiteCert(domain); err != nil {
				return fmt.Errorf("certificate for %s: %w", domain, err)
			}
		}
		status.URL = scheme + "://" + domain + portSuffix(scheme, cfg)
		taken[domain] = true
		webSites = append(webSites, ws)
		domains = append(domains, domain)
		proxies = append(proxies, status)
	}

	// Shares always go through the version's normal FastCGI pool, also for FrankenPHP sites: only
	// FastCGI takes the per-request settings that keep a share safe (see web.Share).
	var shares []web.Share
	if d.shares != nil {
		keep := map[string]bool{}
		for name := range shareables {
			keep[name] = true
		}
		d.shares.Retain(keep)
		for _, s := range d.shares.Serving() {
			info, ok := shareables[s.Site]
			if !ok {
				continue
			}
			upstreams, err := pool(info.version)
			if err != nil {
				return err
			}
			site, _ := cfg.FindSite(s.Site)
			shares = append(shares, web.Share{Port: s.Port, Root: info.root, Public: filepath.Join(site.Path, "public"), FPM: upstreams, Debug: s.Debug})
		}
	}

	if runtime.GOOS == "windows" {
		warnings = append(warnings, d.syncHosts(cfg, domains)...)
	}

	if v := cfg.Services.Postgres; v != "" && cfg.Services.Running("postgres") {
		spec, pgWarnings, err := d.postgresSpec(cfg, v)
		for _, w := range pgWarnings {
			warnings = append(warnings, "postgres: "+w)
		}
		switch {
		case err != nil:
			warnings = append(warnings, "postgres: "+err.Error())
		case !d.procUp(spec.Name) && !ports.Free("127.0.0.1", cfg.Ports.Postgres):
			// Starting would only crash-loop on "could not bind".
			warnings = append(warnings, "postgres: "+ports.Describe(cfg.Ports.Postgres))
		default:
			d.procs.Ensure(spec)
			keep[spec.Name] = true
		}
	}

	if cfg.Dumps && len(installed) > 0 {
		spec, err := dumps.Spec(d.root, php.Binary(d.root, installed[0]), cfg.Ports.Dumps)
		switch {
		case err != nil:
			warnings = append(warnings, "dumps: "+err.Error())
		case !d.procUp(spec.Name) && !ports.Free("127.0.0.1", cfg.Ports.Dumps):
			warnings = append(warnings, "dumps: "+ports.Describe(cfg.Ports.Dumps))
		default:
			d.procs.Ensure(spec)
			keep[spec.Name] = true
		}
	}

	problems := map[string]string{}
	// start runs an optional service on port, unless another Flock service is configured for
	// that port or something else holds it (it would only crash-loop on "address in use").
	start := func(id string, port int, spec supervisor.Spec, err error) {
		problem := ""
		if other, taken := cfg.UsedPorts(id)[port]; taken && other != id && err == nil {
			err = fmt.Errorf("port %d is also configured for %s — change one of the ports", port, serviceName(other))
		}
		switch {
		case err != nil:
			problem = err.Error()
		case !d.procUp(spec.Name) && !ports.Free("127.0.0.1", port):
			problem = ports.Describe(port)
		default:
			d.procs.Ensure(spec)
			keep[spec.Name] = true
			return
		}
		problems[id] = problem
		warnings = append(warnings, id+": "+problem)
	}

	if d.catalog != nil {
		for _, k := range d.catalog.Kinds() {
			if x, ok := cfg.Services.Extra(k.ID); ok && cfg.Services.Running(k.ID) {
				spec, err := catalog.Spec(d.root, k, x)
				start(k.ID, x.Port, spec, err)
			}
		}
	}

	d.procs.Retain(keep)

	// The pools that started, waited for together: a cold start (an antivirus scanning PHP the
	// first time) shouldn't add up per pool.
	ready := make([]bool, len(started))
	var wg sync.WaitGroup
	for i, pool := range started {
		wg.Go(func() { ready[i] = php.WaitReady(pool.upstreams, 5*time.Second) })
	}
	wg.Wait()
	for i, pool := range started {
		if !ready[i] {
			warnings = append(warnings, fmt.Sprintf("php: %s did not start — see `flock logs %s`", pool.log, pool.log))
		}
	}

	bind := "127.0.0.1"
	if runtime.GOOS == "darwin" && (cfg.Ports.HTTP < 1024 || cfg.Ports.HTTPS < 1024) {
		bind = ""
	}

	if err := d.applyWeb(cfg, bind, webSites, waiting, rootless, shares, ca); err != nil {
		warnings = append(warnings, "web: "+err.Error())
	}

	d.mu.Lock()
	d.cfg, d.sites, d.proxies, d.warnings, d.problems, d.breakpointSites = cfg, statuses, proxies, warnings, problems, breakpoints
	d.mu.Unlock()

	go d.checkWildcard()
	d.syncWorktrees(cfg)
	// Until there are defaults: one may be installed now (the first run's, or one the user installed).
	if cfg.DefaultPHP == "" || cfg.DefaultNode == "" {
		go d.ensureDefaults()
	}
	return nil
}

// applyWeb (re)configures Caddy. Before the first successful start it checks the ports itself,
// because Caddy's own error does not say who holds them.
func (d *daemon) applyWeb(cfg *config.Config, bind string, sites []web.Site, waiting []web.Waiting, rootless []web.Rootless, shares []web.Share, ca *certs.CA) error {
	owned := map[int]bool{}
	if d.webUp && d.cfg != nil {
		owned[d.cfg.Ports.HTTP], owned[d.cfg.Ports.HTTPS] = true, true
	}
	for _, port := range []int{cfg.Ports.HTTP, cfg.Ports.HTTPS} {
		if !owned[port] && !ports.Free(bind, port) {
			return errors.New(ports.Describe(port))
		}
		// On Linux the service gives the daemon the capability for ports below 1024; one started
		// some other way (`flock daemon` in a terminal) lacks it.
		if !owned[port] && runtime.GOOS == "linux" && ports.Denied(bind, port) {
			return fmt.Errorf("the daemon may not use port %d: it needs the CAP_NET_BIND_SERVICE capability, which it has when systemd starts it — run `flock restart` instead of `flock daemon`", port)
		}
	}

	err := web.Apply(web.Config{
		StorageDir: filepath.Join(d.root, "caddy"),
		LogFile:    filepath.Join(d.root, "log", "web.log"),
		HTTPPort:   cfg.Ports.HTTP,
		HTTPSPort:  cfg.Ports.HTTPS,
		Bind:       bind,
		Sites:      sites,
		Waiting:    waiting,
		Rootless:   rootless,
		Shares:     shares,
		TLD:        cfg.TLD,
		Issue:      func(name string) (*tls.Certificate, error) { return d.leaves.Get(ca, name) },
	})
	if err != nil {
		return err
	}
	d.webUp = true
	return nil
}

func (d *daemon) procUp(name string) bool {
	for _, p := range d.procs.Status() {
		if p.Name == name {
			return p.Up
		}
	}
	return false
}

func (d *daemon) postgresSpec(cfg *config.Config, id string) (supervisor.Spec, []string, error) {
	if !postgres.Installed(d.root, id) {
		return supervisor.Spec{}, nil, fmt.Errorf("version %s is not installed — reinstall it with `flock services install postgres`", id)
	}
	major := postgres.MajorOf(id)
	bin, data := postgres.BinDir(d.root, id), postgres.DataDir(d.root, major)
	if err := postgres.InitDB(bin, data); err != nil {
		return supervisor.Spec{}, nil, err
	}
	// Every reload: the custom folder may have changed (see the watch in Run).
	_, warnings, err := postgres.Link(d.root, id)
	if err != nil {
		warnings = append(warnings, "extensions: "+err.Error())
	}
	run := "" // Windows has no unix sockets: clients connect over TCP
	if runtime.GOOS != "windows" {
		var err error
		if run, err = paths.EnsureRun(d.root); err != nil {
			return supervisor.Spec{}, nil, err
		}
	}
	preload := postgres.PreloadLibraries(d.root, major)
	spec := postgres.Spec(bin, data, run, filepath.Join(d.root, "log", "postgres.log"), cfg.Ports.Postgres, preload...)
	// A changed environment restarts the server (the supervisor compares Env), so new libraries load.
	spec.Env = postgres.ServerEnv(d.root, major)
	return spec, warnings, nil
}

// PostgresVersions implements api.Backend.
func (d *daemon) PostgresVersions() *postgres.Manager { return d.pg }

// Components implements api.Backend.

// Catalog implements api.Backend.
func (d *daemon) Catalog() *catalog.Manager { return d.catalog }

// serviceName names a service for messages: "MySQL" for "mysql".
func serviceName(id string) string {
	switch id {
	case "web":
		return "the web server"
	case "dns", "api":
		return "Flock's " + strings.ToUpper(id)
	case "mail":
		return "the mail catcher"
	case "dumps":
		return "the dump server"
	case "agent":
		return "the capture collector"
	case "postgres":
		return "PostgreSQL"
	}
	for _, k := range catalog.Kinds(runtime.GOOS) {
		if k.ID == id {
			return k.Name
		}
	}
	return id
}

// pgService lets the postgres.Manager change the service through config.json and reloads.
type pgService struct{ d *daemon }

func (s pgService) Active() (string, bool) {
	cfg, err := config.Load(s.d.root)
	if err != nil {
		return "", false
	}
	return cfg.Services.Postgres, cfg.Services.Running("postgres")
}

func (s pgService) Apply(version string, enable bool) error {
	return s.d.Mutate(func(cfg *config.Config) error {
		cfg.Services.Postgres = version
		if enable {
			cfg.Services.SetRunning("postgres", true)
		}
		return nil
	})
}

func (s pgService) Running() bool { return s.d.procUp("postgres") }

func (s pgService) Port() int {
	if cfg, err := config.Load(s.d.root); err == nil {
		return cfg.Ports.Postgres
	}
	return config.Default().Ports.Postgres
}

func (s pgService) Live() bool { return true }

func (s pgService) Reload() error { return s.d.reload() }

// Status implements api.Backend.
func (d *daemon) Status() api.Status {
	// Probed outside the lock: a dev server that doesn't answer takes a moment to say so.
	d.mu.Lock()
	served := d.proxies
	d.mu.Unlock()
	proxies := d.probe.live(d.root, served)

	d.mu.Lock()
	defer d.mu.Unlock()

	st := api.Status{
		Version:   d.version,
		PID:       os.Getpid(),
		StartedAt: d.startedAt,
		Sites:     d.withWorktrees(d.withShares(withBoost(d.liveSites()))),
		Proxies:   proxies,
		Processes: d.procs.Status(),
		Warnings:  d.warnings,
	}
	if d.mail != nil {
		st.Mail = d.mail.Addr()
	}
	if messages, err := mail.List(filepath.Join(d.root, "mail")); err == nil {
		st.MailCount = len(messages)
	}
	st.DumpCount = dumps.Count(d.root)
	st.EventCount = capture.Count(d.root)
	st.CaptureUnsupported = []string{}
	if installed, err := php.Installed(d.root); err == nil {
		for _, minor := range installed {
			if !capture.Supported(d.root, minor) {
				st.CaptureUnsupported = append(st.CaptureUnsupported, minor)
			}
		}
	}
	st.DNS = api.DNSStatus{Wildcard: d.wildcard.ok, WildcardProblem: d.wildcard.problem, MaxDepth: config.MaxDepth}
	if d.cfg != nil {
		st.DNS.TLD = d.cfg.TLD
	}
	if d.dns != nil {
		st.DNS.Addr = d.dns.Addr()
	}
	if d.cfg != nil {
		st.HTTPPort, st.HTTPSPort = d.cfg.Ports.HTTP, d.cfg.Ports.HTTPS
		if d.cfg.Services.Postgres != "" {
			st.Postgres = fmt.Sprintf("%s on 127.0.0.1:%d", postgres.VersionOf(d.cfg.Services.Postgres), d.cfg.Ports.Postgres)
		}
		if d.catalog != nil {
			for _, k := range d.catalog.Kinds() {
				if x, ok := d.cfg.Services.Extra(k.ID); ok {
					st.Extras = append(st.Extras, api.ExtraStatus{ID: k.ID, Name: k.Name, Version: x.Version, Port: x.Port,
						Enabled: d.cfg.Services.Running(k.ID), Up: d.procUp(k.ID)})
				}
			}
		}
		st.Xdebug, st.XdebugBreakpoints = d.cfg.Xdebug, d.cfg.XdebugBreakpoints
		st.Channel = api.ChannelOf(d.cfg)
		st.Dumps, st.DumpsInPage = d.cfg.Dumps, d.cfg.DumpsInPage
		st.CaptureOff = append([]string{}, d.cfg.CaptureOff...)
		st.Defaults = d.defaultsStatus(d.cfg)

		// Without the helper, sites resolve only through the DNS rule: say so when that fails too.
		if runtime.GOOS == "windows" && d.wildcard.problem != "" && hosts.Blocked(d.root) && !hosts.HelperStatus().Installed {
			st.Warnings = append(st.Warnings[:len(st.Warnings):len(st.Warnings)],
				fmt.Sprintf("*.%s domains don't resolve: %s, and %s", d.cfg.TLD, d.wildcard.problem, helperBlockedProblem))
		}
	}

	return st
}

// PHP implements api.Backend.
func (d *daemon) PHP() *php.Manager { return d.php }

// ReleasePHP implements api.Backend: on Windows it stops the processes that run a PHP version
// ("" for all), so its files can be replaced or deleted. The next reload starts what is needed.
func (d *daemon) ReleasePHP(minor string) {
	if runtime.GOOS != "windows" {
		return
	}
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()

	keep := map[string]bool{}
	for _, name := range d.procs.Running() {
		usesPHP := name == "dump-server" || strings.HasPrefix(name, "php"+minor)
		keep[name] = !usesPHP
	}
	d.procs.Retain(keep)
}

// Services implements api.Backend.
func (d *daemon) Services() []api.Service {
	d.mu.Lock()
	cfg := d.cfg
	dnsUp, mailUp, webUp, wildcard, problems := d.dns != nil, d.mail != nil, d.webUp, d.wildcard, d.problems
	d.mu.Unlock()
	if cfg == nil {
		return []api.Service{}
	}
	p := cfg.Ports

	services := []api.Service{
		{ID: "web", Name: "Web server (Caddy)", Category: "Core", Port: p.HTTPS, Installed: true, Enabled: true, Running: webUp, Log: "web"},
		{ID: "mail", Name: "Mail (SMTP)", Category: "Mail", Port: p.SMTP, Installed: true, Enabled: true, Running: mailUp,
			Env: fmt.Sprintf("MAIL_MAILER=smtp\nMAIL_HOST=127.0.0.1\nMAIL_PORT=%d\nMAIL_USERNAME=${APP_NAME}\nMAIL_PASSWORD=null\nMAIL_SCHEME=null", p.SMTP),
			URL: fmt.Sprintf("smtp://127.0.0.1:%d", p.SMTP)},
		{ID: "dumps", Name: "Dump server", Category: "Debugging", Port: p.Dumps, Installed: true, Enabled: cfg.Dumps, Running: d.procUp("dump-server"), Stoppable: true,
			Log: "dump-server"},
	}
	if runtime.GOOS != "windows" {
		services = append(services, api.Service{ID: "dns", Name: "DNS (*." + cfg.TLD + ")", Category: "Core", Port: p.DNS, Installed: true, Enabled: true, Running: dnsUp,
			Problem: wildcard.problem})
	} else {
		// Wildcard DNS (any subdomain) needs the DNS server and Windows' DNS rule; the hosts file
		// resolves the sites' domains and extra domains either way.
		services = append(services, api.Service{ID: "dns", Name: "DNS (*." + cfg.TLD + ")", Category: "Core", Port: windowsDNSPort,
			Installed: true, Enabled: true, Running: dnsUp && wildcard.ok, Problem: wildcard.problem})
		helper := hosts.HelperStatus()
		hostsService := api.Service{ID: "hosts", Name: "Hosts file", Category: "Core", Installed: helper.Installed, Enabled: true, Running: helper.Running}
		if !helper.Installed && hosts.Blocked(d.root) {
			hostsService.Problem = helperBlockedProblem
		}
		services = append(services, hostsService)
	}
	// Flock's own services run everywhere; only catalog services can be unavailable.
	for i := range services {
		services[i].Available = true
	}

	services = append(services, api.Service{
		ID: "postgres", Name: "PostgreSQL", Category: "Database", Version: postgres.VersionOf(cfg.Services.Postgres), Port: p.Postgres, Available: true,
		Installed: cfg.Services.Postgres != "", Enabled: cfg.Services.Running("postgres"), Running: d.procUp("postgres"), Stoppable: true,
		Env: fmt.Sprintf("DB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\nDB_PORT=%d\nDB_DATABASE=laravel\nDB_USERNAME=postgres\nDB_PASSWORD=", p.Postgres),
		Log: "postgres", URL: fmt.Sprintf("postgres://postgres@127.0.0.1:%d", p.Postgres),
	})

	if d.catalog != nil {
		for _, k := range d.catalog.Kinds() {
			s := api.Service{ID: k.ID, Name: k.Name, Category: k.Category, Description: k.Description, Log: k.ID}
			s.Available, s.Unavailable = d.catalog.Available(k)
			if x, ok := cfg.Services.Extra(k.ID); ok {
				s.Version, s.Port, s.Installed, s.Stoppable = x.Version, x.Port, true, true
				s.Enabled, s.Running, s.Problem = cfg.Services.Running(k.ID), d.procUp(k.ID), problems[k.ID]
				s.Env = k.Env(x.Port)
				if k.URL != nil {
					s.URL = k.URL(x.Port)
				}
			} else {
				s.Port = k.DefaultPort
			}
			services = append(services, s)
		}
	}

	return services
}

// helperBlockedProblem explains a hosts helper that `flock install` couldn't run.
const helperBlockedProblem = "Windows blocked the Flock hosts helper (Smart App Control or an App Control policy only runs signed programs as services)"

// syncHosts hands the site domains to the hosts helper (Windows) and explains when it can't apply them.
func (d *daemon) syncHosts(cfg *config.Config, domains []string) []string {
	if err := hosts.WriteSource(d.root, domains); err != nil {
		return []string{"hosts: " + err.Error()}
	}
	helper := hosts.HelperStatus()
	switch {
	case !helper.Installed && hosts.Blocked(d.root):
		return nil // sites resolve through the DNS rule; Status reports it if that fails too
	case !helper.Installed:
		return []string{fmt.Sprintf("*.%s domains don't resolve: the Flock hosts helper is not installed — run `flock install`", cfg.TLD)}
	case !helper.Running:
		return []string{fmt.Sprintf("*.%s domains don't resolve: the Flock hosts helper service is stopped — run `flock install`", cfg.TLD)}
	case !helper.Configured(hosts.SourceFile(d.root), cfg.TLD):
		return []string{fmt.Sprintf("the Flock hosts helper is set up for another domain or user — run `flock install` to switch it to *.%s", cfg.TLD)}
	}
	return nil
}

func portSuffix(scheme string, cfg *config.Config) string {
	if scheme == "https" && cfg.Ports.HTTPS != 443 {
		return fmt.Sprintf(":%d", cfg.Ports.HTTPS)
	}
	if scheme == "http" && cfg.Ports.HTTP != 80 {
		return fmt.Sprintf(":%d", cfg.Ports.HTTP)
	}
	return ""
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// claimSocket refuses to start when a live daemon answers on socket, and removes a stale one left by a crash.
func claimSocket(socket string) error {
	if _, err := os.Stat(socket); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if conn, err := net.DialTimeout("unix", socket, time.Second); err == nil {
		_ = conn.Close()
		return ErrAlreadyRunning
	}

	if err := os.Remove(socket); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", socket, err)
	}

	return nil
}

// extensionsProblem names the extensions a site requires (composer.json, composer.lock) that its
// PHP doesn't have; "" when it has them all.
func extensionsProblem(root, dir, minor string) string {
	exts, err := php.Extensions(root, minor)
	if err != nil {
		return ""
	}
	missing := phpext.Check(dir, minor, exts).Missing()
	switch len(missing) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("Requires the PHP extension %s, which Flock's PHP %s doesn't include", missing[0], minor)
	}
	return fmt.Sprintf("Requires the PHP extensions %s, which Flock's PHP %s doesn't include", strings.Join(missing, ", "), minor)
}
