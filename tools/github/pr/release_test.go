package pr

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestReleaseRestoresHelpWantedForUnassignedClaim(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
		case "GET /repos/owner/repo/issues/9/events":
			writeTestJSON(t, w, `[]`)
		case "DELETE /repos/owner/repo/issues/comments/11", "DELETE /repos/owner/repo/issues/comments/10",
			"DELETE /repos/owner/repo/issues/9/labels/claimed":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/9/labels":
			writeTestJSON(t, w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	if err := Release(context.Background(), client, "owner", "repo", testPullRequestEvent("closed", 1, false)); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if !containsLog(logs, "POST", "/repos/owner/repo/issues/9/labels") ||
		!containsLog(logs, "DELETE", "/repos/owner/repo/issues/9/labels/claimed") ||
		containsLog(logs, "DELETE", "/repos/owner/repo/issues/9/assignees") {
		t.Fatalf("Release() did not restore the unassigned issue: %v", logs)
	}
}

func TestReleaseDeletesEveryMarkerAndKeepsHumanAssignee(t *testing.T) {
	var logs []requestLog
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"},{"id":12,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=other -->"}]`)
		case "DELETE /repos/owner/repo/issues/comments/11", "DELETE /repos/owner/repo/issues/comments/12", "DELETE /repos/owner/repo/issues/comments/10":
			writeTestJSON(t, w, `{}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[{"login":"human"}]}`)
		case "DELETE /repos/owner/repo/issues/9/labels/claimed":
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	})
	client, closeServer := testClient(t, handler)
	defer closeServer()
	if err := Release(context.Background(), client, "owner", "repo", testPullRequestEvent("closed", 1, false)); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if countLog(logs, "DELETE", "/repos/owner/repo/issues/comments/11") != 1 ||
		countLog(logs, "DELETE", "/repos/owner/repo/issues/comments/12") != 1 {
		t.Fatalf("issue marker deletes = %v", logs)
	}
	if !containsLog(logs, "DELETE", "/repos/owner/repo/issues/9/labels/claimed") {
		t.Fatal("Release() did not remove claimed")
	}
	if containsLog(logs, "DELETE", "/repos/owner/repo/issues/9/assignees") {
		t.Fatal("Release() removed the human assignee")
	}
}

func TestReleaseKeepsClaimedWhenAnotherClaimAuthorIsStillAssigned(t *testing.T) {
	var logs []requestLog
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"},{"id":12,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=2 author=contributor -->"}]`)
		case "DELETE /repos/owner/repo/issues/comments/11", "DELETE /repos/owner/repo/issues/comments/10":
			writeTestJSON(t, w, `{}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[{"login":"contributor"}]}`)
		case "DELETE /repos/owner/repo/issues/9/labels/claimed":
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	})
	client, closeServer := testClient(t, handler)
	defer closeServer()
	if err := Release(context.Background(), client, "owner", "repo", testPullRequestEvent("closed", 1, false)); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if countLog(logs, "DELETE", "/repos/owner/repo/issues/comments/11") != 1 ||
		countLog(logs, "DELETE", "/repos/owner/repo/issues/comments/12") != 0 {
		t.Fatalf("issue marker deletes = %v", logs)
	}
	if containsLog(logs, "DELETE", "/repos/owner/repo/issues/9/labels/claimed") {
		t.Fatal("Release() removed claimed while another claim author is still assigned")
	}
}

func TestReleaseRetryDeletesRemainingMarkersAfterAssigneeFailure(t *testing.T) {
	var logs []requestLog
	commentsReads := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9/comments":
			commentsReads++
			if commentsReads == 1 {
				writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"},{"id":12,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
			} else {
				writeTestJSON(t, w, `[{"id":12,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
			}
		case "DELETE /repos/owner/repo/issues/comments/11", "DELETE /repos/owner/repo/issues/comments/12":
			writeTestJSON(t, w, `{}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[{"login":"author"}]}`)
		case "DELETE /repos/owner/repo/issues/9/labels/claimed":
			if countLog(logs, "DELETE", "/repos/owner/repo/issues/9/labels/claimed") > 1 {
				http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
				return
			}
			writeTestJSON(t, w, `{}`)
		case "GET /repos/owner/repo/issues/9/events":
			writeTestJSON(t, w, `[{"event":"assigned","assignee":{"login":"author"},"assigner":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:00Z"}]`)
		case "DELETE /repos/owner/repo/issues/9/assignees":
			http.Error(w, `{"message":"assignee failed"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	client, closeServer := testClient(t, handler)
	defer closeServer()

	event := testPullRequestEvent("closed", 1, false)
	if err := Release(context.Background(), client, "owner", "repo", event); err == nil ||
		!strings.Contains(err.Error(), "remove @author from issue repo#9") {
		t.Fatalf("first Release() error = %v, want assignee removal error", err)
	}
	if err := Release(context.Background(), client, "owner", "repo", event); err == nil ||
		!strings.Contains(err.Error(), "remove @author from issue repo#9") {
		t.Fatalf("retry Release() error = %v, want assignee removal error", err)
	}
	if countLog(logs, "DELETE", "/repos/owner/repo/issues/comments/11") != 1 ||
		countLog(logs, "DELETE", "/repos/owner/repo/issues/comments/12") != 2 {
		t.Fatalf("marker deletes = %v, want marker 11 once and marker 12 twice", logs)
	}
	if !containsLog(logs, "DELETE", "/repos/owner/repo/issues/9/labels/claimed") {
		t.Fatal("Release() did not remove claimed")
	}
	if countLog(logs, "DELETE", "/repos/owner/repo/issues/9/assignees") != 2 {
		t.Fatalf("assignee removal attempts = %d, want 2 failed attempts", countLog(logs, "DELETE", "/repos/owner/repo/issues/9/assignees"))
	}
}

func TestReleaseRetryRestoresHelpWantedAfterLabelFailure(t *testing.T) {
	var logs []requestLog
	unassigned := false
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed","base":{"ref":"main"}}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			if unassigned {
				writeTestJSON(t, w, `{"number":9,"state":"open","labels":[],"assignees":[]}`)
			} else {
				writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[{"login":"author"}]}`)
			}
		case "GET /repos/owner/repo/issues/9/comments":
			if unassigned {
				writeTestJSON(t, w, `[]`)
			} else {
				writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
			}
		case "GET /repos/owner/repo/issues/9/events":
			if unassigned {
				writeTestJSON(t, w, `[{"event":"unassigned","assignee":{"login":"author"},"assigner":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:01:00Z"}]`)
			} else {
				writeTestJSON(t, w, `[{"event":"assigned","assignee":{"login":"author"},"assigner":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:00Z"}]`)
			}
		case "DELETE /repos/owner/repo/issues/comments/11", "DELETE /repos/owner/repo/issues/9/labels/claimed",
			"DELETE /repos/owner/repo/issues/comments/10":
			writeTestJSON(t, w, `{}`)
		case "DELETE /repos/owner/repo/issues/9/assignees":
			unassigned = true
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/9/labels":
			if countLog(logs, "POST", "/repos/owner/repo/issues/9/labels") == 1 {
				http.Error(w, "label failed", http.StatusInternalServerError)
				return
			}
			writeTestJSON(t, w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	event := testPullRequestEvent("closed", 1, false)
	if err := Release(context.Background(), client, "owner", "repo", event); err == nil || !strings.Contains(err.Error(), `restore "help wanted"`) {
		t.Fatalf("first Release() error = %v, want label restoration error", err)
	}
	if containsLog(logs, "DELETE", "/repos/owner/repo/issues/comments/10") {
		t.Fatal("Release() deleted the record after the label failure")
	}
	if err := Release(context.Background(), client, "owner", "repo", event); err != nil {
		t.Fatalf("retry Release() error = %v", err)
	}
	if countLog(logs, "POST", "/repos/owner/repo/issues/9/labels") != 2 ||
		!containsLog(logs, "DELETE", "/repos/owner/repo/issues/comments/10") {
		t.Fatalf("retry did not restore the label and delete the record: %v", logs)
	}
}
