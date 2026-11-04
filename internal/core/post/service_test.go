package postcore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/sig"
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
		out.ExitCode = 1
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

// refusalCode recovers the typed refusal code with [errors.As].
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
	if tc.wantWarn != "" {
		joined := strings.Join(p.warnings, "\n")
		if !strings.Contains(joined, tc.wantWarn) {
			t.Errorf("warnings miss %q: %q", tc.wantWarn, joined)
		}
	}
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

// postSplitState builds a minimal state for split unit tests.
func postSplitState(warnings *[]string) *state {
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
	return &state{req: req, warnings: *warnings, controls: &controls.Snapshot{Dir: "no-snapshot"}}
}

// TestSplitPreFooterPassthrough proves bodies that fit with their footer
// pass through untouched and unwarned.
func TestSplitPreFooterPassthrough(t *testing.T) {
	t.Parallel()
	var warnings []string
	p := postSplitState(&warnings)
	short := "> ✅ " + strings.Repeat("x", 100)
	if got := p.splitPreFooter(short); got != short {
		t.Errorf("short body split to %d chars", len(got))
	}
	if len(warnings) != 0 {
		t.Errorf("short body warned: %q", warnings)
	}
	if len(p.extraParts) != 0 {
		t.Errorf("short body stashed %d parts", len(p.extraParts))
	}
}

// TestSplitPreFooterSplits proves an over-limit body splits with a
// continuation note, warning loudly instead of trimming.
func TestSplitPreFooterSplits(t *testing.T) {
	t.Parallel()
	var warnings []string
	p := postSplitState(&warnings)
	big := strings.Repeat("z", hostingBodyLimit+1)
	got := p.splitPreFooter(big)
	if !strings.HasSuffix(got, splitFirstNote(2)) {
		t.Errorf("split head misses the continuation note: %.60q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("split head is not valid UTF-8")
	}
	if len(p.extraParts) != 1 {
		t.Fatalf("split stashed %d parts, want 1", len(p.extraParts))
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "split into 2 review comments") {
		t.Errorf("split warned nothing: %q", warnings)
	}
	assertSplitPart(t, got)
	for _, part := range p.extraParts {
		assertSplitPart(t, part)
	}
}

// TestSplitPreFooterMultibyte proves the split keeps every part inside the
// hosting limit on rune boundaries.
func TestSplitPreFooterMultibyte(t *testing.T) {
	t.Parallel()
	var warnings []string
	p := postSplitState(&warnings)
	multi := strings.Repeat("⚠️", hostingBodyLimit/4+10)
	got := p.splitPreFooter(multi)
	if !utf8.ValidString(got) {
		t.Errorf("multibyte split head is not valid UTF-8")
	}
	assertSplitPart(t, got)
	for _, part := range p.extraParts {
		assertSplitPart(t, part)
	}
}

// assertSplitPart proves one split part fits the hosting limit on a rune
// boundary.
func assertSplitPart(t *testing.T, part string) {
	t.Helper()
	if len(part) > hostingBodyLimit {
		t.Errorf("split part still %d chars", len(part))
	}
	if !utf8.ValidString(part) {
		t.Errorf("split part is not valid UTF-8")
	}
}

// TestSplitBodyParagraphs proves the chunker prefers paragraph boundaries.
func TestSplitBodyParagraphs(t *testing.T) {
	t.Parallel()
	joined := strings.Repeat("a", 100) + "\n\n" + strings.Repeat("b", 100)
	if parts := splitBody(joined, 1000); len(parts) != 1 || parts[0] != joined {
		t.Errorf("fitting paragraphs split: %d parts", len(parts))
	}
	wide := strings.Repeat("c", 30) + "\n\n" + strings.Repeat("d", 30)
	parts := splitBody(wide, 40)
	if len(parts) != 2 {
		t.Fatalf("wide paragraphs gave %d parts, want 2", len(parts))
	}
	for _, part := range parts {
		if len(part) > 40 {
			t.Errorf("paragraph part %d chars over budget", len(part))
		}
	}
}

// TestSplitBodyRuneFallback proves over-long paragraphs fall back to rune
// chunks without losing a byte.
func TestSplitBodyRuneFallback(t *testing.T) {
	t.Parallel()
	single := strings.Repeat("e", 101)
	runeParts := splitBody(single, 40)
	if len(runeParts) != 3 {
		t.Fatalf("over-long paragraph gave %d parts, want 3", len(runeParts))
	}
	for _, part := range runeParts {
		if len(part) > 40 {
			t.Errorf("rune part %d chars over budget", len(part))
		}
	}
	if got := strings.Join(runeParts, ""); got != single {
		t.Errorf("rune chunks lose bytes")
	}
}

// TestSplitFollowPayload proves follow-up payloads post as COMMENT reviews
// with an empty (not null) comments array and a continuation header.
func TestSplitFollowPayload(t *testing.T) {
	t.Parallel()
	got := postPayload(splitFollowHead(2, 2)+"rest", eventComment, commitIDValue("abc"), []any{})
	for _, want := range []string{`"event": "COMMENT"`, `"comments": []`, "review body continued (part 2/2", `"commit_id": "abc"`} {
		if !strings.Contains(got, want) {
			t.Errorf("follow-up payload misses %s:\n%s", want, got)
		}
	}
}

// TestReviewID proves the follow-up progress line reads the accepted id,
// and stays silent when the response carries none.
func TestReviewID(t *testing.T) {
	t.Parallel()
	if id := reviewID(`{"id":101,"state":"commented"}`); id != "101" {
		t.Errorf("id=%q want 101", id)
	}
	if id := reviewID("not json"); id != "" {
		t.Errorf("garbage id=%q want empty", id)
	}
}

// TestFollowRefusal proves a partial post refuses typed, naming the landed
// first review and the missing part.
func TestFollowRefusal(t *testing.T) {
	t.Parallel()
	p := postSplitState(&[]string{})
	ref := p.followRefusal(2, 3, `{"id":101,"state":"commented"}`, "")
	if ref.Code != codePostFailed {
		t.Errorf("code=%q want post-failed", ref.Code)
	}
	for _, want := range []string{"follow-up part 2/3", "review 101"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("refusal misses %q: %q", want, ref.Message)
		}
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
	got := projectComment(m, "")
	if jq.R(mustGet(got)) != "seen\n\n```suggestion\nfix();\n```" {
		t.Errorf("suggestion body: %q", jq.R(mustGet(got)))
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
	if v, has := projectComment(falsy, "").Get(fieldBody); !has || v != false {
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
	if keys := postCommentKeys([]any{projectComment(m, "")}); keys != "path,line,side,body" {
		t.Errorf("keys=%q", keys)
	}
	if keys := postCommentKeys(nil); keys != "" {
		t.Errorf("empty keys=%q", keys)
	}
}

// mustGet reads a field the test guarantees present.
func mustGet(m *jq.Obj) any {
	v, _ := m.Get(fieldBody)
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
		`{"state":"OPEN","headRefOid":"`+head+`","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",`+
			`"author":{"login":"someone-else"}}`); err != nil {
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
	seedPostControls(t, dir, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
}

// postReview is the review both Run-path tests post: one inline comment, so
// the payload body and comments are non-empty.
const postReview = `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,` +
	`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.",` +
	`"rigor":"pointed","severity":"informational"}]}`

// decodePostedPayload decodes one reviews-POST stdin body.
func decodePostedPayload(t *testing.T, raw string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("POST stdin is not one JSON object: %v\n%s", err, raw)
	}
	return doc
}

// decodeFindingsStore decodes a findings array from path.
func decodeFindingsStore(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("%s is not a findings array: %v\n%s", path, err, raw)
	}
	return entries
}

// postCommander scripts the post happy path: the live-SHA read, the
// authorship read, the empty thread recheck, and the reviews POST.
func postCommander(t *testing.T) *scriptCommander {
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
	return &scriptCommander{t: t, out: out}
}

// TestRunDryRun proves the dry-run path builds the payload behind the
// commander seam: the exact live-sha/authorship/recheck argv fire with no
// POST, the findings file carries the merged comment, and the typed data
// carries the envelope inputs.
//
//nolint:gocognit,revive,cyclop // dry-run asserts argv, stores, and streams inline
func TestRunDryRun(t *testing.T) {
	const head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)
	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json", DryRun: true}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data == nil || !resp.Data.DryRun || resp.Data.Event != "COMMENT" || resp.Data.CommitID != head {
		t.Fatalf("data = %+v", resp.Data)
	}
	if resp.Data.Anchors.Kept != 1 {
		t.Errorf("anchors kept = %d, want 1", resp.Data.Anchors.Kept)
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
	entries := decodeFindingsStore(t, ".review/7/findings.dryrun.json")
	if len(entries) != 1 {
		t.Fatalf("findings.dryrun.json entries = %d, want 1: %v", len(entries), entries)
	}
	entry := entries[0]
	for key, want := range map[string]any{
		"path":   "notes.txt",
		"line":   float64(2),
		"side":   "RIGHT",
		"body":   "Consider clarifying this line.",
		"status": "open",
		"kind":   "inline",
	} {
		if got := entry[key]; got != want {
			t.Errorf("findings[0].%s = %v, want %v", key, got, want)
		}
	}
	sig, ok := entry["sig"].(string)
	if !ok || len(sig) != 40 {
		t.Errorf("findings[0].sig = %q, want a full 40-char sig", sig)
	}
	if _, err := os.Stat(".review/7/post.json"); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote post.json")
	}
	if got := strings.Join(human, "\n"); !strings.Contains(got, "preflight:") {
		t.Errorf("human lines:\n%s", got)
	}
}

// TestRunSubmit proves the submit path POSTs the payload with one trailing
// newline on stdin and the review's body, event, commit, and comments: the
// exact reviews argv fires, the marker and receipt land, and the typed data
// carries the accepted review.
//
//nolint:gocognit,revive,cyclop // submit asserts POST stdin, the marker, and the receipt inline
func TestRunSubmit(t *testing.T) {
	const head = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)
	cmdr := postCommander(t)
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
	marker := "<!-- verdict-review payload=" + resp.Data.PayloadHash + " head=" + strings.Repeat("a", 40) + " -->"
	if !strings.Contains(posted[0], marker) {
		t.Errorf("POST stdin misses the hidden repeat marker %q: %s", marker, posted[0])
	}
	payload := decodePostedPayload(t, posted[0])
	if payload["event"] != "COMMENT" {
		t.Errorf("posted event = %v, want COMMENT", payload["event"])
	}
	if payload["commit_id"] != head {
		t.Errorf("posted commit_id = %v, want %s", payload["commit_id"], head)
	}
	body, ok := payload["body"].(string)
	if !ok || !strings.HasPrefix(body, "> ✅ Looks good — minor note below.") {
		t.Errorf("posted body lost the review prose: %q", body)
	}
	wantComments := []any{map[string]any{
		"path": "notes.txt", "line": float64(2), "side": "RIGHT",
		"body": "Consider clarifying this line.\n\n<!-- verdict-finding sig=" +
			sig.Of("notes.txt"+"\n"+"\n"+"Consider clarifying this line.") + " -->",
	}}
	if !reflect.DeepEqual(payload["comments"], wantComments) {
		t.Errorf("posted comments = %#v, want %#v", payload["comments"], wantComments)
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
