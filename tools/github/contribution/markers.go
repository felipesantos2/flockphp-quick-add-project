package contribution

import (
	"regexp"

	"github.com/google/go-github/v93/github"
)

var claimRecordMarker = regexp.MustCompile(`^<!-- pr-gate:claim issue=([A-Za-z0-9_.-]+)#([0-9]+) author=([A-Za-z0-9-]+) -->$`)
var issueClaimMarker = regexp.MustCompile(`^<!-- pr-gate:claim pr=([0-9]+) author=([A-Za-z0-9-]+) -->(?:\r?\n|$)`)

func ParseClaimRecord(body string) (IssueReference, string, bool) {
	match := claimRecordMarker.FindStringSubmatch(body)
	if match == nil {
		return IssueReference{}, "", false
	}
	return IssueReference{Repository: match[1], Number: atoi(match[2])}, match[3], true
}

func ParseIssueClaim(body string) (pullRequestNumber int, author string, ok bool) {
	match := issueClaimMarker.FindStringSubmatch(body)
	if match == nil {
		return 0, "", false
	}
	return atoi(match[1]), match[2], true
}

func ClaimsFromComments(comments []*github.IssueComment) []Claim {
	var claims []Claim
	for _, comment := range comments {
		if comment.GetUser().GetLogin() != "github-actions[bot]" {
			continue
		}
		number, author, ok := ParseIssueClaim(comment.GetBody())
		if !ok || number == 0 {
			continue
		}
		claims = append(claims, Claim{PullRequestNumber: number, Author: author, CommentID: comment.GetID()})
	}
	return claims
}

func atoi(value string) int {
	number := 0
	for _, digit := range value {
		number = number*10 + int(digit-'0')
	}
	return number
}
