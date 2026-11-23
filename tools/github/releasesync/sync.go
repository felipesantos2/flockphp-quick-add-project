// Package releasesync assigns release-note pull requests to a release milestone.
package releasesync

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/google/go-github/v93/github"
)

type state struct {
	client    *github.Client
	owner     string
	repo      string
	number    int
	milestone int
	out       io.Writer
	observed  observed
	changes   []change
}

type observed struct {
	release    *github.PullRequest
	candidates []candidate
	unmatched  []string
}

type candidate struct {
	pull  *github.PullRequest
	files []*github.CommitFile
}

type change struct {
	number     int
	unmatched  string
	nonRuntime bool
	already    bool
}

// Run adds the release PR and matching merged PRs to milestone. It does not remove
// other milestone entries. A PR is a match when a release-note commit belongs to it
// and the PR changes a runtime path. Milestone writes are skipped when already applied.
// After those writes succeed, Run returns an error if any release-note commit has no merged PR.
func Run(ctx context.Context, client *github.Client, owner, repo string, number, milestone int, out io.Writer) error {
	current := &state{client: client, owner: owner, repo: repo, number: number, milestone: milestone, out: out}
	if err := current.collect(ctx); err != nil {
		return err
	}
	current.plan()
	return current.apply(ctx)
}

func (s *state) collect(ctx context.Context) error {
	target, _, err := s.client.Issues.GetMilestone(ctx, s.owner, s.repo, s.milestone)
	if err != nil {
		return fmt.Errorf("read milestone #%d: %w", s.milestone, err)
	}
	if target.GetState() != "open" {
		return fmt.Errorf("milestone #%d is not open", s.milestone)
	}
	release, _, err := s.client.PullRequests.Get(ctx, s.owner, s.repo, s.number)
	if err != nil {
		return fmt.Errorf("read release PR #%d: %w", s.number, err)
	}
	if release.GetState() != "open" || release.GetBase().GetSHA() == "" {
		return fmt.Errorf("release PR #%d must be open and have a base commit", s.number)
	}
	s.observed.release = release
	base, hashes, err := releaseNotes(release.GetBody(), s.owner, s.repo)
	if err != nil {
		return err
	}
	commits, err := releaseCommits(ctx, s.client, s.owner, s.repo, base, release.GetBase().GetSHA())
	if err != nil {
		return err
	}
	candidates := map[int]*github.PullRequest{}
	for _, hash := range hashes {
		if !commits[hash] {
			return fmt.Errorf("release-note commit %s is not in the release range", hash)
		}
		opts := &github.ListOptions{PerPage: 100}
		found := false
		for {
			pulls, response, err := s.client.PullRequests.ListPullRequestsWithCommit(ctx, s.owner, s.repo, hash, opts)
			if err != nil {
				return fmt.Errorf("list PRs for commit %s: %w", hash, err)
			}
			for _, pull := range pulls {
				if pull.GetMergedAt().IsZero() || !commits[pull.GetMergeCommitSHA()] ||
					pull.GetBase().GetRef() != release.GetBase().GetRef() {
					continue
				}
				found = true
				candidates[pull.GetNumber()] = pull
			}
			if response.NextPage == 0 {
				break
			}
			opts.Page = response.NextPage
		}
		if !found {
			s.observed.unmatched = append(s.observed.unmatched, hash)
		}
	}
	numbers := make([]int, 0, len(candidates))
	for n := range candidates {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	for _, n := range numbers {
		files, err := listFiles(ctx, s.client, s.owner, s.repo, n)
		if err != nil {
			return err
		}
		s.observed.candidates = append(s.observed.candidates, candidate{pull: candidates[n], files: files})
	}
	return nil
}

func (s *state) plan() {
	for _, hash := range s.observed.unmatched {
		s.changes = append(s.changes, change{unmatched: hash})
	}
	s.planPull(s.observed.release)
	for _, item := range s.observed.candidates {
		runtime := false
		for _, file := range item.files {
			if runtimePath(file.GetFilename()) || runtimePath(file.GetPreviousFilename()) {
				runtime = true
				break
			}
		}
		if runtime {
			s.planPull(item.pull)
			continue
		}
		s.changes = append(s.changes, change{number: item.pull.GetNumber(), nonRuntime: true})
	}
}

func (s *state) planPull(pull *github.PullRequest) {
	s.changes = append(s.changes, change{
		number:  pull.GetNumber(),
		already: pull.GetMilestone().GetNumber() == s.milestone,
	})
}

func (s *state) apply(ctx context.Context) error {
	var unmatched []string
	for _, change := range s.changes {
		if change.unmatched != "" {
			_, _ = fmt.Fprintf(s.out, "No merged PR found for release-note commit %s\n", change.unmatched)
			unmatched = append(unmatched, change.unmatched)
			continue
		}
		if change.nonRuntime {
			_, _ = fmt.Fprintf(s.out, "Skipped non-runtime PR #%d\n", change.number)
			continue
		}
		if change.already {
			_, _ = fmt.Fprintf(s.out, "PR #%d already in milestone #%d\n", change.number, s.milestone)
			continue
		}
		_, _, err := s.client.Issues.Update(ctx, s.owner, s.repo, change.number, github.UpdateIssueRequest{Milestone: &s.milestone})
		if err != nil {
			return fmt.Errorf("set milestone on PR #%d: %w", change.number, err)
		}
		_, _ = fmt.Fprintf(s.out, "Added PR #%d to milestone #%d\n", change.number, s.milestone)
	}
	if len(unmatched) == 0 {
		return nil
	}
	return fmt.Errorf("no merged PR for release-note commits: %s", strings.Join(unmatched, ", "))
}

func releaseNotes(body, owner, repo string) (string, []string, error) {
	prefix := regexp.QuoteMeta("https://github.com/" + owner + "/" + repo + "/")
	comparison := regexp.MustCompile(prefix + `compare/([^/\s)]+?)\.\.\.([^/\s)]+)`).FindStringSubmatch(body)
	if len(comparison) == 0 {
		return "", nil, fmt.Errorf("release notes do not contain a repository comparison link")
	}
	// release-please writes ([shortsha](https://github.com/owner/repo/commit/FULLSHA)).
	// Subjects can link older commits, so bare commit URLs are not release notes.
	matches := regexp.MustCompile(`\(\[[0-9a-fA-F]+\]\(`+prefix+`commit/([0-9a-fA-F]{40})\)\)`).FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	var hashes []string
	for _, match := range matches {
		hash := strings.ToLower(match[1])
		if !seen[hash] {
			seen[hash] = true
			hashes = append(hashes, hash)
		}
	}
	if len(hashes) == 0 {
		return "", nil, fmt.Errorf("release notes contain no full commit links")
	}
	return comparison[1], hashes, nil
}

func releaseCommits(ctx context.Context, client *github.Client, owner, repo, base, head string) (map[string]bool, error) {
	commits := map[string]bool{}
	opts := &github.ListOptions{PerPage: 100}
	for {
		comparison, response, err := client.Repositories.CompareCommits(ctx, owner, repo, base, head, opts)
		if err != nil {
			return nil, fmt.Errorf("compare release range %s...%s: %w", base, head, err)
		}
		if comparison == nil || (comparison.GetStatus() != "ahead" && comparison.GetStatus() != "identical") {
			return nil, fmt.Errorf("release base %s is not an ancestor of %s", base, head)
		}
		for _, commit := range comparison.Commits {
			commits[strings.ToLower(commit.GetSHA())] = true
		}
		if response.NextPage == 0 {
			return commits, nil
		}
		opts.Page = response.NextPage
	}
}

func listFiles(ctx context.Context, client *github.Client, owner, repo string, number int) ([]*github.CommitFile, error) {
	opts := &github.ListOptions{PerPage: 100}
	var result []*github.CommitFile
	for {
		files, response, err := client.PullRequests.ListFiles(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, fmt.Errorf("list files for PR #%d: %w", number, err)
		}
		result = append(result, files...)
		if response.NextPage == 0 {
			return result, nil
		}
		opts.Page = response.NextPage
	}
}

func runtimePath(path string) bool {
	if strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/tests/") ||
		strings.Contains(path, "/Tests/") || strings.Contains(path, ".Tests/") ||
		strings.HasSuffix(path, "Tests.cs") {
		return false
	}
	for _, prefix := range []string{"internal/", "cmd/", "linux/", "macos/", "windows/", "lang/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
