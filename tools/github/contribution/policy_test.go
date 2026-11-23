package contribution

import "testing"

func TestIssueApprovalFor(t *testing.T) {
	author := "contributor"
	tests := []struct {
		name string
		item *Issue
		want bool
	}{
		{name: "nil issue", item: nil, want: false},
		{name: "pull request", item: &Issue{State: "open", IsPullRequest: true, Labels: []Label{{Name: HelpWantedLabel}}}, want: false},
		{name: "closed issue", item: &Issue{State: "closed", Labels: []Label{{Name: HelpWantedLabel}}}, want: false},
		{name: "unassigned help wanted", item: &Issue{State: "open", Labels: []Label{{Name: HelpWantedLabel}}}, want: true},
		{name: "unassigned without help wanted", item: &Issue{State: "open"}, want: false},
		{name: "assigned only to author", item: &Issue{State: "open", Assignees: []User{{Login: author}}}, want: true},
		{
			name: "assigned to someone else with help wanted",
			item: &Issue{State: "open", Assignees: []User{{Login: "maintainer"}}, Labels: []Label{{Name: HelpWantedLabel}}},
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IssueApprovalFor(test.item, author); got != test.want {
				t.Fatalf("IssueApprovalFor() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestHasAssignedClaimAuthor(t *testing.T) {
	issue := &Issue{Assignees: []User{{Login: "contributor"}}}
	claims := []Claim{{Author: "other-contributor", PullRequestNumber: 43}, {Author: "contributor", PullRequestNumber: 42}}
	if !HasAssignedClaimAuthor(issue, claims) {
		t.Fatal("HasAssignedClaimAuthor() = false, want true for one live claim")
	}
	if HasAssignedClaimAuthor(issue, []Claim{{Author: "other-contributor"}}) {
		t.Fatal("HasAssignedClaimAuthor() = true, want false when no claim author is assigned")
	}
	if HasAssignedClaimAuthor(nil, claims) {
		t.Fatal("HasAssignedClaimAuthor() = true for nil issue")
	}
}

func TestOtherAssignee(t *testing.T) {
	issue := &Issue{Assignees: []User{{Login: "contributor"}, {Login: "maintainer"}}}
	if !OtherAssignee(issue, "contributor") {
		t.Fatal("OtherAssignee() = false, want true")
	}
	if OtherAssignee(issue, "maintainer") == false {
		t.Fatal("OtherAssignee() = false, want true")
	}
	if OtherAssignee(&Issue{Assignees: []User{{Login: "contributor"}}}, "contributor") {
		t.Fatal("OtherAssignee() = true for the only assignee")
	}
	if OtherAssignee(nil, "contributor") {
		t.Fatal("OtherAssignee() = true for nil issue")
	}
}
