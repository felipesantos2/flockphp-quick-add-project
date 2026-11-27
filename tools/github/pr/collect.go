package pr

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

func (s *state) collect(ctx context.Context) error {
	if err := s.loadPullRequest(ctx); err != nil {
		return err
	}
	if s.observed.pullRequest == nil || s.observed.pullRequest.Merged {
		return nil
	}
	if err := s.loadRecord(ctx); err != nil {
		return err
	}
	if err := s.loadFiles(ctx); err != nil {
		return err
	}
	if s.reopenCandidate() {
		if err := s.loadGateClosed(ctx); err != nil {
			return err
		}
	}
	if s.observed.pullRequest.State == "open" && s.observed.pullRequest.BaseRef != "main" {
		return s.collectReleaseDetails(ctx)
	}
	if err := s.loadApprovalInputs(ctx); err != nil {
		return err
	}
	if err := s.collectReleaseDetails(ctx); err != nil {
		return err
	}
	if err := s.loadPullRequestMilestone(ctx); err != nil {
		return err
	}
	return s.loadClaimedLabel(ctx)
}

func (s *state) collectRelease(ctx context.Context) error {
	if err := s.loadPullRequest(ctx); err != nil {
		return err
	}
	if s.observed.pullRequest == nil || s.observed.pullRequest.Merged {
		return nil
	}
	if err := s.loadRecord(ctx); err != nil {
		return err
	}
	return s.collectReleaseDetails(ctx)
}

func (s *state) collectReleaseDetails(ctx context.Context) error {
	if s.observed.record == nil {
		return nil
	}
	if err := s.loadLinks(ctx); err != nil {
		return err
	}
	if err := s.loadIssue(ctx, s.observed.record.Issue); err != nil {
		return err
	}
	snapshot := s.issue(s.observed.record.Issue)
	if snapshot == nil || snapshot.issue == nil {
		return nil
	}
	if err := s.loadClaims(ctx, s.observed.record.Issue); err != nil {
		return err
	}
	if contribution.OtherAssignee(snapshot.issue, s.observed.record.Author) {
		return nil
	}
	return s.loadAssignment(ctx, s.observed.record.Issue, s.observed.record.Author)
}

func (s *state) collectGate(ctx context.Context) error {
	if err := s.loadPullRequest(ctx); err != nil {
		return err
	}
	pullRequest := s.observed.pullRequest
	if pullRequest == nil || bypassesGate(pullRequest) || pullRequest.Merged {
		return nil
	}
	if pullRequest.State == "closed" {
		if s.event.action != "edited" || !s.event.bodyEdited || pullRequest.BaseRef != "main" {
			return nil
		}
		if err := s.loadGateClosed(ctx); err != nil {
			return err
		}
		if !s.observed.gateClosed {
			return nil
		}
	} else if pullRequest.BaseRef != "main" {
		return nil
	}
	return s.loadApprovalInputs(ctx)
}

func (s *state) collectClaim(ctx context.Context) error {
	if err := s.loadPullRequest(ctx); err != nil {
		return err
	}
	pullRequest := s.observed.pullRequest
	if pullRequest == nil || pullRequest.State == "closed" || pullRequest.Merged || pullRequest.BaseRef != "main" {
		return nil
	}
	if err := s.loadLinks(ctx); err != nil {
		return err
	}
	if err := s.loadRecord(ctx); err != nil {
		return err
	}
	if err := s.loadLinkedIssues(ctx); err != nil {
		return err
	}
	s.resolve()
	if s.claimTarget() == nil {
		return nil
	}
	return s.loadClaimedLabel(ctx)
}

func (s *state) collectMilestone(ctx context.Context) error {
	if err := s.loadPullRequest(ctx); err != nil {
		return err
	}
	pullRequest := s.observed.pullRequest
	if pullRequest == nil || pullRequest.State != "open" || pullRequest.BaseRef != "main" {
		return nil
	}
	if err := s.loadRecord(ctx); err != nil {
		return err
	}
	if s.observed.record == nil || s.observed.record.Issue.Repository != s.repository {
		return nil
	}
	if err := s.loadIssue(ctx, s.observed.record.Issue); err != nil {
		return err
	}
	return s.loadPullRequestMilestone(ctx)
}

func (s *state) reopenCandidate() bool {
	pullRequest := s.observed.pullRequest
	return pullRequest != nil && pullRequest.State == "closed" && s.event.action == "edited" && s.event.bodyEdited && pullRequest.BaseRef == "main"
}

func (s *state) loadPullRequest(ctx context.Context) error {
	if s.loaded.pullRequest {
		return nil
	}
	item, err := readPullRequest(ctx, s.client, s.owner, s.repository, s.event.number)
	if err != nil {
		return err
	}
	s.observed.pullRequest = item
	s.loaded.pullRequest = true
	return nil
}

func (s *state) loadFiles(ctx context.Context) error {
	if s.loaded.files {
		return nil
	}
	files, err := listAllFiles(ctx, s.client, s.owner, s.repository, s.event.number)
	if err != nil {
		return err
	}
	s.observed.files = files
	s.loaded.files = true
	return nil
}

func (s *state) loadLinks(ctx context.Context) error {
	if s.loaded.links {
		return nil
	}
	return s.reloadLinks(ctx)
}

func (s *state) reloadLinks(ctx context.Context) error {
	links, err := listClosingReferences(ctx, s.client, s.owner, s.repository, s.event.number)
	if err != nil {
		return err
	}
	s.observed.links = links
	s.loaded.links = true
	return nil
}

func (s *state) loadRecord(ctx context.Context) error {
	if s.loaded.record {
		return nil
	}
	record, err := readClaimRecord(ctx, s.client, s.owner, s.repository, s.event.number)
	if err != nil {
		return err
	}
	s.observed.record = record
	s.loaded.record = true
	return nil
}

func (s *state) loadGateClosed(ctx context.Context) error {
	if s.loaded.gateClosed {
		return nil
	}
	comments, err := listAllComments(ctx, s.client, s.owner, s.repository, s.event.number)
	if err != nil {
		return fmt.Errorf("read gate comments for pull request #%d: %w", s.event.number, err)
	}
	events, err := listAllEvents(ctx, s.client, s.owner, s.repository, s.event.number)
	if err != nil {
		return fmt.Errorf("read gate events for pull request #%d: %w", s.event.number, err)
	}
	s.observed.gateClosed = closedByGate(latestGateMarker(comments), events)
	s.loaded.gateClosed = true
	return nil
}

func (s *state) loadApprovalInputs(ctx context.Context) error {
	if err := s.loadFiles(ctx); err != nil {
		return err
	}
	exempt := exemptFilesOnly(s.observed.files)
	for attempt := 0; ; attempt++ {
		if err := s.reloadLinks(ctx); err != nil {
			return err
		}
		if err := s.loadLinkedIssues(ctx); err != nil {
			return err
		}
		approved, _ := s.approval()
		if approved != nil || exempt || attempt >= 2 || (s.event.action != "opened" && !(s.event.action == "edited" && s.event.bodyEdited)) {
			return nil
		}
		if err := wait(ctx, gateRetryDelay); err != nil {
			return err
		}
	}
}

func (s *state) loadLinkedIssues(ctx context.Context) error {
	for _, reference := range s.observed.links {
		if reference.Owner != s.owner || reference.Repository != s.repository {
			continue
		}
		key := contribution.IssueReference{Repository: s.repository, Number: reference.Number}
		if err := s.loadIssue(ctx, key); err != nil {
			return err
		}
		if err := s.loadClaims(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) loadIssue(ctx context.Context, reference contribution.IssueReference) error {
	if s.observed.issues == nil {
		s.observed.issues = map[string]*issueSnapshot{}
	}
	if _, ok := s.observed.issues[reference.String()]; ok {
		return nil
	}
	item, _, err := s.client.Issues.Get(ctx, s.owner, reference.Repository, reference.Number)
	if contribution.IsNotFound(err) {
		s.observed.issues[reference.String()] = &issueSnapshot{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read issue %s: %w", reference, err)
	}
	snapshot := &issueSnapshot{issue: contribution.IssueFromGitHub(item)}
	if item != nil && item.Milestone != nil {
		snapshot.milestone = item.Milestone.GetNumber()
	}
	s.observed.issues[reference.String()] = snapshot
	return nil
}

func (s *state) loadClaims(ctx context.Context, reference contribution.IssueReference) error {
	snapshot := s.issue(reference)
	if snapshot == nil || snapshot.issue == nil || snapshot.claims != nil {
		return nil
	}
	claims, err := readIssueClaims(ctx, s.client, s.owner, reference)
	if err != nil {
		return err
	}
	if claims == nil {
		claims = []contribution.Claim{}
	}
	snapshot.claims = claims
	return nil
}

func (s *state) loadAssignment(ctx context.Context, reference contribution.IssueReference, author string) error {
	snapshot := s.issue(reference)
	if snapshot == nil || snapshot.assignment {
		return nil
	}
	assigner, assigned, err := readLatestAssignment(ctx, s.client, s.owner, reference, author)
	if err != nil {
		return err
	}
	snapshot.assigner = assigner
	snapshot.assigned = assigned
	snapshot.assignment = true
	return nil
}

func (s *state) loadPullRequestMilestone(ctx context.Context) error {
	if s.loaded.pullRequestMilestone {
		return nil
	}
	item, err := readPullRequestIssue(ctx, s.client, s.owner, s.repository, s.event.number)
	if err != nil {
		return err
	}
	s.observed.pullRequestMilestone = pullRequestMilestone(item)
	s.loaded.pullRequestMilestone = true
	return nil
}

func (s *state) loadClaimedLabel(ctx context.Context) error {
	if s.loaded.claimedLabel {
		return nil
	}
	_, _, err := s.client.Issues.GetLabel(ctx, s.owner, s.repository, contribution.ClaimedLabel)
	if contribution.IsNotFound(err) {
		s.loaded.claimedLabel = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %q label in repository %s/%s: %w", contribution.ClaimedLabel, s.owner, s.repository, err)
	}
	s.observed.claimedLabelExists = true
	s.loaded.claimedLabel = true
	return nil
}

func latestGateMarker(comments []*github.IssueComment) time.Time {
	var latest time.Time
	for _, comment := range comments {
		if comment.GetUser().GetLogin() == "github-actions[bot]" && strings.Contains(comment.GetBody(), contribution.GateMarker) &&
			comment.GetCreatedAt().After(latest) {
			latest = comment.GetCreatedAt().Time
		}
	}
	return latest
}

func closedByGate(marker time.Time, events []*github.IssueEvent) bool {
	if marker.IsZero() {
		return false
	}
	var latestTransition *github.IssueEvent
	var latestReopen time.Time
	for _, event := range events {
		if event.GetEvent() == "reopened" && event.GetCreatedAt().After(latestReopen) {
			latestReopen = event.GetCreatedAt().Time
		}
		if (event.GetEvent() == "closed" || event.GetEvent() == "reopened") &&
			(latestTransition == nil || !event.GetCreatedAt().Before(latestTransition.GetCreatedAt().Time)) {
			latestTransition = event
		}
	}
	return latestTransition != nil &&
		latestTransition.GetEvent() == "closed" &&
		latestTransition.GetActor().GetLogin() == "github-actions[bot]" &&
		!marker.Before(latestReopen) &&
		!marker.After(latestTransition.GetCreatedAt().Time)
}
