package record

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// writeStubFile plants one executable stub script in the stub directory.
func writeStubFile(stubDir, name, script string) error {
	return os.WriteFile(filepath.Join(stubDir, name), []byte(script), 0o755)
}

// postTestRunner builds a runner capturing warnings for unit tests.
func postTestRunner() (*cli.Runner, *strings.Builder) {
	var diag strings.Builder
	return &cli.Runner{Diag: &diag, Human: &diag, Effective: cli.Defaults()}, &diag
}

// TestFinalizerGuard proves the single-review finality: a completed post on
// the same SHA refuses naming the advance form and the override; new SHAs,
// missing records, and --force proceed.
func TestFinalizerGuard(t *testing.T) {
	t.Parallel()
	const head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tests := []finalizerCase{
		{"missing-proceeds", "", head, "", "", ""},
		{"garbage-proceeds", "not json{{{", head, "", "unreadable", ""},
		{"headless-proceeds", "{}", head, "", "carries no head", ""},
		{"null-proceeds", "null", head, "", "carries no head", ""},
		{"different-sha-proceeds", `{"head":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`, head, "", "", ""},
		{"same-sha-refuses", `{"head":"` + head + `","posted_at":"2026-01-01T00:00:00Z"}`, head, "already-posted", "", "--base-sha aaaaaaa"},
		{"empty-commit-proceeds", `{"head":"` + head + `"}`, "", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkFinalizerCase(t, &tc)
		})
	}
	forceCase := finalizerCase{"force-overrides", `{"head":"` + head + `"}`, head, "", "", ""}
	dir := t.TempDir()
	if err := writeTestFile(dir+"/post.json", forceCase.postJSON); err != nil {
		t.Fatal(err)
	}
	r, _ := postTestRunner()
	p := &postRun{runner: r, req: Request{Repo: "acme/app", Pr: "1"}, dir: dir, commitID: forceCase.commitID}
	p.flags = postFlags{force: true}
	if ref := p.finalizerGuard(); ref != nil {
		t.Errorf("forced repost refused: %v", ref)
	}
}

// finalizerCase is one single-review finality leg (force rides separately,
// keeping the table narrow).
type finalizerCase struct {
	name      string
	postJSON  string
	commitID  string
	wantCode  string
	wantWarn  string
	wantPiece string
}

// checkFinalizerCase runs one finalizer leg and asserts code and warnings.
func checkFinalizerCase(t *testing.T, tc *finalizerCase) {
	t.Helper()
	dir := t.TempDir()
	if tc.postJSON != "" {
		if err := writeTestFile(dir+"/post.json", tc.postJSON); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := postTestRunner()
	p := &postRun{runner: r, req: Request{Repo: "acme/app", Pr: "1"}, dir: dir, commitID: tc.commitID}
	ref := p.finalizerGuard()
	checkFinalizerRefusal(t, r, ref, tc)
}

// checkFinalizerRefusal asserts the refusal code, message piece, and warns.
func checkFinalizerRefusal(t *testing.T, r *cli.Runner, ref error, tc *finalizerCase) {
	t.Helper()
	if tc.wantCode == "" && ref != nil {
		t.Fatalf("proceed case refused: %v", ref)
	}
	if tc.wantCode == "" {
		return
	}
	if ref == nil {
		t.Fatalf("refusal case proceeded, want %s", tc.wantCode)
	}
	got, ok := refusalCode(ref)
	if !ok || got != tc.wantCode {
		t.Fatalf("code=%q want %q (%v)", got, tc.wantCode, ref)
	}
	checkFinalizerMessage(t, ref, tc)
	checkFinalizerWarn(t, r, tc)
}

// checkFinalizerMessage asserts the refusal names the advance form.
func checkFinalizerMessage(t *testing.T, ref error, tc *finalizerCase) {
	t.Helper()
	if tc.wantPiece != "" && !strings.Contains(ref.Error(), tc.wantPiece) {
		t.Errorf("refusal misses %q: %v", tc.wantPiece, ref)
	}
}

// checkFinalizerWarn asserts the warn-and-proceed lines.
func checkFinalizerWarn(t *testing.T, r *cli.Runner, tc *finalizerCase) {
	t.Helper()
	if tc.wantWarn != "" && !strings.Contains(warnText(r), tc.wantWarn) {
		t.Errorf("warnings miss %q: %q", tc.wantWarn, warnText(r))
	}
}

// TestFinalizerAlreadyPostedNamesOverride proves the refusal names --force
// alongside the advance form.
func TestFinalizerAlreadyPostedNamesOverride(t *testing.T) {
	t.Parallel()
	const head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dir := t.TempDir()
	if err := writeTestFile(dir+"/post.json", `{"head":"`+head+`","posted_at":"t"}`); err != nil {
		t.Fatal(err)
	}
	r, _ := postTestRunner()
	p := &postRun{runner: r, req: Request{Repo: "acme/app", Pr: "7"}, dir: dir, commitID: head}
	ref := p.finalizerGuard()
	if ref == nil {
		t.Fatal("same-SHA repost proceeded")
	}
	for _, piece := range []string{"--force", "--base-sha aaaaaaa", "fetch acme/app 7"} {
		if !strings.Contains(ref.Error(), piece) {
			t.Errorf("refusal misses %q: %v", piece, ref)
		}
	}
}

// TestRetryDelayInternalError proves the shell's exact internal-error retry:
// 1s then 2s, same warning bucket.
func TestRetryDelayInternalError(t *testing.T) {
	t.Parallel()
	wait, ok := retryDelay("HTTP 500: Internal error occurred, please try again (body)", 0)
	if !ok || wait != time.Second {
		t.Errorf("attempt 0: wait=%v ok=%v, want 1s true", wait, ok)
	}
	wait, ok = retryDelay("internal ERROR occurred, please try again", 1)
	if !ok || wait != 2*time.Second {
		t.Errorf("attempt 1: wait=%v ok=%v, want 2s true", wait, ok)
	}
}

// TestRetryDelayRateLimit proves the folded audit-21 behavior: 429/403 with
// a rate-limit signal retry honoring the retry-after hint, the reset
// marker, and the bound; anything else fails fast.
func TestRetryDelayRateLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []retryLeg{
		{"HTTP 429: secondary rate limit exceeded, retry-after: 0", 0, true, 0},
		{"HTTP 403: You have exceeded a secondary rate limit. Please retry after 5 seconds.", 0, true, 5 * time.Second},
		{"HTTP 429: rate limit exceeded, x-ratelimit-reset: 2000000000", 0, true, -1},
		{"HTTP 429: secondary rate limit exceeded for installation", 1, true, 2 * time.Second},
		{"secondary rate limit, retry-after: 9999", 0, true, maxBackoff},
		{"HTTP 403: Resource not accessible by integration", 0, false, 0},
		{"HTTP 422: Validation Failed", 0, false, 0},
		{"connection refused", 0, false, 0},
	} {
		checkRetryLeg(t, tc)
	}
}

// retryLeg is one backoff leg: want -1 means any bounded positive wait.
type retryLeg struct {
	stderr  string
	attempt int
	wantOK  bool
	want    time.Duration
}

// checkRetryLeg asserts one backoff leg.
func checkRetryLeg(t *testing.T, tc retryLeg) {
	t.Helper()
	wait, ok := retryDelay(tc.stderr, tc.attempt)
	if ok != tc.wantOK {
		t.Fatalf("ok=%v want %v for %q", ok, tc.wantOK, tc.stderr)
	}
	if tc.want < 0 {
		checkResetWait(t, tc.stderr, wait)
		return
	}
	if !ok {
		return
	}
	if wait != tc.want {
		t.Errorf("wait=%v want %v for %q", wait, tc.want, tc.stderr)
	}
}

// checkResetWait asserts a reset-marker wait stays inside the bound.
func checkResetWait(t *testing.T, stderr string, wait time.Duration) {
	t.Helper()
	if wait <= 0 || wait > maxBackoff {
		t.Errorf("reset wait=%v out of (0,60s] for %q", wait, stderr)
	}
}

// TestRetryDelayBounded proves the backoff never exceeds the cap even with
// absurd hints.
func TestRetryDelayBounded(t *testing.T) {
	t.Parallel()
	wait, ok := retryDelay("secondary rate limit retry-after: 3600", 2)
	if !ok || wait != maxBackoff {
		t.Errorf("wait=%v ok=%v, want %v true", wait, ok, maxBackoff)
	}
}

// TestTrimmedBodyPassthrough proves under-limit bodies pass through
// untouched and unwarned.
func TestTrimmedBodyPassthrough(t *testing.T) {
	t.Parallel()
	r, diag := postTestRunner()
	p := &postRun{runner: r}
	short := "> ✅ " + strings.Repeat("x", 100)
	if got := p.trimmedBody(short); got != short {
		t.Errorf("short body trimmed to %d chars", len(got))
	}
	if diag.String() != "" {
		t.Errorf("short body warned: %q", diag.String())
	}
	exact := strings.Repeat("y", hostingBodyLimit)
	if got := p.trimmedBody(exact); got != exact {
		t.Errorf("at-limit body trimmed to %d chars", len(got))
	}
}

// TestTrimmedBodyTrims proves over-limit bodies trim on a rune boundary
// with a notice, staying inside the hosting budget.
func TestTrimmedBodyTrims(t *testing.T) {
	t.Parallel()
	r, diag := postTestRunner()
	p := &postRun{runner: r}
	big := strings.Repeat("z", hostingBodyLimit+1)
	got := p.trimmedBody(big)
	if len(got) > hostingBodyLimit {
		t.Errorf("over-limit body still %d chars", len(got))
	}
	if !strings.HasSuffix(got, trimNotice) {
		t.Errorf("trimmed body misses the notice: %.60q", got)
	}
	if !strings.Contains(diag.String(), "trimmed with a notice") {
		t.Errorf("trim warned nothing: %q", diag.String())
	}
	// Multibyte cut: the result stays valid UTF-8 at the same byte budget.
	multi := strings.Repeat("⚠️", hostingBodyLimit/4+10)
	mgot := p.trimmedBody(multi)
	if !validUTF8(mgot) {
		t.Errorf("trimmed body is not valid UTF-8")
	}
	if len(mgot) > hostingBodyLimit {
		t.Errorf("multibyte body still %d bytes", len(mgot))
	}
}

// projectCommentFixture builds a fenced-suggestion comment.
func projectCommentFixture() *jq.Obj {
	m := jq.NewObj(fieldPath, "a.txt", fieldLine, 3, "side", "RIGHT",
		fieldBody, "seen", fieldSuggestion, "fix();", fieldStartLine, nil)
	m.Set("extra", "dropped-by-construction")
	return m
}

// TestProjectCommentProjection proves the payload projection: suggestion
// fences fold into body, nulls drop, unknown keys never ship.
func TestProjectCommentProjection(t *testing.T) {
	t.Parallel()
	m := projectCommentFixture()
	got := projectComment(m)
	if jq.R(mustGet(got, fieldBody)) != "seen\n\n```suggestion\nfix();\n```" {
		t.Errorf("suggestion body: %q", jq.R(mustGet(got, fieldBody)))
	}
	for _, k := range []string{fieldPath, fieldLine, "side", fieldBody} {
		if _, has := got.Get(k); !has {
			t.Errorf("projected comment misses %s", k)
		}
	}
	if _, has := got.Get(fieldStartLine); has {
		t.Errorf("null start_line shipped")
	}
	if _, has := got.Get("extra"); has {
		t.Errorf("unknown key shipped")
	}
	falsy := jq.NewObj(fieldPath, "a.txt", fieldBody, false)
	checkFalsyBodyKept(t, projectComment(falsy))
}

// checkFalsyBodyKept asserts an explicit-false body ships (only null drops).
func checkFalsyBodyKept(t *testing.T, got *jq.Obj) {
	t.Helper()
	if v, has := got.Get(fieldBody); !has || v != false {
		t.Errorf("false body dropped, want it kept: %v %v", v, has)
	}
}

// TestProjectCommentCounts proves the embedded count and the dry-run key
// summary over projected comments.
func TestProjectCommentCounts(t *testing.T) {
	t.Parallel()
	m := projectCommentFixture()
	falsy := jq.NewObj(fieldPath, "a.txt", fieldBody, false)
	if n := countEmbedded([]any{m, falsy}); n != 1 {
		t.Errorf("embedded=%d want 1", n)
	}
	if keys := postCommentKeys([]any{projectComment(m)}); keys != "path,line,side,body" {
		t.Errorf("keys=%q", keys)
	}
	if keys := postCommentKeys(nil); keys != "" {
		t.Errorf("empty keys=%q", keys)
	}
}

// writeTestFile writes content to path (callers pass t.TempDir()-joined
// paths, so no parents are needed).
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// refusalCode recovers the typed refusal code with [errors.As] (R4).
func refusalCode(err error) (string, bool) {
	if ref, ok := errors.AsType[*cli.RefusalError](err); ok {
		return ref.Code, true
	}
	return "", false
}

// warnText returns the warnings the test runner captured.
func warnText(r *cli.Runner) string {
	var sb strings.Builder
	for _, w := range r.Warnings {
		sb.WriteString(w + "\n")
	}
	return sb.String()
}

// mustGet reads a field the test guarantees present.
func mustGet(m *jq.Obj, key string) any {
	v, _ := m.Get(key)
	return v
}

// validUTF8 reports whether s is valid UTF-8.
func validUTF8(s string) bool {
	return utf8.ValidString(s)
}

// TestPostRecheckMalformed proves the recheck downgrade signal for malformed
// live threads: garbage payloads and missing node lists error (callers
// downgrade to COMMENT), writing the gh stderr into .recheck.err. The exact
// jq stderr text is version-dependent, so shell parity covers only the
// behavior (post-refusals.sh); this pins the Go probe format.
func TestPostRecheckMalformed(t *testing.T) {
	stub := "#!/usr/bin/env bash\n" +
		"if [ \"${1:-}\" = \"api\" ] && [ \"${2:-}\" = \"graphql\" ]; then\n" +
		"  case \"${GH_POST_RECHECK:-fail}\" in\n" +
		"    garbage) echo \"boom-gh-stderr\" >&2; printf '%s' 'not json{{{'; exit 0;;\n" +
		"    nodata) echo \"boom-gh-stderr\" >&2; printf '%s' '{\"data\":null}'; exit 0;;\n" +
		"  esac\n" +
		"fi\necho dead >&2; exit 1\n"
	stubDir := t.TempDir()
	if err := writeStubFile(stubDir, "gh", stub); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, mode := range []string{"garbage", "nodata"} {
		t.Setenv("GH_POST_RECHECK", mode)
		checkRecheckMalformed(t, mode)
	}
}

// checkRecheckMalformed asserts one malformed-threads leg: an error (the
// caller's downgrade signal), the flattened gh stderr as reason, and the raw
// gh stderr in .recheck.err.
func checkRecheckMalformed(t *testing.T, mode string) {
	t.Helper()
	dir := t.TempDir()
	_, reason, err := postRecheck(t.Context(), "acme/app", "1", dir)
	if err == nil {
		t.Fatalf("%s: malformed threads proceeded", mode)
	}
	if reason != "boom-gh-stderr " {
		t.Errorf("%s: reason=%q want gh stderr flattened", mode, reason)
	}
	raw, rerr := os.ReadFile(dir + "/.recheck.err")
	if rerr != nil || string(raw) != "boom-gh-stderr\n" {
		t.Errorf("%s: .recheck.err=%q", mode, raw)
	}
}
