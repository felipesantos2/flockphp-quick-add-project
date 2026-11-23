package issue

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

func TestTriageAppliesPlatformLabelsBeforeNeedsTriage(t *testing.T) {
	var requests []string
	var labels []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/issues/73":
			_, _ = io.WriteString(w, `{"number":73,"body":"### Platform\r\n\r\nmacOS, Linux","user":{"login":"contributor","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "POST /repos/flock-php/flock/issues/73/labels":
			var request []string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				http.Error(w, "invalid label request", http.StatusBadRequest)
				return
			}
			labels = append(labels, request...)
			_, _ = io.WriteString(w, `[]`)
		case "GET /repos/flock-php/flock/collaborators/contributor/permission":
			_, _ = io.WriteString(w, `{"permission":"read"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	event := []byte(`{"action":"opened","issue":{"number":73,"body":"### Platform\r\n\r\nmacOS, Linux","user":{"login":"contributor","type":"User"},"author_association":"CONTRIBUTOR"}}`)
	if err := Triage(context.Background(), client, "flock-php", "flock", event); err != nil {
		t.Fatal(err)
	}

	wantLabels := []string{"os: mac", "os: linux", "needs triage"}
	if len(labels) != len(wantLabels) {
		t.Fatalf("labels = %v, want %v", labels, wantLabels)
	}
	for index, want := range wantLabels {
		if labels[index] != want {
			t.Fatalf("labels = %v, want %v", labels, wantLabels)
		}
	}
	assertRequestOrder(t, requests,
		"GET /repos/flock-php/flock/issues/73",
		"GET /repos/flock-php/flock/collaborators/contributor/permission",
		"POST /repos/flock-php/flock/issues/73/labels",
	)
	assertRequestCount(t, requests, "POST /repos/flock-php/flock/issues/73/labels", 3)
}

func TestTriageBypassesNeedsTriageForPermissionAndTrustedAuthors(t *testing.T) {
	tests := []struct {
		name        string
		userType    string
		association string
		permission  string
	}{
		{name: "write permission", permission: "write"},
		{name: "bot", userType: "Bot", permission: "read"},
		{name: "member", association: "MEMBER", permission: "read"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var labels []string
			var permissionCalls int
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /repos/flock-php/flock/issues/73":
					_, _ = io.WriteString(w, `{"number":73,"user":{"login":"contributor","type":"`+test.userType+`"},"author_association":"`+test.association+`"}`)
				case "POST /repos/flock-php/flock/issues/73/labels":
					var request []string
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						http.Error(w, "invalid label request", http.StatusBadRequest)
						return
					}
					labels = append(labels, request...)
					_, _ = io.WriteString(w, `[]`)
				case "GET /repos/flock-php/flock/collaborators/contributor/permission":
					permissionCalls++
					_, _ = io.WriteString(w, `{"permission":"`+test.permission+`"}`)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			})

			event := []byte(`{"action":"opened","issue":{"number":73,"user":{"login":"contributor","type":"` + test.userType + `"},"author_association":"` + test.association + `"}}`)
			if err := Triage(context.Background(), client, "flock-php", "flock", event); err != nil {
				t.Fatal(err)
			}
			if permissionCalls != 1 {
				t.Fatalf("permission calls = %d, want one", permissionCalls)
			}
			if len(labels) != 0 {
				t.Fatalf("labels = %v, want no labels", labels)
			}
		})
	}
}

func TestAssignmentKeepsClaimedWhenMarkerAuthorIsStillAssigned(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/issues/73":
			_, _ = io.WriteString(w, `{"number":73,"assignees":[{"login":"contributor"},{"login":"maintainer"}],"labels":[{"name":"help wanted"},{"name":"claimed"}]}`)
		case "GET /repos/flock-php/flock/issues/73/comments":
			_, _ = io.WriteString(w, `[{"id":87,"body":"<!-- pr-gate:claim pr=42 author=contributor -->","user":{"login":"github-actions[bot]"}}]`)
		case "DELETE /repos/flock-php/flock/issues/73/labels/help wanted", "DELETE /repos/flock-php/flock/issues/73/labels/claimed":
			_, _ = io.WriteString(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	event := []byte(`{"action":"assigned","issue":{"number":73}}`)
	if err := Assignment(context.Background(), client, "flock-php", "flock", event); err != nil {
		t.Fatal(err)
	}
	assertRequestOrder(t, requests,
		"GET /repos/flock-php/flock/issues/73",
		"GET /repos/flock-php/flock/issues/73/comments",
		"DELETE /repos/flock-php/flock/issues/73/labels/help wanted",
	)
	assertRequestCount(t, requests, "DELETE /repos/flock-php/flock/issues/73/labels/claimed", 0)
}

func TestAssignmentRemovesClaimedWhenMarkerAuthorsAreNotAssigned(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/issues/73":
			_, _ = io.WriteString(w, `{"number":73,"assignees":[{"login":"maintainer"}],"labels":[{"name":"claimed"}]}`)
		case "GET /repos/flock-php/flock/issues/73/comments":
			_, _ = io.WriteString(w, `[{"id":87,"body":"<!-- pr-gate:claim pr=42 author=contributor -->","user":{"login":"github-actions[bot]"}},{"id":88,"body":"<!-- pr-gate:claim pr=43 author=other-contributor -->","user":{"login":"github-actions[bot]"}}]`)
		case "DELETE /repos/flock-php/flock/issues/73/labels/claimed":
			_, _ = io.WriteString(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	event := []byte(`{"action":"assigned","issue":{"number":73}}`)
	if err := Assignment(context.Background(), client, "flock-php", "flock", event); err != nil {
		t.Fatal(err)
	}
	assertRequestOrder(t, requests,
		"GET /repos/flock-php/flock/issues/73",
		"GET /repos/flock-php/flock/issues/73/comments",
		"DELETE /repos/flock-php/flock/issues/73/labels/claimed",
	)
}

func TestAssignmentKeepsClaimedWhenOneClaimAuthorIsStillAssigned(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/issues/73":
			_, _ = io.WriteString(w, `{"number":73,"assignees":[{"login":"contributor"}],"labels":[{"name":"claimed"}]}`)
		case "GET /repos/flock-php/flock/issues/73/comments":
			_, _ = io.WriteString(w, `[{"id":87,"body":"<!-- pr-gate:claim pr=42 author=contributor -->","user":{"login":"github-actions[bot]"}},{"id":88,"body":"<!-- pr-gate:claim pr=43 author=other-contributor -->","user":{"login":"github-actions[bot]"}}]`)
		case "DELETE /repos/flock-php/flock/issues/73/labels/claimed":
			_, _ = io.WriteString(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	event := []byte(`{"action":"assigned","issue":{"number":73}}`)
	if err := Assignment(context.Background(), client, "flock-php", "flock", event); err != nil {
		t.Fatal(err)
	}
	assertRequestCount(t, requests, "DELETE /repos/flock-php/flock/issues/73/labels/claimed", 0)
}

func TestAssignmentSkipsCleanupForUnassignedCurrentIssue(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == "GET" && r.URL.Path == "/repos/flock-php/flock/issues/73" {
			_, _ = io.WriteString(w, `{"number":73,"assignees":[],"labels":[{"name":"help wanted"},{"name":"claimed"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	event := []byte(`{"action":"assigned","issue":{"number":73}}`)
	if err := Assignment(context.Background(), client, "flock-php", "flock", event); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0] != "GET /repos/flock-php/flock/issues/73" {
		t.Fatalf("requests = %v, want current issue read only", requests)
	}
}

func TestAssignmentSkipsClaimsWithoutClaimedLabel(t *testing.T) {
	for _, test := range []struct {
		name   string
		labels string
		remove bool
	}{
		{name: "help wanted", labels: `[{"name":"help wanted"}]`, remove: true},
		{name: "no labels", labels: `[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests []string
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch r.Method + " " + r.URL.Path {
				case "GET /repos/flock-php/flock/issues/73":
					_, _ = io.WriteString(w, `{"number":73,"assignees":[{"login":"contributor"}],"labels":`+test.labels+`}`)
				case "DELETE /repos/flock-php/flock/issues/73/labels/help wanted":
					_, _ = io.WriteString(w, `{}`)
				default:
					http.Error(w, "unexpected request", http.StatusInternalServerError)
				}
			})
			if err := Assignment(context.Background(), client, "flock-php", "flock", []byte(`{"action":"assigned","issue":{"number":73}}`)); err != nil {
				t.Fatal(err)
			}
			assertRequestCount(t, requests, "GET /repos/flock-php/flock/issues/73/comments", 0)
			want := 0
			if test.remove {
				want = 1
			}
			assertRequestCount(t, requests, "DELETE /repos/flock-php/flock/issues/73/labels/help wanted", want)
		})
	}
}

func TestAssignmentTreatsMissingCurrentIssueAsNoOp(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
	})

	event := []byte(`{"action":"assigned","issue":{"number":73}}`)
	if err := Assignment(context.Background(), client, "flock-php", "flock", event); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0] != "GET /repos/flock-php/flock/issues/73" {
		t.Fatalf("requests = %v, want current issue read only", requests)
	}
}

func TestTriageSkipsMissingIssueEvent(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
		return
	})
	if err := Triage(context.Background(), client, "flock-php", "flock", []byte(`{"action":"opened"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestTriageSkipsDeletedIssue(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
	})

	event := []byte(`{"action":"opened","issue":{"number":73,"body":"### Platform\n\nLinux","user":{"login":"contributor","type":"User"}}}`)
	if err := Triage(context.Background(), client, "flock-php", "flock", event); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0] != "GET /repos/flock-php/flock/issues/73" {
		t.Fatalf("requests = %v, want current issue read only", requests)
	}
}

func TestTriageRejectsMalformedIssueJSON(t *testing.T) {
	client, err := github.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	err = Triage(context.Background(), client, "flock-php", "flock", []byte("{"))
	if err == nil || !strings.Contains(err.Error(), "parse issue event") {
		t.Fatalf("Triage() error = %v", err)
	}
}

func TestTriageStopsAfterPlatformLabelFailure(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == "GET" && r.URL.Path == "/repos/flock-php/flock/issues/73" {
			_, _ = io.WriteString(w, `{"number":73,"body":"### Platform\n\nLinux","user":{"login":"contributor","type":"User"}}`)
			return
		}
		if r.Method == "GET" {
			_, _ = io.WriteString(w, `{"permission":"read"}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"failure"}`)
	})

	event := []byte(`{"action":"opened","issue":{"number":73,"body":"### Platform\n\nLinux","user":{"login":"contributor","type":"User"}}}`)
	if err := Triage(context.Background(), client, "flock-php", "flock", event); err == nil {
		t.Fatal("Triage returned nil after platform label failure")
	}
	if len(requests) != 3 || requests[0] != "GET /repos/flock-php/flock/issues/73" ||
		requests[1] != "GET /repos/flock-php/flock/collaborators/contributor/permission" ||
		requests[2] != "POST /repos/flock-php/flock/issues/73/labels" {
		t.Fatalf("requests = %v, want issue and permission reads then one label write", requests)
	}
}

func TestAssignmentStopsAfterClaimReadFailure(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/issues/73":
			_, _ = io.WriteString(w, `{"number":73,"assignees":[{"login":"contributor"}],"labels":[{"name":"help wanted"},{"name":"claimed"}]}`)
		case "GET /repos/flock-php/flock/issues/73/comments":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"failure"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	event := []byte(`{"action":"assigned","issue":{"number":73}}`)
	if err := Assignment(context.Background(), client, "flock-php", "flock", event); err == nil {
		t.Fatal("Assignment returned nil after claim read failure")
	}
	if len(requests) != 2 || requests[1] != "GET /repos/flock-php/flock/issues/73/comments" {
		t.Fatalf("requests = %v, want issue and claim reads only", requests)
	}
}

func TestAssignmentStopsAfterHelpWantedRemovalFailure(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/issues/73":
			_, _ = io.WriteString(w, `{"number":73,"assignees":[{"login":"contributor"},{"login":"maintainer"}],"labels":[{"name":"help wanted"},{"name":"claimed"}]}`)
		case "GET /repos/flock-php/flock/issues/73/comments":
			_, _ = io.WriteString(w, `[{"id":87,"body":"<!-- pr-gate:claim pr=42 author=contributor -->","user":{"login":"github-actions[bot]"}}]`)
		case "DELETE /repos/flock-php/flock/issues/73/labels/help wanted":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"failure"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	event := []byte(`{"action":"assigned","issue":{"number":73}}`)
	if err := Assignment(context.Background(), client, "flock-php", "flock", event); err == nil {
		t.Fatal("Assignment returned nil after help wanted removal failure")
	}
	if len(requests) != 3 || requests[2] != "DELETE /repos/flock-php/flock/issues/73/labels/help wanted" {
		t.Fatalf("requests = %v, want no superseded claim removal", requests)
	}
}

func TestIssuePlatformsPreservesPlatformOrder(t *testing.T) {
	got := issuePlatforms("### Platform\r\n\r\nmacOS, Windows, Linux")
	want := []string{"os: mac", "os: windows", "os: linux"}
	if len(got) != len(want) {
		t.Fatalf("issuePlatforms() = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("issuePlatforms() = %v, want %v", got, want)
		}
	}
}

func TestTriageDecisionForTrustedAuthorsAndPermissions(t *testing.T) {
	tests := []struct {
		name        string
		userType    string
		association string
		permission  string
		want        triageDecision
	}{
		{name: "bot", userType: "Bot", want: triageBypass},
		{name: "owner", association: "OWNER", want: triageBypass},
		{name: "member", association: "MEMBER", want: triageBypass},
		{name: "collaborator", association: "COLLABORATOR", want: triageBypass},
		{name: "admin permission", permission: "admin", want: triageBypass},
		{name: "write permission", permission: "write", want: triageBypass},
		{name: "untrusted contributor", userType: "User", association: "CONTRIBUTOR", permission: "read", want: triageNeedsTriage},
		{name: "unknown permission", userType: "User", association: "CONTRIBUTOR", permission: "maintain", want: triageNeedsTriage},
		{name: "unknown association", userType: "User", association: "UNKNOWN", permission: "read", want: triageNeedsTriage},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := contribution.Issue{
				User:              contribution.User{Type: test.userType},
				AuthorAssociation: test.association,
			}
			if got := triageDecisionFor(item, test.permission); got != test.want {
				t.Fatalf("triageDecisionFor() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestTriageDecisionUnknownValueFailsClosed(t *testing.T) {
	client, err := github.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	err = applyTriageDecision(context.Background(), client, "flock-php", "flock", contribution.Issue{Number: 73}, triageDecision(99))
	if err == nil || !strings.Contains(err.Error(), "unknown triage decision") {
		t.Fatalf("applyDecision() error = %v, want unknown decision error", err)
	}
}

func testClient(t *testing.T, handler http.HandlerFunc) *github.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	baseURL.Path += "/"
	base := baseURL.String()
	client, err := github.NewClient(github.WithAuthToken("token"), github.WithURLs(&base, nil))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func assertRequestOrder(t *testing.T, requests []string, want ...string) {
	t.Helper()
	last := -1
	for _, expected := range want {
		found := -1
		for index := last + 1; index < len(requests); index++ {
			if requests[index] == expected {
				found = index
				break
			}
		}
		if found == -1 {
			t.Fatalf("requests = %v, missing %q after index %d", requests, expected, last)
		}
		last = found
	}
}

func assertRequestCount(t *testing.T, requests []string, want string, count int) {
	t.Helper()
	got := 0
	for _, request := range requests {
		if request == want {
			got++
		}
	}
	if got != count {
		t.Fatalf("request %q count = %d, want %d; requests = %v", want, got, count, requests)
	}
}
