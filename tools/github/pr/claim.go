package pr

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

func Claim(ctx context.Context, client *github.Client, owner, repository string, eventData []byte) error {
	current, err := newState(client, owner, repository, eventData)
	if err != nil {
		return err
	}
	if current.event.number == 0 {
		return nil
	}
	if err := current.collectClaim(ctx); err != nil {
		return err
	}
	current.planClaim()
	return current.apply(ctx)
}

func (s *state) planClaim() {
	pullRequest := s.observed.pullRequest
	if pullRequest == nil || pullRequest.State == "closed" || pullRequest.Merged || pullRequest.BaseRef != "main" || bypassesGate(pullRequest) {
		return
	}
	s.resolve()
	target := s.claimTarget()
	if target == nil {
		return
	}
	snapshot := s.issue(*target)
	hasPullRequestClaim := false
	if snapshot != nil {
		for _, claim := range snapshot.claims {
			if claim.PullRequestNumber == s.event.number {
				hasPullRequestClaim = true
				break
			}
		}
	}
	if s.observed.record == nil {
		s.changes = append(s.changes, change{
			kind:       changeComment,
			repository: s.repository,
			number:     s.event.number,
			body:       fmt.Sprintf("<!-- pr-gate:claim issue=%s author=%s -->", *target, pullRequest.Author.Login),
			issue:      *target,
		})
		s.observed.record = &contribution.ClaimRecord{Issue: *target, Author: pullRequest.Author.Login}
	}
	if !hasPullRequestClaim {
		s.changes = append(s.changes, change{
			kind:       changeComment,
			repository: target.Repository,
			number:     target.Number,
			body: fmt.Sprintf("<!-- pr-gate:claim pr=%d author=%s -->\n\nIssue claimed by @%s in PR #%d.",
				s.event.number, pullRequest.Author.Login, pullRequest.Author.Login, s.event.number),
			issue: *target,
		})
	}
	if snapshot == nil || snapshot.issue == nil {
		return
	}
	issue := snapshot.issue
	if !s.observed.claimedLabelExists {
		s.changes = append(s.changes, change{kind: changeCreateLabel})
		s.observed.claimedLabelExists = true
	}
	if !issue.HasAssignee(pullRequest.Author.Login) {
		s.changes = append(s.changes, change{
			kind:       changeAddAssignee,
			repository: target.Repository,
			number:     target.Number,
			user:       pullRequest.Author.Login,
			issue:      *target,
		})
	}
	if !issue.HasLabel(contribution.ClaimedLabel) {
		s.changes = append(s.changes, change{
			kind:       changeAddLabel,
			repository: target.Repository,
			number:     target.Number,
			label:      contribution.ClaimedLabel,
			issue:      *target,
		})
	}
	if issue.HasLabel(contribution.HelpWantedLabel) {
		s.changes = append(s.changes, change{
			kind:       changeRemoveLabel,
			repository: target.Repository,
			number:     target.Number,
			label:      contribution.HelpWantedLabel,
			issue:      *target,
			missingOK:  true,
		})
	}
	if !hasLabel(pullRequest, contribution.ClaimedLabel) {
		s.changes = append(s.changes, change{
			kind:       changeAddLabel,
			repository: s.repository,
			number:     s.event.number,
			label:      contribution.ClaimedLabel,
			issue:      contribution.IssueReference{Repository: s.repository, Number: s.event.number},
		})
	}
}

func (s *state) claimTarget() *contribution.IssueReference {
	if s.observed.record != nil {
		if !containsReference(s.observed.links, s.owner, s.observed.record.Issue) {
			return nil
		}
		if s.decision.approved == nil || s.decision.approved.Repository != s.observed.record.Issue.Repository || s.decision.approved.Number != s.observed.record.Issue.Number {
			return nil
		}
		issue := s.observed.record.Issue
		return &issue
	}
	return s.decision.approved
}

func ensureClaimedLabel(ctx context.Context, client *github.Client, owner, repository string) error {
	_, _, err := client.Issues.GetLabel(ctx, owner, repository, contribution.ClaimedLabel)
	if err == nil {
		return nil
	}
	if !contribution.IsNotFound(err) {
		return fmt.Errorf("read %q label in repository %s/%s: %w", contribution.ClaimedLabel, owner, repository, err)
	}
	color, description := "0e8a16", "A pull request has claimed this issue"
	_, _, err = client.Issues.CreateLabel(ctx, owner, repository, github.CreateIssueLabelRequest{
		Name: contribution.ClaimedLabel, Color: &color, Description: &description,
	})
	if err != nil {
		return fmt.Errorf("create %q label in repository %s/%s: %w", contribution.ClaimedLabel, owner, repository, err)
	}
	return nil
}

func hasLabel(item *contribution.PullRequest, name string) bool {
	if item == nil {
		return false
	}
	for _, label := range item.Labels {
		if label.Name == name {
			return true
		}
	}
	return false
}
