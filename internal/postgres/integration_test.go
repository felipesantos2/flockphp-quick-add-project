package postgres

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flock/internal/binaries"
	"flock/internal/supervisor"
)

// TestIntegrationInstallRunCreate downloads the published PostgreSQL 18 (~50 MB). Run with:
// FLOCK_PG_INTEGRATION=1 go test ./internal/postgres -run Integration -v
func TestIntegrationInstallRunCreate(t *testing.T) {
	if testing.Short() || os.Getenv("FLOCK_PG_INTEGRATION") != "1" {
		t.Skip("set FLOCK_PG_INTEGRATION=1 to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Short root keeps the unix socket path under the macOS limit.
	root, err := os.MkdirTemp("", "pg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)

	inst := NewInstaller(root)
	catalog := binaries.NewCatalog(binaries.DefaultSource(root, "dev"), func() binaries.Channel { return binaries.Stable })
	manifest, err := catalog.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, err := inst.Install(ctx, offers(manifest, binaries.Platform())[DefaultMajor])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inst.BinDir(id), exe("psql"))); err != nil {
		t.Fatal("the package has no psql")
	}

	data := filepath.Join(root, "data")
	if err := InitDB(inst.BinDir(id), data); err != nil {
		t.Fatal(err)
	}
	if err := InitDB(inst.BinDir(id), data); err != nil {
		t.Fatalf("second InitDB must be a no-op: %v", err)
	}

	port := freePort(t)
	runDir := filepath.Join(root, "run")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}

	sup := supervisor.New()
	defer sup.StopAll()
	sup.Ensure(Spec(inst.BinDir(id), data, runDir, filepath.Join(root, "pg.log"), port))

	deadline := time.Now().Add(30 * time.Second)
	for Exec(ctx, port, "postgres", "SELECT 1") != nil {
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(root, "pg.log"))
			t.Fatalf("postgres did not start:\n%s", log)
		}
		time.Sleep(200 * time.Millisecond)
	}

	for range 2 {
		if err := CreateDatabase(ctx, port, "watchtower_testing"); err != nil {
			t.Fatal(err)
		}
	}
	if err := Exec(ctx, port, "watchtower_testing", "CREATE TABLE t (id int); INSERT INTO t VALUES (1); SELECT * FROM t"); err != nil {
		t.Fatal(err)
	}

	var pgErr *Error
	if err := Exec(ctx, port, "watchtower_testing", "SELECT * FROM missing"); err == nil || !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
		t.Fatalf("want undefined_table error, got %v", err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port
}

// TestStopFastShutsDownCleanly runs against installed binaries:
// FLOCK_PG_BIN=~/Library/Application\ Support/Flock/services/postgres/18.6-build1/bin go test ./internal/postgres -run StopFast -v
func TestStopFastShutsDownCleanly(t *testing.T) {
	bin := os.Getenv("FLOCK_PG_BIN")
	if bin == "" {
		t.Skip("set FLOCK_PG_BIN to a postgres bin directory to run")
	}

	root, err := os.MkdirTemp("", "pg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	data := filepath.Join(root, "data")
	if err := InitDB(bin, data); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	logFile := filepath.Join(root, "pg.log")
	spec := Spec(bin, data, root, logFile, port)
	spec.Stop = func() error { return StopFast(bin, data) } // what Windows uses
	sup := supervisor.New()
	sup.Ensure(spec)

	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)
	for Exec(ctx, port, "postgres", "SELECT 1") != nil {
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(logFile)
			t.Fatalf("postgres did not start:\n%s", log)
		}
		time.Sleep(200 * time.Millisecond)
	}

	start := time.Now()
	sup.StopAll()
	log, _ := os.ReadFile(logFile)
	if time.Since(start) > 9*time.Second || !strings.Contains(string(log), "fast shutdown request") ||
		strings.Contains(string(log), "graceful stop of postgres failed") {
		t.Fatalf("no clean fast shutdown:\n%s", log)
	}
}
