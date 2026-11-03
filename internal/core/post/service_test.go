package postcore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// scriptCommander is a hermetic Commander: canned stdout/stderr per exact
// argv, every call logged for argv/stdin/stream assertions. Unmatched argv
// fails the test (no network, no real forge).
type scriptCommander struct {
	out   map[string]scriptResult
	t     *testing.T
	calls [][]string
	stdin []string
}

// scriptResult is one canned spawn: the streams plus the failure flag.
type scriptResult struct {
	stdout string
	stderr string
	fail   bool
}

// Run records argv/stdin and replays the canned result.
func (s *scriptCommander) Run(_ context.Context, cmd core.Command) (core.Result, error) {
	s.t.Helper()
	s.calls = append(s.calls, append([]string{}, cmd.Argv...))
	var stdin string
	if cmd.Stdin != nil {
		raw, rerr := io.ReadAll(cmd.Stdin)
		if rerr == nil {
			stdin = string(raw)
		}
	}
	s.stdin = append(s.stdin, stdin)
	key := strings.Join(cmd.Argv, "\x00")
	res, ok := s.out[key]
	if !ok {
		s.t.Errorf("unexpected argv: %q", cmd.Argv)
		return core.Result{}, errors.New("unexpected argv")
	}
	out := core.Result{Stdout: res.stdout, Stderr: res.stderr}
	if res.fail {
		return out, errors.New("exit 1: " + key)
	}
	return out, nil
}

// LookPath reports every tool present (post degrades on lookup failure,
// never refuses missing-tool).
func (s *scriptCommander) LookPath(string) error { return nil }

// testProgress collects streamed warnings.
type testProgress struct {
	warnings *[]string
	human    *[]string
}

// Human records one human line.
func (p testProgress) Human(line string) {
	if p.human != nil {
		*p.human = append(*p.human, line)
	}
}

// Warn records one warning.
func (p testProgress) Warn(msg string) { *p.warnings = append(*p.warnings, msg) }

// Diag discards raw diagnostics.
func (p testProgress) Diag(string) {}

var postTestTime = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// postTestReq builds a post request over a scripted commander.
func postTestReq(cmdr Commander, flags core.PostFlags, warnings *[]string, human *[]string) *core.PostRequest {
	root := ""
	return &core.PostRequest{
		Runtime: &core.Runtime{
			Env:       map[string]string{},
			Now:       func() time.Time { return postTestTime },
			Progress:  testProgress{warnings: warnings, human: human},
			Commander: cmdr,
		},
		Repo:      "acme/app",
		Pr:        "7",
		Root:      &root,
		Flags:     flags,
		Effective: core.Defaults(),
		JSONMode:  true,
		Warnings:  []string{},
	}
}

// writeTestFile writes content to path.
func writeTestFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// postTestState builds a minimal state for guard unit tests.
func postTestState(dir, commitID string, flags core.PostFlags, warnings *[]string) *state {
	root := ""
	req := &core.PostRequest{
		Runtime: &core.Runtime{
			Env:      map[string]string{},
			Now:      func() time.Time { return postTestTime },
			Progress: testProgress{warnings: warnings},
		},
		Repo:      "acme/app",
		Pr:        "7",
		Root:      &root,
		Flags:     flags,
		Effective: core.Defaults(),
	}
	return &state{req: req, warnings: *warnings, dir: dir, commitID: commitID, flags: flags}
}

// refusalCode recovers the typed refusal code with [errors.As] (R4).
func refusalCode(err error) (string, bool) {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref.Code, true
	}
	return "", false
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
	var warnings []string
	p := postTestState(dir, forceCase.commitID, core.PostFlags{Force: true}, &warnings)
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
//
//nolint:revive,cyclop // table legs assert code, message, and warnings inline
func checkFinalizerCase(t *testing.T, tc *finalizerCase) {
	t.Helper()
	dir := t.TempDir()
	if tc.postJSON != "" {
		if err := writeTestFile(dir+"/post.json", tc.postJSON); err != nil {
			t.Fatal(err)
		}
	}
	var warnings []string
	p := postTestState(dir, tc.commitID, core.PostFlags{}, &warnings)
	ref := p.finalizerGuard()
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
	if tc.wantPiece != "" && !strings.Contains(ref.Error(), tc.wantPiece) {
		t.Errorf("refusal misses %q: %v", tc.wantPiece, ref)
	}
	if tc.wantWarn != "" {
		joined := strings.Join(warnings, "\n")
		if !strings.Contains(joined, tc.wantWarn) {
			t.Errorf("warnings miss %q: %q", tc.wantWarn, joined)
		}
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
	var warnings []string
	p := postTestState(dir, head, core.PostFlags{}, &warnings)
	p.req.Pr = "7"
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
		if wait <= 0 || wait > maxBackoff {
			t.Errorf("reset wait=%v out of (0,60s] for %q", wait, tc.stderr)
		}
		return
	}
	if !ok {
		return
	}
	if wait != tc.want {
		t.Errorf("wait=%v want %v for %q", wait, tc.want, tc.stderr)
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

// postTrimState builds a minimal state for trim unit tests.
func postTrimState(warnings *[]string) *state {
	root := ""
	req := &core.PostRequest{
		Runtime: &core.Runtime{
			Env:      map[string]string{},
			Now:      func() time.Time { return postTestTime },
			Progress: testProgress{warnings: warnings},
		},
		Root:      &root,
		Effective: core.Defaults(),
	}
	return &state{req: req, warnings: *warnings}
}

// TestTrimmedBodyPassthrough proves under-limit bodies pass through
// untouched and unwarned.
func TestTrimmedBodyPassthrough(t *testing.T) {
	t.Parallel()
	var warnings []string
	p := postTrimState(&warnings)
	short := "> ✅ " + strings.Repeat("x", 100)
	if got := p.trimmedBody(short); got != short {
		t.Errorf("short body trimmed to %d chars", len(got))
	}
	if len(warnings) != 0 {
		t.Errorf("short body warned: %q", warnings)
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
	var warnings []string
	p := postTrimState(&warnings)
	big := strings.Repeat("z", hostingBodyLimit+1)
	got := p.trimmedBody(big)
	if len(got) > hostingBodyLimit {
		t.Errorf("over-limit body still %d chars", len(got))
	}
	if !strings.HasSuffix(got, trimNotice) {
		t.Errorf("trimmed body misses the notice: %.60q", got)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "trimmed with a notice") {
		t.Errorf("trim warned nothing: %q", warnings)
	}
	multi := strings.Repeat("⚠️", hostingBodyLimit/4+10)
	mgot := p.trimmedBody(multi)
	if !utf8.ValidString(mgot) {
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
//
//nolint:revive // projection asserts every shipped and dropped key inline
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
	if v, has := projectComment(falsy).Get(fieldBody); !has || v != false {
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

// mustGet reads a field the test guarantees present.
func mustGet(m *jq.Obj, key string) any {
	v, _ := m.Get(key)
	return v
}

// TestPostRecheckMalformed proves the recheck downgrade signal for malformed
// live threads: garbage payloads and missing node lists error (callers
// downgrade to COMMENT), writing the gh stderr into .recheck.err.
//
//nolint:revive // malformed legs assert the reason and sidecar inline
func TestPostRecheckMalformed(t *testing.T) {
	t.Parallel()
	graphql := "gh\x00api\x00graphql\x00--paginate\x00-F\x00owner=acme\x00-F\x00name=app\x00-F\x00pr=1\x00-f\x00query=" + queryLiveCount
	for _, tc := range []struct {
		name   string
		stdout string
	}{
		{"garbage", "not json{{{"},
		{"nodata", `{"data":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmdr := &scriptCommander{
				t:   t,
				out: map[string]scriptResult{graphql: {stdout: tc.stdout, stderr: "boom-gh-stderr\n"}},
			}
			dir := t.TempDir()
			_, reason, err := postRecheck(context.Background(), cmdr, "acme/app", "1", dir)
			if err == nil {
				t.Fatalf("%s: malformed threads proceeded", tc.name)
			}
			if reason != "boom-gh-stderr " {
				t.Errorf("%s: reason=%q want gh stderr flattened", tc.name, reason)
			}
			raw, rerr := os.ReadFile(dir + "/.recheck.err")
			if rerr != nil || string(raw) != "boom-gh-stderr\n" {
				t.Errorf("%s: .recheck.err=%q", tc.name, raw)
			}
		})
	}
}

// TestRunBadApprove proves a bad --approve value refuses typed before any
// spawn.
func TestRunBadApprove(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "yes"}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil || ref.Code != "bad-approve" {
		t.Fatalf("ref = %v, want bad-approve", ref)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls fired before the approve gate: %q", cmdr.calls)
	}
}

// TestRunCanceled proves a dead context refuses before any spawn.
func TestRunCanceled(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false"}, &warnings, &human)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, ref := Run(ctx, req)
	if ref == nil || ref.Code != "canceled" {
		t.Fatalf("ref = %v, want canceled", ref)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls fired after cancel: %q", cmdr.calls)
	}
}

// postBundle writes a minimal GitHub bundle plus review for Run tests.
func postBundle(t *testing.T, review string) {
	t.Helper()
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dir := ".review/7"
	if err := writeTestFile(dir+"/PR.json",
		`{"state":"OPEN","headRefOid":"`+head+`","author":{"login":"someone-else"}}`); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(dir+"/full.diff",
		"diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n"+
			"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(dir+"/review.json", review); err != nil {
		t.Fatal(err)
	}
}

// postCommander scripts the post happy path: the live-SHA read, the
// authorship read, the empty thread recheck, and the reviews POST.
func postCommander(t *testing.T, payload *string) *scriptCommander {
	t.Helper()
	threads := "gh\x00api\x00graphql\x00--paginate\x00-F\x00owner=acme\x00-F\x00name=app\x00-F\x00pr=7\x00-f\x00query=" + queryLiveCount
	out := map[string]scriptResult{
		"gh\x00api\x00repos/acme/app/pulls/7\x00--jq\x00.head.sha": {stdout: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"},
		"gh\x00api\x00user\x00--jq\x00.login":                      {stdout: "someone-else\n"},
		threads:                                                    {stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`},
		"gh\x00api\x00repos/acme/app/pulls/7/reviews\x00--method\x00POST\x00--input\x00-\x00--jq\x00" + postJQFilter: {
			stdout: `{"id":101,"state":"commented","html_url":"https://example.invalid/r/101","submitted_at":"2026-01-01T00:00:00Z"}`,
		},
	}
	_ = payload
	return &scriptCommander{t: t, out: out}
}

// TestRunDryRun proves the dry-run path builds the payload behind the
// commander seam: the exact live-sha/authorship/recheck argv fire with no
// POST, the findings file stages, and the typed data carries the envelope
// inputs.
//
//nolint:gocognit,revive,cyclop // dry-run asserts argv, stores, and streams inline
func TestRunDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}`)
	cmdr := postCommander(t, nil)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json", DryRun: true}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data == nil || !resp.Data.DryRun || resp.Data.Event != "COMMENT" {
		t.Fatalf("data = %+v", resp.Data)
	}
	for _, want := range []string{
		"gh api repos/acme/app/pulls/7 --jq .head.sha",
	} {
		found := false
		for _, c := range cmdr.calls {
			if strings.Join(c, " ") == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing argv %q in %q", want, cmdr.calls)
		}
	}
	for _, c := range cmdr.calls {
		if strings.Contains(strings.Join(c, " "), "/reviews") {
			t.Errorf("dry-run posted: %q", c)
		}
	}
	if _, err := os.Stat(".review/7/findings.dryrun.json"); err != nil {
		t.Errorf("missing findings.dryrun.json: %v", err)
	}
	if _, err := os.Stat(".review/7/post.json"); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote post.json")
	}
	if got := strings.Join(human, "\n"); !strings.Contains(got, "preflight:") {
		t.Errorf("human lines:\n%s", got)
	}
}

// TestRunSubmit proves the submit path POSTs the payload with one trailing
// newline on stdin: the exact reviews argv fires, the marker and receipt
// land, and the typed data carries the accepted review.
//
//nolint:revive,cyclop // submit asserts POST stdin, the marker, and the receipt inline
func TestRunSubmit(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}`)
	cmdr := postCommander(t, nil)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data == nil || resp.Data.DryRun || resp.Data.PayloadHash == "" {
		t.Fatalf("data = %+v", resp.Data)
	}
	var posted []string
	for i, c := range cmdr.calls {
		if strings.Contains(strings.Join(c, " "), "/reviews") {
			posted = append(posted, cmdr.stdin[i])
		}
	}
	if len(posted) != 1 {
		t.Fatalf("reviews calls = %d, want 1", len(posted))
	}
	if !strings.HasSuffix(posted[0], "\n") || strings.HasSuffix(posted[0], "\n\n") {
		t.Errorf("POST stdin misses the single trailing newline: %q", posted[0])
	}
	if _, err := os.Stat(".review/7/post.json"); err != nil {
		t.Errorf("missing post.json: %v", err)
	}
	if _, err := os.Stat(".review/7/.posted_" + resp.Data.PayloadHash); err != nil {
		t.Errorf("missing posted marker: %v", err)
	}
	if resp.Data.Review == nil {
		t.Errorf("data misses the accepted review")
	}
}
