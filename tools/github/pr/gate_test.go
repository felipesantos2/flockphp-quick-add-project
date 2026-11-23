package pr

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"flock/tools/github/contribution"
)

func TestGateEditedOpenPullRequestRetriesClosingKeywords(t *testing.T) {
	for _, keyword := range []string{"Fixes", "Closes"} {
		t.Run(keyword, func(t *testing.T) {
			t.Parallel()
			var logs []requestLog
			graphqlCalls := 0
			client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logs = append(logs, requestLog{r.Method, r.URL.Path})
				switch r.Method + " " + r.URL.Path {
				case "GET /repos/owner/repo/pulls/1":
					writeTestJSON(t, w, fmt.Sprintf(`{"number":1,"state":"open","body":"%s #9","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`, keyword))
				case "GET /repos/owner/repo/pulls/1/files":
					writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
				case "POST /graphql":
					graphqlCalls++
					if graphqlCalls == 1 {
						writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
						return
					}
					writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
				case "GET /repos/owner/repo/issues/9":
					writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"help wanted"}]}`)
				case "GET /repos/owner/repo/issues/9/comments":
					writeTestJSON(t, w, `[]`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer closeServer()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := Gate(ctx, client, "owner", "repo", testPullRequestEvent("edited", 1, true)); err != nil {
				t.Fatalf("Gate() error = %v", err)
			}
			if graphqlCalls != 2 {
				t.Fatalf("closing-reference requests = %d, want 2", graphqlCalls)
			}
			if containsLog(logs, "PATCH", "/repos/owner/repo/pulls/1") ||
				containsLog(logs, "POST", "/repos/owner/repo/issues/1/comments") {
				t.Fatalf("Gate() closed an approved pull request: %v", logs)
			}
		})
	}
}

func TestGateClosedEditWithoutApprovalDoesNotLoop(t *testing.T) {
	var logs []requestLog
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"user":{"login":"github-actions[bot]"},"body":"closed <!-- pr-gate:closed -->","created_at":"2026-01-01T00:00:00Z"}]`)
		case "GET /repos/owner/repo/issues/1/events":
			writeTestJSON(t, w, `[{"event":"closed","actor":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:01Z"}]`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		default:
			http.NotFound(w, r)
		}
	})
	client, closeServer := testClient(t, handler)
	defer closeServer()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := Gate(ctx, client, "owner", "repo", testPullRequestEvent("edited", 1, true)); err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	if countLog(logs, "POST", "/graphql") != 3 {
		t.Fatalf("closing-reference requests = %d, want 3; logs=%v", countLog(logs, "POST", "/graphql"), logs)
	}
	if containsLog(logs, "PATCH", "/repos/owner/repo/pulls/1") {
		t.Fatal("Gate() reopened or closed the pull request")
	}
}

func TestGateEditedOpenPullRequestClosesAfterApprovalRemoved(t *testing.T) {
	var logs []requestLog
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[]`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[],"assignees":[]}`)
		case "GET /repos/owner/repo/issues/9/events":
			writeTestJSON(t, w, `[]`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "DELETE /repos/owner/repo/issues/comments/10":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `{"id":11}`)
		case "PATCH /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed"}`)
		default:
			http.NotFound(w, r)
		}
	})
	client, closeServer := testClient(t, handler)
	defer closeServer()
	event := testPullRequestEvent("edited", 1, true)
	if err := Release(context.Background(), client, "owner", "repo", event); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := Gate(context.Background(), client, "owner", "repo", event); err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	commentIndex, stateIndex := -1, -1
	for index, log := range logs {
		if log.method == "POST" && log.path == "/repos/owner/repo/issues/1/comments" && commentIndex == -1 {
			commentIndex = index
		}
		if log.method == "PATCH" && log.path == "/repos/owner/repo/pulls/1" {
			stateIndex = index
		}
	}
	if commentIndex == -1 || stateIndex == -1 || commentIndex >= stateIndex {
		t.Fatalf("close comment and state requests are out of order: %v", logs)
	}
}

func TestGateClosedEditRetriesUntilClosingReferenceAppears(t *testing.T) {
	var logs []requestLog
	graphqlCalls := 0
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"user":{"login":"github-actions[bot]"},"body":"closed <!-- pr-gate:closed -->","created_at":"2026-01-01T00:00:00Z"}]`)
		case "GET /repos/owner/repo/issues/1/events":
			writeTestJSON(t, w, `[{"event":"closed","actor":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:01Z"}]`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "POST /graphql":
			graphqlCalls++
			if graphqlCalls < 3 {
				writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
				return
			}
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"help wanted"}]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[]`)
		case "PATCH /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open"}`)
		case "POST /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `{"id":11}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := Gate(ctx, client, "owner", "repo", testPullRequestEvent("edited", 1, true)); err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	if countLog(logs, "POST", "/graphql") != 3 {
		t.Fatalf("closing-reference requests = %d, want 3; logs=%v", countLog(logs, "POST", "/graphql"), logs)
	}
	if countLog(logs, "PATCH", "/repos/owner/repo/pulls/1") != 1 ||
		countLog(logs, "POST", "/repos/owner/repo/issues/1/comments") != 1 {
		t.Fatalf("reopen requests = %v, want one state update and one comment", logs)
	}
}

func TestListClosingReferencesRejectsStalledCursor(t *testing.T) {
	calls := 0
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"same"}}}}}}`)
	}))
	defer closeServer()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := listClosingReferences(ctx, client, "owner", "repo", 1)
	if err == nil || !strings.Contains(err.Error(), "page cursor did not advance") {
		t.Fatalf("listClosingReferences() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("graphql calls = %d, want 2", calls)
	}
}

func TestGateRejectsMalformedEvent(t *testing.T) {
	if err := Gate(context.Background(), nil, "owner", "repo", []byte("{")); err == nil ||
		!strings.Contains(err.Error(), "parse pull request event") {
		t.Fatalf("Gate() error = %v", err)
	}
}

func TestClosePullRequestReturnsCommentErrorWithoutClosing(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		if r.Method == "POST" && r.URL.Path == "/repos/owner/repo/issues/1/comments" {
			http.Error(w, `{"message":"comment failed"}`, http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer closeServer()

	pullRequest := &contribution.PullRequest{
		Number:  1,
		State:   "open",
		Author:  contribution.User{Login: "author"},
		BaseRef: "main",
	}
	err := closePullRequest(context.Background(), client, "owner", "repo", pullRequest, nil)
	if err == nil || !strings.Contains(err.Error(), "create comment on pull request #1") {
		t.Fatalf("closePullRequest() error = %v, want comment error", err)
	}
	if containsLog(logs, "PATCH", "/repos/owner/repo/pulls/1") {
		t.Fatal("closePullRequest() changed state after comment failure")
	}
}

func TestClosePullRequestRetriesStateFailure(t *testing.T) {
	var logs []requestLog
	stateCalls := 0
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "POST /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `{"id":10}`)
		case "PATCH /repos/owner/repo/pulls/1":
			stateCalls++
			if stateCalls == 1 {
				http.Error(w, `{"message":"state failed"}`, http.StatusInternalServerError)
				return
			}
			writeTestJSON(t, w, `{"number":1,"state":"closed"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	pullRequest := &contribution.PullRequest{
		Number:  1,
		State:   "open",
		Author:  contribution.User{Login: "author"},
		BaseRef: "main",
	}
	if err := closePullRequest(context.Background(), client, "owner", "repo", pullRequest, nil); err == nil ||
		!strings.Contains(err.Error(), "set pull request #1 state to closed") {
		t.Fatalf("first closePullRequest() error = %v, want state error", err)
	}
	if err := closePullRequest(context.Background(), client, "owner", "repo", pullRequest, nil); err != nil {
		t.Fatalf("retry closePullRequest() error = %v", err)
	}
	if countLog(logs, "POST", "/repos/owner/repo/issues/1/comments") != 2 ||
		countLog(logs, "PATCH", "/repos/owner/repo/pulls/1") != 2 {
		t.Fatalf("close retry requests = %v, want two comments and two state updates", logs)
	}
}

func TestClosePullRequestSkipsAlreadyClosedPullRequest(t *testing.T) {
	var requests int
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, `{"message":"unexpected request"}`, http.StatusInternalServerError)
	}))
	defer closeServer()

	err := closePullRequest(context.Background(), client, "owner", "repo", &contribution.PullRequest{
		Number: 1,
		State:  "closed",
	}, nil)
	if err != nil {
		t.Fatalf("closePullRequest() error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
}

func TestGateWaitsAtMostTwiceBeforeClosing(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "POST /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `{"id":10}`)
		case "PATCH /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := Gate(ctx, client, "owner", "repo", testPullRequestEvent("opened", 1, false)); err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	if countLog(logs, "POST", "/graphql") != 3 {
		t.Fatalf("closing-reference requests = %d, want 3; logs=%v", countLog(logs, "POST", "/graphql"), logs)
	}
	if countLog(logs, "POST", "/repos/owner/repo/issues/1/comments") != 1 ||
		countLog(logs, "PATCH", "/repos/owner/repo/pulls/1") != 1 {
		t.Fatalf("close requests = %v, want one comment followed by one state update", logs)
	}
}

func TestGateReopensDocsOnlyPullRequest(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"user":{"login":"github-actions[bot]"},"body":"closed <!-- pr-gate:closed -->","created_at":"2026-01-01T00:00:00Z"}]`)
		case "GET /repos/owner/repo/issues/1/events":
			writeTestJSON(t, w, `[{"event":"closed","actor":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:01Z"}]`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"docs/guide.md"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "PATCH /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open"}`)
		case "POST /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `{"id":11}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	if err := Gate(context.Background(), client, "owner", "repo", testPullRequestEvent("edited", 1, true)); err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	if countLog(logs, "PATCH", "/repos/owner/repo/pulls/1") != 1 ||
		countLog(logs, "POST", "/repos/owner/repo/issues/1/comments") != 1 {
		t.Fatalf("docs-only reopen requests = %v", logs)
	}
}

func TestGateAllowsTranslationsWithoutIssue(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"lang/fr/app.json"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	if err := Gate(context.Background(), client, "owner", "repo", testPullRequestEvent("opened", 1, false)); err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	if countLog(logs, "POST", "/graphql") != 1 || containsLog(logs, "PATCH", "/repos/owner/repo/pulls/1") ||
		containsLog(logs, "POST", "/repos/owner/repo/issues/1/comments") {
		t.Fatalf("translation-only requests = %v, want one issue lookup and no changes", logs)
	}
}

func TestGateKeepsAssigneePullRequestOpenDespiteForeignClaim(t *testing.T) {
	var logs []requestLog
	var commentBody string
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","assignees":[{"login":"author"}]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=2 author=other -->"}]`)
		case "POST /repos/owner/repo/issues/1/comments":
			body, _ := io.ReadAll(r.Body)
			commentBody = string(body)
			writeTestJSON(t, w, `{"id":12}`)
		case "PATCH /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	if err := Gate(context.Background(), client, "owner", "repo", testPullRequestEvent("synchronize", 1, false)); err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	if containsLog(logs, "PATCH", "/repos/owner/repo/pulls/1") || commentBody != "" {
		t.Fatalf("Gate() closed an assignee pull request: %v body=%s", logs, commentBody)
	}
}
