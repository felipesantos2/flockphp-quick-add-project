package merge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/google/go-github/v93/github"
)

var mergeQueueRef = regexp.MustCompile(`/pr-(\d+)-[0-9a-f]+$`)

var ErrNotAllowed = errors.New("queued by someone without the admin or maintain role")

type mergeGroupEvent struct {
	HeadRef string
	HeadSHA string
}

// state is one merge-queue run.
// observed is the GitHub picture collected before any write.
// decision is whether the enqueue is allowed.
// changes is applied only after that decision, then a rejection is returned.
type state struct {
	client     *github.Client
	owner      string
	repository string
	event      mergeGroupEvent
	runID      int64
	number     int

	observed struct {
		enqueuer string
		role     string
		cancel   []int64
	}

	decision struct {
		allowed bool
	}
}

func Run(ctx context.Context, client *github.Client, owner, repository string, data []byte, runIDValue string) error {
	current, err := newState(client, owner, repository, data, runIDValue)
	if err != nil {
		return err
	}
	if err := current.observe(ctx); err != nil {
		return err
	}
	current.plan()
	if err := current.apply(ctx); err != nil {
		return err
	}
	if current.decision.allowed {
		return nil
	}
	return fmt.Errorf("%w: @%s has the %q role", ErrNotAllowed, current.observed.enqueuer, current.observed.role)
}

func newState(client *github.Client, owner, repository string, data []byte, runIDValue string) (*state, error) {
	event, err := parseMergeGroupEvent(data)
	if err != nil {
		return nil, fmt.Errorf("parse merge group event: %w", err)
	}
	runID, _ := strconv.ParseInt(runIDValue, 10, 64)
	match := mergeQueueRef.FindStringSubmatch(event.HeadRef)
	if match == nil {
		return nil, fmt.Errorf("not a merge queue ref: %q", event.HeadRef)
	}
	number, err := strconv.Atoi(match[1])
	if err != nil {
		return nil, fmt.Errorf("parse pull request number: %w", err)
	}
	return &state{
		client: client, owner: owner, repository: repository,
		event: event, runID: runID, number: number,
	}, nil
}

func (s *state) observe(ctx context.Context) error {
	enqueuer, err := mergeQueueEnqueuer(ctx, s.client, s.owner, s.repository, s.number)
	if err != nil {
		return err
	}
	permission, _, err := s.client.Repositories.GetPermissionLevel(ctx, s.owner, s.repository, enqueuer)
	if err != nil {
		return fmt.Errorf("read repository role for @%s: %w", enqueuer, err)
	}
	s.observed.enqueuer = enqueuer
	s.observed.role = permission.GetRoleName()
	if s.observed.role == "admin" || s.observed.role == "maintain" {
		return nil
	}
	runs, err := listCancellableRuns(ctx, s.client, s.owner, s.repository, s.event.HeadSHA, s.runID)
	if err != nil {
		return err
	}
	s.observed.cancel = runs
	return nil
}

func (s *state) plan() {
	s.decision.allowed = s.observed.role == "admin" || s.observed.role == "maintain"
}

func (s *state) apply(ctx context.Context) error {
	if s.decision.allowed {
		return nil
	}
	if _, _, err := s.client.Issues.CreateComment(ctx, s.owner, s.repository, s.number, github.IssueCommentRequest{
		Body: fmt.Sprintf("Only a repository admin or maintainer can merge, so this was taken out of the merge queue (queued by @%s). A maintainer will merge it.", s.observed.enqueuer),
	}); err != nil {
		return fmt.Errorf("create merge queue comment on pull request #%d: %w", s.number, err)
	}
	for _, id := range s.observed.cancel {
		if _, err := s.client.Actions.CancelWorkflowRunByID(ctx, s.owner, s.repository, id); err != nil {
			if _, ok := errors.AsType[*github.AcceptedError](err); !ok {
				return fmt.Errorf("cancel run %d: %w", id, err)
			}
		}
	}
	return nil
}

func parseMergeGroupEvent(data []byte) (mergeGroupEvent, error) {
	var event github.MergeGroupEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return mergeGroupEvent{}, err
	}
	mergeGroup := event.GetMergeGroup()
	return mergeGroupEvent{HeadRef: mergeGroup.GetHeadRef(), HeadSHA: mergeGroup.GetHeadSHA()}, nil
}

func mergeQueueEnqueuer(ctx context.Context, client *github.Client, owner, repository string, pullRequestNumber int) (string, error) {
	request, err := client.NewRequest(ctx, "POST", "graphql", map[string]any{
		"query": `query($owner: String!, $repository: String!, $pullRequest: Int!) {
  repository(owner: $owner, name: $repository) {
    pullRequest(number: $pullRequest) {
      mergeQueueEntry { enqueuer { login } }
    }
  }
}`,
		"variables": map[string]any{"owner": owner, "repository": repository, "pullRequest": pullRequestNumber},
	})
	if err != nil {
		return "", fmt.Errorf("build merge queue request for pull request #%d: %w", pullRequestNumber, err)
	}
	var response struct {
		Errors []struct{ Message string }
		Data   struct {
			Repository struct {
				PullRequest struct {
					MergeQueueEntry *struct {
						Enqueuer struct{ Login string }
					}
				}
			}
		}
	}
	if _, err := client.Do(request, &response); err != nil {
		return "", fmt.Errorf("read merge queue enqueuer for pull request #%d: %w", pullRequestNumber, err)
	}
	if len(response.Errors) != 0 {
		return "", fmt.Errorf("read merge queue enqueuer for pull request #%d: %s", pullRequestNumber, response.Errors[0].Message)
	}
	entry := response.Data.Repository.PullRequest.MergeQueueEntry
	if entry == nil || entry.Enqueuer.Login == "" {
		return "", fmt.Errorf("pull request #%d is not in the merge queue", pullRequestNumber)
	}
	return entry.Enqueuer.Login, nil
}

func listCancellableRuns(ctx context.Context, client *github.Client, owner, repository, headSHA string, runID int64) ([]int64, error) {
	options := &github.ListWorkflowRunsOptions{
		Event: "merge_group", HeadSHA: headSHA, ListOptions: github.ListOptions{PerPage: 100},
	}
	var ids []int64
	for {
		runs, response, err := client.Actions.ListRepositoryWorkflowRuns(ctx, owner, repository, options)
		if err != nil {
			return nil, fmt.Errorf("list merge-group workflow runs: %w", err)
		}
		if runs != nil {
			for _, run := range runs.WorkflowRuns {
				if run.GetID() == runID || run.GetStatus() == "completed" {
					continue
				}
				ids = append(ids, run.GetID())
			}
		}
		if response == nil || response.NextPage == 0 {
			return ids, nil
		}
		options.Page = response.NextPage
	}
}
