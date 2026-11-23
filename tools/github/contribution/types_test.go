package contribution

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/go-github/v93/github"
)

func TestGitHubModelsPreserveContributionPolicyFields(t *testing.T) {
	var pullRequestPayload github.PullRequest
	if err := json.Unmarshal([]byte(`{
		"number": 42,
		"state": "open",
		"merged": false,
		"body": "Fixes #9",
		"base": {"ref": "main"},
		"user": {"login": "author", "type": "User"},
		"author_association": "CONTRIBUTOR",
		"labels": [{"name": "claimed"}]
	}`), &pullRequestPayload); err != nil {
		t.Fatal(err)
	}
	pullRequest := PullRequestFromGitHub(&pullRequestPayload)
	if pullRequest == nil || pullRequest.Number != 42 || pullRequest.BaseRef != "main" ||
		pullRequest.Author.Login != "author" || !hasLabel(pullRequest.Labels, "claimed") {
		t.Fatalf("PullRequestFromGitHub() = %#v", pullRequest)
	}

	var issuePayload github.Issue
	if err := json.Unmarshal([]byte(`{
		"number": 9,
		"state": "open",
		"body": "### Platform\nLinux",
		"user": {"login": "author", "type": "User"},
		"author_association": "CONTRIBUTOR",
		"assignees": [{"login": "author"}],
		"labels": [{"name": "help wanted"}]
	}`), &issuePayload); err != nil {
		t.Fatal(err)
	}
	issue := IssueFromGitHub(&issuePayload)
	if issue == nil || issue.Number != 9 || issue.Body == "" || !issue.HasAssignee("author") ||
		!issue.HasLabel(HelpWantedLabel) {
		t.Fatalf("IssueFromGitHub() = %#v", issue)
	}
}

func TestGitHubModelsHandleNilValues(t *testing.T) {
	if PullRequestFromGitHub(nil) != nil || IssueFromGitHub(nil) != nil {
		t.Fatal("nil GitHub models must stay nil")
	}
}

func TestClaimsFromCommentsUsesOnlyBotClaimMarkers(t *testing.T) {
	var comments []*github.IssueComment
	if err := json.Unmarshal([]byte(`[
		{"id": 1, "body": "<!-- pr-gate:claim pr=2 author=author -->", "user": {"login": "github-actions[bot]"}},
		{"id": 2, "body": "<!-- pr-gate:claim pr=3 author=other -->", "user": {"login": "person"}},
		{"id": 3, "body": "not a claim", "user": {"login": "github-actions[bot]"}}
	]`), &comments); err != nil {
		t.Fatal(err)
	}
	claims := ClaimsFromComments(comments)
	if len(claims) != 1 || claims[0] != (Claim{PullRequestNumber: 2, Author: "author", CommentID: 1}) {
		t.Fatalf("ClaimsFromComments() = %#v", claims)
	}
}

func TestIsNotFoundRecognizesOnlyGitHubNotFoundResponses(t *testing.T) {
	notFound := &github.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}}
	if !IsNotFound(notFound) {
		t.Fatal("IsNotFound() = false for a GitHub 404 response")
	}
	if IsNotFound(&github.ErrorResponse{Response: &http.Response{StatusCode: http.StatusForbidden}}) {
		t.Fatal("IsNotFound() = true for a GitHub 403 response")
	}
}

func hasLabel(labels []Label, name string) bool {
	for _, label := range labels {
		if label.Name == name {
			return true
		}
	}
	return false
}
