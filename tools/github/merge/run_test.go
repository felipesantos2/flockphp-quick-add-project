package merge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v93/github"
)

const mergeTestRef = "refs/heads/gh-readonly-queue/main/pr-123-ce2a51d86b2e1be42ffd4c0d5a020fd0d7270588"

func TestRunAllowsAdminsAndMaintainers(t *testing.T) {
	for _, role := range []string{"admin", "maintain"} {
		t.Run(role, func(t *testing.T) {
			var requests []string
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch r.Method + " " + r.URL.Path {
				case "POST /graphql":
					_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequest":{"mergeQueueEntry":{"enqueuer":{"login":"maintainer"}}}}}}`)
				case "GET /repos/flock-php/flock/collaborators/maintainer/permission":
					_, _ = io.WriteString(w, `{"permission":"write","role_name":"`+role+`"}`)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			})

			if err := Run(context.Background(), client, "flock-php", "flock", mergeEvent(), "7"); err != nil {
				t.Fatal(err)
			}
			if len(requests) != 2 {
				t.Fatalf("requests = %v, want only enqueuer and role reads", requests)
			}
		})
	}
}

func TestRunRejectsMalformedReferencesBeforeSideEffects(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	})

	err := Run(context.Background(), client, "flock-php", "flock", []byte(`{"merge_group":{"head_ref":"refs/heads/main"}}`), "7")
	if err == nil || !strings.Contains(err.Error(), "not a merge queue ref") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunRejectsMalformedMergeWebhookJSON(t *testing.T) {
	client, err := github.NewClient()
	if err != nil {
		t.Fatal(err)
	}

	err = Run(context.Background(), client, "flock-php", "flock", []byte("{"), "7")
	if err == nil || !strings.Contains(err.Error(), "parse merge group event") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunCommentsBeforeCancellingAndReturnsNotAllowed(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "POST /graphql":
			_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequest":{"mergeQueueEntry":{"enqueuer":{"login":"contributor"}}}}}}`)
		case "GET /repos/flock-php/flock/collaborators/contributor/permission":
			_, _ = io.WriteString(w, `{"permission":"write","role_name":"write"}`)
		case "POST /repos/flock-php/flock/issues/123/comments":
			_, _ = io.WriteString(w, `{"id":101}`)
		case "GET /repos/flock-php/flock/actions/runs":
			_, _ = io.WriteString(w, `{"total_count":2,"workflow_runs":[{"id":7,"status":"in_progress"},{"id":8,"status":"queued"}]}`)
		case "POST /repos/flock-php/flock/actions/runs/8/cancel":
			_, _ = io.WriteString(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	err := Run(context.Background(), client, "flock-php", "flock", mergeEvent(), "7")
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("Run() error = %v, want ErrNotAllowed", err)
	}
	assertRequestOrder(t, requests,
		"GET /repos/flock-php/flock/actions/runs",
		"POST /repos/flock-php/flock/issues/123/comments",
		"POST /repos/flock-php/flock/actions/runs/8/cancel",
	)
}

func TestRunTreatsAcceptedCancellationAsSuccessBeforeNotAllowed(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "POST /graphql":
			_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequest":{"mergeQueueEntry":{"enqueuer":{"login":"contributor"}}}}}}`)
		case "GET /repos/flock-php/flock/collaborators/contributor/permission":
			_, _ = io.WriteString(w, `{"permission":"write","role_name":"write"}`)
		case "POST /repos/flock-php/flock/issues/123/comments":
			_, _ = io.WriteString(w, `{"id":101}`)
		case "GET /repos/flock-php/flock/actions/runs":
			_, _ = io.WriteString(w, `{"total_count":1,"workflow_runs":[{"id":8,"status":"queued"}]}`)
		case "POST /repos/flock-php/flock/actions/runs/8/cancel":
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	err := Run(context.Background(), client, "flock-php", "flock", mergeEvent(), "7")
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("Run() error = %v, want ErrNotAllowed after accepted cancellation", err)
	}
}

func TestRunReturnsNonAcceptedCancellationError(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /graphql":
			_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequest":{"mergeQueueEntry":{"enqueuer":{"login":"contributor"}}}}}}`)
		case "GET /repos/flock-php/flock/collaborators/contributor/permission":
			_, _ = io.WriteString(w, `{"permission":"write","role_name":"write"}`)
		case "POST /repos/flock-php/flock/issues/123/comments":
			_, _ = io.WriteString(w, `{"id":101}`)
		case "GET /repos/flock-php/flock/actions/runs":
			_, _ = io.WriteString(w, `{"total_count":1,"workflow_runs":[{"id":8,"status":"queued"}]}`)
		case "POST /repos/flock-php/flock/actions/runs/8/cancel":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"cancel failed"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	err := Run(context.Background(), client, "flock-php", "flock", mergeEvent(), "7")
	if err == nil || !strings.Contains(err.Error(), "cancel run 8") || !strings.Contains(err.Error(), "cancel failed") {
		t.Fatalf("Run() error = %v, want contextual cancellation error", err)
	}
	if errors.Is(err, ErrNotAllowed) {
		t.Fatalf("Run() error = %v, want cancellation error before ErrNotAllowed", err)
	}
}

func TestRunCancelsInProgressRunsOnLaterPages(t *testing.T) {
	var requests []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch r.Method + " " + r.URL.Path {
		case "POST /graphql":
			_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequest":{"mergeQueueEntry":{"enqueuer":{"login":"contributor"}}}}}}`)
		case "GET /repos/flock-php/flock/collaborators/contributor/permission":
			_, _ = io.WriteString(w, `{"permission":"write","role_name":"write"}`)
		case "POST /repos/flock-php/flock/issues/123/comments":
			_, _ = io.WriteString(w, `{"id":101}`)
		case "GET /repos/flock-php/flock/actions/runs":
			if r.URL.Query().Get("page") == "2" {
				_, _ = io.WriteString(w, `{"total_count":3,"workflow_runs":[{"id":7,"status":"in_progress"},{"id":9,"status":"queued"},{"id":10,"status":"completed"}]}`)
				return
			}
			w.Header().Set("Link", `<https://example.test/repos/flock-php/flock/actions/runs?page=2>; rel="next"`)
			_, _ = io.WriteString(w, `{"total_count":3,"workflow_runs":[{"id":8,"status":"queued"}]}`)
		case "POST /repos/flock-php/flock/actions/runs/8/cancel", "POST /repos/flock-php/flock/actions/runs/9/cancel":
			_, _ = io.WriteString(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	err := Run(context.Background(), client, "flock-php", "flock", mergeEvent(), "7")
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("Run() error = %v, want ErrNotAllowed", err)
	}
	if !containsRequest(requests, "POST /repos/flock-php/flock/actions/runs/8/cancel") ||
		!containsRequest(requests, "POST /repos/flock-php/flock/actions/runs/9/cancel") ||
		containsRequest(requests, "POST /repos/flock-php/flock/actions/runs/7/cancel") ||
		containsRequest(requests, "POST /repos/flock-php/flock/actions/runs/10/cancel") {
		t.Fatalf("requests = %v, want cancels for runs 8 and 9 only", requests)
	}
}

func containsRequest(requests []string, want string) bool {
	for _, request := range requests {
		if strings.HasPrefix(request, want) {
			return true
		}
	}
	return false
}

func TestRunPropagatesMergeOperationFailures(t *testing.T) {
	tests := []struct {
		name        string
		failure     string
		want        string
		lastRequest string
	}{
		{name: "graphql", failure: "graphql", want: "read merge queue enqueuer", lastRequest: "POST /graphql"},
		{name: "permission", failure: "permission", want: "read repository role", lastRequest: "GET /repos/flock-php/flock/collaborators/contributor/permission"},
		{name: "comment", failure: "comment", want: "create merge queue comment", lastRequest: "POST /repos/flock-php/flock/issues/123/comments"},
		{name: "workflow listing", failure: "listing", want: "list merge-group workflow runs", lastRequest: "GET /repos/flock-php/flock/actions/runs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests []string
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				path := r.Method + " " + r.URL.Path
				requests = append(requests, path)
				switch path {
				case "POST /graphql":
					if test.failure == "graphql" {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = io.WriteString(w, `{"message":"graphql failed"}`)
						return
					}
					_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequest":{"mergeQueueEntry":{"enqueuer":{"login":"contributor"}}}}}}`)
				case "GET /repos/flock-php/flock/collaborators/contributor/permission":
					if test.failure == "permission" {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = io.WriteString(w, `{"message":"permission failed"}`)
						return
					}
					_, _ = io.WriteString(w, `{"permission":"write","role_name":"write"}`)
				case "POST /repos/flock-php/flock/issues/123/comments":
					if test.failure == "comment" {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = io.WriteString(w, `{"message":"comment failed"}`)
						return
					}
					_, _ = io.WriteString(w, `{"id":101}`)
				case "GET /repos/flock-php/flock/actions/runs":
					if test.failure == "listing" {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = io.WriteString(w, `{"message":"listing failed"}`)
						return
					}
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			})

			err := Run(context.Background(), client, "flock-php", "flock", mergeEvent(), "7")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want %q", err, test.want)
			}
			if len(requests) == 0 || requests[len(requests)-1] != test.lastRequest {
				t.Fatalf("requests = %v, want last request %q", requests, test.lastRequest)
			}
		})
	}
}

func mergeEvent() []byte {
	return []byte(`{"merge_group":{"head_ref":"` + mergeTestRef + `","head_sha":"abc"}}`)
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
