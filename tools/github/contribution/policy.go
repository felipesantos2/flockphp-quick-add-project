package contribution

import (
	"errors"
	"net/http"

	"github.com/google/go-github/v93/github"
)

func IsNotFound(err error) bool {
	var response *github.ErrorResponse
	return errors.As(err, &response) && response.Response != nil && response.Response.StatusCode == http.StatusNotFound
}

func HasAssignedClaimAuthor(item *Issue, claims []Claim) bool {
	if item == nil {
		return false
	}
	for _, claim := range claims {
		if item.HasAssignee(claim.Author) {
			return true
		}
	}
	return false
}

func IssueApprovalFor(item *Issue, author string) bool {
	if item == nil || item.IsPullRequest || item.State != "open" {
		return false
	}
	if len(item.Assignees) != 0 {
		return item.HasAssignee(author)
	}
	return item.HasLabel(HelpWantedLabel)
}
