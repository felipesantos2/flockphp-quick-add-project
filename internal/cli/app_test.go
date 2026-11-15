package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"flock/internal/api"
	"flock/internal/catalog"
	"flock/internal/config"
	"flock/internal/paths"
)

func appTestEnv(t *testing.T) (root, dir string) {
	t.Helper()
	root, dir = t.TempDir(), t.TempDir()
	t.Setenv("FLOCK_HOME", root)
	t.Chdir(dir)
	return root, dir
}

func runAppCommand(args ...string) (string, error) {
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"app", "create"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func tarGzArchive(t *testing.T, prefix string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: prefix + name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func startFakeAppDaemon(t *testing.T, root string, services []api.Service, dbFail bool) (*atomic.Int32, string) {
	t.Helper()
	var reloads atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/services":
			_ = json.NewEncoder(w).Encode(services)
		case "POST /v1/reload":
			reloads.Add(1)
			_ = json.NewEncoder(w).Encode(api.Status{})
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewUnstartedServer(handler)
	server.Listener.Close()
	listener, err := net.Listen("unix", paths.Socket(root))
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	argsFile := filepath.Join(root, "db.args")
	t.Setenv("FLOCK_TEST_DB_ARGS", argsFile)
	for _, svc := range services {
		client := filepath.Join(catalog.Dir(root, svc.ID, svc.Version), "bin", svc.ID)
		if err := os.MkdirAll(filepath.Dir(client), 0o755); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FLOCK_TEST_DB_ARGS\"\n"
		if dbFail {
			script += "echo 'database denied' >&2\nexit 1\n"
		}
		if err := os.WriteFile(client, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &reloads, argsFile
}

func TestAppCreate(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "default", args: []string{"My App"}, want: "http://my-app.local"},
		{name: "explicit-php", args: []string{"My App", "--type", "php", "--php", "8.3", "--secure"}, want: "https://my-app.local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := appTestEnv(t)
			cfg := config.Default()
			cfg.TLD, cfg.DefaultPHP = "local", "8.4"
			if err := cfg.Save(root); err != nil {
				t.Fatal(err)
			}
			out, err := runAppCommand(tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			path, err := filepath.Abs("My App")
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(path, "index.php"))
			if err != nil || !strings.HasPrefix(string(data), "<?php\n") || !strings.Contains(string(data), "echo 'Hello from Flock!';") {
				t.Fatalf("unexpected PHP entrypoint (%v): %q", err, data)
			}
			saved, err := config.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			site, ok := saved.FindSite("my-app")
			if !ok || site.Parked || site.Path != path {
				t.Fatalf("unexpected site: %+v", site)
			}
			if tc.name == "explicit-php" && (site.PHP != "8.3" || !site.Secured) {
				t.Fatalf("expected isolated PHP 8.3 and secured=true, got %+v", site)
			}
			if !strings.Contains(out, "created "+path) || !strings.Contains(out, tc.want) || !strings.Contains(out, "daemon is not running") {
				t.Fatalf("unexpected output: %s", out)
			}
		})
	}
}

func TestAppCreateBlank(t *testing.T) {
	_, dir := appTestEnv(t)
	out, err := runAppCommand("empty-site", "--type", "blank")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "empty-site")
	if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
		t.Fatalf("expected empty directory: %v, %v", entries, err)
	}
	if !strings.Contains(out, "created "+path) || !strings.Contains(out, "has no index.php or index.html") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestAppCreateFromAppsConfURLAndLocalArchive(t *testing.T) {
	archiveBytes := tarGzArchive(t, "wordpress/", map[string]string{
		"index.php":            "<?php // wp",
		"wp-config-sample.php": "<?php",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.tar.gz":
			_, _ = w.Write(archiveBytes)
		case "/broken.tar.gz":
			http.Error(w, "unavailable", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	t.Run("custom-url-override", func(t *testing.T) {
		root, dir := appTestEnv(t)
		conf := "# custom templates\n---\nwordpress=" + srv.URL + "/latest.tar.gz\ncustom=" + srv.URL + "/latest.tar.gz\n"
		if err := os.WriteFile(filepath.Join(root, "apps.conf"), []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := runAppCommand("My Blog", "--type", "custom")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "My Blog")
		if data, err := os.ReadFile(filepath.Join(path, "index.php")); err != nil || string(data) != "<?php // wp" {
			t.Fatalf("unexpected extracted index.php: %q, %v", data, err)
		}
		if _, err := os.Stat(filepath.Join(path, "wordpress")); !os.IsNotExist(err) {
			t.Fatalf("expected top-level archive folder to be flattened: %v", err)
		}
		if !strings.Contains(out, "http://my-blog.test") {
			t.Fatalf("unexpected output: %s", out)
		}
	})

	t.Run("download-failure-leaves-no-directory", func(t *testing.T) {
		root, dir := appTestEnv(t)
		if err := os.WriteFile(filepath.Join(root, "apps.conf"), []byte("wordpress="+srv.URL+"/broken.tar.gz\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := runAppCommand("broken-blog", "--type", "wordpress"); err == nil {
			t.Fatal("expected download error")
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Fatalf("failed download left files behind: %v, %v", entries, err)
		}
	})

	t.Run("local-archive", func(t *testing.T) {
		root, dir := appTestEnv(t)
		localArchive := filepath.Join(root, "starter.tar.gz")
		if err := os.WriteFile(localArchive, archiveBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "apps.conf"), []byte("starter="+localArchive+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := runAppCommand("local-site", "--type", "starter"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "local-site", "index.php")); err != nil {
			t.Fatalf("expected extracted index.php: %v", err)
		}
	})
}

func TestAppCreateTemplateRequiresDB(t *testing.T) {
	root, dir := appTestEnv(t)
	if _, err := runAppCommand("My Blog", "--type", "wordpress"); err == nil {
		t.Fatal("expected built-in wordpress template to require a running database")
	}
	for _, p := range []string{root, dir} {
		if entries, err := os.ReadDir(p); err != nil || len(entries) != 0 {
			t.Fatalf("prerequisite failure modified %s: %v, %v", p, entries, err)
		}
	}
}

func TestAppCreateRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{}, {"one", "two"}, {""}, {"."}, {".."}, {"../escape"}, {"nested/app"},
		{`nested\app`}, {"C:app"}, {" app"}, {"app."}, {"---"}, {strings.Repeat("a", 64)},
		{"demo", "--type", "laravel"}, {"demo", "--db-service", "redis"}, {"demo", "--db-service", "postgres"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			root, dir := appTestEnv(t)
			if _, err := runAppCommand(args...); err == nil {
				t.Fatal("expected invalid input to fail")
			}
			for _, p := range []string{root, dir} {
				if entries, err := os.ReadDir(p); err != nil || len(entries) != 0 {
					t.Fatalf("invalid input modified %s: %v, %v", p, entries, err)
				}
			}
		})
	}
}

func TestAppCreatePreservesExistingDestination(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			root, _ := appTestEnv(t)
			target := "demo"
			if directory {
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(target, "index.php")
			}
			if err := os.WriteFile(target, []byte("keep me"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := runAppCommand("demo"); err == nil {
				t.Fatal("expected existing destination to fail")
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != "keep me" {
				t.Fatalf("existing content changed: %q, %v", data, err)
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatalf("config was written: %v, %v", entries, err)
			}
		})
	}
}

func TestAppCreatePreservesNameConflicts(t *testing.T) {
	for _, kind := range []string{"linked", "parked", "proxy"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := appTestEnv(t)
			existing := t.TempDir()
			cfg := config.Default()
			switch kind {
			case "linked":
				cfg.Sites = []config.Site{{Name: "my-app", Path: existing}}
			case "parked":
				cfg.Parked = []string{existing}
				if err := os.Mkdir(filepath.Join(existing, "my-app"), 0o755); err != nil {
					t.Fatal(err)
				}
			case "proxy":
				cfg.Proxies = []config.Proxy{{Name: "my-app", Upstream: "http://localhost:3000"}}
			}
			if err := cfg.Save(root); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(root, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runAppCommand("My App"); err == nil {
				t.Fatal("expected name conflict to fail")
			}
			if _, err := os.Stat("My App"); !os.IsNotExist(err) {
				t.Fatalf("destination should not be created: %v", err)
			}
			if after, err := os.ReadFile(filepath.Join(root, "config.json")); err != nil || !bytes.Equal(before, after) {
				t.Fatalf("existing config changed: %v", err)
			}
		})
	}
}

func TestAppCreateReportsSaveFailureAndKeepsProject(t *testing.T) {
	root, dir := appTestEnv(t)
	if err := os.Mkdir(filepath.Join(root, "config.json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := runAppCommand("demo")
	if err == nil || !strings.Contains(err.Error(), "applying site settings failed") {
		t.Fatalf("expected save failure, got %v", err)
	}
	if strings.Contains(out, "created "+filepath.Join(dir, "demo")+" →") {
		t.Fatalf("reported success after save failure: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo", "index.php")); err != nil {
		t.Fatalf("generated project was not kept: %v", err)
	}
}

func TestAppCreateWithDatabase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the MySQL client")
	}
	mysqlRunning := api.Service{ID: "mysql", Name: "MySQL", Installed: true, Running: true, Version: "8.4.11", Port: 3307}
	mysqlStopped := api.Service{ID: "mysql", Name: "MySQL", Installed: true, Running: false, Version: "8.4.11", Port: 3306}
	mariadbRunning := api.Service{ID: "mariadb", Name: "MariaDB", Installed: true, Running: true, Version: "11.4.2", Port: 3308}
	postgresRunning := api.Service{ID: "postgres", Name: "PostgreSQL", Installed: true, Running: true, Version: "17", Port: 5432}

	for _, tc := range []struct {
		name     string
		args     []string
		noDaemon bool
		appsConf string
		services []api.Service
		dbFail   bool
	}{
		{name: "php-skips-db-even-when-running", args: []string{"My Blog"}, services: []api.Service{mysqlRunning}},
		{name: "blank-skips-db-even-when-running", args: []string{"My Blog", "--type", "blank"}, services: []api.Service{mysqlRunning}},
		{name: "template-requires-db-creates-mysql", args: []string{"My Blog", "--type", "blog"}, appsConf: "blog=index.php | db\n", services: []api.Service{mysqlRunning}},
		{name: "template-requires-db-falls-back-to-mariadb", args: []string{"My Blog", "--type", "blog"}, appsConf: "blog=index.php | db\n", services: []api.Service{mysqlStopped, mariadbRunning}},
		{name: "explicit-db-flag", args: []string{"My Blog", "--db"}, services: []api.Service{mysqlRunning}},
		{name: "explicit-db-none-running", args: []string{"My Blog", "--db"}, services: []api.Service{mysqlStopped}},
		{name: "template-requires-db-none-running", args: []string{"My Blog", "--type", "wordpress"}, services: []api.Service{mysqlStopped}},
		{name: "explicit-db-ignores-postgres", args: []string{"My Blog", "--db"}, services: []api.Service{postgresRunning}},
		{name: "explicit-db-no-daemon", args: []string{"My Blog", "--db"}, noDaemon: true},
		{name: "explicit-db-service-stopped", args: []string{"My Blog", "--db-service", "mysql"}, services: []api.Service{mysqlStopped}},
		{name: "explicit-db-error", args: []string{"My Blog", "--db"}, services: []api.Service{mysqlRunning}, dbFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.MkdirTemp("", "flock-app-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(root) })
			dir := t.TempDir()
			t.Setenv("FLOCK_HOME", root)
			t.Chdir(dir)

			if err := config.Default().Save(root); err != nil {
				t.Fatal(err)
			}
			if tc.appsConf != "" {
				if err := os.WriteFile(filepath.Join(root, "apps.conf"), []byte(tc.appsConf), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			var reloads *atomic.Int32
			var argsFile string
			if !tc.noDaemon {
				reloads, argsFile = startFakeAppDaemon(t, root, tc.services, tc.dbFail)
			}

			out, err := runAppCommand(tc.args...)
			switch tc.name {
			case "explicit-db-none-running", "explicit-db-no-daemon", "explicit-db-service-stopped", "template-requires-db-none-running", "explicit-db-ignores-postgres":
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if _, statErr := os.Stat(filepath.Join(dir, "My Blog")); !os.IsNotExist(statErr) {
					t.Fatalf("prerequisite failure should not create project directory: %v", statErr)
				}
			case "explicit-db-error":
				if err == nil || !strings.Contains(err.Error(), "database denied") {
					t.Fatalf("expected DB creation error, got %v", err)
				}
				if reloads.Load() != 0 {
					t.Fatalf("expected no daemon reload on DB error, got %d", reloads.Load())
				}
			case "php-skips-db-even-when-running", "blank-skips-db-even-when-running":
				if err != nil || reloads.Load() != 1 || strings.Contains(out, "database my-blog ready") {
					t.Fatalf("expected no database creation (%v, %d): %s", err, reloads.Load(), out)
				}
				if _, statErr := os.Stat(argsFile); !os.IsNotExist(statErr) {
					t.Fatalf("database client should not have run: %v", statErr)
				}
			case "template-requires-db-falls-back-to-mariadb":
				if err != nil {
					t.Fatal(err)
				}
				if argsData, err := os.ReadFile(argsFile); err != nil || !strings.Contains(string(argsData), "-P3308") || !strings.Contains(out, "database my-blog ready in MariaDB") {
					t.Fatalf("unexpected mariadb output/args (%v, %s): %s", err, argsData, out)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if argsData, err := os.ReadFile(argsFile); err != nil || !strings.Contains(string(argsData), "-P3307") || !strings.Contains(string(argsData), "CREATE DATABASE IF NOT EXISTS `my-blog`") {
					t.Fatalf("unexpected mysql args (%v): %s", err, argsData)
				}
				if reloads.Load() != 1 || !strings.Contains(out, "database my-blog ready in MySQL") {
					t.Fatalf("unexpected output/reloads (%d): %s", reloads.Load(), out)
				}
			}
		})
	}
}
