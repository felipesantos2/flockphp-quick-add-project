package pr

import (
	"testing"

	"flock/tools/github/contribution"
)

func TestApprovalAppliesIssueOwnershipAndClaimPolicy(t *testing.T) {
	tests := []struct {
		name     string
		links    []contribution.LinkedIssue
		snapshot *issueSnapshot
		want     *contribution.IssueReference
		denied   bool
	}{
		{
			name:  "unclaimed help wanted issue is approved",
			links: []contribution.LinkedIssue{{Owner: "owner", Repository: "repo", Number: 9}},
			snapshot: &issueSnapshot{issue: &contribution.Issue{
				State: "open", Labels: []contribution.Label{{Name: contribution.HelpWantedLabel}},
			}, claims: []contribution.Claim{}},
			want: &contribution.IssueReference{Repository: "repo", Number: 9},
		},
		{
			name:  "foreign claim blocks an unassigned issue",
			links: []contribution.LinkedIssue{{Owner: "owner", Repository: "repo", Number: 9}},
			snapshot: &issueSnapshot{issue: &contribution.Issue{
				State: "open", Labels: []contribution.Label{{Name: contribution.HelpWantedLabel}},
			}, claims: []contribution.Claim{{PullRequestNumber: 2, Author: "other"}}},
		},
		{
			name:  "unassigned claim remains approved for its pull request",
			links: []contribution.LinkedIssue{{Owner: "owner", Repository: "repo", Number: 9}},
			snapshot: &issueSnapshot{issue: &contribution.Issue{
				State: "open", Labels: []contribution.Label{{Name: contribution.ClaimedLabel}},
			}, claims: []contribution.Claim{{PullRequestNumber: 1, Author: "author"}}},
			want: &contribution.IssueReference{Repository: "repo", Number: 9},
		},
		{
			name:  "unassigned claim does not approve a different pull request",
			links: []contribution.LinkedIssue{{Owner: "owner", Repository: "repo", Number: 9}},
			snapshot: &issueSnapshot{issue: &contribution.Issue{
				State: "open", Labels: []contribution.Label{{Name: contribution.ClaimedLabel}},
			}, claims: []contribution.Claim{{PullRequestNumber: 2, Author: "author"}}},
		},
		{
			name:  "unassigned claim does not override a maintainer assignment",
			links: []contribution.LinkedIssue{{Owner: "owner", Repository: "repo", Number: 9}},
			snapshot: &issueSnapshot{issue: &contribution.Issue{
				State: "open", Labels: []contribution.Label{{Name: contribution.ClaimedLabel}},
				Assignees: []contribution.User{{Login: "maintainer"}},
			}, claims: []contribution.Claim{{PullRequestNumber: 1, Author: "author"}}},
			denied: true,
		},
		{
			name:  "assigned author can proceed despite another claim",
			links: []contribution.LinkedIssue{{Owner: "owner", Repository: "repo", Number: 9}},
			snapshot: &issueSnapshot{issue: &contribution.Issue{
				State: "open", Assignees: []contribution.User{{Login: "author"}},
			}, claims: []contribution.Claim{{PullRequestNumber: 2, Author: "other"}}},
			want: &contribution.IssueReference{Repository: "repo", Number: 9},
		},
		{
			name:  "assigned other contributor explains denial",
			links: []contribution.LinkedIssue{{Owner: "owner", Repository: "repo", Number: 9}},
			snapshot: &issueSnapshot{issue: &contribution.Issue{
				State: "open", Assignees: []contribution.User{{Login: "maintainer"}},
			}},
			denied: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := decisionState()
			current.observed.links = test.links
			if test.snapshot != nil {
				current.observed.issues["repo#9"] = test.snapshot
			}
			got, denied := current.approval()
			if got != nil && (test.want == nil || *got != *test.want) {
				t.Fatalf("approved issue = %#v, want %#v", got, test.want)
			}
			if got == nil && test.want != nil {
				t.Fatalf("approved issue = nil, want %#v", test.want)
			}
			if (denied != nil) != test.denied {
				t.Fatalf("denial = %#v, want denied=%v", denied, test.denied)
			}
		})
	}
}

func TestPlanGateAppliesOnlyRequiredPullRequestStateChanges(t *testing.T) {
	tests := []struct {
		name    string
		current *state
		want    []changeKind
	}{
		{
			name: "docs only pull request stays open",
			current: &state{owner: "owner", repository: "repo", observed: observed{
				pullRequest: &contribution.PullRequest{Number: 1, State: "open", BaseRef: "main"},
				files:       []contribution.File{{Filename: "docs/guide.md"}},
			}},
		},
		{
			name: "translations only pull request stays open",
			current: &state{owner: "owner", repository: "repo", observed: observed{
				pullRequest: &contribution.PullRequest{Number: 1, State: "open", BaseRef: "main"},
				files:       []contribution.File{{Filename: "lang/fr/app.json"}},
			}},
		},
		{
			name: "translations and code pull request closes",
			current: &state{owner: "owner", repository: "repo", observed: observed{
				pullRequest: &contribution.PullRequest{Number: 1, State: "open", BaseRef: "main"},
				files:       []contribution.File{{Filename: "lang/fr/app.json"}, {Filename: "internal/app.go"}},
			}},
			want: []changeKind{changeComment, changePullState},
		},
		{
			name: "wrong base closes the pull request",
			current: &state{owner: "owner", repository: "repo", observed: observed{
				pullRequest: &contribution.PullRequest{Number: 1, State: "open", BaseRef: "release"},
			}},
			want: []changeKind{changeComment, changePullState},
		},
		{
			name:    "approved issue keeps code pull request open",
			current: approvedDecisionState(),
		},
		{
			name: "gate closed pull request reopens when approved",
			current: &state{owner: "owner", repository: "repo", event: pullRequestEvent{action: "edited", bodyEdited: true}, observed: observed{
				pullRequest: &contribution.PullRequest{Number: 1, State: "closed", BaseRef: "main"},
				gateClosed:  true,
			}, decision: decision{approved: &contribution.IssueReference{Repository: "repo", Number: 9}, made: true}},
			want: []changeKind{changePullState, changeComment},
		},
		{
			name: "gate closed translation pull request reopens after body edit",
			current: &state{owner: "owner", repository: "repo", event: pullRequestEvent{action: "edited", bodyEdited: true}, observed: observed{
				pullRequest: &contribution.PullRequest{Number: 1, State: "closed", BaseRef: "main"},
				files:       []contribution.File{{Filename: "lang/fr/app.json"}},
				gateClosed:  true,
			}},
			want: []changeKind{changePullState, changeComment},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.current.planGate()
			assertChangeKinds(t, test.current.changes, test.want)
		})
	}
}

func TestPlanDoesNotCloseUnapprovedMaintainerPullRequest(t *testing.T) {
	current := decisionState()
	current.observed.pullRequest.AuthorAssociation = "MEMBER"
	current.observed.files = []contribution.File{{Filename: "internal/file.go"}}

	current.plan()

	assertChangeKinds(t, current.changes, nil)
}

func TestPlanMovesStaleMaintainerClaimToApprovedIssue(t *testing.T) {
	current := decisionState()
	current.observed.pullRequest.AuthorAssociation = "MEMBER"
	current.observed.links = []contribution.LinkedIssue{
		{Owner: "owner", Repository: "repo", Number: 9},
		{Owner: "owner", Repository: "repo", Number: 10},
	}
	current.observed.record = &contribution.ClaimRecord{
		Issue: contribution.IssueReference{Repository: "repo", Number: 9}, Author: "author", CommentID: 10,
	}
	current.observed.claimedLabelExists = true
	current.observed.issues["repo#9"] = &issueSnapshot{
		issue: &contribution.Issue{
			State: "open", Assignees: []contribution.User{{Login: "maintainer"}},
			Labels: []contribution.Label{{Name: contribution.ClaimedLabel}},
		},
		claims: []contribution.Claim{{PullRequestNumber: 1, Author: "author", CommentID: 11}},
	}
	current.observed.issues["repo#10"] = &issueSnapshot{
		issue:  &contribution.Issue{State: "open", Labels: []contribution.Label{{Name: contribution.HelpWantedLabel}}},
		claims: []contribution.Claim{},
	}

	current.plan()

	assertChangeKinds(t, current.changes, []changeKind{
		changeDeleteComment, changeRemoveLabel, changeDeleteComment,
		changeComment, changeComment, changeAddAssignee, changeAddLabel, changeRemoveLabel, changeAddLabel,
	})
}

func TestPlanReleasesClaimWhenOpenPullRequestLosesApproval(t *testing.T) {
	tests := []struct {
		name        string
		association string
		files       []contribution.File
	}{
		{"maintainer", "MEMBER", []contribution.File{{Filename: "internal/file.go"}}},
		{"exempt contributor", "CONTRIBUTOR", []contribution.File{{Filename: "docs/file.md"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := decisionState()
			current.event.action = "synchronize"
			current.observed.pullRequest.AuthorAssociation = test.association
			current.observed.files = test.files
			current.observed.links = []contribution.LinkedIssue{{Owner: "owner", Repository: "repo", Number: 9}}
			current.observed.record = &contribution.ClaimRecord{
				Issue: contribution.IssueReference{Repository: "repo", Number: 9}, Author: "author", CommentID: 10,
			}
			current.observed.claimedLabelExists = true
			current.observed.issues["repo#9"] = &issueSnapshot{
				issue: &contribution.Issue{
					State: "open", Assignees: []contribution.User{{Login: "other"}},
					Labels: []contribution.Label{{Name: contribution.ClaimedLabel}},
				},
				milestone: 3,
				claims:    []contribution.Claim{{PullRequestNumber: 1, Author: "author", CommentID: 11}},
			}

			current.plan()

			assertChangeKinds(t, current.changes, []changeKind{changeDeleteComment, changeRemoveLabel, changeDeleteComment})
			if current.observed.pullRequest.State != "open" {
				t.Fatalf("pull request state = %q, want open", current.observed.pullRequest.State)
			}
		})
	}
}

func TestExemptFilesOnly(t *testing.T) {
	tests := []struct {
		name  string
		files []contribution.File
		want  bool
	}{
		{name: "empty"},
		{name: "documentation", files: []contribution.File{{Filename: "docs/guide.md"}}, want: true},
		{name: "translations", files: []contribution.File{{Filename: "lang/fr/app.json"}}, want: true},
		{name: "both directories", files: []contribution.File{{Filename: "docs/guide.md"}, {Filename: "lang/fr/app.json"}}, want: true},
		{name: "rename between exempt directories", files: []contribution.File{{Filename: "lang/file", PreviousFilename: "docs/file"}}, want: true},
		{name: "rename from outside", files: []contribution.File{{Filename: "lang/file", PreviousFilename: "internal/file"}}},
		{name: "rename to outside", files: []contribution.File{{Filename: "internal/file", PreviousFilename: "lang/file"}}},
		{name: "similar prefix", files: []contribution.File{{Filename: "language/file"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := exemptFilesOnly(test.files); got != test.want {
				t.Fatalf("exemptFilesOnly() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPlanClaimReservesApprovedWorkOnce(t *testing.T) {
	current := approvedDecisionState()
	current.observed.claimedLabelExists = false
	current.observed.issues["repo#9"] = &issueSnapshot{issue: &contribution.Issue{
		State: "open", Labels: []contribution.Label{{Name: contribution.HelpWantedLabel}},
	}, claims: []contribution.Claim{}}

	current.planClaim()

	assertChangeKinds(t, current.changes, []changeKind{
		changeComment, changeComment, changeCreateLabel, changeAddAssignee, changeAddLabel,
		changeRemoveLabel, changeAddLabel,
	})
}

func TestPlanMilestoneTransfersOnlyTheClaimedIssueMilestone(t *testing.T) {
	current := approvedDecisionState()
	current.observed.record = &contribution.ClaimRecord{Issue: contribution.IssueReference{Repository: "repo", Number: 9}}
	current.observed.issues["repo#9"] = &issueSnapshot{milestone: 7}

	current.planMilestone()

	assertChangeKinds(t, current.changes, []changeKind{changeSetMilestone, changeRemoveMilestone})
}

func TestPlanReleaseReturnsAnUnmergedClaimToTheIssue(t *testing.T) {
	current := decisionState()
	current.observed.pullRequest.State = "closed"
	current.observed.record = &contribution.ClaimRecord{
		Issue: contribution.IssueReference{Repository: "repo", Number: 9}, Author: "author", CommentID: 10,
	}
	current.observed.issues["repo#9"] = &issueSnapshot{
		issue: &contribution.Issue{
			State:     "open",
			Assignees: []contribution.User{{Login: "author"}},
			Labels:    []contribution.Label{{Name: contribution.ClaimedLabel}},
		},
		claims:     []contribution.Claim{{PullRequestNumber: 1, Author: "author", CommentID: 11}},
		assigned:   true,
		assigner:   "github-actions[bot]",
		assignment: true,
	}

	current.planRelease()

	assertChangeKinds(t, current.changes, []changeKind{
		changeDeleteComment, changeRemoveLabel, changeRemoveAssignee, changeAddLabel, changeDeleteComment,
	})
}

func TestPlanReleaseRetainsClaimsForMergedPullRequests(t *testing.T) {
	current := decisionState()
	current.observed.pullRequest.Merged = true
	current.observed.record = &contribution.ClaimRecord{Issue: contribution.IssueReference{Repository: "repo", Number: 9}, CommentID: 10}

	current.planRelease()

	assertChangeKinds(t, current.changes, nil)
}

func TestPlanReleaseUnassignedClaims(t *testing.T) {
	for _, test := range []struct {
		name      string
		remaining bool
		want      []changeKind
	}{
		{name: "last claim restores help wanted", want: []changeKind{changeDeleteComment, changeAddLabel, changeRemoveLabel, changeDeleteComment}},
		{name: "remaining claim stays reserved", remaining: true, want: []changeKind{changeDeleteComment, changeDeleteComment}},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := decisionState()
			current.observed.pullRequest.State = "closed"
			current.observed.record = &contribution.ClaimRecord{Issue: contribution.IssueReference{Repository: "repo", Number: 9}, Author: "author", CommentID: 10}
			claims := []contribution.Claim{{PullRequestNumber: 1, Author: "author", CommentID: 11}}
			if test.remaining {
				claims = append(claims, contribution.Claim{PullRequestNumber: 2, Author: "other", CommentID: 12})
			}
			current.observed.issues["repo#9"] = &issueSnapshot{
				issue:  &contribution.Issue{State: "open", Labels: []contribution.Label{{Name: contribution.ClaimedLabel}}},
				claims: claims, assignment: true,
			}
			current.planRelease()
			assertChangeKinds(t, current.changes, test.want)
		})
	}
}

func decisionState() *state {
	return &state{
		owner:      "owner",
		repository: "repo",
		event:      pullRequestEvent{number: 1, action: "opened"},
		observed: observed{
			pullRequest: &contribution.PullRequest{
				Number: 1, State: "open", BaseRef: "main", Author: contribution.User{Login: "author"},
			},
			issues: map[string]*issueSnapshot{},
		},
	}
}

func approvedDecisionState() *state {
	current := decisionState()
	current.observed.links = []contribution.LinkedIssue{{Owner: "owner", Repository: "repo", Number: 9}}
	current.decision = decision{approved: &contribution.IssueReference{Repository: "repo", Number: 9}, made: true}
	return current
}

func assertChangeKinds(t *testing.T, changes []change, want []changeKind) {
	t.Helper()
	if len(changes) != len(want) {
		t.Fatalf("changes = %#v, want kinds %#v", changes, want)
	}
	for index := range want {
		if changes[index].kind != want[index] {
			t.Fatalf("changes = %#v, want kinds %#v", changes, want)
		}
	}
}
