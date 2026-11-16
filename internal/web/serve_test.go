package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flock/internal/certs"
)

// freePort returns a port that's free for TCP and UDP: Caddy also listens for HTTP/3 over
// UDP, and Windows can reserve 100-port ranges (e.g. Hyper-V/WSL) for one protocol only.
func freePort(t *testing.T) int {
	t.Helper()
	for range 500 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		pc, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
		ln.Close()
		if err == nil {
			pc.Close()
			return port
		}
	}
	t.Fatal("no port is free for both TCP and UDP")
	return 0
}

func nonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				return ip.String()
			}
		}
	}
	t.Skip("no non-loopback IPv4 address")
	return ""
}

// TestCustomHandlersCannotBypassRemoteGuard runs the adapted handler chain on a wildcard listener.
// Custom terminating handle and route directives work over loopback, but a connection through a
// non-loopback interface is aborted before either can answer.
func TestCustomHandlersCannotBypassRemoteGuard(t *testing.T) {
	dir, err := os.MkdirTemp("", "flock-web-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	port := freePort(t)
	cfg := Config{
		StorageDir: filepath.Join(dir, "caddy"),
		LogFile:    filepath.Join(dir, "web.log"),
		HTTPPort:   port,
		HTTPSPort:  freePort(t),
		TLD:        "test",
		Sites: []Site{{
			Domain: "app.test", Root: dir, FPM: []string{"127.0.0.1:1"},
			Handlers: `
handle /handled {
	respond "handled" 200
}
route {
	@custom_route path /routed
	respond @custom_route "routed" 200
}`,
		}},
	}
	if err := Apply(cfg); err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	t.Cleanup(func() { _ = Stop() })

	client := func(target string) *http.Client {
		return &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				DisableKeepAlives: true,
				DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(target, fmt.Sprint(port)))
				},
			},
		}
	}
	url := func(path string) string { return fmt.Sprintf("http://app.test:%d%s", port, path) }
	for path, want := range map[string]string{"/handled": "handled", "/routed": "routed"} {
		resp, err := client("127.0.0.1").Get(url(path))
		if err != nil {
			t.Fatalf("loopback %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(body) != want {
			t.Fatalf("loopback %s: got %d %q, want 200 %q", path, resp.StatusCode, body, want)
		}
	}

	remote := nonLoopbackIPv4(t)
	address := net.JoinHostPort(remote, fmt.Sprint(port))
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatalf("wildcard listener is not reachable through %s: %v", address, err)
	}
	conn.Close()
	for _, path := range []string{"/handled", "/routed"} {
		if resp, err := client(remote).Get(url(path)); err == nil {
			resp.Body.Close()
			t.Fatalf("non-loopback request reached the custom handler at %s: %d", path, resp.StatusCode)
		}
	}
}

// TestServesEveryDepthAliasAndCatchAllOverTrustedHTTPS runs the embedded Caddy and makes real
// TLS handshakes, verifying the certificates against the Flock CA alone.
func TestServesEveryDepthAliasAndCatchAllOverTrustedHTTPS(t *testing.T) {
	// Not t.TempDir: Caddy keeps its log open after Stop, and Windows can't delete open files.
	dir, err := os.MkdirTemp("", "flock-web-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// php_fastcgi only proxies when the front controller exists; without it file_server answers 404.
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ca, err := certs.Ensure(filepath.Join(dir, "certs"), "test")
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile, err := ca.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(dir, "site.Caddyfile")
	if err := os.WriteFile(custom, []byte(`
@cors_preflight method OPTIONS
header Access-Control-Allow-Origin "https://admin.example.test"
respond @cors_preflight 204
`), 0o644); err != nil {
		t.Fatal(err)
	}
	customHandlers, err := os.ReadFile(custom)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	caPEM, _ := os.ReadFile(ca.CertPath())
	pool.AppendCertsFromPEM(caPEM)

	// No PHP runs here: a routed request answers 502 from php_fastcgi, the catch-all 404.
	leaves := certs.NewLeaves()
	issued := map[string]int{}
	cfg := Config{
		StorageDir: filepath.Join(dir, "caddy"),
		LogFile:    filepath.Join(dir, "web.log"),
		HTTPPort:   freePort(t),
		HTTPSPort:  freePort(t),
		Bind:       "127.0.0.1",
		TLD:        "test",
		Issue: func(name string) (*tls.Certificate, error) {
			issued[name]++
			return leaves.Get(ca, name)
		},
		Sites: []Site{
			{Domain: "app.test", Aliases: []string{"app-2.test"}, Root: dir, Handlers: string(customHandlers), FPM: []string{"127.0.0.1:1"}, DebugFPM: []string{"127.0.0.1:2"}, CertFile: certFile, KeyFile: keyFile},
			{Domain: "plain.test", Root: dir, FPM: []string{"127.0.0.1:1"}},
		},
		Waiting: []Waiting{
			{Domain: "fresh.test", Name: "fresh", PHP: "8.5", State: WaitingInstalling},
			{Domain: "shop.test", Aliases: []string{"shop-2.test"}, Name: "shop", PHP: "8.4", State: WaitingMissing, File: ".flockrc"},
			{Domain: "legacy.test", Name: "legacy", PHP: "7.3", State: WaitingUnavailable},
		},
		Rootless: []Rootless{
			{Domain: "zoo.test", Name: "zoo", State: "not_found", Root: ".", Suggestions: []string{"src/www", "admin/www"}},
			{Domain: "moved.test", Name: "moved", State: "missing", Root: "src/www", Set: true, File: ".flockrc"},
			{Domain: "cms.test", Name: "cms", State: "empty", Root: "src/www", Set: true},
			{Domain: "notes.test", Name: "notes", State: "empty", Root: "."},
		},
	}
	if err := Apply(cfg); err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	t.Cleanup(func() { _ = Stop() })

	// Every name dials 127.0.0.1; TLS still verifies the requested name.
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, _ := net.SplitHostPort(addr)
				return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+port)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	get := func(url string) (*http.Response, string, error) {
		resp, err := client.Get(url)
		if err != nil {
			return nil, "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body), nil
	}
	https := func(host string) string { return fmt.Sprintf("https://%s:%d/", host, cfg.HTTPSPort) }

	preflight, err := http.NewRequest(http.MethodOptions, https("app.test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	preflight.Header.Set("Origin", "https://admin.example.test")
	resp, err := client.Do(preflight)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "https://admin.example.test" {
		t.Errorf("custom Caddy directives: status %d, headers %v", resp.StatusCode, resp.Header)
	}

	// The site answers (with a 502, as nothing runs PHP here) on every depth and alias, and the
	// certificate verifies against the Flock CA alone.
	for _, host := range []string{"app.test", "t1.app.test", "a.b.app.test", "a.b.c.app.test", "a.b.c.d.app.test",
		"app-2.test", "x.app-2.test", "a.b.c.d.app-2.test"} {
		resp, body, err := get(https(host))
		if err != nil {
			t.Errorf("%s: %v", host, err)
			continue
		}
		if resp.StatusCode != http.StatusBadGateway || strings.Contains(body, "No secured Flock site") {
			t.Errorf("%s: want the site (502 from its missing PHP), got %d %q", host, resp.StatusCode, body)
		}
		if got := resp.TLS.PeerCertificates[0]; got.VerifyHostname(host) != nil {
			t.Errorf("%s: served a certificate for %v", host, got.DNSNames)
		}
	}

	// The site's own certificate file covers app.test and *.app.test; only deeper names are issued.
	if issued["app.test"] != 0 || issued["t1.app.test"] != 0 || issued["a.b.app.test"] != 1 || issued["app-2.test"] != 1 {
		t.Errorf("unexpected issued certificates: %v", issued)
	}

	// The catch-all answers the rest: an unsecured site (at any depth) asks to be secured, by its
	// own name; five levels is past MaxDepth, so like an unknown name it asks to be linked.
	for host, want := range map[string]string{
		"plain.test":         "flock secure plain<",
		"x.y.plain.test":     `href="flock://secure/plain"`,
		"a.b.c.d.e.app.test": "Not linked",
		"nope.test":          "flock link nope --secure",
	} {
		resp, body, err := get(https(host))
		if err != nil {
			t.Errorf("%s: %v", host, err)
			continue
		}
		if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, want) || !strings.Contains(body, ">"+host+"<") {
			t.Errorf("%s: want the catch-all with %q, got %d %q", host, want, resp.StatusCode, body)
		}
	}

	// Over plain HTTP, an unknown name gets the rendered "not linked" page, never cached.
	resp, body, err := get(fmt.Sprintf("http://nope.test:%d/some/path", cfg.HTTPPort))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "Not linked") || !strings.Contains(body, "flock link nope<") ||
		strings.Contains(body, "{{") || strings.Contains(body, `href="flock://secure`) ||
		resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Flock-Page") != "unlinked" {
		t.Errorf("http catch-all: got %d %v %q", resp.StatusCode, resp.Header, body)
	}

	// A site waiting for its PHP says so on every name and scheme, with what it needs.
	for url, want := range map[string][]string{
		https("fresh.test"): {"Needs PHP", "installing PHP 8.5", "data-wait"},
		fmt.Sprintf("http://x.shop-2.test:%d/", cfg.HTTPPort): {"<strong>shop</strong>", "pinned in <code>.flockrc</code>",
			`href="flock://install/php/8.4"`, "flock php install 8.4<"},
		fmt.Sprintf("http://legacy.test:%d/", cfg.HTTPPort): {"no build of PHP 7.3", "flock isolate php &lt;version&gt;"},
	} {
		resp, body, err := get(url)
		if err != nil {
			t.Errorf("%s: %v", url, err)
			continue
		}
		for _, w := range want {
			if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, w) || strings.Contains(body, "Not linked") {
				t.Errorf("%s: want the PHP page with %q, got %d %q", url, w, resp.StatusCode, body)
			}
		}
	}

	// A site without a document root says so on every name and scheme, with the folders that could be it.
	for url, want := range map[string][]string{
		fmt.Sprintf("http://x.zoo.test:%d/", cfg.HTTPPort): {"No document root", "<strong>zoo</strong>", "none of the usual document roots", "<code>src/www</code>, <code>admin/www</code>",
			"flock root src/www --site zoo<", "flock pin root src/www --site zoo<", `class="or"`, "Prefer the app?", "data-wait"},
		https("moved.test"): {"src/www isn&rsquo;t there", "set in <code>.flockrc</code>", "but that folder doesn&rsquo;t exist",
			"flock root &lt;folder&gt; --site moved<"},
		fmt.Sprintf("http://cms.test:%d/", cfg.HTTPPort): {"src/www has nothing to serve yet", "set on this machine",
			"but it has no <code>index.php</code> yet"},
		fmt.Sprintf("http://notes.test:%d/", cfg.HTTPPort): {"Which folder should Flock serve?",
			"There&rsquo;s no <code>index.php</code> in the project yet"},
	} {
		resp, body, err := get(url)
		if err != nil {
			t.Errorf("%s: %v", url, err)
			continue
		}
		for _, w := range want {
			if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, w) || strings.Contains(body, "Not linked") || strings.Contains(body, "Not secured") {
				t.Errorf("%s: want the document root page with %q, got %d %q", url, w, resp.StatusCode, body)
			}
		}
	}

	// Plain HTTP redirects secured sites at every depth.
	resp, _, err = get(fmt.Sprintf("http://a.b.c.d.app.test:%d/x", cfg.HTTPPort))
	if err != nil || resp.StatusCode != http.StatusPermanentRedirect || !strings.HasPrefix(resp.Header.Get("Location"), "https://a.b.c.d.app.test:") {
		t.Errorf("http redirect: %v %+v", err, resp)
	}

	// A name outside the TLD gets no certificate, and fails fast rather than hanging.
	start := time.Now()
	if _, _, err := get(https("example.com")); err == nil {
		t.Error("example.com: want a TLS failure")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("example.com: the handshake took %v", time.Since(start))
	}
}

// TestAnAliasUnderAnotherSiteWinsOverItsSubdomains checks the order Caddy tries blocks in: by the
// longest name, which a long alias of the other site must not change, over both schemes.
func TestAnAliasUnderAnotherSiteWinsOverItsSubdomains(t *testing.T) {
	dir, err := os.MkdirTemp("", "flock-web-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// Each site serves who.txt with its own name.
	root := func(name string) string {
		d := filepath.Join(dir, name)
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "index.php"), []byte("<?php\n"), 0o644)
		_ = os.WriteFile(filepath.Join(d, "who.txt"), []byte(name), 0o644)
		return d
	}
	shop, accounts, feature := root("shop"), root("accounts"), root("feature")
	ca, err := certs.Ensure(filepath.Join(dir, "certs"), "test")
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile, err := ca.SiteCert("shop.test")
	if err != nil {
		t.Fatal(err)
	}
	// A worktree of the shop is a site whose domain is one of the shop's subdomains; f.shop.test
	// is exactly as long as *.shop.test.
	featureCert, featureKey, err := ca.SiteCert("f.shop.test")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	caPEM, _ := os.ReadFile(ca.CertPath())
	pool.AppendCertsFromPEM(caPEM)
	leaves := certs.NewLeaves()

	cfg := Config{
		StorageDir: filepath.Join(dir, "caddy"),
		LogFile:    filepath.Join(dir, "web.log"),
		HTTPPort:   freePort(t),
		HTTPSPort:  freePort(t),
		Bind:       "127.0.0.1",
		TLD:        "test",
		Issue:      func(name string) (*tls.Certificate, error) { return leaves.Get(ca, name) },
		Sites: []Site{
			{Domain: "shop.test", Aliases: []string{"a-really-really-long-alias-of-the-shop.test"}, Root: shop, FPM: []string{"127.0.0.1:1"}, CertFile: certFile, KeyFile: keyFile},
			{Domain: "accounts-shop.test", Aliases: []string{"accounts.shop.test"}, Root: accounts, FPM: []string{"127.0.0.1:1"}},
			{Domain: "f.shop.test", Root: feature, FPM: []string{"127.0.0.1:1"}, CertFile: featureCert, KeyFile: featureKey},
		},
		Waiting: []Waiting{{Domain: "fresh.test", Aliases: []string{"fresh.shop.test"}, Name: "fresh", PHP: "8.5", State: WaitingInstalling}},
	}
	if err := Apply(cfg); err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	t.Cleanup(func() { _ = Stop() })

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, _ := net.SplitHostPort(addr)
				return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+port)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	plain := func(host string) string { return fmt.Sprintf("http://%s:%d/who.txt", host, cfg.HTTPPort) }
	secure := func(host string) string { return fmt.Sprintf("https://%s:%d/who.txt", host, cfg.HTTPSPort) }

	for url, want := range map[string]string{
		plain("accounts.shop.test"):         "accounts",
		plain("a.b.c.d.accounts.shop.test"): "accounts",
		secure("tenant1.shop.test"):         "shop",
		secure("a.b.c.d.shop.test"):         "shop",
		// accounts isn't secured: it says so rather than serving the shop.
		secure("accounts.shop.test"):   `href="flock://secure/accounts-shop"`,
		secure("x.accounts.shop.test"): `href="flock://secure/accounts-shop"`,
		secure("f.shop.test"):          "feature",
		secure("tenant1.f.shop.test"):  "feature",
		secure("g.shop.test"):          "shop",
		// fresh waits for its PHP on both schemes.
		plain("fresh.shop.test"):    "installing PHP 8.5",
		secure("x.fresh.shop.test"): "installing PHP 8.5",
	} {
		resp, err := client.Get(url)
		if err != nil {
			t.Errorf("%s: %v", url, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), want) {
			t.Errorf("%s: want %q, got %d %q", url, want, resp.StatusCode, body)
		}
	}

	// Plain HTTP still redirects the shop's other subdomains.
	resp, err := client.Get(plain("tenant1.shop.test"))
	if err != nil || resp.StatusCode != http.StatusPermanentRedirect {
		t.Errorf("http redirect: %v %+v", err, resp)
	}
}

// TestAProxyWhoseServerIsDownSaysSo runs the embedded Caddy against real upstreams: only a proxy
// whose server can't be reached gets Flock's page. A response of the upstream's own passes through
// as it is, whatever its status, and a PHP site served by FrankenPHP keeps Caddy's plain 502.
func TestAProxyWhoseServerIsDownSaysSo(t *testing.T) {
	dir, err := os.MkdirTemp("", "flock-web-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ca, err := certs.Ensure(filepath.Join(dir, "certs"), "test")
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile, err := ca.SiteCert("s3.test")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	caPEM, _ := os.ReadFile(ca.CertPath())
	pool.AppendCertsFromPEM(caPEM)
	leaves := certs.NewLeaves()

	// An app that answers 502 (or 500) itself, as a Laravel app can.
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusBadGateway
		if r.URL.Path == "/500" {
			status = http.StatusInternalServerError
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "the app's own error page")
	}))
	t.Cleanup(app.Close)
	down := fmt.Sprintf("127.0.0.1:%d", freePort(t)) // nothing listens there

	cfg := Config{
		StorageDir: filepath.Join(dir, "caddy"),
		LogFile:    filepath.Join(dir, "web.log"),
		HTTPPort:   freePort(t),
		HTTPSPort:  freePort(t),
		Bind:       "127.0.0.1",
		TLD:        "test",
		Issue:      func(name string) (*tls.Certificate, error) { return leaves.Get(ca, name) },
		Sites: []Site{
			{Domain: "next.test", Proxy: "http://" + down},
			{Domain: "s3.test", Proxy: "http://" + down, CertFile: certFile, KeyFile: keyFile},
			{Domain: "app.test", Proxy: app.URL},
			// A PHP site served by FrankenPHP, which isn't running.
			{Domain: "franken.test", Root: dir, Proxy: down},
		},
	}
	if err := Apply(cfg); err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	t.Cleanup(func() { _ = Stop() })

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, _ := net.SplitHostPort(addr)
				return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+port)
			},
		},
	}
	get := func(method, url string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest(method, url, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body)
	}

	// A proxy whose server is down says so, on its subdomains and over HTTPS when secured, and the
	// page's HEAD check sees it's still Flock's page.
	for _, url := range []string{
		fmt.Sprintf("http://next.test:%d/some/path", cfg.HTTPPort),
		fmt.Sprintf("http://a.b.next.test:%d/", cfg.HTTPPort),
		fmt.Sprintf("https://s3.test:%d/bucket", cfg.HTTPSPort),
	} {
		resp, body := get(http.MethodGet, url)
		for _, want := range []string{"Not running", "Nothing answers at " + down + " yet.", "is a proxy to <code>" + down + "</code>",
			"data-wait", "flock proxy next &lt;port|host:port|url&gt;", ">502<"} {
			if strings.HasPrefix(url, "https://s3") {
				want = strings.ReplaceAll(want, "proxy next", "proxy s3")
			}
			if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, want) || strings.Contains(body, "{{") ||
				resp.Header.Get("X-Flock-Page") != "upstream-down" || resp.Header.Get("Cache-Control") != "no-store" {
				t.Errorf("%s: want the upstream-down page with %q, got %d %v %q", url, want, resp.StatusCode, resp.Header, body)
			}
		}
		if resp, _ := get(http.MethodHead, url); resp.Header.Get("X-Flock-Page") != "upstream-down" {
			t.Errorf("HEAD %s: want X-Flock-Page, got %d %v", url, resp.StatusCode, resp.Header)
		}
	}

	// The app's own 502 and 500 pass through untouched.
	for _, path := range []string{"/", "/500"} {
		resp, body := get(http.MethodGet, fmt.Sprintf("http://app.test:%d%s", cfg.HTTPPort, path))
		want := http.StatusBadGateway
		if path == "/500" {
			want = http.StatusInternalServerError
		}
		if resp.StatusCode != want || body != "the app's own error page" || resp.Header.Get("X-Flock-Page") != "" {
			t.Errorf("app.test%s: want the app's own %d, got %d %v %q", path, want, resp.StatusCode, resp.Header, body)
		}
	}

	// A PHP site served by FrankenPHP isn't a proxy: no page.
	resp, body := get(http.MethodGet, fmt.Sprintf("http://franken.test:%d/", cfg.HTTPPort))
	if resp.StatusCode != http.StatusBadGateway || strings.Contains(body, "Not running") || resp.Header.Get("X-Flock-Page") != "" {
		t.Errorf("franken.test: want Caddy's plain 502, got %d %v %q", resp.StatusCode, resp.Header, body)
	}
}
