package contribution

import "testing"

func TestParseClaimMarkers(t *testing.T) {
	issue, author, ok := ParseClaimRecord("<!-- pr-gate:claim issue=flock#73 author=contributor -->")
	if !ok || issue != (IssueReference{Repository: "flock", Number: 73}) || author != "contributor" {
		t.Fatalf("ParseClaimRecord() = %v, %q, %v", issue, author, ok)
	}

	pullRequest, author, ok := ParseIssueClaim("<!-- pr-gate:claim pr=87 author=contributor -->\n\nIssue claimed")
	if !ok || pullRequest != 87 || author != "contributor" {
		t.Fatalf("ParseIssueClaim() = %d, %q, %v", pullRequest, author, ok)
	}

	if _, _, ok := ParseClaimRecord("not a claim"); ok {
		t.Fatal("malformed claim record parsed successfully")
	}
	if _, _, ok := ParseIssueClaim("<!-- pr-gate:claim pr=87 author=bad user -->"); ok {
		t.Fatal("malformed issue claim parsed successfully")
	}
}
