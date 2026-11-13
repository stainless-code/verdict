package fetchcore

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	hostRepo = "o/r"
	hostPR   = "7"
	// gitlabAPI is the project-scoped API path the diffs read walks.
	gitlabAPI = "glab\x00api\x00projects/11/merge_requests/7/"
	// forgejoAPI is the pull-scoped API base the review reads walk.
	forgejoAPI = "https://example.invalid/api/v1/repos/o/r/pulls/7"
)

// hostRequest is the fetch request every host fixture answers.
func hostRequest() *core.FetchRequest { return &core.FetchRequest{Repo: hostRepo, Pr: hostPR} }

// hostCtx builds a provider run context over a scripted commander: the bundle
// directory, the owner/repo positional, and the PR number.
func hostCtx(t *testing.T, cmdr Commander, dir string) *fetchRunCtx {
	t.Helper()
	s, _ := newTestSession(cmdr)
	return &fetchRunCtx{runner: s, repo: hostRepo, pr: hostPR, dir: dir}
}

// hostObj decodes one bundle record written by a run, failing on a missing or
// malformed file.
func hostObj(t *testing.T, path string) *jq.Obj {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		t.Fatalf("%s is not one JSON document: %v", path, err)
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		t.Fatalf("%s is not a JSON object", path)
	}
	return obj
}

// hostField renders one record field the way jq -r would, so an expectation
// reads as one literal ("main", "true", "3").
func hostField(t *testing.T, obj *jq.Obj, key string) string {
	t.Helper()
	v, ok := obj.Get(key)
	if !ok {
		t.Fatalf("record carries no %q field", key)
	}
	return jq.R(v)
}

// hostFiles lists the file rows as `path:status:adds:dels`.
func hostFiles(t *testing.T, obj *jq.Obj) []string {
	t.Helper()
	v, _ := obj.Get(keyFiles)
	rows := jq.Elements(v)
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		m := objOrMap(row)
		if m == nil {
			t.Fatalf("file row is not an object: %v", row)
		}
		out = append(out, fmt.Sprintf("%s:%s:%s:%s",
			jq.R(m[keyPath]), jq.R(m[keyStatus]), jq.R(m[keyAdditions]), jq.R(m[keyDeletions])))
	}
	return out
}

// hostGet reads one record field, nil when absent.
func hostGet(t *testing.T, path, key string) any {
	t.Helper()
	v, _ := hostObj(t, path).Get(key)
	return v
}

// hostTruncated reads the feed's truncated flag the way jq -r would.
func hostTruncated(t *testing.T, dir string) string {
	t.Helper()
	return hostField(t, hostObj(t, dir+suffixThreadsJSON), "truncated")
}

// assertWarning fails unless one warning carries substr.
func assertWarning(t *testing.T, warnings []string, substr string) {
	t.Helper()
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return
		}
	}
	t.Fatalf("warnings = %q, want one carrying %q", warnings, substr)
}

// assertPR compares a normalized PR record against one host leg's literals.
func assertPR(t *testing.T, pr *jq.Obj, fields map[string]string, files []string) {
	t.Helper()
	for key, want := range fields {
		if got := hostField(t, pr, key); got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
	if got := hostFiles(t, pr); !slices.Equal(got, files) {
		t.Errorf("files = %v, want %v", got, files)
	}
}

// gitlabScripts scripts one complete GitLab read; diffs is the payload
// `glab api --paginate` emits (one JSON array per page, adjacent).
func gitlabScripts(dir, diffs string) map[string]scriptResult {
	return map[string]scriptResult{
		"glab\x00mr\x00view\x007\x00-R\x00o/r\x00-F\x00json": {stdout: `{"iid":7,"title":"T","description":"B",` +
			`"state":"opened","draft":false,"author":{"username":"alice"},` +
			`"source_branch":"feat","target_branch":"main","web_url":"https://gl.example/o/r/-/merge_requests/7",` +
			`"sha":"head111","diff_refs":{"base_sha":"base111","head_sha":"head111","start_sha":"start111"}}`},
		"glab\x00api\x00projects/o%2Fr\x00--jq\x00.id": {stdout: "11\n"},
		"glab\x00mr\x00diff\x007\x00-R\x00o/r\x00--raw": {stdout: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n" +
			"@@ -1 +1 @@\n-x\n+y\n"},
		"git\x00apply\x00--numstat\x00" + dir + "/full.diff": {stdout: "1\t1\ta.go\n2\t1\tb.go\n"},
		gitlabAPI + "diffs\x00--paginate":                    {stdout: diffs},
		gitlabAPI + "notes?per_page=100&page=1":              {stdout: `[]`},
		gitlabAPI + "discussions?per_page=100&page=1":        {stdout: `[]`},
	}
}

// gitlabPRFiles runs one GitLab read and returns PR.json's file rows plus the
// warnings the run raised.
func gitlabPRFiles(t *testing.T, dir, diffs string) ([]string, []string) {
	t.Helper()
	ctx := hostCtx(t, &scriptCommander{t: t, out: gitlabScripts(dir, diffs)}, dir)
	if _, err := fetchGitLab(context.Background(), ctx, hostRequest(), &core.FetchFlags{}); err != nil {
		t.Fatalf("fetchGitLab: %v", err)
	}
	return hostFiles(t, hostObj(t, dir+suffixPRJSON)), *ctx.runner.warnings
}

// TestGitLabMultiPageDiffsKeepEveryFile proves a paginated diff list stays
// whole. `glab api --paginate` emits one adjacent JSON array per page — not a
// single document — and dropping that payload left PR.json with no files at
// all, so a coverage manifest claimed the MR touched nothing.
func TestGitLabMultiPageDiffsKeepEveryFile(t *testing.T) {
	t.Parallel()
	const pageA = `[{"old_path":"a.go","new_path":"a.go"}]`
	const pageB = `[{"old_path":"b.go","new_path":"b.go"}]`
	for _, tc := range []struct {
		name  string
		diffs string
		files []string
	}{
		{"single page", pageA, []string{"a.go:modified:1:1"}},
		{"two pages", pageA + pageB, []string{"a.go:modified:1:1", "b.go:modified:2:1"}},
		{"changes envelope", `{"changes":` + pageA + `}`, []string{"a.go:modified:1:1"}},
		{"newline between pages", pageA + "\n" + pageB + "\n", []string{"a.go:modified:1:1", "b.go:modified:2:1"}},
		{"empty page between", pageA + `[]` + pageB, []string{"a.go:modified:1:1", "b.go:modified:2:1"}},
		{"empty merge request", `[]`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertGitLabDiffFiles(t, tc.diffs, tc.files)
		})
	}
}

// assertGitLabDiffFiles reads one diffs payload and checks the file rows and
// the absence of a read warning.
func assertGitLabDiffFiles(t *testing.T, diffs string, files []string) {
	t.Helper()
	got, warnings := gitlabPRFiles(t, t.TempDir(), diffs)
	if !slices.Equal(got, files) {
		t.Errorf("files = %v, want %v", got, files)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
}

// TestGitLabChangedFileReadFailureWarns proves a failed diffs+changes read is
// visible instead of leaving a PR.json that reads like a file-less MR.
func TestGitLabChangedFileReadFailureWarns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scripts := gitlabScripts(dir, `[]`)
	for _, leg := range []string{"diffs", "changes"} {
		scripts[gitlabAPI+leg+"\x00--paginate"] = scriptResult{stderr: "glab: 403 Forbidden", fail: true}
	}
	ctx := hostCtx(t, &scriptCommander{t: t, out: scripts}, dir)
	if _, err := fetchGitLab(context.Background(), ctx, hostRequest(), &core.FetchFlags{}); err != nil {
		t.Fatalf("fetchGitLab: %v", err)
	}
	assertWarning(t, *ctx.runner.warnings, "changed-file read failed")
	if files := hostFiles(t, hostObj(t, dir+suffixPRJSON)); len(files) != 0 {
		t.Errorf("files = %v, want none", files)
	}
}

// TestGitLabPRFallbacks pins the field fallbacks the MR record depends on: the
// head SHA prefers the MR sha and falls back to the diff ref, the number
// prefers iid over id, and the base SHA prefers base_sha over start_sha.
func TestGitLabPRFallbacks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, meta, number, baseSHA, headSHA string
	}{
		{
			name: "sha and iid win",
			meta: `{"iid":7,"id":100,"sha":"head111",` +
				`"diff_refs":{"base_sha":"base111","head_sha":"head222","start_sha":"start111"}}`,
			number: "7", baseSHA: "base111", headSHA: "head111",
		},
		{
			name:   "diff refs carry the gap",
			meta:   `{"id":100,"diff_refs":{"head_sha":"head222","start_sha":"start111"}}`,
			number: "100", baseSHA: "start111", headSHA: "head222",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertGitLabFallback(t, tc.meta, tc.number, tc.baseSHA, tc.headSHA)
		})
	}
}

// assertGitLabFallback checks one MR payload against the expected number and
// SHAs.
func assertGitLabFallback(t *testing.T, meta, number, baseSHA, headSHA string) {
	t.Helper()
	pr, err := normalizeGitLab(meta, `[]`, nil)
	if err != nil {
		t.Fatalf("normalizeGitLab: %v", err)
	}
	for key, want := range map[string]string{keyNumber: number, keyBaseSHA: baseSHA, keyHeadSHA: headSHA} {
		if got := hostField(t, pr, key); got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
}

// cafeReviewCall renders the reviews page call for one cursor.
func cafeReviewCall(cursor string) string {
	call := "cafe\x00pr\x00reviews\x007\x00-R\x00o/r\x00--json"
	if cursor == "" {
		return call
	}
	return call + "\x00--cursor\x00" + cursor
}

// cafeScripts is the rest of one GitCafe read: the PR view, the diff, and the
// comments page; extra adds the reviews pages under test.
func cafeScripts(extra map[string]scriptResult) map[string]scriptResult {
	out := map[string]scriptResult{
		"cafe\x00pr\x00view\x007\x00-R\x00o/r\x00--json": {stdout: `{"data":{"resource":{"number":7,"title":"T",` +
			`"description":"B","state":"open","targetBranch":"main","sourceBranch":"feat",` +
			`"observedBaseOid":"base111","sourceOid":"head111","draft":false,` +
			`"author":{"handle":"alice"}}}}`},
		"cafe\x00pr\x00diff\x007\x00-R\x00o/r\x00--patch\x00--json": {stdout: `{"data":{"items":[` +
			`{"path":"a.go","status":"modified","patch":"@@ -1 +1 @@\n-x\n+y"}]}}`},
		"cafe\x00pr\x00comments\x007\x00-R\x00o/r\x00--filter\x00all\x00--json": {stdout: `{"data":{"items":[` +
			`{"id":"c1","body":"comment","author":{"handle":"rev"}}]}}`},
	}
	maps.Copy(out, extra)
	return out
}

// cafeReviewPage is one reviews page holding the given review ids and naming
// the next cursor ("" ends the walk).
func cafeReviewPage(ids []string, next string) scriptResult {
	rows := jsonRows(len(ids), func(i int) string {
		return fmt.Sprintf(`{"id":%q,"body":"review","author":{"handle":"rev"}}`, ids[i])
	})
	return scriptResult{stdout: fmt.Sprintf(`{"data":{"items":%s},"next":%q}`, rows, next)}
}

// cafeCappedReviews is the 20-page walk a host cap ends: every page is full
// and names a live cursor, so the walk stops at cafeMaxPages.
func cafeCappedReviews() map[string]scriptResult {
	out := map[string]scriptResult{}
	cursor := ""
	for page := 1; page <= cafeMaxPages; page++ {
		out[cafeReviewCall(cursor)] = cafeReviewPage([]string{fmt.Sprintf("r%d", page)}, fmt.Sprintf("c%d", page+1))
		cursor = fmt.Sprintf("c%d", page+1)
	}
	return out
}

// cafeFetch runs one GitCafe read and returns the warnings it raised.
func cafeFetch(t *testing.T, dir string, extra map[string]scriptResult) []string {
	t.Helper()
	ctx := hostCtx(t, &scriptCommander{t: t, out: cafeScripts(extra)}, dir)
	if err := fetchGitCafe(context.Background(), ctx, hostRequest()); err != nil {
		t.Fatalf("fetchGitCafe: %v", err)
	}
	return *ctx.runner.warnings
}

// TestGitCafeIncompletePagingWarnsAndTruncates proves a reviews walk that stops
// early is reported twice over — an envelope warning and truncated:true in
// threads.json — for all three ways the walk ends early: a failed page, an
// unreadable page, and the cursor cap.
func TestGitCafeIncompletePagingWarnsAndTruncates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		out  map[string]scriptResult
		name string
	}{
		{
			name: "failed second page",
			out: map[string]scriptResult{
				cafeReviewCall(""):   cafeReviewPage([]string{"r1"}, "c2"),
				cafeReviewCall("c2"): {stderr: "cafe: 500 upstream unavailable", fail: true},
			},
		},
		{
			name: "unreadable second page",
			out: map[string]scriptResult{
				cafeReviewCall(""):   cafeReviewPage([]string{"r1"}, "c2"),
				cafeReviewCall("c2"): {stdout: "cafe: not json at all"},
			},
		},
		{name: "cursor cap", out: cafeCappedReviews()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertCafePagingIncomplete(t, tc.out)
		})
	}
}

// assertCafePagingIncomplete checks that one incomplete reviews walk warns, is
// marked truncated, and still wrote a readable reviews record.
func assertCafePagingIncomplete(t *testing.T, out map[string]scriptResult) {
	t.Helper()
	dir := t.TempDir()
	assertWarning(t, cafeFetch(t, dir, out), "gitcafe reviews paging")
	if got := hostTruncated(t, dir); got != "true" {
		t.Errorf("threads.json truncated = %s, want true", got)
	}
	if _, err := os.ReadFile(dir + suffixReviewsJSON); err != nil {
		t.Fatalf("read reviews.json: %v", err)
	}
}

// TestGitCafeFailedPageKeepsReadPages proves the walk keeps what it read: a
// failed or unreadable second page must not collapse the feed to nothing.
func TestGitCafeFailedPageKeepsReadPages(t *testing.T) {
	t.Parallel()
	for name, second := range map[string]scriptResult{
		"failed call": {stderr: "cafe: 500 upstream unavailable", fail: true},
		"unreadable":  {stdout: "cafe: not json at all"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertCafeKeepsFirstReview(t, second)
		})
	}
}

// assertCafeKeepsFirstReview runs a walk whose second page is the given
// result and checks the first page's review survives into threads.json.
func assertCafeKeepsFirstReview(t *testing.T, second scriptResult) {
	t.Helper()
	dir := t.TempDir()
	cafeFetch(t, dir, map[string]scriptResult{
		cafeReviewCall(""):   cafeReviewPage([]string{"r1"}, "c2"),
		cafeReviewCall("c2"): second,
	})
	threads := jq.Elements(hostGet(t, dir+suffixThreadsJSON, "threads"))
	if len(threads) == 0 {
		t.Fatal("threads = [], want the first page's review kept")
	}
	if id := jq.R(objOrMap(threads[0])["id"]); id != "r1" {
		t.Errorf("first thread id = %s, want the first page's review r1", id)
	}
}

// forgejoReviewCall renders the review page call for one page number.
func forgejoReviewCall(page int) string {
	return forgejoCall(fmt.Sprintf("%s/reviews?limit=50&page=%d", forgejoAPI, page))
}

// forgejoFullReviews is a page-walk that ends on the provider cap: all
// providerMaxPages pages are full.
func forgejoFullReviews() map[string]scriptResult {
	out := map[string]scriptResult{forgejoCall(forgejoAPI + "/reviews/1/comments"): {stdout: `[]`}}
	for page := 1; page <= providerMaxPages; page++ {
		out[forgejoReviewCall(page)] = scriptResult{stdout: jsonRows(50, func(int) string {
			return `{"id":1,"body":"summary","user":{"login":"rev"}}`
		})}
	}
	return out
}

// TestForgejoCappedReviewsTruncateFeed proves a capped review page walk reaches
// threads.json, not just the envelope: the schema's truncated flag is exactly
// the "a page walk stopped at the provider cap" signal.
func TestForgejoCappedReviewsTruncateFeed(t *testing.T) {
	t.Parallel()
	oneShortPage := map[string]scriptResult{
		forgejoReviewCall(1):                            {stdout: `[{"id":1,"body":"summary","user":{"login":"rev"}}]`},
		forgejoCall(forgejoAPI + "/reviews/1/comments"): {stdout: `[]`},
	}
	assertForgejoFeed(t, "one short page", oneShortPage, "false", false)
	assertForgejoFeed(t, "cap", forgejoFullReviews(), "true", true)
}

// assertForgejoFeed reads the reviews feed and checks its truncated flag and
// whether the run warned.
func assertForgejoFeed(t *testing.T, name string, out map[string]scriptResult, truncated string, warned bool) {
	t.Helper()
	dir := t.TempDir()
	s, warns := newTestSession(&scriptCommander{t: t, out: out})
	over := forgejoCred(t)
	if err := forgejoThreads(context.Background(), s, dir, over, forgejoAPI, hostPR, `{"head":{"sha":"head111"}}`); err != nil {
		t.Fatalf("%s: forgejoThreads: %v", name, err)
	}
	if got := hostTruncated(t, dir); got != truncated {
		t.Errorf("%s: threads.json truncated = %s, want %s", name, got, truncated)
	}
	if got := len(*warns) > 0; got != warned {
		t.Errorf("%s: warnings = %q, want warned=%v", name, *warns, warned)
	}
}

// TestNormalizeProviderPRFields proves every hosted PR normalizes to the one
// PR.json shape the manual promises: the same keys, per-host value sources, and
// the file-stat rollup. The expectations are literals read off each API's own
// payload shape.
func TestNormalizeProviderPRFields(t *testing.T) {
	t.Parallel()
	for name, leg := range map[string]func(*testing.T){
		"gitlab":    gitlabPRLeg,
		"gitcafe":   gitcafePRLeg,
		"forgejo":   forgejoPRLeg,
		"bitbucket": bitbucketPRLeg,
		"azure":     azurePRLeg,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			leg(t)
		})
	}
}

func gitlabPRLeg(t *testing.T) {
	t.Helper()
	pr, err := normalizeGitLab(`{"iid":7,"id":100,"title":"MR","description":"body","state":"opened",`+
		`"draft":true,"author":{"username":"alice","name":"Alice"},"target_branch":"main",`+
		`"source_branch":"feat","web_url":"https://gl.example/o/r/-/merge_requests/7",`+
		`"diff_refs":{"base_sha":"base111","head_sha":"head222","start_sha":"start111"}}`,
		`[{"old_path":"a.go","new_path":"a.go"},`+
			`{"old_path":"old.go","new_path":"new.go","renamed_file":true},`+
			`{"old_path":"gone.go","new_path":"/dev/null","deleted_file":true}]`,
		map[string]map[string]int64{"a.go": {keyAdds: 3, keyDels: 1}, "new.go": {keyAdds: 5, keyDels: 2}})
	if err != nil {
		t.Fatalf("normalizeGitLab: %v", err)
	}
	assertPR(t, pr, map[string]string{
		keyProvider: "gitlab", keyNumber: "7", keyTitle: "MR", keyBody: "body",
		keyBaseRef: "main", keyHeadRef: "feat", keyBaseSHA: "base111", keyHeadSHA: "head222",
		keyAuthor: "alice", keyURL: "https://gl.example/o/r/-/merge_requests/7",
		keyState: "open", keyDraft: "true", keyAdditions: "8", keyDeletions: "3",
	}, []string{"a.go:modified:3:1", "new.go:renamed:5:2", "gone.go:deleted:0:0"})
}

func gitcafePRLeg(t *testing.T) {
	t.Helper()
	pr, err := normalizeGitCafe(`{"data":{"url":"https://cafe.example/o/r/7","resource":{`+
		`"number":9,"title":"CF","description":"body","state":"merged",`+
		`"targetBranch":"main","sourceBranch":"feat","observedBaseOid":"base111",`+
		`"sourceOid":"head222","author":{"handle":"alice"}}}}`,
		`{"data":{"items":[{"path":"a.go","status":"modified","additions":4,"deletions":2},`+
			`{"path":"b.go","additions":1}]}}`)
	if err != nil {
		t.Fatalf("normalizeGitCafe: %v", err)
	}
	assertPR(t, pr, map[string]string{
		keyProvider: "gitcafe", keyNumber: "9", keyTitle: "CF", keyBody: "body",
		keyBaseRef: "main", keyHeadRef: "feat", keyBaseSHA: "base111", keyHeadSHA: "head222",
		keyAuthor: "alice", keyURL: "https://cafe.example/o/r/7",
		// The payload omits draft, so the shared default renders false.
		keyState: "merged", keyDraft: "false", keyAdditions: "5", keyDeletions: "2",
	}, []string{"a.go:modified:4:2", "b.go:modified:1:0"})
}

func forgejoPRLeg(t *testing.T) {
	t.Helper()
	pr, err := normalizeForgejo(`{"index":9,"number":12,"title":"FJ","body":"body","state":"closed",`+
		`"merged":true,"user":{"login":"alice"},"html_url":"https://fj.example/o/r/pulls/9",`+
		`"base":{"label":"main","sha":"base111"},"head":{"label":"feat","sha":"head222"}}`,
		`[{"filename":"a.go","status":"modified","additions":2,"deletions":1}]`)
	if err != nil {
		t.Fatalf("normalizeForgejo: %v", err)
	}
	assertPR(t, pr, map[string]string{
		keyProvider: "forgejo", keyNumber: "9", keyTitle: "FJ", keyBody: "body",
		keyBaseRef: "main", keyHeadRef: "feat", keyBaseSHA: "base111", keyHeadSHA: "head222",
		keyAuthor: "alice", keyURL: "https://fj.example/o/r/pulls/9",
		keyState: "merged", keyDraft: "false", keyAdditions: "2", keyDeletions: "1",
	}, []string{"a.go:modified:2:1"})
}

func bitbucketPRLeg(t *testing.T) {
	t.Helper()
	pr, err := normalizeBitbucket(`{"id":11,"title":"BB","summary":{"raw":"body"},"state":"DECLINED",`+
		`"draft":true,"author":{"nickname":"alice","display_name":"Alice A"},`+
		`"destination":{"branch":{"name":"main"},"commit":{"hash":"base111"}},`+
		`"source":{"branch":{"name":"feat"},"commit":{"hash":"head222"}},`+
		`"links":{"html":{"href":"https://bb.example/o/r/pull-requests/11"}}}`,
		[]any{
			map[string]any{
				"new": map[string]any{"path": "a.go"}, "status": "modified",
				"lines_added": 6, "lines_removed": 2,
			},
			map[string]any{"old": map[string]any{"path": "gone.go"}, "status": "removed"},
		})
	if err != nil {
		t.Fatalf("normalizeBitbucket: %v", err)
	}
	assertPR(t, pr, map[string]string{
		keyProvider: "bitbucket", keyNumber: "11", keyTitle: "BB", keyBody: "body",
		keyBaseRef: "main", keyHeadRef: "feat", keyBaseSHA: "base111", keyHeadSHA: "head222",
		keyAuthor: "alice", keyURL: "https://bb.example/o/r/pull-requests/11",
		keyState: "closed", keyDraft: "true", keyAdditions: "6", keyDeletions: "2",
	}, []string{"a.go:modified:6:2", "gone.go:removed:0:0"})
}

func azurePRLeg(t *testing.T) {
	t.Helper()
	pr, err := normalizeAzure(`{"pullRequestId":5,"title":"AZ","description":"body","status":"active",`+
		`"isDraft":true,"createdBy":{"uniqueName":"alice@example.com","displayName":"Alice"},`+
		`"targetRefName":"refs/heads/main","sourceRefName":"refs/heads/feat",`+
		`"lastMergeTargetCommit":{"commitId":"base111"},`+
		`"lastMergeSourceCommit":{"commitId":"head222"},`+
		`"url":"https://dev.azure.example/acme/app/pullrequest/5"}`,
		`[{"item":{"path":"/a.go"},"changeType":"edit"}]`,
		map[string]map[string]int64{"a.go": {keyAdds: 7, keyDels: 4}})
	if err != nil {
		t.Fatalf("normalizeAzure: %v", err)
	}
	assertPR(t, pr, map[string]string{
		keyProvider: "azure", keyNumber: "5", keyTitle: "AZ", keyBody: "body",
		keyBaseRef: "main", keyHeadRef: "feat", keyBaseSHA: "base111", keyHeadSHA: "head222",
		keyAuthor: "alice@example.com", keyURL: "https://dev.azure.example/acme/app/pullrequest/5",
		keyState: "open", keyDraft: "true", keyAdditions: "7", keyDeletions: "4",
	}, []string{"a.go:edit:7:4"})
}

// TestProviderStateFold pins each host's wire state onto the shared
// open|closed|merged|unknown vocabulary the manual documents, including the
// unmapped fallback. Forgejo is absent: it folds state and its merged flag in
// forgejoState, pinned by TestForgejoStateAndDraft.
func TestProviderStateFold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		provider, wire, want string
	}{
		{providerGitLab, "opened", "open"},
		{providerGitLab, "merged", "merged"},
		{providerGitLab, "locked", "unknown"},
		{providerGitCafe, "closed", "closed"},
		{providerBitbucket, "OPEN", "open"},
		{providerBitbucket, "DECLINED", "closed"},
		{providerBitbucket, "MERGED", "merged"},
		{providerBitbucket, "SUPERSEDED", "closed"},
		{providerAzure, "active", "open"},
		{providerAzure, "abandoned", "closed"},
		{providerAzure, "completed", "merged"},
		{providerAzure, "", "unknown"},
	} {
		if got := providerState(tc.provider, tc.wire); got != tc.want {
			t.Errorf("providerState(%s, %q) = %s, want %s", tc.provider, tc.wire, got, tc.want)
		}
	}
}

// TestForgejoStateAndDraft pins the two folds Forgejo does not share with the
// other hosts: merged wins over state, and a WIP title marks a draft only when
// the payload carries no draft field of its own.
func TestForgejoStateAndDraft(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, meta, state, draft string
	}{
		{"merged flag wins", `{"state":"closed","merged":true}`, "merged", "false"},
		{"closed", `{"state":"closed","merged":false}`, "closed", "false"},
		{"open", `{"state":"open"}`, "open", "false"},
		{"unmapped", `{"state":"weird"}`, "unknown", "false"},
		{"wip title is a draft", `{"state":"open","title":"WIP: half done"}`, "open", "true"},
		{"draft field wins over title", `{"state":"open","title":"WIP: x","draft":false}`, "open", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pr, err := normalizeForgejo(tc.meta, `[]`)
			if err != nil {
				t.Fatalf("normalizeForgejo: %v", err)
			}
			assertPR(t, pr, map[string]string{keyState: tc.state, keyDraft: tc.draft}, nil)
		})
	}
}

// TestGitLabUnreadableChangedFilePayloadWarns proves a changed-file read that
// exits 0 with a body that is not a diff list is visible: it must not read as
// a merge request that touched no files.
func TestGitLabUnreadableChangedFilePayloadWarns(t *testing.T) {
	t.Parallel()
	for name, payload := range map[string]string{
		"html error page":      `<html>502 Bad Gateway</html>`,
		"trailing data":        `[{"old_path":"a.go","new_path":"a.go"}] oops`,
		"scalar between pages": `[{"old_path":"a.go","new_path":"a.go"}]7[]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			ctx := hostCtx(t, &scriptCommander{t: t, out: gitlabScripts(dir, payload)}, dir)
			if _, err := fetchGitLab(context.Background(), ctx, hostRequest(), &core.FetchFlags{}); err != nil {
				t.Fatalf("fetchGitLab: %v", err)
			}
			assertWarning(t, *ctx.runner.warnings, "changed-file payload unreadable")
		})
	}
}

// bitbucketNextURL is the second diffstat/comments page the host names.
const bitbucketNextURL = testBitbucketFirst + "&cursor=2"

// TestBitbucketFailedPageWarnsAndTruncates proves a failed Bitbucket page is
// not read as an empty one: a first-page failure and a later-page failure both
// warn and report truncation, and the later failure keeps the page it read.
func TestBitbucketFailedPageWarnsAndTruncates(t *testing.T) {
	t.Parallel()
	failed := scriptResult{stderr: "curl: (22) The requested URL returned error: 500", fail: true}
	for _, tc := range []struct {
		out   map[string]scriptResult
		name  string
		items int
	}{
		{map[string]scriptResult{bitbucketCall(testBitbucketFirst): failed}, "first page fails", 0},
		{map[string]scriptResult{
			bitbucketCall(testBitbucketFirst): {stdout: `{"values":[{"id":1}],"next":"` + bitbucketNextURL + `"}`},
			bitbucketCall(bitbucketNextURL):   failed,
		}, "second page fails", 1},
		{map[string]scriptResult{bitbucketCall(testBitbucketFirst): {stdout: `[]`}}, "page is not an object", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertBitbucketPartial(t, tc.out, tc.items)
		})
	}
}

// assertBitbucketPartial walks one scripted page list and checks it kept the
// expected items, reported truncation, and warned.
func assertBitbucketPartial(t *testing.T, out map[string]scriptResult, items int) {
	t.Helper()
	s, warns := newTestSession(&scriptCommander{t: t, out: out})
	got, truncated, ref := bitbucketPages(context.Background(), s, bitbucketBearer(t), testBitbucketFirst)
	if ref != nil {
		t.Fatalf("refusal = %v, want a warning path", ref)
	}
	if len(got) != items {
		t.Errorf("items = %d, want %d", len(got), items)
	}
	if !truncated {
		t.Error("truncated = false, want true")
	}
	assertWarning(t, *warns, "bitbucket paging stopped on an unreadable page")
}

// TestBitbucketFailedCommentsMarkThreadsTruncated proves the failure reaches
// threads.json, the artifact a re-review trusts, not only the envelope.
func TestBitbucketFailedCommentsMarkThreadsTruncated(t *testing.T) {
	t.Parallel()
	const base = "https://api.bitbucket.org/2.0/repos/o/r/pullrequests/7"
	dir := t.TempDir()
	s, _ := newTestSession(&scriptCommander{t: t, out: map[string]scriptResult{
		bitbucketCall(base + "/diffstat?pagelen=100"): {stdout: `{"values":[]}`},
		bitbucketCall(base + "/comments?pagelen=100"): {stderr: "curl: (22) 500", fail: true},
	}})
	if _, ref := bitbucketThreads(context.Background(), s, dir, bitbucketBearer(t), base, hostPR, "head111"); ref != nil {
		t.Fatalf("bitbucketThreads: %v", ref)
	}
	if got := hostTruncated(t, dir); got != "true" {
		t.Errorf("threads.json truncated = %s, want true", got)
	}
}

// TestAzureIterationFailureWarns proves a failed iteration read is visible
// instead of leaving a PR.json that reads like a file-less pull request.
func TestAzureIterationFailureWarns(t *testing.T) {
	t.Parallel()
	conn := &azureConn{org: "https://example.invalid", project: "acme", rname: "app"}
	iter := "az\x00rest\x00--method\x00get\x00--url\x00https://example.invalid/acme/_apis/git/repositories/app" +
		"/pullRequests/7/iterations?api-version=7.1\x00--query\x00max_by(value, &id).id\x00-o\x00tsv"
	s, warns := newTestSession(&scriptCommander{t: t, out: map[string]scriptResult{
		iter: {stderr: "az: 401 Unauthorized", fail: true},
	}})
	if raw := azureChanges(context.Background(), s, conn, "7"); raw != emptyJSONArray {
		t.Errorf("changes = %q, want the empty array", raw)
	}
	assertWarning(t, *warns, "azure iteration read failed")
}
