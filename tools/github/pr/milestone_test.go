package pr

import (
	"context"
	"net/http"
	"testing"
)

func TestMilestoneKeepsClaimWhenRemovalFailsAndRetries(t *testing.T) {
	var logs []requestLog
	removeCalls := 0
	milestoneSet := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","milestone":{"number":7},"labels":[{"name":"claimed"}],"assignees":[{"login":"author"}]}`)
		case "GET /repos/owner/repo/issues/1":
			if milestoneSet {
				writeTestJSON(t, w, `{"number":1,"state":"open","milestone":{"number":7}}`)
			} else {
				writeTestJSON(t, w, `{"number":1,"state":"open"}`)
			}
		case "PATCH /repos/owner/repo/issues/1":
			milestoneSet = true
			writeTestJSON(t, w, `{"number":1,"state":"open","milestone":{"number":7}}`)
		case "PATCH /repos/owner/repo/issues/9":
			removeCalls++
			if removeCalls <= 3 {
				http.Error(w, `{"message":"failed"}`, http.StatusInternalServerError)
			} else {
				http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			}
		default:
			http.NotFound(w, r)
		}
	})
	client, closeServer := testClient(t, handler)
	defer closeServer()
	event := testPullRequestEvent("opened", 1, false)
	if err := Milestone(context.Background(), client, "owner", "repo", event); err == nil {
		t.Fatalf("Milestone() error = nil, want removal error; logs=%v", logs)
	}
	if countLog(logs, "PATCH", "/repos/owner/repo/issues/9") != 3 {
		t.Fatalf("remove calls = %d, want 3", countLog(logs, "PATCH", "/repos/owner/repo/issues/9"))
	}
	if countLog(logs, "DELETE", "/repos/owner/repo/issues/comments/10") != 0 ||
		countLog(logs, "DELETE", "/repos/owner/repo/issues/9/labels/claimed") != 0 {
		t.Fatal("Milestone() released the claim")
	}
	if err := Milestone(context.Background(), client, "owner", "repo", event); err != nil {
		t.Fatalf("retry Milestone() error = %v", err)
	}
}
