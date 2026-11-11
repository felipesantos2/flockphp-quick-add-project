package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flock/internal/config"
)

func runAppCommand(args ...string) (string, error) {
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"app", "create"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func TestAppCreate(t *testing.T) {
	for _, explicitType := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "explicit-type"}[explicitType], func(t *testing.T) {
			root, dir := t.TempDir(), t.TempDir()
			t.Setenv("FLOCK_HOME", root)
			t.Chdir(dir)
			cfg := config.Default()
			cfg.TLD = "local"
			cfg.DefaultPHP = "8.4"
			if err := cfg.Save(root); err != nil {
				t.Fatal(err)
			}
			args := []string{"My App"}
			if explicitType {
				args = append(args, "--type", "php")
			}
			out, err := runAppCommand(args...)
			if err != nil {
				t.Fatal(err)
			}
			path, err := filepath.Abs("My App")
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(path, "index.php"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(data), "<?php\n") || !strings.Contains(string(data), "echo 'Hello from Flock!';") {
				t.Fatalf("unexpected PHP entrypoint: %q", data)
			}
			saved, err := config.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			site, ok := saved.FindSite("my-app")
			if !ok || site.Parked || site.Path != path || saved.PHPFor(site) != "8.4" {
				t.Fatalf("unexpected site: %+v", site)
			}
			if !strings.Contains(out, "created "+path) || !strings.Contains(out, "http://my-app.local") ||
				!strings.Contains(out, "daemon is not running") {
				t.Fatalf("unexpected output: %s", out)
			}
		})
	}
}

func TestAppCreateRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{}, {"one", "two"}, {""}, {"."}, {".."}, {"../escape"}, {"nested/app"},
		{`nested\app`}, {"C:app"}, {" app"}, {"app."}, {"---"}, {strings.Repeat("a", 64)},
		{"demo", "--type", "laravel"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			root, dir := t.TempDir(), t.TempDir()
			t.Setenv("FLOCK_HOME", root)
			t.Chdir(dir)
			if _, err := runAppCommand(args...); err == nil {
				t.Fatal("expected invalid input to fail")
			}
			for _, path := range []string{root, dir} {
				entries, err := os.ReadDir(path)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 0 {
					t.Fatalf("invalid input modified %s: %v", path, entries)
				}
			}
		})
	}
}

func TestAppCreatePreservesExistingDestination(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			root, dir := t.TempDir(), t.TempDir()
			t.Setenv("FLOCK_HOME", root)
			t.Chdir(dir)
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
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "keep me" {
				t.Fatalf("existing content changed: %q, %v", data, err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("config was written: %v, %v", entries, err)
			}
		})
	}
}

func TestAppCreatePreservesNameConflicts(t *testing.T) {
	for _, kind := range []string{"linked", "parked", "proxy"} {
		t.Run(kind, func(t *testing.T) {
			root, dir, existing := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("FLOCK_HOME", root)
			t.Chdir(dir)
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
			after, err := os.ReadFile(filepath.Join(root, "config.json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("existing config changed: %v", err)
			}
		})
	}
}

func TestAppCreateReportsSaveFailureAndKeepsProject(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	t.Setenv("FLOCK_HOME", root)
	t.Chdir(dir)
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

func TestAppCreateWordPressNeedsDaemon(t *testing.T) {
	root, dir := t.TempDir(), t.TempDir()
	t.Setenv("FLOCK_HOME", root)
	t.Chdir(dir)
	if _, err := runAppCommand("demo", "--type", "wordpress"); err == nil ||
		!strings.Contains(err.Error(), "WordPress requires running MySQL") {
		t.Fatalf("expected MySQL prerequisite error, got %v", err)
	}
	for _, path := range []string{root, dir} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatalf("prerequisite failure modified %s: %v, %v", path, entries, err)
		}
	}
}
