package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/catalog"
	"flock/internal/config"
	"flock/internal/paths"
)

type wordpressTransport func(*http.Request) (*http.Response, error)

func (f wordpressTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func wordpressArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "wordpress/" + name, Mode: 0o644, Size: int64(len(content))}); err != nil {
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

func TestDownloadWordPress(t *testing.T) {
	files := map[string]string{"index.php": "<?php", "wp-settings.php": "<?php", "wp-config-sample.php": "<?php"}
	for _, kind := range []string{"valid", "http-error", "invalid-archive", "incomplete"} {
		t.Run(kind, func(t *testing.T) {
			data := wordpressArchive(t, files)
			if kind == "incomplete" {
				data = wordpressArchive(t, map[string]string{"index.php": "<?php"})
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "http-error":
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
				case "invalid-archive":
					w.Write([]byte("not an archive"))
				default:
					w.Write(data)
				}
			}))
			defer server.Close()
			dir := t.TempDir()
			err := downloadWordPress(context.Background(), server.Client(), server.URL, dir)
			if kind != "valid" {
				if err == nil {
					t.Fatal("expected download or extraction to fail")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range files {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || string(got) != want {
					t.Fatalf("%s: %q, %v", name, got, err)
				}
			}
		})
	}
}

func TestDownloadWordPressHonorsDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := downloadWordPress(ctx, server.Client(), server.URL, t.TempDir())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}

	if time.Since(start) > time.Second {
		t.Fatal("download did not stop promptly")
	}
}

func TestDownloadWordPressSets120SecondDeadline(t *testing.T) {
	stopped := errors.New("stop before downloading")
	client := &http.Client{Transport: wordpressTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < 119*time.Second || time.Until(deadline) > 120*time.Second {
			t.Errorf("expected a 120-second deadline, got %v", deadline)
		}
		return nil, stopped
	})}
	if err := downloadWordPress(context.Background(), client, wordpressURL, t.TempDir()); !errors.Is(err, stopped) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wordpressConfigSample() string {
	var content strings.Builder
	content.WriteString("<?php\ndefine('DB_NAME', 'database_name_here');\ndefine('DB_USER', 'username_here');\n")
	content.WriteString("define('DB_PASSWORD', 'password_here');\ndefine('DB_HOST', 'localhost');\n")
	for _, key := range []string{"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY", "AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT"} {
		content.WriteString("define('" + key + "', 'put your unique phrase here');\n")
	}
	content.WriteString("$table_prefix = 'wp_';\nrequire_once __DIR__ . '/wp-settings.php';\n")
	return content.String()
}

func TestConfigureWordPress(t *testing.T) {
	dir := t.TempDir()
	sample := wordpressConfigSample()
	if err := os.WriteFile(filepath.Join(dir, "wp-config-sample.php"), []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := configureWordPress(dir, "my-blog", 3307); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"define('DB_NAME', 'my-blog')", "define('DB_USER', 'root')",
		"define('DB_PASSWORD', '')", "define('DB_HOST', '127.0.0.1:3307')",
		"$table_prefix = 'wp_';", "require_once __DIR__ . '/wp-settings.php';",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q", want)
		}
	}
	keys := regexp.MustCompile(`'[a-f0-9]{64}'`).FindAllString(content, -1)
	unique := map[string]bool{}
	for _, key := range keys {
		unique[key] = true
	}
	if len(keys) != 8 || len(unique) != 8 || strings.Contains(content, "put your unique phrase here") {
		t.Fatal("authentication keys and salts were not generated independently")
	}
	unchanged, err := os.ReadFile(filepath.Join(dir, "wp-config-sample.php"))
	if err != nil || string(unchanged) != sample {
		t.Fatalf("config sample changed: %v", err)
	}
}

func TestConfigureWordPressRejectsIncompleteSample(t *testing.T) {
	for _, placeholder := range []string{"'database_name_here'", "'localhost'", "put your unique phrase here"} {
		t.Run(placeholder, func(t *testing.T) {
			dir := t.TempDir()
			content := strings.Replace(wordpressConfigSample(), placeholder, "unexpected", 1)
			if err := os.WriteFile(filepath.Join(dir, "wp-config-sample.php"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := configureWordPress(dir, "my-blog", 3306); err == nil {
				t.Fatal("expected incomplete config to fail")
			}
			if _, err := os.Stat(filepath.Join(dir, "wp-config.php")); !os.IsNotExist(err) {
				t.Fatalf("invalid config was written: %v", err)
			}
		})
	}
}

func TestWordPressCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the MySQL client")
	}
	for _, kind := range []string{"success", "mysql-missing", "mysql-uninstalled", "mysql-stopped", "mariadb-only", "download-error", "database-error"} {
		t.Run(kind, func(t *testing.T) {
			root, err := os.MkdirTemp("", "flock-wp-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(root) })
			dir := t.TempDir()
			t.Setenv("FLOCK_HOME", root)
			t.Chdir(dir)
			path, err := filepath.Abs("My Blog")
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			if err := cfg.Save(root); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(root, "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			service := api.Service{ID: "mysql", Name: "MySQL", Installed: true, Running: true, Version: "8.4.11", Port: 3307}
			services := []api.Service{service}
			switch kind {
			case "mysql-missing":
				services = nil
			case "mysql-uninstalled":
				services[0].Installed = false
			case "mysql-stopped":
				services[0].Running = false
			case "mariadb-only":
				services[0].ID = "mariadb"
			}
			var reloads atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /v1/services":
					json.NewEncoder(w).Encode(services)
				case "POST /v1/reload":
					reloads.Add(1)
					json.NewEncoder(w).Encode(api.Status{})
				default:
					t.Errorf("unexpected daemon request: %s %s", r.Method, r.URL.Path)
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

			argsFile := filepath.Join(root, "mysql.args")
			t.Setenv("FLOCK_TEST_MYSQL_ARGS", argsFile)
			client := filepath.Join(catalog.Dir(root, "mysql", service.Version), "bin", "mysql")
			if err := os.MkdirAll(filepath.Dir(client), 0o755); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FLOCK_TEST_MYSQL_ARGS\"\n"
			if kind == "database-error" {
				script += "echo 'database denied' >&2\nexit 1\n"
			}
			if err := os.WriteFile(client, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			data := wordpressArchive(t, map[string]string{
				"index.php":       "<?php /* WordPress entrypoint */",
				"wp-settings.php": "<?php", "wp-config-sample.php": wordpressConfigSample(),
			})
			var downloads atomic.Int32
			oldClient := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: wordpressTransport(func(r *http.Request) (*http.Response, error) {
				downloads.Add(1)
				if r.URL.String() != "https://wordpress.org/latest.tar.gz" {
					t.Errorf("unexpected download: %s", r.URL)
				}
				status := http.StatusOK
				if kind == "download-error" {
					status = http.StatusServiceUnavailable
				}
				return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
			})}
			t.Cleanup(func() { http.DefaultClient = oldClient })

			out, err := runAppCommand("My Blog", "--type", "wordpress")
			if kind == "success" {
				if err != nil {
					t.Fatal(err)
				}
				saved, err := config.Load(root)
				if err != nil {
					t.Fatal(err)
				}
				site, ok := saved.FindSite("my-blog")
				if !ok || site.Path != path || reloads.Load() != 1 {
					t.Fatalf("site not linked and applied: %+v, reloads %d", site, reloads.Load())
				}
				content, err := os.ReadFile(filepath.Join(site.Path, "wp-config.php"))
				if err != nil || !strings.Contains(string(content), "'127.0.0.1:3307'") || !strings.Contains(string(content), "'my-blog'") {
					t.Fatalf("unexpected WordPress config: %s, %v", content, err)
				}
				if !strings.Contains(out, "http://my-blog.test") {
					t.Fatalf("site URL not reported: %s", out)
				}
			} else {
				if err == nil || reloads.Load() != 0 {
					t.Fatalf("setup should fail without reloading: %v, reloads %d", err, reloads.Load())
				}
				after, readErr := os.ReadFile(filepath.Join(root, "config.json"))
				if readErr != nil || !bytes.Equal(before, after) {
					t.Fatalf("failed setup changed site settings: %v", readErr)
				}
			}
			switch kind {
			case "mysql-missing", "mysql-uninstalled", "mysql-stopped", "mariadb-only":
				if _, err := os.Stat("My Blog"); !os.IsNotExist(err) || downloads.Load() != 0 {
					t.Fatalf("created files or downloaded before MySQL was ready: %v", err)
				}
			case "success", "database-error":
				args, err := os.ReadFile(argsFile)
				if err != nil || string(args) != "--no-defaults\n-uroot\n-h127.0.0.1\n-P3307\n-e\nCREATE DATABASE IF NOT EXISTS `my-blog`\n" {
					t.Fatalf("unexpected MySQL invocation: %s, %v", args, err)
				}
			}
			if kind == "database-error" && !strings.Contains(err.Error(), "database denied") {
				t.Fatalf("database error was not surfaced: %v", err)
			}
			if kind == "download-error" {
				if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
					t.Fatalf("database command ran after failed download: %v", err)
				}
			}
		})
	}
}
