package daemon

import (
	"fmt"
	"runtime"
	"strings"

	"flock/internal/api"
	"flock/internal/binaries"
	"flock/internal/config"
	"flock/internal/frankenphp"
	"flock/internal/ports"
	"flock/internal/supervisor"
)

// frankenPHPSite is how a FrankenPHP site is served: its process, when it can run, and its state.
type frankenPHPSite struct {
	spec   *supervisor.Spec
	status api.SiteFrankenPHP
}

// frankenPHPSite prepares a site on FrankenPHP. When the build for its PHP version is missing it
// starts downloading it; until the process can run, the site is served through FastCGI.
func (d *daemon) frankenPHPSite(cfg *config.Config, site config.Site, minor, docRoot string, extraEnv []string) frankenPHPSite {
	name := frankenphp.ProcessName(site.Name)
	fp := frankenPHPSite{status: api.SiteFrankenPHP{Port: site.Port, Log: name}}
	fail := func(state, format string, args ...any) frankenPHPSite {
		fp.status.State, fp.status.Problem = state, fmt.Sprintf(format, args...)
		return fp
	}

	switch {
	case d.frankenphp == nil || !d.frankenphp.Available():
		return fail(api.FrankenPHPUnavailable, "FrankenPHP isn't available for this platform")
	case !frankenphp.Supports(minor):
		return fail(api.FrankenPHPUnavailable, "FrankenPHP needs PHP %s or newer (the site uses %s)", frankenphp.MinimumMinor, minor)
	case site.Port == 0:
		return fail(api.FrankenPHPFailed, "the site has no FrankenPHP port — switch it to FrankenPHP again")
	}

	info, installed := frankenphp.InstalledInfo(d.root, minor)
	if !installed {
		d.frankenphp.Ensure(minor)
		job := d.frankenphp.Job(minor)
		if job != nil && job.State == "failed" {
			return fail(api.FrankenPHPFailed, "FrankenPHP for PHP %s couldn't be installed: %s", minor, job.Error)
		}
		fp.status.State, fp.status.Progress = api.FrankenPHPInstalling, -1
		if job != nil {
			fp.status.Progress = job.Progress
		}
		return fp
	}
	fp.status.Version, fp.status.PHPVersion = info.Version, info.PHP

	if other, taken := cfg.UsedPorts("")[site.Port]; taken && other != config.SitePortID(site.Name) {
		return fail(api.FrankenPHPFailed, "port %d is also configured for %s — switch the site to FrankenPHP again for a new port", site.Port, serviceName(other))
	}

	d.mu.Lock()
	restart := d.restarts[site.Name]
	d.mu.Unlock()
	spec, err := frankenphp.Spec(d.root, frankenphp.Site{
		Name: site.Name, Path: site.Path, Root: docRoot, Minor: minor, Port: site.Port,
		Secured: site.Secured, Octane: site.Worker == config.WorkerOctane, Env: extraEnv, Restart: restart,
	})
	if err != nil {
		return fail(api.FrankenPHPFailed, "%v", err)
	}
	if !d.procUp(spec.Name) && !ports.Free("127.0.0.1", site.Port) {
		return fail(api.FrankenPHPFailed, "%s", ports.Describe(site.Port))
	}
	fp.spec = &spec
	return fp
}

// RestartSite implements api.Backend: it restarts a FrankenPHP site's process, e.g. so Octane's
// workers load changed code, or a Node.js site's dev server (starting it when it doesn't run).
// A site's Reverb server has RestartReverb.
func (d *daemon) RestartSite(name string) error {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	if cfg == nil {
		return fmt.Errorf("the daemon has no configuration yet")
	}
	site, ok := cfg.FindSite(name)
	if !ok {
		return fmt.Errorf("no site named %s", name)
	}
	node := site.Kind() == config.TypeNode
	if !site.FrankenPHP() && !node {
		return fmt.Errorf("%s doesn't run on FrankenPHP", name)
	}
	d.mu.Lock()
	d.restarts[name]++
	d.mu.Unlock()
	if node {
		d.node.mu.Lock()
		d.node.wake(name)
		d.node.mu.Unlock()
	}
	return d.reload()
}

// FrankenPHP implements api.Backend.
func (d *daemon) FrankenPHP() *frankenphp.Manager { return d.frankenphp }

// Binaries implements api.Backend.
func (d *daemon) Binaries() *binaries.Catalog { return d.binaries }

// releaseFrankenPHP stops the FrankenPHP processes (Windows locks a running build's files, so it
// can't be replaced). The reload after the install starts them again.
func (d *daemon) releaseFrankenPHP(string) {
	if runtime.GOOS != "windows" {
		return
	}
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	keep := map[string]bool{}
	for _, name := range d.procs.Running() {
		keep[name] = !strings.HasPrefix(name, "frankenphp-")
	}
	d.procs.Retain(keep)
}

// liveSites is the sites of the last reload with the progress of FrankenPHP downloads brought up to
// date (a finished download reloads; a failed one doesn't). d.mu must be held.
func (d *daemon) liveSites() []api.SiteStatus {
	if d.frankenphp == nil {
		return d.sites
	}
	sites := d.sites
	copied := false
	for i, s := range sites {
		if s.FrankenPHP == nil || s.FrankenPHP.State != api.FrankenPHPInstalling {
			continue
		}
		job := d.frankenphp.Job(s.PHP)
		if job == nil {
			continue
		}
		if !copied {
			sites, copied = append([]api.SiteStatus(nil), sites...), true
		}
		fp := *s.FrankenPHP
		if job.State == "failed" {
			fp.State, fp.Problem = api.FrankenPHPFailed, fmt.Sprintf("FrankenPHP for PHP %s couldn't be installed: %s", s.PHP, job.Error)
		} else {
			fp.Progress = job.Progress
		}
		sites[i].FrankenPHP = &fp
	}
	return sites
}
