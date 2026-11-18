package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"flock/internal/config"
	"flock/internal/frankenphp"
)

// FrankenPHPState is every FrankenPHP build: installed, published, being installed.
type FrankenPHPState = frankenphp.State

func registerFrankenPHP(mux *http.ServeMux, b Backend) {
	manager := func(w http.ResponseWriter) *frankenphp.Manager {
		m := b.FrankenPHP()
		if m == nil {
			writeError(w, http.StatusNotImplemented, frankenphp.ErrUnsupported)
		}
		return m
	}
	respond := func(w http.ResponseWriter, m *frankenphp.Manager, err error) {
		if err != nil {
			code := http.StatusInternalServerError
			switch {
			case errors.Is(err, frankenphp.ErrBusy), errors.Is(err, errConflict):
				code = http.StatusConflict
			case errors.Is(err, frankenphp.ErrUnsupported):
				code = http.StatusNotImplemented
			case errors.Is(err, errInvalid):
				code = http.StatusBadRequest
			}
			writeError(w, code, err)
			return
		}
		writeJSON(w, http.StatusOK, m.State())
	}
	action := func(do func(m *frankenphp.Manager, minor string) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if m := manager(w); m != nil {
				respond(w, m, do(m, r.PathValue("minor")))
			}
		}
	}

	mux.HandleFunc("GET /v1/frankenphp", func(w http.ResponseWriter, _ *http.Request) {
		if m := manager(w); m != nil {
			respond(w, m, nil)
		}
	})
	mux.HandleFunc("POST /v1/frankenphp/check", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
		defer cancel()
		_ = m.Check(ctx) // a failed check is reported in check_error
		respond(w, m, nil)
	})
	// Installs the build for a PHP minor, updates it, or retries a failed install.
	mux.HandleFunc("POST /v1/frankenphp/{minor}/install", action(func(m *frankenphp.Manager, minor string) error {
		m.Dismiss(minor)
		return m.Install(minor)
	}))
	mux.HandleFunc("POST /v1/frankenphp/{minor}/cancel", action(func(m *frankenphp.Manager, minor string) error { m.Cancel(minor); return nil }))
	mux.HandleFunc("DELETE /v1/frankenphp/{minor}/job", action(func(m *frankenphp.Manager, minor string) error { m.Dismiss(minor); return nil }))
	mux.HandleFunc("DELETE /v1/frankenphp/{minor}", action(func(m *frankenphp.Manager, minor string) error {
		if m.Busy(minor) {
			return frankenphp.ErrBusy
		}
		for _, s := range b.Status().Sites {
			if s.Server == config.ServerFrankenPHP && s.PHP == minor {
				return fmt.Errorf("%w: %s runs on FrankenPHP with PHP %s — switch it to FastCGI first", errConflict, s.Name, minor)
			}
		}
		if m.Release != nil {
			m.Release(minor)
		}
		err := frankenphp.Uninstall(b.Root(), minor)
		_ = b.Reload()
		return err
	}))
	mux.HandleFunc("GET /v1/frankenphp/{minor}/log", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		text, err := tailLines(m.LogPath(r.PathValue("minor")), 2000)
		if err != nil {
			writeError(w, http.StatusNotFound, errors.New("no install log yet"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(text))
	})

	mux.HandleFunc("POST /v1/sites/{name}/restart", func(w http.ResponseWriter, r *http.Request) {
		if err := b.RestartSite(r.PathValue("name")); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	})
	mux.HandleFunc("POST /v1/sites/{name}/reverb/restart", func(w http.ResponseWriter, r *http.Request) {
		if err := b.RestartReverb(r.PathValue("name")); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	})
	mux.HandleFunc("POST /v1/sites/{name}/stop", func(w http.ResponseWriter, r *http.Request) {
		if err := b.StopSite(r.PathValue("name")); err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	})
}

// StopSite puts a Node.js site's dev server to sleep until the site's next request.
func (c *Client) StopSite(ctx context.Context, name string) (Status, error) {
	var out Status
	err := c.timeout(time.Minute).do(ctx, http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/stop", &out)
	return out, err
}

// FrankenPHP fetches every FrankenPHP build.
func (c *Client) FrankenPHP(ctx context.Context) (FrankenPHPState, error) {
	var out FrankenPHPState
	err := c.get(ctx, "/v1/frankenphp", &out)
	return out, err
}

// CheckFrankenPHP makes the daemon fetch the binaries manifest now.
func (c *Client) CheckFrankenPHP(ctx context.Context) (FrankenPHPState, error) {
	var out FrankenPHPState
	err := c.slow().do(ctx, http.MethodPost, "/v1/frankenphp/check", &out)
	return out, err
}

// InstallFrankenPHP installs or updates the build for a PHP minor in the background.
func (c *Client) InstallFrankenPHP(ctx context.Context, minor string) (FrankenPHPState, error) {
	var out FrankenPHPState
	err := c.do(ctx, http.MethodPost, "/v1/frankenphp/"+minor+"/install", &out)
	return out, err
}

// UninstallFrankenPHP removes the build for a PHP minor.
func (c *Client) UninstallFrankenPHP(ctx context.Context, minor string) (FrankenPHPState, error) {
	var out FrankenPHPState
	err := c.slow().do(ctx, http.MethodDelete, "/v1/frankenphp/"+minor, &out)
	return out, err
}

// RestartSite restarts a FrankenPHP site's process.
func (c *Client) RestartSite(ctx context.Context, name string) (Status, error) {
	var out Status
	err := c.timeout(time.Minute).do(ctx, http.MethodPost, "/v1/sites/"+name+"/restart", &out)
	return out, err
}

// RestartReverb restarts a site's Reverb server.
func (c *Client) RestartReverb(ctx context.Context, name string) (Status, error) {
	var out Status
	err := c.timeout(time.Minute).do(ctx, http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/reverb/restart", &out)
	return out, err
}
