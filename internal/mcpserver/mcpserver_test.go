package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"flock/internal/api"
	"flock/internal/catalog"
	"flock/internal/php"
	"flock/internal/phpext"
	"flock/internal/postgres"
)

// fakeDaemon answers like the daemon and records the changes asked of it.
type fakeDaemon struct {
	mu       sync.Mutex
	down     bool
	status   api.Status
	cfg      api.ConfigView
	services []api.Service
	catalog  api.Catalog
	php      api.PHPVersions
	pg       api.PostgresVersions
	pgExt    api.PostgresExtensions
	// phpPolls is how many PHPVersions calls a PHP install takes to finish (0: never).
	phpPolls int
	polled   int
	changes  []string
}

func (f *fakeDaemon) record(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := json.Marshal(args)
	f.changes = append(f.changes, format+" "+string(b))
}

func (f *fakeDaemon) err() error {
	if f.down {
		return api.ErrDaemonNotRunning
	}
	return nil
}

func (f *fakeDaemon) Status(context.Context) (api.Status, error)      { return f.status, f.err() }
func (f *fakeDaemon) Config(context.Context) (api.ConfigView, error)  { return f.cfg, f.err() }
func (f *fakeDaemon) Services(context.Context) ([]api.Service, error) { return f.services, f.err() }
func (f *fakeDaemon) Catalog(context.Context) (api.Catalog, error)    { return f.catalog, f.err() }
func (f *fakeDaemon) PostgresVersions(context.Context) (api.PostgresVersions, error) {
	return f.pg, f.err()
}
func (f *fakeDaemon) PostgresExtensions(_ context.Context, major string) (api.PostgresExtensions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.pgExt
	out.Major = major
	// A copy, as the daemon's answer is: the background install changes f.pgExt meanwhile.
	out.Extensions = slices.Clone(f.pgExt.Extensions)
	return out, f.err()
}
func (f *fakeDaemon) InstallPostgresExtension(_ context.Context, major, name string) (api.PostgresExtensions, error) {
	f.record("install postgres extension", major, name)
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, e := range f.pgExt.Extensions {
		if e.Name == name {
			f.pgExt.Extensions[i].Installed = e.Latest
		}
	}
	out := f.pgExt
	out.Extensions = slices.Clone(f.pgExt.Extensions)
	return out, nil
}
func (f *fakeDaemon) CheckPHP(ctx context.Context) (api.PHPVersions, error) {
	return f.PHPVersions(ctx)
}
func (f *fakeDaemon) SiteExtensions(_ context.Context, name string) (phpext.Report, error) {
	f.record("extensions", name)
	return phpext.Report{PHP: "8.4", Extensions: []phpext.Extension{{Name: "intl", RequiredBy: []string{"composer.json"}, Available: true}}}, f.err()
}
func (f *fakeDaemon) NodeVersions(context.Context) (api.NodeVersions, error) {
	return api.NodeVersions{}, f.err()
}

func (f *fakeDaemon) PHPVersions(context.Context) (api.PHPVersions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polled++
	for i, v := range f.php.Versions {
		if v.Job != nil && f.phpPolls > 0 && f.polled >= f.phpPolls {
			f.php.Versions[i].Job, f.php.Versions[i].Installed = nil, v.Latest
		}
	}
	return f.php, f.err()
}

func (f *fakeDaemon) InstallPHP(_ context.Context, minor string) (api.PHPVersions, error) {
	f.record("install php", minor)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polled = 0
	for i, v := range f.php.Versions {
		if v.Minor == minor {
			f.php.Versions[i].Job = &php.Job{State: "running", Phase: "downloading", Progress: 0.5}
		}
	}
	return f.php, nil
}

func (f *fakeDaemon) SetServiceRunning(_ context.Context, id string, on bool) ([]api.Service, error) {
	f.record("service", id, on)
	return f.services, nil
}

func (f *fakeDaemon) InstallService(_ context.Context, id, line string, port int) (api.Catalog, error) {
	f.record("install service", id, line, port)
	return f.catalog, nil
}

func (f *fakeDaemon) InstallPostgres(_ context.Context, major string, _ ...string) (api.PostgresVersions, error) {
	f.record("install postgres", major)
	return api.PostgresVersions{}, nil
}

func (f *fakeDaemon) InstallNode(_ context.Context, major string) (api.NodeVersions, error) {
	f.record("install node", major)
	return api.NodeVersions{}, nil
}

func (f *fakeDaemon) LinkSite(_ context.Context, dir, name string) (api.Status, error) {
	f.record("link", dir, name)
	st := f.status
	st.Sites = append(st.Sites, api.SiteStatus{Name: filepath.Base(dir), Domain: filepath.Base(dir) + ".test", Path: dir, URL: "https://" + filepath.Base(dir) + ".test"})
	return st, nil
}

func (f *fakeDaemon) Proxy(_ context.Context, name, upstream string, secured *bool) (api.Status, error) {
	f.record("proxy", name, upstream, secured)
	st := f.status
	st.Proxies = append(st.Proxies, api.ProxyStatus{Name: name, Domain: name + ".test", Upstream: "http://127.0.0.1:" + upstream, URL: "http://" + name + ".test"})
	return st, nil
}

func (f *fakeDaemon) UpdateProxy(_ context.Context, name string, c api.ProxyChange) (api.Status, error) {
	f.record("update proxy", name, c)
	return f.status, nil
}

func (f *fakeDaemon) StartProxy(_ context.Context, name string) (api.Status, error) {
	f.record("start proxy", name)
	st := f.status
	st.Proxies = []api.ProxyStatus{{Name: name, Command: &api.ProxyCommand{Command: "npm run dev", State: api.ProxyStarting, Log: "proxy-" + name}}}
	return st, nil
}

func (f *fakeDaemon) StopProxy(_ context.Context, name string) (api.Status, error) {
	f.record("stop proxy", name)
	return f.status, nil
}

func (f *fakeDaemon) UpdateSite(_ context.Context, name string, c api.SiteChange) (api.Status, error) {
	f.record("update", name, c)
	return f.status, nil
}

func (f *fakeDaemon) AddDomain(_ context.Context, name, domain string) (api.Status, error) {
	f.record("domain", name, domain)
	return f.status, nil
}

func (f *fakeDaemon) PinSite(_ context.Context, name, tool, version string) (api.Status, error) {
	f.record("pin", name, tool, version)
	return f.status, nil
}

func (f *fakeDaemon) UnpinSite(_ context.Context, name, tool string) (api.Status, error) {
	f.record("unpin", name, tool)
	return f.status, nil
}

func (f *fakeDaemon) RestartSite(_ context.Context, name string) (api.Status, error) {
	f.record("restart", name)
	return f.status, nil
}

func (f *fakeDaemon) RestartReverb(_ context.Context, name string) (api.Status, error) {
	f.record("restart_reverb", name)
	return f.status, nil
}

func (f *fakeDaemon) StopSite(_ context.Context, name string) (api.Status, error) {
	f.record("stop", name)
	return f.status, nil
}

func (f *fakeDaemon) CreateWorktree(_ context.Context, req api.WorktreeCreate) (api.WorktreeCreated, error) {
	f.record("worktree create", req)
	path := filepath.Join(filepath.Dir(f.status.Sites[0].Path), "shop-feature-x")
	f.mu.Lock()
	f.status.Sites = append(f.status.Sites, api.SiteStatus{
		Name: "feature-x.shop", Domain: "feature-x.shop.test", Path: path, URL: "https://feature-x.shop.test", Secured: true,
		Parent: "shop", Branch: req.Branch, Worktree: &api.WorktreeStatus{State: "running", Step: "composer install", Log: "worktree-feature-x.shop"},
	})
	f.mu.Unlock()
	return api.WorktreeCreated{Path: path, Setup: api.WorktreeStatus{State: "queued"}}, f.err()
}
func (f *fakeDaemon) SetupWorktree(_ context.Context, path string, again bool, db string) (api.WorktreeStatus, error) {
	f.record("worktree setup", path, again, db)
	return api.WorktreeStatus{State: "running"}, f.err()
}

// WorktreeSetup finishes a setup on the second poll, with a database of its own.
func (f *fakeDaemon) WorktreeSetup(_ context.Context, path string) (api.WorktreeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polled++
	for i, site := range f.status.Sites {
		if site.Path == path && site.Worktree != nil && f.polled >= 2 {
			f.status.Sites[i].Worktree = &api.WorktreeStatus{State: "ready", Log: site.Worktree.Log, Database: "shop_feature_x"}
		}
		if site.Path == path && site.Worktree != nil {
			return *f.status.Sites[i].Worktree, nil
		}
	}
	return api.WorktreeStatus{}, api.ErrNotWorktree
}

func (f *fakeDaemon) UpdateSettings(_ context.Context, s api.Settings) (api.Status, error) {
	f.record("settings", s)
	return f.status, nil
}

// fixture is a Flock root with one site, "shop", in a project folder.
func fixture(t *testing.T) (*fakeDaemon, Options) {
	t.Helper()
	root, project := t.TempDir(), t.TempDir()
	d := &fakeDaemon{
		status: api.Status{Dumps: true, Sites: []api.SiteStatus{{
			Name: "shop", Domain: "shop.test", Path: project, URL: "https://shop.test", Secured: true,
			PHP: "8.4", PHPSource: "project", PHPFile: filepath.Join(project, ".flockrc"), Node: "22", NodeSource: "default",
			Server: "fastcgi", Domains: []api.SiteDomain{{Entry: "tenant1", Host: "tenant1.shop.test", URL: "https://tenant1.shop.test"}},
		}}},
		cfg: api.ConfigView{DefaultPHP: "8.5", DefaultNode: "22"},
	}
	return d, Options{Version: "test", Root: root, Daemon: d, Dir: filepath.Join(project, "app", "Http"), Wait: 2 * time.Second}
}

func connect(t *testing.T, opts Options) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := New(ctx, opts).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool calls a tool and decodes its structured result into out; it returns the text of an error result.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		return res.Content[0].(*mcp.TextContent).Text
	}
	if out != nil {
		data, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s: %v in %s", name, err, data)
		}
	}
	return ""
}

func TestToolsAndAnnotations(t *testing.T) {
	_, opts := fixture(t)
	cs := connect(t, opts)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	readOnly := map[string]bool{}
	for _, tool := range res.Tools {
		readOnly[tool.Name] = tool.Annotations != nil && tool.Annotations.ReadOnlyHint
		if tool.Annotations != nil && !tool.Annotations.ReadOnlyHint && (tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint) {
			t.Errorf("%s may be destructive; Flock's tools never remove anything", tool.Name)
		}
	}
	for _, name := range []string{"status", "site_info", "list_sites", "list_services", "list_php_versions", "list_node_versions",
		"get_errors", "get_dumps", "get_events", "list_mail", "read_mail", "service_logs", "php_extensions", "list_worktrees"} {
		if !readOnly[name] {
			t.Errorf("%s should be a read-only tool", name)
		}
	}
	for _, name := range []string{"service", "install_version", "link_site", "proxy_site", "proxy_server", "update_site", "set_debugging", "create_database", "postgres_extension",
		"create_worktree", "setup_worktree"} {
		if ro, ok := readOnly[name]; !ok || ro {
			t.Errorf("%s should be a tool that changes things (listed: %v)", name, ok)
		}
	}
	if len(res.Tools) != 25 {
		t.Errorf("got %d tools", len(res.Tools))
	}
}

func TestInstructionsDescribeTheProject(t *testing.T) {
	_, opts := fixture(t)
	cs := connect(t, opts)

	got := cs.InitializeResult().Instructions
	for _, want := range []string{`"shop"`, "https://shop.test", "PHP 8.4 (pinned in .flockrc)", "Node.js 22 (Flock's default)", "php artisan serve"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions lack %q:\n%s", want, got)
		}
	}

	d, opts := fixture(t)
	d.down = true
	if got := connect(t, opts).InitializeResult().Instructions; !strings.Contains(got, "isn't running") {
		t.Errorf("daemon down: %s", got)
	}
}

func TestSiteResolution(t *testing.T) {
	d, opts := fixture(t)
	project := d.status.Sites[0].Path
	cs := connect(t, opts)

	var info siteInfo
	if e := callTool(t, cs, "site_info", nil, &info); e != "" || info.Name != "shop" {
		t.Fatalf("from the working directory: %q %+v", e, info)
	}
	if info.PHP.Source != "project" || len(info.OtherURLs) != 1 || !info.HTTPS {
		t.Fatalf("details: %+v", info)
	}
	for _, ref := range []string{"shop", "shop.test", "https://tenant1.shop.test/", filepath.Join(project, "routes")} {
		if e := callTool(t, cs, "site_info", map[string]any{"site": ref}, &info); e != "" || info.Name != "shop" {
			t.Errorf("site %q: %q", ref, e)
		}
	}
	if e := callTool(t, cs, "site_info", map[string]any{"site": "nope"}, nil); !strings.Contains(e, "no Flock site") {
		t.Errorf("unknown site: %q", e)
	}

	opts.Dir = t.TempDir()
	if e := callTool(t, connect(t, opts), "site_info", nil, nil); !strings.Contains(e, "pass `site`") {
		t.Errorf("outside every site: %q", e)
	}
	opts.Site = "shop"
	if e := callTool(t, connect(t, opts), "site_info", nil, &info); e != "" || info.Name != "shop" {
		t.Errorf("FLOCK_SITE: %q", e)
	}
}

func TestGetErrorsReadsTheLaravelLogAndPHPLog(t *testing.T) {
	d, opts := fixture(t)
	project := d.status.Sites[0].Path
	logs := filepath.Join(project, "storage", "logs")
	_ = os.MkdirAll(logs, 0o755)
	_ = os.WriteFile(filepath.Join(logs, "laravel.log"), []byte(
		"[2026-10-01 10:00:00] local.INFO: started\n"+
			"[2026-10-01 10:00:01] local.ERROR: Undefined variable $cart {\"exception\":\"x\"}\n#0 trace line\n"+
			"[2026-10-01 10:00:02] local.DEBUG: noise\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(opts.Root, "log"), 0o755)
	phpLog := filepath.Join(opts.Root, "log", php.PoolLog("8.4")+".log")
	_ = os.WriteFile(phpLog, []byte("NOTICE: ready\nWARNING: child said into stderr: \"PHP Parse error: syntax error in "+
		filepath.Join(project, "routes", "web.php")+" on line 3\"\nPHP Fatal error: elsewhere in /other/app.php\n"), 0o644)
	cs := connect(t, opts)

	var out errorsOut
	if e := callTool(t, cs, "get_errors", nil, &out); e != "" {
		t.Fatal(e)
	}
	if len(out.Entries) != 1 || !strings.EqualFold(out.Entries[0].Level, "error") || !strings.Contains(out.Entries[0].Details, "trace line") {
		t.Fatalf("errors only: %+v", out.Entries)
	}
	if len(out.PHPErrors) != 1 || !strings.Contains(out.PHPErrors[0], "Parse error") {
		t.Fatalf("PHP errors of this site only: %q", out.PHPErrors)
	}
	if callTool(t, cs, "get_errors", map[string]any{"all_levels": true}, &out); len(out.Entries) != 3 || !strings.EqualFold(out.Entries[0].Level, "debug") {
		t.Fatalf("all levels, newest first: %+v", out.Entries)
	}
}

func TestDumpsAreFilteredBySite(t *testing.T) {
	d, opts := fixture(t)
	project := d.status.Sites[0].Path
	_ = os.MkdirAll(filepath.Join(opts.Root, "dumps"), 0o755)
	lines := []string{
		`{"id":"1","text":"\"shop dump\"","source":{"file":` + jsonString(filepath.Join(project, "app", "x.php")) + `,"line":7}}`,
		`{"id":"2","text":"\"other\"","request":{"method":"GET","uri":"https://blog.test/"}}`,
		`{"id":"3","text":"\"by host\"","request":{"method":"POST","uri":"https://tenant1.shop.test/cart"}}`,
	}
	_ = os.WriteFile(filepath.Join(opts.Root, "dumps", "dumps.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	cs := connect(t, opts)

	var out dumpsOut
	callTool(t, cs, "get_dumps", nil, &out)
	if len(out.Dumps) != 3 || out.Dumps[0].Value != `"by host"` {
		t.Fatalf("every site, newest first: %+v", out.Dumps)
	}
	callTool(t, cs, "get_dumps", map[string]any{"site": "shop"}, &out)
	if len(out.Dumps) != 2 || out.Dumps[1].Source != filepath.Join(project, "app", "x.php")+":7" || out.Dumps[0].Request != "POST https://tenant1.shop.test/cart" {
		t.Fatalf("shop only: %+v", out.Dumps)
	}

	d.status.Dumps = false
	callTool(t, cs, "get_dumps", nil, &out)
	if out.Capturing || !strings.Contains(out.Note, "set_debugging") {
		t.Fatalf("capture off: %+v", out)
	}
}

func TestMailTools(t *testing.T) {
	_, opts := fixture(t)
	path := filepath.Join(opts.Root, "mail", "shop", "1-000001.eml")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw := "From: Shop <hi@shop.test>\r\nTo: ann@example.com\r\nSubject: Verify your email\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/html\r\n\r\n<html><head><style>p{}</style></head><body><p>Hi Ann,</p>" +
		"<p><a href=\"https://shop.test/verify?id=1&amp;sig=abc\">Verify</a></p></body></html>\r\n"
	_ = os.WriteFile(path, []byte(raw), 0o600)
	cs := connect(t, opts)

	var list listMailOut
	callTool(t, cs, "list_mail", map[string]any{"search": "verify"}, &list)
	if len(list.Messages) != 1 || list.Messages[0].ID != "1-000001" {
		t.Fatalf("list: %+v", list)
	}
	if callTool(t, cs, "list_mail", map[string]any{"search": "invoice"}, &list); len(list.Messages) != 0 {
		t.Fatalf("search: %+v", list)
	}

	var msg readMailOut
	callTool(t, cs, "read_mail", map[string]any{"id": "1-000001"}, &msg)
	if len(msg.Links) != 1 || msg.Links[0] != "https://shop.test/verify?id=1&sig=abc" {
		t.Fatalf("links: %q", msg.Links)
	}
	if !strings.Contains(msg.Text, "Hi Ann,") || strings.Contains(msg.Text, "<p>") || strings.Contains(msg.Text, "p{}") {
		t.Fatalf("text: %q", msg.Text)
	}
	if e := callTool(t, cs, "read_mail", map[string]any{"id": "nope"}, nil); !strings.Contains(e, "list_mail") {
		t.Fatalf("unknown id: %q", e)
	}
}

func TestInstallPHPWaitsAndReportsProgress(t *testing.T) {
	d, opts := fixture(t)
	d.php = api.PHPVersions{Versions: []api.PHPRelease{{Release: php.Release{Minor: "8.3", Latest: "8.3.20", Status: "available"}}}}
	d.phpPolls = 3
	cs := connect(t, opts)

	var out actionOut
	if e := callTool(t, cs, "install_version", map[string]any{"tool": "php", "version": "8.3"}, &out); e != "" || !out.Done || !strings.Contains(out.Message, "8.3.20") {
		t.Fatalf("%q %+v", e, out)
	}

	// An install that outlasts the wait answers that it's still running.
	d.php.Versions[0].Installed = ""
	d.phpPolls = 0
	opts.Wait = 300 * time.Millisecond
	if e := callTool(t, connect(t, opts), "install_version", map[string]any{"tool": "php", "version": "8.3"}, &out); e != "" || out.Done || !strings.Contains(out.Message, "list_php_versions") {
		t.Fatalf("%q %+v", e, out)
	}

	if e := callTool(t, cs, "install_version", map[string]any{"tool": "ruby", "version": "3"}, nil); e == "" {
		t.Fatal("an unknown tool must be rejected by the schema")
	}
}

func TestInstallCatalogService(t *testing.T) {
	d, opts := fixture(t)
	d.catalog = api.Catalog{Services: []catalog.Entry{{ID: "mysql", Name: "MySQL"}}}
	d.services = []api.Service{{ID: "mysql", Name: "MySQL", Version: "8.4.11", Port: 3306, Installed: true, Enabled: true, Running: true, Env: "DB_CONNECTION=mysql"}}
	cs := connect(t, opts)

	var out actionOut
	if e := callTool(t, cs, "service", map[string]any{"action": "install", "service": "mysql", "version": "8.4"}, &out); e != "" || !out.Done {
		t.Fatalf("%q %+v", e, out)
	}
	if !strings.Contains(out.Message, "running on 127.0.0.1:3306") || !strings.Contains(out.Message, "DB_CONNECTION=mysql") {
		t.Fatalf("message: %s", out.Message)
	}

	d.catalog.Services[0].Job = &catalog.Job{State: "failed", Error: "checksum mismatch"}
	if e := callTool(t, cs, "service", map[string]any{"action": "install", "service": "mysql"}, nil); !strings.Contains(e, "checksum mismatch") {
		t.Fatalf("failed install: %q", e)
	}
}

func TestUpdateSiteActions(t *testing.T) {
	d, opts := fixture(t)
	cs := connect(t, opts)

	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"action": "isolate", "tool": "php", "version": "8.3"}, `update ["shop",{"php":"8.3"}]`},
		{map[string]any{"action": "unisolate", "tool": "node"}, `update ["shop",{"node":""}]`},
		{map[string]any{"action": "pin", "tool": "php"}, `pin ["shop","php",""]`},
		{map[string]any{"action": "unsecure"}, `update ["shop",{"secured":false}]`},
		{map[string]any{"action": "set_server", "server": "frankenphp", "worker": "octane"}, `update ["shop",{"server":"frankenphp","worker":"octane"}]`},
		{map[string]any{"action": "set_type", "type": "node"}, `update ["shop",{"type":"node"}]`},
		{map[string]any{"action": "add_domain", "domain": "tenant2"}, `domain ["shop","tenant2"]`},
		{map[string]any{"action": "restart", "site": "shop.test"}, `restart ["shop"]`},
		{map[string]any{"action": "stop"}, `stop ["shop"]`},
	} {
		d.changes = nil
		if e := callTool(t, cs, "update_site", c.args, nil); e != "" {
			t.Errorf("%v: %s", c.args, e)
			continue
		}
		if len(d.changes) != 1 || d.changes[0] != c.want {
			t.Errorf("%v: got %q, want %q", c.args, d.changes, c.want)
		}
	}
	if e := callTool(t, cs, "update_site", map[string]any{"action": "isolate", "tool": "php"}, nil); !strings.Contains(e, "needs version") {
		t.Errorf("isolate without a version: %q", e)
	}
}

func TestLinkSiteAndDebugging(t *testing.T) {
	d, opts := fixture(t)
	opts.Dir = filepath.Join(t.TempDir(), "blog")
	_ = os.Mkdir(opts.Dir, 0o755)
	cs := connect(t, opts)

	var out siteActionOut
	if e := callTool(t, cs, "link_site", nil, &out); e != "" || out.Site == nil || out.Site.URL != "https://blog.test" {
		t.Fatalf("%q %+v", e, out)
	}
	if d.changes[0] != `link [`+jsonString(opts.Dir)+`,""]` {
		t.Fatalf("changes: %q", d.changes)
	}

	d.changes = nil
	callTool(t, cs, "set_debugging", map[string]any{"feature": "dumps", "enabled": true}, nil)
	if len(d.changes) != 1 || !strings.Contains(d.changes[0], `"dumps":true`) || !strings.Contains(d.changes[0], `"xdebug":null`) {
		t.Fatalf("settings: %q", d.changes)
	}
}

func TestProxySite(t *testing.T) {
	d, opts := fixture(t)
	cs := connect(t, opts)

	var out proxySiteOut
	if e := callTool(t, cs, "proxy_site", map[string]any{"name": "softpack", "upstream": "3000"}, &out); e != "" || out.Proxy == nil || out.Proxy.URL != "http://softpack.test" {
		t.Fatalf("%q %+v", e, out)
	}
	if d.changes[0] != `proxy ["softpack","3000",null]` {
		t.Fatalf("changes: %q", d.changes)
	}
	if e := callTool(t, cs, "proxy_site", map[string]any{"name": "softpack"}, nil); !strings.Contains(e, "upstream") {
		t.Fatalf("a proxy needs an upstream: %q", e)
	}

	// A command runs in the project the server was started in, unless a folder is given.
	if e := callTool(t, cs, "proxy_site", map[string]any{"name": "softpack", "upstream": "3000", "command": "npm run dev"}, &out); e != "" {
		t.Fatal(e)
	}
	if got, want := d.changes[len(d.changes)-1], `update proxy ["softpack",{"command":"npm run dev","dir":`+jsonString(opts.Dir)+`}]`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if e := callTool(t, cs, "proxy_server", map[string]any{"name": "softpack", "action": "start"}, &out); e != "" || out.Proxy == nil || !strings.Contains(out.Message, "proxy-softpack") {
		t.Fatalf("start: %q %+v", e, out)
	}
	if e := callTool(t, cs, "proxy_server", map[string]any{"name": "softpack", "action": "restart"}, nil); e == "" {
		t.Fatal("an unknown action was taken")
	}

	d.status.Proxies = []api.ProxyStatus{{Name: "softpack", URL: "http://softpack.test", Upstream: "http://127.0.0.1:3000"}}
	var sites sitesOut
	if e := callTool(t, cs, "list_sites", nil, &sites); e != "" || len(sites.Proxies) != 1 || sites.Proxies[0].Name != "softpack" {
		t.Fatalf("%q %+v", e, sites)
	}
}

func TestCreateDatabaseNeedsARunningService(t *testing.T) {
	d, opts := fixture(t)
	cs := connect(t, opts)
	if e := callTool(t, cs, "create_database", map[string]any{"name": "shop"}, nil); !strings.Contains(e, "no database service runs") {
		t.Fatalf("nothing running: %q", e)
	}
	d.services = []api.Service{{ID: "mysql", Name: "MySQL", Installed: true}, {ID: "postgres", Name: "PostgreSQL", Installed: true, Running: true}}
	if e := callTool(t, cs, "create_database", map[string]any{"name": "shop", "service": "mysql"}, nil); !strings.Contains(e, "isn't running") {
		t.Fatalf("stopped service: %q", e)
	}
}

func TestStatusWhenTheDaemonIsDown(t *testing.T) {
	d, opts := fixture(t)
	d.down = true
	var out statusOut
	if e := callTool(t, connect(t, opts), "status", nil, &out); e != "" || out.Running || !strings.Contains(out.Message, "flock daemon") {
		t.Fatalf("%q %+v", e, out)
	}
}

func TestSiteResourceAndDebugPrompt(t *testing.T) {
	_, opts := fixture(t)
	cs := connect(t, opts)
	ctx := context.Background()

	for _, uri := range []string{currentSiteURI, siteURIPrefix + "shop"} {
		res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil || !strings.Contains(res.Contents[0].Text, `"url": "https://shop.test"`) {
			t.Fatalf("%s: %v %+v", uri, err, res)
		}
	}

	res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "debug_site", Arguments: map[string]string{"problem": "checkout shows a 500"}})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Messages[0].Content.(*mcp.TextContent).Text
	for _, want := range []string{"checkout shows a 500", "https://shop.test", "## Recent errors", "## Recent mail"} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt lacks %q:\n%s", want, text)
		}
	}
}

func TestHTMLText(t *testing.T) {
	got := htmlText("<div>Hello&nbsp;<b>you</b></div><script>x()</script><p>Line&amp;two</p>\n\n\n\n<p>end</p>")
	if got != "Hello\u00a0you\nLine&two\n\nend" {
		t.Fatalf("%q", got)
	}
	if got := truncate("ééé", 3); !strings.HasPrefix(got, "é\n") {
		t.Fatalf("truncate splits runes: %q", got)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestPHPExtensionsOfTheCurrentSite(t *testing.T) {
	_, opts := fixture(t)
	cs := connect(t, opts)

	var out phpext.Report
	if e := callTool(t, cs, "php_extensions", nil, &out); e != "" || out.PHP != "8.4" || len(out.Extensions) != 1 || out.Extensions[0].Name != "intl" {
		t.Fatalf("%q %+v", e, out)
	}
}

func TestWorktreeTools(t *testing.T) {
	d, opts := fixture(t)
	cs := connect(t, opts)

	if e := callTool(t, cs, "setup_worktree", nil, nil); !strings.Contains(e, "not in a worktree") {
		t.Errorf("setup_worktree in the site itself: %q", e)
	}

	var created worktreeActionOut
	if e := callTool(t, cs, "create_worktree", map[string]any{"branch": "feature/x", "database": "copy"}, &created); e != "" {
		t.Fatal(e)
	}
	if want := `worktree create [{"site":"shop","branch":"feature/x","db":"copy"}]`; len(d.changes) != 1 || d.changes[0] != want {
		t.Errorf("got %q, want %q", d.changes, want)
	}
	if !created.Done || created.Worktree == nil || created.Worktree.Database != "shop_feature_x" ||
		!strings.Contains(created.Message, "https://feature-x.shop.test") {
		t.Errorf("created: %+v %+v", created, created.Worktree)
	}

	var list listWorktreesOut
	if e := callTool(t, cs, "list_worktrees", map[string]any{"site": "shop"}, &list); e != "" {
		t.Fatal(e)
	}
	if len(list.Worktrees) != 1 || list.Worktrees[0].Branch != "feature/x" || list.Worktrees[0].Site != "shop" {
		t.Errorf("list: %+v", list)
	}

	d.changes = nil
	for _, ref := range []string{"feature/x", "feature-x", "feature-x.shop.test", created.Worktree.Path} {
		if e := callTool(t, cs, "setup_worktree", map[string]any{"worktree": ref, "again": true}, nil); e != "" {
			t.Errorf("%s: %s", ref, e)
		}
	}
	if len(d.changes) != 4 || !strings.Contains(d.changes[0], `shop-feature-x",true,""`) {
		t.Errorf("setups: %q", d.changes)
	}
	if e := callTool(t, cs, "setup_worktree", map[string]any{"worktree": "nope"}, nil); !strings.Contains(e, "no worktree") {
		t.Errorf("an unknown worktree: %q", e)
	}
}

func TestPostgresExtension(t *testing.T) {
	d, opts := fixture(t)
	cs := connect(t, opts)
	if e := callTool(t, cs, "postgres_extension", map[string]any{"action": "list"}, nil); !strings.Contains(e, "isn't installed") {
		t.Fatalf("without PostgreSQL: %q", e)
	}

	d.pg = api.PostgresVersions{Active: "18"}
	ext := func(name string) postgres.ExtensionState {
		e, _ := postgres.FindExtension(name)
		return postgres.ExtensionState{Extension: e, Supported: true, Latest: "1.0"}
	}
	d.pgExt = api.PostgresExtensions{
		Extensions: []postgres.ExtensionState{ext("postgis"), ext("pgrouting"), ext("pgvector")},
		CustomDir:  "/flock/custom/18",
		Custom:     []postgres.CustomExtension{{Name: "hello", Linked: true}, {Name: "broken", Problem: "its library broken isn't in lib/"}},
	}

	var out actionOut
	if e := callTool(t, cs, "postgres_extension", map[string]any{"action": "install", "extension": "pgrouting"}, &out); e != "" || !out.Done {
		t.Fatalf("%q %+v", e, out)
	}
	for _, want := range []string{"pgRouting for PostgreSQL 18 is installed", "CREATE EXTENSION IF NOT EXISTS postgis;", "CREATE EXTENSION IF NOT EXISTS pgrouting;", "DB::statement("} {
		if !strings.Contains(out.Message, want) {
			t.Errorf("install message lacks %q: %s", want, out.Message)
		}
	}
	d.mu.Lock()
	got := d.changes[len(d.changes)-1]
	d.mu.Unlock()
	if got != `install postgres extension ["18","pgrouting"]` {
		t.Fatalf("asked the daemon: %s", got)
	}

	if e := callTool(t, cs, "postgres_extension", map[string]any{"action": "list", "major": "17"}, &out); e != "" {
		t.Fatal(e)
	}
	for _, want := range []string{"PostgreSQL 17 extensions", "pgrouting 1.0: installed (CREATE EXTENSION pgrouting)", "vector", "hello: CREATE EXTENSION hello", "broken: can't be used"} {
		if !strings.Contains(out.Message, want) {
			t.Errorf("list lacks %q: %s", want, out.Message)
		}
	}
	if e := callTool(t, cs, "postgres_extension", map[string]any{"action": "install", "extension": "plv8"}, nil); e == "" {
		t.Fatal("an unknown extension must be rejected by the schema")
	}
}
