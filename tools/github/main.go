// Command github applies Flock's pull request, merge queue and issue-triage policy in GitHub Actions.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/issue"
	"flock/tools/github/merge"
	"flock/tools/github/pr"
	"flock/tools/github/releasesync"
)

func main() {
	if !validCommand(os.Args[1:]) {
		fail(errors.New("usage: github pr | issue triage|assignment | merge | release-sync <pr-number> <milestone-number>"))
	}
	owner, repo, err := parseRepository(os.Getenv("GITHUB_REPOSITORY"))
	if err != nil {
		fail(err)
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fail(errors.New("GITHUB_TOKEN is not set"))
	}
	ctx := context.Background()
	client, err := newGitHubClient(apiURL(), token)
	if err != nil {
		fail(err)
	}
	if os.Args[1] == "release-sync" {
		number, milestone, err := releaseSyncArguments(os.Args[2:])
		if err == nil {
			err = releasesync.Run(ctx, client, owner, repo, number, milestone, os.Stdout)
		}
		if err != nil {
			fail(err)
		}
		return
	}
	event, err := os.ReadFile(os.Getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		fail(err)
	}
	switch os.Args[1] {
	case "pr":
		err = pr.Run(ctx, client, owner, repo, event)
	case "issue":
		if os.Args[2] == "triage" {
			err = issue.Triage(ctx, client, owner, repo, event)
		} else {
			err = issue.Assignment(ctx, client, owner, repo, event)
		}
	case "merge":
		err = merge.Run(ctx, client, owner, repo, event, os.Getenv("GITHUB_RUN_ID"))
	}
	if err != nil {
		fail(err)
	}
}

func validCommand(args []string) bool {
	if len(args) == 3 && args[0] == "release-sync" {
		_, _, err := releaseSyncArguments(args[1:])
		return err == nil
	}
	if len(args) == 1 {
		return args[0] == "pr" || args[0] == "merge"
	}
	return len(args) == 2 && args[0] == "issue" && (args[1] == "triage" || args[1] == "assignment")
}

func releaseSyncArguments(args []string) (int, int, error) {
	if len(args) != 2 {
		return 0, 0, errors.New("release-sync requires a PR number and milestone number")
	}
	number, err := strconv.Atoi(args[0])
	if err != nil || number < 1 {
		return 0, 0, errors.New("release PR number must be a positive integer")
	}
	milestone, err := strconv.Atoi(args[1])
	if err != nil || milestone < 1 {
		return 0, 0, errors.New("milestone number must be a positive integer")
	}
	return number, milestone, nil
}

func newGitHubClient(baseURL, token string) (*github.Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	return github.NewClient(
		github.WithAuthToken(token),
		github.WithURLs(new(parsed.String()), nil),
	)
}

func parseRepository(value string) (string, string, error) {
	owner, name, ok := strings.Cut(value, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("GITHUB_REPOSITORY must be owner/repository, got %q", value)
	}
	return owner, name, nil
}

func apiURL() string {
	if value := os.Getenv("GITHUB_API_URL"); value != "" {
		return value
	}
	return "https://api.github.com"
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "github:", err)
	os.Exit(1)
}
