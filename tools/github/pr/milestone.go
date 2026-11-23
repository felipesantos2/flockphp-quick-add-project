package pr

import (
	"context"

	"github.com/google/go-github/v93/github"
)

const maxMilestoneRemovals = 3

func Milestone(ctx context.Context, client *github.Client, owner, repository string, eventData []byte) error {
	current, err := newState(client, owner, repository, eventData)
	if err != nil {
		return err
	}
	if current.event.number == 0 {
		return nil
	}
	if err := current.collectMilestone(ctx); err != nil {
		return err
	}
	current.planMilestone()
	return current.apply(ctx)
}

func (s *state) planMilestone() {
	pullRequest := s.observed.pullRequest
	record := s.observed.record
	if pullRequest == nil || pullRequest.State != "open" || pullRequest.BaseRef != "main" || record == nil || record.Issue.Repository != s.repository {
		return
	}
	snapshot := s.issue(record.Issue)
	if snapshot == nil || snapshot.milestone == 0 {
		return
	}
	if s.observed.pullRequestMilestone != snapshot.milestone {
		s.changes = append(s.changes, change{
			kind:      changeSetMilestone,
			number:    s.event.number,
			milestone: snapshot.milestone,
		})
	}
	s.changes = append(s.changes, change{
		kind:       changeRemoveMilestone,
		repository: record.Issue.Repository,
		number:     record.Issue.Number,
		issue:      record.Issue,
	})
}

func pullRequestMilestone(item *github.Issue) int {
	if item == nil || item.Milestone == nil {
		return 0
	}
	return item.Milestone.GetNumber()
}
