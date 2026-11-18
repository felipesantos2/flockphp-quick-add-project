package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig"
)

func testConfig() Config {
	return Config{
		StorageDir: "/data/Application Support/caddy",
		LogFile:    "/data/web.log",
		HTTPPort:   80,
		HTTPSPort:  443,
		Sites: []Site{
			{Domain: "plain.test", Root: "/code/plain/public", FPM: []string{"unix//run/php85.sock"}},
			{Domain: "safe.test", Root: "/code/safe/public", FPM: []string{"unix//run/php84.sock"}, CertFile: "/c/safe.crt", KeyFile: "/c/safe.key"},
		},
	}
}

func TestCaddyfileAdaptsWithoutErrors(t *testing.T) {
	if _, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(Caddyfile(testConfig())), nil); err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(testConfig()))
	}
}

func TestCaddyfileRoutesSitesAndSubdomains(t *testing.T) {
	cf := Caddyfile(testConfig())

	for _, want := range []string{
		"http://plain.test:80, http://*.plain.test:80, http://*.*.plain.test:80, http://*.*.*.plain.test:80, http://*.*.*.*.plain.test:80 {",
		"https://safe.test:443, https://*.safe.test:443, https://*.*.safe.test:443, https://*.*.*.safe.test:443, https://*.*.*.*.safe.test:443 {",
		"get_certificate flock",
		"redir https://{host}{uri} 308",
		"https://:443 {",
		"rewrite * /unlinked.html",
		"https://plain.test:443, https://*.plain.test:443, https://*.*.plain.test:443, https://*.*.*.plain.test:443, https://*.*.*.*.plain.test:443 {",
		`vars flock_site "plain"`,
		`php_fastcgi "unix//run/php84.sock"`,
		`storage file_system "/data/Application Support/caddy"`,
		"abort @remote",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("missing %q in:\n%s", want, cf)
		}
	}
}

func TestCaddyfileRunsSiteHandlersInOrderBeforePHP(t *testing.T) {
	cfg := testConfig()
	cfg.Sites[1].DebugFPM = []string{"unix//run/php84-xdebug.sock"}
	cfg.Sites[1].Handlers = "handle /custom {\r\n\theader X-Flock-Custom yes\r\n\trespond custom 200\r\n}\r\n"
	cf := Caddyfile(cfg)
	want := "\troute {\n\t\troot * " + quote(cfg.Sites[1].Root) + "\n\t\thandle /custom {\n\t\t\theader X-Flock-Custom yes\n\t\t\trespond custom 200\n\t\t}\n\t\t@xdebug"
	if !strings.Contains(cf, want) {
		t.Fatalf("custom handlers are not in the route between the root and PHP:\n%s", cf)
	}
	if strings.Contains(cf, "\r") {
		t.Fatalf("CRLF line endings kept:\n%q", cf)
	}
	adapted, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(cf), nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, cf)
	}
	if !strings.Contains(string(adapted), `"X-Flock-Custom"`) {
		t.Fatalf("custom header missing from adapted config:\n%s", adapted)
	}
}

func TestCheckHandlers(t *testing.T) {
	site := Site{Domain: "app.test", Root: "/code/app/public", FPM: []string{"unix//run/php85.sock"}, DebugFPM: []string{"unix//run/php85-xdebug.sock"}}
	for _, tc := range []struct {
		name, handlers, want string
	}{
		{"cors", "@cors_preflight method OPTIONS\nheader Access-Control-Allow-Origin \"https://admin.example.test\"\nrespond @cors_preflight 204\n", ""},
		{"docs example", "@cors_preflight method OPTIONS\n\nheader {\n    Access-Control-Allow-Origin \"https://admin.example.test\"\n    Access-Control-Allow-Methods \"GET, POST, PUT, PATCH, DELETE, OPTIONS\"\n    Access-Control-Allow-Headers \"Content-Type, Authorization\"\n    Vary \"Origin\"\n}\n\nrespond @cors_preflight 204\n", ""},
		{"braces in quotes", "respond \"{ not a block\" 200\n", ""},
		{"placeholders", "header X-Host {host}\n", ""},
		{"breakout", "header X-A b\n}\n}\n:9999 {\n\tfile_server\n", "app.caddy:2: } without a matching {"},
		{"unclosed", "handle {\n\trespond x\n", "app.caddy: 1 { without a matching }"},
		{"import", "header X-A b\nimport /etc/caddy/*\n", "app.caddy:2: import isn't supported here"},
		{"unknown directive", "header X-A b\nfoo bar\n", "app.caddy: parsing caddyfile tokens for 'route': unrecognized directive: foo"},
		{"nested error", "\n\nheader X-A b\nrespond 204 {\n\tclose\n\tnope\n}\n", "unrecognized subdirective 'nope', at app.caddy:6"},
		{"Flock's matcher", "@xdebug path /x\nrespond @xdebug 200\n", "app.caddy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := site
			s.Handlers = tc.handlers
			err := CheckHandlers(s, "app.caddy")
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("got %v, want an error with %q", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "flock-site-handlers") {
				t.Fatalf("error names the generated config: %v", err)
			}
		})
	}
}

func TestWithBindQualifiesListenAddresses(t *testing.T) {
	in := []byte(`{"apps":{"http":{"servers":{"srv0":{"listen":[":443"]}}}}}`)

	if got := string(withBind(in, "127.0.0.1")); !strings.Contains(got, `"127.0.0.1:443"`) {
		t.Fatalf("got %s", got)
	}
	if got := string(withBind(in, "")); got != string(in) {
		t.Fatalf("empty bind must not change config, got %s", got)
	}
}

func TestCaddyfileBalancesPHPCGIWorkersAndKeepsWindowsPaths(t *testing.T) {
	cfg := Config{
		StorageDir: `C:\Users\me\AppData\Roaming\Flock\caddy`,
		LogFile:    `C:\Users\me\AppData\Roaming\Flock\log\web.log`,
		HTTPPort:   80,
		HTTPSPort:  443,
		Sites: []Site{{
			Domain: "app.test", Root: `C:\code\app\public`,
			FPM:      []string{"127.0.0.1:19840", "127.0.0.1:19841"},
			CertFile: `C:\Users\me\AppData\Roaming\Flock\certs\app.test.crt`, KeyFile: `C:\Users\me\AppData\Roaming\Flock\certs\app.test.key`,
		}},
	}

	json, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(Caddyfile(cfg)), nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	for _, want := range []string{
		`"dial":"127.0.0.1:19840"`, `"dial":"127.0.0.1:19841"`, `"policy":"least_conn"`,
		`"root":"C:/code/app/public"`,
		`"via":"flock"`,
		`"root":"C:/Users/me/AppData/Roaming/Flock/caddy"`,
	} {
		if !strings.Contains(string(json), want) {
			t.Errorf("missing %s in:\n%s", want, json)
		}
	}
}

func TestCaddyfileSendsXdebugRequestsToTheDebugPool(t *testing.T) {
	cfg := testConfig()
	cfg.Sites = []Site{
		{Domain: "plain.test", Root: "/code/plain/public", FPM: []string{"unix//run/php85.sock"}, DebugFPM: []string{"unix//run/php85-xdebug.sock"}},
		{Domain: "debugged.test", Root: "/code/debugged/public", FPM: []string{"unix//run/php84.sock"}, DebugFPM: []string{"unix//run/php84-xdebug.sock"}, Debugging: true},
	}

	json, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(Caddyfile(cfg)), nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	for _, want := range []string{
		`"dial":"unix//run/php85-xdebug.sock"`, `"dial":"unix//run/php85.sock"`, `XDEBUG_SESSION`, `XDEBUG_TRIGGER`,
		// With breakpoints, the site has no fast pool at all.
		`"dial":"unix//run/php84-xdebug.sock"`,
	} {
		if !strings.Contains(string(json), want) {
			t.Errorf("missing %s in:\n%s", want, json)
		}
	}
	if strings.Contains(string(json), `"dial":"unix//run/php84.sock"`) {
		t.Errorf("a site with breakpoints still uses its fast pool:\n%s", json)
	}
}

func TestCaddyfileProxiesFrankenPHPSitesButNotTheirXdebugRequests(t *testing.T) {
	cfg := testConfig()
	cfg.Sites = []Site{
		{Domain: "franken.test", Root: "/code/franken/public", Proxy: "127.0.0.1:21000", DebugFPM: []string{"unix//run/php84-xdebug.sock"}},
		{Domain: "octane.test", Root: "/code/octane/public", Proxy: "127.0.0.1:21001"},
	}

	json, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(Caddyfile(cfg)), nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	for _, want := range []string{`"dial":"127.0.0.1:21000"`, `"dial":"127.0.0.1:21001"`, `"dial":"unix//run/php84-xdebug.sock"`, `XDEBUG_TRIGGER`} {
		if !strings.Contains(string(json), want) {
			t.Errorf("missing %s in:\n%s", want, json)
		}
	}
}

func TestCaddyfileProxiesNamesToTheirServer(t *testing.T) {
	cfg := testConfig()
	cfg.Sites = append(cfg.Sites,
		Site{Domain: "next.test", Proxy: "http://127.0.0.1:3000"},
		Site{Domain: "s3.test", Proxy: "http://127.0.0.1:9000", CertFile: "/c/s3.crt", KeyFile: "/c/s3.key"},
		Site{Domain: "api.test", Proxy: "https://127.0.0.1:8443"},
	)
	cf := Caddyfile(cfg)
	if _, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(cf), nil); err != nil {
		t.Fatalf("%v\n%s", err, cf)
	}

	for _, want := range []string{
		"http://next.test:80, http://*.next.test:80, http://*.*.next.test:80, http://*.*.*.next.test:80, http://*.*.*.*.next.test:80 {\n\timport flock_site\n\treverse_proxy \"http://127.0.0.1:3000\"\n",
		"https://s3.test:443, https://*.s3.test:443, https://*.*.s3.test:443, https://*.*.*.s3.test:443, https://*.*.*.*.s3.test:443 {\n\timport flock_site\n\ttls {\n\t\tget_certificate flock\n\t}\n\treverse_proxy \"http://127.0.0.1:9000\"\n",
		"\tvars {\n\t\tflock_site \"next\"\n\t\tflock_proxy \"1\"\n\t}\n",
		"\treverse_proxy \"https://127.0.0.1:8443\" {\n\t\theader_up Host {http.request.hostport}\n\t}\n",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("missing %q in:\n%s", want, cf)
		}
	}
	if !strings.Contains(cf, `vars flock_site "plain"`+"\n") || strings.Count(cf, "flock_proxy") != 2 {
		t.Errorf("only the unsecured proxies' HTTPS pages say they're proxies:\n%s", cf)
	}
	// Only the blocks that pass requests to a proxy's server show its page when the server is down:
	// next's and api's over HTTP, s3's over HTTPS. The PHP sites keep their plain errors.
	if n := strings.Count(cf, "handle_errors 502 {"); n != 3 || !strings.Contains(cf, "\t\t\tflock_down_upstream \"127.0.0.1:3000\"\n") ||
		!strings.Contains(cf, "\t\t\tflock_down_upstream \"https://127.0.0.1:8443\"\n") {
		t.Errorf("want the upstream-down page on the 3 proxies' serving blocks, got %d in:\n%s", n, cf)
	}
	if strings.Contains(cf, `root * ""`) {
		t.Errorf("a proxy has no document root:\n%s", cf)
	}
}

// TestCaddyfileRejectsRemoteClientsBeforeAnyHandler checks the adapted config of every kind of
// block: Caddy sorts handle and route before abort, so a site's Xdebug handle pair must not run
// before the remote guard.
func TestCaddyfileRejectsRemoteClientsBeforeAnyHandler(t *testing.T) {
	cfg := testConfig()
	cfg.Sites = append(cfg.Sites,
		Site{Domain: "xdebug.test", Root: "/code/xdebug/public", FPM: []string{"unix//run/php85.sock"}, DebugFPM: []string{"unix//run/php85-xdebug.sock"}},
		Site{Domain: "franken.test", Root: "/code/franken/public", Proxy: "127.0.0.1:21000", DebugFPM: []string{"unix//run/php84-xdebug.sock"}},
	)

	adapted, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(Caddyfile(cfg)), nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	type route struct {
		Match  []map[string]any `json:"match"`
		Handle []struct {
			Handler string  `json:"handler"`
			Abort   bool    `json:"abort"`
			Routes  []route `json:"routes"`
		} `json:"handle"`
	}
	var parsed struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []route `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(adapted, &parsed); err != nil {
		t.Fatal(err)
	}

	checked := 0
	for name, server := range parsed.Apps.HTTP.Servers {
		for i, r := range server.Routes {
			// A site block is a host-matched route around a subroute of its directives.
			first := r
			if len(r.Handle) == 1 && r.Handle[0].Handler == "subroute" && len(r.Handle[0].Routes) > 0 {
				first = r.Handle[0].Routes[0]
			}
			if len(first.Handle) == 0 || first.Handle[0].Handler != "static_response" || !first.Handle[0].Abort {
				t.Errorf("%s route %d (%v) does not start with the remote abort", name, i, r.Match)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatalf("no routes in:\n%s", adapted)
	}
}

func TestCaddyfileSendsReverbRequestsFirst(t *testing.T) {
	cfg := testConfig()
	cfg.Sites = []Site{
		{Domain: "chat.test", Root: "/code/chat/public", FPM: []string{"unix//run/php84.sock"}, Reverb: "127.0.0.1:21001", DebugFPM: []string{"unix//run/php84-xdebug.sock"}},
		{Domain: "octane.test", Root: "/code/octane/public", Proxy: "127.0.0.1:21002", Reverb: "127.0.0.1:21003"},
	}

	cf := Caddyfile(cfg)
	json, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(cf), nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, cf)
	}
	for _, want := range []string{`"dial":"127.0.0.1:21001"`, `"dial":"127.0.0.1:21003"`, `"/apps/*"`, `query.auth_signature`} {
		if !strings.Contains(string(json), want) {
			t.Errorf("missing %s in:\n%s", want, json)
		}
	}
	// Xdebug's handle block would win if it came first.
	if strings.Index(cf, "handle @reverb") > strings.Index(cf, "handle @xdebug") {
		t.Errorf("Reverb's route comes after Xdebug's:\n%s", cf)
	}
	if strings.Contains(Caddyfile(testConfig()), "@reverb") {
		t.Error("a site without Reverb has its route")
	}

	// With custom handlers the site is a route, which keeps the written order: Reverb comes first.
	cfg.Sites[0].Handlers = "handle /app/* {\n\trespond custom 200\n}\n"
	cf = Caddyfile(cfg)
	if _, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(cf), nil); err != nil {
		t.Fatalf("%v\n%s", err, cf)
	}
	if r, c := strings.Index(cf, "\t\thandle @reverb"), strings.Index(cf, "\t\thandle /app/*"); r < 0 || c < 0 || r > c {
		t.Errorf("Reverb's route isn't ahead of the custom handlers in the route:\n%s", cf)
	}
}
