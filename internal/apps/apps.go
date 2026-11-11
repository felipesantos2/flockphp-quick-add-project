// Package apps creates new project directories from templates configured in apps.conf.
package apps

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
	"path/filepath"
	"slices"
	"strings"
	"time"

	"flock/internal/archive"
	"flock/internal/catalog"
	"flock/internal/config"
	"flock/internal/download"
)

//go:embed apps.conf
var defaultAppsConf string

const downloadTimeout = 120 * time.Second

var (
	// ErrInvalid marks a bad project name, unknown template, destination conflict, or missing DB prerequisite.
	ErrInvalid = errors.New("invalid app request")
	// ErrDownload marks a template download or archive extraction failure.
	ErrDownload = errors.New("template download failed")
	// AllowedDBServices lists the database services supported during app creation, in fallback order.
	AllowedDBServices = []string{"mysql", "mariadb"}
)

// Template is a single entry from apps.conf.
type Template struct {
	Name       string `json:"name"`
	Target     string `json:"-"`
	RequiresDB bool   `json:"requires_db"`
}

// Database describes a background database service available for app creation.
type Database struct {
	ID        string
	Name      string
	Version   string
	Port      int
	Installed bool
	Running   bool
}

// CreateRequest describes an application to create and register as a Flock site.
type CreateRequest struct {
	Root      string
	Name      string
	ParentDir string
	Type      string
	DB        bool
	DBService string
	PHP       string
	Secured   bool
	Services  func(context.Context) ([]Database, error)
	SaveSite  func(config.Site) error
}

// CreateResult reports the site and optional database set up by Create.
type CreateResult struct {
	Site         config.Site
	Database     Database
	WPConfigured bool
}

// Create validates the request, prepares the project folder, creates an optional database, and
// persists the site through req.SaveSite.
func Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	name, err := ValidateName(req.Name)
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	parentDir := strings.TrimSpace(req.ParentDir)
	if !filepath.IsAbs(parentDir) {
		return CreateResult{}, fmt.Errorf("%w: parent directory must be absolute", ErrInvalid)
	}
	parentDir = filepath.Clean(parentDir)
	if info, err := os.Stat(parentDir); err != nil || !info.IsDir() {
		return CreateResult{}, fmt.Errorf("%w: %s is not a directory", ErrInvalid, parentDir)
	}

	appType := req.Type
	if strings.TrimSpace(appType) == "" {
		appType = "php"
	}
	tmpl, err := Find(req.Root, appType)
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	path := filepath.Join(parentDir, req.Name)
	if _, err := os.Lstat(path); err == nil {
		return CreateResult{}, fmt.Errorf("%w: destination %s already exists", ErrInvalid, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return CreateResult{}, err
	}

	site := config.Site{Name: name, Path: path, PHP: req.PHP, Secured: req.Secured}
	cfg, err := config.Load(req.Root)
	if err != nil {
		return CreateResult{}, err
	}
	if _, ok := cfg.FindSite(name); ok {
		return CreateResult{}, fmt.Errorf("%w: a site named %q already exists; choose another project name", ErrInvalid, name)
	}
	if err := cfg.Upsert(site); err != nil {
		return CreateResult{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	var dbSvc Database
	if req.DB || tmpl.RequiresDB || req.DBService != "" {
		if req.Services == nil {
			return CreateResult{}, fmt.Errorf("%w: no running database service found: install or start mysql or mariadb", ErrInvalid)
		}
		services, err := req.Services(ctx)
		if err != nil {
			return CreateResult{}, err
		}
		dbSvc, err = SelectDatabase(services, req.DBService)
		if err != nil {
			return CreateResult{}, err
		}
	}

	if err := CreateDir(ctx, nil, path, tmpl); err != nil {
		return CreateResult{}, err
	}

	var wpConfigured bool
	if dbSvc.ID != "" {
		wpConfigured, err = SetupDatabase(ctx, req.Root, dbSvc.ID, dbSvc.Version, dbSvc.Port, path, name)
		if err != nil {
			return CreateResult{}, err
		}
	}

	if req.SaveSite != nil {
		if err := req.SaveSite(site); err != nil {
			return CreateResult{}, err
		}
	}
	return CreateResult{Site: site, Database: dbSvc, WPConfigured: wpConfigured}, nil
}

// ValidateName checks a project folder name and returns its normalized site hostname.
func ValidateName(folder string) (string, error) {
	name := config.SiteName(folder)
	if folder == "" || folder == "." || folder == ".." ||
		strings.ContainsAny(folder, `/\:`) || strings.TrimSpace(folder) != folder ||
		strings.HasSuffix(folder, ".") || name == "" || len(name) > 63 {
		return "", fmt.Errorf("%q is not a valid project name: use a single folder name that produces a hostname of 1-63 characters", folder)
	}
	return name, nil
}

// SelectDatabase chooses a running MySQL or MariaDB service (explicitly requested or first active).
func SelectDatabase(services []Database, dbService string) (Database, error) {
	if dbService != "" {
		dbID := strings.ToLower(strings.TrimSpace(dbService))
		if !slices.Contains(AllowedDBServices, dbID) {
			return Database{}, fmt.Errorf("%w: unsupported database service %q: use %s", ErrInvalid, dbService, strings.Join(AllowedDBServices, " or "))
		}
		i := slices.IndexFunc(services, func(x Database) bool { return x.ID == dbID })
		switch {
		case i < 0 || !services[i].Installed:
			return Database{}, fmt.Errorf("%w: %s isn't installed: install it with `flock services install %s`", ErrInvalid, dbID, dbID)
		case !services[i].Running:
			return Database{}, fmt.Errorf("%w: %s isn't running: start it in the Flock app", ErrInvalid, services[i].Name)
		}
		return services[i], nil
	}
	for _, id := range AllowedDBServices {
		i := slices.IndexFunc(services, func(x Database) bool { return x.ID == id })
		if i >= 0 && services[i].Installed && services[i].Running {
			return services[i], nil
		}
	}
	return Database{}, fmt.Errorf("%w: no running database service found: install or start mysql or mariadb", ErrInvalid)
}

// Load returns all configured templates sorted by name, merging <root>/apps.conf over the built-ins.
func Load(root string) ([]Template, error) {
	byName, err := loadMap(root)
	if err != nil {
		return nil, err
	}
	out := make([]Template, 0, len(byName))
	for _, tmpl := range byName {
		out = append(out, tmpl)
	}
	slices.SortFunc(out, func(a, b Template) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

// Find resolves template name from the built-in and <root>/apps.conf definitions.
func Find(root, name string) (Template, error) {
	byName, err := loadMap(root)
	if err != nil {
		return Template{}, err
	}
	key := strings.ToLower(strings.TrimSpace(name))
	tmpl, ok := byName[key]
	if !ok {
		names := make([]string, 0, len(byName))
		for k := range byName {
			names = append(names, k)
		}
		slices.Sort(names)
		return Template{}, fmt.Errorf("unsupported app type %q: available types are %s", name, strings.Join(names, ", "))
	}
	return tmpl, nil
}

func loadMap(root string) (map[string]Template, error) {
	templates := make(map[string]Template)
	if err := Parse(defaultAppsConf, templates); err != nil {
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
	if err := Parse(string(data), templates); err != nil {
		return nil, fmt.Errorf("%s: %w", customPath, err)
	}
	return templates, nil
}

// Parse reads apps.conf key=value entries into dst, skipping blank lines, comments, and --- separators.
func Parse(src string, dst map[string]Template) error {
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
		if !validKey(key) {
			return fmt.Errorf("line %d: invalid template key %q", i+1, strings.TrimSpace(k))
		}
		target, opts, hasOpts := strings.Cut(strings.TrimSpace(v), "|")
		tmpl := Template{Name: key, Target: strings.TrimSpace(target)}
		if hasOpts {
			for _, opt := range strings.FieldsFunc(opts, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
				switch strings.ToLower(opt) {
				case "db":
					tmpl.RequiresDB = true
				default:
					return fmt.Errorf("line %d: unknown template option %q", i+1, opt)
				}
			}
		}
		dst[key] = tmpl
	}
	return nil
}

func validKey(key string) bool {
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

// CreateDir populates path from tmpl inside a sibling temporary directory before moving it into place.
func CreateDir(ctx context.Context, client *http.Client, path string, tmpl Template) error {
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

	if err := prepareTemplate(ctx, client, tmpDir, tmpl.Target); err != nil {
		return err
	}
	_ = os.Chmod(tmpDir, 0o755)
	if err := os.Rename(tmpDir, path); err != nil {
		return fmt.Errorf("create project directory: %w", err)
	}
	moved = true
	return nil
}

// SetupDatabase creates database name in the running MySQL or MariaDB service and writes
// wp-config.php when wp-config-sample.php is present.
func SetupDatabase(ctx context.Context, root, dbID, dbVersion string, dbPort int, path, name string) (bool, error) {
	if err := catalog.CreateDatabase(ctx, root, dbID, dbVersion, dbPort, name); err != nil {
		return false, fmt.Errorf("project created at %s, but creating database %s failed: %w", path, name, err)
	}
	configured, err := ConfigureWPConfig(path, name, dbPort)
	if err != nil {
		return false, fmt.Errorf("project created at %s, but configuring wp-config.php failed: %w", path, err)
	}
	return configured, nil
}

func prepareTemplate(ctx context.Context, client *http.Client, dir, spec string) error {
	switch {
	case spec == "":
		return nil
	case strings.EqualFold(spec, "index.php"):
		if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php\n\necho 'Hello from Flock!';\n"), 0o644); err != nil {
			return fmt.Errorf("write index.php: %w", err)
		}
		return nil
	case isHTTPURL(spec):
		return downloadAndExtract(ctx, client, spec, dir)
	case archive.IsArchive(spec):
		if _, err := os.Stat(spec); err != nil {
			return err
		}
		if err := archive.Extract(spec, dir); err != nil {
			return fmt.Errorf("%w: %w", ErrDownload, err)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported template target %q: use empty, index.php, an http(s) URL, or a .zip/.tar.gz/.tgz path", ErrInvalid, spec)
	}
}

func isHTTPURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func downloadAndExtract(ctx context.Context, client *http.Client, rawURL, dir string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: invalid template URL %q: %w", ErrInvalid, rawURL, err)
	}
	if !archive.IsArchive(u.Path) {
		return fmt.Errorf("%w: template URL %q must point to a .zip, .tar.gz or .tgz archive", ErrInvalid, rawURL)
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, downloadTimeout)
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
		return fmt.Errorf("%w: download template: %w", ErrDownload, err)
	}
	if err := archive.Extract(file, dir); err != nil {
		return fmt.Errorf("%w: %w", ErrDownload, err)
	}
	return nil
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

// ConfigureWPConfig writes wp-config.php from wp-config-sample.php when present, filling the local
// MySQL/MariaDB database, credentials and random keys so the web installer only asks for site details.
func ConfigureWPConfig(dir, database string, port int) (bool, error) {
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
