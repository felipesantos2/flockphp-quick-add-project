package catalog

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"flock/internal/binaries"
	"flock/internal/config"
	"flock/internal/supervisor"
)

// TestIntegrationInstallAndRun installs real services through the manager (downloads of up to
// 350 MB each) from the published manifest, starts each one and talks to it. Run with:
//
//	FLOCK_CATALOG_INTEGRATION=mysql,valkey go test ./internal/catalog -run Integration -v -timeout 30m
//
// FLOCK_CATALOG_INTEGRATION=all runs every service available here.
func TestIntegrationInstallAndRun(t *testing.T) {
	want := os.Getenv("FLOCK_CATALOG_INTEGRATION")
	if testing.Short() || want == "" {
		t.Skip("set FLOCK_CATALOG_INTEGRATION=all (or a list of services) to run")
	}

	for _, k := range Kinds(runtime.GOOS) {
		if want != "all" && !strings.Contains(","+want+",", ","+k.ID+",") {
			continue
		}
		t.Run(k.ID, func(t *testing.T) {
			// A short root keeps unix socket paths under the macOS limit; the space in it stands
			// in for "Application Support".
			tmp, err := os.MkdirTemp("", "cat")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmp)
			root := filepath.Join(tmp, "a b")

			source := binaries.NewCatalog(binaries.DefaultSource(root, "dev"), func() binaries.Channel { return binaries.Stable })
			m := NewManager(root, []*Kind{k}, source, configMutate(root))
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
			defer cancel()
			if err := m.Check(ctx); err != nil {
				t.Fatalf("check: %v", err)
			}
			e, _ := m.Catalog().Find(k.ID)
			for _, l := range e.Lines {
				t.Logf("%s %s: newest %q", k.Name, l.ID, l.Latest)
			}

			port := freePort(t)
			if ok, why := m.Available(k); !ok {
				t.Skip(why)
			}
			if err := m.Start(k.ID, "", port); err != nil {
				t.Fatal(err)
			}
			m.Wait()
			e, _ = m.Catalog().Find(k.ID)
			if e.Job != nil {
				log, _ := os.ReadFile(m.LogPath(k.ID))
				t.Fatalf("install failed: %s\n%s", e.Job.Error, tail(string(log), 4000))
			}
			t.Logf("installed %s %s", k.Name, e.Installed)

			spec, err := Spec(root, k, config.Extra{Version: e.Installed, Port: port})
			if err != nil {
				t.Fatalf("spec: %v", err)
			}
			procs := supervisor.New()
			procs.Ensure(spec)
			defer procs.StopAll()

			if err := waitListening(port, 90*time.Second); err != nil {
				log, _ := os.ReadFile(spec.LogFile)
				t.Fatalf("%v\n%s", err, tail(string(log), 4000))
			}
			if err := probeService(k.ID, port); err != nil {
				log, _ := os.ReadFile(spec.LogFile)
				t.Fatalf("%v\n%s", err, tail(string(log), 4000))
			}
			if size, err := dirSize(Dir(root, k.ID, e.Installed)); err == nil {
				t.Logf("%s listens on %d and answers; install is %d MB", k.Name, port, size>>20)
			}
			if os.Getenv("FLOCK_CATALOG_SIZES") != "" {
				_ = filepath.Walk(Dir(root, k.ID, e.Installed), func(path string, info os.FileInfo, err error) error {
					if err == nil && !info.IsDir() && info.Size() > 4<<20 {
						t.Logf("%4d MB %s", info.Size()>>20, path)
					}
					return err
				})
			}
		})
	}
}

// probeService checks that the server on port speaks its protocol.
func probeService(id string, port int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	switch id {
	case "redis", "valkey":
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			return err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write([]byte("PING\r\n")); err != nil {
			return err
		}
		buf := make([]byte, 16)
		n, _ := conn.Read(buf)
		if !strings.HasPrefix(string(buf[:n]), "+PONG") {
			return fmt.Errorf("PING answered %q", buf[:n])
		}
	case "mysql", "mariadb":
		// The server greets first: protocol version 10, then its version string.
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			return err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 128)
		n, err := conn.Read(buf)
		if err != nil || n < 6 || buf[4] != 10 {
			return fmt.Errorf("no MySQL greeting: %q %v", buf[:n], err)
		}
	case "mongodb":
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			return err
		}
		conn.Close()
	case "meilisearch", "typesense", "rustfs":
		path := map[string]string{"meilisearch": "/health", "typesense": "/health", "rustfs": "/health"}[id]
		var last error
		for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
			resp, err := http.Get("http://" + addr + path)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
				err = fmt.Errorf("GET %s: %s", path, resp.Status)
			}
			last = err
		}
		return last
	}
	return nil
}

func waitListening(port int, timeout time.Duration) error {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
			conn.Close()
			return nil
		}
	}
	return fmt.Errorf("nothing listens on port %d after %s", port, timeout)
}

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

func dirSize(dir string) (int64, error) {
	var size int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	return size, err
}
