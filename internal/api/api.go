// Package api is the local HTTP API between the Flock daemon and its clients
// (the CLI now, the native macOS/Windows apps later). It is served over a unix socket.
package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"flock/internal/binaries"
	"flock/internal/catalog"
	"flock/internal/config"
	"flock/internal/frankenphp"
	"flock/internal/php"
	"flock/internal/postgres"
	"flock/internal/share"
	"flock/internal/supervisor"
)

// ErrDaemonNotRunning is returned when nothing is listening on the socket.
var ErrDaemonNotRunning = errors.New("flock daemon is not running (start it with `flock daemon`)")

// Status describes the running daemon.
type Status struct {
	Version     string    `json:"version"`
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
	DNS         DNSStatus `json:"dns"`
	HTTPPort    int       `json:"http_port"`
	HTTPSPort   int       `json:"https_port"`
	Mail        string    `json:"mail"`
	Dumps       bool      `json:"dumps"`
	CaptureOff  []string  `json:"capture_off"`   // event kinds not captured (see config.Config.CaptureOff)
	DumpsInPage bool      `json:"dumps_in_page"` // dump() shows in the page / terminal too
	// CaptureUnsupported lists installed PHP versions without the agent's extension: only their
	// dumps are captured (through the dump server).
	CaptureUnsupported []string                `json:"capture_unsupported"`
	MailCount          int                     `json:"mail_count"`
	DumpCount          int                     `json:"dump_count"`
	EventCount         int                     `json:"event_count"`
	Postgres           string                  `json:"postgres,omitempty"`
	Extras             []ExtraStatus           `json:"extras,omitempty"` // installed catalog services
	Xdebug             bool                    `json:"xdebug"`           // Xdebug detection; see config.Config.Xdebug
	XdebugBreakpoints  bool                    `json:"xdebug_breakpoints"`
	Sites              []SiteStatus            `json:"sites"`
	Proxies            []ProxyStatus           `json:"proxies,omitempty"` // see config.Proxy
	Processes          []supervisor.ProcStatus `json:"processes"`
	Warnings           []string                `json:"warnings,omitempty"`
	Channel            string                  `json:"channel"` // see ChannelOf; the apps' own updates follow it too
	// Defaults is set while there is no default PHP or Node.js yet: the first run installs the
	// newest stable ones and makes them the defaults.
	Defaults *DefaultsStatus `json:"defaults,omitempty"`
}

// ProxyStatus is a name served by a server of its own, e.g. a Node.js dev server (see config.Proxy).
type ProxyStatus struct {
	Name     string `json:"name"`
	Domain   string `json:"domain"`
	Upstream string `json:"upstream"` // e.g. "http://127.0.0.1:3000"
	Secured  bool   `json:"secured"`
	URL      string `json:"url,omitempty"`
	Problem  string `json:"problem,omitempty"` // why it isn't served, e.g. a site serves its domain
	// Running is set while something answers at the upstream. Server names it when it runs on this
	// machine: "Node.js 22.23.4" (one of Flock's versions), "Node.js", or the program's name.
	Running bool   `json:"running"`
	Server  string `json:"server,omitempty"`
}

// DefaultsStatus is the first run's install of the default PHP and Node.js.
type DefaultsStatus struct {
	PHP  DefaultInstall `json:"php"`
	Node DefaultInstall `json:"node"`
}

// DefaultInstall is the install of one default. Version is "" until the package manifest names it.
type DefaultInstall struct {
	Version string `json:"version,omitempty"`
	State   string `json:"state"` // DefaultWaiting, DefaultInstalling, DefaultFailed or DefaultDone
	Error   string `json:"error,omitempty"`
}

// States of a DefaultInstall: waiting for the package manifest (offline), installing, failed (the
// PHP or Node pane can try again), or done (the default is set).
const (
	DefaultWaiting    = "waiting"
	DefaultInstalling = "installing"
	DefaultFailed     = "failed"
	DefaultDone       = "done"
)

// ExtraStatus is an installed catalog service (MySQL, Redis, ...).
type ExtraStatus struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Port    int    `json:"port"`
	Enabled bool   `json:"enabled"`
	Up      bool   `json:"up"`
}

// SiteStatus is one served site as the daemon sees it.
type SiteStatus struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
	// PHP and Node are the versions the site uses. Each Source is "isolated" (set for the site in
	// Flock), "project" (pinned in the project) or "default". Pinned is what the project pins, in
	// File, also when an isolation wins over it. NodeShadowed is a .nvmrc / .node-version that
	// .flockrc's node= wins over, e.g. ".nvmrc: 20".
	PHP          string `json:"php"`
	PHPSource    string `json:"php_source"`
	PHPPinned    string `json:"php_pinned"`
	PHPFile      string `json:"php_file"`
	Node         string `json:"node"`
	NodeSource   string `json:"node_source"`
	NodePinned   string `json:"node_pinned"`
	NodeFile     string `json:"node_file"`
	NodeShadowed string `json:"node_shadowed"`
	Secured      bool   `json:"secured"`
	Parked       bool   `json:"parked"`
	// Parent is set on a git worktree of another site: that site's name, whose settings it uses.
	// Branch is the worktree's branch ("" when HEAD is detached).
	Parent string `json:"parent,omitempty"`
	Branch string `json:"branch,omitempty"`
	// Worktree is the setup of a worktree Flock set up (nil for others).
	Worktree *WorktreeStatus `json:"worktree,omitempty"`
	URL      string          `json:"url,omitempty"`
	Problem  string          `json:"problem,omitempty"`
	// Notice is something about the site that needs a decision from the user but isn't broken,
	// e.g. that Flock found no document root in it.
	Notice string `json:"notice,omitempty"`
	// Root is the document root, relative to Path ("." for Path itself). RootSource is "isolated"
	// (set for the site on this machine), "project" (root= in the project's .flockrc) or "default"
	// (detected: public/, www/, web/ or webroot/ with an index.php, else Path). RootPinned is what
	// the project's .flockrc says, in RootFile, also when an isolation wins over it.
	Root       string `json:"root"`
	RootSource string `json:"root_source"`
	RootPinned string `json:"root_pinned"`
	RootFile   string `json:"root_file"`
	// RootState is "" when the site is served from Root, else why Flock serves its page instead:
	// "missing" (the root set for it isn't a folder; see Problem), "not_found" (a Composer
	// project without an index.php in Path or a conventional root; see Notice) or "empty" (Root
	// has no index.php or index.html). RootSuggestions are then the folders with an index.php
	// that could be the root.
	RootState       string   `json:"root_state,omitempty"`
	RootSuggestions []string `json:"root_suggestions,omitempty"`
	// Debugging is set while PhpStorm breakpoints send every request of the site to Xdebug.
	Debugging bool `json:"debugging,omitempty"`
	// Domains are the site's extra domains (subdomains and aliases), in the order they were added.
	Domains []SiteDomain `json:"domains"`
	// Server is what runs the site's PHP: "fastcgi" (the version's PHP-FPM pool; php-cgi on
	// Windows) or "frankenphp". Worker is FrankenPHP's worker mode: "" (classic) or "octane".
	// OctaneAvailable is set when the project has Laravel Octane installed.
	Server          string `json:"server"`
	Worker          string `json:"worker"`
	OctaneAvailable bool   `json:"octane_available"`
	// FrankenPHP is the state of the site's FrankenPHP process, when Server is "frankenphp". Until
	// it runs, the site is served through FastCGI.
	FrankenPHP *SiteFrankenPHP `json:"frankenphp,omitempty"`
	// Boost is whether the project uses Laravel Boost and has Flock's guideline (see package boost):
	// "" (no Boost), "missing", "current", "outdated" or "custom".
	Boost string `json:"boost,omitempty"`
	// Share is the site's tunnel (`flock share`), while there is one.
	Share *share.Status `json:"share,omitempty"`
}

// FrankenPHP states of a site.
const (
	FrankenPHPRunning     = "running"     // serving the site
	FrankenPHPInstalling  = "installing"  // the build for the site's PHP version is downloading
	FrankenPHPFailed      = "failed"      // see Problem; the site is served through FastCGI
	FrankenPHPUnavailable = "unavailable" // no build for this platform or PHP version; see Problem
)

// SiteFrankenPHP is a site's FrankenPHP process.
type SiteFrankenPHP struct {
	State      string  `json:"state"`
	Version    string  `json:"version,omitempty"`     // FrankenPHP, e.g. "1.13.0"
	PHPVersion string  `json:"php_version,omitempty"` // the embedded PHP, e.g. "8.4.14"
	Port       int     `json:"port"`                  // loopback port the web server proxies to
	Progress   float64 `json:"progress"`              // while installing: 0..1, or -1 when unknown
	Problem    string  `json:"problem,omitempty"`
	Log        string  `json:"log"` // name for /v1/logs/services/{name}
}

// SiteDomain is one of a site's extra domains.
type SiteDomain struct {
	Entry   string `json:"entry"` // as stored and passed to the API: "tenant1" or "my-app-2.test"
	Host    string `json:"host"`  // the full name: "tenant1.app.test"
	Alias   bool   `json:"alias"` // a name of its own (with its subdomains) rather than a subdomain
	URL     string `json:"url,omitempty"`
	Problem string `json:"problem,omitempty"` // why it isn't served, e.g. another site uses it
}

// DNSStatus describes how site names resolve.
type DNSStatus struct {
	TLD  string `json:"tld"`
	Addr string `json:"addr"` // the embedded DNS server, when it runs
	// Wildcard reports whether every name under the TLD resolves (any subdomain, at any depth).
	// When it doesn't, WildcardProblem says why. On Windows (the DNS rule is missing or overridden
	// by a policy) the sites' domains and extra domains still resolve, through the hosts file; on
	// macOS (/etc/resolver/<tld> missing or overridden) nothing under the TLD does.
	Wildcard        bool   `json:"wildcard"`
	WildcardProblem string `json:"wildcard_problem,omitempty"`
	MaxDepth        int    `json:"max_depth"` // subdomain levels below a site that are served
}

// Settings are the global toggles a UI can change; nil fields stay as they are.
type Settings struct {
	Xdebug            *bool `json:"xdebug"`
	XdebugBreakpoints *bool `json:"xdebug_breakpoints"`
	Dumps             *bool `json:"dumps"`
	// CaptureOff replaces the list of event kinds not captured.
	CaptureOff  *[]string `json:"capture_off"`
	DumpsInPage *bool     `json:"dumps_in_page"`
	DefaultPHP  *string   `json:"default_php"`
	DefaultNode *string   `json:"default_node"`
	// Channel is "stable", "beta" or "experimental" (see config.Config.Channel).
	Channel *string `json:"channel"`
}

// Backend is what the API needs from the daemon.
type Backend interface {
	Status() Status
	Reload() error
	Root() string
	// Mutate loads config.json, applies change, saves it and reloads the daemon.
	Mutate(change func(*config.Config) error) error
	// Services describes every service with its live state.
	Services() []Service
	// PHP installs PHP versions from the binaries manifest.
	PHP() *php.Manager
	// ReleasePHP stops whatever runs a PHP version so its files can be removed (Windows locks them).
	ReleasePHP(minor string)
	// PostgresVersions manages the PostgreSQL majors (nil when unavailable).
	PostgresVersions() *postgres.Manager
	// Catalog manages MySQL, Redis, Meilisearch and the other catalog services (nil when unavailable).
	Catalog() *catalog.Manager
	// FrankenPHP installs FrankenPHP builds (nil when unavailable).
	FrankenPHP() *frankenphp.Manager
	// Binaries is the package manifest of the configured channel (nil when there is none).
	Binaries() *binaries.Catalog
	// RestartSite restarts a FrankenPHP site's process, e.g. so Octane's workers load changed code.
	RestartSite(name string) error
	// Shares runs the sites' tunnels (nil when unavailable); StartShare starts one after checking
	// that the site can be shared, with the app's debug mode on for visitors when debug is set.
	Shares() *share.Manager
	StartShare(site string, allow []string, debug bool) error
	// CreateWorktree creates a git worktree of a site and starts its setup.
	CreateWorktree(ctx context.Context, req WorktreeCreate) (WorktreeCreated, error)
	// SiteBranches lists the git branches of a site, for where a new worktree's branch starts.
	SiteBranches(ctx context.Context, name string) (WorktreeBranches, error)
	// SetupWorktree starts the setup of the git worktree at path (ErrNotWorktree when it isn't
	// one of a site's) with the database mode db ("" for flock.yml's), or joins the one that runs;
	// one set up already is left alone unless again.
	SetupWorktree(path string, again bool, db string) (WorktreeStatus, error)
	// SwitchWorktreeDatabase gives the worktree a database by mode as its job, dropping the one
	// Flock made for it.
	SwitchWorktreeDatabase(path, mode string) (WorktreeStatus, error)
	// WorktreeSetup is the state of the worktree's setup, nil when Flock hasn't set it up.
	WorktreeSetup(path string) (*WorktreeStatus, error)
	// RemoveWorktree runs the worktree's before_delete hook, removes it with git and drops the
	// database Flock made for it, returning what they printed.
	RemoveWorktree(ctx context.Context, path string, force bool) (string, error)
}

// NewHandler builds the API routes. A non-empty token requires `Authorization: Bearer <token>`
// (used for the TCP listener the native apps talk to; the CLI's unix socket is protected by file permissions).
func NewHandler(b Backend, token string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, b.Status())
	})
	mux.HandleFunc("POST /v1/reload", func(w http.ResponseWriter, _ *http.Request) {
		if err := b.Reload(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	})
	mux.HandleFunc("POST /v1/settings", func(w http.ResponseWriter, r *http.Request) {
		var settings Settings
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&settings); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid settings: %w", err))
			return
		}
		err := b.Mutate(func(cfg *config.Config) error { return applySettings(b.Root(), cfg, settings) })
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	})

	registerConfig(mux, b)
	registerApps(mux, b)
	registerPHPVersions(mux, b)
	registerExtensions(mux, b)
	registerNodeVersions(mux, b)
	registerPostgresVersions(mux, b)
	registerCatalog(mux, b)
	registerFrankenPHP(mux, b)
	registerMail(mux, b)
	registerDumps(mux, b)
	registerEvents(mux, b)
	registerLogs(mux, b)
	registerMCP(mux, b)
	registerBoost(mux, b)
	registerShares(mux, b)
	registerWorktrees(mux, b)

	if token == "" {
		return mux
	}
	return requireToken(token, mux)
}

func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			writeError(w, http.StatusUnauthorized, errors.New("missing or invalid token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// Client talks to the daemon over its socket.
type Client struct {
	http *http.Client
}

// NewClient creates a client for the daemon listening on socket.
func NewClient(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}

	return &Client{http: &http.Client{Transport: transport, Timeout: 5 * time.Second}}
}

// Status fetches the daemon status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	err := c.get(ctx, "/v1/status", &status)

	return status, err
}

// Reload asks the daemon to apply config.json and returns the resulting status.
func (c *Client) Reload(ctx context.Context) (Status, error) {
	var status Status
	err := c.do(ctx, http.MethodPost, "/v1/reload", &status)

	return status, err
}

// UpdateSettings changes the given global settings and returns the resulting status.
func (c *Client) UpdateSettings(ctx context.Context, s Settings) (Status, error) {
	var status Status
	err := c.send(ctx, http.MethodPost, "/v1/settings", s, &status)
	return status, err
}

// writeTimeout is how long any request but a GET may take, at least.
const writeTimeout = 30 * time.Second

// slow is the client for requests that wait on the network or copy files (checks, installs).
func (c *Client) slow() *Client {
	return c.timeout(2 * time.Minute)
}

// timeout is the client for requests that may take up to d (installs, switching servers).
func (c *Client) timeout(d time.Duration) *Client {
	return &Client{http: &http.Client{Transport: c.http.Transport, Timeout: d}}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, out)
}

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	return c.send(ctx, method, path, nil, out)
}

// send makes a request with body (encoded as JSON unless nil) and decodes the answer into out.
func (c *Client) send(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://flockd"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := c.http
	// A change reloads the daemon, which may start PHP: the first start after an install can take
	// several seconds while an antivirus scans it.
	if method != http.MethodGet && client.Timeout < writeTimeout {
		client = &http.Client{Transport: client.Transport, Timeout: writeTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return ErrDaemonNotRunning
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Error != "" {
			return errors.New(body.Error)
		}
		return fmt.Errorf("%s %s: unexpected status %s", method, path, resp.Status)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
