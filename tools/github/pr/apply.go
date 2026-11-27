package pr

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

type changeKind string

const (
	changeComment         changeKind = "comment"
	changePullState       changeKind = "pull-state"
	changeDeleteComment   changeKind = "delete-comment"
	changeAddLabel        changeKind = "add-label"
	changeRemoveLabel     changeKind = "remove-label"
	changeAddAssignee     changeKind = "add-assignee"
	changeRemoveAssignee  changeKind = "remove-assignee"
	changeCreateLabel     changeKind = "create-label"
	changeSetMilestone    changeKind = "set-milestone"
	changeRemoveMilestone changeKind = "remove-milestone"
)

type change struct {
	kind       changeKind
	repository string
	number     int
	commentID  int64
	body       string
	label      string
	user       string
	pullState  string
	milestone  int
	issue      contribution.IssueReference
	missingOK  bool
}

func (s *state) apply(ctx context.Context) error {
	for _, change := range s.changes {
		if err := s.applyChange(ctx, change); err != nil {
			return err
		}
		s.status("PR #%d: %s.", s.event.number, change.description())
	}
	return nil
}

func (s *state) applyChange(ctx context.Context, change change) error {
	switch change.kind {
	case changeComment:
		return createComment(ctx, s.client, s.owner, change.repository, change.number, change.body)
	case changePullState:
		return setPullRequestState(ctx, s.client, s.owner, s.repository, change.number, change.pullState)
	case changeDeleteComment:
		if change.commentID == 0 {
			return nil
		}
		if err := deleteComment(ctx, s.client, s.owner, change.repository, change.commentID); err != nil &&
			!(change.missingOK && contribution.IsNotFound(err)) {
			if change.issue.Number != 0 {
				return fmt.Errorf("delete issue claim for %s: %w", change.issue, err)
			}
			return fmt.Errorf("delete claim record for pull request #%d issue %s: %w", s.event.number, change.body, err)
		}
		return nil
	case changeAddLabel:
		if _, _, err := s.client.Issues.AddLabelsToIssue(ctx, s.owner, change.repository, change.number, []string{change.label}); err != nil {
			if change.label == contribution.HelpWantedLabel {
				return fmt.Errorf("restore %q label on issue %s: %w", change.label, change.issue, err)
			}
			if change.number == s.event.number && change.repository == s.repository {
				return fmt.Errorf("add %q label to pull request #%d: %w", change.label, change.number, err)
			}
			return fmt.Errorf("add %q label to %s: %w", change.label, change.issue, err)
		}
		return nil
	case changeRemoveLabel:
		if _, err := s.client.Issues.RemoveLabelForIssue(ctx, s.owner, change.repository, change.number, change.label); err != nil &&
			!(change.missingOK && contribution.IsNotFound(err)) {
			return fmt.Errorf("remove %q label from issue %s: %w", change.label, change.issue, err)
		}
		return nil
	case changeAddAssignee:
		_, _, err := s.client.Issues.AddAssignees(ctx, s.owner, change.repository, change.number, []string{change.user})
		if err != nil {
			return fmt.Errorf("assign issue %s to @%s: %w", change.issue, change.user, err)
		}
		return nil
	case changeRemoveAssignee:
		if _, _, err := s.client.Issues.RemoveAssignees(ctx, s.owner, change.repository, change.number, []string{change.user}); err != nil &&
			!(change.missingOK && contribution.IsNotFound(err)) {
			return fmt.Errorf("remove @%s from issue %s: %w", change.user, change.issue, err)
		}
		return nil
	case changeCreateLabel:
		return ensureClaimedLabel(ctx, s.client, s.owner, s.repository)
	case changeSetMilestone:
		if _, _, err := s.client.Issues.Update(ctx, s.owner, s.repository, change.number, github.UpdateIssueRequest{Milestone: &change.milestone}); err != nil {
			return fmt.Errorf("set milestone on pull request #%d: %w", change.number, err)
		}
		return nil
	case changeRemoveMilestone:
		return s.removeMilestone(ctx, change)
	default:
		return fmt.Errorf("unknown github change %q", change.kind)
	}
}

func (s *state) removeMilestone(ctx context.Context, change change) error {
	var removeErr error
	for attempt := 0; attempt < maxMilestoneRemovals; attempt++ {
		_, _, removeErr = s.client.Issues.RemoveMilestone(ctx, s.owner, change.repository, change.number)
		if removeErr == nil || contribution.IsNotFound(removeErr) {
			return nil
		}
		if attempt == maxMilestoneRemovals-1 {
			break
		}
		if err := wait(ctx, gateRetryDelay); err != nil {
			return err
		}
	}
	return fmt.Errorf("remove milestone from issue %s: %w", change.issue, removeErr)
}

func (change change) description() string {
	switch change.kind {
	case changeComment:
		return fmt.Sprintf("added a comment to %s#%d", change.repository, change.number)
	case changePullState:
		return fmt.Sprintf("set pull request state to %s", change.pullState)
	case changeDeleteComment:
		return fmt.Sprintf("deleted a comment from %s", change.repository)
	case changeAddLabel:
		return fmt.Sprintf("added %q to %s#%d", change.label, change.repository, change.number)
	case changeRemoveLabel:
		return fmt.Sprintf("removed %q from %s#%d", change.label, change.repository, change.number)
	case changeAddAssignee:
		return fmt.Sprintf("assigned @%s to %s#%d", change.user, change.repository, change.number)
	case changeRemoveAssignee:
		return fmt.Sprintf("unassigned @%s from %s#%d", change.user, change.repository, change.number)
	case changeCreateLabel:
		return fmt.Sprintf("created the %q label", contribution.ClaimedLabel)
	case changeSetMilestone:
		return fmt.Sprintf("set pull request milestone to #%d", change.milestone)
	case changeRemoveMilestone:
		return fmt.Sprintf("removed milestone from %s", change.issue)
	default:
		return fmt.Sprintf("applied %q", change.kind)
	}
}
