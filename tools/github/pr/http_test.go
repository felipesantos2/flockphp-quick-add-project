package pr

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v93/github"
)

type requestLog struct {
	method string
	path   string
}

func testClient(t *testing.T, handler http.Handler) (*github.Client, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	baseURL.Path += "/"
	base := baseURL.String()
	client, err := github.NewClient(github.WithAuthToken("token"), github.WithURLs(&base, nil))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return client, server.Close
}

func writeTestJSON(t *testing.T, writer http.ResponseWriter, value string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(writer, value)
}

func testPullRequestEvent(action string, number int, bodyEdited bool) []byte {
	event := map[string]any{
		"action":       action,
		"pull_request": map[string]any{"number": number},
	}
	if bodyEdited {
		event["changes"] = map[string]any{"body": map[string]any{"from": "old"}}
	}
	data, _ := json.Marshal(event)
	return data
}

func containsLog(logs []requestLog, method, path string) bool {
	for _, log := range logs {
		if log.method == method && log.path == path {
			return true
		}
	}
	return false
}

func countLog(logs []requestLog, method, path string) int {
	count := 0
	for _, log := range logs {
		if log.method == method && log.path == path {
			count++
		}
	}
	return count
}
