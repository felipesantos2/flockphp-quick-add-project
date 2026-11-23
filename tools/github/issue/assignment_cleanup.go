package issue

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

func Assignment(ctx context.Context, client *github.Client, owner, repository string, data []byte) error {
	current, err := newState(client, owner, repository, data)
	if err != nil {
		return err
	}
	if err := current.observeAssignment(ctx); err != nil {
		return err
	}
	current.planAssignment()
	return current.apply(ctx)
}

func (s *state) observeAssignment(ctx context.Context) error {
	if s.event.action != "assigned" || s.event.issue.Number == 0 {
		return nil
	}
	item, _, err := s.client.Issues.Get(ctx, s.owner, s.repository, s.event.issue.Number)
	if contribution.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read assigned issue #%d: %w", s.event.issue.Number, err)
	}
	s.observed.issue = contribution.IssueFromGitHub(item)
	if s.observed.issue == nil || len(s.observed.issue.Assignees) == 0 || !s.observed.issue.HasLabel(contribution.ClaimedLabel) {
		return nil
	}
	claims, err := findIssueClaims(ctx, s.client, s.owner, s.repository, s.event.issue.Number)
	if err != nil {
		return fmt.Errorf("read claim for issue #%d: %w", s.event.issue.Number, err)
	}
	s.observed.claims = claims
	return nil
}

func (s *state) planAssignment() {
	item := s.observed.issue
	if s.event.action != "assigned" || item == nil || len(item.Assignees) == 0 {
		return
	}
	if item.HasLabel(contribution.HelpWantedLabel) {
		s.changes = append(s.changes, labelChange{label: contribution.HelpWantedLabel})
	}
	if item.HasLabel(contribution.ClaimedLabel) && len(s.observed.claims) != 0 && !contribution.HasAssignedClaimAuthor(item, s.observed.claims) {
		s.changes = append(s.changes, labelChange{label: contribution.ClaimedLabel})
	}
}

func findIssueClaims(ctx context.Context, client *github.Client, owner, repository string, number int) ([]contribution.Claim, error) {
	options := &github.IssueListCommentsOptions{PerPage: 100}
	var claims []contribution.Claim
	for {
		comments, response, err := client.Issues.ListComments(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		claims = append(claims, contribution.ClaimsFromComments(comments)...)
		if response.NextPage == 0 {
			return claims, nil
		}
		options.Page = response.NextPage
	}
}
