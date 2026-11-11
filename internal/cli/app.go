package cli

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/archive"
	"flock/internal/config"
	"flock/internal/download"
	"flock/internal/project"
)

//go:embed apps.conf
var defaultAppsConf string

const templateDownloadTimeout = 120 * time.Second

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
			templates, err := loadAppTemplates(s.root)
			if err != nil {
				return err
			}
			key := strings.ToLower(strings.TrimSpace(appType))
			tmpl, ok := templates[key]
			if !ok {
				return fmt.Errorf("unsupported app type %q: available types are %s", appType, strings.Join(sortedTemplateNames(templates), ", "))
			}
			if _, err := os.Lstat(path); err == nil {
				return fmt.Errorf("destination %s already exists", path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if _, ok := s.cfg.FindSite(name); ok {
				return fmt.Errorf("a site named %q already exists; choose another project name", name)
			}
			site := config.Site{Name: name, Path: path, PHP: phpVersion, Secured: secure}
			if err := s.cfg.Upsert(site); err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()

			dbSvc, err := resolveAppDatabase(ctx, s, withDB || tmpl.requiresDB, dbService)
			if err != nil {
				return err
			}

			if err := createProjectDir(ctx, nil, path, tmpl.target); err != nil {
				return err
			}
			if err := setupAppDatabase(ctx, cmd, s, dbSvc, path, name); err != nil {
				return err
			}
			if err := s.commit(ctx, cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("project created at %s, but applying site settings failed: %w", path, err)
			}

			scheme := "http"
			if site.Secured {
				scheme = "https"
			}
			cmd.Printf("created %s → %s://%s\n", path, scheme, s.cfg.Domain(site))
			if r := project.RootFor(&site); r.State != "" {
				printRoot(cmd, site.Name, site.Path, r)
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

func resolveAppDatabase(ctx context.Context, s *state, wantsDB bool, dbService string) (api.Service, error) {
	if dbService != "" {
		dbID := strings.ToLower(strings.TrimSpace(dbService))
		if !slices.Contains(appDatabaseServices, dbID) {
			return api.Service{}, fmt.Errorf("unsupported database service %q: use mysql or mariadb", dbService)
		}
		return runningDatabaseService(ctx, s, dbID)
	}
	if !wantsDB {
		return api.Service{}, nil
	}

	services, err := s.client().Services(ctx)
	if err != nil {
		return api.Service{}, err
	}
	svc, ok := firstRunningDatabaseService(services)
	if !ok {
		return api.Service{}, errors.New("no running database service found: install or start mysql or mariadb")
	}
	return svc, nil
}

func setupAppDatabase(ctx context.Context, cmd *cobra.Command, s *state, dbSvc api.Service, path, name string) error {
	if dbSvc.ID == "" {
		return nil
	}
	if err := createDatabase(ctx, s, dbSvc, name); err != nil {
		return fmt.Errorf("project created at %s, but creating database %s failed: %w", path, name, err)
	}
	cmd.Printf("database %s ready in %s\n", name, dbSvc.Name)
	configured, err := configureWPConfig(path, name, dbSvc.Port)
	if err != nil {
		return fmt.Errorf("project created at %s, but configuring wp-config.php failed: %w", path, err)
	}
	if configured {
		cmd.Println("configured wp-config.php")
	}
	return nil
}

type appTemplate struct {
	target     string
	requiresDB bool
}

func loadAppTemplates(root string) (map[string]appTemplate, error) {
	templates := make(map[string]appTemplate)
	if err := parseAppTemplates(defaultAppsConf, templates); err != nil {
		return nil, fmt.Errorf("built-in apps.conf: %w", err)
	}
	if root == "" {
		return templates, nil
	}
	customPath := filepath.Join(root, "apps.conf")
	data, err := os.ReadFile(customPath)
	if errors.Is(err, os.ErrNotExist) {
		return templates, nil
	}
	if err != nil {
		return nil, err
	}
	if err := parseAppTemplates(string(data), templates); err != nil {
		return nil, fmt.Errorf("%s: %w", customPath, err)
	}
	return templates, nil
}

func parseAppTemplates(src string, dst map[string]appTemplate) error {
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "---") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("line %d: expected key=value", i+1)
		}
		key := strings.ToLower(strings.TrimSpace(k))
		if !validTemplateKey(key) {
			return fmt.Errorf("line %d: invalid template key %q", i+1, strings.TrimSpace(k))
		}
		target, opts, hasOpts := strings.Cut(strings.TrimSpace(v), "|")
		tmpl := appTemplate{target: strings.TrimSpace(target)}
		if hasOpts {
			for _, opt := range strings.FieldsFunc(opts, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
				switch strings.ToLower(opt) {
				case "db":
					tmpl.requiresDB = true
				default:
					return fmt.Errorf("line %d: unknown template option %q", i+1, opt)
				}
			}
		}
		dst[key] = tmpl
	}
	return nil
}

func validTemplateKey(key string) bool {
	if key == "" {
		return false
	}
	for i := range len(key) {
		c := key[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

func sortedTemplateNames(templates map[string]appTemplate) []string {
	names := make([]string, 0, len(templates))
	for k := range templates {
		names = append(names, k)
	}
	slices.Sort(names)
	return names
}

func createProjectDir(ctx context.Context, client *http.Client, path, spec string) error {
	tmpDir, err := os.MkdirTemp(filepath.Dir(path), ".flock-app-*")
	if err != nil {
		return fmt.Errorf("create project directory: %w", err)
	}
	moved := false
	defer func() {
		if !moved {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	if err := prepareAppTemplate(ctx, client, tmpDir, spec); err != nil {
		return err
	}
	_ = os.Chmod(tmpDir, 0o755)
	if err := os.Rename(tmpDir, path); err != nil {
		return fmt.Errorf("create project directory: %w", err)
	}
	moved = true
	return nil
}

func prepareAppTemplate(ctx context.Context, client *http.Client, dir, spec string) error {
	switch {
	case spec == "":
		return nil
	case strings.EqualFold(spec, "index.php"):
		if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php\n\necho 'Hello from Flock!';\n"), 0o644); err != nil {
			return fmt.Errorf("write index.php: %w", err)
		}
		return nil
	case isHTTPURL(spec):
		return downloadAndExtractTemplate(ctx, client, spec, dir)
	case archive.IsArchive(spec):
		if _, err := os.Stat(spec); err != nil {
			return err
		}
		return archive.Extract(spec, dir)
	default:
		return fmt.Errorf("unsupported template target %q: use empty, index.php, an http(s) URL, or a .zip/.tar.gz/.tgz path", spec)
	}
}

func isHTTPURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func downloadAndExtractTemplate(ctx context.Context, client *http.Client, rawURL, dir string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid template URL %q: %w", rawURL, err)
	}
	if !archive.IsArchive(u.Path) {
		return fmt.Errorf("template URL %q must point to a .zip, .tar.gz or .tgz archive", rawURL)
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, templateDownloadTimeout)
		defer cancel()
	}

	f, err := os.CreateTemp("", "flock-app-*"+archiveSuffix(u.Path))
	if err != nil {
		return err
	}
	file := f.Name()
	defer os.Remove(file)
	if err := f.Close(); err != nil {
		return err
	}

	if err := download.File(ctx, client, rawURL, file, "", nil); err != nil {
		return fmt.Errorf("download template: %w", err)
	}
	return archive.Extract(file, dir)
}

func archiveSuffix(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".tgz", ".zip"} {
		if strings.HasSuffix(lower, ext) {
			return ext
		}
	}
	return ""
}

// configureWPConfig writes wp-config.php from wp-config-sample.php when present, filling the local
// MySQL/MariaDB database, credentials and random keys so the web installer only asks for site details.
func configureWPConfig(dir, database string, port int) (bool, error) {
	dest := filepath.Join(dir, "wp-config.php")
	if _, err := os.Stat(dest); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	data, err := os.ReadFile(filepath.Join(dir, "wp-config-sample.php"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	content := string(data)
	replacements := [][2]string{
		{"'database_name_here'", "'" + database + "'"},
		{"'username_here'", "'root'"},
		{"'password_here'", "''"},
		{"'localhost'", fmt.Sprintf("'127.0.0.1:%d'", port)},
	}
	for _, r := range replacements {
		if strings.Count(content, r[0]) != 1 {
			return false, nil
		}
		content = strings.Replace(content, r[0], r[1], 1)
	}

	const saltPlaceholder = "put your unique phrase here"
	for strings.Contains(content, saltPlaceholder) {
		var key [32]byte
		if _, err := rand.Read(key[:]); err != nil {
			return false, err
		}
		content = strings.Replace(content, saltPlaceholder, hex.EncodeToString(key[:]), 1)
	}

	if err := os.WriteFile(dest, []byte(content), 0o600); err != nil {
		return false, err
	}
	return true, nil
}
