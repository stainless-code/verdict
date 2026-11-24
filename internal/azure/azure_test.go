package azure

import (
	"strings"
	"testing"
)

const testOrg = "https://dev.azure.com/acme"

// testEnv is the environment a resolve reads.
func testEnv() map[string]string { return map[string]string{EnvOrg: testOrg} }

// TestOrgGate pins the organization gate: a missing variable, a non-https
// URL, and a URL carrying credentials or a query each refuse with a code,
// and no refusal echoes the value it rejected.
//
//nolint:revive // the organization matrix reads as one block
func TestOrgGate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, value, code string
	}{
		{"missing", "", CodeAuthMissing},
		{"blank", "   ", CodeAuthMissing},
		{"http", "http://dev.azure.com/acme", CodeBadTarget},
		{"no host", "https://", CodeBadTarget},
		{"credential", "https://user:topsecret@dev.azure.com/acme", CodeBadTarget},
		{"query", "https://dev.azure.com/acme?tok=topsecret", CodeBadTarget},
		{"fragment", "https://dev.azure.com/acme#topsecret", CodeBadTarget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ref := Org(map[string]string{EnvOrg: tc.value})
			if ref == nil || ref.Code != tc.code {
				t.Fatalf("Org(%q) = %+v, want %s", tc.name, ref, tc.code)
			}
			if strings.Contains(ref.Message, "topsecret") {
				t.Errorf("refusal echoes the rejected secret: %q", ref.Message)
			}
		})
	}
}

// TestOrgTrimsTrailingSlash proves the one normalization the URL builder
// relies on: a trailing slash cannot double a separator in every call.
func TestOrgTrimsTrailingSlash(t *testing.T) {
	t.Parallel()
	org, ref := Org(map[string]string{EnvOrg: testOrg + "/"})
	if ref != nil {
		t.Fatalf("Org refusal: %s: %s", ref.Code, ref.Message)
	}
	if org != testOrg {
		t.Errorf("org = %q, want %q", org, testOrg)
	}
}

// TestResolveRefusals pins the position gate: the repo must be one
// project/repository pair of real path segments and the pull id must be a
// positive integer, so a traversing value can never reach another endpoint.
func TestResolveRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, repo, pr string }{
		{"no slash", "app", "7"},
		{"empty project", "/app", "7"},
		{"empty repo", "acme/", "7"},
		{"three segments", "acme/app/extra", "7"},
		{"dotdot project", "../..", "7"},
		{"dotdot repo", "acme/..", "7"},
		{"dot project", "./app", "7"},
		{"backslash", `acme\app`, "7"},
		{"control byte", "acme/ap\tp", "7"},
		{"non numeric pr", "acme/app", "seven"},
		{"traversing pr", "acme/app", "7/../../user"},
		{"zero pr", "acme/app", "0"},
		{"negative pr", "acme/app", "-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ref := Resolve(testEnv(), tc.repo, tc.pr)
			if ref == nil || ref.Code != CodeBadTarget {
				t.Fatalf("Resolve(%q, %q) = %+v, want %s", tc.repo, tc.pr, ref, CodeBadTarget)
			}
		})
	}
}

// TestTargetURLs pins every URL the post path builds, as literals: a rename
// in the builder fails here instead of following the helper that produced
// it.
func TestTargetURLs(t *testing.T) {
	t.Parallel()
	target, ref := Resolve(testEnv(), "acme/app", "7")
	if ref != nil {
		t.Fatalf("Resolve refusal: %s: %s", ref.Code, ref.Message)
	}
	const base = testOrg + "/acme/_apis/git/repositories/app/pullRequests/7"
	for _, tc := range []struct{ name, got, want string }{
		{"base", target.Base(), base},
		{"pull", target.PullURL(), base + "?api-version=7.1"},
		{"threads", target.ThreadsURL(), base + "/threads?api-version=7.1"},
		{"thread comment", target.ThreadCommentURL("501", "1"), base + "/threads/501/comments/1?api-version=7.1"},
		{"web", target.WebURL(), testOrg + "/acme/_git/app/pullrequest/7"},
		{"thread web", target.ThreadWebURL("501"), testOrg + "/acme/_git/app/pullrequest/7?discussionId=501"},
		{"connection data", ConnectionDataURL(testOrg), testOrg + "/_apis/connectionData?api-version=7.1"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// TestResolveEscapesSegments proves a segment that survives the gate cannot
// splice a new path: the escape is what keeps a `?` or `#` inside the name.
func TestResolveEscapesSegments(t *testing.T) {
	t.Parallel()
	target, ref := Resolve(testEnv(), "acme project/app?x", "7")
	if ref != nil {
		t.Fatalf("Resolve refusal: %s: %s", ref.Code, ref.Message)
	}
	if strings.Contains(target.Base(), "app?x") {
		t.Errorf("base %q splices an unescaped segment", target.Base())
	}
}

// TestDecodePull pins the head read fetch and post share.
func TestDecodePull(t *testing.T) {
	t.Parallel()
	pull := DecodePull(`{"pullRequestId":7,"lastMergeSourceCommit":{"commitId":"aaaa"}}`)
	if got := pull.Head(); got != "aaaa" {
		t.Errorf("DecodePull head = %q, want aaaa", got)
	}
	if head := DecodePull("not json").Head(); head != "" {
		t.Errorf("an unreadable pull reads head %q, want empty", head)
	}
}

// TestDecodeThreads pins the `{count, value}` unwrapping: Azure does not
// answer a bare array, and a caller that forgot the wrapper would read zero
// threads.
func TestDecodeThreads(t *testing.T) {
	t.Parallel()
	items, ok := DecodeThreads(`{"count":2,"value":[{"id":1},{"id":2}]}`)
	if !ok || len(items) != 2 {
		t.Fatalf("DecodeThreads = %v, %v; want two threads", items, ok)
	}
	if _, ok := DecodeThreads(`[]`); ok {
		t.Error("a bare array decoded as a thread list")
	}
	if _, ok := DecodeThreads("not json"); ok {
		t.Error("an unreadable document decoded as a thread list")
	}
}

// TestThreadPredicates pins the three questions the recheck, the summary
// read, and the publication walk ask of one decoded thread.
func TestThreadPredicates(t *testing.T) {
	t.Parallel()
	text := `{"id":7,"status":"active","threadContext":{"filePath":"/a.go",` +
		`"rightFileStart":{"line":4,"offset":1}},"comments":[{"id":1,"parentCommentId":0,` +
		`"content":"x","commentType":"text","author":{"uniqueName":"rev"}}]}`
	system := `{"id":8,"status":"active","threadContext":null,"comments":[{"id":1,` +
		`"content":"rev voted 10","commentType":"system"}]}`
	resolved := `{"id":9,"status":"fixed","threadContext":{"filePath":"/a.go",` +
		`"leftFileStart":{"line":2,"offset":1}},"comments":[{"id":1,"content":"y","commentType":"text"}]}`
	deleted := `{"id":10,"isDeleted":true,"status":"active","threadContext":{"filePath":"/a.go"},` +
		`"comments":[{"id":1,"content":"z","commentType":"text"}]}`
	for _, tc := range []struct {
		name, raw                  string
		inline, open, deleted, sys bool
	}{
		{"inline text", text, true, true, false, false},
		{"general system", system, false, true, false, true},
		{"resolved inline", resolved, true, false, false, false},
		{"deleted inline", deleted, true, true, true, false},
	} {
		items, ok := DecodeThreads(`{"value":[` + tc.raw + `]}`)
		if !ok {
			t.Fatalf("%s: thread list unreadable", tc.name)
		}
		m := items[0]
		if Inline(m) != tc.inline || Open(m) != tc.open || Deleted(m) != tc.deleted || System(m) != tc.sys {
			t.Errorf("%s: inline=%v open=%v deleted=%v system=%v, want %v %v %v %v",
				tc.name, Inline(m), Open(m), Deleted(m), System(m),
				tc.inline, tc.open, tc.deleted, tc.sys)
		}
	}
}

// TestThreadStatusSpellings proves both spellings of a status decode: a
// request sends the number, a response carries the string.
func TestThreadStatusSpellings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{`{"status":"active"}`, StatusActive},
		{`{"status":"pending"}`, StatusPending},
		{`{"status":1}`, StatusActive},
		{`{"status":6}`, StatusPending},
		{`{"status":"fixed"}`, "fixed"},
		{`{"status":42}`, "unknown"},
		{`{}`, "unknown"},
	} {
		if got := Status(decodeOne(t, tc.raw)); got != tc.want {
			t.Errorf("Status(%s) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestCommentFields pins the body, the author, and the ids a summary comment
// is addressed by.
func TestCommentFields(t *testing.T) {
	t.Parallel()
	m := decodeOne(t, `{"id":501,"comments":[{"id":3,"parentCommentId":0,"content":"hi",`+
		`"commentType":"text","author":{"uniqueName":"rev@example.com","displayName":"Rev"}}]}`)
	if got := Body(m); got != "hi" {
		t.Errorf("Body = %q, want hi", got)
	}
	if got := Author(m); got != "rev@example.com" {
		t.Errorf("Author = %q, want the unique name", got)
	}
	if got, want := ThreadID(m), "501"; got != want {
		t.Errorf("ThreadID = %q, want %q", got, want)
	}
	if got, want := CommentID(m), "3"; got != want {
		t.Errorf("CommentID = %q, want %q", got, want)
	}
}

// TestAnchorOf pins the anchor read: a right-side position first, an
// old-side one second, the diff prefix stripped, and no anchor on a general
// thread.
func TestAnchorOf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw string
		want      Anchor
		wantOK    bool
	}{
		{
			name: "right", wantOK: true,
			raw:  `{"threadContext":{"filePath":"/a.go","rightFileStart":{"line":4},"rightFileEnd":{"line":6}}}`,
			want: Anchor{Path: "a.go", Side: "RIGHT", Start: 4, End: 6},
		},
		{
			name: "left", wantOK: true,
			raw:  `{"threadContext":{"filePath":"/a.go","leftFileStart":{"line":2},"leftFileEnd":{"line":2}}}`,
			want: Anchor{Path: "a.go", Side: "LEFT", Start: 2, End: 2},
		},
		{
			name: "general", raw: `{"threadContext":null}`,
		},
		{
			name: "no line", raw: `{"threadContext":{"filePath":"/a.go","rightFileStart":{"line":0}}}`,
		},
	} {
		anchor, ok := AnchorOf(decodeOne(t, tc.raw))
		if ok != tc.wantOK || anchor != tc.want {
			t.Errorf("%s: AnchorOf = %+v, %v; want %+v, %v", tc.name, anchor, ok, tc.want, tc.wantOK)
		}
	}
}

// TestDecodeIdentity pins the one identity read: Azure publishes the account
// name as a property, not as `uniqueName`, with the display name as
// fallback.
func TestDecodeIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, raw, want string }{
		{"account", `{"authenticatedUser":{"id":"guid","providerDisplayName":"Rev",` +
			`"properties":{"Account":{"$value":"rev@example.com"}}}}`, "rev@example.com"},
		{"display name", `{"authenticatedUser":{"id":"guid","providerDisplayName":"Rev"}}`, "Rev"},
		{"id only", `{"authenticatedUser":{"id":"guid"}}`, "guid"},
	} {
		identity, ok := DecodeIdentity(tc.raw)
		if !ok || identity.Name != tc.want {
			t.Errorf("%s: DecodeIdentity = %+v, %v; want name %q", tc.name, identity, ok, tc.want)
		}
	}
	if _, ok := DecodeIdentity(`{"authenticatedUser":{}}`); ok {
		t.Error("an identity-less document decoded as a user")
	}
}

// TestDecodeCreated pins the created-thread read the receipt and the summary
// handle come from.
func TestDecodeCreated(t *testing.T) {
	t.Parallel()
	created, ok := DecodeCreated(`{"id":501,"publishedDate":"2026-11-02T12:00:00Z",` +
		`"comments":[{"id":1,"content":"hi"}]}`)
	if !ok || created.ID != 501 || created.FirstCommentID() != 1 || created.FirstCommentBody() != "hi" {
		t.Fatalf("DecodeCreated = %+v, %v", created, ok)
	}
	if _, ok := DecodeCreated(`{"comments":[]}`); ok {
		t.Error("a thread with no id decoded as created")
	}
}

// decodeOne decodes one JSON object into the map shape the accessors read.
func decodeOne(t *testing.T, raw string) map[string]any {
	t.Helper()
	m, ok := DecodeThreads(`{"value":[` + raw + `]}`)
	if !ok || len(m) != 1 {
		t.Fatalf("decode %s: %v, %v", raw, m, ok)
	}
	return m[0]
}
