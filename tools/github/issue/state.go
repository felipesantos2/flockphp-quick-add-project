package issue

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

// state is one issue run.
// observed is the GitHub picture collected before any write.
// decision is what the step concludes from that picture.
// changes is applied to GitHub only after the decision is finished.
type state struct {
	client     *github.Client
	owner      string
	repository string
	event      issueEvent

	observed observed
	decision decision
	changes  []labelChange
}

type observed struct {
	permission string
	issue      *contribution.Issue
	claims     []contribution.Claim
}

type decision struct {
	triage triageDecision
}

type labelChange struct {
	add   bool
	label string
}

func newState(client *github.Client, owner, repository string, data []byte) (*state, error) {
	event, err := parseIssueEvent(data)
	if err != nil {
		return nil, fmt.Errorf("parse issue event: %w", err)
	}
	return &state{client: client, owner: owner, repository: repository, event: event}, nil
}

func (s *state) apply(ctx context.Context) error {
	if s.decision.triage != triageNeedsTriage && s.decision.triage != triageBypass {
		return fmt.Errorf("unknown triage decision %d", s.decision.triage)
	}
	for _, change := range s.changes {
		if change.add {
			if _, _, err := s.client.Issues.AddLabelsToIssue(ctx, s.owner, s.repository, s.event.issue.Number, []string{change.label}); err != nil {
				return fmt.Errorf("add %q label to issue #%d: %w", change.label, s.event.issue.Number, err)
			}
			continue
		}
		if _, err := s.client.Issues.RemoveLabelForIssue(ctx, s.owner, s.repository, s.event.issue.Number, change.label); err != nil &&
			!contribution.IsNotFound(err) {
			return fmt.Errorf("remove %q label from issue #%d: %w", change.label, s.event.issue.Number, err)
		}
	}
	return nil
}
