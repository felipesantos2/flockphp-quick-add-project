package pr

import (
	"context"
	"net/http"
	"testing"
)

func TestRunKeepsUnassignableClaimAndTransfersMilestone(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			labels := `[]`
			if containsLog(logs, "POST", "/repos/owner/repo/issues/1/labels") {
				labels = `[{"name":"claimed"}]`
			}
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR","labels":`+labels+`}`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/1/comments":
			if containsLog(logs, "POST", "/repos/owner/repo/issues/1/comments") {
				writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
			} else {
				writeTestJSON(t, w, `[]`)
			}
		case "GET /repos/owner/repo/issues/9/comments":
			if containsLog(logs, "POST", "/repos/owner/repo/issues/9/comments") {
				writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
			} else {
				writeTestJSON(t, w, `[]`)
			}
		case "GET /repos/owner/repo/issues/9":
			labels, milestone := `[{"name":"help wanted"}]`, `,"milestone":{"number":7}`
			if containsLog(logs, "DELETE", "/repos/owner/repo/issues/9/labels/help wanted") {
				labels = `[{"name":"claimed"}]`
			}
			if containsLog(logs, "PATCH", "/repos/owner/repo/issues/9") {
				milestone = ""
			}
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":`+labels+`,"assignees":[]`+milestone+`}`)
		case "GET /repos/owner/repo/issues/9/events":
			writeTestJSON(t, w, `[]`)
		case "GET /repos/owner/repo/issues/1":
			writeTestJSON(t, w, `{"number":1}`)
		case "GET /repos/owner/repo/labels/claimed":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/1/comments", "POST /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `{"id":10}`)
		case "POST /repos/owner/repo/issues/9/assignees":
			writeTestJSON(t, w, `{"number":9,"assignees":[]}`)
		case "POST /repos/owner/repo/issues/9/labels", "POST /repos/owner/repo/issues/1/labels":
			writeTestJSON(t, w, `[]`)
		case "DELETE /repos/owner/repo/issues/9/labels/help wanted", "PATCH /repos/owner/repo/issues/1",
			"PATCH /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	for _, action := range []string{"opened", "synchronize"} {
		if err := Run(context.Background(), client, "owner", "repo", testPullRequestEvent(action, 1, false)); err != nil {
			t.Fatalf("Run(%s) error = %v", action, err)
		}
	}
	for _, expected := range []requestLog{
		{"POST", "/repos/owner/repo/issues/9/labels"},
		{"DELETE", "/repos/owner/repo/issues/9/labels/help wanted"},
		{"POST", "/repos/owner/repo/issues/1/labels"},
		{"PATCH", "/repos/owner/repo/issues/1"},
		{"PATCH", "/repos/owner/repo/issues/9"},
	} {
		if !containsLog(logs, expected.method, expected.path) {
			t.Fatalf("missing %s %s: %v", expected.method, expected.path, logs)
		}
	}
	if containsLog(logs, "PATCH", "/repos/owner/repo/pulls/1") ||
		countLog(logs, "POST", "/repos/owner/repo/issues/9/comments") != 1 {
		t.Fatalf("retry closed the PR or duplicated the claim: %v", logs)
	}
}

func TestRunReleasesClaimAfterGateClosesStillLinkedPullRequest(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[{"login":"maintainer"}]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
		case "POST /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `{"id":12}`)
		case "PATCH /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed"}`)
		case "DELETE /repos/owner/repo/issues/comments/11", "DELETE /repos/owner/repo/issues/comments/10":
			writeTestJSON(t, w, `{}`)
		case "DELETE /repos/owner/repo/issues/9/labels/claimed":
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	if err := Run(context.Background(), client, "owner", "repo", testPullRequestEvent("synchronize", 1, false)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	closeIndex, releaseIndex := -1, -1
	for index, log := range logs {
		if log.method == "PATCH" && log.path == "/repos/owner/repo/pulls/1" {
			closeIndex = index
		}
		if log.method == "DELETE" && log.path == "/repos/owner/repo/issues/comments/10" {
			releaseIndex = index
		}
	}
	if closeIndex == -1 || releaseIndex == -1 || releaseIndex < closeIndex {
		t.Fatalf("claim record was not released after the gate close: %v", logs)
	}
}

func TestRunReleasesBotAssignmentAfterGateClosesLinkedClosedIssue(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"closed","labels":[{"name":"claimed"}],"assignees":[{"login":"author"}]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
		case "GET /repos/owner/repo/issues/9/events":
			writeTestJSON(t, w, `[{"event":"assigned","assignee":{"login":"author"},"assigner":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:00Z"}]`)
		case "POST /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `{"id":12}`)
		case "PATCH /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed"}`)
		case "DELETE /repos/owner/repo/issues/comments/11", "DELETE /repos/owner/repo/issues/comments/10",
			"DELETE /repos/owner/repo/issues/9/labels/claimed", "DELETE /repos/owner/repo/issues/9/assignees":
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	if err := Run(context.Background(), client, "owner", "repo", testPullRequestEvent("synchronize", 1, false)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !containsLog(logs, "DELETE", "/repos/owner/repo/issues/9/assignees") {
		t.Fatalf("Run() did not remove the bot assignment: %v", logs)
	}
}

func TestRunDoesNotRestoreHelpWantedAfterHumanUnassignment(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
		case "GET /repos/owner/repo/issues/9/events":
			writeTestJSON(t, w, `[{"event":"assigned","assignee":{"login":"author"},"assigner":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:00Z"},{"event":"unassigned","assignee":{"login":"author"},"assigner":{"login":"maintainer"},"created_at":"2026-01-01T00:01:00Z"}]`)
		case "GET /repos/owner/repo/issues/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed"}`)
		case "DELETE /repos/owner/repo/issues/comments/11", "DELETE /repos/owner/repo/issues/comments/10",
			"DELETE /repos/owner/repo/issues/9/labels/claimed":
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	if err := Run(context.Background(), client, "owner", "repo", testPullRequestEvent("closed", 1, false)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if containsLog(logs, "DELETE", "/repos/owner/repo/issues/9/assignees") ||
		containsLog(logs, "POST", "/repos/owner/repo/issues/9/labels") {
		t.Fatalf("Run() undid the maintainer unassignment: %v", logs)
	}
}

func TestRunKeepsAssignmentWhenAnotherClaimByTheAuthorRemains(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[{"login":"author"}]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"},{"id":12,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=2 author=author -->"}]`)
		case "GET /repos/owner/repo/issues/9/events":
			writeTestJSON(t, w, `[{"event":"assigned","assignee":{"login":"author"},"assigner":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:00Z"}]`)
		case "GET /repos/owner/repo/issues/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed"}`)
		case "DELETE /repos/owner/repo/issues/comments/11", "DELETE /repos/owner/repo/issues/comments/10":
			writeTestJSON(t, w, `{}`)
		case "DELETE /repos/owner/repo/issues/9/assignees":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/9/labels":
			writeTestJSON(t, w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	if err := Run(context.Background(), client, "owner", "repo", testPullRequestEvent("closed", 1, false)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if containsLog(logs, "DELETE", "/repos/owner/repo/issues/9/assignees") ||
		containsLog(logs, "POST", "/repos/owner/repo/issues/9/labels") {
		t.Fatalf("Run() removed the remaining claim's assignment: %v", logs)
	}
}

func TestRunMovesAStaleClaimToAnotherApprovedLinkedIssue(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}},{"number":10,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[{"login":"maintainer"}]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
		case "GET /repos/owner/repo/issues/10":
			writeTestJSON(t, w, `{"number":10,"state":"open","labels":[{"name":"help wanted"}],"assignees":[]}`)
		case "GET /repos/owner/repo/issues/10/comments":
			writeTestJSON(t, w, `[]`)
		case "GET /repos/owner/repo/issues/1":
			writeTestJSON(t, w, `{"number":1,"state":"open"}`)
		case "GET /repos/owner/repo/labels/claimed":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/1/comments", "POST /repos/owner/repo/issues/10/comments":
			writeTestJSON(t, w, `{"id":12}`)
		case "DELETE /repos/owner/repo/issues/comments/10", "DELETE /repos/owner/repo/issues/comments/11",
			"DELETE /repos/owner/repo/issues/9/labels/claimed", "DELETE /repos/owner/repo/issues/10/labels/help wanted":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/10/assignees":
			writeTestJSON(t, w, `{"number":10,"state":"open","labels":[{"name":"help wanted"}],"assignees":[{"login":"author"}]}`)
		case "POST /repos/owner/repo/issues/10/labels", "POST /repos/owner/repo/issues/1/labels":
			writeTestJSON(t, w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	if err := Run(context.Background(), client, "owner", "repo", testPullRequestEvent("synchronize", 1, false)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !containsLog(logs, "DELETE", "/repos/owner/repo/issues/comments/11") ||
		!containsLog(logs, "DELETE", "/repos/owner/repo/issues/comments/10") ||
		!containsLog(logs, "POST", "/repos/owner/repo/issues/10/comments") ||
		!containsLog(logs, "POST", "/repos/owner/repo/issues/10/assignees") {
		t.Fatalf("Run() did not move the claim to the approved issue: %v", logs)
	}
}
