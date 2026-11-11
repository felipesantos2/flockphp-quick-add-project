package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"flock/internal/config"
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
	var appType string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a PHP application and link it as a Flock site",
		Long: "Creates a new folder in the current directory with an index.php and links it as a\n" +
			"Flock site, using the default PHP version. Existing folders and site names are never replaced.\n" +
			"Only the php type is supported for now.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if appType != "php" {
				return fmt.Errorf("unsupported app type %q: supported type is php", appType)
			}
			folder := args[0]
			name := config.SiteName(folder)
			if folder == "" || folder == "." || folder == ".." ||
				strings.ContainsAny(folder, `/\:`) || strings.TrimSpace(folder) != folder ||
				strings.HasSuffix(folder, ".") || name == "" || len(name) > 63 {
				return fmt.Errorf("%q is not a valid project name: use a single folder name that produces a hostname of 1-63 characters", folder)
			}
			path, err := filepath.Abs(folder)
			if err != nil {
				return err
			}
			s, err := load()
			if err != nil {
				return err
			}
			if _, ok := s.cfg.FindSite(name); ok {
				return fmt.Errorf("a site named %q already exists; choose another project name", name)
			}
			site := config.Site{Name: name, Path: path}
			if err := s.cfg.Upsert(site); err != nil {
				return err
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				return fmt.Errorf("create project directory: %w", err)
			}
			if err := os.WriteFile(filepath.Join(path, "index.php"), []byte("<?php\n\necho 'Hello from Flock!';\n"), 0o644); err != nil {
				return fmt.Errorf("project directory created at %s, but writing index.php failed: %w", path, err)
			}
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("project created at %s, but applying site settings failed: %w", path, err)
			}
			cmd.Printf("created %s → http://%s\n", path, s.cfg.Domain(site))
			return nil
		},
	}
	cmd.Flags().StringVar(&appType, "type", "php", "application type (php)")
	return cmd
}
