package worktree

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"flock/internal/fsutil"
)

// Database modes: the site's database, a copy of it, or an empty one with the migrations run.
const (
	Shared = "shared"
	Copy   = "copy"
	Fresh  = "fresh"
)

// Database is a worktree's own database: one in Engine ("pgsql", "mysql", "mariadb") on the
// server of Service, or for "sqlite" the file. The zero value is none: the worktree uses the site's.
type Database struct {
	Engine  string `json:"engine,omitempty"`
	Name    string `json:"name,omitempty"`
	Service string `json:"service,omitempty"`
}

// Server is a database server Flock runs, where a worktree can get a database of its own.
type Server interface {
	// Service is the Flock service that runs it: "postgres", "mysql" or "mariadb".
	Service() string
	// Create makes an empty database; ErrExists when there is one by that name.
	Create(ctx context.Context, name string) error
	// Copy makes database to as a copy of from; ErrExists when to exists.
	Copy(ctx context.Context, from, to string, log io.Writer) error
	// Drop drops the database, closing its connections.
	Drop(ctx context.Context, name string) error
}

// ErrExists is a database that exists already: Flock didn't make it, so it is never used or dropped.
var ErrExists = errors.New("exists already")

// DatabaseName is the name of a worktree's own database: <site's database>_<slug>, in the
// characters every engine takes unquoted, and short enough for PostgreSQL's 63 (MySQL: 64).
func DatabaseName(parent, slug string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(parent + "_" + slug) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	name := b.String()
	if len(name) > 63 {
		sum := sha1.Sum([]byte(slug))
		name = name[:56] + "_" + hex.EncodeToString(sum[:])[:6]
	}
	return name
}

// dbEnv is how the worktree reaches its database: its .env, and the site's values to start from.
type dbEnv struct {
	env        *Dotenv
	path       string
	connection string
	parentDB   string // the site's DB_DATABASE
	host       string
	port       int
}

func (j Job) readDBEnv() (*dbEnv, error) {
	path := filepath.Join(j.Path, ".env")
	env, err := ReadDotenv(path)
	if err != nil {
		return nil, err
	}
	// The site's .env says which database is the site's: the worktree's may name its own by now.
	site, err := ReadDotenv(filepath.Join(j.Site, ".env"))
	if err != nil {
		site = env
	}
	d := &dbEnv{env: env, path: path, connection: env.Get("DB_CONNECTION"), parentDB: site.Get("DB_DATABASE"), host: site.Get("DB_HOST")}
	d.port, _ = strconv.Atoi(site.Get("DB_PORT"))
	if d.port == 0 {
		d.port = map[string]int{"pgsql": 5432, "mysql": 3306, "mariadb": 3306}[d.connection]
	}
	return d, nil
}

// server is the Flock server the .env names, nil when Flock doesn't run it (Docker, a remote host).
func (j Job) server(d *dbEnv, engine string) Server {
	switch d.host {
	case "", "127.0.0.1", "localhost", "::1":
	default:
		return nil
	}
	if j.Servers == nil {
		return nil
	}
	return j.Servers(engine, d.port)
}

// setUpDatabase gives the worktree the database its mode asks for and points .env at it; with a
// database of its own, the migrations run (with copy too: the branch may add some). A database
// Flock can't make leaves the worktree on the site's, saying why in the log.
func (j *Job) setUpDatabase(ctx context.Context, log io.Writer, step func(string)) error {
	d, err := j.readDBEnv()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	mode := j.DBMode
	if mode == "" {
		mode = Shared
	}

	var db Database
	switch d.connection {
	case "sqlite":
		db, err = j.sqliteDatabase(d, mode, log)
	case "pgsql", "mysql", "mariadb":
		db, err = j.serverDatabase(ctx, d, mode, log)
	default:
		if mode != Shared && d.connection != "" {
			fmt.Fprintf(log, "database: Flock can't give a %s database of its own; the worktree uses the site's\n", d.connection)
		}
	}
	if err != nil {
		return err
	}
	j.Database = db
	if j.OnDatabase != nil {
		j.OnDatabase(db)
	}
	if err := d.env.Write(d.path); err != nil {
		return err
	}

	if db.Name != "" && j.Settings.Migrate && exists(filepath.Join(j.Path, "artisan")) && exists(filepath.Join(j.Path, "vendor")) {
		step(StepMigrate)
		if err := j.run(ctx, log, j.Flock, "exec", "php", "artisan", "migrate", "--force", "--no-interaction"); err != nil {
			return fmt.Errorf("php artisan migrate failed: %w", err)
		}
	}
	return nil
}

// sqliteFile is the site's database file.
func (j Job) sqliteFile(d *dbEnv) string {
	switch {
	case d.parentDB == "":
		return filepath.Join(j.Site, "database", "database.sqlite")
	case filepath.IsAbs(d.parentDB) || d.parentDB == ":memory:":
		return d.parentDB
	}
	return filepath.Join(j.Site, d.parentDB)
}

func (j Job) sqliteDatabase(d *dbEnv, mode string, log io.Writer) (Database, error) {
	site := j.sqliteFile(d)
	if site == ":memory:" {
		return Database{}, nil
	}
	if mode == Shared {
		set(d.env, log, "DB_DATABASE", site)
		return Database{}, nil
	}

	file := j.Database.Name
	if j.Database.Engine != "sqlite" || file == "" {
		// The same place in the worktree as in the site, else database/ in the worktree.
		file = filepath.Join(j.Path, "database", filepath.Base(site))
		if rel, err := filepath.Rel(j.Site, site); err == nil && !strings.HasPrefix(rel, "..") {
			file = filepath.Join(j.Path, rel)
		}
	}
	if !exists(file) {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return Database{}, err
		}
		if mode == Copy && exists(site) {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				if exists(site + suffix) {
					if err := fsutil.CloneFile(site+suffix, file+suffix); err != nil {
						return Database{}, err
					}
				}
			}
			fmt.Fprintf(log, "database: copied %s\n", site)
		} else {
			if err := os.WriteFile(file, nil, 0o644); err != nil {
				return Database{}, err
			}
			fmt.Fprintf(log, "database: new %s\n", file)
		}
	}
	set(d.env, log, "DB_DATABASE", file)
	return Database{Engine: "sqlite", Name: file}, nil
}

func (j Job) serverDatabase(ctx context.Context, d *dbEnv, mode string, log io.Writer) (Database, error) {
	if mode == Shared {
		return Database{}, nil
	}
	server := j.server(d, d.connection)
	if server == nil {
		fmt.Fprintf(log, "database: Flock doesn't run the %s server on %s:%d that .env names, so the worktree uses the site's database\n", d.connection, d.host, d.port)
		return Database{}, nil
	}
	if d.parentDB == "" {
		fmt.Fprintln(log, "database: .env names no DB_DATABASE, so the worktree uses the site's database")
		return Database{}, nil
	}
	if j.Database.Engine == d.connection && j.Database.Name != "" {
		set(d.env, log, "DB_DATABASE", j.Database.Name) // made before: kept
		return j.Database, nil
	}

	name := DatabaseName(d.parentDB, j.Vars()["branch"])
	var err error
	if mode == Copy {
		fmt.Fprintf(log, "database: copying %s to %s\n", d.parentDB, name)
		err = server.Copy(ctx, d.parentDB, name, log)
	} else {
		fmt.Fprintf(log, "database: creating %s\n", name)
		err = server.Create(ctx, name)
	}
	switch {
	case errors.Is(err, ErrExists):
		fmt.Fprintf(log, "database: %s exists already and Flock didn't make it, so it stays as it is; the worktree uses the site's database\n", name)
		return Database{}, nil
	case err != nil:
		if ctx.Err() != nil {
			return Database{}, ctx.Err()
		}
		fmt.Fprintf(log, "database: %v — the worktree uses the site's database\n", err)
		return Database{}, nil
	}
	set(d.env, log, "DB_DATABASE", name)
	return Database{Engine: d.connection, Name: name, Service: server.Service()}, nil
}

// SwitchDatabase changes the worktree's database to j.DBMode: its own database is dropped, .env
// points at the site's again, then the new mode's database is set up.
func (j *Job) SwitchDatabase(ctx context.Context, log io.Writer, step func(string)) error {
	step(StepDatabase)
	if err := j.DropDatabase(ctx, log); err != nil {
		return err
	}
	j.Database = Database{}
	if d, err := j.readDBEnv(); err == nil && d.parentDB != "" && d.connection != "sqlite" {
		set(d.env, log, "DB_DATABASE", d.parentDB)
		if err := d.env.Write(d.path); err != nil {
			return err
		}
	}
	return j.setUpDatabase(ctx, log, step)
}

// DropDatabase drops the worktree's own database (j.Database).
func (j Job) DropDatabase(ctx context.Context, log io.Writer) error {
	db := j.Database
	if db.Name == "" {
		return nil
	}
	fmt.Fprintf(log, "database: dropping %s\n", db.Name)
	if db.Engine == "sqlite" {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(db.Name + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	// The server it was made on; recorded before Flock kept that, the one Flock runs for the engine.
	services := []string{db.Service}
	if db.Service == "" {
		services = map[string][]string{"pgsql": {"postgres"}, "mysql": {"mysql", "mariadb"}, "mariadb": {"mysql", "mariadb"}}[db.Engine]
	}
	for _, id := range services {
		if j.Services == nil {
			break
		}
		server, installed := j.Services(id)
		if !installed {
			continue
		}
		if server == nil {
			return fmt.Errorf("%s is stopped: start it to drop %s", id, db.Name)
		}
		return server.Drop(ctx, db.Name)
	}
	// Uninstalled: there is nothing to drop it from.
	fmt.Fprintf(log, "database: Flock doesn't run the server of %s any more, so it isn't dropped\n", db.Name)
	return nil
}

func set(env *Dotenv, log io.Writer, key, value string) {
	if env.Get(key) != value {
		env.Set(key, value)
		fmt.Fprintf(log, ".env: %s=%s\n", key, value)
	}
}
