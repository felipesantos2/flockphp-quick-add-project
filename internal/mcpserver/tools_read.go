package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"flock/internal/api"
	"flock/internal/capture"
	"flock/internal/dumps"
	"flock/internal/laravellog"
	"flock/internal/mail"
	"flock/internal/php"
	"flock/internal/phpext"
)

// readOnly marks tools that only look; clients may run them without asking.
var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}

// siteArg is the `site` argument every site tool takes.
type siteArg struct {
	Site string `json:"site,omitempty" jsonschema:"the site: its name, domain or project folder; defaults to the project Flock was started in"`
}

func (s *server) addReadTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "status",
		Description: "Whether Flock runs, its default PHP and Node.js versions, ports, whether dump capture and Xdebug are on, and any problems.",
		Annotations: readOnly,
	}, s.status)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "site_info",
		Description: "A site's URL(s), folder, HTTPS, the PHP and Node.js versions it uses and where they come from " +
			"(isolated in Flock, pinned in .flockrc/.nvmrc, or Flock's default), and whether FastCGI or FrankenPHP serves it.",
		Annotations: readOnly,
	}, s.siteInfo)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "php_extensions",
		Description: "The PHP extensions a site requires (ext-* in composer.json and its locked packages), the ones its " +
			"Laravel .env and config show it uses without requiring them, and whether Flock's PHP has each.",
		Annotations: readOnly,
	}, s.phpExtensions)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_sites",
		Description: "Every site Flock serves, with its URL, folder and PHP version, and the proxies (names served by a server of their own).",
		Annotations: readOnly,
	}, s.listSites)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_services",
		Description: "Flock's services (MySQL, MariaDB, PostgreSQL, Redis/Valkey, Meilisearch, Typesense, MongoDB, RustFS, mail, dumps): " +
			"whether each is installed and running, its port, the .env settings to connect a Laravel app, and installs in progress.",
		Annotations: readOnly,
	}, s.listServices)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_php_versions",
		Description: "PHP versions: which are installed, the default, the newest releases, and installs in progress.",
		Annotations: readOnly,
	}, s.listPHPVersions)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_node_versions",
		Description: "Node.js versions: which are installed, the default, and the release lines that can be installed.",
		Annotations: readOnly,
	}, s.listNodeVersions)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_errors",
		Description: "Recent entries of a site's Laravel log (storage/logs, newest first) and PHP errors from Flock's PHP log. " +
			"By default only errors and worse; set all_levels for every entry.",
		Annotations: readOnly,
	}, s.getErrors)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_dumps",
		Description: "Output of dump() and dd() captured by Flock (newest first), with the file and line and the request or " +
			"command that produced it. Needs dump capture on (see set_debugging).",
		Annotations: readOnly,
	}, s.getDumps)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_events",
		Description: "What the apps did, captured by Flock's agent while capture is on: database queries (SQL, bindings, " +
			"duration, the app code that ran them), log messages, rendered views, queued jobs (state, duration, exception) " +
			"and requests (status, duration), grouped by request or CLI run, newest first. Use it to see what a page or " +
			"command actually does, e.g. N+1 queries or a failing job. Needs capture on (see set_debugging, feature dumps).",
		Annotations: readOnly,
		InputSchema: eventsSchema(),
	}, s.getEvents)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_mail",
		Description: "Mail the apps sent, caught by Flock instead of being delivered (newest first).",
		Annotations: readOnly,
	}, s.listMail)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "read_mail",
		Description: "One caught mail: headers, text, the links in it (e.g. to verify a sign-up or reset a password) " +
			"and its attachments.",
		Annotations: readOnly,
	}, s.readMail)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "service_logs",
		Description: "The last lines of one of Flock's logs: a service (mysql, postgres, redis, ...), the web server (web), " +
			"a PHP version (e.g. php8.4-fpm), or the daemon. Without a name, lists the logs.",
		Annotations: readOnly,
	}, s.serviceLogs)
}

// ---------------------------------------------------------------------------------------------
// status

type statusOut struct {
	Running     bool     `json:"running"`
	Message     string   `json:"message,omitempty"`
	Version     string   `json:"version,omitempty"`
	TLD         string   `json:"tld,omitempty"`
	DefaultPHP  string   `json:"default_php,omitempty"`
	DefaultNode string   `json:"default_node,omitempty"`
	HTTPPort    int      `json:"http_port,omitempty"`
	HTTPSPort   int      `json:"https_port,omitempty"`
	DumpCapture bool     `json:"dump_capture"`
	Xdebug      bool     `json:"xdebug"`
	Mail        int      `json:"caught_mail"`
	Dumps       int      `json:"captured_dumps"`
	Events      int      `json:"captured_events"`
	Sites       int      `json:"sites"`
	Warnings    []string `json:"warnings,omitempty"`
}

func (s *server) status(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, statusOut, error) {
	st, err := s.Daemon.Status(ctx)
	if errors.Is(err, api.ErrDaemonNotRunning) {
		return nil, statusOut{Message: "Flock isn't running. It starts with the Flock app, or with `flock daemon`."}, nil
	}
	if err != nil {
		return nil, statusOut{}, err
	}
	out := statusOut{
		Running: true, Version: st.Version, TLD: st.DNS.TLD, HTTPPort: st.HTTPPort, HTTPSPort: st.HTTPSPort,
		DumpCapture: st.Dumps, Xdebug: st.Xdebug, Mail: st.MailCount, Dumps: st.DumpCount, Events: st.EventCount, Sites: len(st.Sites), Warnings: st.Warnings,
	}
	if cfg, err := s.Daemon.Config(ctx); err == nil {
		out.DefaultPHP, out.DefaultNode = cfg.DefaultPHP, cfg.DefaultNode
	}
	return nil, out, nil
}

// ---------------------------------------------------------------------------------------------
// sites

type versionInfo struct {
	Version string `json:"version"`
	// Source is "isolated" (set for the site in Flock, this machine only), "project" (pinned in
	// File, committed with the project) or "default" (Flock's default version).
	Source string `json:"source"`
	File   string `json:"file,omitempty"`
	Pinned string `json:"pinned,omitempty"` // what File pins, when an isolation wins over it
}

type rootInfo struct {
	Path string `json:"path"` // relative to the project, "." for the project folder itself
	// Source is "isolated" (set for the site in Flock, this machine only), "project" (root= in
	// File, committed with the project) or "default" (detected: public/, www/, web/ or webroot/).
	Source string `json:"source"`
	File   string `json:"file,omitempty"`
	// Suggestions are folders with an index.php, when Flock found no usable root and serves a page
	// saying so instead of the site.
	Suggestions []string `json:"suggestions,omitempty"`
}

type siteInfo struct {
	Name      string      `json:"name"`
	URL       string      `json:"url"`
	OtherURLs []string    `json:"other_urls,omitempty"`
	Path      string      `json:"path"`
	HTTPS     bool        `json:"https"`
	PHP       versionInfo `json:"php"`
	Node      versionInfo `json:"node"`
	Root      rootInfo    `json:"document_root"`
	// Server is "fastcgi" (PHP-FPM; php-cgi on Windows) or "frankenphp"; Worker "octane" in worker mode.
	Server     string `json:"server"`
	Worker     string `json:"worker,omitempty"`
	FrankenPHP string `json:"frankenphp_state,omitempty"`
	Octane     bool   `json:"octane_installed,omitempty"`
	Parked     bool   `json:"parked,omitempty"`
	// WorktreeOf is set on a git worktree: the site whose settings it uses; Branch is its branch.
	WorktreeOf string `json:"worktree_of,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Problem    string `json:"problem,omitempty"`
	// Reverb is the state of the site's Laravel Reverb server when it's on; ReverbEnv the .env lines
	// that send the app's broadcasts and its Echo client through Flock (wss://<site>.test/app).
	Reverb    string   `json:"reverb_state,omitempty"`
	ReverbEnv []string `json:"reverb_env,omitempty"`
	Notice    string   `json:"notice,omitempty"`
}

func siteInfoOf(st api.SiteStatus) siteInfo {
	info := siteInfo{
		Name: st.Name, URL: st.URL, Path: st.Path, HTTPS: st.Secured, Server: st.Server, Worker: st.Worker,
		Octane: st.OctaneAvailable, Parked: st.Parked, WorktreeOf: st.Parent, Branch: st.Branch, Problem: st.Problem, Notice: st.Notice,
		PHP:  versionInfo{Version: st.PHP, Source: st.PHPSource, File: st.PHPFile},
		Node: versionInfo{Version: st.Node, Source: st.NodeSource, File: st.NodeFile},
		Root: rootInfo{Path: st.Root, Source: st.RootSource, File: st.RootFile, Suggestions: st.RootSuggestions},
	}
	if info.URL == "" {
		scheme := "http"
		if st.Secured {
			scheme = "https"
		}
		info.URL = scheme + "://" + st.Domain
	}
	if st.PHPSource == "isolated" && st.PHPPinned != "" {
		info.PHP.Pinned = st.PHPPinned
	}
	if st.NodeSource == "isolated" && st.NodePinned != "" {
		info.Node.Pinned = st.NodePinned
	}
	for _, d := range st.Domains {
		if d.URL != "" {
			info.OtherURLs = append(info.OtherURLs, d.URL)
		}
	}
	if st.FrankenPHP != nil {
		info.FrankenPHP = st.FrankenPHP.State
		if info.Problem == "" {
			info.Problem = st.FrankenPHP.Problem
		}
	}
	if st.Reverb != nil {
		info.Reverb, info.ReverbEnv = st.Reverb.State, st.Reverb.Env
		if info.Problem == "" {
			info.Problem = st.Reverb.Problem
		}
	}
	return info
}

func (s *server) siteInfo(ctx context.Context, _ *mcp.CallToolRequest, in siteArg) (*mcp.CallToolResult, siteInfo, error) {
	site, err := s.site(ctx, in.Site)
	if err != nil {
		return nil, siteInfo{}, err
	}
	return nil, siteInfoOf(site), nil
}

type siteSummary struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Path    string `json:"path"`
	PHP     string `json:"php"`
	Server  string `json:"server"`
	Problem string `json:"problem,omitempty"`
}

type sitesOut struct {
	Sites []siteSummary `json:"sites"`
	// Proxies are names served by a server of their own (see proxy_site).
	Proxies []api.ProxyStatus `json:"proxies,omitempty"`
	// Current is the site Flock was started in, if any.
	Current string `json:"current,omitempty"`
}

func (s *server) listSites(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, sitesOut, error) {
	st, err := s.Daemon.Status(ctx)
	if err != nil {
		return nil, sitesOut{}, err
	}
	out := sitesOut{Sites: []siteSummary{}}
	for _, site := range st.Sites {
		info := siteInfoOf(site)
		out.Sites = append(out.Sites, siteSummary{Name: info.Name, URL: info.URL, Path: info.Path, PHP: info.PHP.Version, Server: info.Server, Problem: info.Problem})
	}
	out.Proxies = st.Proxies
	if current, err := s.site(ctx, ""); err == nil {
		out.Current = current.Name
	}
	return nil, out, nil
}

// ---------------------------------------------------------------------------------------------
// services

type serviceOut struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Available   bool     `json:"available"`
	Unavailable string   `json:"unavailable,omitempty"`
	Installed   bool     `json:"installed"`
	Version     string   `json:"version,omitempty"`
	Running     bool     `json:"running"`
	Enabled     bool     `json:"enabled"`
	Port        int      `json:"port,omitempty"`
	Env         string   `json:"env,omitempty"`
	URL         string   `json:"url,omitempty"`
	Problem     string   `json:"problem,omitempty"`
	Versions    []string `json:"versions,omitempty"` // lines that can be installed
	Installing  string   `json:"installing,omitempty"`
	InstallErr  string   `json:"install_error,omitempty"`
}

type servicesOut struct {
	Services []serviceOut `json:"services"`
}

func (s *server) listServices(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, servicesOut, error) {
	services, err := s.Daemon.Services(ctx)
	if err != nil {
		return nil, servicesOut{}, err
	}
	// The catalog and PostgreSQL may be unsupported on a platform; skip what fails.
	cat, _ := s.Daemon.Catalog(ctx)
	pg, pgErr := s.Daemon.PostgresVersions(ctx)

	out := servicesOut{Services: []serviceOut{}}
	for _, svc := range services {
		if svc.ID == "web" || svc.ID == "dns" || svc.ID == "hosts" {
			continue // always on; nothing an agent can do with them
		}
		o := serviceOut{
			ID: svc.ID, Name: svc.Name, Category: svc.Category, Available: svc.Available || svc.Installed, Unavailable: svc.Unavailable,
			Installed: svc.Installed, Version: svc.Version, Running: svc.Running, Enabled: svc.Enabled, Port: svc.Port,
			Env: svc.Env, URL: svc.URL, Problem: svc.Problem,
		}
		if svc.ID == "mail" || svc.ID == "dumps" {
			o.Available = true
		}
		if e, ok := cat.Find(svc.ID); ok {
			for _, l := range e.Lines {
				o.Versions = append(o.Versions, l.ID)
			}
			if e.Job != nil {
				o.Installing, o.InstallErr = jobState(e.Job.State, e.Job.Phase, e.Job.Progress, e.Job.Error)
			}
		}
		if svc.ID == "postgres" && pgErr == nil {
			for _, m := range pg.Majors {
				o.Versions = append(o.Versions, m.Major)
				if m.Busy != "" {
					o.Installing = fmt.Sprintf("PostgreSQL %s: %s", m.Major, m.Busy)
				}
			}
		}
		out.Services = append(out.Services, o)
	}
	return nil, out, nil
}

// jobState describes an install job: what it is doing, or why it failed.
func jobState(state, phase string, progress float64, errText string) (installing, failed string) {
	if state == "failed" {
		return "", errText
	}
	if phase == "" {
		phase = "installing"
	}
	if progress > 0 {
		return fmt.Sprintf("%s (%d%%)", phase, int(progress*100)), ""
	}
	return phase, ""
}

// ---------------------------------------------------------------------------------------------
// versions

type phpVersionOut struct {
	Version    string `json:"version"`             // minor, e.g. "8.4"
	Installed  string `json:"installed,omitempty"` // full version
	Latest     string `json:"latest,omitempty"`
	Default    bool   `json:"default,omitempty"`
	Status     string `json:"status"` // installed, update or available
	Prerelease bool   `json:"prerelease,omitempty"`
	Withdrawn  string `json:"withdrawn,omitempty"` // why the installed build was withdrawn
	Installing string `json:"installing,omitempty"`
	InstallErr string `json:"install_error,omitempty"`
}

type phpVersionsOut struct {
	Default  string          `json:"default"`
	Versions []phpVersionOut `json:"versions"`
}

func (s *server) listPHPVersions(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, phpVersionsOut, error) {
	cfg, err := s.Daemon.Config(ctx)
	if err != nil {
		return nil, phpVersionsOut{}, err
	}
	out := phpVersionsOut{Default: cfg.DefaultPHP, Versions: []phpVersionOut{}}

	versions, err := s.Daemon.PHPVersions(ctx)
	if err != nil {
		// No release information on this platform: list what is installed.
		for _, v := range cfg.PHP {
			out.Versions = append(out.Versions, phpVersionOut{Version: v.Version, Installed: v.FullVersion, Default: v.Version == cfg.DefaultPHP, Status: "installed"})
		}
		return nil, out, nil
	}
	for _, v := range versions.Versions {
		o := phpVersionOut{Version: v.Minor, Installed: v.Installed, Latest: v.Latest, Default: v.Default, Status: v.Status, Prerelease: v.Prerelease, Withdrawn: v.Yanked}
		if v.Job != nil {
			if v.Job.State == "failed" {
				o.InstallErr = v.Job.Error
			} else {
				o.Installing = v.Job.Text(v.Minor)
			}
		}
		out.Versions = append(out.Versions, o)
	}
	return nil, out, nil
}

type nodeVersionOut struct {
	Major     string `json:"major"`
	Installed string `json:"installed,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Default   bool   `json:"default,omitempty"`
	Update    bool   `json:"update_available,omitempty"`
}

type nodeVersionsOut struct {
	Default    string           `json:"default"`
	Versions   []nodeVersionOut `json:"versions"`
	Installing []string         `json:"installing,omitempty"`
}

func (s *server) listNodeVersions(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, nodeVersionsOut, error) {
	cfg, err := s.Daemon.Config(ctx)
	if err != nil {
		return nil, nodeVersionsOut{}, err
	}
	versions, err := s.Daemon.NodeVersions(ctx)
	if err != nil {
		return nil, nodeVersionsOut{}, err
	}
	out := nodeVersionsOut{Default: cfg.DefaultNode, Versions: []nodeVersionOut{}}
	for _, job := range s.bg.running() {
		if major, ok := strings.CutPrefix(job, "Node.js "); ok {
			out.Installing = append(out.Installing, major)
		}
	}
	for _, v := range versions.Versions {
		out.Versions = append(out.Versions, nodeVersionOut{Major: v.Major, Installed: v.Installed, Latest: v.Latest, Default: v.Default, Update: v.Update})
	}
	return nil, out, nil
}

// ---------------------------------------------------------------------------------------------
// logs

type errorsIn struct {
	siteArg
	Limit     int  `json:"limit,omitempty" jsonschema:"how many log entries to return (default 20)"`
	AllLevels bool `json:"all_levels,omitempty" jsonschema:"include every level (info, debug, ...), not just errors and worse"`
}

type logEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Details string    `json:"details,omitempty"`
}

type errorsOut struct {
	Site      string     `json:"site"`
	LogFile   string     `json:"log_file,omitempty"`
	Entries   []logEntry `json:"entries"`
	PHPErrors []string   `json:"php_errors,omitempty"`
	Note      string     `json:"note,omitempty"`
}

var errorLevels = []string{"ERROR", "CRITICAL", "ALERT", "EMERGENCY"}

func (s *server) getErrors(ctx context.Context, _ *mcp.CallToolRequest, in errorsIn) (*mcp.CallToolResult, errorsOut, error) {
	site, err := s.site(ctx, in.Site)
	if err != nil {
		return nil, errorsOut{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}

	out := errorsOut{Site: site.Name, Entries: []logEntry{}}
	if files := laravellog.Files(site.Path); len(files) > 0 {
		out.LogFile = files[0]
	}
	// Read more than asked for, since lower levels are filtered out.
	entries, err := laravellog.Read(site.Path, 500)
	if err != nil {
		return nil, errorsOut{}, err
	}
	for _, e := range entries {
		if !in.AllLevels && !slices.Contains(errorLevels, strings.ToUpper(e.Level)) {
			continue
		}
		out.Entries = append(out.Entries, logEntry{Time: e.Time, Level: e.Level, Message: e.Message, Details: truncate(e.Details, 4000)})
		if len(out.Entries) == limit {
			break
		}
	}

	// Errors before Laravel handles them (parse errors, fatal errors while booting) end up in
	// the PHP version's log.
	if text, err := tailFile(filepath.Join(s.Root, "log", php.PoolLog(site.PHP)+".log"), 400); err == nil {
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "PHP ") && strings.Contains(line, site.Path) {
				out.PHPErrors = append(out.PHPErrors, truncate(line, 1000))
			}
		}
		if n := len(out.PHPErrors); n > 10 {
			out.PHPErrors = out.PHPErrors[n-10:]
		}
	}

	if out.LogFile == "" && len(out.PHPErrors) == 0 {
		out.Note = "The site has no Laravel log (storage/logs/*.log) yet, and Flock's PHP log has no errors for it."
	}
	return nil, out, nil
}

type serviceLogsIn struct {
	Name  string `json:"name,omitempty" jsonschema:"the log, e.g. mysql, postgres, web, daemon or php8.4-fpm; omit to list the logs"`
	Lines int    `json:"lines,omitempty" jsonschema:"how many lines (default 100)"`
}

type serviceLogsOut struct {
	Name      string   `json:"name,omitempty"`
	Text      string   `json:"text,omitempty"`
	Available []string `json:"available,omitempty"`
}

func (s *server) serviceLogs(_ context.Context, _ *mcp.CallToolRequest, in serviceLogsIn) (*mcp.CallToolResult, serviceLogsOut, error) {
	dir := filepath.Join(s.Root, "log")
	var names []string
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if name, ok := strings.CutSuffix(e.Name(), ".log"); ok {
				names = append(names, name)
			}
		}
	}
	name := strings.TrimSuffix(in.Name, ".log")
	if name == "" {
		return nil, serviceLogsOut{Available: names}, nil
	}
	if !slices.Contains(names, name) {
		return nil, serviceLogsOut{}, fmt.Errorf("there is no log named %q; the logs are: %s", name, strings.Join(names, ", "))
	}
	lines := in.Lines
	if lines <= 0 {
		lines = 100
	}
	text, err := tailFile(filepath.Join(dir, name+".log"), min(lines, 2000))
	if err != nil {
		return nil, serviceLogsOut{}, err
	}
	return nil, serviceLogsOut{Name: name, Text: text}, nil
}

// ---------------------------------------------------------------------------------------------
// dumps

type dumpsIn struct {
	Site  string `json:"site,omitempty" jsonschema:"only dumps of this site (name, domain or project folder); omit for every site"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many dumps to return (default 20)"`
}

type dumpOut struct {
	Time    time.Time `json:"time"`
	Site    string    `json:"site,omitempty"`
	Label   string    `json:"label,omitempty"`
	Source  string    `json:"source,omitempty"` // file:line
	Request string    `json:"request,omitempty"`
	Command string    `json:"command,omitempty"`
	Value   string    `json:"value"`
}

type dumpsOut struct {
	Capturing bool      `json:"capturing"`
	Dumps     []dumpOut `json:"dumps"`
	Note      string    `json:"note,omitempty"`
}

func (s *server) getDumps(ctx context.Context, _ *mcp.CallToolRequest, in dumpsIn) (*mcp.CallToolResult, dumpsOut, error) {
	st, err := s.Daemon.Status(ctx)
	if err != nil {
		return nil, dumpsOut{}, err
	}
	var only *api.SiteStatus
	if in.Site != "" {
		site, err := s.site(ctx, in.Site)
		if err != nil {
			return nil, dumpsOut{}, err
		}
		only = &site
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}

	entries, err := dumps.List(s.Root)
	if err != nil {
		return nil, dumpsOut{}, err
	}
	out := dumpsOut{Capturing: st.Dumps, Dumps: []dumpOut{}}
	for _, e := range entries {
		site := siteFor(st.Sites, e.Source, e.Request)
		if only != nil && site != only.Name {
			continue
		}
		d := dumpOut{Time: e.Time, Site: site, Value: truncate(e.Text, 8000)}
		if e.Label != nil {
			d.Label = *e.Label
		}
		if e.Source != nil {
			d.Source = fmt.Sprintf("%s:%d", e.Source.File, e.Source.Line)
		}
		if e.Request != nil {
			d.Request = e.Request.Method + " " + e.Request.URI
		}
		if e.CommandLine != nil {
			d.Command = *e.CommandLine
		}
		out.Dumps = append(out.Dumps, d)
		if len(out.Dumps) == limit {
			break
		}
	}
	if !st.Dumps {
		out.Note = "Dump capture is off, so dump() output shows in the page or console instead. Turn it on with set_debugging (feature dumps)."
	}
	return nil, out, nil
}

// ---------------------------------------------------------------------------------------------
// events

type eventsIn struct {
	Site          string   `json:"site,omitempty" jsonschema:"only events of this site (name, domain or project folder); omit for every site"`
	Kinds         []string `json:"kinds,omitempty" jsonschema:"only these kinds; omit for all"`
	LatestRequest bool     `json:"latest_request,omitempty" jsonschema:"only the most recent request or CLI run (of the site, when given)"`
	Limit         int      `json:"limit,omitempty" jsonschema:"how many events to return (default 50)"`
}

// eventsSchema is eventsIn's schema with the kinds an item of "kinds" can be.
func eventsSchema() *jsonschema.Schema {
	sch := schema[eventsIn](nil)
	kinds := make([]any, len(capture.Kinds))
	for i, k := range capture.Kinds {
		kinds[i] = k
	}
	sch.Properties["kinds"].Items.Enum = kinds
	return sch
}

type eventOut struct {
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	Site   string    `json:"site,omitempty"`
	Origin string    `json:"origin,omitempty"` // "GET https://..." or the command line
	Group  string    `json:"group"`
	Source string    `json:"source,omitempty"` // file:line of the app code
	capture.Event
}

type eventsOut struct {
	Capturing bool       `json:"capturing"`
	Events    []eventOut `json:"events"`
	Note      string     `json:"note,omitempty"`
}

func (s *server) getEvents(ctx context.Context, _ *mcp.CallToolRequest, in eventsIn) (*mcp.CallToolResult, eventsOut, error) {
	st, err := s.Daemon.Status(ctx)
	if err != nil {
		return nil, eventsOut{}, err
	}
	if err := capture.Valid(in.Kinds); err != nil {
		return nil, eventsOut{}, err
	}
	var only *api.SiteStatus
	if in.Site != "" {
		site, err := s.site(ctx, in.Site)
		if err != nil {
			return nil, eventsOut{}, err
		}
		only = &site
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}

	events, err := capture.List(s.Root, 0, in.Kinds...)
	if err != nil {
		return nil, eventsOut{}, err
	}
	out := eventsOut{Capturing: st.Dumps, Events: []eventOut{}}
	group := ""
	for _, e := range events {
		site := siteFor(st.Sites, e.Source, e.Request)
		if only != nil && site != only.Name {
			continue
		}
		if in.LatestRequest {
			if group == "" {
				group = e.Group
			} else if e.Group != group {
				continue
			}
		}
		o := eventOut{Time: e.Time, Kind: e.Kind, Site: site, Group: e.Group, Event: e}
		switch {
		case e.Request != nil:
			o.Origin = e.Request.Method + " " + e.Request.URI
		case e.CommandLine != nil:
			o.Origin = *e.CommandLine
		}
		if e.Source != nil {
			o.Source = fmt.Sprintf("%s:%d", e.Source.File, e.Source.Line)
		}
		// The embedded event repeats these; leave only the flattened form.
		o.Event.ID, o.Event.Site, o.Event.Request, o.Event.CommandLine, o.Event.Source = "", "", nil, nil, nil
		o.Event.SQL, o.Event.Message = truncate(e.SQL, 4000), truncate(e.Message, 4000)
		out.Events = append(out.Events, o)
		if len(out.Events) == limit {
			break
		}
	}
	switch {
	case !st.Dumps:
		out.Note = "Capture is off, so nothing new is recorded. Turn it on with set_debugging (feature dumps)."
	case len(st.CaptureUnsupported) > 0:
		out.Note = "PHP " + strings.Join(st.CaptureUnsupported, ", ") + " only capture dumps: their builds lack the agent's extension (a newer build of that PHP version has it)."
	}
	return nil, out, nil
}

// siteFor attributes a dump or event to a site by the file that caused it, or by the request host.
func siteFor(sites []api.SiteStatus, source *dumps.Source, request *dumps.Request) string {
	if source != nil {
		if site, ok := siteForDir(sites, source.File); ok {
			return site.Name
		}
	}
	if request != nil {
		if u, err := url.Parse(request.URI); err == nil {
			if site, ok := findSite(sites, u.Hostname()); ok {
				return site.Name
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------------------------
// mail

type listMailIn struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"how many messages to return (default 20)"`
	Search string `json:"search,omitempty" jsonschema:"only messages whose subject, sender or recipients contain this text"`
}

type mailSummary struct {
	ID      string    `json:"id"`
	Date    time.Time `json:"date"`
	From    string    `json:"from"`
	To      []string  `json:"to"`
	Subject string    `json:"subject"`
	Inbox   string    `json:"inbox,omitempty"` // the app that sent it (its MAIL_USERNAME)
}

type listMailOut struct {
	Messages []mailSummary `json:"messages"`
	Total    int           `json:"total"`
}

func (s *server) listMail(_ context.Context, _ *mcp.CallToolRequest, in listMailIn) (*mcp.CallToolResult, listMailOut, error) {
	messages, err := mail.List(filepath.Join(s.Root, "mail"))
	if err != nil {
		return nil, listMailOut{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	search := strings.ToLower(in.Search)
	out := listMailOut{Messages: []mailSummary{}, Total: len(messages)}
	for _, m := range messages {
		if search != "" && !strings.Contains(strings.ToLower(m.Subject+" "+m.From+" "+strings.Join(m.To, " ")), search) {
			continue
		}
		out.Messages = append(out.Messages, mailSummary{ID: m.ID, Date: m.Date, From: m.From, To: m.To, Subject: m.Subject, Inbox: m.Inbox})
		if len(out.Messages) == limit {
			break
		}
	}
	return nil, out, nil
}

type readMailIn struct {
	ID string `json:"id" jsonschema:"the message's id, from list_mail"`
}

type attachmentOut struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
}

type readMailOut struct {
	ID          string          `json:"id"`
	Date        time.Time       `json:"date"`
	From        string          `json:"from"`
	To          []string        `json:"to"`
	Cc          []string        `json:"cc,omitempty"`
	Subject     string          `json:"subject"`
	Text        string          `json:"text"`
	Links       []string        `json:"links,omitempty"`
	Attachments []attachmentOut `json:"attachments,omitempty"`
}

func (s *server) readMail(_ context.Context, _ *mcp.CallToolRequest, in readMailIn) (*mcp.CallToolResult, readMailOut, error) {
	msg, err := mail.Find(filepath.Join(s.Root, "mail"), in.ID)
	if err != nil {
		return nil, readMailOut{}, fmt.Errorf("no caught mail with id %q — use list_mail for the ids", in.ID)
	}
	d, err := mail.Parse(msg)
	if err != nil {
		return nil, readMailOut{}, err
	}
	out := readMailOut{ID: d.ID, Date: d.Date, From: d.From, To: d.To, Cc: d.Cc, Subject: d.Subject, Text: d.Text, Links: links(d.HTML)}
	if strings.TrimSpace(out.Text) == "" {
		out.Text = htmlText(d.HTML)
	}
	out.Text = truncate(out.Text, 20000)
	for _, a := range d.Attachments {
		out.Attachments = append(out.Attachments, attachmentOut{Filename: a.Filename, ContentType: a.ContentType, Size: a.Size})
	}
	return nil, out, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------------------------
// extensions

func (s *server) phpExtensions(ctx context.Context, _ *mcp.CallToolRequest, in siteArg) (*mcp.CallToolResult, phpext.Report, error) {
	site, err := s.site(ctx, in.Site)
	if err != nil {
		return nil, phpext.Report{}, err
	}
	report, err := s.Daemon.SiteExtensions(ctx, site.Name)
	return nil, report, err
}
