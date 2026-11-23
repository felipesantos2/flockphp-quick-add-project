package pr

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

const gateRetryDelay = 3 * time.Second

func Gate(ctx context.Context, client *github.Client, owner, repository string, eventData []byte) error {
	current, err := newState(client, owner, repository, eventData)
	if err != nil {
		return err
	}
	if current.event.number == 0 {
		return nil
	}
	if err := current.collectGate(ctx); err != nil {
		return err
	}
	current.planGate()
	return current.apply(ctx)
}

func (s *state) planGate() {
	pullRequest := s.observed.pullRequest
	if pullRequest == nil || bypassesGate(pullRequest) || pullRequest.Merged {
		return
	}
	if pullRequest.State == "closed" {
		if s.event.action != "edited" || !s.event.bodyEdited || pullRequest.BaseRef != "main" || !s.observed.gateClosed {
			return
		}
		s.decision.reopen = true
	} else if pullRequest.BaseRef != "main" {
		s.planClose(nil)
		return
	}
	s.resolve()
	if exemptFilesOnly(s.observed.files) {
		if s.decision.reopen {
			s.planReopen(s.reopenBody())
		}
		return
	}
	if s.decision.approved != nil {
		if s.decision.reopen {
			s.planReopen(s.reopenBody())
		}
		return
	}
	if s.decision.reopen {
		return
	}
	s.planClose(s.decision.denied)
}

func (s *state) reopenBody() string {
	author := s.observed.pullRequest.Author.Login
	if s.decision.approved != nil {
		return fmt.Sprintf("Thanks, @%s! This pull request references approved issue %s, so it has been reopened.", author, s.decision.approved)
	}
	return fmt.Sprintf("Thanks, @%s! This pull request only changes files in docs/ or lang/, so it has been reopened.", author)
}

func (s *state) planClose(denied *closeReason) {
	pullRequest := s.observed.pullRequest
	if pullRequest == nil || pullRequest.State == "closed" {
		return
	}
	s.changes = append(s.changes, change{
		kind:       changeComment,
		repository: s.repository,
		number:     pullRequest.Number,
		body:       closeComment(s.owner, s.repository, pullRequest, denied),
	}, change{
		kind:      changePullState,
		number:    pullRequest.Number,
		pullState: "closed",
	})
	pullRequest.State = "closed"
}

func (s *state) planReopen(body string) {
	s.changes = append(s.changes, change{
		kind:      changePullState,
		number:    s.event.number,
		pullState: "open",
	}, change{
		kind:       changeComment,
		repository: s.repository,
		number:     s.event.number,
		body:       body,
	})
	s.observed.pullRequest.State = "open"
}

func bypassesGate(pullRequest *contribution.PullRequest) bool {
	if pullRequest == nil {
		return true
	}
	return pullRequest.Author.Type == "Bot" || pullRequest.AuthorAssociation == "OWNER" ||
		pullRequest.AuthorAssociation == "MEMBER" || pullRequest.AuthorAssociation == "COLLABORATOR"
}

func exemptFilesOnly(files []contribution.File) bool {
	if len(files) == 0 {
		return false
	}
	for _, file := range files {
		if !hasExemptPrefix(file.Filename) || (file.PreviousFilename != "" && !hasExemptPrefix(file.PreviousFilename)) {
			return false
		}
	}
	return true
}

func hasExemptPrefix(filename string) bool {
	return strings.HasPrefix(filename, "docs/") || strings.HasPrefix(filename, "lang/")
}

func closeComment(owner, repository string, pullRequest *contribution.PullRequest, denied *closeReason) string {
	var body string
	if pullRequest.BaseRef != "main" {
		body = fmt.Sprintf("Thanks for the PR, @%s! Pull requests go into `main`, not `%s`, so this one is closed. Please open a new pull request against `main`. See [CONTRIBUTING.md](https://github.com/%s/%s/blob/main/CONTRIBUTING.md).",
			pullRequest.Author.Login, pullRequest.BaseRef, owner, repository)
	} else if denied != nil {
		body = fmt.Sprintf("Thanks for the PR, @%s! Issue %s is assigned to %s, so this pull request is closed. Edit the description to link an approved issue using `Fixes #NNN` or `Closes #NNN`, then save it to reopen automatically. See [CONTRIBUTING.md](https://github.com/%s/%s/blob/main/CONTRIBUTING.md).",
			pullRequest.Author.Login, denied.Issue, denied.Names, owner, repository)
	} else {
		body = fmt.Sprintf("Thanks for the PR, @%s! Flock takes pull requests only for issues a maintainer has approved, so this one is closed. Edit the description to link an approved issue using `Fixes #NNN` or `Closes #NNN`, then save it to reopen automatically. See [CONTRIBUTING.md](https://github.com/%s/%s/blob/main/CONTRIBUTING.md).",
			pullRequest.Author.Login, owner, repository)
	}
	return body + "\n\n" + contribution.GateMarker
}

func closePullRequest(ctx context.Context, client *github.Client, owner, repository string, pullRequest *contribution.PullRequest, denied *closeReason) error {
	if pullRequest.State == "closed" {
		return nil
	}
	if err := createComment(ctx, client, owner, repository, pullRequest.Number, closeComment(owner, repository, pullRequest, denied)); err != nil {
		return err
	}
	return setPullRequestState(ctx, client, owner, repository, pullRequest.Number, "closed")
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
