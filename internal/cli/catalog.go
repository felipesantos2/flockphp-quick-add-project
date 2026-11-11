package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/catalog"
	"flock/internal/postgres"
)

func newServicesCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "services",
		Short: "List, install, update and remove PostgreSQL, MySQL, MariaDB, MongoDB, Valkey, Redis (macOS and Linux), Meilisearch, Typesense and RustFS",
		Long: `Lists the services and their versions. With --json, for scripts: every service with
whether it is installed and running, its port, and the .env settings that connect a
Laravel app to it (env).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			client := s.client()
			if asJSON {
				services, err := client.Services(cmd.Context())
				if err != nil {
					return err
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(services)
			}
			c, err := client.Catalog(cmd.Context())
			if err != nil {
				return err
			}
			pg, err := client.PostgresVersions(cmd.Context())
			if err != nil {
				return err
			}
			printCatalog(cmd.OutOrStdout(), pg, c)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print every service as JSON, for scripts")

	var port int
	var withExtensions bool
	install := &cobra.Command{
		Use:   "install <service> [version] [--port <port>]",
		Short: "Install a service (the newest release of a version line, e.g. `mysql 8.4` or `postgres 17`) and start it",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			line := ""
			if len(args) == 2 {
				line = args[1]
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			if isPostgres(args[0]) {
				if port != 0 {
					return errors.New("--port doesn't apply to PostgreSQL; change its port in the Flock app's settings")
				}
				var extensions []string
				if withExtensions {
					extensions = activeExtensions(s)
				}
				return installPostgres(ctx, s, line, extensions, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			if withExtensions {
				return errors.New("--with-extensions only applies to PostgreSQL")
			}
			return installCatalogService(ctx, s, args[0], line, port, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	install.Flags().IntVar(&port, "port", 0, "port to listen on (default: the usual one, or the next free one)")
	install.Flags().BoolVar(&withExtensions, "with-extensions", false, "PostgreSQL: also install the extensions the active version has (where the new one has a build)")

	var extMajor string
	var open bool
	extensions := &cobra.Command{
		Use:   "extensions postgres [list|install|update|uninstall|dir|pg-config] [extension] [--major <version>]",
		Short: "List, install, update or remove PostgreSQL extensions (PostGIS, pgRouting, pgvector, TimescaleDB) of a version, or print its folder for your own",
		Args:  cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isPostgres(args[0]) {
				return fmt.Errorf("only PostgreSQL has extensions, not %q", args[0])
			}
			action, name := "", ""
			if len(args) > 1 {
				action = args[1]
			}
			if len(args) > 2 {
				name = args[2]
			}
			return postgresExtensions(cmd, action, name, extMajor, open)
		},
	}
	extensions.Flags().StringVar(&extMajor, "major", "", "the PostgreSQL version (default: the active one)")
	extensions.Flags().BoolVar(&open, "open", false, "dir: also open the folder")

	var deleteData bool
	uninstall := &cobra.Command{
		Use:   "uninstall <service> [version] [--delete-data]",
		Short: "Remove a version of a service (the active one unless given); its data stays unless --delete-data",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			line := ""
			if len(args) == 2 {
				line = args[1]
			}
			if isPostgres(args[0]) {
				// The daemon finishes an operation it started even when the CLI is interrupted.
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return uninstallPostgres(ctx, s, line, deleteData, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			client := s.client()
			if line == "" {
				c, err := client.Catalog(cmd.Context())
				if err != nil {
					return err
				}
				if e, _ := c.Find(args[0]); e.Line != "" {
					line = e.Line
				} else {
					return fmt.Errorf("which version? e.g. `flock services uninstall %s <version>`", args[0])
				}
			}
			c, err := client.UninstallService(cmd.Context(), args[0], line, deleteData)
			if err != nil {
				return err
			}
			e, _ := c.Find(args[0])
			cmd.Printf("%s %s uninstalled\n", cmp.Or(e.Name, args[0]), line)
			if e.Installed != "" {
				cmd.Printf("%s %s runs on port %d\n", e.Name, e.Installed, e.Port)
			}
			return nil
		},
	}
	uninstall.Flags().BoolVar(&deleteData, "delete-data", false, "also delete that version's data (PostgreSQL: its databases)")

	use := &cobra.Command{
		Use:   "use <service> <version>",
		Short: "Run another installed version of a service on its port (each version keeps its own data)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if isPostgres(args[0]) {
				return postgresOperation(cmd, args[1], "switching to", func(ctx context.Context, ops pgOps) error {
					return ops.Activate(ctx, args[1])
				})
			}
			s, err := load()
			if err != nil {
				return err
			}
			c, err := s.client().ActivateService(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			e, _ := c.Find(args[0])
			cmd.Printf("%s %s runs on port %d\n", e.Name, e.Installed, e.Port)
			return nil
		},
	}

	update := &cobra.Command{
		Use:   "update <service> [version]",
		Short: "Update a version of a service to its newest release, keeping its data (the active one unless given)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			line := ""
			if len(args) == 2 {
				line = args[1]
			}
			// The daemon finishes an operation it started even when the CLI is interrupted.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			if isPostgres(args[0]) {
				return updatePostgres(ctx, s, line, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			c, err := s.client().Catalog(ctx)
			if err != nil {
				return err
			}
			e, ok := c.Find(args[0])
			if !ok {
				return fmt.Errorf("unknown service %q", args[0])
			}
			if line == "" {
				if line = e.Line; line == "" {
					return fmt.Errorf("which version? e.g. `flock services update %s <version>`", e.ID)
				}
			}
			i := slices.IndexFunc(e.Lines, func(l catalog.LineState) bool { return l.ID == line && l.Installed })
			switch {
			case i < 0:
				return fmt.Errorf("%s %s isn't installed: install it with `flock services install %s %s`", e.Name, line, e.ID, line)
			case !e.Lines[i].Update:
				cmd.Printf("%s %s is up to date\n", e.Name, e.Lines[i].Version)
				return nil
			}
			return installCatalogService(ctx, s, e.ID, line, 0, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}

	list := &cobra.Command{
		Use:   "list <service>",
		Short: "List the versions of a service with what is installed, updates and data sizes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			if isPostgres(args[0]) {
				ops, err := postgresOps(cmd.Context(), s)
				if err != nil {
					return err
				}
				v, err := ops.Versions(cmd.Context())
				if err != nil {
					return err
				}
				printPostgresVersions(cmd.OutOrStdout(), v)
				return nil
			}
			c, err := s.client().Catalog(cmd.Context())
			if err != nil {
				return err
			}
			e, ok := c.Find(args[0])
			if !ok {
				return fmt.Errorf("unknown service %q", args[0])
			}
			printLines(cmd.OutOrStdout(), s.root, e)
			if c.CheckError != "" {
				cmd.Printf("could not look up the newest releases: %s\n", c.CheckError)
			}
			return nil
		},
	}

	createDB := &cobra.Command{
		Use:   "create-db <service> <name>...",
		Short: "Create databases in PostgreSQL, MySQL or MariaDB (no-op when they exist)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			id := strings.ToLower(args[0])
			if isPostgres(id) {
				id = "postgres"
			}
			if !slices.Contains([]string{"postgres", "mysql", "mariadb"}, id) {
				return fmt.Errorf("%s has no databases to create: use postgres, mysql or mariadb", args[0])
			}
			services, err := s.client().Services(cmd.Context())
			if err != nil {
				return err
			}
			i := slices.IndexFunc(services, func(x api.Service) bool { return x.ID == id })
			switch {
			case i < 0 || !services[i].Installed:
				return fmt.Errorf("%s isn't installed: install it with `flock services install %s`", id, id)
			case !services[i].Running:
				return fmt.Errorf("%s isn't running: start it in the Flock app", services[i].Name)
			}
			svc := services[i]
			for _, name := range args[1:] {
				if id == "postgres" {
					err = postgres.CreateDatabase(cmd.Context(), svc.Port, name)
				} else {
					err = catalog.CreateDatabase(cmd.Context(), s.root, id, svc.Version, svc.Port, name)
				}
				if err != nil {
					return fmt.Errorf("create %s: %w", name, err)
				}
				cmd.Printf("database %s ready in %s\n", name, svc.Name)
			}
			return nil
		},
	}

	cmd.AddCommand(install, update, uninstall, use, list, createDB, extensions)
	return cmd
}

// printLines prints the versions of a catalog service like printPostgresVersions does
// PostgreSQL's.
func printLines(out io.Writer, root string, e catalog.Entry) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  VERSION\tINSTALLED\tLATEST\tDATA\t")
	for _, l := range e.Lines {
		marker := " "
		if l.Active {
			marker = "*"
		}
		installed, latest, data := dash(l.Version), dash(l.Latest), "-"
		if l.Update {
			latest += " (update)"
		}
		if l.HasData {
			data = humanSize(postgres.DirSize(catalog.DataDir(root, e.ID, l.ID)))
		}
		if e.Job != nil && e.Job.State == "running" && e.Job.Line == l.ID {
			installed += " (" + strings.TrimSuffix(e.Job.Action, "e") + "ing)"
		}
		fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\t\n", marker, l.ID, installed, latest, data)
	}
	_ = tw.Flush()
}

func printCatalog(out io.Writer, pg postgres.Versions, c api.Catalog) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tSTATE\tVERSIONS")
	fmt.Fprintf(w, "postgres\t%s\t%s\n", postgresState(pg), strings.Join(postgresMajors(pg), ", "))
	for _, e := range c.Services {
		state := "not installed"
		switch {
		case !e.Available:
			state = "not available: " + e.Unavailable
		case e.Job != nil && e.Job.State == "running":
			state = "installing…"
		case e.Installed != "":
			state = fmt.Sprintf("%s on port %d", e.Installed, e.Port)
			if e.Update {
				state += fmt.Sprintf(" (%s available)", e.Latest)
			}
		}
		var lines []string
		for _, l := range e.Lines {
			lines = append(lines, l.ID)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.ID, state, strings.Join(lines, ", "))
	}
	_ = w.Flush()
}

// postgresState is PostgreSQL's STATE in `flock services`: the active version, like a catalog
// service's, with the other installed majors (`flock services list postgres` shows them in full).
func postgresState(v postgres.Versions) string {
	var active postgres.MajorVersion
	var others []string
	for _, m := range v.Majors {
		switch {
		case m.Busy != "":
			return m.Busy + "…"
		case m.Active:
			active = m
		case m.Installed != "":
			others = append(others, m.Installed)
		}
	}
	if active.Installed == "" {
		if len(others) > 0 {
			return strings.Join(others, ", ") + " installed, none active"
		}
		return "not installed"
	}
	state := fmt.Sprintf("%s on port %d", active.Installed, v.Port)
	if active.Update {
		state += fmt.Sprintf(" (%s available)", active.Latest)
	}
	if len(others) > 0 {
		state += "; also " + strings.Join(others, ", ")
	}
	return state
}

func postgresMajors(v postgres.Versions) []string {
	var majors []string
	for _, m := range v.Majors {
		majors = append(majors, m.Major)
	}
	return majors
}

// installCatalogService starts an install in the daemon and follows it until it finishes.
func installCatalogService(ctx context.Context, s *state, id, line string, port int, out, errOut io.Writer) error {
	client := s.client()
	c, err := client.InstallService(ctx, id, line, port)
	if err != nil {
		return err
	}
	p := newProgress(errOut)
	defer p.clear()
	for {
		e, ok := c.Find(id)
		if !ok {
			return fmt.Errorf("unknown service %q", id)
		}
		switch {
		case e.Job == nil:
			p.clear()
			for _, l := range e.Lines {
				if l.ID == line && l.Installed && !l.Active {
					fmt.Fprintf(out, "%s %s installed next to %s, which keeps running; switch with `flock services use %s %s`\n", e.Name, l.Version, e.Installed, id, line)
					return nil
				}
			}
			fmt.Fprintf(out, "%s %s on 127.0.0.1:%d\n", e.Name, e.Installed, e.Port)
			return nil
		case e.Job.State == "failed":
			p.clear()
			return fmt.Errorf("installing %s failed: %s (the log is %s)", e.Name, e.Job.Error, filepath.Join(s.root, "log", id+"-install.log"))
		}
		p.set(jobText(e))

		select {
		case <-ctx.Done():
			// Interrupted: cancel the install in the daemon too.
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = client.CancelService(cancelCtx, id)
			return errors.New("canceled")
		case <-time.After(time.Second):
		}
		if c, err = client.Catalog(ctx); err != nil && ctx.Err() == nil {
			return err
		}
	}
}

func jobText(e catalog.Entry) string {
	target := e.Name
	if e.Job.Version != "" {
		target += " " + e.Job.Version
	}
	switch e.Job.Phase {
	case "downloading":
		if e.Job.Progress >= 0 {
			return fmt.Sprintf("downloading %s (%d%%)…", target, int(e.Job.Progress*100))
		}
		return fmt.Sprintf("downloading %s…", target)
	}
	return fmt.Sprintf("installing %s…", target)
}
