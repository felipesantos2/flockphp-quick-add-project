package pr_gate

import (
	"testing"

	"github.com/google/go-github/v93/github"
)

func TestRunReopensOnlyGateClosedPullRequests(t *testing.T) {
	tests := []struct {
		name       string
		gateClosed bool
		wantState  string
	}{
		{name: "reopens gate-closed pull request", gateClosed: true, wantState: "open"},
		{name: "does not reopen manually closed pull request", gateClosed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeService{
				pullRequest: pullRequest("closed", "Fixes #73"),
				gateClosed:  tt.gateClosed,
				issue:       approvedIssue(),
			}
			result, err := Run(api, "flock-php", "flock", Event{Action: "edited", Number: 87, BodyEdited: true})
			if err != nil {
				t.Fatal(err)
			}
			if api.state != tt.wantState {
				t.Fatalf("state = %q, want %q", api.state, tt.wantState)
			}
			if tt.gateClosed && (result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73) {
				t.Fatalf("approved issue = %+v, want flock#73", result.ApprovedIssue)
			}
		})
	}
}

func TestRunClosesUnapprovedPullRequest(t *testing.T) {
	api := &fakeService{pullRequest: pullRequest("open", "Fixes #73"), files: []*github.CommitFile{{Filename: new("internal/site.go")}}, issue: &github.Issue{State: new("open")}}
	if _, err := Run(api, "flock-php", "flock", Event{Action: "synchronize", Number: 87}); err != nil {
		t.Fatal(err)
	}
	if api.state != "closed" || !api.commented {
		t.Fatalf("state = %q, commented = %t", api.state, api.commented)
	}
}

func TestRunDoesNotListFilesForApprovedIssue(t *testing.T) {
	api := &fakeService{pullRequest: pullRequest("open", "Fixes #73"), issue: approvedIssue()}
	result, err := Run(api, "flock-php", "flock", Event{Action: "opened", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73 {
		t.Fatalf("approved issue = %+v, want flock#73", result.ApprovedIssue)
	}
	if api.filesCalls != 0 {
		t.Fatalf("Files() calls = %d, want 0", api.filesCalls)
	}
}

func TestRunAllowsDocsOnlyPullRequestsWithoutAnApprovedIssue(t *testing.T) {
	api := &fakeService{
		pullRequest: pullRequest("open", ""),
		files:       []*github.CommitFile{{Filename: new("docs/contributing.md")}},
	}
	if _, err := Run(api, "flock-php", "flock", Event{Action: "opened", Number: 87}); err != nil {
		t.Fatal(err)
	}
	if api.filesCalls != 1 {
		t.Fatalf("Files() calls = %d, want 1", api.filesCalls)
	}
	if api.state != "" || api.commented {
		t.Fatalf("state = %q, commented = %t", api.state, api.commented)
	}
}

func TestTranslationsNeedNoIssue(t *testing.T) {
	files := func(names ...string) []*github.CommitFile {
		var list []*github.CommitFile
		for _, name := range names {
			list = append(list, &github.CommitFile{Filename: new(name)})
		}
		return list
	}
	for _, test := range []struct {
		files []*github.CommitFile
		want  bool
	}{
		{files("lang/de/app.json"), true},
		{files("lang/cs/app.json", "macos/Resources/Localizable.xcstrings", "windows/Flock.Kit/Strings.cs.resx",
			"windows/Flock.Kit/Languages.g.cs", "windows/installer/Strings.iss", "linux/po/cs.po", "docs/sites.md"), true},
		{files("lang/cs/app.json", "linux/src/i18n.rs"), false},
		{files("lang/cs/app.json", "windows/Flock.Kit/Localization.cs"), false},
		{[]*github.CommitFile{{Filename: new("lang/cs/app.json"), PreviousFilename: new("linux/src/main.rs")}}, false},
	} {
		var names []string
		for _, file := range test.files {
			names = append(names, file.GetFilename())
		}
		if got := needsNoIssue(test.files); got != test.want {
			t.Errorf("needsNoIssue(%v) = %t, want %t", names, got, test.want)
		}
	}
}

func TestRunBypassesMaintainersWithoutCheckingIssuesOrFiles(t *testing.T) {
	pr := pullRequest("open", "")
	pr.AuthorAssociation = new("MEMBER")
	api := &fakeService{pullRequest: pr}
	if _, err := Run(api, "flock-php", "flock", Event{Action: "opened", Number: 87}); err != nil {
		t.Fatal(err)
	}
	if api.issueCalls != 0 || api.filesCalls != 0 {
		t.Fatalf("Issue() calls = %d, Files() calls = %d, want 0", api.issueCalls, api.filesCalls)
	}
}

type fakeService struct {
	pullRequest *github.PullRequest
	files       []*github.CommitFile
	issue       *github.Issue
	gateClosed  bool
	state       string
	commented   bool
	filesCalls  int
	issueCalls  int
}

func (f *fakeService) PullRequest(string, string, int) (*github.PullRequest, error) {
	return f.pullRequest, nil
}
func (f *fakeService) Files(string, string, int) ([]*github.CommitFile, error) {
	f.filesCalls++
	return f.files, nil
}
func (f *fakeService) Issue(string, string, int) (*github.Issue, error) {
	f.issueCalls++
	return f.issue, nil
}
func (f *fakeService) GateClosed(string, string, int) (bool, error) { return f.gateClosed, nil }
func (f *fakeService) Comment(string, string, int, string) error {
	f.commented = true
	return nil
}
func (f *fakeService) SetPullRequestState(_ string, _ string, _ int, state string) error {
	f.state = state
	return nil
}

func pullRequest(state, body string) *github.PullRequest {
	return &github.PullRequest{
		Number: new(87),
		State:  new(state),
		Base:   &github.PullRequestBranch{Ref: new("main")},
		Body:   new(body),
		User:   &github.User{Login: new("contributor"), Type: new("User")},
	}
}

func approvedIssue() *github.Issue {
	return &github.Issue{State: new("open"), Labels: []*github.Label{{Name: openToAnyone}}}
}
