package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/apps"
	"flock/internal/config"
	"flock/internal/project"
)

func newAppCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Create applications",
	}
	cmd.AddCommand(newAppCreateCommand())
	return cmd
}

func newAppCreateCommand() *cobra.Command {
	var appType, dbService, phpVersion string
	var withDB, secure bool

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an application from a template and link it as a Flock site",
		Long: "Creates a new project directory from a template in apps.conf and links it as a\n" +
			"Flock site. Existing folders, sites and proxies are never replaced.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parentDir, err := filepath.Abs(".")
			if err != nil {
				return err
			}
			s, err := load()
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()

			res, err := apps.Create(ctx, apps.CreateRequest{
				Root: s.root, Name: args[0], ParentDir: parentDir, Type: appType,
				DB: withDB, DBService: dbService, PHP: phpVersion, Secured: secure,
				Services: func(ctx context.Context) ([]apps.Database, error) {
					services, err := s.client().Services(ctx)
					if err != nil {
						return nil, err
					}
					return api.AppsDatabases(services), nil
				},
				SaveSite: func(site config.Site) error {
					if err := s.cfg.Upsert(site); err != nil {
						return err
					}
					if err := s.commit(ctx, cmd.ErrOrStderr()); err != nil {
						return fmt.Errorf("project created at %s, but applying site settings failed: %w", site.Path, err)
					}
					return nil
				},
			})
			if err != nil {
				return err
			}

			if res.Database.ID != "" {
				cmd.Printf("database %s ready in %s\n", res.Site.Name, res.Database.Name)
			}
			if res.WPConfigured {
				cmd.Println("configured wp-config.php")
			}
			scheme := "http"
			if res.Site.Secured {
				scheme = "https"
			}
			cmd.Printf("created %s → %s://%s\n", res.Site.Path, scheme, s.cfg.Domain(res.Site))
			if r := project.RootFor(&res.Site); r.State != "" {
				printRoot(cmd, res.Site.Name, res.Site.Path, r)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&appType, "type", "php", "application template (e.g. blank, php, wordpress, or a key from apps.conf)")
	cmd.Flags().BoolVar(&withDB, "db", false, "require a running MySQL or MariaDB service and create a database for the app")
	cmd.Flags().StringVar(&dbService, "db-service", "", "database service to use (mysql or mariadb; implies --db)")
	cmd.Flags().StringVar(&phpVersion, "php", "", "PHP version for this site (default: the global default)")
	cmd.Flags().BoolVar(&secure, "secure", false, "serve over HTTPS")
	return cmd
}
