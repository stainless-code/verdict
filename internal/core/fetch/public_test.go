package fetchcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// publicCall is the exact anonymous curl argv one read fires: no --config, no
// credential, the fail-and-follow flags the other providers use.
func publicCall(url string) string {
	return strings.Join([]string{"curl", flagCurlDisable, flagCurlPublicFail, url}, "\x00")
}

// testkitDiff is the shared three-line fixture diff with one replaced line.
func testkitDiff() string {
	return "diff --git a/notes.txt b/notes.txt\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/notes.txt\n" +
		"+++ b/notes.txt\n" +
		"@@ -1,3 +1,3 @@\n" +
		" top\n" +
		"-old line\n" +
		"+new line\n" +
		" bottom\n"
}

// publicDiffCall is the diff read's argv, which also carries the media type.
func publicDiffCall() string {
	return strings.Join(
		[]string{"curl", flagCurlDisable, flagCurlPublicFail, flagH, publicDiffAccept, publicTestBase}, "\x00")
}

// publicTestBase is the anonymous API root for the fixture repository.
const publicTestBase = "https://api.github.com/repos/acme/app/pulls/7"

// publicMetaDoc is the anonymous PR document the REST read serves.
const publicMetaDoc = `{"number":7,"title":"Retry the upload","body":"Adds a retry.",` +
	`"state":"open","draft":false,"html_url":"https://github.com/acme/app/pull/7",` +
	`"user":{"login":"mate"},"labels":[{"name":"bug"},{"name":"infra"}],` +
	`"base":{"ref":"main","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},` +
	`"head":{"ref":"feat/retry","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`

// publicFilesDoc is the one page of changed files the listing serves.
const publicFilesDoc = `[{"filename":"notes.txt","status":"modified","additions":1,"deletions":1}]`

// publicRunCtx is the fetch run a public read works inside: the temp bundle
// directory, the positionals, and the scripted commander.
func publicRunCtx(t *testing.T, cmdr Commander) (*fetchRunCtx, string) {
	t.Helper()
	dir := t.TempDir()
	s, _ := newTestSession(cmdr)
	return &fetchRunCtx{runner: s, dir: dir, repo: "acme/app", pr: "7"}, dir
}

// TestPublicGitHubBackend proves the anonymous read's exact argv, the artifacts
// it writes, and the one read that needs a token (thread resolution).
func TestPublicGitHubBackend(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		publicCall(publicTestBase): {stdout: publicMetaDoc},
		publicDiffCall():           {stdout: testkitDiff()},
		publicCall(publicPageURL(publicTestBase+publicFilesPath, 1)):    {stdout: publicFilesDoc},
		publicCall(publicPageURL(publicTestBase+publicReviewsPath, 1)):  {stdout: `[{"id":1}]`},
		publicCall(publicPageURL(publicTestBase+publicCommentsPath, 1)): {stdout: `[{"id":2}]`},
		"git\x00patch-id\x00--stable":                                   {stdout: testPatchID},
	}}
	ctx2, dir := publicRunCtx(t, cmdr)
	if err := fetchGitHubPublic(context.Background(), ctx2, &Request{Repo: "acme/app", Pr: "7"}); err != nil {
		t.Fatalf("fetchGitHubPublic: %v", err)
	}
	assertPublicArgv(t, cmdr)
	assertPublicPRJSON(t, dir)
	assertPublicSidecars(t, ctx2, dir)
}

// assertPublicArgv proves every anonymous read fires without a credential and
// in the documented order.
func assertPublicArgv(t *testing.T, cmdr *scriptCommander) {
	t.Helper()
	want := []string{
		publicCall(publicTestBase),
		publicDiffCall(),
		publicCall(publicPageURL(publicTestBase+publicFilesPath, 1)),
		publicCall(publicPageURL(publicTestBase+publicReviewsPath, 1)),
		publicCall(publicPageURL(publicTestBase+publicCommentsPath, 1)),
	}
	if len(cmdr.calls) != len(want) {
		t.Fatalf("argv calls = %q, want %d", cmdr.calls, len(want))
	}
	for i, argv := range want {
		if got := strings.Join(cmdr.calls[i], "\x00"); got != argv {
			t.Errorf("argv %d = %q, want %q", i, got, argv)
		}
	}
	for i, stdin := range cmdr.stdin {
		if stdin != "" {
			t.Errorf("argv %d sent %q on stdin, want no credential document", i, stdin)
		}
	}
}

// assertPublicPRJSON proves the anonymous document normalized into the one
// PR.json record every provider shares.
func assertPublicPRJSON(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "PR.json"))
	if err != nil {
		t.Fatalf("read PR.json: %v", err)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		t.Fatalf("decode PR.json: %v", err)
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		t.Fatal("PR.json is not an object")
	}
	assertPublicPRFields(t, m)
	assertPublicFileRow(t, m)
}

// assertPublicFileRow proves the normalized file row: one changed path with the
// status the listing reported.
func assertPublicFileRow(t *testing.T, m map[string]any) {
	t.Helper()
	files := jq.Elements(m[keyFiles])
	if len(files) != 1 {
		t.Fatalf("PR.json files = %d, want 1", len(files))
	}
	file, ok := jq.AsMap(files[0])
	if !ok {
		t.Fatalf("PR.json file row = %v, want an object", files[0])
	}
	for field, want := range map[string]string{keyPath: "notes.txt", keyStatus: statusModified} {
		if got := jq.StrOf(file[field]); got != want {
			t.Errorf("file %s = %q, want %q", field, got, want)
		}
	}
}

// publicPRFields are the literals the anonymous document has to normalize into:
// the same keys every provider's PR.json carries.
var publicPRFields = map[string]string{
	keyProvider: providerGitHub,
	keyTitle:    "Retry the upload",
	keyBaseRef:  "main",
	keyHeadRef:  "feat/retry",
	keyBaseSHA:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	keyHeadSHA:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	keyAuthor:   "mate",
	keyState:    stateOpen,
	keyURL:      "https://github.com/acme/app/pull/7",
}

// assertPublicPRFields proves every normalized PR.json field against its
// literal.
func assertPublicPRFields(t *testing.T, m map[string]any) {
	t.Helper()
	for field, want := range publicPRFields {
		if got := jq.StrOf(m[field]); got != want {
			t.Errorf("PR.json %s = %q, want %q", field, got, want)
		}
	}
}

// assertPublicSidecars proves the diff, the file-list receipt, the two raw
// lists, and the truncated thread feed all landed.
func assertPublicSidecars(t *testing.T, ctx2 *fetchRunCtx, dir string) {
	t.Helper()
	if got := readOptional(dir + suffixFullDiff); got != testkitDiff() {
		t.Errorf("full.diff = %q, want the served diff", got)
	}
	assertPublicFilesource(t, dir)
	// The two lists carry different ids so a swapped write fails here: the
	// review list and the inline-comment list are not interchangeable.
	assertListIDs(t, readOptional(dir+suffixReviewsJSON), "reviews.json", 1)
	assertListIDs(t, readOptional(dir+suffixInlineJSON), "inline.json", 2)
	threads := readOptional(dir + suffixThreadsJSON)
	if !strings.Contains(threads, `"truncated": true`) {
		t.Errorf("threads.json = %q, want a truncated empty feed", threads)
	}
	assertWarning(t, *ctx2.runner.warnings, msgPublicThreads)
}

// assertListIDs proves one raw list sidecar holds exactly the served entry.
func assertListIDs(t *testing.T, raw, name string, want int) {
	t.Helper()
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	items := jq.Elements(doc)
	if len(items) != 1 {
		t.Fatalf("%s = %q, want one entry", name, raw)
	}
	entry, ok := jq.AsMap(items[0])
	if !ok {
		t.Fatalf("%s entry = %v, want an object", name, items[0])
	}
	got, ok := jq.IntOf(entry["id"])
	if !ok || got != want {
		t.Errorf("%s id = %v (readable %v), want %d", name, entry["id"], ok, want)
	}
}

// assertPublicFilesource proves the listing receipt names the anonymous API and
// its page size, so the coverage receipt matches the other providers'.
func assertPublicFilesource(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, publicFilesourceFile))
	if err != nil {
		t.Fatalf("read filesource.json: %v", err)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		t.Fatalf("decode filesource.json: %v", err)
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		t.Fatal("filesource.json is not an object")
	}
	if got := jq.StrOf(m[keySource]); got != publicFilesSource {
		t.Errorf("filesource source = %q, want %q", got, publicFilesSource)
	}
}

// TestPublicFailedFilePageMarksTheListCut proves a file-list page that could
// not be read is recorded as an incomplete scan: the warning names the page and
// both receipts (the listing's and PR.json's) carry the cut, so a consumer
// never reads a partial checklist as the whole change.
func TestPublicFailedFilePageMarksTheListCut(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		publicCall(publicTestBase): {stdout: publicMetaDoc},
		publicDiffCall():           {stdout: testkitDiff()},
		publicCall(publicPageURL(publicTestBase+publicFilesPath, 1)):    {fail: true, stderr: "curl: (22) 503"},
		publicCall(publicPageURL(publicTestBase+publicReviewsPath, 1)):  {stdout: `[]`},
		publicCall(publicPageURL(publicTestBase+publicCommentsPath, 1)): {stdout: `[]`},
		"git\x00patch-id\x00--stable":                                   {stdout: testPatchID},
	}}
	ctx2, dir := publicRunCtx(t, cmdr)
	if err := fetchGitHubPublic(context.Background(), ctx2, &Request{Repo: "acme/app", Pr: "7"}); err != nil {
		t.Fatalf("fetchGitHubPublic: %v", err)
	}
	warns := strings.Join(*ctx2.runner.warnings, "\n")
	if !strings.Contains(warns, "file list page 1 failed") {
		t.Errorf("warnings = %q, want the failed file-list page named", warns)
	}
	assertReceiptCut(t, dir+publicFilesourceFile, "filesource.json")
	assertReceiptCut(t, dir+"/PR.json", "PR.json")
}

// assertReceiptCut proves one JSON receipt carries the incomplete-scan block.
func assertReceiptCut(t *testing.T, path, name string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		t.Fatalf("%s is not an object", name)
	}
	if m["truncated"] != true {
		t.Errorf("%s truncated = %v, want true (the page read failed)", name, m["truncated"])
	}
}

// TestPublicMetaShape proves a document that is not a pull request refuses
// before it normalizes: the anonymous read has no host client validating the
// answer, so an empty body or a page missing the number or a commit must not
// produce a bundle. A complete document, with optional fields absent, passes.
func TestPublicMetaShape(t *testing.T) {
	t.Parallel()
	if err := publicMetaShape(mustDoc(t, `{"number":7,"base":{"sha":"bbbb"},"head":{"sha":"aaaa"}}`)); err != nil {
		t.Fatalf("publicMetaShape(complete) = %v, want nil", err)
	}
	for _, tc := range []struct{ name, doc string }{
		{"empty object", `{}`},
		{"no number", `{"base":{"sha":"bbbb"},"head":{"sha":"aaaa"}}`},
		{"zero number", `{"number":0,"base":{"sha":"bbbb"},"head":{"sha":"aaaa"}}`},
		{"no base sha", `{"number":7,"base":{},"head":{"sha":"aaaa"}}`},
		{"no head sha", `{"number":7,"base":{"sha":"bbbb"},"head":{}}`},
		{"no commits", `{"number":7}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertMalformedRefused(t, tc.doc)
		})
	}
}

// assertMalformedRefused proves one malformed document refuses at the shape
// check and never normalizes into a bundle.
func assertMalformedRefused(t *testing.T, doc string) {
	t.Helper()
	err := publicMetaShape(mustDoc(t, doc))
	if err == nil {
		t.Fatalf("publicMetaShape(%s) = nil, want a refusal", doc)
	}
	if !strings.Contains(err.Error(), "public API answered no") {
		t.Errorf("error = %v, want it to name the missing field", err)
	}
	if _, nerr := normalizeGitHubPublic(doc, nil, false); nerr == nil {
		t.Errorf("normalizeGitHubPublic(%s) normalized a malformed document", doc)
	}
}

// mustDoc decodes one test document into the map shape the normalizer reads.
func mustDoc(t *testing.T, raw string) map[string]any {
	t.Helper()
	doc, err := normDoc(raw)
	if err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return doc
}

// TestPublicCurlKeepsStderr proves the anonymous read's flag set keeps curl's
// own error line: a silent -sfL leaves nothing on stderr for a 403, 404, or
// 429, so publicRefusal could never name the real cause.
func TestPublicCurlKeepsStderr(t *testing.T) {
	t.Parallel()
	if !strings.Contains(flagCurlPublicFail, "S") || !strings.Contains(flagCurlPublicFail, "f") {
		t.Errorf("flagCurlPublicFail = %q, want both -S (show errors) and -f (fail on status)", flagCurlPublicFail)
	}
	// The production line a real curl prints for a 404 classifies as not-found.
	ref := publicRefusal("curl: (22) The requested URL returned error: 404", "GitHub PR")
	if !strings.Contains(ref.Message, "missing or private") {
		t.Errorf("message = %q, want the real curl line classified as not-found", ref.Message)
	}
	for _, line := range []string{
		"curl: (22) The requested URL returned error: 403",
		"curl: (22) The requested URL returned error: 429",
	} {
		if got := publicRefusal(line, "GitHub PR"); !strings.Contains(got.Message, "capped at 60 requests/hour") {
			t.Errorf("message(%q) = %q, want the cap advice", line, got.Message)
		}
	}
}

// TestPublicRefusalAdvice proves the refusal names the cause it actually hit: a
// 404 is a missing or private pull request and never carries the hourly-cap
// advice, while 403, 429, and the host's own rate-limit note do.
func TestPublicRefusalAdvice(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		stderr   string
		want     []string
		verboten []string
	}{
		{
			name:     "rate limited 403",
			stderr:   "curl: (22) The requested URL returned error: 403",
			want:     []string{"capped at 60 requests/hour", "gh auth login"},
			verboten: []string{"missing or private"},
		},
		{
			name:   "rate limited 429",
			stderr: "curl: (22) The requested URL returned error: 429",
			want:   []string{"capped at 60 requests/hour"},
		},
		{
			name:   "rate limit note",
			stderr: "API rate limit exceeded for 203.0.113.7",
			want:   []string{"capped at 60 requests/hour", "gh auth login"},
		},
		{
			name:     "not found",
			stderr:   "curl: (22) The requested URL returned error: 404",
			want:     []string{"missing or private", "gh auth login"},
			verboten: []string{"capped at", "wait"},
		},
		{
			name:     "other failure",
			stderr:   "curl: (7) Failed to connect to api.github.com port 443",
			want:     []string{"Failed to connect"},
			verboten: []string{"capped at", "missing or private", "gh auth login"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertRefusalAdvice(t, tc.stderr, tc.want, tc.verboten)
		})
	}
}

// assertRefusalAdvice proves one failure's message names what it must and
// carries no remedy belonging to another cause.
func assertRefusalAdvice(t *testing.T, stderr string, want, verboten []string) {
	t.Helper()
	ref := publicRefusal(stderr, "GitHub PR")
	if ref.Code != CodeFetchFailed {
		t.Errorf("code = %q, want %q", ref.Code, CodeFetchFailed)
	}
	for _, line := range want {
		if !strings.Contains(ref.Message, line) {
			t.Errorf("message = %q, want it to name %q", ref.Message, line)
		}
	}
	for _, line := range verboten {
		if strings.Contains(ref.Message, line) {
			t.Errorf("message = %q, want no %q for this cause", ref.Message, line)
		}
	}
}

// fullPageDoc renders one full (per_page) listing page: the walk only sees a
// short page as the end, so a page of exactly publicPerPage keeps it going.
func fullPageDoc(start int) string {
	rows := make([]string, 0, publicPerPage)
	for i := range publicPerPage {
		rows = append(rows, fmt.Sprintf(`{"id":%d,"filename":"f%d.txt","status":"modified"}`, start+i, start+i))
	}
	return "[" + strings.Join(rows, ",") + "]"
}

// TestPublicFileWalkExhaustedIsTruncated proves a listing that runs to the page
// ceiling without a short page is recorded as truncated: every page read
// cleanly, so nothing else would mark the >1000-file PR as a partial checklist.
func TestPublicFileWalkExhaustedIsTruncated(t *testing.T) {
	t.Parallel()
	out := map[string]scriptResult{}
	for page := 1; page <= providerMaxPages; page++ {
		out[publicCall(publicPageURL(publicTestBase+publicFilesPath, page))] = scriptResult{stdout: fullPageDoc(page * publicPerPage)}
	}
	ctx2, dir := publicRunCtx(t, &scriptCommander{t: t, out: out})
	files, truncated, ref := publicFiles(t.Context(), ctx2.runner, dir, publicTestBase)
	if ref != nil {
		t.Fatalf("publicFiles refused: %v", ref)
	}
	if !truncated {
		t.Error("truncated = false, want true for a walk that ran out of pages")
	}
	if len(files) != providerMaxPages*publicPerPage {
		t.Errorf("files = %d, want %d", len(files), providerMaxPages*publicPerPage)
	}
	assertReceiptCut(t, dir+publicFilesourceFile, "filesource.json")
}

// TestPublicListWalkExhaustedWarns proves an exhausted review/comment list walk
// is never silent: the walk stops at the page ceiling and warns that the list
// is incomplete, so a truncated thread feed cannot read as the whole list.
func TestPublicListWalkExhaustedWarns(t *testing.T) {
	t.Parallel()
	out := map[string]scriptResult{}
	for page := 1; page <= providerMaxPages; page++ {
		out[publicCall(publicPageURL(publicTestBase+publicReviewsPath, page))] = scriptResult{stdout: fullPageDoc(page * publicPerPage)}
		out[publicCall(publicPageURL(publicTestBase+publicCommentsPath, page))] = scriptResult{stdout: `[]`}
	}
	ctx2, dir := publicRunCtx(t, &scriptCommander{t: t, out: out})
	if ref := publicLists(t.Context(), ctx2.runner, dir, publicTestBase); ref != nil {
		t.Fatalf("publicLists refused: %v", ref)
	}
	warns := strings.Join(*ctx2.runner.warnings, "\n")
	want := "is longer than 10 pages — the list is incomplete"
	if !strings.Contains(warns, want) {
		t.Errorf("warnings = %q, want it to carry %q", warns, want)
	}
	if strings.Count(warns, want) != 1 {
		t.Errorf("warnings = %q, want the incomplete-list note exactly once (the comments list ended short)", warns)
	}
}

// TestPublicMergedState proves a merged pull request records "merged": the list
// endpoint says "closed" and carries the fact in `merged`, so reading state
// alone would file every merged PR as closed.
func TestPublicMergedState(t *testing.T) {
	t.Parallel()
	const mergedDoc = `{"number":7,"title":"Retry the upload","state":"closed","merged":true,` +
		`"html_url":"https://github.com/acme/app/pull/7",` +
		`"base":{"ref":"main","sha":"bbbb"}, "head":{"ref":"feat/retry","sha":"aaaa"}}`
	doc, err := normalizeGitHubPublic(mergedDoc, nil, false)
	if err != nil {
		t.Fatalf("normalizeGitHubPublic: %v", err)
	}
	if got, _ := doc.Get(keyState); jq.StrOf(got) != stateMerged {
		t.Errorf("state = %v, want %q", got, stateMerged)
	}
	// A closed PR that never merged still reads closed.
	closed, err := normalizeGitHubPublic(
		`{"number":7,"state":"closed","merged":false,"base":{"sha":"b"},"head":{"sha":"a"}}`, nil, false)
	if err != nil {
		t.Fatalf("normalizeGitHubPublic (closed): %v", err)
	}
	if got, _ := closed.Get(keyState); jq.StrOf(got) != stateClosed {
		t.Errorf("closed state = %v, want %q", got, stateClosed)
	}
}

// TestPublicCancelIsNotARateLimit proves a stopped context is the cancel
// refusal, not the anonymous-cap story: the meta read and the diff read both
// fail once the context is done, and neither may claim the hourly cap or offer
// `gh auth login` as the remedy for a read the operator stopped.
func TestPublicCancelIsNotARateLimit(t *testing.T) {
	t.Parallel()
	reads := map[string]func(context.Context, *fetchRunCtx) error{
		"meta read": func(ctx context.Context, ctx2 *fetchRunCtx) error {
			_, err := publicMeta(ctx, ctx2.runner, ctx2.dir, "acme/app", "7", publicTestBase)
			return err
		},
		"diff read": func(ctx context.Context, ctx2 *fetchRunCtx) error {
			return publicDiff(ctx, ctx2.runner, ctx2.dir, publicTestBase)
		},
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertCancelRefusal(t, read)
		})
	}
}

// assertCancelRefusal proves one read stopped by a done context answers the
// cancel refusal, never the anonymous-cap story.
func assertCancelRefusal(t *testing.T, read func(context.Context, *fetchRunCtx) error) {
	t.Helper()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		publicCall(publicTestBase): {fail: true, stderr: "curl: (22) 403"},
		publicDiffCall():           {fail: true, stderr: "curl: (22) 403"},
	}}
	ctx2, _ := publicRunCtx(t, cmdr)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ref, ok := errors.AsType[*core.RefusalError](read(ctx, ctx2))
	if !ok {
		t.Fatalf("err = %v, want a typed refusal", read(ctx, ctx2))
	}
	if ref.Code != codeCanceled {
		t.Errorf("code = %q, want %q", ref.Code, codeCanceled)
	}
	if strings.Contains(ref.Message, "capped at") || strings.Contains(ref.Message, "gh auth login") {
		t.Errorf("message = %q, want no anonymous rate-limit remedy for a cancel", ref.Message)
	}
}

// TestPublicPagingStopsOnCancel proves the two paged reads surface the cancel
// instead of writing a short list: the first page fails with a done context, so
// the walk refuses rather than recording an empty reviews.json.
func TestPublicPagingStopsOnCancel(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		publicCall(publicPageURL(publicTestBase+publicFilesPath, 1)):   {fail: true, stderr: "curl: (22) 403"},
		publicCall(publicPageURL(publicTestBase+publicReviewsPath, 1)): {fail: true},
	}}
	ctx2, dir := publicRunCtx(t, cmdr)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, ref := publicFiles(ctx, ctx2.runner, dir, publicTestBase); ref == nil || ref.Code != codeCanceled {
		t.Errorf("publicFiles ref = %v, want %q", ref, codeCanceled)
	}
	if ref := publicLists(ctx, ctx2.runner, dir, publicTestBase); ref == nil || ref.Code != codeCanceled {
		t.Errorf("publicLists ref = %v, want %q", ref, codeCanceled)
	}
	for _, name := range []string{suffixReviewsJSON, suffixInlineJSON} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s was written for a canceled read", name)
		}
	}
}

// TestPublicGitHubBackendNeedsCurl proves the anonymous transport is required:
// without curl the read is the missing-tool kind, not a refusal about a host.
func TestPublicGitHubBackendNeedsCurl(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}, tools: map[string]bool{}}
	ctx2, _ := publicRunCtx(t, cmdr)
	err := fetchGitHubPublic(context.Background(), ctx2, &Request{Repo: "acme/app", Pr: "7"})
	mt, ok := errors.AsType[*core.MissingToolError](err)
	if !ok || mt.Tool != binCurl {
		t.Fatalf("err = %v, want a missing-tool error for curl", err)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none before the tool gate", cmdr.calls)
	}
}

// TestFetchProviderBundleDispatchesPublicRead proves the provider switch sends
// a github run with PublicRead set to the anonymous backend (and not to the
// unknown-provider refusal, which is what github takes without the flag).
func TestFetchProviderBundleDispatchesPublicRead(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}, tools: map[string]bool{}}
	ctx2, _ := publicRunCtx(t, cmdr)
	_, _, err := fetchProviderBundle(context.Background(), ctx2,
		&Request{Repo: "acme/app", Pr: "7"}, &Flags{Provider: providerGitHub, PublicRead: true})
	if _, ok := errors.AsType[*core.MissingToolError](err); !ok {
		t.Fatalf("public read err = %v, want the missing-curl tool gate", err)
	}
	_, _, err = fetchProviderBundle(context.Background(), ctx2,
		&Request{Repo: "acme/app", Pr: "7"}, &Flags{Provider: providerGitHub})
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok || ref.Code != codeBadProvider {
		t.Fatalf("plain github run err = %v, want bad-provider", err)
	}
}

// TestPublicBackendSelection proves the fetch backend routes a github run with
// PublicRead to the shared provider tail instead of the gh path: the gh path
// would gate on gh, and this commander has no gh at all.
func TestPublicBackendSelection(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{t: t, tools: map[string]bool{binCurl: true}, out: map[string]scriptResult{
		publicCall(publicTestBase): {stderr: "curl: (22) 404", fail: true},
	}}
	ctx2, _ := publicRunCtx(t, cmdr)
	st := &state{req: &Request{Repo: "acme/app", Pr: "7"}}
	_, _, err := st.backend(context.Background(), ctx2, st.req,
		&Flags{Provider: providerGitHub, PublicRead: true}, &budgetState{})
	// The gh path gates on gh before anything else, and this commander has none;
	// the public path asks curl first, so a fetch-failed from the anonymous read
	// is the proof the shared provider tail was reached.
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok || ref.Code != CodeFetchFailed || !strings.Contains(ref.Message, "public API") {
		t.Fatalf("backend err = %v, want the anonymous read's own refusal", err)
	}
	if len(cmdr.calls) == 0 || cmdr.calls[0][0] != binCurl {
		t.Fatalf("first call = %q, want the anonymous curl read", cmdr.calls)
	}
}

// TestNoRepoLayerReadsAbsentControls proves the two postures on one unreadable
// base: the optional read records an honest empty snapshot, and the shipped
// strict read still refuses controls-unavailable. A demo run must never relax
// the refusal a real fetch depends on.
func TestNoRepoLayerReadsAbsentControls(t *testing.T) {
	t.Parallel()
	const base = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dir := t.TempDir()
	cmdr := failGit(t, dir, base)
	s, _ := newTestSession(cmdr)
	ctx2 := &fetchRunCtx{runner: s, dir: dir, runCfg: &runConfig{optional: true}}
	if err := resolveControls(context.Background(), ctx2, "", dir, base, false, false); err != nil {
		t.Fatalf("optional controls: %v", err)
	}
	assertAbsentControls(t, s, "an unreadable base")
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none: a run with no repository layer lists nothing", cmdr.calls)
	}
	strict, _ := newTestSession(failGit(t, dir, base))
	ctx2 = &fetchRunCtx{runner: strict, dir: dir, runCfg: &runConfig{}}
	err := resolveControls(context.Background(), ctx2, "", dir, base, false, false)
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok || ref.Code != controls.CodeUnavailable {
		t.Fatalf("strict controls err = %v, want %s", err, controls.CodeUnavailable)
	}
}

// TestOptionalControlsIgnoreACloneHoldingTheBase proves the optional posture is
// not "a listing that happens to fail": with a git that answers the base as a
// real repository would (verdict demo's URL form run inside a clone holding the
// PR's base commit), the snapshot stays empty and git is never asked.
func TestOptionalControlsIgnoreACloneHoldingTheBase(t *testing.T) {
	t.Parallel()
	const base = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dir := t.TempDir()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		"git\x00-C\x00" + dir + "\x00ls-tree\x00-lz\x00" + base + "\x00--\x00" +
			strings.Join(controls.Inventory, "\x00"): {
			stdout: controls.Inventory[0] + " blob\x00123\t" + controls.Inventory[0] + "\x00",
		},
	}}
	s, _ := newTestSession(cmdr)
	ctx2 := &fetchRunCtx{runner: s, dir: dir, runCfg: &runConfig{optional: true}}
	if err := resolveControls(context.Background(), ctx2, "", dir, base, false, false); err != nil {
		t.Fatalf("optional controls: %v", err)
	}
	assertAbsentControls(t, s, "a clone holding the base commit")
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none: the checkout's controls are not this PR's", cmdr.calls)
	}
}

// failGit scripts the snapshot's one listing as a checkout-less run's failure.
func failGit(t *testing.T, dir, base string) *scriptCommander {
	t.Helper()
	return &scriptCommander{t: t, out: map[string]scriptResult{
		"git\x00-C\x00" + dir + "\x00ls-tree\x00-lz\x00" + base + "\x00--\x00" +
			strings.Join(controls.Inventory, "\x00"): {
			stderr: "fatal: not a git repository", fail: true,
		},
	}}
}

// assertAbsentControls proves one snapshot holds no control at all.
func assertAbsentControls(t *testing.T, s *session, reason string) {
	t.Helper()
	if used := s.controls.Used(); used != 0 {
		t.Errorf("optional snapshot used = %d files, want none (%s)", used, reason)
	}
	if _, ok := s.controls.ReadFile("config.json"); ok {
		t.Errorf("optional snapshot carries a config control from %s", reason)
	}
}

// TestNoRepoLayerCancelIsNotAnAbsentLayer proves a stopped optional read is the
// cancel, never an absent repository layer: the listing failure must not be
// swallowed into an empty snapshot when the operator interrupted the read.
func TestNoRepoLayerCancelIsNotAnAbsentLayer(t *testing.T) {
	t.Parallel()
	const base = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dir := t.TempDir()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		"git\x00-C\x00" + dir + "\x00ls-tree\x00-lz\x00" + base + "\x00--\x00" +
			strings.Join(controls.Inventory, "\x00"): {fail: true},
	}}
	s, _ := newTestSession(cmdr)
	ctx2 := &fetchRunCtx{runner: s, dir: dir, runCfg: &runConfig{optional: true}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := resolveControls(ctx, ctx2, "", dir, base, false, false)
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok {
		t.Fatalf("err = %v, want a typed refusal", err)
	}
	if ref.Code != controls.CodeCanceled {
		t.Errorf("code = %q, want %q for a stopped optional read", ref.Code, controls.CodeCanceled)
	}
}

// TestReadBaseOptionalReadsNoPolicy proves the configuration read follows the
// same posture: the optional sibling answers "no repository policy" without
// touching git, while the shipped reader refuses the same unreadable base.
func TestReadBaseOptionalReadsNoPolicy(t *testing.T) {
	t.Parallel()
	const base = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dir := t.TempDir()
	key := "git\x00-C\x00" + dir + "\x00ls-tree\x00-lz\x00" + base + "\x00--\x00" + config.ConfigPath
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		key: {stderr: "fatal: not a git repository", fail: true},
	}}
	found, ref := config.ReadBaseOptional(context.Background(), cmdr, "", dir, base)
	if ref != nil {
		t.Fatalf("optional base: %v", ref)
	}
	if found.Present {
		t.Error("optional base read a document, want none")
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none: an absent repository layer reads no checkout at all", cmdr.calls)
	}
	if _, ref := config.ReadBase(context.Background(), cmdr, "", dir, base); ref == nil {
		t.Fatal("strict base read succeeded on an unreadable base")
	}
}

// TestOptionalBaseIgnoresACloneHoldingTheBaseCommit proves the optional posture
// is not a read that happens to fail: with a git that answers the base commit
// (a local clone holding it), the URL form still resolves no base policy, so a
// demo never adopts the checkout's controls for someone else's pull request.
func TestOptionalBaseIgnoresACloneHoldingTheBaseCommit(t *testing.T) {
	t.Parallel()
	const base = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dir := t.TempDir()
	key := "git\x00-C\x00" + dir + "\x00ls-tree\x00-lz\x00" + base + "\x00--\x00" + config.ConfigPath
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		key: {stdout: config.ConfigPath + " blob\x00123\t" + config.ConfigPath + "\x00"},
	}}
	rc := &runConfig{optional: true, root: dir}
	found, ref := rc.readBase(context.Background(), cmdr, "acme/app", base)
	if ref != nil {
		t.Fatalf("optional base: %v", ref)
	}
	if found.Present {
		t.Error("optional base read the checkout's policy, want the empty result")
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none for a run with no repository layer", cmdr.calls)
	}
}
