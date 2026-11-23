package pr

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

type pullRequestEvent struct {
	action     string
	number     int
	bodyEdited bool
}

type closeReason struct {
	Issue string
	Names string
}

func parsePullRequestEvent(data []byte) (pullRequestEvent, error) {
	var event github.PullRequestEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return pullRequestEvent{}, err
	}
	changes := event.GetChanges()
	pullRequest := event.GetPullRequest()
	return pullRequestEvent{
		action:     event.GetAction(),
		number:     pullRequest.GetNumber(),
		bodyEdited: changes != nil && changes.Body != nil,
	}, nil
}

func readPullRequest(ctx context.Context, client *github.Client, owner, repository string, number int) (*contribution.PullRequest, error) {
	item, _, err := client.PullRequests.Get(ctx, owner, repository, number)
	if contribution.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pull request #%d: %w", number, err)
	}
	return contribution.PullRequestFromGitHub(item), nil
}

func readPullRequestIssue(ctx context.Context, client *github.Client, owner, repository string, number int) (*github.Issue, error) {
	item, _, err := client.Issues.Get(ctx, owner, repository, number)
	if contribution.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pull request issue #%d: %w", number, err)
	}
	return item, nil
}

func readIssue(ctx context.Context, client *github.Client, owner string, reference contribution.IssueReference) (*contribution.Issue, error) {
	item, _, err := client.Issues.Get(ctx, owner, reference.Repository, reference.Number)
	if contribution.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read issue %s: %w", reference, err)
	}
	return contribution.IssueFromGitHub(item), nil
}

func listClosingReferences(ctx context.Context, client *github.Client, owner, repository string, number int) ([]contribution.LinkedIssue, error) {
	var cursor *string
	var issues []contribution.LinkedIssue
	for {
		request, err := client.NewRequest(ctx, "POST", "graphql", map[string]any{
			"query": `query($owner: String!, $repository: String!, $pullRequest: Int!, $cursor: String) {
  repository(owner: $owner, name: $repository) {
    pullRequest(number: $pullRequest) {
      closingIssuesReferences(first: 100, after: $cursor) {
       nodes { number repository { name owner { login } } }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`,
			"variables": map[string]any{
				"owner": owner, "repository": repository, "pullRequest": number, "cursor": cursor,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("build linked issues request for pull request #%d: %w", number, err)
		}
		var response struct {
			Errors []struct{ Message string }
			Data   struct {
				Repository struct {
					PullRequest struct {
						ClosingIssuesReferences struct {
							Nodes []struct {
								Number     int
								Repository struct {
									Name  string
									Owner struct{ Login string }
								}
							}
							PageInfo struct {
								HasNextPage bool
								EndCursor   string
							}
						}
					}
				}
			}
		}
		if _, err := client.Do(request, &response); err != nil {
			return nil, fmt.Errorf("list linked issues for pull request #%d: %w", number, err)
		}
		if len(response.Errors) != 0 {
			return nil, fmt.Errorf("list linked issues for pull request #%d: %s", number, response.Errors[0].Message)
		}
		current := response.Data.Repository.PullRequest.ClosingIssuesReferences
		for _, item := range current.Nodes {
			linked := contribution.LinkedIssue{
				Owner: item.Repository.Owner.Login, Repository: item.Repository.Name, Number: item.Number,
			}
			issues = append(issues, linked)
		}
		if !current.PageInfo.HasNextPage {
			return issues, nil
		}
		next := current.PageInfo.EndCursor
		if next == "" || (cursor != nil && *cursor == next) {
			return nil, fmt.Errorf("list linked issues for pull request #%d: page cursor did not advance", number)
		}
		cursor = &next
	}
}

func listAllComments(ctx context.Context, client *github.Client, owner, repository string, number int) ([]*github.IssueComment, error) {
	options := &github.IssueListCommentsOptions{PerPage: 100}
	var result []*github.IssueComment
	for {
		items, response, err := client.Issues.ListComments(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if response.NextPage == 0 {
			return result, nil
		}
		options.Page = response.NextPage
	}
}

func listAllEvents(ctx context.Context, client *github.Client, owner, repository string, number int) ([]*github.IssueEvent, error) {
	options := &github.ListOptions{PerPage: 100}
	var result []*github.IssueEvent
	for {
		items, response, err := client.Issues.ListIssueEvents(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if response.NextPage == 0 {
			return result, nil
		}
		options.Page = response.NextPage
	}
}

func listAllFiles(ctx context.Context, client *github.Client, owner, repository string, number int) ([]contribution.File, error) {
	options := &github.ListOptions{PerPage: 100}
	var result []contribution.File
	for {
		items, response, err := client.PullRequests.ListFiles(ctx, owner, repository, number, options)
		if err != nil {
			return nil, fmt.Errorf("list files for pull request #%d: %w", number, err)
		}
		for _, item := range items {
			result = append(result, contribution.File{Filename: item.GetFilename(), PreviousFilename: item.GetPreviousFilename()})
		}
		if response.NextPage == 0 {
			return result, nil
		}
		options.Page = response.NextPage
	}
}

func readClaimRecord(ctx context.Context, client *github.Client, owner, repository string, number int) (*contribution.ClaimRecord, error) {
	comments, err := listAllComments(ctx, client, owner, repository, number)
	if err != nil {
		return nil, fmt.Errorf("read claim record for pull request #%d: %w", number, err)
	}
	var record *contribution.ClaimRecord
	for _, comment := range comments {
		if comment.GetUser().GetLogin() != "github-actions[bot]" {
			continue
		}
		reference, author, ok := contribution.ParseClaimRecord(comment.GetBody())
		if !ok || reference.Number == 0 || (record != nil && comment.GetID() <= record.CommentID) {
			continue
		}
		record = &contribution.ClaimRecord{Issue: reference, Author: author, CommentID: comment.GetID()}
	}
	return record, nil
}

func readIssueClaims(ctx context.Context, client *github.Client, owner string, reference contribution.IssueReference) ([]contribution.Claim, error) {
	comments, err := listAllComments(ctx, client, owner, reference.Repository, reference.Number)
	if err != nil {
		return nil, fmt.Errorf("read issue claims for %s: %w", reference, err)
	}
	return contribution.ClaimsFromComments(comments), nil
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func readLatestAssignment(ctx context.Context, client *github.Client, owner string, reference contribution.IssueReference, author string) (string, bool, error) {
	options := &github.ListOptions{PerPage: 100}
	var latest time.Time
	assigner := ""
	assigned := false
	for {
		events, response, err := client.Issues.ListIssueEvents(ctx, owner, reference.Repository, reference.Number, options)
		if err != nil {
			return "", false, fmt.Errorf("read latest assignment for issue %s: %w", reference, err)
		}
		for _, event := range events {
			if (event.GetEvent() == "assigned" || event.GetEvent() == "unassigned") && event.GetAssignee().GetLogin() == author &&
				(latest.IsZero() || event.GetCreatedAt().After(latest)) {
				latest = event.GetCreatedAt().Time
				assigner = event.GetAssigner().GetLogin()
				assigned = event.GetEvent() == "assigned"
			}
		}
		if response.NextPage == 0 {
			return assigner, assigned, nil
		}
		options.Page = response.NextPage
	}
}

func createComment(ctx context.Context, client *github.Client, owner, repository string, number int, body string) error {
	_, _, err := client.Issues.CreateComment(ctx, owner, repository, number, github.IssueCommentRequest{Body: body})
	if err != nil {
		return fmt.Errorf("create comment on pull request #%d: %w", number, err)
	}
	return nil
}

func setPullRequestState(ctx context.Context, client *github.Client, owner, repository string, number int, state string) error {
	_, _, err := client.PullRequests.Edit(ctx, owner, repository, number, &github.PullRequest{State: &state})
	if err != nil {
		return fmt.Errorf("set pull request #%d state to %s: %w", number, state, err)
	}
	return nil
}

func deleteComment(ctx context.Context, client *github.Client, owner, repository string, number int64) error {
	_, err := client.Issues.DeleteComment(ctx, owner, repository, number)
	return err
}
