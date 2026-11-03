package fetchcore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// testBitbucketFirst is the trusted first page URL bitbucketPages builds.
const testBitbucketFirst = "https://api.bitbucket.org/2.0/repos/o/r/pullrequests/7/comments?pagelen=100"

// bitbucketBearer is the auth argv the bitbucket reads attach.
func bitbucketBearer() []string { return []string{"-H", "Authorization: Bearer tok"} }

// bitbucketCall is the exact scripted argv one page fires.
func bitbucketCall(url string) string {
	return "curl\x00-sfL\x00-H\x00Authorization: Bearer tok\x00" + url
}

// newTestSession builds the service session over a scripted commander.
func newTestSession(cmdr Commander) (*session, *[]string) {
	warns := &[]string{}
	return &session{Human: io.Discard, commander: cmdr, warnings: warns}, warns
}

// TestSameOrigin proves the credential-bound origin check: same scheme and
// host pass (case-insensitively, relative links resolve onto the origin),
// and every other scheme, host, or port refuses.
func TestSameOrigin(t *testing.T) {
	t.Parallel()
	base := "https://api.bitbucket.org/2.0/repos/o/r"
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{"same origin", "https://api.bitbucket.org/2.0/next", true},
		{"host case", "https://API.BITBUCKET.ORG/2.0/next", true},
		{"relative", "/2.0/next", true},
		{"query only", "?cursor=2", true},
		{"scheme downgrade", "http://api.bitbucket.org/2.0/next", false},
		{"other host", "https://evil.invalid/steal", false},
		{"protocol relative", "//evil.invalid/steal", false},
		{"other port", "https://api.bitbucket.org:8443/2.0/next", false},
		{"garbage", "://", false},
		{"empty", "", false},
	} {
		if got := sameOrigin(base, tc.raw); got != tc.want {
			t.Errorf("%s: sameOrigin(%q) = %v, want %v", tc.name, tc.raw, got, tc.want)
		}
	}
}

// TestBitbucketPagesRefusesOffOriginNext proves a server-supplied next on
// another origin refuses before the bearer token is sent anywhere.
func TestBitbucketPagesRefusesOffOriginNext(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		bitbucketCall(testBitbucketFirst): {stdout: `{"values":[{"id":1}],` +
			`"next":"https://evil.invalid/steal?cursor=token-like"}`},
	}}
	s, _ := newTestSession(cmdr)
	items, ref := bitbucketPages(context.Background(), s, bitbucketBearer(), testBitbucketFirst, "values")
	if ref == nil || ref.Code != CodeFetchFailed {
		t.Fatalf("refusal = %v, want fetch-failed", ref)
	}
	if items != nil {
		t.Errorf("items = %v, want none", items)
	}
	if len(cmdr.calls) != 1 {
		t.Errorf("calls = %q, want only the first page (token never sent off-origin)", cmdr.calls)
	}
	if !strings.Contains(ref.Message, "evil.invalid") || strings.Contains(ref.Message, "token-like") {
		t.Errorf("message = %q, want the host only", ref.Message)
	}
}

// TestBitbucketPagesFollowsSameOriginNext proves a same-origin next is
// followed and the pages concatenate.
func TestBitbucketPagesFollowsSameOriginNext(t *testing.T) {
	t.Parallel()
	next := testBitbucketFirst + "&cursor=2"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		bitbucketCall(testBitbucketFirst): {stdout: `{"values":[{"id":1}],"next":"` + next + `"}`},
		bitbucketCall(next):               {stdout: `{"values":[{"id":2}],"next":null}`},
	}}
	s, warns := newTestSession(cmdr)
	items, ref := bitbucketPages(context.Background(), s, bitbucketBearer(), testBitbucketFirst, "values")
	if ref != nil {
		t.Fatalf("refusal = %v", ref)
	}
	if len(items) != 2 || jq.R(objOrMap(items[1])["id"]) != "2" {
		t.Errorf("items = %v, want ids 1 and 2", items)
	}
	if len(cmdr.calls) != 2 {
		t.Errorf("calls = %q, want both pages", cmdr.calls)
	}
	if len(*warns) != 0 {
		t.Errorf("warnings = %v, want none", *warns)
	}
}

// TestBitbucketPagesRepeatedCursorTrips proves a next cursor that never
// advances stops the walk with a warning instead of re-fetching forever.
func TestBitbucketPagesRepeatedCursorTrips(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		bitbucketCall(testBitbucketFirst): {stdout: `{"values":[{"id":1}],"next":"` + testBitbucketFirst + `"}`},
	}}
	s, warns := newTestSession(cmdr)
	items, ref := bitbucketPages(context.Background(), s, bitbucketBearer(), testBitbucketFirst, "values")
	if ref != nil {
		t.Fatalf("refusal = %v", ref)
	}
	if len(items) != 1 {
		t.Errorf("items = %v, want one page", items)
	}
	if len(cmdr.calls) != 1 {
		t.Errorf("calls = %q, want one fetch", cmdr.calls)
	}
	if len(*warns) != 1 || !strings.Contains((*warns)[0], "ended early") {
		t.Errorf("warnings = %v, want one early-stop warning", *warns)
	}
}

// TestPageWalkCap proves the shared walk stops at the page cap.
func TestPageWalkCap(t *testing.T) {
	t.Parallel()
	fetches := 0
	items, truncated, err := pageWalk(context.Background(), "p1", 2,
		func(_ context.Context, cursor string) ([]any, string, error) {
			fetches++
			return []any{cursor}, fmt.Sprintf("p%d", fetches+1), nil
		})
	if err != nil || !truncated {
		t.Fatalf("walk = %v %v, want truncated nil", truncated, err)
	}
	if fetches != 2 || len(items) != 2 {
		t.Errorf("fetches = %d, items = %v, want two pages", fetches, items)
	}
}

// TestForgejoDiffRefusesOffOriginDiffURL proves the metadata diff_url
// fallback never carries the token to another origin.
func TestForgejoDiffRefusesOffOriginDiffURL(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	s, _ := newTestSession(cmdr)
	err := forgejoDiffFallback(context.Background(), s, t.TempDir(), bitbucketBearer(),
		"https://example.invalid/api/v1/repos/o/r/pulls/7",
		`{"diff_url":"https://evil.invalid/steal.diff"}`)
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != CodeFetchFailed {
		t.Fatalf("err = %v, want fetch-failed", err)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none (token never sent off-origin)", cmdr.calls)
	}
}

// TestAzureSetupRefusesOffOriginRemote proves a metadata remoteUrl on
// another origin refuses before any git fetch can carry the PAT.
func TestAzureSetupRefusesOffOriginRemote(t *testing.T) {
	t.Parallel()
	argv := "az\x00repos\x00pr\x00show\x00--id\x007\x00--org\x00https://example.invalid" +
		"\x00--project\x00acme\x00--repository\x00app\x00-o\x00json"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		argv: {stdout: `{"repository":{"remoteUrl":"https://evil.invalid/r.git"},` +
			`"lastMergeTargetCommit":{"commitId":"bbbb"},` +
			`"lastMergeSourceCommit":{"commitId":"aaaa"}}`},
	}}
	s, _ := newTestSession(cmdr)
	ctx2 := &fetchRunCtx{
		runner: s,
		env: map[string]string{
			"AZURE_DEVOPS_ORG":     "https://example.invalid",
			"AZURE_DEVOPS_EXT_PAT": "tok",
		},
		dir: t.TempDir(),
	}
	_, err := azureSetup(context.Background(), ctx2, "acme/app", "7")
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != CodeFetchFailed {
		t.Fatalf("err = %v, want fetch-failed", err)
	}
	if len(cmdr.calls) != 1 {
		t.Errorf("calls = %q, want only the metadata read (no credentialed fetch)", cmdr.calls)
	}
}

// jsonRows renders a JSON array of n items from an item builder.
func jsonRows(n int, item func(int) string) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = item(i)
	}
	return "[" + strings.Join(rows, ",") + "]"
}

// forgejoCall is the exact scripted argv one Forgejo list page fires.
func forgejoCall(url string) string {
	return "curl\x00-sf\x00-H\x00Authorization: token tok\x00" + url
}

// TestForgejoFilesPages proves the file list walks every page instead of
// writing the 50-row default page.
func TestForgejoFilesPages(t *testing.T) {
	t.Parallel()
	base := "https://example.invalid/api/v1/repos/o/r/pulls/7/files"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		forgejoCall(base + "?limit=50&page=1"): {stdout: jsonRows(forgejoPageSize, func(int) string { return `{"id":1}` })},
		forgejoCall(base + "?limit=50&page=2"): {stdout: `[{"id":2}]`},
	}}
	s, warns := newTestSession(cmdr)
	raw := forgejoFiles(context.Background(), s, []string{"-H", "Authorization: token tok"},
		"https://example.invalid/api/v1/repos/o/r/pulls/7")
	items, err := jsonArray(raw)
	if err != nil || len(items) != forgejoPageSize+1 {
		t.Fatalf("files = %d, %v; want %d", len(items), err, forgejoPageSize+1)
	}
	if len(cmdr.calls) != 2 {
		t.Errorf("calls = %q, want both pages", cmdr.calls)
	}
	if len(*warns) != 0 {
		t.Errorf("warnings = %v, want none", *warns)
	}
}

// TestForgejoListSinglePageVerbatim proves a complete one-page list keeps
// the host's exact bytes (the golden bundles compare it).
func TestForgejoListSinglePageVerbatim(t *testing.T) {
	t.Parallel()
	base := "https://example.invalid/api/v1/repos/o/r/pulls/7/reviews"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		forgejoCall(base + "?limit=50&page=1"): {stdout: `[{"id":7}]`},
	}}
	s, _ := newTestSession(cmdr)
	raw := forgejoList(context.Background(), s, []string{"-H", "Authorization: token tok"},
		base, "reviews")
	if raw != `[{"id":7}]` {
		t.Errorf("raw = %q, want the host bytes", raw)
	}
}

// TestForgejoListWarnsAtPageCap proves the cap stops a long list with a
// warning instead of silently truncating at page one.
func TestForgejoListWarnsAtPageCap(t *testing.T) {
	t.Parallel()
	base := "https://example.invalid/api/v1/repos/o/r/pulls/7/files"
	out := map[string]scriptResult{}
	for page := 1; page <= providerMaxPages; page++ {
		u := fmt.Sprintf("%s?limit=%d&page=%d", base, forgejoPageSize, page)
		out[forgejoCall(u)] = scriptResult{stdout: jsonRows(forgejoPageSize, func(int) string { return `{"id":1}` })}
	}
	s, warns := newTestSession(&scriptCommander{t: t, out: out})
	raw := forgejoFiles(context.Background(), s, []string{"-H", "Authorization: token tok"},
		"https://example.invalid/api/v1/repos/o/r/pulls/7")
	items, err := jsonArray(raw)
	if err != nil || len(items) != providerMaxPages*forgejoPageSize {
		t.Fatalf("files = %d, %v", len(items), err)
	}
	if len(*warns) != 1 || !strings.Contains((*warns)[0], "10-page cap") {
		t.Errorf("warnings = %v, want the cap warning", *warns)
	}
}

// TestForgejoCommentReadFailureWarns proves a failed per-review comment
// read surfaces instead of writing a silent empty inline list.
func TestForgejoCommentReadFailureWarns(t *testing.T) {
	t.Parallel()
	api := "https://example.invalid/api/v1/repos/o/r/pulls/7"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		forgejoCall(api + "/reviews?limit=50&page=1"): {stdout: `[{"id":7}]`},
		forgejoCall(api + "/reviews/7/comments"):      {stderr: "curl: (22)", fail: true},
	}}
	s, warns := newTestSession(cmdr)
	dir := t.TempDir()
	if err := forgejoThreads(context.Background(), s, dir, []string{"-H", "Authorization: token tok"}, api); err != nil {
		t.Fatal(err)
	}
	if len(*warns) != 1 || !strings.Contains((*warns)[0], "review-comment read failed") {
		t.Errorf("warnings = %v, want the comment-read warning", *warns)
	}
}

// TestGitLabNotesPages proves the MR notes walk both pages instead of one
// `glab mr note list` page.
func TestGitLabNotesPages(t *testing.T) {
	t.Parallel()
	page := func(n int) string {
		return fmt.Sprintf("glab\x00api\x00projects/11/merge_requests/7/notes?per_page=100&page=%d", n)
	}
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		page(1): {stdout: jsonRows(gitlabNotePageSize, func(int) string { return `{"id":1}` })},
		page(2): {stdout: `[{"id":2}]`},
	}}
	s, warns := newTestSession(cmdr)
	raw := gitlabNotes(context.Background(), s, "11", "7")
	items, err := jsonArray(raw)
	if err != nil || len(items) != gitlabNotePageSize+1 {
		t.Fatalf("notes = %d, %v; want %d", len(items), err, gitlabNotePageSize+1)
	}
	if len(cmdr.calls) != 2 {
		t.Errorf("calls = %q, want both pages", cmdr.calls)
	}
	if len(*warns) != 0 {
		t.Errorf("warnings = %v, want none", *warns)
	}
}

// TestGitLabNotesFailureWarns proves a failed note read surfaces before the
// empty inline/review files land.
func TestGitLabNotesFailureWarns(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		"glab\x00api\x00projects/11/merge_requests/7/notes?per_page=100&page=1": {
			stderr: "glab: notes unavailable", fail: true,
		},
	}}
	s, warns := newTestSession(cmdr)
	raw := gitlabNotes(context.Background(), s, "11", "7")
	if raw != emptyJSONArray {
		t.Errorf("notes = %q, want empty array", raw)
	}
	if len(*warns) != 1 || !strings.Contains((*warns)[0], "notes paging stopped") {
		t.Errorf("warnings = %v, want the paging warning", *warns)
	}
}

// TestAzureChangesPages proves the change-entry walk sends $top/$skip and
// concatenates every page.
func TestAzureChangesPages(t *testing.T) {
	t.Parallel()
	conn := &azureConn{org: "https://example.invalid", project: "acme", rname: "app"}
	iter := "az\x00rest\x00--method\x00get\x00--url\x00https://example.invalid/acme/_apis/git/repositories/app" +
		"/pullRequests/7/iterations?api-version=7.1\x00--query\x00max_by(value, &id).id\x00-o\x00tsv"
	changes := func(skip int) string {
		return fmt.Sprintf("az\x00rest\x00--method\x00get\x00--url\x00https://example.invalid/acme/_apis/git/repositories/app"+
			"/pullRequests/7/iterations/3/changes?api-version=7.1&$top=1000&$skip=%d\x00--query\x00changeEntries\x00-o\x00json", skip)
	}
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		iter:          {stdout: "3\n"},
		changes(0):    {stdout: jsonRows(azureChangePageSize, func(int) string { return `{"item":{"path":"/f.txt"}}` })},
		changes(1000): {stdout: `[{"item":{"path":"/last.txt"}}]`},
	}}
	s, warns := newTestSession(cmdr)
	raw := azureChanges(context.Background(), s, conn, "7")
	items, err := jsonArray(raw)
	if err != nil || len(items) != azureChangePageSize+1 {
		t.Fatalf("changes = %d, %v; want %d", len(items), err, azureChangePageSize+1)
	}
	if len(cmdr.calls) != 3 {
		t.Errorf("calls = %d, want the iteration read plus both pages", len(cmdr.calls))
	}
	if len(*warns) != 0 {
		t.Errorf("warnings = %v, want none", *warns)
	}
}
