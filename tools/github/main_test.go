package main

import "testing"

func TestParseRepository(t *testing.T) {
	owner, repository, err := parseRepository("flock-php/flock")
	if err != nil || owner != "flock-php" || repository != "flock" {
		t.Fatalf("parseRepository() = %q, %q, %v", owner, repository, err)
	}
	if _, _, err := parseRepository("flock-php"); err == nil {
		t.Fatal("malformed repository returned nil error")
	}
}

func TestValidCommandAcceptsOnlyWorkflowCommands(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{args: []string{"pr"}, want: true},
		{args: []string{"merge"}, want: true},
		{args: []string{"release-sync", "134", "2"}, want: true},
		{args: []string{"release-sync", "0", "2"}, want: false},
		{args: []string{"release-sync", "134", "abc"}, want: false},
		{args: []string{"issue", "triage"}, want: true},
		{args: []string{"issue", "assignment"}, want: true},
		{args: []string{"issue"}, want: false},
		{args: []string{"issue", "close"}, want: false},
		{args: []string{"pr", "extra"}, want: false},
		{args: nil, want: false},
	}
	for _, test := range tests {
		t.Run("command", func(t *testing.T) {
			if got := validCommand(test.args); got != test.want {
				t.Fatalf("validCommand(%q) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}

func TestNewGitHubClientUsesTheConfiguredAPIURL(t *testing.T) {
	client, err := newGitHubClient("https://github.example/api/v3", "token")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := client.BaseURL(), "https://github.example/api/v3/"; got != want {
		t.Fatalf("client base URL = %q, want %q", got, want)
	}
	if _, err := newGitHubClient("://invalid", "token"); err == nil {
		t.Fatal("newGitHubClient() accepted an invalid URL")
	}
}
