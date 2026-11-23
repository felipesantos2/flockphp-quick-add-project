package issue

import (
	"encoding/json"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

type issueEvent struct {
	action string
	issue  contribution.Issue
}

func parseIssueEvent(data []byte) (issueEvent, error) {
	var event github.IssuesEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return issueEvent{}, err
	}
	item := contribution.IssueFromGitHub(event.GetIssue())
	if item == nil {
		return issueEvent{action: event.GetAction()}, nil
	}
	return issueEvent{action: event.GetAction(), issue: *item}, nil
}
