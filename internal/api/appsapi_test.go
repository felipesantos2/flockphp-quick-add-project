package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flock/internal/apps"
	"flock/internal/config"
)

func TestAppsTemplatesEndpoint(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "apps.conf"), []byte("custom=index.php | db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(&fakeBackend{root: root}, "")
	req := httptest.NewRequest(http.MethodGet, "/v1/apps/templates", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/apps/templates = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Templates []apps.Template `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Templates) != 4 || out.Templates[1].Name != "custom" || !out.Templates[1].RequiresDB {
		t.Fatalf("unexpected templates: %+v", out.Templates)
	}
}

func TestCreateAppEndpoint(t *testing.T) {
	t.Run("creates-php-app", func(t *testing.T) {
		root, parent := t.TempDir(), t.TempDir()
		handler := NewHandler(&fakeBackend{root: root}, "")
		body, _ := json.Marshal(map[string]any{
			"name":       "My App",
			"parent_dir": parent,
			"type":       "php",
			"php":        "8.4",
			"secured":    true,
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/apps", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("POST /v1/apps = %d: %s", rec.Code, rec.Body.String())
		}
		target := filepath.Join(parent, "My App")
		if _, err := os.Stat(filepath.Join(target, "index.php")); err != nil {
			t.Fatalf("expected index.php: %v", err)
		}
		cfg, err := config.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		site, ok := cfg.FindSite("my-app")
		if !ok || site.Path != target || site.PHP != "8.4" || !site.Secured {
			t.Fatalf("unexpected saved site: %+v", site)
		}
	})

	t.Run("rejects-missing-db-for-wordpress", func(t *testing.T) {
		root, parent := t.TempDir(), t.TempDir()
		handler := NewHandler(&fakeBackend{root: root}, "")
		body, _ := json.Marshal(map[string]any{
			"name":       "my-blog",
			"parent_dir": parent,
			"type":       "wordpress",
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/apps", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no running database service found") {
			t.Fatalf("expected 400 missing DB error, got %d: %s", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(parent, "my-blog")); !os.IsNotExist(err) {
			t.Fatalf("expected destination not to be created: %v", err)
		}
	})

	t.Run("rejects-existing-site-before-creating-dir", func(t *testing.T) {
		root, parent := t.TempDir(), t.TempDir()
		cfg := config.Default()
		cfg.Sites = []config.Site{{Name: "demo", Path: parent}}
		if err := cfg.Save(root); err != nil {
			t.Fatal(err)
		}
		handler := NewHandler(&fakeBackend{root: root}, "")
		body, _ := json.Marshal(map[string]any{
			"name":       "demo",
			"parent_dir": parent,
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/apps", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 conflict, got %d: %s", rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(parent, "demo")); !os.IsNotExist(err) {
			t.Fatalf("expected destination not to be created: %v", err)
		}
	})
}
