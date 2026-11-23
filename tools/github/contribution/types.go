package contribution

import (
	"fmt"

	"github.com/google/go-github/v93/github"
)

const (
	ClaimedLabel    = "claimed"
	HelpWantedLabel = "help wanted"
	GateMarker      = "<!-- pr-gate:closed -->"
)

type IssueReference struct {
	Repository string
	Number     int
}

func (r IssueReference) String() string {
	return fmt.Sprintf("%s#%d", r.Repository, r.Number)
}

type User struct {
	Login string
	Type  string
}

type Label struct {
	Name string
}

type Issue struct {
	Number            int
	State             string
	IsPullRequest     bool
	Body              string
	User              User
	AuthorAssociation string
	Assignees         []User
	Labels            []Label
}

func (issue *Issue) HasLabel(name string) bool {
	if issue == nil {
		return false
	}
	for _, label := range issue.Labels {
		if label.Name == name {
			return true
		}
	}
	return false
}

func (issue *Issue) HasAssignee(username string) bool {
	if issue == nil {
		return false
	}
	for _, assignee := range issue.Assignees {
		if assignee.Login == username {
			return true
		}
	}
	return false
}

type PullRequest struct {
	Number            int
	State             string
	Merged            bool
	Body              string
	BaseRef           string
	Author            User
	AuthorAssociation string
	Labels            []Label
}

func OtherAssignee(issue *Issue, author string) bool {
	if issue == nil {
		return false
	}
	for _, assignee := range issue.Assignees {
		if assignee.Login != author {
			return true
		}
	}
	return false
}

type File struct {
	Filename         string
	PreviousFilename string
}

type LinkedIssue struct {
	Owner      string
	Repository string
	Number     int
}

type Claim struct {
	PullRequestNumber int
	Author            string
	CommentID         int64
}

type ClaimRecord struct {
	Issue     IssueReference
	Author    string
	CommentID int64
}

func PullRequestFromGitHub(source *github.PullRequest) *PullRequest {
	if source == nil {
		return nil
	}
	return &PullRequest{
		Number:            source.GetNumber(),
		State:             source.GetState(),
		Merged:            source.GetMerged(),
		Body:              source.GetBody(),
		BaseRef:           source.GetBase().GetRef(),
		Author:            User{Login: source.GetUser().GetLogin(), Type: source.GetUser().GetType()},
		AuthorAssociation: source.GetAuthorAssociation(),
		Labels:            labelsFromGitHub(source.Labels),
	}
}

func IssueFromGitHub(source *github.Issue) *Issue {
	if source == nil {
		return nil
	}
	return &Issue{
		Number:            source.GetNumber(),
		State:             source.GetState(),
		IsPullRequest:     source.IsPullRequest(),
		Body:              source.GetBody(),
		User:              User{Login: source.GetUser().GetLogin(), Type: source.GetUser().GetType()},
		AuthorAssociation: source.GetAuthorAssociation(),
		Assignees:         usersFromGitHub(source.Assignees),
		Labels:            labelsFromGitHub(source.Labels),
	}
}

func usersFromGitHub(sources []*github.User) []User {
	result := make([]User, 0, len(sources))
	for _, source := range sources {
		result = append(result, User{Login: source.GetLogin(), Type: source.GetType()})
	}
	return result
}

func labelsFromGitHub(sources []*github.Label) []Label {
	result := make([]Label, 0, len(sources))
	for _, source := range sources {
		result = append(result, Label{Name: source.GetName()})
	}
	return result
}
