package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/binaries"
	"flock/internal/config"
	"flock/internal/paths"
	"flock/internal/web"
	"flock/internal/worktree"
)

// TestMain keeps the daemons offline: without the package manifest, the first run doesn't
// download PHP and Node.js, and versions count as offered.
func TestMain(m *testing.M) {
	binaries.Bases = nil
	os.Exit(m.Run())
}

// shortTempDir keeps the socket path under macOS's ~104 byte AF_UNIX limit,
// which t.TempDir() under /var/folders can exceed.
func shortTempDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "flk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return dir
}

// freePort returns a port that's free for TCP and UDP: Caddy also listens for HTTP/3 over
// UDP, and Windows can reserve a port for UDP that's free for TCP.
func freePort(t *testing.T) int {
	t.Helper()

	for range 20 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		pc, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(port))
		ln.Close()
		if err == nil {
			pc.Close()

			return port
		}
	}
	t.Fatal("no port is free for both TCP and UDP")

	return 0
}

// testRoot writes a config.json that only uses free high ports.
func testRoot(t *testing.T, mutate func(*config.Config)) (string, *config.Config) {
	t.Helper()

	root := shortTempDir(t)
	cfg := config.Default()
	cfg.DefaultPHP, cfg.DefaultNode = "8.5", "22" // as after the first run
	cfg.Ports = config.Ports{HTTP: freePort(t), HTTPS: freePort(t), DNS: freePort(t), SMTP: freePort(t), Postgres: freePort(t)}
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	return root, cfg
}

func startDaemon(t *testing.T, root string) *api.Client {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Config{Root: root, Version: "dev"}) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	client := api.NewClient(paths.Socket(root))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := client.Status(context.Background()); err == nil {
			return client
		}
		select {
		case err := <-done:
			t.Fatalf("daemon exited: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStatusReportsRunningServices(t *testing.T) {
	root, cfg := testRoot(t, nil)
	client := startDaemon(t, root)

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if status.PID != os.Getpid() || status.Version != "dev" || status.DNS.TLD != "test" || status.Mail == "" || status.HTTPPort != cfg.Ports.HTTP {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestStatusListsValkeyOnce(t *testing.T) {
	root, _ := testRoot(t, func(c *config.Config) {
		c.Services.SetExtra("valkey", config.Extra{Version: "9.1.2", Port: freePort(t)})
	})
	client := startDaemon(t, root)

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}

	var listed struct {
		Valkey string `json:"valkey"`
		Extras []struct {
			ID string `json:"id"`
		} `json:"extras"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}

	count := 0
	if listed.Valkey != "" {
		count++
	}
	for _, x := range listed.Extras {
		if x.ID == "valkey" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("valkey listed %d times: %s", count, body)
	}
}

func TestUnknownHostsGetTheCatchAllPage(t *testing.T) {
	root, cfg := testRoot(t, nil)
	startDaemon(t, root)

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/", nil)
	req.Host = "nothing.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "nothing.test") {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
}

func TestReloadReportsSitesWithMissingPHP(t *testing.T) {
	root, cfg := testRoot(t, nil)
	client := startDaemon(t, root)

	cfg.Upsert(config.Site{Name: "app", Path: t.TempDir(), PHP: "7.0"})
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	status, err := client.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Sites) != 1 || status.Sites[0].Problem != "PHP 7.0 is not installed" {
		t.Fatalf("unexpected sites: %+v", status.Sites)
	}
}

func TestTheFirstRunWaitsForTheDefaultsAndSaysSo(t *testing.T) {
	root, cfg := testRoot(t, func(cfg *config.Config) { cfg.DefaultPHP, cfg.DefaultNode = "", "" })
	cfg.Upsert(config.Site{Name: "app", Path: t.TempDir()})
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	client := startDaemon(t, root)

	status, err := client.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Offline there is no manifest yet to pick the versions from.
	if d := status.Defaults; d == nil || d.PHP.State != api.DefaultWaiting || d.Node.State != api.DefaultWaiting {
		t.Fatalf("defaults: %+v", status.Defaults)
	}
	if len(status.Sites) != 1 || status.Sites[0].Problem != "PHP is being installed" {
		t.Fatalf("sites: %+v", status.Sites)
	}

	// The site's name gets the page that says PHP is coming, not "not linked". The first run's
	// background work reloads the web server meanwhile, which can drop a connection.
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/", nil)
	req.Host = "app.test"
	var resp *http.Response
	deadline := time.Now().Add(5 * time.Second)
	for resp, err = http.DefaultClient.Do(req); err != nil; resp, err = http.DefaultClient.Do(req) {
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Installing PHP") || strings.Contains(string(body), "Not linked") {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
}

func TestRefusesToStartTwice(t *testing.T) {
	root, _ := testRoot(t, nil)
	startDaemon(t, root)

	err := Run(context.Background(), Config{Root: root})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("want ErrAlreadyRunning, got %v", err)
	}
}

func TestRemovesStaleSocket(t *testing.T) {
	root, _ := testRoot(t, nil)
	if err := os.WriteFile(paths.Socket(root), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	startDaemon(t, root)
}

func TestClientReportsDaemonNotRunning(t *testing.T) {
	client := api.NewClient(paths.Socket(shortTempDir(t)))

	if _, err := client.Status(context.Background()); !errors.Is(err, api.ErrDaemonNotRunning) {
		t.Fatalf("want ErrDaemonNotRunning, got %v", err)
	}
}

func TestBusyPortsBecomeWarningsAndRecover(t *testing.T) {
	retryInterval = 100 * time.Millisecond
	t.Cleanup(func() { retryInterval = 15 * time.Second })

	root, cfg := testRoot(t, nil)
	blocker, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP))
	if err != nil {
		t.Fatal(err)
	}

	client := startDaemon(t, root)
	status, _ := client.Status(context.Background())
	if !containsText(status.Warnings, "web: port "+strconv.Itoa(cfg.Ports.HTTP)+" is used by") {
		t.Fatalf("busy web port not reported: %v", status.Warnings)
	}

	blocker.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ = client.Status(context.Background())
		// Only the web server's warning matters: on Windows there is also one about the hosts helper.
		if !containsText(status.Warnings, "web:") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("web server did not recover: %v", status.Warnings)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestReloadMovesServicesToNewPorts(t *testing.T) {
	root, cfg := testRoot(t, nil)
	client := startDaemon(t, root)

	cfg.Ports.SMTP = freePort(t)
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	status, err := client.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Mail != "127.0.0.1:"+strconv.Itoa(cfg.Ports.SMTP) {
		t.Fatalf("mail did not move: %s (warnings %v)", status.Mail, status.Warnings)
	}
}

func TestFoldersInParkedPathsAreFollowed(t *testing.T) {
	projectWatchInterval = 50 * time.Millisecond
	t.Cleanup(func() { projectWatchInterval = 3 * time.Second })

	parked := t.TempDir()
	root, _ := testRoot(t, func(cfg *config.Config) { cfg.Parked = []string{parked} })
	client := startDaemon(t, root)

	hasSite := func(name string) bool {
		status, _ := client.Status(context.Background())
		for _, s := range status.Sites {
			if s.Name == name {
				return true
			}
		}
		return false
	}
	waitFor := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for hasSite("acme") != want {
			if time.Now().After(deadline) {
				t.Fatalf("acme listed = %v, want %v", !want, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	if err := os.Mkdir(filepath.Join(parked, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	waitFor(true)

	if err := os.Remove(filepath.Join(parked, "acme")); err != nil {
		t.Fatal(err)
	}
	waitFor(false)
}

func TestProjectFilesFollowsTheConfiguredCaddyfile(t *testing.T) {
	site := t.TempDir()
	d := daemon{cfg: &config.Config{Sites: []config.Site{{Name: "app", Path: site}}}}
	before := d.projectFiles()

	// A project's own Caddyfile isn't Flock's.
	writeFile(t, filepath.Join(site, "Caddyfile"), "example.com {\n}\n")
	if d.projectFiles() != before {
		t.Fatal("a Caddyfile nobody configured changed the project fingerprint")
	}

	writeFile(t, filepath.Join(site, "flock.yml"), "caddyfile: ops/local.caddy\n")
	named := d.projectFiles()
	if named == before {
		t.Fatal("flock.yml did not change the project fingerprint")
	}
	configured := filepath.Join(site, "ops", "local.caddy")
	if err := os.MkdirAll(filepath.Dir(configured), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, configured, "header X-Configured yes\n")
	if created := d.projectFiles(); created == named || !strings.Contains(created, configured) {
		t.Fatalf("creating the configured file did not change the project fingerprint: %q", created)
	}
}

func TestSiteHandlersProblemsLeaveTheSiteServedWithoutThem(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	site := config.Site{Name: "app", Path: dir}
	ws := web.Site{Domain: "app.test", Root: dir, FPM: []string{"127.0.0.1:1"}}

	if handlers, problem := siteHandlers(cfg, site, ws); handlers != "" || problem != "" {
		t.Fatalf("without settings: got %q, %q", handlers, problem)
	}

	writeFile(t, filepath.Join(dir, "flock.yml"), "caddyfile: local.caddy\n")
	if _, problem := siteHandlers(cfg, site, ws); !strings.Contains(problem, "doesn't exist") {
		t.Fatalf("missing file: got %q", problem)
	}

	writeFile(t, filepath.Join(dir, "local.caddy"), "header X-A b\n}\n:9999 {\n")
	if handlers, problem := siteHandlers(cfg, site, ws); handlers != "" || !strings.Contains(problem, "local.caddy:2") {
		t.Fatalf("breakout: got %q, %q", handlers, problem)
	}

	writeFile(t, filepath.Join(dir, "local.caddy"), "header X-A b\n")
	if handlers, problem := siteHandlers(cfg, site, ws); handlers != "header X-A b\n" || problem != "" {
		t.Fatalf("valid file: got %q, %q", handlers, problem)
	}
}

func TestGitWorktreesAreServedUnderTheirSite(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	projectWatchInterval = 50 * time.Millisecond
	t.Cleanup(func() { projectWatchInterval = 3 * time.Second })

	parked := t.TempDir()
	repo := filepath.Join(parked, "acme")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Flock", "-c", "user.email=flock@example.test"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	writeFile(t, filepath.Join(repo, ".gitignore"), ".env\nhooked.txt\n")
	writeFile(t, filepath.Join(repo, "flock.yml"), "worktrees:\n  hooks:\n    after_create:\n      - echo done> hooked.txt\n")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	writeFile(t, filepath.Join(repo, ".env"), "APP_URL=http://acme.test\n")
	// Made before Flock looked for worktrees: served, but left as it is.
	git("worktree", "add", "-q", "-b", "old", filepath.Join(parked, "acme-old"))

	root, _ := testRoot(t, func(cfg *config.Config) { cfg.Parked = []string{parked} })
	client := startDaemon(t, root)

	sites := func() map[string]api.SiteStatus {
		status, _ := client.Status(context.Background())
		out := map[string]api.SiteStatus{}
		for _, s := range status.Sites {
			out[s.Name] = s
		}
		return out
	}
	waitFor := func(name string, want bool) api.SiteStatus {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			site, ok := sites()[name]
			if ok == want {
				return site
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s listed = %v, want %v: %v", name, ok, want, sites())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	waitFor("old.acme", true)

	// Next to the site, in the parked folder: the worktree, not a site acme-auth.test.
	path := filepath.Join(parked, "acme-auth")
	git("worktree", "add", "-q", "-b", "feature/auth", path)
	wt := waitFor("feature-auth.acme", true)
	if wt.Domain != "feature-auth.acme.test" || wt.Parent != "acme" || wt.Branch != "feature/auth" {
		t.Errorf("unexpected worktree: %+v", wt)
	}
	if _, twice := sites()["acme-auth"]; twice {
		t.Error("the worktree is also served as a parked site")
	}

	// It is set up by itself: .env from the site, pointed at the worktree, then the hook.
	deadline := time.Now().Add(10 * time.Second)
	for st := sites()["feature-auth.acme"].Worktree; st == nil || st.State != worktree.Ready; st = sites()["feature-auth.acme"].Worktree {
		if st != nil && st.State == worktree.Failed || time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(root, "log", "worktree-feature-auth.acme.log"))
			t.Fatalf("setup: %+v\n%s", st, log)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if env, _ := os.ReadFile(filepath.Join(path, ".env")); string(env) != "APP_URL=http://feature-auth.acme.test\n" {
		t.Errorf(".env: %q", env)
	}
	if hooked, err := os.ReadFile(filepath.Join(path, "hooked.txt")); err != nil || strings.TrimSpace(string(hooked)) != "done" {
		t.Errorf("the after_create hook didn't run: %q %v", hooked, err)
	}
	if st := sites()["old.acme"].Worktree; st != nil {
		t.Errorf("a worktree older than Flock's worktree support was set up: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(parked, "acme-old", ".env")); err == nil {
		t.Error("a worktree older than Flock's worktree support got a .env")
	}

	// Removed outside Flock (an agent app, git): forgotten, with what Flock made for it.
	other := filepath.Join(t.TempDir(), "acme-other")
	git("worktree", "add", "-q", "-b", "other", other)
	waitFor("other.acme", true)
	known := func() bool {
		cfg, err := config.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		_, ok := cfg.WorktreeEntry(other)
		return ok
	}
	deadline = time.Now().Add(10 * time.Second)
	for st := sites()["other.acme"].Worktree; st == nil || st.State != worktree.Ready || !known(); st = sites()["other.acme"].Worktree {
		if time.Now().After(deadline) {
			t.Fatalf("other's setup: %+v, recorded %v", st, known())
		}
		time.Sleep(20 * time.Millisecond)
	}
	git("worktree", "remove", "--force", other)
	waitFor("other.acme", false)
	for deadline = time.Now().Add(5 * time.Second); known(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a worktree removed outside Flock is still recorded")
		}
	}

	if _, err := client.RemoveWorktree(context.Background(), path, true); err != nil {
		t.Fatal(err)
	}
	waitFor("feature-auth.acme", false)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the worktree's folder is still there: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsText(list []string, needle string) bool {
	for _, s := range list {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// An unlinked site's worktrees stop being served: their records go, and so do the databases Flock
// made for them, as when the site was unlinked while the daemon was down and its worktree is gone.
func TestUnlinkingASiteForgetsItsWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := filepath.Join(t.TempDir(), "acme")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Flock", "-c", "user.email=flock@example.test"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "init")
	tmp, err := filepath.EvalSymlinks(t.TempDir()) // as git lists it (macOS: /private/var)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmp, "acme-auth")
	git("worktree", "add", "-q", "-b", "auth", path)
	db := filepath.Join(path, "database.sqlite")
	writeFile(t, db, "worktree data")

	gone := filepath.Join(t.TempDir(), "gone")
	goneDB := filepath.Join(t.TempDir(), "gone.sqlite")
	writeFile(t, goneDB, "worktree data")

	root, _ := testRoot(t, func(cfg *config.Config) {
		cfg.Sites = []config.Site{{Name: "acme", Path: repo}}
		cfg.Worktrees = []config.WorktreeEntry{
			{Path: path, Site: "acme", Slug: "auth", SetUp: true, Database: db, Engine: "sqlite"},
			{Path: gone, Site: "unlinked", Slug: "x", SetUp: true, Database: goneDB, Engine: "sqlite"},
		}
	})
	client := startDaemon(t, root)

	recorded := func(path string) bool {
		cfg, err := config.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		_, ok := cfg.WorktreeEntry(path)
		return ok
	}
	waitForgotten := func(path, db string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); recorded(path); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				status, _ := client.Status(context.Background())
				t.Fatalf("%s is still recorded: %v", path, status.Warnings)
			}
		}
		if _, err := os.Stat(db); !os.IsNotExist(err) {
			t.Errorf("the database %s is still there: %v", db, err)
		}
	}

	// Its site was unlinked before: forgotten all the same, not warned about on every reload.
	waitForgotten(gone, goneDB)
	if !recorded(path) {
		t.Fatal("the worktree of a linked site was forgotten")
	}

	// As `flock unlink acme` does it.
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Remove("acme")
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForgotten(path, db)
}
