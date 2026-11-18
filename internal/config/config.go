// Package config is Flock's persisted state: sites, parked directories, default versions and ports.
// The CLI edits it and asks the daemon to reload; the daemon only reads it.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// Config is stored as JSON in <root>/config.json.
type Config struct {
	TLD         string   `json:"tld"`
	DefaultPHP  string   `json:"default_php"`
	DefaultNode string   `json:"default_node"`
	Ports       Ports    `json:"ports"`
	Services    Services `json:"services"`
	// Xdebug routes requests that ask for Xdebug (browser extension, XDEBUG_TRIGGER) to a PHP pool
	// that loads it; XdebugBreakpoints also routes sites with PhpStorm breakpoints there. The key
	// is new: the old "xdebug" loaded Xdebug for every request and defaulted to off.
	Xdebug            bool `json:"xdebug_detection"`
	XdebugBreakpoints bool `json:"xdebug_breakpoints"`
	// Dumps is the capture switch: dump()/dd() output, and on PHP versions with Flock's agent
	// (see package capture) also the kinds of events not in CaptureOff, go to Flock.
	Dumps      bool     `json:"dumps"`
	CaptureOff []string `json:"capture_off,omitempty"` // event kinds not captured: "query", "log", ...
	// DumpsInPage keeps dump() output in the page or terminal too (dd() always shows there).
	DumpsInPage bool `json:"dumps_in_page,omitempty"`
	// Channel is which manifest of prebuilt packages Flock follows: "" or "stable", "beta" (new
	// versions earlier, pre-releases) or "experimental" (everything published, for testing;
	// set with `flock channel`, not in the apps).
	Channel string   `json:"channel,omitempty"`
	Parked  []string `json:"parked"`
	Sites   []Site   `json:"sites"`
	// Proxies are names served by a server of their own, e.g. a dashboard or a Docker container (see Proxy).
	Proxies []Proxy `json:"proxies,omitempty"`

	// Worktrees are the git worktrees Flock sets up (see package worktree). WorktreesSince is when
	// Flock first looked for worktrees: older ones are served, but only set up when asked.
	Worktrees      []WorktreeEntry `json:"worktrees,omitempty"`
	WorktreesSince time.Time       `json:"worktrees_since,omitzero"`
}

// Ports are the local ports Flock's services listen on.
type Ports struct {
	HTTP     int `json:"http"`
	HTTPS    int `json:"https"`
	DNS      int `json:"dns"`
	SMTP     int `json:"smtp"`
	Postgres int `json:"postgres"`
	Dumps    int `json:"dumps"`
	Agent    int `json:"agent"` // collector the PHP agent sends dumps and events to
	API      int `json:"api"`   // token-protected HTTP API for the native apps
}

// Services are optional background services; an empty version means not installed.
type Services struct {
	Postgres string `json:"postgres,omitempty"` // the active build, e.g. "18.6-build1"

	// Extras are the services of the catalog (MySQL, Redis, Meilisearch, ...) that are installed,
	// by id, with the active version: the one that runs, on the service's port. Other versions can
	// be installed next to it (see internal/catalog).
	Extras map[string]Extra `json:"extras,omitempty"`

	// Stopped lists installed services the user switched off ("postgres", "valkey", "mysql").
	Stopped []string `json:"stopped,omitempty"`
}

// Extra is an installed catalog service.
type Extra struct {
	Version string `json:"version"`         // e.g. "8.4.6"
	Build   int    `json:"build,omitempty"` // the manifest build installed (0 when unknown)
	Port    int    `json:"port"`
}

// Extra returns the installed catalog service id.
func (s Services) Extra(id string) (Extra, bool) {
	x, ok := s.Extras[id]
	return x, ok && x.Version != ""
}

// SetExtra records an installed catalog service.
func (s *Services) SetExtra(id string, x Extra) {
	if s.Extras == nil {
		s.Extras = map[string]Extra{}
	}
	s.Extras[id] = x
}

// RemoveExtra forgets a catalog service, and that it was switched off.
func (s *Services) RemoveExtra(id string) {
	delete(s.Extras, id)
	if len(s.Extras) == 0 {
		s.Extras = nil
	}
	s.SetRunning(id, true)
}

// Running reports whether an installed service should run.
func (s Services) Running(id string) bool {
	return !slices.Contains(s.Stopped, id)
}

// SetRunning starts or stops a service by editing Stopped.
func (s *Services) SetRunning(id string, on bool) {
	s.Stopped = slices.DeleteFunc(s.Stopped, func(x string) bool { return x == id })
	if !on {
		s.Stopped = append(s.Stopped, id)
	}
}

// Site is a directory served as <name>.<tld> and its subdomains (up to MaxDepth levels).
type Site struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Type is what the site is: TypePHP or TypeNode. Sites stored before types existed have none,
	// which means TypePHP (see Kind); Load fills it in, so they're saved with it.
	Type    string `json:"type,omitempty"`
	PHP     string `json:"php,omitempty"`  // "8.5"; empty means Config.DefaultPHP
	Node    string `json:"node,omitempty"` // major, e.g. "22"; empty means Config.DefaultNode
	Secured bool   `json:"secured"`
	// Domains are extra names for the site (see NormalizeDomain): subdomains ("tenant1") and
	// aliases ("my-app-2.test"). On Windows they go into the hosts file, so they also work where
	// wildcard DNS doesn't.
	Domains []string `json:"domains,omitempty"`
	Parked  bool     `json:"-"` // discovered from a parked directory rather than linked

	// Parent is set on a git worktree of another site (see Worktrees): it is served as
	// <slug>.<parent domain>, named <slug>.<parent>, and its settings follow the parent's.
	// Branch is its branch ("" when HEAD is detached).
	Parent string `json:"-"`
	Branch string `json:"-"`

	// Root is the document root set for the site on this machine, relative to Path ("src/www", "."
	// for Path itself); empty means the project's .flockrc or detection decides (see project.RootFor).
	Root string `json:"root,omitempty"`

	// Server runs the site's PHP: "" through the version's FastCGI pool (PHP-FPM; php-cgi on
	// Windows), or ServerFrankenPHP in a FrankenPHP process of its own on Port, behind the web server.
	Server string `json:"server,omitempty"`
	// Worker is FrankenPHP's worker mode: "" (classic: every request boots the app) or WorkerOctane.
	// It is kept while the site runs on FastCGI, for switching back.
	Worker string `json:"worker,omitempty"`
	Port   int    `json:"port,omitempty"` // FrankenPHP's loopback port, assigned by SetServer
	// Reverb runs the app's Laravel Reverb server on ReverbPort, and the web server sends the site's
	// WebSocket and broadcast requests there. The port is kept while Reverb is off.
	Reverb     bool `json:"reverb,omitempty"`
	ReverbPort int  `json:"reverb_port,omitempty"`
}

// Servers and worker modes of a site (see Site.Server and Site.Worker).
const (
	ServerFastCGI    = "fastcgi" // how the API and CLI name the default, stored as ""
	ServerFrankenPHP = "frankenphp"
	WorkerOctane     = "octane"
)

// FirstSitePort is where the loopback ports of sites' FrankenPHP and Reverb processes start.
const FirstSitePort = 21000

// SitePortID is how UsedPorts names the port of a FrankenPHP site: "frankenphp-<site>", which is
// also the name of its process and log.
func SitePortID(site string) string { return "frankenphp-" + site }

// ReverbPortID is how UsedPorts names the port of a site's Reverb server: "reverb-<site>", which
// is also the name of its process and log.
func ReverbPortID(site string) string { return "reverb-" + site }

// FrankenPHP reports whether the site runs on FrankenPHP.
func (s Site) FrankenPHP() bool { return s.Kind() == TypePHP && s.Server == ServerFrankenPHP }

// SetServer switches site to server (ServerFastCGI, "" or ServerFrankenPHP) with worker ("" or
// WorkerOctane), giving it a loopback port for FrankenPHP the first time. The site becomes linked
// so the setting can be stored; the caller saves it with Upsert.
func (c *Config) SetServer(site *Site, server, worker string) error {
	if site.Kind() != TypePHP {
		return fmt.Errorf("%s is a %s site: only PHP sites run on PHP-FPM or FrankenPHP", site.Name, TypeName(site.Kind()))
	}
	switch server {
	case ServerFastCGI, "":
		server = ""
	case ServerFrankenPHP:
	default:
		return fmt.Errorf("unknown server %q (fastcgi or frankenphp)", server)
	}
	if worker != "" && worker != WorkerOctane {
		return fmt.Errorf("unknown worker mode %q (octane, or empty for classic)", worker)
	}
	site.Server, site.Worker, site.Parked = server, worker, false
	if server == ServerFrankenPHP {
		reverb := 0
		if site.Reverb {
			reverb = site.ReverbPort
		}
		site.Port = c.sitePort(site.Port, SitePortID(site.Name), reverb)
	}
	return nil
}

// SetReverb turns the site's Reverb server on or off, giving it a loopback port the first time.
// The site becomes linked so the setting can be stored; the caller saves it with Upsert.
func (c *Config) SetReverb(site *Site, on bool) {
	site.Reverb, site.Parked = on, false
	if on {
		frankenphp := 0
		if site.FrankenPHP() {
			frankenphp = site.Port
		}
		site.ReverbPort = c.sitePort(site.ReverbPort, ReverbPortID(site.Name), frankenphp)
	}
}

// sitePort keeps port for the process id when nothing else uses it, or finds the first free one.
// sibling is the site's other process's port, which may not be saved yet.
func (c *Config) sitePort(port int, id string, sibling int) int {
	if port != 0 && port != sibling && !c.portTaken(port, id) {
		return port
	}
	for port = FirstSitePort; port == sibling || c.portTaken(port, id); port++ {
	}
	return port
}

// portTaken reports whether a service or a process other than id uses port.
func (c *Config) portTaken(port int, id string) bool {
	other, used := c.UsedPorts("")[port]
	return used && other != id
}

// Default returns the config used when none exists yet. It has no default PHP and Node.js: the
// daemon installs the newest stable ones on the first run and makes them the defaults then.
func Default() *Config {
	return &Config{
		TLD:               "test",
		Xdebug:            true,
		XdebugBreakpoints: true,
		Ports:             Ports{HTTP: 80, HTTPS: 443, DNS: 53535, SMTP: 2525, Postgres: 5432, Dumps: 9912, Agent: 9913, API: 53531},
	}
}

func file(root string) string {
	return filepath.Join(root, "config.json")
}

// Load reads <root>/config.json, falling back to Default when it does not exist.
func Load(root string) (*Config, error) {
	cfg := Default()

	data, err := readFile(file(root))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", file(root), err)
	}
	for i := range cfg.Sites {
		cfg.Sites[i].Type = cfg.Sites[i].Kind()
	}

	return cfg, nil
}

// UsedPorts maps every port a Flock service is configured for to that service's id, leaving out
// the catalog service except.
func (c *Config) UsedPorts(except string) map[int]string {
	used := map[int]string{
		c.Ports.HTTP: "web", c.Ports.HTTPS: "web", c.Ports.DNS: "dns", c.Ports.SMTP: "mail",
		c.Ports.Dumps: "dumps", c.Ports.Agent: "agent", c.Ports.API: "api",
	}
	if c.Services.Postgres != "" {
		used[c.Ports.Postgres] = "postgres"
	}
	for id, x := range c.Services.Extras {
		if id != except && x.Version != "" {
			used[x.Port] = id
		}
	}
	for _, s := range c.Sites {
		if s.FrankenPHP() && s.Port != 0 {
			used[s.Port] = SitePortID(s.Name)
		}
		if s.Reverb && s.ReverbPort != 0 {
			used[s.ReverbPort] = ReverbPortID(s.Name)
		}
	}
	return used
}

// Save writes the config atomically.
func (c *Config) Save(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	tmp := file(root) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}

	return replaceFile(tmp, file(root))
}

// Domain is the primary hostname of a site.
func (c *Config) Domain(s Site) string {
	return s.Name + "." + c.TLD
}

// PHPFor returns the PHP version a site runs on.
func (c *Config) PHPFor(s Site) string {
	if s.PHP != "" {
		return s.PHP
	}
	return c.DefaultPHP
}

// NodeFor returns the Node major a site uses.
func (c *Config) NodeFor(s Site) string {
	if s.Node != "" {
		return s.Node
	}
	return c.DefaultNode
}

// AllSites returns linked sites plus every directory found in parked paths, and the git worktrees
// of both (see worktreeSites). A linked site wins over a parked directory with the same name, and
// so does a proxy: the directory isn't a site then.
// Sorted by name, so a site's worktrees follow it.
func (c *Config) AllSites() []Site {
	byName := map[string]Site{}

	for _, dir := range c.Parked {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			name := SiteName(e.Name())
			if _, proxied := c.FindProxy(name); proxied {
				continue
			}
			path := filepath.Join(dir, e.Name())
			byName[name] = Site{Name: name, Path: path, Type: DetectType(path), Parked: true}
		}
	}

	for _, s := range c.Sites {
		byName[s.Name] = s
	}
	c.worktreeSites(byName)

	sites := make([]Site, 0, len(byName))
	for _, s := range byName {
		sites = append(sites, s)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].Name < sites[j].Name })

	return sites
}

// FindSite looks a site up by name among all sites.
func (c *Config) FindSite(name string) (Site, bool) {
	for _, s := range c.AllSites() {
		if s.Name == name {
			return s, true
		}
	}
	return Site{}, false
}

// SiteForDir returns the site whose path contains dir (deepest match wins). Without a match it
// tries again with symlinks resolved: git records a worktree's real path (/private/tmp/... on
// macOS), a shell may be in /tmp/....
func (c *Config) SiteForDir(dir string) (Site, bool) {
	sites := c.AllSites()
	if s, ok := siteForDir(sites, dir, func(s Site) string { return s.Path }); ok {
		return s, true
	}
	return siteForDir(sites, realPath(dir), func(s Site) string { return realPath(s.Path) })
}

func siteForDir(sites []Site, dir string, pathOf func(Site) string) (Site, bool) {
	var best Site
	bestLen, found := 0, false
	for _, s := range sites {
		path := pathOf(s)
		rel, err := filepath.Rel(path, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if !found || len(path) > bestLen {
			best, bestLen, found = s, len(path), true
		}
	}
	return best, found
}

// Upsert adds or replaces a linked site by name. A site without a type gets the one its folder
// suggests (see DetectType). A worktree has no settings of its own to store: it follows its
// parent site.
func (c *Config) Upsert(site Site) error {
	if site.IsWorktree() {
		return fmt.Errorf("%s is a git worktree of %s and uses its settings — change them on %s", site.Name, site.Parent, site.Parent)
	}
	switch site.Type {
	case "":
		site.Type = DetectType(site.Path)
	case TypePHP, TypeNode:
	default:
		return fmt.Errorf("unknown site type %q (php or node)", site.Type)
	}
	if _, ok := c.FindProxy(site.Name); ok {
		return fmt.Errorf("%s is a proxy's name — remove it with `flock unproxy %s` first, or choose another name", site.Name, site.Name)
	}
	for i, s := range c.Sites {
		if s.Name == site.Name {
			c.Sites[i] = site
			return nil
		}
	}
	c.Sites = append(c.Sites, site)
	return nil
}

// Remove deletes a linked site by name and reports whether it existed.
func (c *Config) Remove(name string) bool {
	for i, s := range c.Sites {
		if s.Name == name {
			c.Sites = append(c.Sites[:i], c.Sites[i+1:]...)
			return true
		}
	}
	return false
}

// SiteName turns a directory name into a valid hostname label.
func SiteName(dir string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(dir) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
