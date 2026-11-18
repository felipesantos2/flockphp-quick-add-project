package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/binaries"
	"flock/internal/capture"
	"flock/internal/catalog"
	"flock/internal/config"
	"flock/internal/dumps"
	"flock/internal/frankenphp"
	"flock/internal/php"
	"flock/internal/phpext"
	"flock/internal/postgres"
	"flock/internal/share"
)

// fakeBackend applies mutations to config.json in root, like the daemon, without running services.
type fakeBackend struct {
	catalog *catalog.Manager
	root    string
	sites   []SiteStatus
	php     *php.Manager

	pg         *postgres.Manager
	frankenphp *frankenphp.Manager
	binaries   *binaries.Catalog

	proxyCalls []string // "start <name>", "stop <name>"
}

func (f *fakeBackend) PHP() *php.Manager { return f.php }

func (f *fakeBackend) StartProxy(name string) error {
	if name == "missing" {
		return fmt.Errorf("%w: no proxy named %s", ErrInvalidRequest, name)
	}
	f.proxyCalls = append(f.proxyCalls, "start "+name)
	return nil
}

func (f *fakeBackend) StopProxy(name string) error {
	f.proxyCalls = append(f.proxyCalls, "stop "+name)
	return nil
}

func (f *fakeBackend) PostgresVersions() *postgres.Manager     { return f.pg }
func (f *fakeBackend) Catalog() *catalog.Manager               { return f.catalog }
func (f *fakeBackend) FrankenPHP() *frankenphp.Manager         { return f.frankenphp }
func (f *fakeBackend) Binaries() *binaries.Catalog             { return f.binaries }
func (f *fakeBackend) RestartSite(string) error                { return nil }
func (f *fakeBackend) RestartReverb(string) error              { return nil }
func (f *fakeBackend) StopSite(string) error                   { return nil }
func (f *fakeBackend) Shares() *share.Manager                  { return nil }
func (f *fakeBackend) StartShare(string, []string, bool) error { return nil }
func (f *fakeBackend) CreateWorktree(context.Context, WorktreeCreate) (WorktreeCreated, error) {
	return WorktreeCreated{}, ErrInvalidRequest
}
func (f *fakeBackend) SiteBranches(context.Context, string) (WorktreeBranches, error) {
	return WorktreeBranches{}, ErrInvalidRequest
}
func (f *fakeBackend) SetupWorktree(string, bool, string) (WorktreeStatus, error) {
	return WorktreeStatus{}, ErrNotWorktree
}
func (f *fakeBackend) SwitchWorktreeDatabase(string, string) (WorktreeStatus, error) {
	return WorktreeStatus{}, ErrNotWorktree
}
func (f *fakeBackend) WorktreeSetup(string) (*WorktreeStatus, error) { return nil, ErrNotWorktree }
func (f *fakeBackend) RemoveWorktree(context.Context, string, bool) (string, error) {
	return "", ErrNotWorktree
}

func (f *fakeBackend) Status() Status      { return Status{Sites: f.sites} }
func (f *fakeBackend) Reload() error       { return nil }
func (f *fakeBackend) Root() string        { return f.root }
func (f *fakeBackend) ReleasePHP(string)   {}
func (f *fakeBackend) Services() []Service { return []Service{} }
func (f *fakeBackend) Mutate(change func(*config.Config) error) error {
	cfg, err := config.Load(f.root)
	if err != nil {
		return err
	}
	if err := change(cfg); err != nil {
		return err
	}
	return cfg.Save(f.root)
}

func (f *fakeBackend) config(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(f.root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func serve(t *testing.T, b Backend, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewHandler(b, token))
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, method, url, token, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func TestTokenIsRequiredWhenConfigured(t *testing.T) {
	srv := serve(t, &fakeBackend{root: t.TempDir()}, "secret")

	if resp, _ := call(t, "GET", srv.URL+"/v1/status", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "GET", srv.URL+"/v1/status", "wrong", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "GET", srv.URL+"/v1/status", "secret", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("right token: %d", resp.StatusCode)
	}
}

func TestSettingsOnlyChangeGivenKeys(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")

	if resp, body := call(t, "POST", srv.URL+"/v1/settings", "", `{"dumps":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	// Xdebug detection is on by default and must stay so.
	if cfg := b.config(t); !cfg.Dumps || !cfg.Xdebug {
		t.Fatalf("got %+v", cfg)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/settings", "", `{"default_php":"5.6"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("uninstalled default PHP must be rejected, got %d", resp.StatusCode)
	}
}

func TestChannelSetting(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")
	channel := func() string {
		_, body := call(t, "GET", srv.URL+"/v1/config", "", "")
		var view ConfigView
		if err := json.Unmarshal([]byte(body), &view); err != nil {
			t.Fatal(err)
		}
		return view.Channel
	}
	if got := channel(); got != "stable" {
		t.Fatalf("default channel %q", got)
	}
	for _, c := range []struct{ set, stored, shown string }{{"beta", "beta", "beta"}, {"experimental", "experimental", "experimental"}, {"stable", "", "stable"}} {
		if resp, body := call(t, "POST", srv.URL+"/v1/settings", "", `{"channel":"`+c.set+`"}`); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", c.set, resp.StatusCode, body)
		}
		if cfg := b.config(t); cfg.Channel != c.stored || channel() != c.shown {
			t.Errorf("%s: stored %q, shown %q", c.set, cfg.Channel, channel())
		}
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/settings", "", `{"channel":"nightly"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an unknown channel must be rejected, got %d", resp.StatusCode)
	}
}

func TestMailEndpoints(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mail", "app", "1-000001.eml")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw := "From: a@app.test\r\nSubject: Hi\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/html\r\n\r\n<b>hi</b>\r\n" +
		"--b\r\nContent-Type: text/csv\r\nContent-Disposition: attachment; filename=\"r.csv\"\r\n\r\na,b\r\n--b--\r\n"
	_ = os.WriteFile(path, []byte(raw), 0o600)
	srv := serve(t, &fakeBackend{root: root}, "")

	_, list := call(t, "GET", srv.URL+"/v1/mail", "", "")
	if !strings.Contains(list, `"id":"1-000001"`) {
		t.Fatalf("list: %s", list)
	}

	_, detail := call(t, "GET", srv.URL+"/v1/mail/1-000001", "", "")
	var d struct {
		HTML        string `json:"html"`
		Attachments []struct {
			Filename string `json:"filename"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(detail), &d); err != nil || !strings.Contains(d.HTML, "<b>hi</b>") || len(d.Attachments) != 1 {
		t.Fatalf("detail: %s", detail)
	}

	resp, body := call(t, "GET", srv.URL+"/v1/mail/1-000001/attachments/0", "", "")
	if strings.TrimSpace(body) != "a,b" || !strings.Contains(resp.Header.Get("Content-Disposition"), "r.csv") {
		t.Fatalf("attachment: %q %v", body, resp.Header)
	}

	if resp, _ := call(t, "GET", srv.URL+"/v1/mail/nope", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing message: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/mail", "", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear: %d", resp.StatusCode)
	}
	if _, list := call(t, "GET", srv.URL+"/v1/mail", "", ""); strings.TrimSpace(list) != "[]" {
		t.Fatalf("after clear: %s", list)
	}
}

func TestDumpsAreAttributedToSites(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Dir(dumps.File(root)), 0o700)
	lines := `{"id":"1","time":"2026-10-13T12:00:00.000+00:00","source":{"file":"/code/shop/app/Http/Kernel.php","line":3,"name":"Kernel.php"},"text":"a","html":""}
{"id":"2","time":"2026-10-13T12:00:01.000+00:00","request":{"method":"GET","uri":"https://api.blog.test/x"},"text":"b","html":""}
{"id":"3","time":"2026-10-13T12:00:02.000+00:00","request":{"method":"GET","uri":"https://x.accounts.blog.test/y"},"text":"c","html":""}
`
	_ = os.WriteFile(dumps.File(root), []byte(lines), 0o600)
	b := &fakeBackend{root: root, sites: []SiteStatus{
		{Name: "shop", Domain: "shop.test", Path: "/code/shop"},
		{Name: "blog", Domain: "blog.test", Path: "/code/blog"},
		{Name: "accounts", Domain: "accounts.test", Path: "/code/accounts", Domains: []SiteDomain{{Entry: "accounts.blog.test", Host: "accounts.blog.test", Alias: true}}},
	}}
	srv := serve(t, b, "")

	_, body := call(t, "GET", srv.URL+"/v1/dumps", "", "")
	var entries []dumps.Entry
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Site != "accounts.test" || entries[1].Site != "blog.test" || entries[2].Site != "shop.test" {
		t.Fatalf("got %+v", entries)
	}
}

func TestCaptureSettingsAndEvents(t *testing.T) {
	root := t.TempDir()
	b := &fakeBackend{root: root, sites: []SiteStatus{{Name: "shop", Domain: "shop.test", Path: "/code/shop"}}}
	srv := serve(t, b, "")

	if resp, body := call(t, "POST", srv.URL+"/v1/settings", "", `{"capture_off":["view","query","view"],"dumps_in_page":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if cfg := b.config(t); !slices.Equal(cfg.CaptureOff, []string{"query", "view"}) || !cfg.DumpsInPage {
		t.Fatalf("got %+v", cfg)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/settings", "", `{"capture_off":["sql"]}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown kinds must be rejected, got %d", resp.StatusCode)
	}

	src := &dumps.Source{File: "/code/shop/app/Models/User.php", Line: 9}
	_ = capture.Append(root, capture.Event{Kind: "query", SQL: "select 1", Source: src}, capture.Event{Kind: "log", Message: "hi"})
	_, body := call(t, "GET", srv.URL+"/v1/events?kind=query", "", "")
	var events []capture.Event
	if err := json.Unmarshal([]byte(body), &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].SQL != "select 1" || events[0].Site != "shop.test" {
		t.Fatalf("got %s", body)
	}
	if resp, _ := call(t, "GET", srv.URL+"/v1/events?kind=nope", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown kind: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/events", "", ""); resp.StatusCode != http.StatusNoContent || capture.Count(root) != 0 {
		t.Errorf("clear: %d, %d left", resp.StatusCode, capture.Count(root))
	}
}

func TestLogsEndpoints(t *testing.T) {
	root := t.TempDir()
	site := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "log"), 0o700)
	_ = os.WriteFile(filepath.Join(root, "log", "web.log"), []byte("one\ntwo\nthree\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(site, "storage", "logs"), 0o755)
	_ = os.WriteFile(filepath.Join(site, "storage", "logs", "laravel.log"), []byte("[2026-10-13 10:00:00] local.ERROR: Boom\n"), 0o644)
	srv := serve(t, &fakeBackend{root: root, sites: []SiteStatus{{Name: "shop", Path: site}}}, "")

	if _, body := call(t, "GET", srv.URL+"/v1/logs", "", ""); !strings.Contains(body, `"services":["web"]`) || !strings.Contains(body, `"sites":["shop"]`) {
		t.Fatalf("index: %s", body)
	}
	if _, body := call(t, "GET", srv.URL+"/v1/logs/services/web?lines=2", "", ""); body != "two\nthree\n" {
		t.Fatalf("tail: %q", body)
	}
	if resp, _ := call(t, "GET", srv.URL+"/v1/logs/services/..%2Fconfig", "", ""); resp.StatusCode == http.StatusOK {
		t.Fatal("path traversal must be rejected")
	}
	if _, body := call(t, "GET", srv.URL+"/v1/logs/sites/shop", "", ""); !strings.Contains(body, `"level":"error"`) {
		t.Fatalf("site log: %s", body)
	}
}

func TestSiteAndParkedEndpoints(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")
	project := t.TempDir()

	if resp, body := call(t, "POST", srv.URL+"/v1/sites", "", `{"path":`+jsonString(project)+`,"name":"My App"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("link: %d %s", resp.StatusCode, body)
	}
	if site, ok := b.config(t).FindSite("my-app"); !ok || site.Path != project {
		t.Fatalf("site not linked: %+v", b.config(t).Sites)
	}

	if resp, body := call(t, "PATCH", srv.URL+"/v1/sites/my-app", "", `{"secured":true,"node":"20"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: %d %s", resp.StatusCode, body)
	}
	if site, _ := b.config(t).FindSite("my-app"); !site.Secured || site.Node != "20" {
		t.Fatalf("patch not applied: %+v", site)
	}
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/sites/my-app", "", `{"php":"5.6"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("uninstalled PHP must be rejected, got %d", resp.StatusCode)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/sites", "", `{"path":"relative/dir"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("relative path must be rejected, got %d", resp.StatusCode)
	}

	parent := filepath.Dir(project)
	if resp, body := call(t, "POST", srv.URL+"/v1/parked", "", `{"path":`+jsonString(parent)+`}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("park: %d %s", resp.StatusCode, body)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/sites/"+filepath.Base(t.TempDir()), "", ""); resp.StatusCode != http.StatusConflict {
		t.Fatalf("unlinking a parked-only site must be a conflict, got %d", resp.StatusCode)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/sites/my-app", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("unlink: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/parked", "", `{"path":`+jsonString(parent)+`}`); resp.StatusCode != http.StatusOK || len(b.config(t).Parked) != 0 {
		t.Fatalf("unpark: %d %v", resp.StatusCode, b.config(t).Parked)
	}
}

func TestServiceStartStopAndPorts(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")

	if resp, _ := call(t, "POST", srv.URL+"/v1/services/postgres/stop", "", ""); resp.StatusCode != http.StatusOK || b.config(t).Services.Running("postgres") {
		t.Fatalf("stop: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/services/postgres/start", "", ""); resp.StatusCode != http.StatusOK || !b.config(t).Services.Running("postgres") {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/services/web/stop", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("web must not be stoppable, got %d", resp.StatusCode)
	}
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/services/postgres", "", `{"port":5433}`); resp.StatusCode != http.StatusOK || b.config(t).Ports.Postgres != 5433 {
		t.Fatalf("port: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/services/postgres", "", `{"port":80}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("privileged port must be rejected, got %d", resp.StatusCode)
	}
}

func TestConfigView(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")

	_, body := call(t, "GET", srv.URL+"/v1/config", "", "")
	for _, want := range []string{`"tld":"test"`, `"php":[]`, `"node":[]`, `"parked":[]`, `"postgres":5432`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
}

func TestSiteDomainEndpoints(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	cfg := config.Default()
	cfg.Upsert(config.Site{Name: "app", Path: t.TempDir()})
	cfg.Upsert(config.Site{Name: "shop", Path: t.TempDir()})
	if err := cfg.Save(b.root); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, b, "")

	for _, d := range []string{"tenant1", "A.B.app.test", "app-2.test"} {
		if resp, body := call(t, "POST", srv.URL+"/v1/sites/app/domains", "", `{"domain":"`+d+`"}`); resp.StatusCode != http.StatusOK {
			t.Fatalf("add %s: %d %s", d, resp.StatusCode, body)
		}
	}
	if site, _ := b.config(t).FindSite("app"); strings.Join(site.Domains, ",") != "tenant1,a.b,app-2.test" {
		t.Fatalf("domains not stored as expected: %v", site.Domains)
	}

	for d, want := range map[string]string{"tenant1": "already", "shop.test": "shop", "a.b.c.d.e": "levels"} {
		resp, body := call(t, "POST", srv.URL+"/v1/sites/app/domains", "", `{"domain":"`+d+`"}`)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, want) {
			t.Errorf("add %s: want 400 mentioning %q, got %d %s", d, want, resp.StatusCode, body)
		}
	}

	// Removal takes the stored entry or the full name.
	if resp, body := call(t, "DELETE", srv.URL+"/v1/sites/app/domains/a.b.app.test", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("remove: %d %s", resp.StatusCode, body)
	}
	if resp, body := call(t, "DELETE", srv.URL+"/v1/sites/app/domains/tenant1", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("remove: %d %s", resp.StatusCode, body)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/sites/app/domains/tenant1", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("removing a missing domain must fail, got %d", resp.StatusCode)
	}
	if site, _ := b.config(t).FindSite("app"); strings.Join(site.Domains, ",") != "app-2.test" {
		t.Fatalf("domains after removal: %v", site.Domains)
	}
}

func TestSitesSwitchToFrankenPHPAndBack(t *testing.T) {
	root := t.TempDir()
	b := &fakeBackend{root: root, frankenphp: frankenphp.NewManager(root, binaries.NewCatalog(binaries.Source{}, func() binaries.Channel { return binaries.Stable }))}
	if !b.frankenphp.Available() {
		t.Skip("no FrankenPHP for this platform")
	}
	srv := serve(t, b, "")
	cfg := b.config(t)
	cfg.DefaultPHP = "8.5"
	cfg.Upsert(config.Site{Name: "app", Path: t.TempDir()})
	cfg.Upsert(config.Site{Name: "old", Path: t.TempDir(), PHP: "8.1"})
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	if resp, body := call(t, "PATCH", srv.URL+"/v1/sites/app", "", `{"server":"frankenphp","worker":"octane"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("to FrankenPHP: %d %s", resp.StatusCode, body)
	}
	site, _ := b.config(t).FindSite("app")
	if !site.FrankenPHP() || site.Worker != config.WorkerOctane || site.Port != config.FirstSitePort {
		t.Fatalf("site: %+v", site)
	}
	if resp, body := call(t, "PATCH", srv.URL+"/v1/sites/app", "", `{"server":"fastcgi"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("back to FastCGI: %d %s", resp.StatusCode, body)
	}
	if site, _ := b.config(t).FindSite("app"); site.FrankenPHP() || site.Worker != config.WorkerOctane {
		t.Fatalf("site: %+v", site)
	}

	if resp, body := call(t, "PATCH", srv.URL+"/v1/sites/old", "", `{"server":"frankenphp"}`); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "8.2 or newer") {
		t.Fatalf("PHP 8.1: %d %s", resp.StatusCode, body)
	}
	if resp, body := call(t, "PATCH", srv.URL+"/v1/sites/app", "", `{"server":"swoole"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown server: %d %s", resp.StatusCode, body)
	}
}

func TestSitesTurnReverbOnAndOff(t *testing.T) {
	root := t.TempDir()
	b := &fakeBackend{root: root}
	srv := serve(t, b, "")
	app, plain := t.TempDir(), t.TempDir()
	for _, file := range []string{filepath.Join(app, "artisan"), filepath.Join(app, "vendor", "laravel", "reverb", "composer.json")} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := b.config(t)
	cfg.Upsert(config.Site{Name: "app", Path: app})
	cfg.Upsert(config.Site{Name: "plain", Path: plain})
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	if resp, body := call(t, "PATCH", srv.URL+"/v1/sites/app", "", `{"reverb":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("on: %d %s", resp.StatusCode, body)
	}
	if site, _ := b.config(t).FindSite("app"); !site.Reverb || site.ReverbPort != config.FirstSitePort {
		t.Fatalf("site: %+v", site)
	}
	if resp, body := call(t, "PATCH", srv.URL+"/v1/sites/app", "", `{"reverb":false}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("off: %d %s", resp.StatusCode, body)
	}
	if site, _ := b.config(t).FindSite("app"); site.Reverb || site.ReverbPort != config.FirstSitePort {
		t.Fatalf("site: %+v", site)
	}
	if resp, body := call(t, "PATCH", srv.URL+"/v1/sites/plain", "", `{"reverb":true}`); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "install:broadcasting --reverb") {
		t.Fatalf("without Reverb: %d %s", resp.StatusCode, body)
	}
}

// jsonString quotes s for a JSON body (Windows paths have backslashes).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestSiteExtensionsAndRequiringOne(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "composer.json"), []byte(`{"require": {"ext-intl": "*"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, &fakeBackend{root: root, sites: []SiteStatus{{Name: "shop", Path: project, PHP: "8.4"}}}, "")

	var report phpext.Report
	resp, body := call(t, "GET", srv.URL+"/v1/sites/shop/extensions", "", "")
	if err := json.Unmarshal([]byte(body), &report); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if len(report.Extensions) != 1 || report.Extensions[0].Name != "intl" || report.Extensions[0].Available {
		t.Fatalf("%+v (PHP 8.4 isn't installed, so nothing is available)", report)
	}

	saved := composerRequire
	t.Cleanup(func() { composerRequire = saved })
	var required string
	composerRequire = func(_ context.Context, _, minor, dir, name string) error {
		required = minor + " " + dir + " " + name
		return nil
	}
	if resp, body := call(t, "POST", srv.URL+"/v1/sites/shop/extensions/ext-PDO_pgsql/require", "", ""); resp.StatusCode != http.StatusOK || required != "8.4 "+project+" pdo_pgsql" {
		t.Fatalf("required %q: %d %s", required, resp.StatusCode, body)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/sites/shop/extensions/intl%3Brm/require", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an invalid name must be refused: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "GET", srv.URL+"/v1/sites/nope/extensions", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown site: %d", resp.StatusCode)
	}
}

func TestSiteDocumentRootEndpoints(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "src", "www"), 0o755); err != nil {
		t.Fatal(err)
	}
	if resp, body := call(t, "POST", srv.URL+"/v1/sites", "", `{"path":`+jsonString(project)+`,"name":"zoo"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("link: %d %s", resp.StatusCode, body)
	}

	// On this machine: relative to the project, inside it, a folder.
	if resp, body := call(t, "PATCH", srv.URL+"/v1/sites/zoo", "", `{"root":"./src/www/"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("set root: %d %s", resp.StatusCode, body)
	}
	if site, _ := b.config(t).FindSite("zoo"); site.Root != "src/www" {
		t.Fatalf("root not stored normalized: %+v", site)
	}
	for _, bad := range []string{"../elsewhere", "/etc", "missing"} {
		if resp, _ := call(t, "PATCH", srv.URL+"/v1/sites/zoo", "", `{"root":`+jsonString(bad)+`}`); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("root %q must be rejected, got %d", bad, resp.StatusCode)
		}
	}

	// Pinning writes the root in use into .flockrc and lifts this machine's.
	if resp, body := call(t, "POST", srv.URL+"/v1/sites/zoo/pin", "", `{"tool":"root"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("pin root: %d %s", resp.StatusCode, body)
	}
	if data, _ := os.ReadFile(filepath.Join(project, ".flockrc")); string(data) != "root=src/www\n" {
		t.Fatalf(".flockrc: %q", data)
	}
	if site, _ := b.config(t).FindSite("zoo"); site.Root != "" {
		t.Fatalf("the machine's root must be lifted: %+v", site)
	}

	if resp, body := call(t, "DELETE", srv.URL+"/v1/sites/zoo/pin/root", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("unpin root: %d %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(project, ".flockrc")); !os.IsNotExist(err) {
		t.Fatalf("an empty .flockrc must be removed: %v", err)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/sites/zoo/pin/root", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unpinning nothing must be rejected, got %d", resp.StatusCode)
	}

	// An empty root lets the project decide again.
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/sites/zoo", "", `{"root":"src/www"}`); resp.StatusCode != http.StatusOK {
		t.Fatal("set root again")
	}
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/sites/zoo", "", `{"root":""}`); resp.StatusCode != http.StatusOK {
		t.Fatal("clear root")
	}
	if site, _ := b.config(t).FindSite("zoo"); site.Root != "" {
		t.Fatalf("root not cleared: %+v", site)
	}
}

func TestProxyEndpoints(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")

	if resp, body := call(t, "POST", srv.URL+"/v1/proxies", "", `{"name":"Soft Pack","upstream":"3000"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy: %d %s", resp.StatusCode, body)
	}
	if p, ok := b.config(t).FindProxy("soft-pack"); !ok || p.Upstream != "http://127.0.0.1:3000" || p.Secured {
		t.Fatalf("proxy not stored: %+v", b.config(t).Proxies)
	}

	if resp, body := call(t, "PATCH", srv.URL+"/v1/proxies/soft-pack", "", `{"secured":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("secure: %d %s", resp.StatusCode, body)
	}
	// A new upstream keeps the proxy secured.
	if resp, body := call(t, "POST", srv.URL+"/v1/proxies", "", `{"name":"soft-pack","upstream":"localhost:3001"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("re-proxy: %d %s", resp.StatusCode, body)
	}
	if p, _ := b.config(t).FindProxy("soft-pack"); p.Upstream != "http://localhost:3001" || !p.Secured {
		t.Fatalf("re-proxy: %+v", p)
	}

	for _, bad := range []string{`{"name":"x","upstream":"ftp://h:1"}`, `{"name":"x","upstream":""}`, `{"name":"","upstream":"3000"}`} {
		if resp, _ := call(t, "POST", srv.URL+"/v1/proxies", "", bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s must be rejected, got %d", bad, resp.StatusCode)
		}
	}
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/proxies/missing", "", `{"secured":true}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("patching a missing proxy, got %d", resp.StatusCode)
	}

	if resp, _ := call(t, "DELETE", srv.URL+"/v1/proxies/soft-pack", "", ""); resp.StatusCode != http.StatusOK || len(b.config(t).Proxies) != 0 {
		t.Fatalf("unproxy: %d %+v", resp.StatusCode, b.config(t).Proxies)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/proxies/soft-pack", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unproxying twice, got %d", resp.StatusCode)
	}
}

func TestProxyCommandEndpoints(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")
	dir := t.TempDir()
	dirJSON, _ := json.Marshal(dir)

	body := `{"name":"app","upstream":"3000","command":"npm run dev","dir":` + string(dirJSON) + `}`
	if resp, out := call(t, "POST", srv.URL+"/v1/proxies", "", body); resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy with a command: %d %s", resp.StatusCode, out)
	}
	if p, _ := b.config(t).FindProxy("app"); p.Command != "npm run dev" || p.Dir != dir || p.Background() {
		t.Fatalf("command not stored: %+v", p)
	}

	// A new upstream keeps the command; a stop command makes it a background one.
	if resp, out := call(t, "POST", srv.URL+"/v1/proxies", "", `{"name":"app","upstream":"3001"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("re-proxy: %d %s", resp.StatusCode, out)
	}
	if resp, out := call(t, "PATCH", srv.URL+"/v1/proxies/app", "", `{"command":"docker compose up -d","stop_command":"docker compose stop"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("stop command: %d %s", resp.StatusCode, out)
	}
	if p, _ := b.config(t).FindProxy("app"); p.Upstream != "http://127.0.0.1:3001" || p.Command != "docker compose up -d" || !p.Background() || p.Dir != dir {
		t.Fatalf("after the changes: %+v", p)
	}

	for _, bad := range []string{`{"dir":"/no/such/folder"}`, `{"command":"a\nb"}`, `{"stop_command":"x\ny"}`} {
		if resp, _ := call(t, "PATCH", srv.URL+"/v1/proxies/app", "", bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s must be rejected, got %d", bad, resp.StatusCode)
		}
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/proxies", "", `{"name":"web","upstream":"4000","command":"npm run dev"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a command without its folder must be rejected, got %d", resp.StatusCode)
	}

	// An empty command removes it, with its folder and stop command.
	if resp, out := call(t, "PATCH", srv.URL+"/v1/proxies/app", "", `{"command":""}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("remove the command: %d %s", resp.StatusCode, out)
	}
	if p, _ := b.config(t).FindProxy("app"); p.Command != "" || p.Dir != "" || p.StopCommand != "" {
		t.Fatalf("command not removed: %+v", p)
	}

	for _, action := range []string{"start", "stop"} {
		if resp, out := call(t, "POST", srv.URL+"/v1/proxies/app/"+action, "", ""); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", action, resp.StatusCode, out)
		}
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/proxies/missing/start", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("starting a missing proxy, got %d", resp.StatusCode)
	}
	if strings.Join(b.proxyCalls, ", ") != "start app, stop app" {
		t.Fatalf("calls: %v", b.proxyCalls)
	}
}

func TestSwitchingASitesType(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	cfg := config.Default()
	cfg.Upsert(config.Site{Name: "app", Path: t.TempDir()})
	if err := cfg.Save(b.root); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, b, "")

	if resp, body := call(t, "PATCH", srv.URL+"/v1/sites/app", "", `{"type":"node"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("to node: %d %s", resp.StatusCode, body)
	}
	if s, _ := b.config(t).FindSite("app"); s.Type != config.TypeNode {
		t.Fatalf("type: %q", s.Type)
	}
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/sites/app", "", `{"server":"frankenphp"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a Node.js site got a PHP server: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/sites/app", "", `{"type":"ruby"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an unknown type was taken: %d", resp.StatusCode)
	}
}

func TestClearingAServiceLog(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")
	log := filepath.Join(b.root, "log", "proxy-app.log")
	if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, []byte("ready on :3000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if resp, _ := call(t, "DELETE", srv.URL+"/v1/logs/services/proxy-app", "", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear: %d", resp.StatusCode)
	}
	if data, _ := os.ReadFile(log); len(data) != 0 {
		t.Fatalf("not cleared: %q", data)
	}
	// A log that doesn't exist is empty already.
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/logs/services/proxy-gone", "", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear a missing log: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/logs/services/..%2Fconfig.json", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a path must be refused, got %d", resp.StatusCode)
	}
}
