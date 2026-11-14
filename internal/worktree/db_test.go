package worktree

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeServer is a database server that keeps its databases' names.
type fakeServer struct {
	dbs     []string
	fail    error  // what Create and Copy return
	service string // "postgres" when empty
}

func (s *fakeServer) Service() string {
	if s.service == "" {
		return "postgres"
	}
	return s.service
}

func (s *fakeServer) Create(_ context.Context, name string) error {
	if s.fail != nil {
		return s.fail
	}
	if slices.Contains(s.dbs, name) {
		return ErrExists
	}
	s.dbs = append(s.dbs, name)
	return nil
}

func (s *fakeServer) Copy(ctx context.Context, from, to string, _ io.Writer) error {
	if !slices.Contains(s.dbs, from) {
		return errors.New("no database " + from)
	}
	return s.Create(ctx, to)
}

func (s *fakeServer) Drop(_ context.Context, name string) error {
	s.dbs = slices.DeleteFunc(s.dbs, func(d string) bool { return d == name })
	return nil
}

func TestDatabaseName(t *testing.T) {
	if got := DatabaseName("Acme-Shop", "feature-auth"); got != "acme_shop_feature_auth" {
		t.Errorf("got %q", got)
	}
	long := DatabaseName("acme", strings.Repeat("x", 80))
	if len(long) != 63 || long == DatabaseName("acme", strings.Repeat("x", 79)) {
		t.Errorf("long names are cut to 63 and stay apart: %q", long)
	}
}

// dbJob is a worktree of a site whose .env is env, with the fake server on 5432 and 3306.
func dbJob(t *testing.T, env string, server *fakeServer) *Job {
	t.Helper()
	site, wt := t.TempDir(), t.TempDir()
	write(t, filepath.Join(site, ".env"), env)
	write(t, filepath.Join(wt, ".env"), env)
	return &Job{Site: site, Path: wt, Domain: "feature-auth.acme.test", ParentDomain: "acme.test",
		Settings: Defaults(), Servers: func(engine string, port int) Server {
			if (engine == "pgsql" && port == 5432) || (engine == "mysql" && port == 3306) {
				return server
			}
			return nil
		}, Services: func(id string) (Server, bool) {
			if id == server.Service() {
				return server, true
			}
			return nil, false
		}}
}

func TestServerDatabases(t *testing.T) {
	ctx, log := context.Background(), &bytes.Buffer{}

	server := &fakeServer{dbs: []string{"acme"}}
	j := dbJob(t, "DB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\nDB_DATABASE=acme\n", server)
	j.DBMode = Copy
	var recorded Database
	j.OnDatabase = func(db Database) { recorded = db }
	if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	want := Database{Engine: "pgsql", Name: "acme_feature_auth", Service: "postgres"}
	if recorded != want || !slices.Equal(server.dbs, []string{"acme", "acme_feature_auth"}) {
		t.Fatalf("copy: recorded %+v, server %v", recorded, server.dbs)
	}
	if !strings.Contains(read(t, filepath.Join(j.Path, ".env")), "DB_DATABASE=acme_feature_auth\n") {
		t.Errorf(".env: %s", read(t, filepath.Join(j.Path, ".env")))
	}

	// Set up again, it keeps the database it made.
	j.Database = recorded
	if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil || len(server.dbs) != 2 {
		t.Fatalf("again: %v %v", err, server.dbs)
	}

	// Back to the site's: dropped, .env names the site's again.
	j.DBMode = Shared
	if err := j.SwitchDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(server.dbs, []string{"acme"}) || recorded != (Database{}) {
		t.Errorf("shared: recorded %+v, server %v", recorded, server.dbs)
	}
	if !strings.Contains(read(t, filepath.Join(j.Path, ".env")), "DB_DATABASE=acme\n") {
		t.Errorf(".env: %s", read(t, filepath.Join(j.Path, ".env")))
	}

	// A database Flock didn't make is never used: the worktree stays on the site's.
	server.dbs = append(server.dbs, "acme_feature_auth")
	j.DBMode = Fresh
	if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if recorded != (Database{}) || !strings.Contains(log.String(), "Flock didn't make it") {
		t.Errorf("an existing database: recorded %+v\n%s", recorded, log)
	}

	// A server Flock doesn't run, or one that fails: the site's database, and the log says why.
	for _, env := range []string{"DB_CONNECTION=pgsql\nDB_HOST=db.example.com\nDB_DATABASE=acme\n", "DB_CONNECTION=mysql\nDB_DATABASE=acme\n"} {
		failing := &fakeServer{fail: errors.New("access denied")}
		j := dbJob(t, env, failing)
		j.DBMode = Fresh
		log.Reset()
		if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil || j.Database != (Database{}) {
			t.Fatalf("%q: %v %+v", env, err, j.Database)
		}
		if !strings.Contains(log.String(), "uses the site's database") {
			t.Errorf("%q: %s", env, log)
		}
	}
}

func TestSQLiteDatabases(t *testing.T) {
	ctx, log := context.Background(), &bytes.Buffer{}
	j := dbJob(t, "DB_CONNECTION=sqlite\n", nil)
	write(t, filepath.Join(j.Site, "database", "database.sqlite"), "site data")

	if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(j.Path, ".env")), "DB_DATABASE="+filepath.Join(j.Site, "database", "database.sqlite")) {
		t.Errorf("shared: %s", read(t, filepath.Join(j.Path, ".env")))
	}

	j.DBMode = Copy
	if err := j.setUpDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(j.Path, "database", "database.sqlite")
	if j.Database != (Database{Engine: "sqlite", Name: file}) || read(t, file) != "site data" {
		t.Fatalf("copy: %+v", j.Database)
	}

	j.DBMode = Fresh
	if err := j.SwitchDatabase(ctx, log, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if read(t, file) != "" || read(t, filepath.Join(j.Site, "database", "database.sqlite")) != "site data" {
		t.Error("fresh: the copy wasn't replaced by an empty file, or the site's changed")
	}
	if err := j.DropDatabase(ctx, log); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("dropped, the file is still there: %v", err)
	}
}

// The database is dropped on the server of the service it was made on, with no site needed (it may
// be unlinked): one recorded before Flock kept the service on the one Flock runs for the engine.
func TestDropDatabase(t *testing.T) {
	const name = "acme_feature_auth"
	for _, tc := range []struct {
		desc            string
		db              Database
		running         []string // of mysql, mariadb: both installed
		dropped, failed string   // the service it's dropped on; the one that fails, stopped
	}{
		{"recorded", Database{Engine: "mariadb", Name: name, Service: "mariadb"}, []string{"mysql", "mariadb"}, "mariadb", ""},
		{"recorded, on mysql", Database{Engine: "mysql", Name: name, Service: "mariadb"}, []string{"mysql", "mariadb"}, "mariadb", ""},
		{"not recorded", Database{Engine: "mariadb", Name: name}, []string{"mariadb"}, "", "mysql"},
		{"not recorded, mysql stopped", Database{Engine: "mysql", Name: name}, []string{"mariadb"}, "", "mysql"},
		{"stopped", Database{Engine: "mysql", Name: name, Service: "mariadb"}, []string{"mysql"}, "", "mariadb"},
		{"uninstalled", Database{Engine: "pgsql", Name: name, Service: "postgres"}, nil, "", ""},
	} {
		servers := map[string]*fakeServer{}
		for _, id := range []string{"mysql", "mariadb"} {
			servers[id] = &fakeServer{dbs: []string{name}, service: id}
		}
		log := &bytes.Buffer{}
		j := Job{Path: filepath.Join(t.TempDir(), "gone"), Database: tc.db,
			Services: func(id string) (Server, bool) {
				s, installed := servers[id]
				if !installed || !slices.Contains(tc.running, id) {
					return nil, installed
				}
				return s, true
			}}
		err := j.DropDatabase(context.Background(), log)
		switch {
		case tc.failed != "":
			if err == nil || !strings.Contains(err.Error(), tc.failed+" is stopped") {
				t.Errorf("%s: %v, want %s stopped", tc.desc, err, tc.failed)
			}
		case err != nil:
			t.Errorf("%s: %v", tc.desc, err)
		case tc.dropped == "" && !strings.Contains(log.String(), "isn't dropped"):
			t.Errorf("%s: log %q", tc.desc, log.String())
		}
		for id, s := range servers {
			if gone := len(s.dbs) == 0; gone != (id == tc.dropped) {
				t.Errorf("%s: dropped on %s: %v", tc.desc, id, gone)
			}
		}
	}
}
