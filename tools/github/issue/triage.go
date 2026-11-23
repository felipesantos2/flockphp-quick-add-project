package issue

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

type triageDecision uint8

const (
	triageBypass triageDecision = iota
	triageNeedsTriage
)

func triageDecisionFor(item contribution.Issue, permission string) triageDecision {
	if item.User.Type == "Bot" || item.AuthorAssociation == "OWNER" || item.AuthorAssociation == "MEMBER" ||
		item.AuthorAssociation == "COLLABORATOR" || permission == "admin" || permission == "write" {
		return triageBypass
	}
	return triageNeedsTriage
}

func Triage(ctx context.Context, client *github.Client, owner, repository string, data []byte) error {
	current, err := newState(client, owner, repository, data)
	if err != nil {
		return err
	}
	if err := current.observeTriage(ctx); err != nil {
		return err
	}
	current.planTriage()
	return current.apply(ctx)
}

func (s *state) observeTriage(ctx context.Context) error {
	if s.event.action != "opened" || s.event.issue.Number == 0 {
		return nil
	}
	item, _, err := s.client.Issues.Get(ctx, s.owner, s.repository, s.event.issue.Number)
	if contribution.IsNotFound(err) {
		s.event.issue = contribution.Issue{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read issue #%d: %w", s.event.issue.Number, err)
	}
	current := contribution.IssueFromGitHub(item)
	if current == nil {
		return nil
	}
	s.event.issue = *current
	permission, _, err := s.client.Repositories.GetPermissionLevel(ctx, s.owner, s.repository, s.event.issue.User.Login)
	if err != nil {
		return fmt.Errorf("read permission for @%s: %w", s.event.issue.User.Login, err)
	}
	s.observed.permission = permission.GetPermission()
	return nil
}

func (s *state) planTriage() {
	if s.event.action != "opened" || s.event.issue.Number == 0 {
		return
	}
	for _, label := range issuePlatforms(s.event.issue.Body) {
		s.changes = append(s.changes, labelChange{add: true, label: label})
	}
	s.decision.triage = triageDecisionFor(s.event.issue, s.observed.permission)
	if s.decision.triage == triageNeedsTriage {
		s.changes = append(s.changes, labelChange{add: true, label: "needs triage"})
	}
}

var platformLabels = map[string]string{
	"macOS":   "os: mac",
	"Windows": "os: windows",
	"Linux":   "os: linux",
}

func issuePlatforms(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) != "### Platform" {
			continue
		}
		for _, answer := range lines[index+1:] {
			answer = strings.TrimSpace(answer)
			if answer == "" {
				continue
			}
			var labels []string
			for platform := range strings.SplitSeq(answer, ",") {
				if label, ok := platformLabels[strings.TrimSpace(platform)]; ok {
					labels = append(labels, label)
				}
			}
			return labels
		}
	}
	return nil
}

func applyTriageDecision(ctx context.Context, client *github.Client, owner, repository string, item contribution.Issue, chosen triageDecision) error {
	current := &state{
		client:     client,
		owner:      owner,
		repository: repository,
		event:      issueEvent{issue: item},
		decision:   decision{triage: chosen},
	}
	if chosen == triageNeedsTriage {
		current.changes = []labelChange{{add: true, label: "needs triage"}}
	}
	return current.apply(ctx)
}
