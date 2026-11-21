package readcommon

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestClosingIssueRefs proves the keyword gate: the three closing-verb
// families, an optional repository slug, dedupe in first-seen order, and the
// shapes that are not a closing reference at all.
func TestClosingIssueRefs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		title string
		body  string
		want  []LinkedIssue
	}{
		{name: "no keyword", title: "Tidy the retry loop", body: "Refs #12", want: nil},
		{name: "fixes", body: "Fixes #12", want: []LinkedIssue{{Number: 12}}},
		{name: "fixes with colon", body: "Fixes: #12", want: []LinkedIssue{{Number: 12}}},
		{name: "closes with slug", body: "Closes acme/app#7", want: []LinkedIssue{{Slug: "acme/app", Number: 7}}},
		{
			name: "closes with colon and slug", body: "Closes: acme/app#7",
			want: []LinkedIssue{{Slug: "acme/app", Number: 7}},
		},
		{name: "resolved title", title: "Resolved #3", want: []LinkedIssue{{Number: 3}}},
		{name: "case and spacing", body: "FIXES   #41", want: []LinkedIssue{{Number: 41}}},
		{
			name: "title then body, deduped", title: "fix #12", body: "Closes #12\nResolves #13",
			want: []LinkedIssue{{Number: 12}, {Number: 13}},
		},
		{name: "zero is not an issue", body: "Fixes #0", want: nil},
		{name: "words around it", body: "This fixes #9 by hand", want: []LinkedIssue{{Number: 9}}},
		// The full GitHub issue URL is the same link as `owner/repo#N`: the
		// scheme and host are case-insensitive, and a query or fragment after
		// the number is not part of the reference.
		{
			name: "full issue URL", body: "Fixes https://github.com/acme/app/issues/12",
			want: []LinkedIssue{{Slug: "acme/app", Number: 12}},
		},
		{
			name: "colon, mixed case, and query", body: "Closes: HTTPS://GitHub.com/Acme/App/issues/7?notification=1",
			want: []LinkedIssue{{Slug: "Acme/App", Number: 7}},
		},
		{
			name: "fragment after the number", body: "Resolves https://github.com/acme/app/issues/9#issuecomment-1",
			want: []LinkedIssue{{Slug: "acme/app", Number: 9}},
		},
		{name: "another host's issue URL", body: "Fixes https://example.com/acme/app/issues/12", want: nil},
	} {
		got := ClosingIssueRefs(tc.title, tc.body)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: ClosingIssueRefs(%q, %q) = %v, want %v", tc.name, tc.title, tc.body, got, tc.want)
		}
	}
}

// TestClosingIssuesSearchReturnsOnePage reads one closingIssuesReferences
// payload: the node list, and whether the host held another page.
func TestClosingIssuesSearchReturnsOnePage(t *testing.T) {
	t.Parallel()
	page := `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{` +
		`"totalCount":2,"pageInfo":{"hasNextPage":true},` +
		`"nodes":[{"number":12,"title":"A","body":null,"url":"https://github.com/acme/app/issues/12",` +
		`"state":"OPEN","repository":{"nameWithOwner":"acme/app"}},null]}}}}}`
	nodes, more, err := ClosingIssuesPage(page)
	if err != nil {
		t.Fatalf("ClosingIssuesPage: %v", err)
	}
	if !more {
		t.Error("more = false, want true for hasNextPage")
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %v, want the two entries the payload carries", nodes)
	}
	first, ok := nodes[0].(map[string]any)
	if !ok {
		t.Fatalf("first node = %v, want an object", nodes[0])
	}
	if number, ok := jq.IntOf(first["number"]); !ok || number != 12 {
		t.Errorf("first node number = %v, want 12", first["number"])
	}
}

// TestClosingIssuesSearchRefusesPayloadWithoutData proves a failed or
// unrecognized GraphQL answer is an error: reading it as "this PR links
// nothing" would drop the issue the review was asked to check against.
func TestClosingIssuesSearchRefusesPayloadWithoutData(t *testing.T) {
	t.Parallel()
	for _, payload := range []string{
		`{"errors":[{"type":"NOT_FOUND"}]}`,
		`{"data":{"repository":null}}`,
		`not json`,
	} {
		if _, _, err := ClosingIssuesPage(payload); err == nil {
			t.Errorf("ClosingIssuesPage(%q) = nil error, want a refused payload", payload)
		}
	}
}

// TestClosingIssuesArgv pins the exact argv the linked-issue read fires.
func TestClosingIssuesArgv(t *testing.T) {
	t.Parallel()
	got := ClosingIssuesArgv("acme", "app", "4701")
	want := []string{
		"api", "graphql",
		"-F", "owner=acme", "-F", "name=app", "-F", "pr=4701",
		"-f", "query=" + QueryClosingIssues,
	}
	if !slices.Equal(got, want) {
		t.Errorf("ClosingIssuesArgv = %q, want %q", got, want)
	}
}
