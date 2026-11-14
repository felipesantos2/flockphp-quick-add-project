package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"flock/internal/catalog"
	"flock/internal/config"
	"flock/internal/postgres"
	"flock/internal/worktree"
)

// dbServers finds the database server Flock runs for an engine of a .env ("pgsql", "mysql",
// "mariadb") on port: the active PostgreSQL, or MySQL or MariaDB, whichever has the port.
func (d *daemon) dbServers(cfg *config.Config) func(engine string, port int) worktree.Server {
	return func(engine string, port int) worktree.Server {
		switch engine {
		case "pgsql":
			if cfg.Services.Postgres != "" && cfg.Services.Running("postgres") && port == cfg.Ports.Postgres {
				return pgServer{bin: postgres.BinDir(d.root, cfg.Services.Postgres), port: port}
			}
		case "mysql", "mariadb":
			for _, id := range []string{"mysql", "mariadb"} {
				if x, ok := cfg.Services.Extra(id); ok && cfg.Services.Running(id) && x.Port == port {
					return mysqlServer{root: d.root, id: id, version: x.Version, port: port}
				}
			}
		}
		return nil
	}
}

// dbServices finds the database server of a Flock service ("postgres", "mysql", "mariadb"):
// whether it's installed, and the server, on its port, while it runs.
func (d *daemon) dbServices(cfg *config.Config) func(id string) (worktree.Server, bool) {
	return func(id string) (worktree.Server, bool) {
		switch id {
		case "postgres":
			if cfg.Services.Postgres == "" {
				return nil, false
			}
			if !cfg.Services.Running(id) {
				return nil, true
			}
			return pgServer{bin: postgres.BinDir(d.root, cfg.Services.Postgres), port: cfg.Ports.Postgres}, true
		case "mysql", "mariadb":
			x, ok := cfg.Services.Extra(id)
			if !ok {
				return nil, false
			}
			if !cfg.Services.Running(id) {
				return nil, true
			}
			return mysqlServer{root: d.root, id: id, version: x.Version, port: x.Port}, true
		}
		return nil, false
	}
}

type pgServer struct {
	bin  string // the client tools
	port int
}

func (pgServer) Service() string { return "postgres" }

func (s pgServer) Create(ctx context.Context, name string) error {
	return pgError(postgres.NewDatabase(ctx, s.port, name, ""))
}

// Copy uses the site's database as a template, which takes no time; a template must have no
// other connections, so while the site's app holds one it dumps and restores instead.
func (s pgServer) Copy(ctx context.Context, from, to string, log io.Writer) error {
	err := postgres.NewDatabase(ctx, s.port, to, from)
	var pgErr *postgres.Error
	if !errors.As(err, &pgErr) || pgErr.Code != postgres.SQLStateTemplateInUse {
		return pgError(err)
	}
	fmt.Fprintf(log, "database: %s is in use, so it is dumped and restored instead\n", from)
	if err := s.Create(ctx, to); err != nil {
		return err
	}
	conn := []string{"-h", "127.0.0.1", "-p", strconv.Itoa(s.port), "-U", "postgres"}
	dump := exec.CommandContext(ctx, filepath.Join(s.bin, exe("pg_dump")), append(conn, "--no-owner", "--no-acl", from)...)
	restore := exec.CommandContext(ctx, filepath.Join(s.bin, exe("psql")), append(conn, "-q", "-v", "ON_ERROR_STOP=1", "-d", to)...)
	if err := pipe(dump, restore, log); err != nil {
		_ = s.Drop(context.WithoutCancel(ctx), to)
		return err
	}
	return nil
}

func (s pgServer) Drop(ctx context.Context, name string) error {
	return postgres.DropDatabase(ctx, s.port, name)
}

func pgError(err error) error {
	var pgErr *postgres.Error
	if errors.As(err, &pgErr) && pgErr.Code == postgres.SQLStateDatabaseExists {
		return worktree.ErrExists
	}
	return err
}

type mysqlServer struct {
	root, id, version string
	port              int
}

func (s mysqlServer) Service() string { return s.id }

func (s mysqlServer) Create(ctx context.Context, name string) error {
	err := catalog.ExecSQL(ctx, s.root, s.id, s.version, s.port, "CREATE DATABASE `"+name+"`")
	if err != nil && strings.Contains(err.Error(), "database exists") { // ERROR 1007
		return worktree.ErrExists
	}
	return err
}

func (s mysqlServer) Copy(ctx context.Context, from, to string, log io.Writer) error {
	dumpTool, err := catalog.Tool(s.root, s.id, s.version, "dump")
	if err != nil {
		return err
	}
	client, err := catalog.Tool(s.root, s.id, s.version, "client")
	if err != nil {
		return err
	}
	if err := s.Create(ctx, to); err != nil {
		return err
	}
	conn := catalog.Connect(s.port)
	dump := exec.CommandContext(ctx, dumpTool, append(conn, "--single-transaction", "--routines", "--triggers", "--events", from)...)
	restore := exec.CommandContext(ctx, client, append(conn, to)...)
	if err := pipe(dump, restore, log); err != nil {
		_ = s.Drop(context.WithoutCancel(ctx), to)
		return err
	}
	return nil
}

func (s mysqlServer) Drop(ctx context.Context, name string) error {
	return catalog.ExecSQL(ctx, s.root, s.id, s.version, s.port, "DROP DATABASE IF EXISTS `"+name+"`")
}

// pipe runs from with its output going into to, both writing their messages to log.
func pipe(from, to *exec.Cmd, log io.Writer) error {
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	from.Stdout, from.Stderr = w, log
	to.Stdin, to.Stdout, to.Stderr = r, log, log
	if err := from.Start(); err != nil {
		r.Close()
		w.Close()
		return err
	}
	err = to.Start()
	// The children have their ends; closing ours lets each see the other exit.
	r.Close()
	w.Close()
	if err != nil {
		_ = from.Process.Kill()
		_ = from.Wait()
		return err
	}
	errTo, errFrom := to.Wait(), from.Wait()
	if errFrom != nil {
		return fmt.Errorf("%s: %w", filepath.Base(from.Path), errFrom)
	}
	if errTo != nil {
		return fmt.Errorf("%s: %w", filepath.Base(to.Path), errTo)
	}
	return nil
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
