package binaries

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func signed(priv ed25519.PrivateKey, body string) ([]byte, []byte) {
	data := []byte(body)
	return data, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)) + "\n")
}

func manifestJSON(channel string, serial int64, expires string) string {
	return fmt.Sprintf(`{"schema":1,"channel":%q,"serial":%d,"expires":%q,"packages":{"frankenphp":{`+
		`"8.4.26":{"macos-arm64":{"build":2,"url":"https://example.test/f.tar.gz","sha256":"ab","size":1,"yanked":{"1":"broken"}}},`+
		`"8.6.0RC2":{"macos-arm64":{"build":1,"url":"https://example.test/r.tar.gz","sha256":"cd","size":1,"prerelease":true}}}}}`,
		channel, serial, expires)
}

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := Source{Key: pub, Cache: t.TempDir()}
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	const exp = "2026-11-01T00:00:00Z"

	data, sig := signed(priv, manifestJSON("beta", 100, exp))
	m, err := s.verify(data, sig, Beta, now, true)
	if err != nil {
		t.Fatal(err)
	}
	e := m.Packages["frankenphp"]["8.4.26"]["macos-arm64"]
	if e.Build != 2 || e.YankReason(1) != "broken" || !e.Installable() {
		t.Errorf("entry: %+v", e)
	}
	if !m.Packages["frankenphp"]["8.6.0RC2"]["macos-arm64"].Prerelease {
		t.Error("prerelease flag lost")
	}

	if _, err := s.verify(append(data, ' '), sig, Beta, now, true); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Errorf("tampered manifest: %v", err)
	}
	if _, err := s.verify(data, sig, Stable, now, true); err == nil || !strings.Contains(err.Error(), `for the "beta" channel`) {
		t.Errorf("a beta manifest taken for stable: %v", err)
	}
	if _, err := s.verify(data, sig, Beta, now.AddDate(0, 1, 0), true); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired manifest: %v", err)
	}
	if _, err := s.verify(data, sig, Beta, now.AddDate(0, 1, 0), false); err != nil {
		t.Errorf("an expired cached manifest is still readable: %v", err)
	}

	// Serials are per channel: stable at 200 doesn't hold back beta at 100.
	s.save(Beta, data, sig, 100)
	st, stSig := signed(priv, manifestJSON("stable", 200, exp))
	s.save(Stable, st, stSig, 200)
	if _, err := s.verify(data, sig, Beta, now, true); err != nil {
		t.Errorf("the same beta manifest again: %v", err)
	}
	old, oldSig := signed(priv, manifestJSON("beta", 99, exp))
	if _, err := s.verify(old, oldSig, Beta, now, true); err == nil || !strings.Contains(err.Error(), "older") {
		t.Errorf("replayed beta manifest: %v", err)
	}
	if c := s.Cached(Stable); c == nil || c.Serial != 200 {
		t.Errorf("cached stable: %+v", c)
	}
}

// server serves a channel's manifest; its responses can be broken per path.
func server(t *testing.T, priv ed25519.PrivateKey, channel string, serial *atomic.Int64, broken map[string]bool) *httptest.Server {
	// Fixed per server: the manifest and its signature are separate requests, and a time taken
	// per request may differ between them, which makes the signature not match.
	expires := time.Now().AddDate(0, 0, 30).Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken[r.URL.Path] {
			http.Error(w, "nope", http.StatusBadGateway)
			return
		}
		ch := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/m/"), ".sig"), "manifest"), ".json")
		ch = strings.TrimPrefix(ch, "-")
		if ch == "" {
			ch = "stable"
		}
		data, sig := signed(priv, manifestJSON(ch, serial.Load(), expires))
		if strings.HasSuffix(r.URL.Path, ".sig") {
			w.Write(sig)
		} else {
			w.Write(data)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchFallsBackAndCaches(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var serial atomic.Int64
	serial.Store(5)
	site := server(t, priv, "stable", &serial, map[string]bool{"/m/manifest-beta.json": true})
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	forged := server(t, otherPriv, "stable", &serial, nil)
	github := server(t, priv, "stable", &serial, nil)
	cache := t.TempDir()
	s := Source{Bases: []string{site.URL + "/m/", github.URL + "/m/"}, Key: pub, HTTP: site.Client(), Cache: cache}

	// Beta is broken on the website: GitHub answers.
	m, err := s.Fetch(context.Background(), Beta)
	if err != nil || m.Channel != Beta {
		t.Fatalf("beta: %v %+v", err, m)
	}
	// A host serving a manifest signed with another key is skipped, not trusted.
	s.Bases = []string{forged.URL + "/m/", github.URL + "/m/"}
	if m, err := s.Fetch(context.Background(), Stable); err != nil || m.Serial != 5 {
		t.Fatalf("stable past a forged host: %v %+v", err, m)
	}
	if _, err := os.Stat(filepath.Join(cache, "manifest-beta.json")); err != nil {
		t.Error("beta manifest not cached")
	}
	// Nothing reachable: the error names every address.
	s.Bases = []string{forged.URL + "/m/"}
	if _, err := s.Fetch(context.Background(), Stable); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Errorf("only a forged host: %v", err)
	}
}

func TestFetchSendsTheUserAgent(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var serial atomic.Int64
	serial.Store(1)
	site := server(t, priv, "stable", &serial, nil)
	var agents []string
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.UserAgent())
		http.Redirect(w, r, site.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(recorder.Close)

	s := DefaultSource(t.TempDir(), "1.2.0")
	s.Bases, s.Key = []string{recorder.URL + "/m/"}, pub
	if _, err := s.Fetch(context.Background(), Stable); err != nil {
		t.Fatal(err)
	}

	want := "Flock/1.2.0 (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
	if len(agents) != 2 || agents[0] != want || agents[1] != want {
		t.Errorf("user agents: %q, want %q for the manifest and its signature", agents, want)
	}
}

func TestCatalogFollowsTheChannel(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var serial atomic.Int64
	serial.Store(1)
	srv := server(t, priv, "stable", &serial, nil)
	var channel atomic.Value
	channel.Store(Stable)
	s := Source{Bases: []string{srv.URL + "/m/"}, Key: pub, HTTP: srv.Client(), Cache: t.TempDir()}
	c := NewCatalog(s, func() Channel { return channel.Load().(Channel) })
	changed := make(chan struct{}, 4)
	c.OnChange(func() { changed <- struct{}{} })

	if c.Manifest() != nil {
		t.Fatal("a manifest before any fetch")
	}
	if m, err := c.Check(context.Background()); err != nil || m.Channel != Stable {
		t.Fatalf("check: %v", err)
	}
	<-changed
	if st := c.Status(); st.Channel != Stable || st.CheckedAt == nil || st.CheckError != "" {
		t.Errorf("status: %+v", st)
	}

	// Switching the channel drops the stable manifest; beta has no cache yet.
	channel.Store(Beta)
	c.ChannelChanged()
	waitFor(t, func() bool { m := c.Manifest(); return m != nil && m.Channel == Beta })

	// Back to stable: its cached manifest is there at once.
	channel.Store(Stable)
	c.mu.Lock()
	c.sync()
	m := c.manifest
	c.mu.Unlock()
	if m == nil || m.Channel != Stable {
		t.Errorf("cached stable after switching back: %+v", m)
	}

	// An unchanged manifest doesn't fire OnChange again.
	for len(changed) > 0 {
		<-changed
	}
	if _, err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
		t.Error("OnChange for an unchanged manifest")
	case <-time.After(100 * time.Millisecond):
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestVersionsAndCompare(t *testing.T) {
	m := &Manifest{Packages: map[string]map[string]map[string]Entry{"php": {
		"8.4.9": {"macos-arm64": {}}, "8.4.10": {"macos-arm64": {}}, "8.6.0RC1": {"macos-arm64": {}},
		"8.6.0": {"macos-arm64": {}}, "8.5.0": {"windows-x64": {}},
	}}}
	var got []string
	for _, v := range m.Versions("php", "macos-arm64") {
		got = append(got, v.Version)
	}
	if strings.Join(got, " ") != "8.6.0 8.6.0RC1 8.4.10 8.4.9" {
		t.Errorf("versions: %v", got)
	}
	for _, c := range [][3]string{{"8.6.0RC1", "8.6.0RC2", "-1"}, {"8.6.0", "8.6.0RC2", "1"}, {"8.10", "8.9", "1"}, {"1.0", "1.0.0", "0"}} {
		if got := fmt.Sprint(CompareVersions(c[0], c[1])); got != c[2] {
			t.Errorf("CompareVersions(%s, %s) = %s, want %s", c[0], c[1], got, c[2])
		}
	}
	for s, want := range map[string]Channel{"": Stable, "stable": Stable, "beta": Beta, "experimental": Experimental} {
		if got, err := ParseChannel(s); err != nil || got != want {
			t.Errorf("ParseChannel(%q) = %q, %v", s, got, err)
		}
	}
	if _, err := ParseChannel("nightly"); err == nil {
		t.Error("ParseChannel accepted nightly")
	}
	if Stable.File() != "manifest.json" || Beta.File() != "manifest-beta.json" {
		t.Error("File")
	}
}

func TestEmbeddedKeyAndPlatforms(t *testing.T) {
	if len(PublicKey()) != ed25519.PublicKeySize {
		t.Fatal("embedded key")
	}
	for _, c := range [][3]string{{"darwin", "arm64", "macos-arm64"}, {"darwin", "amd64", ""}, {"windows", "amd64", "windows-x64"}, {"linux", "amd64", "linux-x64"}, {"linux", "arm64", "linux-arm64"}, {"linux", "386", ""}} {
		if got := platform(c[0], c[1]); got != c[2] {
			t.Errorf("%s/%s: %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
