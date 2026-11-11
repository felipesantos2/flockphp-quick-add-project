package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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
