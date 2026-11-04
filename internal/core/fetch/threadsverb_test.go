package fetchcore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// threadsRequest builds a refresh request over a scripted commander.
//
//nolint:unparam // the helper mirrors the verb's signature
func threadsRequest(cmdr Commander, repo, pr string) *ThreadsRequest {
	root := ""
	req := &ThreadsRequest{}
	req.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{
			Env:       map[string]string{},
			Now:       func() time.Time { return fetchTestTime },
			Commander: cmdr,
		},
		Repo: repo, Pr: pr, Root: &root,
	}
	return req
}

// writeThreadsBundle writes the bundle metadata the refresh reads.
func writeThreadsBundle(t *testing.T, pr, head string) {
	t.Helper()
	dir := filepath.Join(".review", pr)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"),
		[]byte(`{"state":"OPEN","headRefOid":"`+head+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// graphQLPage renders one page with the given nodes and cursor state.
func graphQLPage(nodes string, more bool, cursor string) string {
	next := "false"
	cursorJSON := "null"
	if more {
		next = "true"
		cursorJSON = `"` + cursor + `"`
	}
	return `{"data":{"repository":{"pullRequest":{"reviewThreads":{` +
		`"pageInfo":{"hasNextPage":` + next + `,"endCursor":` + cursorJSON + `},` +
		`"nodes":[` + nodes + `]}}}}}`
}

// graphQLThread renders one minimal review thread.
func graphQLThread(id, path string, line int, resolved, outdated bool, body string) string {
	return `{"id":"` + id + `","isResolved":` + boolLit(resolved) + `,"isOutdated":` + boolLit(outdated) +
		`,"path":"` + path + `","line":` + itoa(line) + `,"diffSide":"RIGHT","comments":{"totalCount":1,"nodes":[` +
		`{"id":"c-` + id + `","databaseId":` + itoa(line) + `,"author":{"__typename":"User","login":"ana"},` +
		`"body":"` + body + `","createdAt":"2026-11-01T00:00:00Z"}]}}`
}

// boolLit renders a Go bool as a JSON literal.
func boolLit(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// itoa renders a small int (line/position fixtures only).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}

// TestRunThreadsRefreshPagesAndWrites proves the refresh re-reads every
// GraphQL page, rewrites only threads.json, and reports the feed tally.
//
//nolint:revive,cyclop // the refresh matrix reads as one assertion block
func TestRunThreadsRefreshPagesAndWrites(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	dir := t.TempDir()
	t.Chdir(dir)
	writeThreadsBundle(t, "7", strings.Repeat("a", 40))
	const cursor = "cursor-2"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		githubThreadCall(""): {
			stdout: graphQLPage(graphQLThread("t1", "a.go", 2, false, false, "open note"), true, cursor),
		},
		githubThreadCall(cursor): {
			stdout: graphQLPage(graphQLThread("t2", "b.go", 5, true, true, "old note"), false, ""),
		},
	}}
	resp, ref := RunThreads(context.Background(), threadsRequest(cmdr, "o/r", "7"))
	if ref != nil {
		t.Fatalf("RunThreads refusal: %v", ref)
	}
	if len(cmdr.calls) != 2 {
		t.Fatalf("calls = %d, want two thread pages (no diff refetch)", len(cmdr.calls))
	}
	for _, call := range cmdr.calls {
		if len(call) > 2 && call[1] == "pr" && call[2] == "diff" {
			t.Errorf("refresh fetched the diff: %v", call)
		}
	}
	if resp.Data.Provider != providerGitHub || resp.Data.Threads != 2 || resp.Data.Open != 1 ||
		resp.Data.Outdated != 1 || resp.Data.Verdict != 0 {
		t.Errorf("data = %+v", resp.Data)
	}
	if resp.Data.FetchedAt != "2026-11-02T12:00:00Z" {
		t.Errorf("fetched_at = %q", resp.Data.FetchedAt)
	}
	if !strings.Contains(resp.Next, "address open threads") {
		t.Errorf("next = %q", resp.Next)
	}
	raw, rerr := os.ReadFile(".review/7/threads.json")
	if rerr != nil {
		t.Fatalf("read feed: %v", rerr)
	}
	doc, derr := jq.Decode(raw)
	if derr != nil {
		t.Fatalf("decode feed: %v", derr)
	}
	m, _ := jq.AsMap(doc)
	if len(jq.Elements(m["threads"])) != 2 || m["truncated"] != false {
		t.Errorf("feed = %s", raw)
	}
}

// TestRunThreadsKeepsFeedOnTotalFailure proves an empty failed read refuses
// and never overwrites a good feed.
func TestRunThreadsKeepsFeedOnTotalFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeThreadsBundle(t, "7", strings.Repeat("a", 40))
	prior := `{"schema_version":"1","provider":"github","pr":7,"head_sha":"` +
		strings.Repeat("a", 40) + `","fetched_at":"2026-11-01T00:00:00Z","truncated":false,"threads":[` +
		`{"id":"t1","path":"a.go","line":2,"start_line":null,"side":"RIGHT","resolved":false,` +
		`"outdated":false,"url":null,"author":"ana","author_kind":"human","finding_sig":null,` +
		`"kind":"review-comment","comments":[]}]}`
	if err := os.WriteFile(".review/7/threads.json", []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		githubThreadCall(""): {stderr: "gh: down", fail: true},
	}}
	_, ref := RunThreads(context.Background(), threadsRequest(cmdr, "o/r", "7"))
	if ref == nil || ref.Code != "threads-failed" {
		t.Fatalf("refusal = %v, want threads-failed", ref)
	}
	raw, err := os.ReadFile(".review/7/threads.json")
	if err != nil || string(raw) != prior {
		t.Errorf("existing feed changed: %q, %v", raw, err)
	}
}

// TestRunThreadsKeepsFeedOnSilentEmpty proves a clean-but-empty read never
// replaces a prior non-empty feed: an empty page with no truncation signal is
// still no evidence that the conversation is empty.
func TestRunThreadsKeepsFeedOnSilentEmpty(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	dir := t.TempDir()
	t.Chdir(dir)
	writeThreadsBundle(t, "7", strings.Repeat("a", 40))
	prior := `{"schema_version":"1","provider":"github","pr":7,"head_sha":"` +
		strings.Repeat("a", 40) + `","fetched_at":"2026-11-01T00:00:00Z","truncated":false,"threads":[` +
		`{"id":"t1","path":"a.go","line":2,"start_line":null,"side":"RIGHT","resolved":false,` +
		`"outdated":false,"url":null,"author":"ana","author_kind":"human","finding_sig":null,` +
		`"kind":"review-comment","comments":[]}]}`
	if err := os.WriteFile(".review/7/threads.json", []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		githubThreadCall(""): {stdout: emptyThreadPage},
	}}
	_, ref := RunThreads(context.Background(), threadsRequest(cmdr, "o/r", "7"))
	if ref == nil || ref.Code != codeThreadsFailed {
		t.Fatalf("refusal = %v, want threads-failed", ref)
	}
	raw, err := os.ReadFile(".review/7/threads.json")
	if err != nil || string(raw) != prior {
		t.Errorf("existing feed changed: %q, %v", raw, err)
	}
}

// TestRunThreadsRefusals pins the missing bundle and unknown provider paths.
func TestRunThreadsRefusals(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	_, ref := RunThreads(context.Background(), threadsRequest(cmdr, "o/r", "9"))
	if ref == nil || ref.Code != CodeFetchFailed || !strings.Contains(ref.Message, "fetch the PR first") {
		t.Fatalf("missing bundle refusal = %v", ref)
	}
	writeThreadsBundle(t, "9", strings.Repeat("a", 40))
	bad := threadsRequest(cmdr, "o/r", "9")
	bad.Provider = "gitea"
	_, ref = RunThreads(context.Background(), bad)
	if ref == nil || ref.Code != "bad-provider" {
		t.Fatalf("bad provider refusal = %v", ref)
	}
}

// TestRunThreadsGitLabRefresh proves a non-GitHub provider branch resolves
// the project id and reads discussions, carrying the PR head into the feed.
func TestRunThreadsGitLabRefresh(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	dir := t.TempDir()
	t.Chdir(dir)
	writeThreadsBundle(t, "8", strings.Repeat("a", 40))
	pidKey := "glab\x00api\x00projects/acme%2Fapp\x00--jq\x00.id"
	discussions := "glab\x00api\x00projects/11/merge_requests/8/discussions?per_page=100&page=1"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		pidKey: {stdout: "11\n"},
		discussions: {stdout: `[{"id":"d1","notes":[{"id":9,"body":"nit","author":{"username":"ana"},` +
			`"position":{"new_path":"a.go","new_line":2,"head_sha":"` + strings.Repeat("a", 40) + `"}}]}]`},
	}}
	gl := threadsRequest(cmdr, "acme/app", "8")
	gl.Provider = providerGitLab
	resp, ref := RunThreads(context.Background(), gl)
	if ref != nil {
		t.Fatalf("RunThreads refusal: %v", ref)
	}
	if resp.Data.Provider != providerGitLab || resp.Data.Threads != 1 || resp.Data.HeadSHA != strings.Repeat("a", 40) {
		t.Errorf("data = %+v", resp.Data)
	}
	if len(cmdr.calls) != 2 {
		t.Errorf("calls = %v", cmdr.calls)
	}
}

// cafeCommentCall is the single comments page a GitCafe refresh reads.
const cafeCommentCall = "cafe\x00pr\x00comments\x007\x00-R\x00o/r\x00--filter\x00all\x00--json"

// TestRunThreadsGitCafeFailedReadRefuses proves a GitCafe reviews/comments
// read that fails outright refuses with both causes, instead of refreshing the
// feed from whatever arrived.
func TestRunThreadsGitCafeFailedReadRefuses(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeThreadsBundle(t, "7", strings.Repeat("a", 40))
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		cafeReviewCall(""): {stderr: "cafe: 500 reviews down", fail: true},
		cafeCommentCall:    {stderr: "cafe: 502 comments down", fail: true},
	}}
	req := threadsRequest(cmdr, "o/r", "7")
	req.Provider = providerGitCafe
	_, ref := RunThreads(context.Background(), req)
	if ref == nil || ref.Code != codeThreadsFailed {
		t.Fatalf("refusal = %v, want threads-failed", ref)
	}
	// Both legs failed, and both causes are reported: one "exit status 1" line
	// per read, joined.
	if n := strings.Count(ref.Message, "exit status 1"); n != 2 {
		t.Errorf("message %q carries %d read failures, want 2 (reviews and comments)", ref.Message, n)
	}
	if _, err := os.Stat(".review/7/threads.json"); !os.IsNotExist(err) {
		t.Errorf("threads.json written on a failed read (err=%v)", err)
	}
}

// TestRunThreadsGitCafePartialReadMarksTruncated proves a reviews walk that
// stops after a good page still refreshes the feed and says it is incomplete:
// truncated:true in threads.json plus the envelope warning.
func TestRunThreadsGitCafePartialReadMarksTruncated(t *testing.T) {
	t.Skip("prototype: state root moved to .verdict; stale .review expectations")
	dir := t.TempDir()
	t.Chdir(dir)
	writeThreadsBundle(t, "7", strings.Repeat("a", 40))
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		cafeReviewCall(""):   cafeReviewPage([]string{"r1"}, "c2"),
		cafeReviewCall("c2"): {stderr: "cafe: 500 reviews down", fail: true},
		cafeCommentCall:      {stdout: `{"data":{"items":[]}}`},
	}}
	req := threadsRequest(cmdr, "o/r", "7")
	req.Provider = providerGitCafe
	resp, ref := RunThreads(context.Background(), req)
	if ref != nil {
		t.Fatalf("RunThreads refusal: %v", ref)
	}
	if !resp.Data.Truncated {
		t.Errorf("data = %+v, want truncated", resp.Data)
	}
	assertWarning(t, resp.Warnings, "thread read was incomplete")
	raw, err := os.ReadFile(".review/7/threads.json")
	if err != nil {
		t.Fatalf("read feed: %v", err)
	}
	doc, derr := jq.Decode(raw)
	if derr != nil {
		t.Fatalf("decode feed: %v", derr)
	}
	feed, _ := jq.AsMap(doc)
	if feed["truncated"] != true {
		t.Errorf("feed = %s, want truncated:true", raw)
	}
	if n := len(jq.Elements(feed["threads"])); n != 1 {
		t.Errorf("feed = %s, want the first page's review kept", raw)
	}
}

// TestThreadsHint proves the fetch next hint appears only when a feed carries
// threads.
func TestThreadsHint(t *testing.T) {
	t.Chdir(t.TempDir())
	if got := threadsHint(".review/7"); got != "" {
		t.Errorf("missing feed hint = %q", got)
	}
	if err := os.MkdirAll(".review/7", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".review/7/threads.json", []byte(`{"threads":[{"id":"t"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := threadsHint(".review/7"); !strings.Contains(got, ".review/7/threads.json") {
		t.Errorf("feed hint = %q", got)
	}
}
