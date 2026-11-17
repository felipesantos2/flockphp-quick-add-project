package apps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flock/internal/config"
	"flock/internal/php"
)

func TestParse(t *testing.T) {
	dst := map[string]Template{}
	err := Parse("\n# comment\n; semi\n---\nBlank=\nPHP = index.php \nWordPress = https://wordpress.org/latest.tar.gz | db \n", dst)
	if err != nil {
		t.Fatal(err)
	}
	if dst["blank"] != (Template{Name: "blank"}) ||
		dst["php"] != (Template{Name: "php", Target: "index.php"}) ||
		dst["wordpress"] != (Template{Name: "wordpress", Target: "https://wordpress.org/latest.tar.gz", RequiresDB: true}) {
		t.Fatalf("unexpected parsed templates: %+v", dst)
	}
	for _, bad := range []string{"no-equals", "=value", "bad key=value", "wp=https://example.com/a.zip | unknown", "php=index.php\n\ufeffblank="} {
		if err := Parse(bad, map[string]Template{}); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
	bomDst := map[string]Template{}
	if err := Parse("\ufeff# comment\nphp=index.php\n", bomDst); err != nil || bomDst["php"] != (Template{Name: "php", Target: "index.php"}) {
		t.Fatalf("expected leading UTF-8 BOM to be stripped: dst=%+v, err=%v", bomDst, err)
	}
}

func TestLoadMergesOverride(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "apps.conf"), []byte("wordpress=https://example.com/wp.zip\ncustom=index.php | db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	templates, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 4 {
		t.Fatalf("expected 4 templates, got %+v", templates)
	}
	wp, err := Find(root, "wordpress")
	if err != nil || wp.Target != "https://example.com/wp.zip" || wp.RequiresDB {
		t.Fatalf("unexpected overridden wordpress template: %+v, %v", wp, err)
	}
	custom, err := Find(root, "custom")
	if err != nil || !custom.RequiresDB {
		t.Fatalf("unexpected custom template: %+v, %v", custom, err)
	}
}

func TestConfigureWPConfig(t *testing.T) {
	sample := strings.Join([]string{
		"<?php",
		"define( 'DB_NAME', 'database_name_here' );",
		"define( 'DB_USER', 'username_here' );",
		"define( 'DB_PASSWORD', 'password_here' );",
		"define( 'DB_HOST', 'localhost' );",
		"define( 'AUTH_KEY', 'put your unique phrase here' );",
		"define( 'SECURE_AUTH_KEY', 'put your unique phrase here' );",
		"define( 'LOGGED_IN_KEY', 'put your unique phrase here' );",
		"define( 'NONCE_KEY', 'put your unique phrase here' );",
		"define( 'AUTH_SALT', 'put your unique phrase here' );",
		"define( 'SECURE_AUTH_SALT', 'put your unique phrase here' );",
		"define( 'LOGGED_IN_SALT', 'put your unique phrase here' );",
		"define( 'NONCE_SALT', 'put your unique phrase here' );",
	}, "\n")

	t.Run("writes-wp-config", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "wp-config-sample.php"), []byte(sample), 0o644); err != nil {
			t.Fatal(err)
		}
		ok, err := ConfigureWPConfig(dir, "my-blog", 3307)
		if err != nil || !ok {
			t.Fatalf("expected wp-config.php to be configured, got ok=%v err=%v", ok, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "wp-config.php"))
		if err != nil {
			t.Fatal(err)
		}
		got := string(data)
		for _, want := range []string{
			"define( 'DB_NAME', 'my-blog' );",
			"define( 'DB_USER', 'root' );",
			"define( 'DB_PASSWORD', '' );",
			"define( 'DB_HOST', '127.0.0.1:3307' );",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q in wp-config.php:\n%s", want, got)
			}
		}
		if strings.Contains(got, "put your unique phrase here") {
			t.Fatalf("unreplaced salt placeholder in wp-config.php")
		}
	})

	t.Run("no-sample-skips", func(t *testing.T) {
		ok, err := ConfigureWPConfig(t.TempDir(), "my-blog", 3306)
		if err != nil || ok {
			t.Fatalf("expected (false, nil), got (%v, %v)", ok, err)
		}
	})

	t.Run("preserves-existing-wp-config", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "wp-config-sample.php"), []byte(sample), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		ok, err := ConfigureWPConfig(dir, "my-blog", 3306)
		if err != nil || ok {
			t.Fatalf("expected (false, nil), got (%v, %v)", ok, err)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "wp-config.php"))
		if string(data) != "keep" {
			t.Fatalf("existing wp-config.php was overwritten: %q", data)
		}
	})

	t.Run("custom-sample-skips-gracefully", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "wp-config-sample.php"), []byte("<?php // custom"), 0o644); err != nil {
			t.Fatal(err)
		}
		ok, err := ConfigureWPConfig(dir, "my-blog", 3306)
		if err != nil || ok {
			t.Fatalf("expected (false, nil), got (%v, %v)", ok, err)
		}
	})
}

func TestValidateNameRejectsWindowsInvalidCharacters(t *testing.T) {
	for _, bad := range []string{"foo*", "foo?", "foo<bar", "foo>bar", `foo"bar`, "foo|bar", "foo:bar", "CON", "prn", "aux", "nul", "com1", "lpt9"} {
		if _, err := ValidateName(bad); err == nil {
			t.Fatalf("expected ValidateName(%q) to fail", bad)
		}
	}
	if got, err := ValidateName("my_app_2"); err != nil || got != "my-app-2" {
		t.Fatalf("ValidateName(my_app_2) = (%q, %v)", got, err)
	}
}

func TestSamePathComparesIdentities(t *testing.T) {
	dir := t.TempDir()
	if !SamePath(dir, dir) {
		t.Fatal("expected identical paths to match")
	}
	if !SamePath(filepath.Join(dir, "A"), filepath.Join(dir, "a")) {
		// on Windows it should match
		if os.PathSeparator == '\\' {
			t.Fatal("expected case-insensitive match on Windows")
		}
	}
}

func TestCreateRejectsUninstalledPHP(t *testing.T) {
	root, parent := t.TempDir(), t.TempDir()

	_, err := Create(context.Background(), CreateRequest{
		Root:      root,
		Name:      "bad-php",
		ParentDir: parent,
		Type:      "blank",
		PHP:       "9.9",
	})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "PHP 9.9 is not installed") {
		t.Fatalf("expected ErrInvalid for uninstalled PHP, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(parent, "bad-php")); !os.IsNotExist(statErr) {
		t.Fatalf("expected destination not to be created: %v", statErr)
	}

	bin := php.Binary(root, "8.4")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Create(context.Background(), CreateRequest{
		Root:      root,
		Name:      "ok-php",
		ParentDir: parent,
		Type:      "blank",
		PHP:       "8.4",
	})
	if err != nil || res.Site.PHP != "8.4" {
		t.Fatalf("expected installed PHP 8.4 to succeed, got %+v, err=%v", res, err)
	}
}

func TestCreateSaveSiteErrorIncludesProjectPath(t *testing.T) {
	root, parent := t.TempDir(), t.TempDir()
	sentinel := errors.New("save conflict")
	target := filepath.Join(parent, "my-app")

	_, err := Create(context.Background(), CreateRequest{
		Root:      root,
		Name:      "my-app",
		ParentDir: parent,
		Type:      "blank",
		SaveSite: func(config.Site) error {
			return sentinel
		},
	})
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "project created at "+target+", but save conflict") {
		t.Fatalf("expected created path and wrapped error, got %v", err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("expected created project directory to remain on disk: %v", statErr)
	}
}
