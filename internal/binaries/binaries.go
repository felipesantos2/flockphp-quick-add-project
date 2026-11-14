// Package binaries reads the signed manifests of Flock's prebuilt packages (github.com/flock-php/binaries)
// and downloads packages from them.
//
// There is a manifest per channel (stable, beta, experimental). Each lists, for every package, its
// versions and for each version one entry per platform, and is signed as a whole with ed25519.
// Flock rejects a manifest whose signature doesn't verify, that is for another channel, that has
// expired, or whose serial is lower than the last one it accepted for that channel (an old
// manifest can't be replayed to hold back updates).
package binaries

import (
	"context"
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"flock/internal/archive"
	"flock/internal/download"
)

// Bases are where the manifests are published, tried in order: Flock's website (a redirect to
// the GitHub release, so its address can move), then the GitHub release itself. Which host
// answers doesn't matter for trust: every manifest is checked against the embedded key.
var Bases = []string{
	"https://flockphp.com/binaries/",
	"https://github.com/flock-php/binaries/releases/download/manifest/",
}

//go:embed manifest.pub
var publicKey string

// PublicKey is the key the manifests are signed with.
func PublicKey() ed25519.PublicKey {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKey))
	if err != nil || len(key) != ed25519.PublicKeySize {
		panic("binaries: invalid embedded manifest.pub")
	}
	return key
}

// Channel is which manifest Flock follows.
type Channel string

const (
	Stable       Channel = "stable"
	Beta         Channel = "beta"
	Experimental Channel = "experimental"
)

// ParseChannel accepts "stable", "beta" and "experimental"; "" is stable.
func ParseChannel(s string) (Channel, error) {
	switch Channel(s) {
	case "", Stable:
		return Stable, nil
	case Beta, Experimental:
		return Channel(s), nil
	}
	return "", fmt.Errorf("unknown channel %q (stable, beta or experimental)", s)
}

// File is the channel's manifest; its signature is File() + ".sig".
func (c Channel) File() string {
	if c == Stable {
		return "manifest.json"
	}
	return "manifest-" + string(c) + ".json"
}

// Manifest is every package published in a channel.
type Manifest struct {
	Schema    int                                    `json:"schema"`
	Channel   Channel                                `json:"channel"`
	Serial    int64                                  `json:"serial"`
	Generated time.Time                              `json:"generated"`
	Expires   time.Time                              `json:"expires"`
	Packages  map[string]map[string]map[string]Entry `json:"packages"` // package → version → platform
}

// Entry is the newest build of a package version for one platform in the channel. A higher Build
// of the same version replaces an installed one. Yanked lists withdrawn builds of the version
// (build number → reason): they aren't installed, and whoever has one gets a warning. When every
// build is yanked, the entry is the newest of them.
type Entry struct {
	Build      int               `json:"build"`
	URL        string            `json:"url"`
	SHA256     string            `json:"sha256"`
	Size       int64             `json:"size"`
	Published  time.Time         `json:"published"`
	Prerelease bool              `json:"prerelease,omitempty"`
	LTS        bool              `json:"lts,omitempty"` // Node.js: a long-term support release
	Yanked     map[string]string `json:"yanked,omitempty"`
}

// YankReason is why build was withdrawn, or "" when it wasn't.
func (e Entry) YankReason(build int) string { return e.Yanked[strconv.Itoa(build)] }

// Installable reports whether the entry's own build may be installed (it isn't yanked).
func (e Entry) Installable() bool { return e.YankReason(e.Build) == "" }

// Version is one version of a package on this platform.
type Version struct {
	Version string
	Entry
}

// Versions are a package's versions with an entry for platform, newest first.
func (m *Manifest) Versions(pkg, platform string) []Version {
	if m == nil {
		return nil
	}
	var out []Version
	for v, platforms := range m.Packages[pkg] {
		if e, ok := platforms[platform]; ok {
			out = append(out, Version{Version: v, Entry: e})
		}
	}
	sortVersions(out)
	return out
}

// Platform names this machine in the manifest ("macos-arm64", "windows-x64", "linux-x64"); ""
// where Flock publishes no packages (Flock supports Apple Silicon Macs, Windows x64 and Linux on
// x64 and arm64).
func Platform() string {
	return platform(runtime.GOOS, runtime.GOARCH)
}

func platform(goos, goarch string) string {
	switch {
	case goos == "darwin" && goarch == "arm64":
		return "macos-arm64"
	case goos == "windows" && goarch == "amd64":
		return "windows-x64"
	case goos == "linux" && goarch == "amd64":
		return "linux-x64"
	case goos == "linux" && goarch == "arm64":
		return "linux-arm64"
	}
	return ""
}

// Source fetches and verifies manifests.
type Source struct {
	Bases []string // tried in order (see Bases)
	Key   ed25519.PublicKey
	HTTP  *http.Client
	// UserAgent is sent with every manifest request (see UserAgent); Go's default when empty.
	UserAgent string
	// Cache keeps the last manifest accepted per channel and its serial, so an older one is
	// refused and the packages are known before (or without) the first fetch.
	Cache string
}

// DefaultSource reads the published manifests, caching them under root. version is Flock's own,
// sent in the user agent.
func DefaultSource(root, version string) Source {
	return Source{
		Bases:     Bases,
		Key:       PublicKey(),
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: UserAgent(version),
		Cache:     filepath.Join(root, "cache", "binaries"),
	}
}

// UserAgent names this Flock to the manifests' host, "Flock/1.2.0 (darwin; arm64)": Flock's
// website counts the installs that check for updates by version and platform.
func UserAgent(version string) string {
	return "Flock/" + version + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
}

// Fetch downloads the channel's manifest and its signature, from the first base that serves a
// valid one, verifies them and caches the result.
func (s Source) Fetch(ctx context.Context, ch Channel) (*Manifest, error) {
	var errs []string
	for _, base := range s.Bases {
		data, err := s.get(ctx, base+ch.File())
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		sig, err := s.get(ctx, base+ch.File()+".sig")
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		m, err := s.verify(data, sig, ch, time.Now(), true)
		if err != nil {
			errs = append(errs, base+ch.File()+": "+err.Error())
			continue
		}
		s.save(ch, data, sig, m.Serial)
		return m, nil
	}
	if len(errs) == 0 {
		return nil, errors.New("no address to fetch the package manifest from")
	}
	return nil, errors.New(strings.Join(errs, "; "))
}

// Cached is the last manifest accepted for the channel, verified again; nil when there is none.
// It may have expired: Expired tells, and nothing is installed from an expired manifest.
func (s Source) Cached(ch Channel) *Manifest {
	if s.Cache == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.Cache, ch.File()))
	if err != nil {
		return nil
	}
	sig, err := os.ReadFile(filepath.Join(s.Cache, ch.File()+".sig"))
	if err != nil {
		return nil
	}
	m, err := s.verify(data, sig, ch, time.Now(), false)
	if err != nil {
		return nil
	}
	return m
}

// Expired reports whether the manifest's signature has run out.
func (m *Manifest) Expired(now time.Time) bool { return m != nil && now.After(m.Expires) }

// verify checks the signature, schema and channel; for a fetched manifest (fresh) also that it
// hasn't expired and isn't older than the last one accepted for the channel.
func (s Source) verify(data, sig []byte, ch Channel, now time.Time, fresh bool) (*Manifest, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize || !ed25519.Verify(s.Key, data, raw) {
		return nil, errors.New("the package manifest's signature doesn't verify")
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse the package manifest: %w", err)
	}
	if m.Schema != 1 {
		return nil, fmt.Errorf("the package manifest has schema %d — update Flock", m.Schema)
	}
	if m.Channel != ch {
		return nil, fmt.Errorf("the package manifest is for the %q channel, not %q", m.Channel, ch)
	}
	if !fresh {
		return &m, nil
	}
	if m.Expired(now) {
		return nil, fmt.Errorf("the package manifest expired on %s", m.Expires.Format(time.DateOnly))
	}
	if last := s.lastSerial(ch); m.Serial < last {
		return nil, fmt.Errorf("the package manifest is older than one already seen (serial %d < %d)", m.Serial, last)
	}
	return &m, nil
}

func (s Source) serialFile(ch Channel) string {
	return filepath.Join(s.Cache, string(ch)+".serial")
}

func (s Source) lastSerial(ch Channel) int64 {
	if s.Cache == "" {
		return 0
	}
	data, err := os.ReadFile(s.serialFile(ch))
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	return n
}

// save keeps an accepted manifest and its serial (best effort: a cache that can't be written
// only means fetching again, and serial checks then compare against the last one written).
func (s Source) save(ch Channel, data, sig []byte, serial int64) {
	if s.Cache == "" || os.MkdirAll(s.Cache, 0o755) != nil {
		return
	}
	write := func(name string, b []byte) {
		tmp := filepath.Join(s.Cache, name+".tmp")
		if os.WriteFile(tmp, b, 0o644) == nil {
			_ = os.Rename(tmp, filepath.Join(s.Cache, name))
		}
	}
	write(ch.File(), data)
	write(ch.File()+".sig", sig)
	write(string(ch)+".serial", []byte(strconv.FormatInt(serial, 10)+"\n"))
}

func (s Source) get(ctx context.Context, url string) ([]byte, error) {
	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// Install downloads e into a work directory next to dir, verifies its SHA-256 and unpacks it
// into dir, which must not exist yet. progress gets the bytes downloaded so far and the total.
func Install(ctx context.Context, client *http.Client, e Entry, dir string, progress func(done, total int64)) error {
	if !e.Installable() {
		return fmt.Errorf("build %d was withdrawn: %s", e.Build, e.YankReason(e.Build))
	}
	work := dir + ".download"
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(work)

	name := filepath.Base(strings.SplitN(e.URL, "?", 2)[0])
	if !archive.IsArchive(name) {
		return fmt.Errorf("%s is not a .zip or .tar.gz", name)
	}
	file := filepath.Join(work, name)
	if e.SHA256 == "" {
		return fmt.Errorf("the package manifest has no checksum for %s", name)
	}
	if err := download.File(ctx, client, e.URL, file, e.SHA256, progress); err != nil {
		return err
	}
	return archive.Extract(file, dir)
}

var versionParts = regexp.MustCompile(`^(\d+(?:\.\d+)*)(.*)$`)

// CompareVersions orders versions like 8.4.9 < 8.4.10, with a pre-release before its release:
// 8.6.0RC1 < 8.6.0RC2 < 8.6.0.
func CompareVersions(a, b string) int {
	ma, mb := versionParts.FindStringSubmatch(a), versionParts.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return strings.Compare(a, b)
	}
	pa, pb := strings.Split(ma[1], "."), strings.Split(mb[1], ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch sa, sb := strings.ToLower(ma[2]), strings.ToLower(mb[2]); {
	case sa == sb:
		return 0
	case sa == "":
		return 1
	case sb == "":
		return -1
	default:
		return strings.Compare(sa, sb)
	}
}

func sortVersions(vs []Version) {
	for i := 1; i < len(vs); i++ {
		for j := i; j > 0 && CompareVersions(vs[j].Version, vs[j-1].Version) > 0; j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}
