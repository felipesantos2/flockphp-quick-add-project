// Package mcpserver is Flock's MCP server (`flock mcp`): it lets AI agents read a site's URL,
// versions, logs, dumps and mail, and manage sites, versions and services through the daemon.
//
// It runs over stdio, started by the agent's client (Claude Code, Cursor, ...), and is installed
// once for the user rather than per project: it finds the project from its working directory,
// FLOCK_SITE, or a tool's `site` argument (a site name or a project folder).
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"flock/internal/api"
	"flock/internal/phpext"
)

// Daemon is what the server needs from the Flock daemon; *api.Client implements it.
type Daemon interface {
	Status(ctx context.Context) (api.Status, error)
	Config(ctx context.Context) (api.ConfigView, error)
	Services(ctx context.Context) ([]api.Service, error)
	Catalog(ctx context.Context) (api.Catalog, error)
	PostgresVersions(ctx context.Context) (api.PostgresVersions, error)
	PHPVersions(ctx context.Context) (api.PHPVersions, error)
	CheckPHP(ctx context.Context) (api.PHPVersions, error)
	NodeVersions(ctx context.Context) (api.NodeVersions, error)
	SiteExtensions(ctx context.Context, name string) (phpext.Report, error)

	SetServiceRunning(ctx context.Context, id string, on bool) ([]api.Service, error)
	InstallService(ctx context.Context, id, line string, port int) (api.Catalog, error)
	InstallPostgres(ctx context.Context, major string, extensions ...string) (api.PostgresVersions, error)
	PostgresExtensions(ctx context.Context, major string) (api.PostgresExtensions, error)
	InstallPostgresExtension(ctx context.Context, major, name string) (api.PostgresExtensions, error)
	InstallPHP(ctx context.Context, minor string) (api.PHPVersions, error)
	InstallNode(ctx context.Context, major string) (api.NodeVersions, error)

	LinkSite(ctx context.Context, dir, name string) (api.Status, error)
	Proxy(ctx context.Context, name, upstream string, secured *bool) (api.Status, error)
	UpdateProxy(ctx context.Context, name string, change api.ProxyChange) (api.Status, error)
	StartProxy(ctx context.Context, name string) (api.Status, error)
	StopProxy(ctx context.Context, name string) (api.Status, error)
	UpdateSite(ctx context.Context, name string, change api.SiteChange) (api.Status, error)
	AddDomain(ctx context.Context, name, domain string) (api.Status, error)
	PinSite(ctx context.Context, name, tool, version string) (api.Status, error)
	CreateWorktree(ctx context.Context, req api.WorktreeCreate) (api.WorktreeCreated, error)
	SetupWorktree(ctx context.Context, path string, again bool, db string) (api.WorktreeStatus, error)
	WorktreeSetup(ctx context.Context, path string) (api.WorktreeStatus, error)
	UnpinSite(ctx context.Context, name, tool string) (api.Status, error)
	RestartSite(ctx context.Context, name string) (api.Status, error)
	RestartReverb(ctx context.Context, name string) (api.Status, error)
	StopSite(ctx context.Context, name string) (api.Status, error)
	UpdateSettings(ctx context.Context, s api.Settings) (api.Status, error)
}

// Options configure a server.
type Options struct {
	Version string // Flock's version
	Root    string // Flock's data directory (mail, dumps and logs are read from it directly)
	Daemon  Daemon
	// Dir is the directory the client started the server in, usually the project.
	Dir string
	// Site is FLOCK_SITE: a site name or project folder that wins over Dir.
	Site string
	// Wait is how long an install tool waits before answering that it is still running
	// (default 45s; clients often give up on a tool call after about a minute).
	Wait time.Duration
}

type server struct {
	Options
	bg background
}

// New builds the MCP server with Flock's tools, resources and prompt.
func New(ctx context.Context, opts Options) *mcp.Server {
	if opts.Wait == 0 {
		opts.Wait = 45 * time.Second
	}
	s := &server{Options: opts}

	srv := mcp.NewServer(&mcp.Implementation{Name: "flock", Title: "Flock", Version: opts.Version}, &mcp.ServerOptions{
		Instructions: s.instructions(ctx),
		Capabilities: &mcp.ServerCapabilities{},
	})
	s.addReadTools(srv)
	s.addWriteTools(srv)
	s.addWorktreeTools(srv)
	s.addResources(srv)
	s.addPrompts(srv)
	return srv
}

// Run serves over stdin/stdout until the client disconnects.
func Run(ctx context.Context, opts Options) error {
	return New(ctx, opts).Run(ctx, &mcp.StdioTransport{})
}

// instructions tell the agent how this machine serves the project, before it calls any tool.
func (s *server) instructions(ctx context.Context) string {
	var b strings.Builder
	b.WriteString("Flock is the local PHP development environment on this machine. It serves PHP/Laravel sites on " +
		"local domains (like https://my-app.test), and manages PHP and Node.js versions and services " +
		"(databases, Redis, a mail catcher and a dump server).\n\n")

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	site, err := s.site(ctx, "")
	switch {
	case err == nil:
		info := siteInfoOf(site)
		fmt.Fprintf(&b, "This project is the Flock site %q, served at %s. It uses PHP %s (%s) and Node.js %s (%s).\n\n",
			info.Name, info.URL, info.PHP.Version, describeSource(info.PHP), info.Node.Version, describeSource(info.Node))
		if site.Parent != "" {
			fmt.Fprintf(&b, "It is a git worktree of the site %q, on the branch %q: it uses %s's settings, and Flock set up its "+
				"dependencies and .env (see list_worktrees).\n\n", site.Parent, site.Branch, site.Parent)
		}
	case errors.Is(err, api.ErrDaemonNotRunning):
		b.WriteString("Flock isn't running right now; most tools need it. It starts with the Flock app, or with `flock daemon`.\n\n")
	default:
		b.WriteString("Flock couldn't tell which project this is. Site tools take a `site` argument: a site name, or the " +
			"project's folder. Use list_sites to see the sites, or link_site to serve a new project.\n\n")
	}

	b.WriteString(`- Flock always serves its sites: don't run "php artisan serve" or another web server. Use site_info for the URL.
- Flock runs a Laravel app's Reverb server when update_site's reverb_on turned it on: then don't run "php artisan reverb:start".
- Run php, composer, node and npm as usual: Flock's shims pick the versions the project uses.
- When code uses a PHP extension (imagick, intl, ...), require it in composer.json ("composer require ext-<name>:*"). php_extensions shows what the project requires and what Flock's PHP has.
- Mail the app sends is caught by Flock, not delivered: read it with list_mail and read_mail.
- To debug: get_errors (the Laravel log and PHP errors), get_dumps (dump() and dd() output), get_events (the queries, logs, views, jobs and requests a page or command ran) and service_logs.
- To work on another branch side by side, create_worktree makes a git worktree that Flock serves and sets up at its own URL; git worktrees made any other way are served and set up too.
- If other guidelines describe a different local development server, follow them only when Flock isn't the one serving the project.`)
	return b.String()
}

func describeSource(v versionInfo) string {
	switch v.Source {
	case "isolated":
		return "set for this site in Flock"
	case "project":
		if v.File != "" {
			return "pinned in " + filepath.Base(v.File)
		}
		return "pinned in the project"
	}
	return "Flock's default"
}

// site finds the site a tool is about: arg (a site name, domain or folder), else FLOCK_SITE, else
// the directory the server runs in.
func (s *server) site(ctx context.Context, arg string) (api.SiteStatus, error) {
	status, err := s.Daemon.Status(ctx)
	if err != nil {
		return api.SiteStatus{}, err
	}

	for _, ref := range []string{arg, s.Site} {
		if ref == "" {
			continue
		}
		if site, ok := findSite(status.Sites, ref); ok {
			return site, nil
		}
		return api.SiteStatus{}, fmt.Errorf("there is no Flock site %q — use list_sites to see the sites, or link_site to serve a project", ref)
	}

	if s.Dir != "" {
		if site, ok := siteForDir(status.Sites, s.Dir); ok {
			return site, nil
		}
	}
	return api.SiteStatus{}, errNoSite
}

var errNoSite = errors.New("Flock can't tell which site you mean: pass `site` (a site name or the project's folder), " +
	"use list_sites to see the sites, or link_site to serve the project")

// findSite matches a site name, one of its domains, or a folder inside a site.
func findSite(sites []api.SiteStatus, ref string) (api.SiteStatus, bool) {
	name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(ref, "https://"), "http://"), "/")
	for _, s := range sites {
		if s.Name == name || s.Domain == name {
			return s, true
		}
		for _, d := range s.Domains {
			if d.Host == name {
				return s, true
			}
		}
	}
	if filepath.IsAbs(ref) {
		return siteForDir(sites, ref)
	}
	return api.SiteStatus{}, false
}

// siteForDir returns the site whose folder contains dir (the deepest one).
func siteForDir(sites []api.SiteStatus, dir string) (api.SiteStatus, bool) {
	var best api.SiteStatus
	found := false
	for _, s := range sites {
		rel, err := filepath.Rel(s.Path, filepath.Clean(dir))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if !found || len(s.Path) > len(best.Path) {
			best, found = s, true
		}
	}
	return best, found
}

func ptr[T any](v T) *T { return &v }
