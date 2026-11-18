// Package pr_gate applies Flock's pull request contribution policy.
package pr_gate

import (
	"fmt"
	"path"
	"strings"
	"time"

	"flock/tools/github/github_service"
	"flock/tools/github/issue_reference"
	"github.com/google/go-github/v93/github"
)

const openToAnyone = "help wanted"

type Service interface {
	PullRequest(owner, repository string, number int) (*github.PullRequest, error)
	Files(owner, repository string, number int) ([]*github.CommitFile, error)
	Issue(owner, repository string, number int) (*github.Issue, error)
	GateClosed(owner, repository string, number int) (bool, error)
	Comment(owner, repository string, number int, body string) error
	SetPullRequestState(owner, repository string, number int, state string) error
}

type Event struct {
	Action     string
	Number     int
	BodyEdited bool
	BaseEdited bool
}

type Result struct {
	ApprovedIssue *issue_reference.Reference
}

type action string

const (
	allow           action = "allow"
	closeWrongBase  action = "close-wrong-base"
	closeUnapproved action = "close-unapproved"
)

func Run(api Service, owner, repository string, event Event) (Result, error) {
	for attempt := 0; ; attempt++ {
		pr, err := api.PullRequest(owner, repository, event.Number)
		if err != nil {
			return Result{}, err
		}
		if bypassesGate(pr.GetUser(), pr.GetAuthorAssociation()) {
			return Result{}, nil
		}
		if pr.GetState() == "closed" {
			if !event.BodyEdited || pr.GetMerged() || pr.GetBase().GetRef() != "main" {
				return Result{}, nil
			}
			closedByGate, err := api.GateClosed(owner, repository, pr.GetNumber())
			if err != nil || !closedByGate {
				return Result{}, err
			}
			if issue, approved := firstApprovedIssue(api, owner, pr); approved {
				if err := api.SetPullRequestState(owner, repository, pr.GetNumber(), "open"); err != nil {
					return Result{}, err
				}
				if err := api.Comment(owner, repository, pr.GetNumber(), fmt.Sprintf("Thanks, @%s! This pull request references approved issue %s, so it has been reopened.", pr.GetUser().GetLogin(), issue)); err != nil {
					return Result{}, err
				}
				return approvedResult(issue), nil
			}
			return Result{}, nil
		}
		if event.Action == "edited" && !event.BaseEdited {
			return Result{}, nil
		}
		if pr.GetBase().GetRef() != "main" {
			return Result{}, closePullRequest(api, owner, repository, pr, closeWrongBase)
		}
		if issue, approved := firstApprovedIssue(api, owner, pr); approved {
			return approvedResult(issue), nil
		}
		files, err := api.Files(owner, repository, pr.GetNumber())
		if err != nil {
			return Result{}, err
		}
		decision := evaluate(files)
		if decision == allow {
			return Result{}, nil
		}
		if decision == closeUnapproved && event.Action == "opened" && attempt < 2 {
			time.Sleep(3 * time.Second)
			continue
		}
		return Result{}, closePullRequest(api, owner, repository, pr, decision)
	}
}

func approvedResult(issue issue_reference.Reference) Result {
	return Result{ApprovedIssue: &issue}
}

func evaluate(files []*github.CommitFile) action {
	if needsNoIssue(files) {
		return allow
	}
	return closeUnapproved
}

func firstApprovedIssue(api Service, owner string, pr *github.PullRequest) (issue_reference.Reference, bool) {
	for _, reference := range issue_reference.Parse(pr.GetBody(), "flock") {
		issue, err := api.Issue(owner, reference.Repository, reference.Number)
		if err == nil && isApproved(issue, pr.GetUser().GetLogin()) {
			return reference, true
		}
	}
	return issue_reference.Reference{}, false
}

func closePullRequest(api Service, owner, repository string, pr *github.PullRequest, decision action) error {
	var reason, nextStep string
	switch decision {
	case closeWrongBase:
		reason = fmt.Sprintf("Pull requests go into `main`, not `%s`, so this one is closed.", pr.GetBase().GetRef())
		nextStep = "Please open a new pull request against `main`."
	case closeUnapproved:
		reason = "Flock takes pull requests only for issues a maintainer has approved, so this one is closed."
		nextStep = "Edit this pull request's description to reference an approved issue, then save it to reopen automatically."
	default:
		return fmt.Errorf("cannot close a pull request for %q", decision)
	}
	comment := fmt.Sprintf("Thanks for the PR, @%s! %s %s See [CONTRIBUTING.md](https://github.com/%s/%s/blob/main/CONTRIBUTING.md).\n\n%s", pr.GetUser().GetLogin(), reason, nextStep, owner, repository, github_service.GateMarker)
	if err := api.Comment(owner, repository, pr.GetNumber(), comment); err != nil {
		return err
	}
	return api.SetPullRequestState(owner, repository, pr.GetNumber(), "closed")
}

func bypassesGate(user *github.User, authorAssociation string) bool {
	return user.GetType() == "Bot" || authorAssociation == "OWNER" || authorAssociation == "MEMBER" || authorAssociation == "COLLABORATOR"
}

// Files a pull request may change without an approved issue: the docs, and the translations in
// lang/ with the files tools/i18n generates from them (CI fails when those don't match lang/).
var (
	openFolders = []string{"docs/", "lang/"}
	openFiles   = []string{
		"macos/Resources/Localizable.xcstrings", "macos/Sources/FlockUI/Languages.swift",
		"windows/Flock.Kit/Strings*.resx", "windows/Flock.Kit/Languages.g.cs", "windows/installer/Strings.iss",
		"linux/po/*.po",
	}
)

func needsNoIssue(files []*github.CommitFile) bool {
	for _, file := range files {
		if !openToChange(file.GetFilename()) || (file.GetPreviousFilename() != "" && !openToChange(file.GetPreviousFilename())) {
			return false
		}
	}
	return true
}

func openToChange(name string) bool {
	for _, folder := range openFolders {
		if strings.HasPrefix(name, folder) {
			return true
		}
	}
	for _, pattern := range openFiles {
		if matched, _ := path.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

func isApproved(issue *github.Issue, author string) bool {
	if issue.IsPullRequest() || issue.GetState() != "open" {
		return false
	}
	for _, label := range issue.Labels {
		if label.GetName() == openToAnyone {
			return true
		}
	}
	for _, assignee := range issue.Assignees {
		if assignee.GetLogin() == author {
			return true
		}
	}
	return false
}
