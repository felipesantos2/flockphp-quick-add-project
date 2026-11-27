package pr

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

// state is one pull-request run.
// observed is the GitHub picture collected before any write.
// decision is what the steps conclude from that picture.
// changes is applied to GitHub only after every step has finished.
type state struct {
	client     *github.Client
	owner      string
	repository string
	event      pullRequestEvent
	out        io.Writer

	observed observed
	decision decision
	changes  []change
	loaded   loaded
}

type observed struct {
	pullRequest          *contribution.PullRequest
	files                []contribution.File
	links                []contribution.LinkedIssue
	record               *contribution.ClaimRecord
	issues               map[string]*issueSnapshot
	pullRequestMilestone int
	claimedLabelExists   bool
	gateClosed           bool
}

type issueSnapshot struct {
	issue      *contribution.Issue
	milestone  int
	claims     []contribution.Claim
	assigner   string
	assigned   bool
	assignment bool
}

type decision struct {
	approved *contribution.IssueReference
	denied   *closeReason
	reopen   bool
	made     bool
}

type loaded struct {
	pullRequest          bool
	files                bool
	links                bool
	record               bool
	gateClosed           bool
	claimedLabel         bool
	pullRequestMilestone bool
}

func Run(ctx context.Context, client *github.Client, owner, repository string, eventData []byte, out io.Writer) error {
	current, err := newState(client, owner, repository, eventData)
	if err != nil {
		return err
	}
	if out != nil {
		current.out = out
	}
	return current.run(ctx)
}

func newState(client *github.Client, owner, repository string, eventData []byte) (*state, error) {
	event, err := parsePullRequestEvent(eventData)
	if err != nil {
		return nil, fmt.Errorf("parse pull request event: %w", err)
	}
	return &state{
		client:     client,
		owner:      owner,
		repository: repository,
		event:      event,
		out:        io.Discard,
		observed:   observed{issues: map[string]*issueSnapshot{}},
	}, nil
}

func (s *state) run(ctx context.Context) error {
	if s.event.number == 0 {
		return nil
	}
	s.status("Checking PR #%d for %s.", s.event.number, s.event.action)
	if err := s.collect(ctx); err != nil {
		return err
	}
	if s.observed.pullRequest == nil {
		s.status("PR #%d no longer exists.", s.event.number)
		return nil
	}
	s.status("PR #%d is %s, targets %s, and was opened by @%s.", s.event.number, s.observed.pullRequest.State,
		s.observed.pullRequest.BaseRef, s.observed.pullRequest.Author.Login)
	s.plan()
	if len(s.changes) == 0 {
		s.status("PR #%d needs no changes.", s.event.number)
	}
	return s.apply(ctx)
}

func (s *state) plan() {
	if s.observed.pullRequest == nil || s.observed.pullRequest.Merged {
		return
	}
	if bypassesGate(s.observed.pullRequest) {
		s.status("PR #%d bypasses contributor gate enforcement.", s.event.number)
		s.resolve()
	}
	s.planRelease()
	if !bypassesGate(s.observed.pullRequest) {
		s.planGate()
		s.planRelease()
	}
	s.planClaim()
	s.planMilestone()
}

func (s *state) issue(reference contribution.IssueReference) *issueSnapshot {
	if s.observed.issues == nil {
		return nil
	}
	return s.observed.issues[reference.String()]
}

func (s *state) resolve() {
	if s.decision.made {
		return
	}
	s.decision.approved, s.decision.denied = s.approval()
	s.decision.made = true
	if s.decision.approved != nil {
		s.status("PR #%d is approved through issue %s.", s.event.number, s.decision.approved)
		return
	}
	if s.decision.denied != nil {
		s.status("PR #%d cannot use issue %s because it is assigned to %s.", s.event.number, s.decision.denied.Issue,
			s.decision.denied.Names)
		return
	}
	s.status("PR #%d has no approved linked issue.", s.event.number)
}

func (s *state) approval() (*contribution.IssueReference, *closeReason) {
	var denied *closeReason
	for _, reference := range s.observed.links {
		if reference.Owner != s.owner || reference.Repository != s.repository {
			continue
		}
		key := contribution.IssueReference{Repository: s.repository, Number: reference.Number}
		snapshot := s.issue(key)
		if snapshot == nil || snapshot.issue == nil {
			continue
		}
		var foreignAuthors []string
		hasOwnClaim := false
		for _, claim := range snapshot.claims {
			if claim.PullRequestNumber != s.event.number {
				foreignAuthors = appendUnique(foreignAuthors, "@"+claim.Author)
			} else if s.observed.pullRequest != nil && strings.EqualFold(claim.Author, s.observed.pullRequest.Author.Login) {
				hasOwnClaim = true
			}
		}
		author := ""
		if s.observed.pullRequest != nil {
			author = s.observed.pullRequest.Author.Login
		}
		approved := contribution.IssueApprovalFor(snapshot.issue, author)
		if snapshot.issue.State == "open" && !snapshot.issue.IsPullRequest && len(snapshot.issue.Assignees) == 0 &&
			snapshot.issue.HasLabel(contribution.ClaimedLabel) && hasOwnClaim {
			approved = true
		}
		if approved && (len(snapshot.issue.Assignees) != 0 || len(foreignAuthors) == 0) {
			result := key
			return &result, nil
		}
		var names []string
		for _, assignee := range snapshot.issue.Assignees {
			if assignee.Login != author {
				names = appendUnique(names, "@"+assignee.Login)
			}
		}
		if len(names) != 0 && denied == nil {
			denied = &closeReason{Issue: fmt.Sprintf("%s#%d", s.repository, reference.Number), Names: joinNames(names)}
		}
	}
	return nil, denied
}

func joinNames(names []string) string {
	return strings.Join(names, " and ")
}

func (s *state) status(format string, arguments ...any) {
	if s.out == nil {
		return
	}
	_, _ = fmt.Fprintf(s.out, format+"\n", arguments...)
}
