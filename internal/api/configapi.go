package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"flock/internal/binaries"
	"flock/internal/capture"
	"flock/internal/config"
	"flock/internal/frankenphp"
	"flock/internal/node"
	"flock/internal/php"
	"flock/internal/ports"
	"flock/internal/project"
	"flock/internal/reverb"
)

// Service is one background service as shown in the Services settings.
type Service struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
	// Available is false for a service Flock can't run on this platform; Unavailable says why.
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable,omitempty"`
	Version     string `json:"version"`
	Port        int    `json:"port"`
	Installed   bool   `json:"installed"`
	Enabled     bool   `json:"enabled"`
	Running     bool   `json:"running"`
	Stoppable   bool   `json:"stoppable"`
	Env         string `json:"env"`               // .env snippet to connect a Laravel app
	Log         string `json:"log"`               // name for /v1/logs/services/{name}
	URL         string `json:"url"`               // connection URL (e.g. for TablePlus)
	Problem     string `json:"problem,omitempty"` // why it isn't working, when that isn't simply "stopped"
}

// ConfigView is config.json plus installed versions, for the settings UI.
type ConfigView struct {
	TLD               string       `json:"tld"`
	DefaultPHP        string       `json:"default_php"`
	DefaultNode       string       `json:"default_node"`
	Xdebug            bool         `json:"xdebug"`
	XdebugBreakpoints bool         `json:"xdebug_breakpoints"`
	Dumps             bool         `json:"dumps"`
	CaptureOff        []string     `json:"capture_off"`
	DumpsInPage       bool         `json:"dumps_in_page"`
	Channel           string       `json:"channel"` // "stable", "beta" or "experimental"
	Root              string       `json:"root"`
	Ports             config.Ports `json:"ports"`
	Parked            []string     `json:"parked"`
	PHP               []PHPVersion `json:"php"`
	Node              []string     `json:"node"`
}

// PHPVersion is one installed PHP minor version.
type PHPVersion struct {
	Version         string `json:"version"`
	FullVersion     string `json:"full_version"`
	Ini             string `json:"ini"`
	XdebugAvailable bool   `json:"xdebug_available"`
}

// errConflict and errInvalid map to HTTP 409 and 400.
var (
	errConflict = errors.New("conflict")
	errInvalid  = errors.New("invalid request")
)

func statusFor(err error) int {
	switch {
	case errors.Is(err, errConflict):
		return http.StatusConflict
	case errors.Is(err, errInvalid):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, args...))
}

// pinSite pins a version, or with tool "root" the document root, into the site's project (see
// project.PinSite and project.PinSiteRoot), storing the site when its isolation was lifted.
func pinSite(cfg *config.Config, site config.Site, tool, version string) error {
	var lifted bool
	var err error
	if tool == project.Root {
		_, lifted, err = project.PinSiteRoot(&site, version)
	} else {
		_, lifted, err = project.PinSite(cfg, &site, tool, version)
	}
	if err != nil {
		if errors.Is(err, project.ErrInvalid) {
			return invalid("%v", err)
		}
		return err
	}
	if lifted {
		if err := cfg.Upsert(site); err != nil {
			return invalid("%v", err)
		}
	}
	return nil
}

// setServer switches a site's server. Switching to FrankenPHP retries a failed install of the build
// for the site's PHP version; the reload starts installing a missing one.
func setServer(b Backend, cfg *config.Config, site *config.Site, server, worker string) error {
	m := b.FrankenPHP()
	if server == config.ServerFrankenPHP {
		if m == nil || !m.Available() {
			return invalid("FrankenPHP isn't available for this platform")
		}
		minor := project.PHPFor(cfg, site, site.Path).Version
		if !frankenphp.Supports(minor) {
			return invalid("FrankenPHP needs PHP %s or newer (%s uses %s)", frankenphp.MinimumMinor, site.Name, minor)
		}
		m.Dismiss(minor)
	}
	if err := cfg.SetServer(site, server, worker); err != nil {
		return invalid("%v", err)
	}
	return nil
}

func applySettings(root string, cfg *config.Config, s Settings) error {
	if s.Xdebug != nil {
		cfg.Xdebug = *s.Xdebug
	}
	if s.XdebugBreakpoints != nil {
		cfg.XdebugBreakpoints = *s.XdebugBreakpoints
	}
	if s.Dumps != nil {
		cfg.Dumps = *s.Dumps
	}
	if s.CaptureOff != nil {
		if err := capture.Valid(*s.CaptureOff); err != nil {
			return invalid("%v", err)
		}
		cfg.CaptureOff = nil
		for _, k := range capture.Kinds { // known kinds once each, in a stable order
			if slices.Contains(*s.CaptureOff, k) {
				cfg.CaptureOff = append(cfg.CaptureOff, k)
			}
		}
	}
	if s.DumpsInPage != nil {
		cfg.DumpsInPage = *s.DumpsInPage
	}
	if s.DefaultPHP != nil {
		installed, _ := php.Installed(root)
		if !slices.Contains(installed, *s.DefaultPHP) {
			return invalid("PHP %s is not installed", *s.DefaultPHP)
		}
		cfg.DefaultPHP = *s.DefaultPHP
	}
	if s.Channel != nil {
		ch, err := binaries.ParseChannel(*s.Channel)
		if err != nil {
			return invalid("%v", err)
		}
		cfg.Channel = string(ch)
		if ch == binaries.Stable {
			cfg.Channel = ""
		}
	}
	if s.DefaultNode != nil {
		if *s.DefaultNode == "" {
			return invalid("default_node must not be empty")
		}
		cfg.DefaultNode = *s.DefaultNode
	}
	return nil
}

func decodeBody(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v); err != nil {
		return invalid("%v", err)
	}
	return nil
}

// applyProxyChange changes p as c says and stores it. A command's folder must exist.
func applyProxyChange(cfg *config.Config, p config.Proxy, c ProxyChange) error {
	if c.Upstream != nil {
		upstream, err := config.NormalizeUpstream(*c.Upstream)
		if err != nil {
			return invalid("%v", err)
		}
		p.Upstream = upstream
	}
	if c.Secured != nil {
		p.Secured = *c.Secured
	}
	if c.Command != nil {
		p.Command = strings.TrimSpace(*c.Command)
	}
	if c.StopCommand != nil {
		p.StopCommand = strings.TrimSpace(*c.StopCommand)
	}
	if c.Dir != nil {
		p.Dir = filepath.Clean(strings.TrimSpace(*c.Dir))
		if info, err := os.Stat(p.Dir); err != nil || !info.IsDir() {
			return invalid("%s is not a folder", *c.Dir)
		}
	}
	if p.Command == "" {
		p.Dir, p.StopCommand = "", ""
	}
	if err := cfg.UpsertProxy(p); err != nil {
		return invalid("%v", err)
	}
	return nil
}

func registerConfig(mux *http.ServeMux, b Backend) {
	// mutate runs change through the backend and answers with the new status.
	mutate := func(w http.ResponseWriter, change func(*config.Config) error) {
		if err := b.Mutate(change); err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	}

	mux.HandleFunc("GET /v1/config", func(w http.ResponseWriter, _ *http.Request) {
		cfg, err := config.Load(b.Root())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, configView(b.Root(), cfg))
	})

	mux.HandleFunc("POST /v1/sites", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path    string `json:"path"`
			Name    string `json:"name"`
			PHP     string `json:"php"`
			Secured bool   `json:"secured"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			if !filepath.IsAbs(body.Path) {
				return invalid("path must be absolute")
			}
			if info, err := os.Stat(body.Path); err != nil || !info.IsDir() {
				return invalid("%s is not a directory", body.Path)
			}
			name := body.Name
			if name == "" {
				name = filepath.Base(body.Path)
			}
			if err := cfg.Upsert(config.Site{Name: config.SiteName(name), Path: filepath.Clean(body.Path), PHP: body.PHP, Secured: body.Secured}); err != nil {
				return invalid("%v", err)
			}
			return nil
		})
	})

	mux.HandleFunc("PATCH /v1/sites/{name}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Type    *string `json:"type"` // "php" or "node"
			Secured *bool   `json:"secured"`
			PHP     *string `json:"php"`
			Node    *string `json:"node"`
			// Server is "fastcgi" or "frankenphp"; Worker "" (classic) or "octane" (FrankenPHP's worker mode).
			Server *string `json:"server"`
			Worker *string `json:"worker"`
			Reverb *bool   `json:"reverb"`
			// Root is the document root on this machine, relative to the project ("" lets the
			// project's .flockrc or detection decide again).
			Root *string `json:"root"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			site, ok := cfg.FindSite(r.PathValue("name"))
			if !ok {
				return invalid("no site named %s", r.PathValue("name"))
			}
			if body.Type != nil {
				if err := cfg.SetType(&site, *body.Type); err != nil {
					return invalid("%v", err)
				}
			}
			if body.Secured != nil {
				site.Secured = *body.Secured
			}
			if body.PHP != nil {
				installed, _ := php.Installed(b.Root())
				if *body.PHP != "" && !slices.Contains(installed, *body.PHP) {
					return invalid("PHP %s is not installed", *body.PHP)
				}
				site.PHP = *body.PHP
			}
			if body.Node != nil {
				if *body.Node != "" {
					if err := project.ValidVersion(project.Node, *body.Node); err != nil {
						return invalid("%v", err)
					}
				}
				site.Node = *body.Node
			}
			if body.Root != nil {
				site.Root = ""
				if *body.Root != "" {
					root, err := project.ValidRoot(site.Path, *body.Root)
					if err != nil {
						return invalid("%v", err)
					}
					site.Root = root
				}
			}
			if body.Server != nil || body.Worker != nil {
				server, worker := site.Server, site.Worker
				if body.Server != nil {
					server = *body.Server
				}
				if body.Worker != nil {
					worker = *body.Worker
				}
				if err := setServer(b, cfg, &site, server, worker); err != nil {
					return err
				}
			}
			if body.Reverb != nil {
				if *body.Reverb && !reverb.Installed(site.Path) {
					return invalid("Laravel Reverb isn't installed in %s — run `php artisan install:broadcasting --reverb`", site.Path)
				}
				cfg.SetReverb(&site, *body.Reverb)
			}
			// A parked site becomes linked so its settings can be stored.
			site.Parked = false
			if err := cfg.Upsert(site); err != nil {
				return invalid("%v", err)
			}
			return nil
		})
	})

	// Extra domains: a subdomain ("tenant1") or an alias ("my-app-2.test"), see config.NormalizeDomain.
	mux.HandleFunc("POST /v1/sites/{name}/domains", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Domain string `json:"domain"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			site, ok := cfg.FindSite(r.PathValue("name"))
			if !ok {
				return invalid("no site named %s", r.PathValue("name"))
			}
			if _, err := cfg.AddDomain(&site, body.Domain); err != nil {
				return invalid("%v", err)
			}
			site.Parked = false // a parked site becomes linked so the domain can be stored
			if err := cfg.Upsert(site); err != nil {
				return invalid("%v", err)
			}
			return nil
		})
	})

	mux.HandleFunc("DELETE /v1/sites/{name}/domains/{domain}", func(w http.ResponseWriter, r *http.Request) {
		mutate(w, func(cfg *config.Config) error {
			site, ok := cfg.FindSite(r.PathValue("name"))
			if !ok {
				return invalid("no site named %s", r.PathValue("name"))
			}
			if !cfg.RemoveDomain(&site, r.PathValue("domain")) {
				return invalid("%s is not an extra domain of %s", r.PathValue("domain"), site.Name)
			}
			if err := cfg.Upsert(site); err != nil {
				return invalid("%v", err)
			}
			return nil
		})
	})

	// Pins a version into the project's files (by default the version the site uses now) and
	// lifts the site's isolation for that tool, so the project file decides from now on. With
	// tool "root", version is the document root (by default the one the site uses now).
	mux.HandleFunc("POST /v1/sites/{name}/pin", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tool    string `json:"tool"`
			Version string `json:"version"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			site, ok := cfg.FindSite(r.PathValue("name"))
			if !ok {
				return invalid("no site named %s", r.PathValue("name"))
			}
			return pinSite(cfg, site, body.Tool, body.Version)
		})
	})

	mux.HandleFunc("DELETE /v1/sites/{name}/pin/{tool}", func(w http.ResponseWriter, r *http.Request) {
		mutate(w, func(cfg *config.Config) error {
			site, ok := cfg.FindSite(r.PathValue("name"))
			if !ok {
				return invalid("no site named %s", r.PathValue("name"))
			}
			tool := r.PathValue("tool")
			if tool != project.PHP && tool != project.Node && tool != project.Root {
				return invalid("unknown tool %q (php, node or root)", tool)
			}
			changed, err := project.Unpin(site.Path, tool)
			if err != nil {
				return err
			}
			if len(changed) == 0 {
				if tool == project.Root {
					return invalid("%s has no document root pinned in %s", site.Name, site.Path)
				}
				return invalid("%s has no %s version pinned in %s", site.Name, tool, site.Path)
			}
			return nil
		})
	})

	mux.HandleFunc("DELETE /v1/sites/{name}", func(w http.ResponseWriter, r *http.Request) {
		mutate(w, func(cfg *config.Config) error {
			if site, ok := cfg.FindSite(r.PathValue("name")); ok && site.IsWorktree() {
				return fmt.Errorf("%w: %s is a git worktree of %s; it stops being served when the worktree is removed", errConflict, site.Name, site.Parent)
			}
			if !cfg.Remove(r.PathValue("name")) {
				return fmt.Errorf("%w: %s is served from a parked directory; remove its parent from the project folders instead", errConflict, r.PathValue("name"))
			}
			return nil
		})
	})

	// A proxy passes its name to a server of its own (see config.Proxy). Posting a proxy's name
	// again changes it; what's left out is kept (a new proxy is served over HTTP, without a command).
	mux.HandleFunc("POST /v1/proxies", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
			ProxyChange
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			if body.Upstream == nil {
				return invalid("give the proxy's upstream: a port (3000), host:port or URL")
			}
			p := config.Proxy{Name: config.SiteName(body.Name)}
			if existing, ok := cfg.FindProxy(p.Name); ok {
				p = existing
			}
			return applyProxyChange(cfg, p, body.ProxyChange)
		})
	})

	mux.HandleFunc("PATCH /v1/proxies/{name}", func(w http.ResponseWriter, r *http.Request) {
		var body ProxyChange
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			p, ok := cfg.FindProxy(r.PathValue("name"))
			if !ok {
				return invalid("no proxy named %q", r.PathValue("name"))
			}
			return applyProxyChange(cfg, p, body)
		})
	})

	proxyAction := func(do func(string) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if err := do(r.PathValue("name")); err != nil {
				writeError(w, statusFor(err), err)
				return
			}
			writeJSON(w, http.StatusOK, b.Status())
		}
	}
	mux.HandleFunc("POST /v1/proxies/{name}/start", proxyAction(b.StartProxy))
	mux.HandleFunc("POST /v1/proxies/{name}/stop", proxyAction(b.StopProxy))

	mux.HandleFunc("DELETE /v1/proxies/{name}", func(w http.ResponseWriter, r *http.Request) {
		mutate(w, func(cfg *config.Config) error {
			if !cfg.RemoveProxy(r.PathValue("name")) {
				return invalid("no proxy named %q", r.PathValue("name"))
			}
			return nil
		})
	})

	parked := func(w http.ResponseWriter, r *http.Request, add bool) {
		var body struct {
			Path string `json:"path"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			path := filepath.Clean(body.Path)
			if !filepath.IsAbs(path) {
				return invalid("path must be absolute")
			}
			i := slices.Index(cfg.Parked, path)
			switch {
			case add && i < 0:
				if info, err := os.Stat(path); err != nil || !info.IsDir() {
					return invalid("%s is not a directory", path)
				}
				cfg.Parked = append(cfg.Parked, path)
			case !add && i >= 0:
				cfg.Parked = slices.Delete(cfg.Parked, i, i+1)
			case !add:
				return invalid("%s is not parked", path)
			}
			return nil
		})
	}
	mux.HandleFunc("POST /v1/parked", func(w http.ResponseWriter, r *http.Request) { parked(w, r, true) })
	mux.HandleFunc("DELETE /v1/parked", func(w http.ResponseWriter, r *http.Request) { parked(w, r, false) })

	mux.HandleFunc("POST /v1/node/install", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Version string `json:"version"`
		}
		if err := decodeBody(r, &body); err != nil || body.Version == "" {
			writeError(w, http.StatusBadRequest, invalid("version is required"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		version, err := (&node.Installer{Root: b.Root(), Catalog: b.Binaries()}).Install(ctx, body.Version)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"version": version})
	})

	mux.HandleFunc("GET /v1/services", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, b.Services())
	})

	service := func(w http.ResponseWriter, r *http.Request, change func(cfg *config.Config, id string) error) {
		if err := b.Mutate(func(cfg *config.Config) error { return change(cfg, r.PathValue("id")) }); err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, b.Services())
	}
	setRunning := func(on bool) func(*config.Config, string) error {
		return func(cfg *config.Config, id string) error {
			switch id {
			case "postgres":
				cfg.Services.SetRunning(id, on)
			case "dumps":
				cfg.Dumps = on
			default:
				if _, ok := cfg.Services.Extra(id); !ok {
					return invalid("%s cannot be started or stopped", id)
				}
				cfg.Services.SetRunning(id, on)
			}
			return nil
		}
	}
	mux.HandleFunc("POST /v1/services/{id}/start", func(w http.ResponseWriter, r *http.Request) { service(w, r, setRunning(true)) })
	mux.HandleFunc("POST /v1/services/{id}/stop", func(w http.ResponseWriter, r *http.Request) { service(w, r, setRunning(false)) })

	mux.HandleFunc("PATCH /v1/services/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Port int `json:"port"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		service(w, r, func(cfg *config.Config, id string) error {
			if body.Port < 1024 || body.Port > 65535 {
				return invalid("port must be between 1024 and 65535")
			}
			if other, taken := cfg.UsedPorts(id)[body.Port]; taken && other != id {
				return invalid("port %d is used by Flock's %s service", body.Port, other)
			}
			if _, current := cfg.UsedPorts("")[body.Port]; !current {
				if taken := ports.Taken(body.Port, b.Root()); taken != "" {
					return invalid("%s", taken)
				}
			}
			if x, ok := cfg.Services.Extra(id); ok {
				x.Port = body.Port
				cfg.Services.SetExtra(id, x)
				return nil
			}
			switch id {
			case "postgres":
				cfg.Ports.Postgres = body.Port
			case "mail":
				cfg.Ports.SMTP = body.Port
			case "dumps":
				cfg.Ports.Dumps = body.Port
			default:
				return invalid("the %s port can only be changed with `flock install`", id)
			}
			return nil
		})
	})
}

// SiteChange is a PATCH /v1/sites/{name} body; nil fields stay as they are.
type SiteChange struct {
	Type    *string `json:"type,omitempty"` // "php" or "node"
	Secured *bool   `json:"secured,omitempty"`
	PHP     *string `json:"php,omitempty"`
	Node    *string `json:"node,omitempty"`
	Server  *string `json:"server,omitempty"`
	Worker  *string `json:"worker,omitempty"`
	Reverb  *bool   `json:"reverb,omitempty"`
	Root    *string `json:"root,omitempty"`
}

// Config fetches config.json plus the installed versions.
func (c *Client) Config(ctx context.Context) (ConfigView, error) {
	var out ConfigView
	err := c.get(ctx, "/v1/config", &out)
	return out, err
}

// Services fetches every background service with its live state.
func (c *Client) Services(ctx context.Context) ([]Service, error) {
	var out []Service
	err := c.get(ctx, "/v1/services", &out)
	return out, err
}

// SetServiceRunning starts or stops a service.
func (c *Client) SetServiceRunning(ctx context.Context, id string, on bool) ([]Service, error) {
	action := "stop"
	if on {
		action = "start"
	}
	var out []Service
	err := c.do(ctx, http.MethodPost, "/v1/services/"+url.PathEscape(id)+"/"+action, &out)
	return out, err
}

// LinkSite serves dir (absolute) as a site; name defaults to the directory's name.
func (c *Client) LinkSite(ctx context.Context, dir, name string) (Status, error) {
	var out Status
	err := c.send(ctx, http.MethodPost, "/v1/sites", map[string]string{"path": dir, "name": name}, &out)
	return out, err
}

// Proxy serves <name>.<tld> from upstream (a port, host:port or URL; see config.NormalizeUpstream),
// adding the proxy or changing its upstream. A nil secured keeps the proxy's setting (HTTP for a new one).
func (c *Client) Proxy(ctx context.Context, name, upstream string, secured *bool) (Status, error) {
	var out Status
	body := map[string]any{"name": name, "upstream": upstream}
	if secured != nil {
		body["secured"] = *secured
	}
	err := c.send(ctx, http.MethodPost, "/v1/proxies", body, &out)
	return out, err
}

// SecureProxy serves a proxy over HTTPS (on) or plain HTTP.
func (c *Client) SecureProxy(ctx context.Context, name string, on bool) (Status, error) {
	var out Status
	err := c.send(ctx, http.MethodPatch, "/v1/proxies/"+url.PathEscape(name), map[string]bool{"secured": on}, &out)
	return out, err
}

// UpdateProxy changes a proxy's settings, e.g. the command that starts its server.
func (c *Client) UpdateProxy(ctx context.Context, name string, change ProxyChange) (Status, error) {
	var out Status
	err := c.timeout(time.Minute).send(ctx, http.MethodPatch, "/v1/proxies/"+url.PathEscape(name), change, &out)
	return out, err
}

// StartProxy runs a proxy's command, which starts its server.
func (c *Client) StartProxy(ctx context.Context, name string) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodPost, "/v1/proxies/"+url.PathEscape(name)+"/start", &out)
	return out, err
}

// StopProxy stops a proxy's server that Flock started. It waits for it to exit.
func (c *Client) StopProxy(ctx context.Context, name string) (Status, error) {
	var out Status
	err := c.timeout(time.Minute).do(ctx, http.MethodPost, "/v1/proxies/"+url.PathEscape(name)+"/stop", &out)
	return out, err
}

// RemoveProxy stops serving a proxy.
func (c *Client) RemoveProxy(ctx context.Context, name string) (Status, error) {
	var out Status
	err := c.send(ctx, http.MethodDelete, "/v1/proxies/"+url.PathEscape(name), nil, &out)
	return out, err
}

// UpdateSite changes a site's settings. Switching servers or turning on Reverb can take a while
// (the process starts).
func (c *Client) UpdateSite(ctx context.Context, name string, change SiteChange) (Status, error) {
	var out Status
	err := c.timeout(time.Minute).send(ctx, http.MethodPatch, "/v1/sites/"+url.PathEscape(name), change, &out)
	return out, err
}

// AddDomain adds an extra domain to a site: a subdomain ("tenant1") or an alias ("my-app-2.test").
func (c *Client) AddDomain(ctx context.Context, name, domain string) (Status, error) {
	var out Status
	err := c.send(ctx, http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/domains", map[string]string{"domain": domain}, &out)
	return out, err
}

// PinSite pins a PHP or Node.js version into the site's project files (empty: the one it uses now).
func (c *Client) PinSite(ctx context.Context, name, tool, version string) (Status, error) {
	var out Status
	err := c.send(ctx, http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/pin", map[string]string{"tool": tool, "version": version}, &out)
	return out, err
}

// UnpinSite removes a pinned version from the site's project files.
func (c *Client) UnpinSite(ctx context.Context, name, tool string) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodDelete, "/v1/sites/"+url.PathEscape(name)+"/pin/"+url.PathEscape(tool), &out)
	return out, err
}

func configView(root string, cfg *config.Config) ConfigView {
	view := ConfigView{
		TLD: cfg.TLD, DefaultPHP: cfg.DefaultPHP, DefaultNode: cfg.DefaultNode,
		Xdebug: cfg.Xdebug, XdebugBreakpoints: cfg.XdebugBreakpoints, Dumps: cfg.Dumps, Channel: ChannelOf(cfg), Root: root, Ports: cfg.Ports,
		CaptureOff: append([]string{}, cfg.CaptureOff...), DumpsInPage: cfg.DumpsInPage,
		Parked: cfg.Parked, PHP: []PHPVersion{}, Node: []string{},
	}
	if view.Parked == nil {
		view.Parked = []string{}
	}

	installed, _ := php.Installed(root)
	for _, v := range installed {

		view.PHP = append(view.PHP, PHPVersion{
			Version:         v,
			FullVersion:     php.FullVersion(root, v),
			Ini:             php.IniPath(root, v),
			XdebugAvailable: php.XdebugAvailable(root, v),
		})
	}

	if versions, err := node.Installed(root); err == nil && versions != nil {
		view.Node = versions
	}
	return view
}

// ChannelOf is the channel config.json follows ("stable" when it names none or an unknown one).
func ChannelOf(cfg *config.Config) string {
	ch, err := binaries.ParseChannel(cfg.Channel)
	if err != nil {
		return string(binaries.Stable)
	}
	return string(ch)
}
