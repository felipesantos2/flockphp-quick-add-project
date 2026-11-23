package daemon

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"flock/internal/config"
	"flock/internal/ports"
)

// upstream is a dev server: it answers with the Host and X-Forwarded-Proto it got, and echoes a
// line back over a WebSocket-style upgrade, as hot reload uses.
func upstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			io.WriteString(w, r.Host+" "+r.Header.Get("X-Forwarded-Proto"))
			return
		}
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		line, _ := rw.ReadString('\n')
		rw.WriteString("echo " + line)
		rw.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProxiesPassRequestsAndUpgradesToTheirServer(t *testing.T) {
	dev := upstream(t)
	root, cfg := testRoot(t, func(c *config.Config) {
		if err := c.UpsertProxy(config.Proxy{Name: "softpack", Upstream: dev.URL}); err != nil {
			t.Fatal(err)
		}
	})
	client := startDaemon(t, root)

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Proxies) != 1 || status.Proxies[0].URL != "http://softpack.test:"+strconv.Itoa(cfg.Ports.HTTP) || status.Proxies[0].Problem != "" {
		t.Fatalf("unexpected proxies: %+v", status.Proxies)
	}
	// The test binary listens there, so the upstream is running. Process names are unavailable when
	// the platform cannot inspect the listener.
	if !status.Proxies[0].Running {
		t.Errorf("the upstream answers: %+v", status.Proxies[0])
	}

	web := "127.0.0.1:" + strconv.Itoa(cfg.Ports.HTTP)
	req, _ := http.NewRequest(http.MethodGet, "http://"+web+"/_next/app.js", nil)
	req.Host = "softpack.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "softpack.test http" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}

	conn, err := net.DialTimeout("tcp", web, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(conn, "GET /_next/webpack-hmr HTTP/1.1\r\nHost: softpack.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	r := bufio.NewReader(conn)
	upgraded, err := http.ReadResponse(r, nil)
	if err != nil || upgraded.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %+v", err, upgraded)
	}
	io.WriteString(conn, "ping\n")
	if line, err := r.ReadString('\n'); err != nil || line != "echo ping\n" {
		t.Fatalf("over the upgraded connection: %q %v", line, err)
	}
}

func TestAProxyWinsOverAParkedFolderOfTheSameName(t *testing.T) {
	dev := upstream(t)
	parked := t.TempDir()
	root, cfg := testRoot(t, func(c *config.Config) {
		c.Proxies = []config.Proxy{{Name: "blog", Upstream: dev.URL}}
		c.Parked = []string{parked}
	})
	if err := os.Mkdir(filepath.Join(parked, "blog"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := startDaemon(t, root)

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Sites) != 0 || len(status.Proxies) != 1 || status.Proxies[0].Problem != "" {
		t.Fatalf("sites %+v, proxies %+v", status.Sites, status.Proxies)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/", nil)
	req.Host = "blog.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "blog.test") {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
}

func TestAProxyWhoseServerIsDownSaysSo(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	ln.Close()
	root, _ := testRoot(t, func(c *config.Config) {
		c.Proxies = []config.Proxy{{Name: "api", Upstream: "http://" + closed}}
	})
	client := startDaemon(t, root)

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Proxies) != 1 || status.Proxies[0].Running || status.Proxies[0].Server != "" {
		t.Fatalf("unexpected proxies: %+v", status.Proxies)
	}
}

func TestServerNameNamesFlocksNode(t *testing.T) {
	root := filepath.Join(string(filepath.Separator)+"data", "Flock")
	for h, want := range map[ports.Holder]string{
		{Process: "node", Path: filepath.Join(root, "node", "22.23.4", "bin", "node")}: "Node.js 22.23.4",
		{Process: "node", Path: "/opt/homebrew/bin/node"}:                              "Node.js",
		{Process: "next-server (v15.5.28)"}:                                            "next-server (v15.5.28)",
		{Process: "bun", Path: "/Users/me/.bun/bin/bun"}:                               "bun",
		{}: "",
	} {
		if got := serverName(root, h); got != want {
			t.Errorf("serverName(%+v) = %q, want %q", h, got, want)
		}
	}
}
