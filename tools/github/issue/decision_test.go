package issue

import (
	"testing"

	"flock/tools/github/contribution"
)

func TestPlanTriageBuildsOnlyTheRequiredLabels(t *testing.T) {
	tests := []struct {
		name       string
		event      issueEvent
		permission string
		want       []labelChange
	}{
		{
			name:       "untrusted author needs platform and triage labels",
			event:      issueEvent{action: "opened", issue: contribution.Issue{Number: 9, Body: "### Platform\n\nLinux, macOS"}},
			permission: "read",
			want:       []labelChange{{add: true, label: "os: linux"}, {add: true, label: "os: mac"}, {add: true, label: "needs triage"}},
		},
		{
			name:       "trusted author only gets platform labels",
			event:      issueEvent{action: "opened", issue: contribution.Issue{Number: 9, User: contribution.User{Type: "Bot"}, Body: "### Platform\n\nWindows"}},
			permission: "read",
			want:       []labelChange{{add: true, label: "os: windows"}},
		},
		{name: "non-open event does nothing", event: issueEvent{action: "assigned", issue: contribution.Issue{Number: 9}}, want: nil},
		{name: "missing issue does nothing", event: issueEvent{action: "opened"}, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := &state{event: test.event, observed: observed{permission: test.permission}}
			current.planTriage()
			assertLabelChanges(t, current.changes, test.want)
		})
	}
}

func TestPlanAssignmentRemovesOnlyStaleAvailabilityLabels(t *testing.T) {
	tests := []struct {
		name   string
		item   *contribution.Issue
		claims []contribution.Claim
		want   []labelChange
	}{
		{
			name: "assigned claim author keeps claimed",
			item: &contribution.Issue{
				Assignees: []contribution.User{{Login: "author"}},
				Labels:    []contribution.Label{{Name: contribution.HelpWantedLabel}, {Name: contribution.ClaimedLabel}},
			},
			claims: []contribution.Claim{{Author: "author"}},
			want:   []labelChange{{label: contribution.HelpWantedLabel}},
		},
		{
			name: "different assignee clears stale claimed",
			item: &contribution.Issue{
				Assignees: []contribution.User{{Login: "maintainer"}},
				Labels:    []contribution.Label{{Name: contribution.ClaimedLabel}},
			},
			claims: []contribution.Claim{{Author: "author"}},
			want:   []labelChange{{label: contribution.ClaimedLabel}},
		},
		{name: "no assignee does nothing", item: &contribution.Issue{}, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := &state{
				event:    issueEvent{action: "assigned", issue: contribution.Issue{Number: 9}},
				observed: observed{issue: test.item, claims: test.claims},
			}
			current.planAssignment()
			assertLabelChanges(t, current.changes, test.want)
		})
	}
}

func assertLabelChanges(t *testing.T, got, want []labelChange) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("label changes = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("label changes = %#v, want %#v", got, want)
		}
	}
}
