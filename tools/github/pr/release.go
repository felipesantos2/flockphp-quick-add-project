package pr

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

func Release(ctx context.Context, client *github.Client, owner, repository string, eventData []byte) error {
	current, err := newState(client, owner, repository, eventData)
	if err != nil {
		return err
	}
	if current.event.number == 0 {
		return nil
	}
	if err := current.collectRelease(ctx); err != nil {
		return err
	}
	current.planRelease()
	return current.apply(ctx)
}

func (s *state) planRelease() {
	pullRequest := s.observed.pullRequest
	record := s.observed.record
	if pullRequest == nil || pullRequest.Merged || record == nil {
		return
	}
	if pullRequest.State == "open" && pullRequest.BaseRef == "main" && containsReference(s.observed.links, s.owner, record.Issue) {
		if !s.decision.made || s.decision.approved == nil ||
			(s.decision.approved.Repository == record.Issue.Repository && s.decision.approved.Number == record.Issue.Number) {
			return
		}
	}
	snapshot := s.issue(record.Issue)
	if snapshot == nil || snapshot.issue == nil {
		s.queueForgetRecord()
		return
	}
	var remaining []contribution.Claim
	for _, claim := range snapshot.claims {
		if claim.PullRequestNumber != s.event.number {
			remaining = append(remaining, claim)
			continue
		}
		s.changes = append(s.changes, change{
			kind:       changeDeleteComment,
			repository: record.Issue.Repository,
			commentID:  claim.CommentID,
			issue:      record.Issue,
			missingOK:  true,
		})
	}
	snapshot.claims = remaining
	issue := snapshot.issue
	remainingClaim := contribution.HasAssignedClaimAuthor(issue, remaining) || (len(issue.Assignees) == 0 && len(remaining) != 0)
	wasClaimed := issue.HasLabel(contribution.ClaimedLabel)
	if ((wasClaimed && snapshot.assigner == "") || (!snapshot.assigned && snapshot.assigner == "github-actions[bot]")) &&
		!remainingClaim && issue.State == "open" &&
		len(issue.Assignees) == 0 && !issue.HasLabel(contribution.HelpWantedLabel) {
		s.changes = append(s.changes, change{
			kind: changeAddLabel, repository: record.Issue.Repository, number: record.Issue.Number,
			label: contribution.HelpWantedLabel, issue: record.Issue,
		})
	}
	if wasClaimed && !remainingClaim {
		s.changes = append(s.changes, change{
			kind:       changeRemoveLabel,
			repository: record.Issue.Repository,
			number:     record.Issue.Number,
			label:      contribution.ClaimedLabel,
			issue:      record.Issue,
			missingOK:  true,
		})
		issue.Labels = withoutLabel(issue.Labels, contribution.ClaimedLabel)
	}
	if remainingClaim {
		s.queueForgetRecord()
		return
	}
	if contribution.OtherAssignee(issue, record.Author) {
		s.queueForgetRecord()
		return
	}
	if !snapshot.assigned || snapshot.assigner != "github-actions[bot]" {
		s.queueForgetRecord()
		return
	}
	s.changes = append(s.changes, change{
		kind:       changeRemoveAssignee,
		repository: record.Issue.Repository,
		number:     record.Issue.Number,
		user:       record.Author,
		issue:      record.Issue,
		missingOK:  true,
	})
	issue.Assignees = withoutUser(issue.Assignees, record.Author)
	if issue.State == "open" && len(issue.Assignees) == 0 {
		s.changes = append(s.changes, change{
			kind:       changeAddLabel,
			repository: record.Issue.Repository,
			number:     record.Issue.Number,
			label:      contribution.HelpWantedLabel,
			issue:      record.Issue,
		})
	}
	s.queueForgetRecord()
}

func (s *state) queueForgetRecord() {
	record := s.observed.record
	if record != nil && record.CommentID != 0 {
		s.changes = append(s.changes, change{
			kind:       changeDeleteComment,
			repository: s.repository,
			commentID:  record.CommentID,
			body:       record.Issue.String(),
			missingOK:  true,
		})
	}
	s.observed.record = nil
}

func withoutLabel(labels []contribution.Label, name string) []contribution.Label {
	var remaining []contribution.Label
	for _, label := range labels {
		if label.Name != name {
			remaining = append(remaining, label)
		}
	}
	return remaining
}

func withoutUser(users []contribution.User, login string) []contribution.User {
	var remaining []contribution.User
	for _, user := range users {
		if user.Login != login {
			remaining = append(remaining, user)
		}
	}
	return remaining
}

func containsReference(issues []contribution.LinkedIssue, owner string, reference contribution.IssueReference) bool {
	for _, issue := range issues {
		if issue.Owner == owner && issue.Repository == reference.Repository && issue.Number == reference.Number {
			return true
		}
	}
	return false
}

func deleteClaimRecord(ctx context.Context, client *github.Client, owner, repository string, pullRequestNumber int, record *contribution.ClaimRecord) error {
	if record == nil || record.CommentID == 0 {
		return nil
	}
	if err := deleteComment(ctx, client, owner, repository, record.CommentID); err != nil &&
		!contribution.IsNotFound(err) {
		return fmt.Errorf("delete claim record for pull request #%d issue %s: %w", pullRequestNumber, record.Issue, err)
	}
	return nil
}
