package postcore

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	gitlabHead  = "dddddddddddddddddddddddddddddddddddddddd"
	gitlabBase  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	gitlabStart = "cccccccccccccccccccccccccccccccccccccccc"
)

// gitlabTestDiff is the reviewed hunk the position tests read: new line 1 is
// unchanged, new line 2 is added, and old line 2 is the removed line.
const gitlabTestDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// gitlabBlankContextDiff has a truly empty context line: git renders a blank
// context line as an empty string with no leading space, so a line map that
// skips it shifts every new-side number after it by one.
const gitlabBlankContextDiff = "diff --git a/blank.txt b/blank.txt\nindex 1111111..2222222 100644\n" +
	"--- a/blank.txt\n+++ b/blank.txt\n@@ -1,4 +1,4 @@\n top\n\n-old line\n+new line\n tail\n"

// gitlabTestRenameDiff is the renamed-file hunk: GitLab wants the pre-change
// path beside the post-change one.
const gitlabTestRenameDiff = "diff --git a/old.go b/new.go\nsimilarity index 90%\nrename from old.go\nrename to new.go\n" +
	"--- a/old.go\n+++ b/new.go\n@@ -1,2 +1,2 @@\n top\n-gone\n+added\n"

// The exact argv a GitLab post fires. Every one is asserted, never assumed:
// the endpoint, the method, the content type, and the stdin body are the
// contract. The `--header` is not decoration: glab marks a `--input` body as
// JSON nowhere, so a header-less write is refused with 415 by GitLab.
const (
	gitlabPRArgv          = "glab\x00api\x00projects/acme%2Fapp/merge_requests/7"
	gitlabJSONHeader      = "--header\x00Content-Type: application/json"
	gitlabDiscussionsArgv = gitlabPRArgv + "/discussions\x00--method\x00POST\x00" + gitlabJSONHeader + "\x00--input\x00-"
	gitlabNotesArgv       = gitlabPRArgv + "/notes\x00--method\x00POST\x00" + gitlabJSONHeader + "\x00--input\x00-"
	gitlabApproveArgv     = gitlabPRArgv + "/approve\x00--method\x00POST\x00-f\x00sha=" + gitlabHead
	gitlabRecheckArgv     = gitlabPRArgv + "/discussions?per_page=100\x00--paginate"
	gitlabUserArgv        = "glab\x00api\x00user"
)

// gitlabMRJSON is the merge-request read the diff refs come from.
const gitlabMRJSON = `{"iid":7,"state":"opened","web_url":"https://gl.example/acme/app/-/merge_requests/7",` +
	`"sha":"` + gitlabHead + `","diff_refs":{"base_sha":"` + gitlabBase + `","start_sha":"` + gitlabStart + `",` +
	`"head_sha":"` + gitlabHead + `"}}`

// gitlabBundleReview is the review the Run-path tests post: one added-line
// finding, so the payload body and comments are non-empty.
const gitlabBundleReview = `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,` +
	`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Consider clarifying this line.",` +
	`"rigor":"pointed","severity":"informational"}]}`

// gitlabCommander scripts the reads and writes a GitLab post fires.
func gitlabCommander(t *testing.T) *scriptCommander {
	t.Helper()
	return &scriptCommander{t: t, out: map[string]scriptResult{
		gitlabPRArgv:          {stdout: gitlabMRJSON},
		gitlabDiscussionsArgv: {stdout: `{"id":"disc-1"}`},
		gitlabNotesArgv:       {stdout: `{"id":4242,"created_at":"2026-11-02T12:00:00Z"}`},
		gitlabApproveArgv:     {stdout: `{"id":7,"iid":7,"approved":true}`},
		gitlabRecheckArgv:     {stdout: `[]`},
		gitlabUserArgv:        {stdout: `{"username":"bob"}`},
	}}
}

// gitlabBundleDir writes the reviewed diff into a scratch bundle directory and
// returns it.
func gitlabBundleDir(t *testing.T, diff string) string {
	t.Helper()
	dir := t.TempDir()
	if err := writeTestFile(dir+"/full.diff", diff); err != nil {
		t.Fatal(err)
	}
	return dir
}

// countCalls counts the calls that fired one exact argv.
func countCalls(cmdr *scriptCommander, key string) int {
	n := 0
	for _, call := range cmdr.calls {
		if strings.Join(call, "\x00") == key {
			n++
		}
	}
	return n
}

// callKeys renders the fired argv list for a readable failure message.
func callKeys(cmdr *scriptCommander) []string {
	out := make([]string, 0, len(cmdr.calls))
	for _, call := range cmdr.calls {
		out = append(out, strings.Join(call, " "))
	}
	return out
}

// gitlabStdinDoc decodes one posted body from the commander's stdin log.
func gitlabStdinDoc(t *testing.T, cmdr *scriptCommander, i int) map[string]any {
	t.Helper()
	if i >= len(cmdr.stdin) {
		t.Fatalf("call %d never fired: %q", i, callKeys(cmdr))
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(cmdr.stdin[i]), &doc); err != nil {
		t.Fatalf("stdin %d is not one JSON object: %v\n%s", i, err, cmdr.stdin[i])
	}
	return doc
}

// gitlabPostedNote returns the summary-note body a run posted: the posted
// bodies are in call order, and only the note carries the review marker.
func gitlabPostedNote(t *testing.T, cmdr *scriptCommander) string {
	t.Helper()
	for _, raw := range cmdr.stdin {
		if body, ok := markedReviewBody(raw); ok {
			return body
		}
	}
	t.Fatalf("no posted body carries the review marker: %q", callKeys(cmdr))
	return ""
}

// markedReviewBody returns the review body a posted stdin document carries,
// when it carries the payload marker.
func markedReviewBody(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return "", false
	}
	body, isBody := doc[fieldBody].(string)
	if !isBody || !strings.Contains(body, "<!-- verdict-review payload=") {
		return "", false
	}
	return body, true
}

// gitlabPositionCase is one anchor and the position fields it must produce.
type gitlabPositionCase struct {
	comment  *jq.Obj
	name     string
	diff     string
	oldPath  string
	oldLine  any
	newLine  any
	bodyPath string
}

// TestGitLabPositionMapping proves the payload's anchors become the position
// GitLab documents: both paths from the diff (so a rename anchors its
// pre-change path), the MR's three diff SHAs, a new-side line for an added
// line, both numbers for an unchanged line, and only the old number for a
// removed line.
func TestGitLabPositionMapping(t *testing.T) {
	t.Parallel()
	cases := []gitlabPositionCase{
		{
			name: "added line", diff: gitlabTestDiff,
			comment: jq.NewObj("path", "notes.txt", "line", 2, "side", "RIGHT", "body", "B"),
			oldPath: "notes.txt", oldLine: nil, newLine: float64(2),
		},
		{
			name: "unchanged line keeps its old number", diff: gitlabTestDiff,
			comment: jq.NewObj("path", "notes.txt", "line", 1, "side", "RIGHT", "body", "B"),
			oldPath: "notes.txt", oldLine: float64(1), newLine: float64(1),
		},
		{
			name: "removed line is old-side only", diff: gitlabTestDiff,
			comment: jq.NewObj("path", "notes.txt", "line", 2, "side", "LEFT", "body", "B"),
			oldPath: "notes.txt", oldLine: float64(2), newLine: nil,
		},
		{
			// The blank context line is new 2 / old 2. Skipping it in the line
			// map makes new 2 read as the added line (no old_line) and pushes
			// every number after it one down, so this row fails on the bug.
			name: "blank context line keeps its old number", diff: gitlabBlankContextDiff,
			comment: jq.NewObj("path", "blank.txt", "line", 2, "side", "RIGHT", "body", "B"),
			oldPath: "blank.txt", oldLine: float64(2), newLine: float64(2), bodyPath: "blank.txt",
		},
		{
			name: "line after a blank context line keeps its old number", diff: gitlabBlankContextDiff,
			comment: jq.NewObj("path", "blank.txt", "line", 4, "side", "RIGHT", "body", "B"),
			oldPath: "blank.txt", oldLine: float64(4), newLine: float64(4), bodyPath: "blank.txt",
		},
		{
			name: "rename keeps both paths", diff: gitlabTestRenameDiff,
			comment: jq.NewObj("path", "new.go", "line", 2, "side", "RIGHT", "body", "B"),
			oldPath: "old.go", oldLine: nil, newLine: float64(2), bodyPath: "new.go",
		},
	}
	for i := range cases {
		tc := &cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.bodyPath == "" {
				tc.bodyPath = "notes.txt"
			}
			assertGitLabPosition(t, tc)
		})
	}
}

// TestGitLabPostApprovesOnlyOnApproval proves the approval is the last write
// and covers the reviewed head by SHA, while a comment review never approves
// and a host whose capability carries no approve writes no approval at all.
func TestGitLabPostApprovesOnlyOnApproval(t *testing.T) {
	t.Parallel()
	cases := []approveLegCase{
		{name: "approval approves last", event: eventApprove, state: "approved", approves: true},
		{name: "comment never approves", event: eventComment, state: "commented", approves: false},
		{
			name: "no approve capability writes no approval", event: eventApprove,
			caps:  core.HostCapabilities{Provider: providerGitLab, Fetch: true, Post: true, InlineAnchors: true},
			state: "commented", approves: false,
		},
	}
	for i := range cases {
		tc := &cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertApproveLeg(t, tc)
		})
	}
}

// approveLegCase is one verdict event, the capability table it runs against,
// and the receipt state it must produce.
type approveLegCase struct {
	name     string
	event    string
	state    string
	caps     core.HostCapabilities
	approves bool
}

// assertApproveLeg posts one verdict and pins the approval write: exactly one
// approve call as the last write when the host advertises it, none otherwise.
func assertApproveLeg(t *testing.T, tc *approveLegCase) {
	t.Helper()
	cmdr := gitlabCommander(t)
	caps := tc.caps
	if caps.Provider == "" {
		caps = core.CapabilitiesFor(providerGitLab)
	}
	payload := postPayload("review body", tc.event, gitlabHead, []any{
		jq.NewObj("path", "notes.txt", "line", 2, "side", "RIGHT", "body", "B"),
	})
	attempt := gitlabPost(context.Background(), cmdr, caps, "acme/app", "7", gitlabBundleDir(t, gitlabTestDiff), payload)
	if !attempt.ok {
		t.Fatalf("gitlabPost = %+v, want the accepted attempt", attempt)
	}
	assertApproveCalls(t, cmdr, tc.approves)
	assertGitLabReceipt(t, attempt.posted, tc.state)
}

// assertApproveCalls pins the approval write count and, when one fired, that it
// was the last write the run made.
func assertApproveCalls(t *testing.T, cmdr *scriptCommander, approves bool) {
	t.Helper()
	want := 0
	if approves {
		want = 1
	}
	if got := countCalls(cmdr, gitlabApproveArgv); got != want {
		t.Fatalf("approve calls = %d, want %d (%q)", got, want, callKeys(cmdr))
	}
	if !approves {
		return
	}
	if last := strings.Join(cmdr.calls[len(cmdr.calls)-1], "\x00"); last != gitlabApproveArgv {
		t.Errorf("last call = %q, want the approval", last)
	}
}

// assertGitLabReceipt pins the accepted receipt: the verdict state and the note
// identity the envelope publishes.
func assertGitLabReceipt(t *testing.T, posted, state string) {
	t.Helper()
	receipt := decodePostedPayload(t, posted)
	if receipt["state"] != state {
		t.Errorf("receipt state = %v, want %v", receipt["state"], state)
	}
	for key, want := range map[string]any{
		"id": float64(4242), "html_url": "https://gl.example/acme/app/-/merge_requests/7",
		"submitted_at": "2026-11-02T12:00:00Z",
	} {
		if got := receipt[key]; got != want {
			t.Errorf("receipt %s = %v, want %v", key, got, want)
		}
	}
}

// TestGitLabPostFollowUpPartIsNoteOnly proves a body-split part (an empty
// comments array) lands as the summary note alone, never as a discussion.
func TestGitLabPostFollowUpPartIsNoteOnly(t *testing.T) {
	t.Parallel()
	cmdr := gitlabCommander(t)
	payload := postPayload("part two", eventComment, gitlabHead, []any{})
	attempt := gitlabPost(context.Background(), cmdr, core.CapabilitiesFor(providerGitLab), "acme/app", "7", gitlabBundleDir(t, gitlabTestDiff), payload)
	if !attempt.ok {
		t.Fatalf("gitlabPost = %+v, want the accepted attempt", attempt)
	}
	if got := countCalls(cmdr, gitlabDiscussionsArgv); got != 0 {
		t.Errorf("discussions posted for a comment-less payload: %q", callKeys(cmdr))
	}
	if got := countCalls(cmdr, gitlabNotesArgv); got != 1 {
		t.Errorf("note calls = %d, want 1 (%q)", got, callKeys(cmdr))
	}
}

// TestGitLabPostWarnings proves the mapping gaps the payload hits are said out
// loud instead of silently dropped: a multi-line anchor flattened onto its end
// line, and a finding that never became a discussion. The request-changes gap
// is a capability, so the service warns about it before any write.
func TestGitLabPostWarnings(t *testing.T) {
	t.Parallel()
	cmdr := gitlabCommander(t)
	payload := postPayload("review body", eventRequestChanges, gitlabHead, []any{
		jq.NewObj("path", "notes.txt", "line", 2, "start_line", 1, "side", "RIGHT", "body", "B"),
		jq.NewObj("line", 2, "side", "RIGHT", "body", "no path"),
	})
	attempt := gitlabPost(context.Background(), cmdr, core.CapabilitiesFor(providerGitLab), "acme/app", "7", gitlabBundleDir(t, gitlabTestDiff), payload)
	if !attempt.ok {
		t.Fatalf("gitlabPost = %+v, want the accepted attempt", attempt)
	}
	if got := countCalls(cmdr, gitlabApproveArgv); got != 0 {
		t.Errorf("a request-changes review approved: %q", callKeys(cmdr))
	}
	joined := strings.Join(attempt.warnings, "\n")
	for _, want := range []string{"single-line", "1 multi-line anchor", "1 finding(s) kept out of the diff"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings = %q, want one carrying %q", attempt.warnings, want)
		}
	}
	if strings.Contains(joined, "request-changes") {
		t.Errorf("warnings = %q, want the verdict gap to come from the capability gate", attempt.warnings)
	}
}

// gitlabBareBundle writes the provider marker and reviewed diff a submit-only
// test needs, without the merge inputs a full Run requires.
func gitlabBareBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := writeTestFile(dir+"/PR.json", `{"provider":"gitlab","state":"open"}`); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(dir+"/full.diff", gitlabTestDiff); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestGitLabPostFailureStopsWithoutReplay proves a transient failure after
// some discussions landed refuses instead of replaying them: the retry the
// single-call GitHub path documents would duplicate every landed thread.
func TestGitLabPostFailureStopsWithoutReplay(t *testing.T) {
	t.Parallel()
	dir := gitlabBareBundle(t)
	limit := scriptResult{stderr: "HTTP 429: rate limit exceeded, retry-after: 0\n", fail: true}
	cmdr := gitlabCommander(t)
	cmdr.out[gitlabNotesArgv] = limit
	var warnings []string
	st := postTestState(dir, "", core.PostFlags{}, &warnings)
	payload := postPayload("review body", eventComment, gitlabHead, []any{
		jq.NewObj("path", "notes.txt", "line", 2, "side", "RIGHT", "body", "B"),
		jq.NewObj("path", "notes.txt", "line", 1, "side", "RIGHT", "body", "C"),
	})
	posted, ref := st.postOneWithRetry(context.Background(), cmdr, payload)
	if ref == nil || ref.Code != codePostFailed {
		t.Fatalf("ref = %v, want post-failed", ref)
	}
	if posted != "" {
		t.Errorf("posted = %q, want nothing", posted)
	}
	if got := countCalls(cmdr, gitlabDiscussionsArgv); got != 2 {
		t.Errorf("discussion calls = %d, want 2 (a replay would post 4): %q", got, callKeys(cmdr))
	}
	if got := countCalls(cmdr, gitlabNotesArgv); got != 1 {
		t.Errorf("note calls = %d, want 1: %q", got, callKeys(cmdr))
	}
	if got := readTestFile(t, dir+"/.post.err"); !strings.Contains(got, "rate limit exceeded") {
		t.Errorf(".post.err = %q, want the forge's stderr", got)
	}
	if !strings.Contains(strings.Join(st.warnings, "\n"), "2 of 2 inline discussion(s)") {
		t.Errorf("warnings = %q, want the landed count named", st.warnings)
	}
}

// TestGitLabPostFailureSidecar proves a refused discussion puts the host's own
// words in the documented sidecar, which the refusal quotes back.
func TestGitLabPostFailureSidecar(t *testing.T) {
	t.Parallel()
	dir := gitlabBareBundle(t)
	cmdr := gitlabCommander(t)
	cmdr.out[gitlabDiscussionsArgv] = scriptResult{stderr: "HTTP 400: Note position is invalid\n", fail: true}
	var warnings []string
	st := postTestState(dir, "", core.PostFlags{}, &warnings)
	payload := postPayload("review body", eventComment, gitlabHead, []any{
		jq.NewObj("path", "notes.txt", "line", 2, "side", "RIGHT", "body", "B"),
	})
	_, ref := st.postOneWithRetry(context.Background(), cmdr, payload)
	if ref == nil || ref.Code != codePostFailed {
		t.Fatalf("ref = %v, want post-failed", ref)
	}
	if !strings.Contains(ref.Message, "Note position is invalid") {
		t.Errorf("refusal = %q, want the host's stderr quoted", ref.Message)
	}
	if got := readTestFile(t, dir+"/.post.err"); got != "HTTP 400: Note position is invalid\n" {
		t.Errorf(".post.err = %q, want the discussion's stderr", got)
	}
	if got := countCalls(cmdr, gitlabDiscussionsArgv); got != 1 {
		t.Errorf("discussion calls = %d, want 1 (a validation error never retries)", got)
	}
}

// TestGitLabPostFailureWithoutStderr proves a silent non-zero exit still
// refuses with a readable reason instead of quoting an empty sidecar.
func TestGitLabPostFailureWithoutStderr(t *testing.T) {
	t.Parallel()
	dir := gitlabBareBundle(t)
	cmdr := gitlabCommander(t)
	cmdr.out[gitlabDiscussionsArgv] = scriptResult{fail: true}
	var warnings []string
	st := postTestState(dir, "", core.PostFlags{}, &warnings)
	payload := postPayload("review body", eventComment, gitlabHead, []any{
		jq.NewObj("path", "notes.txt", "line", 2, "side", "RIGHT", "body", "B"),
	})
	_, ref := st.postOneWithRetry(context.Background(), cmdr, payload)
	if ref == nil || ref.Code != codePostFailed {
		t.Fatalf("ref = %v, want post-failed", ref)
	}
	if !strings.Contains(ref.Message, "exit 1 with no stderr") {
		t.Errorf("refusal = %q, want the silent exit named", ref.Message)
	}
}

// TestGitLabRecheckCountsResolvableOpen proves the live-thread count uses
// GitLab's own resolution pair: only a resolvable root note that is not
// resolved counts, system notes and plain MR comments never do, and every
// paged array is read.
func TestGitLabRecheckCountsResolvableOpen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want int
		bad  bool
	}{
		{name: "no discussions", raw: `[]`, want: 0},
		{name: "resolvable open", raw: `[{"notes":[{"resolvable":true,"resolved":false}]}]`, want: 1},
		{name: "resolved", raw: `[{"notes":[{"resolvable":true,"resolved":true}]}]`, want: 0},
		{name: "plain comment is not resolvable", raw: `[{"notes":[{"resolvable":false}]}]`, want: 0},
		{
			name: "system note skipped",
			raw:  `[{"notes":[{"system":true},{"resolvable":true,"resolved":false}]}]`, want: 1,
		},
		{
			name: "pages are adjacent arrays",
			raw:  `[{"notes":[{"resolvable":true}]}]` + `[{"notes":[{"resolvable":true}]}]`, want: 2,
		},
		{name: "object page is unreadable", raw: `{"message":"404 Not found"}`, bad: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertOpenCount(t, tc.raw, tc.want, tc.bad)
		})
	}
}

// writeGitLabBundle writes the bundle a GitLab fetch leaves plus the review
// the agent wrote.
func writeGitLabBundle(t *testing.T, provider, review string) {
	t.Helper()
	dir := ".verdict/state/7"
	pr := `{"provider":"` + provider + `","number":7,"state":"open","author":"alice",` +
		`"head_sha":"` + gitlabHead + `","base_sha":"` + gitlabBase + `"}`
	if err := writeTestFile(dir+"/PR.json", pr); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(dir+"/full.diff", gitlabTestDiff); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(dir+"/review.json", review); err != nil {
		t.Fatal(err)
	}
	seedPostControls(t, dir, gitlabBase)
}

// TestRunPostGitLabSubmits proves the whole verb posts to GitLab: the bundle's
// own provider picks the argv (never gh), the discussion and the summary note
// land, and the receipt and envelope carry the accepted review.
func TestRunPostGitLabSubmits(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGitLabBundle(t, "gitlab", gitlabBundleReview)
	cmdr := gitlabCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	assertGitLabRunData(t, resp)
	assertGitLabRunWrites(t, cmdr)
}

// TestRunPostGitLabApprovesWhenBound proves a bound approval reaches GitLab's
// own reads and approve call: the live-thread recheck and the authorship read
// run on glab, and the approval names the reviewed head.
func TestRunPostGitLabApprovesWhenBound(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGitLabBundle(t, "gitlab", `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}`)
	cmdr := gitlabCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{
		Approve: "true", Bind: true, Force: true, ReviewFile: ".verdict/state/7/review.json",
	}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Event != eventApprove {
		t.Errorf("event = %q, want APPROVE", resp.Data.Event)
	}
	if got := resp.Data.Review["state"]; got != "approved" {
		t.Errorf("envelope review state = %v, want approved", got)
	}
	assertGitLabApprovalCalls(t, cmdr)
}

// TestRunPostGitLabRefusesRepostFromFetchedMarker proves the repeat guard the
// fetched record feeds works on GitLab too: a summary note carrying this
// payload's marker refuses the second run before any write fires. The marker
// keys on the exact payload (body, anchors, reviewed head), never on a diff
// position, so a re-review of a moved diff stays a different payload on
// purpose.
func TestRunPostGitLabRefusesRepostFromFetchedMarker(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGitLabBundle(t, "gitlab", gitlabBundleReview)
	cmdr := gitlabCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	if _, ref := Run(context.Background(), req); ref != nil {
		t.Fatalf("first Run refusal: %v", ref)
	}
	note := gitlabPostedNote(t, cmdr)
	if !strings.Contains(note, "<!-- verdict-review payload=") {
		t.Fatalf("posted note carries no review marker: %q", note)
	}
	if ref := refetchThenRepost(t, note, &warnings, &human); ref == nil || ref.Code != codeAlreadyPosted {
		t.Fatalf("second Run ref = %v, want already-posted", ref)
	}
}

// TestRunPostRefusesHostWithoutSubmit proves the capability gate still refuses
// the hosts whose submit path is not implemented, before any read fires.
func TestRunPostRefusesHostWithoutSubmit(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGitLabBundle(t, "forgejo", gitlabBundleReview)
	cmdr := gitlabCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil || ref.Code != "provider-unimplemented" {
		t.Fatalf("ref = %v, want provider-unimplemented", ref)
	}
	if !strings.Contains(ref.Message, "forgejo") {
		t.Errorf("refusal = %q, want the provider named", ref.Message)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls fired for a refused host: %q", callKeys(cmdr))
	}
}

// assertGitLabPosition posts one anchor and pins the position GitLab receives:
// both paths from the diff, the MR's three SHAs, and the old/new line pair.
func assertGitLabPosition(t *testing.T, tc *gitlabPositionCase) {
	t.Helper()
	cmdr := gitlabCommander(t)
	payload := postPayload("review body", eventComment, gitlabHead, []any{tc.comment})
	attempt := gitlabPost(context.Background(), cmdr, core.CapabilitiesFor(providerGitLab), "acme/app", "7", gitlabBundleDir(t, tc.diff), payload)
	if !attempt.ok {
		t.Fatalf("gitlabPost = %+v, want the accepted attempt", attempt)
	}
	assertGitLabWriteOrder(t, cmdr)
	posted := gitlabStdinDoc(t, cmdr, 1)
	if posted[fieldBody] != "B" {
		t.Errorf("discussion body = %v, want the finding body", posted[fieldBody])
	}
	pos, ok := posted["position"].(map[string]any)
	if !ok {
		t.Fatalf("discussion carries no position: %v", posted)
	}
	assertPositionFields(t, pos, tc)
}

// assertGitLabWriteOrder pins the three writes one positioned payload makes and
// the order they land in: the MR read, the discussion, then the summary note.
func assertGitLabWriteOrder(t *testing.T, cmdr *scriptCommander) {
	t.Helper()
	if got := callKeys(cmdr); len(got) != 3 || got[0] != strings.Join(strings.Split(gitlabPRArgv, "\x00"), " ") {
		t.Fatalf("calls = %q, want the MR read, one discussion, and the summary note", got)
	}
	if countCalls(cmdr, gitlabDiscussionsArgv) != 1 || countCalls(cmdr, gitlabNotesArgv) != 1 {
		t.Fatalf("calls = %q, want exactly one discussion and one note", callKeys(cmdr))
	}
}

// assertPositionFields pins the thirteen position fields GitLab validates.
func assertPositionFields(t *testing.T, pos map[string]any, tc *gitlabPositionCase) {
	t.Helper()
	want := map[string]any{
		"position_type": "text",
		"base_sha":      gitlabBase,
		"start_sha":     gitlabStart,
		"head_sha":      gitlabHead,
		"old_path":      tc.oldPath,
		"new_path":      tc.bodyPath,
		"old_line":      tc.oldLine,
		"new_line":      tc.newLine,
	}
	for key, value := range want {
		if got, want := pos[key], value; got != want {
			t.Errorf("position.%s = %v, want %v (position %v)", key, got, want, pos)
		}
	}
	if _, has := pos["old_line"]; has != (tc.oldLine != nil) {
		t.Errorf("position carries old_line = %v, want %v", pos["old_line"], tc.oldLine)
	}
	if _, has := pos["new_line"]; has != (tc.newLine != nil) {
		t.Errorf("position carries new_line = %v, want %v", pos["new_line"], tc.newLine)
	}
}

// assertOpenCount pins one recheck payload: a readable page counts, an
// unreadable one errors instead of counting zero.
func assertOpenCount(t *testing.T, raw string, want int, bad bool) {
	t.Helper()
	got, err := countGitLabOpen(raw)
	if bad {
		if err == nil {
			t.Fatalf("countGitLabOpen(%s) = %d, want an unreadable-page error", raw, got)
		}
		return
	}
	if err != nil {
		t.Fatalf("countGitLabOpen(%s): %v", raw, err)
	}
	if got != want {
		t.Errorf("countGitLabOpen(%s) = %d, want %d", raw, got, want)
	}
}

// assertGitLabRunData pins the envelope a posted comment review returns.
func assertGitLabRunData(t *testing.T, resp *core.PostResponse) {
	t.Helper()
	if resp.Data.Event != eventComment || resp.Data.CommitID != gitlabHead {
		t.Errorf("data = %+v, want a COMMENT review of %s", resp.Data, gitlabHead)
	}
	if id, ok := resp.Data.Review["id"].(float64); !ok || id != 4242 {
		t.Errorf("envelope review id = %v, want the summary note id 4242", resp.Data.Review["id"])
	}
	if got := resp.Data.Review["state"]; got != "commented" {
		t.Errorf("envelope review state = %v, want commented", got)
	}
}

// assertGitLabRunWrites pins the writes a comment review makes: one discussion,
// no approval, one receipt, and exactly one marker on disk.
func assertGitLabRunWrites(t *testing.T, cmdr *scriptCommander) {
	t.Helper()
	for _, call := range cmdr.calls {
		if call[0] != "glab" {
			t.Fatalf("a GitLab run spawned %q, want glab only", strings.Join(call, " "))
		}
	}
	if got := countCalls(cmdr, gitlabDiscussionsArgv); got != 1 {
		t.Errorf("discussions = %d, want 1 (%q)", got, callKeys(cmdr))
	}
	if got := countCalls(cmdr, gitlabApproveArgv); got != 0 {
		t.Errorf("a comment review approved (%q)", callKeys(cmdr))
	}
	if !exists(".verdict/state/7/post.json") {
		t.Error("the accepted post wrote no receipt")
	}
	if matches := globMarkers(t); len(matches) != 1 {
		t.Errorf("duplicate markers = %v, want exactly one", matches)
	}
}

// assertGitLabApprovalCalls pins the three reads and writes a bound approval
// makes, once each, all of them on glab.
func assertGitLabApprovalCalls(t *testing.T, cmdr *scriptCommander) {
	t.Helper()
	for _, key := range []string{gitlabRecheckArgv, gitlabUserArgv, gitlabApproveArgv} {
		if got := countCalls(cmdr, key); got != 1 {
			t.Errorf("%q fired %d times, want 1 (%q)", key, got, callKeys(cmdr))
		}
	}
	for _, call := range cmdr.calls {
		if call[0] != "glab" {
			t.Fatalf("a GitLab approval spawned %q, want glab only", strings.Join(call, " "))
		}
	}
}

// refetchThenRepost turns the landed note into the fetched record, drops the
// local receipt and markers, and runs the verb again: the repeat guard must
// hold on the live record alone.
func dropLocalRecord(t *testing.T) {
	t.Helper()
	for _, marker := range globMarkers(t) {
		if err := os.Remove(marker); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(".verdict/state/7/post.json"); err != nil {
		t.Fatal(err)
	}
}

func refetchThenRepost(t *testing.T, note string, warnings, human *[]string) *core.RefusalError {
	t.Helper()
	record, err := json.Marshal(map[string]any{fieldBody: note, "created_at": "2026-11-02T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if werr := writeTestFile(".verdict/state/7/reviews.json", "["+string(record)+"]"); werr != nil {
		t.Fatal(werr)
	}
	dropLocalRecord(t)
	second := gitlabCommander(t)
	req := postTestReq(second, core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, warnings, human)
	_, ref := Run(context.Background(), req)
	for _, key := range []string{gitlabDiscussionsArgv, gitlabNotesArgv, gitlabApproveArgv} {
		if got := countCalls(second, key); got != 0 {
			t.Errorf("a refused repost fired %q %d times: %q", key, got, callKeys(second))
		}
	}
	return ref
}
