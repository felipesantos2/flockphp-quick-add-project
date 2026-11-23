package pr

import (
	"context"
	"net/http"
	"testing"
)

func TestClaimCreatesOneRecordAndOneMarkerAcrossRetries(t *testing.T) {
	var logs []requestLog
	issueComments := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			labels := `[]`
			if countLog(logs, "POST", "/repos/owner/repo/issues/1/labels") > 0 {
				labels = `[{"name":"claimed"}]`
			}
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR","labels":`+labels+"}")
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			labels := `[{"name":"help wanted"}]`
			assignees := `[]`
			if countLog(logs, "POST", "/repos/owner/repo/issues/9/assignees") > 0 {
				labels = `[{"name":"claimed"}]`
				assignees = `[{"login":"author"}]`
			}
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":`+labels+`,"assignees":`+assignees+`}`)
		case "GET /repos/owner/repo/issues/9/comments":
			if issueComments == 0 {
				writeTestJSON(t, w, `[]`)
			} else {
				writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=other -->"}]`)
			}
		case "GET /repos/owner/repo/issues/1/comments":
			if countLog(logs, "POST", "/repos/owner/repo/issues/1/comments") == 0 {
				writeTestJSON(t, w, `[]`)
			} else {
				writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
			}
		case "POST /repos/owner/repo/issues/1/comments":
			issueComments++
			writeTestJSON(t, w, `{"id":10}`)
		case "POST /repos/owner/repo/issues/9/comments":
			issueComments++
			writeTestJSON(t, w, `{"id":11}`)
		case "GET /repos/owner/repo/labels/claimed":
			if countLog(logs, "POST", "/repos/owner/repo/labels") == 0 {
				http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			} else {
				writeTestJSON(t, w, `{}`)
			}
		case "POST /repos/owner/repo/labels":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/9/assignees":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[{"login":"author"}]}`)
		case "POST /repos/owner/repo/issues/9/labels", "POST /repos/owner/repo/issues/1/labels":
			writeTestJSON(t, w, `[]`)
		case "DELETE /repos/owner/repo/issues/9/labels/help wanted":
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	})
	client, closeServer := testClient(t, handler)
	defer closeServer()
	event := testPullRequestEvent("opened", 1, false)
	if err := Claim(context.Background(), client, "owner", "repo", event); err != nil {
		t.Fatalf("first Claim() error = %v", err)
	}
	if err := Claim(context.Background(), client, "owner", "repo", event); err != nil {
		t.Fatalf("second Claim() error = %v", err)
	}
	if countLog(logs, "POST", "/repos/owner/repo/issues/1/comments") != 1 {
		t.Fatalf("claim record count = %d, want 1", countLog(logs, "POST", "/repos/owner/repo/issues/1/comments"))
	}
	if countLog(logs, "POST", "/repos/owner/repo/issues/9/comments") != 1 {
		t.Fatalf("issue claim count = %d, want 1", countLog(logs, "POST", "/repos/owner/repo/issues/9/comments"))
	}
}

func TestClaimAcceptsIssueAssignedToAuthor(t *testing.T) {
	var logs []requestLog
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[],"assignees":[{"login":"author"}]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			if countLog(logs, "POST", "/repos/owner/repo/issues/9/comments") == 0 {
				writeTestJSON(t, w, `[]`)
			} else {
				writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
			}
		case "GET /repos/owner/repo/issues/1/comments":
			if countLog(logs, "POST", "/repos/owner/repo/issues/1/comments") == 0 {
				writeTestJSON(t, w, `[]`)
			} else {
				writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
			}
		case "POST /repos/owner/repo/issues/1/comments", "POST /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `{"id":10}`)
		case "GET /repos/owner/repo/labels/claimed":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/9/labels", "POST /repos/owner/repo/issues/1/labels":
			writeTestJSON(t, w, `[]`)
		default:
			http.NotFound(w, r)
		}
	})
	client, closeServer := testClient(t, handler)
	defer closeServer()
	event := testPullRequestEvent("opened", 1, false)
	if err := Claim(context.Background(), client, "owner", "repo", event); err != nil {
		t.Fatalf("first Claim() error = %v", err)
	}
	if err := Claim(context.Background(), client, "owner", "repo", event); err != nil {
		t.Fatalf("second Claim() error = %v", err)
	}
	if countLog(logs, "POST", "/repos/owner/repo/issues/9/comments") != 1 {
		t.Fatalf("issue claim count = %d, want 1", countLog(logs, "POST", "/repos/owner/repo/issues/9/comments"))
	}
}

func TestClaimContinuesWhenAssigneeIsIgnored(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"help wanted"}],"assignees":[]}`)
		case "GET /repos/owner/repo/issues/9/comments", "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[]`)
		case "POST /repos/owner/repo/issues/1/comments", "POST /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `{"id":10}`)
		case "GET /repos/owner/repo/labels/claimed":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/9/assignees":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"help wanted"}],"assignees":[]}`)
		case "POST /repos/owner/repo/issues/9/labels", "POST /repos/owner/repo/issues/1/labels":
			writeTestJSON(t, w, `[]`)
		case "DELETE /repos/owner/repo/issues/9/labels/help wanted":
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	err := Claim(context.Background(), client, "owner", "repo", testPullRequestEvent("opened", 1, false))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if !containsLog(logs, "POST", "/repos/owner/repo/issues/9/labels") ||
		!containsLog(logs, "DELETE", "/repos/owner/repo/issues/9/labels/help wanted") ||
		!containsLog(logs, "POST", "/repos/owner/repo/issues/1/labels") {
		t.Fatalf("Claim() did not finish the claim after a dropped assignee: %v", logs)
	}
}

func TestClaimRetryAfterIssueMarkerFailureDoesNotCreateSecondRecord(t *testing.T) {
	var logs []requestLog
	recordCreated := false
	issueMarkerCreated := false
	pullRequestLabelAdded := false
	issueMarkerAttempts := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			labels := `[]`
			if pullRequestLabelAdded {
				labels = `[{"name":"claimed"}]`
			}
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR","labels":`+labels+"}")
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[{"login":"author"}]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			if issueMarkerCreated {
				writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
			} else {
				writeTestJSON(t, w, `[]`)
			}
		case "GET /repos/owner/repo/issues/1/comments":
			if recordCreated {
				writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
			} else {
				writeTestJSON(t, w, `[]`)
			}
		case "POST /repos/owner/repo/issues/1/comments":
			recordCreated = true
			writeTestJSON(t, w, `{"id":10}`)
		case "POST /repos/owner/repo/issues/9/comments":
			issueMarkerAttempts++
			if issueMarkerAttempts == 1 {
				http.Error(w, `{"message":"marker failed"}`, http.StatusInternalServerError)
				return
			}
			issueMarkerCreated = true
			writeTestJSON(t, w, `{"id":11}`)
		case "GET /repos/owner/repo/labels/claimed":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/1/labels":
			pullRequestLabelAdded = true
			writeTestJSON(t, w, `[]`)
		default:
			http.NotFound(w, r)
		}
	})
	client, closeServer := testClient(t, handler)
	defer closeServer()

	event := testPullRequestEvent("opened", 1, false)
	if err := Claim(context.Background(), client, "owner", "repo", event); err == nil {
		t.Fatal("first Claim() returned nil after issue marker failure")
	}
	if err := Claim(context.Background(), client, "owner", "repo", event); err != nil {
		t.Fatalf("retry Claim() error = %v", err)
	}
	if countLog(logs, "POST", "/repos/owner/repo/issues/1/comments") != 1 {
		t.Fatalf("claim record count = %d, want 1", countLog(logs, "POST", "/repos/owner/repo/issues/1/comments"))
	}
	if countLog(logs, "POST", "/repos/owner/repo/issues/9/comments") != 2 {
		t.Fatalf("issue marker attempts = %d, want 2", countLog(logs, "POST", "/repos/owner/repo/issues/9/comments"))
	}
}

func TestClaimRetryAfterAssigneeFailureDoesNotCreateSecondMarker(t *testing.T) {
	var logs []requestLog
	recordCreated := false
	issueMarkerCreated := false
	pullRequestLabelAdded := false
	assigneeAttempts := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			labels := `[]`
			if pullRequestLabelAdded {
				labels = `[{"name":"claimed"}]`
			}
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR","labels":`+labels+"}")
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"help wanted"}],"assignees":[]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			if issueMarkerCreated {
				writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
			} else {
				writeTestJSON(t, w, `[]`)
			}
		case "GET /repos/owner/repo/issues/1/comments":
			if recordCreated {
				writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
			} else {
				writeTestJSON(t, w, `[]`)
			}
		case "POST /repos/owner/repo/issues/1/comments":
			recordCreated = true
			writeTestJSON(t, w, `{"id":10}`)
		case "POST /repos/owner/repo/issues/9/comments":
			issueMarkerCreated = true
			writeTestJSON(t, w, `{"id":11}`)
		case "GET /repos/owner/repo/labels/claimed":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/9/assignees":
			assigneeAttempts++
			if assigneeAttempts == 1 {
				http.Error(w, `{"message":"assignee failed"}`, http.StatusInternalServerError)
				return
			}
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[{"login":"author"}]}`)
		case "POST /repos/owner/repo/issues/9/labels":
			writeTestJSON(t, w, `[]`)
		case "DELETE /repos/owner/repo/issues/9/labels/help wanted":
			writeTestJSON(t, w, `{}`)
		case "POST /repos/owner/repo/issues/1/labels":
			pullRequestLabelAdded = true
			writeTestJSON(t, w, `[]`)
		default:
			http.NotFound(w, r)
		}
	})
	client, closeServer := testClient(t, handler)
	defer closeServer()

	event := testPullRequestEvent("opened", 1, false)
	if err := Claim(context.Background(), client, "owner", "repo", event); err == nil {
		t.Fatal("first Claim() returned nil after assignee failure")
	}
	if err := Claim(context.Background(), client, "owner", "repo", event); err != nil {
		t.Fatalf("retry Claim() error = %v", err)
	}
	if countLog(logs, "POST", "/repos/owner/repo/issues/9/comments") != 1 {
		t.Fatalf("issue marker count = %d, want 1", countLog(logs, "POST", "/repos/owner/repo/issues/9/comments"))
	}
	if countLog(logs, "POST", "/repos/owner/repo/issues/9/assignees") != 2 {
		t.Fatalf("assignee attempts = %d, want 2", countLog(logs, "POST", "/repos/owner/repo/issues/9/assignees"))
	}
}

func TestClaimDoesNotMutateADifferentApprovedIssue(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":10,"repository":{"name":"repo","owner":{"login":"owner"}}},{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "GET /repos/owner/repo/issues/10":
			writeTestJSON(t, w, `{"number":10,"state":"open","labels":[{"name":"help wanted"}]}`)
		case "GET /repos/owner/repo/issues/10/comments":
			writeTestJSON(t, w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	if err := Claim(context.Background(), client, "owner", "repo", testPullRequestEvent("synchronize", 1, false)); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if containsLog(logs, "POST", "/repos/owner/repo/issues/10/comments") ||
		containsLog(logs, "POST", "/repos/owner/repo/issues/10/assignees") ||
		containsLog(logs, "POST", "/repos/owner/repo/issues/1/comments") {
		t.Fatalf("Claim() mutated a different issue: %v", logs)
	}
}
