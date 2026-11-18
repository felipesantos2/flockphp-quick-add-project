package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReturnsDefaultsWhenMissingAndRoundTrips(t *testing.T) {
	root := t.TempDir()

	cfg, err := Load(root)
	if err != nil || cfg.TLD != "test" || cfg.Ports.HTTPS != 443 {
		t.Fatalf("unexpected defaults: %+v, %v", cfg, err)
	}

	cfg.Upsert(Site{Name: "app", Path: "/srv/app", Secured: true})
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	again, err := Load(root)
	if err != nil || len(again.Sites) != 1 || !again.Sites[0].Secured {
		t.Fatalf("round trip failed: %+v, %v", again, err)
	}
}

func TestAllSitesMergesParkedAndLinkedSites(t *testing.T) {
	parked := t.TempDir()
	for _, d := range []string{"Blog", "shop", ".hidden"} {
		if err := os.Mkdir(filepath.Join(parked, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := Default()
	cfg.DefaultPHP = "8.5"
	cfg.Parked = []string{parked}
	cfg.Upsert(Site{Name: "shop", Path: "/elsewhere/shop", PHP: "8.4"})

	sites := cfg.AllSites()
	if len(sites) != 2 || sites[0].Name != "blog" || !sites[0].Parked || sites[1].Path != "/elsewhere/shop" {
		t.Fatalf("unexpected sites: %+v", sites)
	}
	if cfg.PHPFor(sites[0]) != "8.5" || cfg.PHPFor(sites[1]) != "8.4" {
		t.Fatal("wrong PHP resolution")
	}
}

func TestSiteForDirPicksDeepestMatch(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "outer", Path: "/code"})
	cfg.Upsert(Site{Name: "inner", Path: "/code/inner"})

	if s, ok := cfg.SiteForDir("/code/inner/app/Models"); !ok || s.Name != "inner" {
		t.Fatalf("want inner, got %+v", s)
	}
	if _, ok := cfg.SiteForDir("/codex"); ok {
		t.Fatal("/codex must not match /code")
	}
}

func TestSiteNameSanitisesDirectoryNames(t *testing.T) {
	if got := SiteName("My_App.v2"); got != "my-app-v2" {
		t.Fatalf("got %q", got)
	}
}

func TestXdebugDetectionIsOnForOlderConfigs(t *testing.T) {
	root := t.TempDir()
	// "xdebug" loaded Xdebug for every request and was off by default; detection replaces it.
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"tld":"test","xdebug":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Xdebug || !cfg.XdebugBreakpoints {
		t.Fatalf("got xdebug %v, breakpoints %v", cfg.Xdebug, cfg.XdebugBreakpoints)
	}
}

func TestSetServerAssignsStablePortsAndValidates(t *testing.T) {
	cfg := Default()
	cfg.Services.SetExtra("mysql", Extra{Version: "8.4", Port: FirstSitePort})

	a := Site{Name: "a", Path: "/srv/a", Parked: true}
	if err := cfg.SetServer(&a, ServerFrankenPHP, WorkerOctane); err != nil {
		t.Fatal(err)
	}
	if a.Parked || !a.FrankenPHP() || a.Worker != WorkerOctane || a.Port != FirstSitePort+1 {
		t.Fatalf("a: %+v (the first port is MySQL's)", a)
	}
	cfg.Upsert(a)

	b := Site{Name: "b", Path: "/srv/b"}
	if err := cfg.SetServer(&b, ServerFrankenPHP, ""); err != nil || b.Port != FirstSitePort+2 {
		t.Fatalf("b: %+v, %v", b, err)
	}
	cfg.Upsert(b)

	// Switching back keeps the port and the worker mode; switching again reuses them.
	if err := cfg.SetServer(&a, ServerFastCGI, WorkerOctane); err != nil || a.Server != "" || a.Port != FirstSitePort+1 {
		t.Fatalf("back to FastCGI: %+v, %v", a, err)
	}
	cfg.Upsert(a)
	if err := cfg.SetServer(&a, ServerFrankenPHP, WorkerOctane); err != nil || a.Port != FirstSitePort+1 {
		t.Fatalf("again: %+v, %v", a, err)
	}
	if got := cfg.UsedPorts("")[FirstSitePort+2]; got != "frankenphp-b" {
		t.Errorf("UsedPorts names b's port %q", got)
	}

	// Reverb gets a port of its own, past b's, and keeps it while off.
	cfg.SetReverb(&a, true)
	if !a.Reverb || a.ReverbPort != FirstSitePort+3 {
		t.Fatalf("reverb: %+v", a)
	}
	cfg.Upsert(a)
	if got := cfg.UsedPorts("")[FirstSitePort+3]; got != "reverb-a" {
		t.Errorf("UsedPorts names a's Reverb port %q", got)
	}
	cfg.SetReverb(&a, false)
	cfg.Upsert(a)
	if _, used := cfg.UsedPorts("")[FirstSitePort+3]; used || a.ReverbPort != FirstSitePort+3 {
		t.Errorf("Reverb off: %+v", a)
	}
	cfg.SetReverb(&a, true)
	if a.ReverbPort != FirstSitePort+3 {
		t.Errorf("Reverb on again: %+v", a)
	}
	cfg.Upsert(a)

	// Both switched on before either is saved: the ports still differ.
	c := Site{Name: "c", Path: "/srv/c"}
	cfg.SetReverb(&c, true)
	if err := cfg.SetServer(&c, ServerFrankenPHP, ""); err != nil || c.ReverbPort != FirstSitePort+4 || c.Port != FirstSitePort+5 {
		t.Fatalf("c: %+v, %v", c, err)
	}

	if err := cfg.SetServer(&a, "swoole", ""); err == nil {
		t.Error("unknown server accepted")
	}
	if err := cfg.SetServer(&a, ServerFrankenPHP, "roadrunner"); err == nil {
		t.Error("unknown worker mode accepted")
	}
}
