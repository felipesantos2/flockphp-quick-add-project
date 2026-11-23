package releasesync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-github/v93/github"
)

const (
	commitA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	mergeA     = "cccccccccccccccccccccccccccccccccccccccc"
	mergeB     = "dddddddddddddddddddddddddddddddddddddddd"
	commitOld  = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	commitOld2 = "ffffffffffffffffffffffffffffffffffffffff"
)

func commitLink(hash string) string {
	return "([" + hash[:7] + "](https://github.com/o/r/commit/" + hash + "))"
}

func TestRunCollectsBeforeWritingAndSkipsInternalPRs(t *testing.T) {
	var writes []string
	failFiles := false
	alreadySynced := false
	milestoneState := "open"
	releaseState := "open"
	releaseSHA := "head"
	outsideRange := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PATCH":
			var body struct {
				Milestone int `json:"milestone"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Milestone != 2 {
				t.Errorf("milestone update body = %#v, error = %v", body, err)
			}
			writes = append(writes, r.URL.Path)
			fmt.Fprint(w, `{}`)
		case r.URL.Path == "/repos/o/r/milestones/2":
			fmt.Fprintf(w, `{"number":2,"state":%q}`, milestoneState)
		case r.URL.Path == "/repos/o/r/pulls/134":
			existing := "null"
			if alreadySynced {
				existing = `{"number":2}`
			}
			notes := "https://github.com/o/r/compare/v1.0.0...v1.1.0\n" + commitLink(commitA) + "\n" + commitLink(commitB)
			if outsideRange {
				notes += "\n" + commitLink(commitOld)
			}
			fmt.Fprintf(w, `{"number":134,"state":%q,"milestone":%s,"base":{"sha":%q,"ref":"main"},"body":%q}`, releaseState, existing, releaseSHA, notes)
		case strings.Contains(r.URL.Path, "/compare/"):
			if r.URL.Path != "/repos/o/r/compare/v1.0.0...head" {
				t.Errorf("compare path = %s, want release PR base SHA", r.URL.Path)
			}
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprintf(w, `{"status":"ahead","commits":[{"sha":%q},{"sha":%q}]}`, commitB, mergeB)
			} else {
				w.Header().Set("Link", `<http://example.com/?page=2>; rel="next"`)
				fmt.Fprintf(w, `{"status":"ahead","commits":[{"sha":%q},{"sha":%q}]}`, commitA, mergeA)
			}
		case strings.Contains(r.URL.Path, "/commits/"+commitA+"/pulls"):
			existing := "null"
			if alreadySynced {
				existing = `{"number":2}`
			}
			fmt.Fprintf(w, `[{"number":1,"milestone":%s,"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":%q,"base":{"ref":"main"}}]`, existing, mergeA)
		case strings.Contains(r.URL.Path, "/commits/"+commitB+"/pulls"):
			fmt.Fprintf(w, `[{"number":2,"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":%q,"base":{"ref":"main"}}]`, mergeB)
		case r.URL.Path == "/repos/o/r/pulls/1/files":
			fmt.Fprint(w, `[{"filename":"internal/web/share.go"}]`)
		case r.URL.Path == "/repos/o/r/pulls/2/files":
			if failFiles {
				http.Error(w, "failed", http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, `[{"filename":".github/workflows/ci.yml"},{"filename":"internal/web/share_test.go"}]`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	base := server.URL + "/"
	client, err := github.NewClient(github.WithURLs(&base, nil))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	mustFailClosed := func(name, want string) {
		t.Helper()
		writes = nil
		err := Run(context.Background(), client, "o", "r", 134, 2, &output)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: error = %v, want %q", name, err, want)
		}
		if len(writes) != 0 {
			t.Fatalf("%s: patched %v", name, writes)
		}
	}
	milestoneState = "closed"
	mustFailClosed("closed milestone", "not open")
	milestoneState = "open"
	releaseState = "closed"
	mustFailClosed("closed release PR", "must be open and have a base commit")
	releaseState = "open"
	releaseSHA = ""
	mustFailClosed("empty release base", "must be open and have a base commit")
	releaseSHA = "head"
	outsideRange = true
	mustFailClosed("commit outside release range", commitOld)
	outsideRange = false
	failFiles = true
	if err := Run(context.Background(), client, "o", "r", 134, 2, &output); err == nil {
		t.Fatal("expected file listing failure")
	}
	if len(writes) != 0 {
		t.Fatalf("wrote before collecting all data: %v", writes)
	}
	failFiles = false
	if err := Run(context.Background(), client, "o", "r", 134, 2, &output); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(writes, ","); got != "/repos/o/r/issues/134,/repos/o/r/issues/1" {
		t.Fatalf("writes = %s", got)
	}
	if !strings.Contains(output.String(), "Skipped non-runtime PR #2\n") {
		t.Fatalf("output = %q", output.String())
	}
	writes = nil
	alreadySynced = true
	if err := Run(context.Background(), client, "o", "r", 134, 2, &output); err != nil {
		t.Fatal(err)
	}
	if len(writes) != 0 {
		t.Fatalf("already synced PRs were written: %v", writes)
	}
}

func TestApplyStopsOnWriteFailure(t *testing.T) {
	var writes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes = append(writes, r.URL.Path)
		http.Error(w, "failed", http.StatusInternalServerError)
	}))
	defer server.Close()
	base := server.URL + "/"
	client, err := github.NewClient(github.WithURLs(&base, nil))
	if err != nil {
		t.Fatal(err)
	}
	current := &state{
		client: client, owner: "o", repo: "r", milestone: 2, out: &bytes.Buffer{},
		changes: []change{{number: 1}, {number: 2}},
	}
	if err := current.apply(context.Background()); err == nil || !strings.Contains(err.Error(), "PR #1") {
		t.Fatalf("apply error = %v, want PR #1 failure", err)
	}
	if got := strings.Join(writes, ","); got != "/repos/o/r/issues/1" {
		t.Fatalf("writes = %s", got)
	}
}

func TestPlanUsesCollectedSnapshot(t *testing.T) {
	current := &state{
		milestone: 2,
		observed: observed{
			release:   &github.PullRequest{Number: new(134)},
			unmatched: []string{commitA},
			candidates: []candidate{
				{pull: &github.PullRequest{Number: new(1)}, files: []*github.CommitFile{{Filename: new("internal/web/share.go")}}},
				{pull: &github.PullRequest{Number: new(2)}, files: []*github.CommitFile{{Filename: new("docs/readme.md")}}},
				{pull: &github.PullRequest{Number: new(3), Milestone: &github.Milestone{Number: new(2)}},
					files: []*github.CommitFile{{Filename: new("linux/src/main.rs")}}},
			},
		},
	}
	current.plan()
	want := []change{{unmatched: commitA}, {number: 134}, {number: 1}, {number: 2, nonRuntime: true}, {number: 3, already: true}}
	if !reflect.DeepEqual(current.changes, want) {
		t.Fatalf("plan = %#v, want %#v", current.changes, want)
	}
}

func TestReleaseNotes(t *testing.T) {
	body := "https://github.com/o/r/compare/v1.0.0...v1.1.0\n" +
		"subject https://github.com/o/r/commit/" + commitB + "\n" +
		commitLink(commitA) + "\n" + commitLink(commitA)
	base, hashes, err := releaseNotes(body, "o", "r")
	if err != nil || base != "v1.0.0" || len(hashes) != 1 || hashes[0] != commitA {
		t.Fatalf("releaseNotes() = %q, %v, %v", base, hashes, err)
	}
	if _, _, err := releaseNotes(body, "other", "r"); err == nil {
		t.Fatal("accepted links to another repository")
	}
}

func TestRuntimePath(t *testing.T) {
	for path, want := range map[string]bool{
		"internal/web/share.go":                            true,
		"internal/web/share_test.go":                       false,
		"cmd/flock/main.go":                                true,
		"windows/Flock.Tray/Settings/GeneralPane.xaml":     true,
		"windows/Flock.Tray.Tests/Flock.Tray.Tests.csproj": false,
		"windows/Flock.Tray.Tests/Directory.Build.props":   false,
		"windows/Flock.Kit.Tests/Flock.Kit.Tests.csproj":   false,
		"windows/Flock.Kit/SelfTest.cs":                    true,
		"macos/Sources/FlockBar/SelfTest.swift":            true,
		"macos/Tests/FlockTests/Example.swift":             false,
		"linux/src/panes/sites.rs":                         true,
		"windows/installer/Flock.iss":                      true,
		"lang/es/app.json":                                 true,
		"docs/config.md":                                   false,
		"":                                                 false,
	} {
		if got := runtimePath(path); got != want {
			t.Errorf("runtimePath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestRunAssignsGeneratedCommitDespiteSubjectURL(t *testing.T) {
	var writes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PATCH":
			writes = append(writes, r.URL.Path)
			fmt.Fprint(w, `{}`)
		case r.URL.Path == "/repos/o/r/milestones/2":
			fmt.Fprint(w, `{"number":2,"state":"open"}`)
		case r.URL.Path == "/repos/o/r/pulls/134":
			body := "https://github.com/o/r/compare/v1.0.0...v1.1.0\n" +
				"fix from https://github.com/o/r/commit/" + commitOld + "\n" +
				commitLink(commitA)
			fmt.Fprintf(w, `{"number":134,"state":"open","base":{"sha":"head","ref":"main"},"body":%q}`, body)
		case r.URL.Path == "/repos/o/r/compare/v1.0.0...head":
			fmt.Fprintf(w, `{"status":"ahead","commits":[{"sha":%q},{"sha":%q}]}`, commitA, mergeA)
		case strings.Contains(r.URL.Path, "/commits/"+commitA+"/pulls"):
			fmt.Fprintf(w, `[{"number":1,"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":%q,"base":{"ref":"main"}}]`, mergeA)
		case r.URL.Path == "/repos/o/r/pulls/1/files":
			fmt.Fprint(w, `[{"filename":"internal/web/share.go"}]`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base := server.URL + "/"
	client, err := github.NewClient(github.WithURLs(&base, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), client, "o", "r", 134, 2, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(writes, ","); got != "/repos/o/r/issues/134,/repos/o/r/issues/1" {
		t.Fatalf("writes = %s", got)
	}
}

func TestRunReturnsUnmatchedAfterWritesAndSkipsRepeatWrites(t *testing.T) {
	var writes []string
	synced := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "PATCH":
			writes = append(writes, r.URL.Path)
			fmt.Fprint(w, `{}`)
		case r.URL.Path == "/repos/o/r/milestones/2":
			fmt.Fprint(w, `{"number":2,"state":"open"}`)
		case r.URL.Path == "/repos/o/r/pulls/134":
			existing := "null"
			if synced {
				existing = `{"number":2}`
			}
			body := "https://github.com/o/r/compare/v1.0.0...v1.1.0\n" +
				commitLink(commitA) + "\n" + commitLink(commitB) + "\n" +
				commitLink(commitOld) + "\n" + commitLink(commitOld2)
			fmt.Fprintf(w, `{"number":134,"state":"open","milestone":%s,"base":{"sha":"head","ref":"main"},"body":%q}`, existing, body)
		case r.URL.Path == "/repos/o/r/compare/v1.0.0...head":
			fmt.Fprintf(w, `{"status":"ahead","commits":[{"sha":%q},{"sha":%q},{"sha":%q},{"sha":%q},{"sha":%q},{"sha":%q}]}`,
				commitA, mergeA, commitB, mergeB, commitOld, commitOld2)
		case strings.Contains(r.URL.Path, "/commits/"+commitA+"/pulls"):
			existing := "null"
			if synced {
				existing = `{"number":2}`
			}
			fmt.Fprintf(w, `[{"number":1,"milestone":%s,"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":%q,"base":{"ref":"main"}}]`, existing, mergeA)
		case strings.Contains(r.URL.Path, "/commits/"+commitB+"/pulls"):
			fmt.Fprintf(w, `[{"number":2,"merged_at":"2026-01-01T00:00:00Z","merge_commit_sha":%q,"base":{"ref":"main"}}]`, mergeB)
		case strings.Contains(r.URL.Path, "/commits/"+commitOld+"/pulls"), strings.Contains(r.URL.Path, "/commits/"+commitOld2+"/pulls"):
			fmt.Fprint(w, `[]`)
		case r.URL.Path == "/repos/o/r/pulls/1/files":
			fmt.Fprint(w, `[{"filename":"windows/Flock.Kit/SelfTest.cs"}]`)
		case r.URL.Path == "/repos/o/r/pulls/2/files":
			fmt.Fprint(w, `[{"filename":"windows/Flock.Kit.Tests/Flock.Kit.Tests.csproj"},{"filename":"windows/Flock.Tray.Tests/TrayServiceTests.cs"}]`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base := server.URL + "/"
	client, err := github.NewClient(github.WithURLs(&base, nil))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = Run(context.Background(), client, "o", "r", 134, 2, &output)
	if err == nil || !strings.Contains(err.Error(), commitOld) || !strings.Contains(err.Error(), commitOld2) {
		t.Fatalf("error = %v, want both unmatched commits", err)
	}
	if got := strings.Join(writes, ","); got != "/repos/o/r/issues/134,/repos/o/r/issues/1" {
		t.Fatalf("writes = %s", got)
	}
	if !strings.Contains(output.String(), "Skipped non-runtime PR #2\n") {
		t.Fatalf("output = %q", output.String())
	}
	writes = nil
	synced = true
	output.Reset()
	err = Run(context.Background(), client, "o", "r", 134, 2, &output)
	if err == nil || !strings.Contains(err.Error(), commitOld) || !strings.Contains(err.Error(), commitOld2) {
		t.Fatalf("rerun error = %v, want both unmatched commits", err)
	}
	if len(writes) != 0 {
		t.Fatalf("rerun repeated writes: %v", writes)
	}
}
