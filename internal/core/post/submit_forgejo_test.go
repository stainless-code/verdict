package postcore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	forgejoHead   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	forgejoAPI    = "https://forgejo.invalid/api/v1/repos/acme/app/pulls/7"
	forgejoAuthHd = "-H\x00Authorization: token eval-token"

	// The exact argv a Forgejo post fires. Every one is a literal: a rename in
	// the submit path fails here instead of following the helper that built it.
	forgejoPullRoute     = "curl\x00-sf\x00" + forgejoAuthHd + "\x00" + forgejoAPI
	forgejoUserRoute     = "curl\x00-sf\x00" + forgejoAuthHd + "\x00https://forgejo.invalid/api/v1/user"
	forgejoReviewsRoute  = "curl\x00-sf\x00" + forgejoAuthHd + "\x00" + forgejoAPI + "/reviews?limit=50&page=1"
	forgejoCommentsRoute = "curl\x00-sf\x00" + forgejoAuthHd + "\x00" + forgejoAPI + "/reviews/301/comments"
	forgejoWriteRoute    = "curl\x00-sS\x00-i\x00" + forgejoAuthHd +
		"\x00-H\x00Content-Type: application/json\x00-X\x00POST\x00--data-binary\x00@-\x00" + forgejoAPI + "/reviews"
)

// forgejoTestDiff is the reviewed change: one modified file with both sides
// indexed, and one deleted file that carries an OLD row alone.
const forgejoTestDiff = "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n" +
	" top\n-old line\n+new line\n bottom\n" +
	"diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\n--- a/gone.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n"

// forgejoEnv is the environment a Forgejo post resolves its host from.
func forgejoEnv() map[string]string {
	return map[string]string{"FORGEJO_HOST": "https://forgejo.invalid", "FORGEJO_TOKEN": "eval-token"}
}

// forgejoPostDir writes the bundle files a Forgejo post reads and returns the
// directory: the provider marker, the reviewed diff, and its hunk index.
func forgejoPostDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"PR.json":   `{"provider":"forgejo","number":7,"state":"open","head_sha":"` + forgejoHead + `","author":"mate"}`,
		"full.diff": forgejoTestDiff,
		"hunks.tsv": "gone.txt\tOLD\t1\t2\nnotes.txt\tOLD\t1\t3\nnotes.txt\tNEW\t1\t3\n",
	}
	for name, body := range files {
		if err := writeTestFile(dir+"/"+name, body); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// forgejoPayload renders one hosted payload with the given event and comments.
func forgejoPayload(event, comments string) string {
	return `{"body":"> ✅ Looks good","event":"` + event + `","commit_id":"` + forgejoHead + `","comments":[` + comments + `]}`
}

// forgejoAnswer renders one `curl -i` answer with a CRLF header block.
func forgejoAnswer(status, body string) string {
	return "HTTP/1.1 " + status + "\r\nContent-Type: application/json\r\n\r\n" + body + "\n"
}

// forgejoReviewBody is one created review in Forgejo's own shape.
func forgejoReviewBody(state string) string {
	return `{"id":901,"state":"` + state + `","commit_id":"` + forgejoHead + `",` +
		`"submitted_at":"2026-11-02T12:00:00Z","html_url":"https://forgejo.invalid/acme/app/pulls/7#issuecomment-901"}`
}

// forgejoCommander scripts the reads and the one write a Forgejo post fires.
func forgejoCommander(t *testing.T, write string) *scriptCommander {
	t.Helper()
	return &scriptCommander{t: t, out: map[string]scriptResult{
		forgejoPullRoute:    {stdout: `{"head":{"sha":"` + forgejoHead + `"}}`},
		forgejoUserRoute:    {stdout: `{"login":"rev"}`},
		forgejoReviewsRoute: {stdout: `[]`},
		forgejoWriteRoute:   {stdout: write},
	}}
}

// forgejoPost runs one payload through the Forgejo writer.
func forgejoPost(t *testing.T, cmdr Commander, dir, payload string) postAttempt {
	t.Helper()
	return forgejoHost{}.post(context.Background(), cmdr, forgejoEnv(),
		core.CapabilitiesFor(providerForgejo), "acme/app", "7", dir, payload)
}

// postedBodyDoc decodes the write call's stdin.
func postedBodyDoc(t *testing.T, cmdr *scriptCommander, route string) map[string]any {
	t.Helper()
	for i, call := range cmdr.calls {
		if strings.Join(call, "\x00") != route {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(cmdr.stdin[i]), &doc); err != nil {
			t.Fatalf("write stdin is not one JSON object: %v\n%s", err, cmdr.stdin[i])
		}
		return doc
	}
	t.Fatalf("no write call fired: %q", callKeys(cmdr))
	return nil
}

// TestForgejoEventMap pins the one place the two hosts spell an event
// differently: APPOVE must go out as APPROVED, because Forgejo turns an event
// it does not know into a PENDING draft with a 200.
func TestForgejoEventMap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		verdict string
		want    string
	}{
		{verdict: eventApprove, want: "APPROVED"},
		{verdict: eventRequestChanges, want: "REQUEST_CHANGES"},
		{verdict: eventComment, want: "COMMENT"},
		{verdict: "APPROVAL", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.verdict, func(t *testing.T) {
			t.Parallel()
			if got := forgejoEvent(tc.verdict); got != tc.want {
				t.Errorf("forgejoEvent(%q) = %q, want %q", tc.verdict, got, tc.want)
			}
		})
	}
}

// TestForgejoWriteRequest proves the wire body: the host's event spelling, the
// reviewed commit, one anchor per placeable finding on the right side, and the
// three withheld findings named in the envelope warnings.
func TestForgejoWriteRequest(t *testing.T) {
	t.Parallel()
	dir := forgejoPostDir(t)
	cmdr := forgejoCommander(t, forgejoAnswer("200 OK", forgejoReviewBody("REQUEST_CHANGES")))
	comment := `{"path":"notes.txt","body":"right side","line":2,"start_line":1,"side":"RIGHT"},` +
		`{"path":"b/notes.txt","body":"old side","line":2,"side":"LEFT"},` +
		`{"path":"notes.txt","body":"no line","line":"abc","side":"RIGHT"},` +
		`{"path":"gone.txt","body":"deleted","line":1,"side":"LEFT"}`
	attempt := forgejoPost(t, cmdr, dir, forgejoPayload(eventRequestChanges, comment))
	if !attempt.ok {
		t.Fatalf("post refused: %s", attempt.message)
	}
	body := postedBodyDoc(t, cmdr, forgejoWriteRoute)
	if got := body["event"]; got != "REQUEST_CHANGES" {
		t.Errorf("wire event = %v, want REQUEST_CHANGES", got)
	}
	if got := body["commit_id"]; got != forgejoHead {
		t.Errorf("wire commit_id = %v, want the reviewed head", got)
	}
	comments := jq.Elements(body["comments"])
	if len(comments) != 2 {
		t.Fatalf("wire comments = %v, want the two placeable findings", body["comments"])
	}
	assertForgejoWireAnchors(t, commentMap(t, comments[0]), commentMap(t, comments[1]))
	want := []string{
		"forgejo anchors need a numeric line — 1 finding(s) withheld from the review",
		"forgejo cannot anchor a comment on a file the change deletes — 1 finding(s) withheld at submit",
		"forgejo anchors are single-line — 1 multi-line anchor(s) anchored at their end line",
	}
	if strings.Join(attempt.warnings, "\n") != strings.Join(want, "\n") {
		t.Errorf("warnings = %q, want %q", attempt.warnings, want)
	}
}

// assertForgejoWireAnchors pins the two side mappings: a RIGHT anchor is a
// new_position on the file line, a LEFT anchor is an old_position, and neither
// carries the other host's range fields.
func assertForgejoWireAnchors(t *testing.T, right, left map[string]any) {
	t.Helper()
	if right["path"] != "notes.txt" || right["new_position"] != float64(2) {
		t.Errorf("right anchor = %v, want notes.txt new_position 2", right)
	}
	assertForgejoAbsent(t, right, "old_position", "extra_lines_count", "start_line")
	if left["path"] != "notes.txt" || left["old_position"] != float64(2) {
		t.Errorf("left anchor = %v, want the b/ prefix stripped and old_position 2", left)
	}
	assertForgejoAbsent(t, left, "new_position")
}

// assertForgejoAbsent fails when a comment carries a field this host must never
// send: Forgejo reads new_position/old_position as file lines, and a range
// field would silently change the anchor.
func assertForgejoAbsent(t *testing.T, comment map[string]any, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if _, has := comment[field]; has {
			t.Errorf("anchor carries %s: %v", field, comment)
		}
	}
}

// commentMap reads one decoded comment object.
func commentMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("comment is not an object: %v", v)
	}
	return m
}

// TestForgejoApprovalEvent proves the approved review goes out under Forgejo's
// spelling and that the receipt records the state the host answered.
func TestForgejoApprovalEvent(t *testing.T) {
	t.Parallel()
	dir := forgejoPostDir(t)
	cmdr := forgejoCommander(t, forgejoAnswer("200 OK", forgejoReviewBody("APPROVED")))
	attempt := forgejoPost(t, cmdr, dir, forgejoPayload(eventApprove, `{"path":"notes.txt","body":"ok","line":2,"side":"RIGHT"}`))
	if !attempt.ok {
		t.Fatalf("approve refused: %s", attempt.message)
	}
	if got := postedBodyDoc(t, cmdr, forgejoWriteRoute)["event"]; got != "APPROVED" {
		t.Errorf("wire event = %v, want APPROVED", got)
	}
	var recorded map[string]any
	if err := json.Unmarshal([]byte(attempt.posted), &recorded); err != nil {
		t.Fatalf("receipt is not one JSON object: %v\n%s", err, attempt.posted)
	}
	if recorded["state"] != "APPROVED" || recorded["id"] != float64(901) {
		t.Errorf("receipt = %v, want state APPROVED and id 901", recorded)
	}
}

// TestForgejoPendingAnswerRefuses proves the post-condition: a 200 that answers
// PENDING is a refusal naming the mismatch, never a receipt, because Forgejo
// creates a draft nobody sees when the event is not one it knows.
func TestForgejoPendingAnswerRefuses(t *testing.T) {
	t.Parallel()
	dir := forgejoPostDir(t)
	cmdr := forgejoCommander(t, forgejoAnswer("200 OK", forgejoReviewBody("PENDING")))
	attempt := forgejoPost(t, cmdr, dir, forgejoPayload(eventComment, ""))
	if attempt.ok {
		t.Fatal("a PENDING answer was accepted as a posted review")
	}
	want := "post failed: forgejo answered state PENDING, not the requested COMMENT"
	if !strings.Contains(attempt.message, want) {
		t.Errorf("refusal = %q, want %q", attempt.message, want)
	}
	if attempt.posted != "" {
		t.Errorf("a refused attempt carries a receipt: %q", attempt.posted)
	}
}

// TestForgejoNoCommitRefuses proves the reviewed commit is never
// substituted: an empty commit_id lets Forgejo pin the review to its head.
func TestForgejoNoCommitRefuses(t *testing.T) {
	t.Parallel()
	dir := forgejoPostDir(t)
	cmdr := forgejoCommander(t, forgejoAnswer("200 OK", forgejoReviewBody("COMMENT")))
	payload := `{"body":"x","event":"COMMENT","commit_id":null,"comments":[]}`
	attempt := forgejoPost(t, cmdr, dir, payload)
	if !strings.Contains(attempt.message, "carries no commit_id") {
		t.Fatalf("refusal = %q, want the missing commit_id", attempt.message)
	}
	if n := countCalls(cmdr, forgejoWriteRoute); n != 0 {
		t.Errorf("write calls = %d, want none before the commit guard", n)
	}
}

// TestForgejoDraftGateRefusesSelfDraft proves a pre-existing self-authored
// PENDING review refuses with the submit-or-discard hint and no write:
// Forgejo's comment create would attach the comments to that draft.
func TestForgejoDraftGateRefusesSelfDraft(t *testing.T) {
	t.Parallel()
	dir := forgejoPostDir(t)
	cmdr := forgejoCommander(t, forgejoAnswer("200 OK", forgejoReviewBody("COMMENT")))
	cmdr.out[forgejoReviewsRoute] = scriptResult{
		stdout: `[{"id":301,"state":"PENDING","user":{"login":"rev"}}]`,
	}
	attempt := forgejoPost(t, cmdr, dir, forgejoPayload(eventComment, ""))
	if !strings.Contains(attempt.message, "an unsubmitted review draft exists on this PR") ||
		!strings.Contains(attempt.message, "submit or discard") {
		t.Fatalf("refusal = %q, want the submit-or-discard hint", attempt.message)
	}
	if n := countCalls(cmdr, forgejoWriteRoute); n != 0 {
		t.Errorf("write calls = %d, want none while a draft exists", n)
	}
}

// TestForgejoDraftGateAllowsStrangerDraft proves another user's draft does not
// refuse: Forgejo attaches a comment to the token user's own draft, so someone
// else's in-progress review is not this post's to submit.
func TestForgejoDraftGateAllowsStrangerDraft(t *testing.T) {
	t.Parallel()
	dir := forgejoPostDir(t)
	cmdr := forgejoCommander(t, forgejoAnswer("200 OK", forgejoReviewBody("COMMENT")))
	cmdr.out[forgejoReviewsRoute] = scriptResult{
		stdout: `[{"id":301,"state":"PENDING","user":{"login":"mate"}}]`,
	}
	attempt := forgejoPost(t, cmdr, dir, forgejoPayload(eventComment, ""))
	if !attempt.ok {
		t.Fatalf("post refused on another user's draft: %s", attempt.message)
	}
	if n := countCalls(cmdr, forgejoWriteRoute); n != 1 {
		t.Errorf("write calls = %d, want the one review", n)
	}
}

// TestForgejoDraftGateUnreadableUserRefuses proves the draft gate fails closed:
// without the token user's login verdict cannot tell a stranger's draft from
// its own, so the write is refused instead of risking publishing that draft.
func TestForgejoDraftGateUnreadableUserRefuses(t *testing.T) {
	t.Parallel()
	dir := forgejoPostDir(t)
	cmdr := forgejoCommander(t, forgejoAnswer("200 OK", forgejoReviewBody("COMMENT")))
	cmdr.out[forgejoUserRoute] = scriptResult{stdout: "not json"}
	attempt := forgejoPost(t, cmdr, dir, forgejoPayload(eventComment, ""))
	if !strings.Contains(attempt.message, "could not name the token user") {
		t.Fatalf("refusal = %q, want the unreadable user read named", attempt.message)
	}
	if n := countCalls(cmdr, forgejoWriteRoute); n != 0 {
		t.Errorf("write calls = %d, want none before the draft gate completes", n)
	}
}

// TestParseForgejoResponse proves the `-i` split: the status line, the header
// block, and the body, with CRLF and LF both accepted and a header-only answer
// read as an empty body.
func TestParseForgejoResponse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		raw    string
		body   string
		status int
		ok     bool
	}{
		{
			name:   "crlf",
			raw:    "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"id\":901}",
			status: 200, body: `{"id":901}`, ok: true,
		},
		{
			name:   "lf",
			raw:    "HTTP/2 422 Unprocessable Entity\nContent-Type: application/json\n\n{\"message\":\"approve your own pull is not allowed\"}",
			status: 422, body: `{"message":"approve your own pull is not allowed"}`, ok: true,
		},
		{name: "headers only", raw: "HTTP/1.1 204 No Content\r\n\r\n", status: 204, ok: true},
		{name: "no status line", raw: "curl: (6) Could not resolve host", ok: false},
		{name: "empty", raw: "", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertForgejoResponse(t, tc.raw, tc.status, tc.body, tc.ok)
		})
	}
}

// assertForgejoResponse checks one parse against the literal it must produce.
func assertForgejoResponse(t *testing.T, raw string, status int, body string, ok bool) {
	t.Helper()
	got, parsed := parseForgejoResponse(raw)
	if parsed != ok {
		t.Fatalf("parse ok = %v, want %v (%+v)", parsed, ok, got)
	}
	if !parsed {
		return
	}
	if got.status != status || got.body != body {
		t.Errorf("parse = %+v, want status %d body %q", got, status, body)
	}
}

// forgejoState builds a post state over one bundle dir, so the write goes
// through the retry loop the shipped path uses.
func forgejoState(t *testing.T, dir string, cmdr Commander) *state {
	t.Helper()
	return &state{
		req: &core.PostRequest{
			Runtime: &core.Runtime{Env: forgejoEnv(), Commander: cmdr, Now: func() time.Time { return postTestTime }},
			Repo:    "acme/app", Pr: "7", Effective: core.Defaults(),
		},
		dir: dir,
	}
}

// TestForgejoNon200QuotesHostAndNeverRetries proves a 422 is terminal: the
// refusal quotes the host's own message from the `-i` body, and the write fires
// exactly once, because a replay could append the comments again.
func TestForgejoNon200QuotesHostAndNeverRetries(t *testing.T) {
	t.Parallel()
	dir := forgejoPostDir(t)
	cmdr := forgejoCommander(t, forgejoAnswer("422 Unprocessable Entity",
		`{"message":"approve your own pull is not allowed"}`))
	st := forgejoState(t, dir, cmdr)
	posted, ref := st.postOneWithRetry(context.Background(), cmdr, forgejoPayload(eventComment, ""))
	if posted != "" || ref == nil {
		t.Fatalf("posted = %q, refusal = %v; want a refusal", posted, ref)
	}
	if ref.Code != codePostFailed {
		t.Errorf("refusal code = %q, want post-failed", ref.Code)
	}
	if !strings.Contains(ref.Message, "approve your own pull is not allowed") {
		t.Errorf("refusal = %q, want the host's own message", ref.Message)
	}
	if n := countCalls(cmdr, forgejoWriteRoute); n != 1 {
		t.Errorf("write calls = %d, want exactly 1 (a partial write is never replayed)", n)
	}
}

// TestForgejoReads proves the drift read, the token-user read, and the open
// thread recheck: one unresolved comment counts, a resolved one does not, and a
// pending review's comments are never read because a draft is not visible.
func TestForgejoReads(t *testing.T) {
	t.Parallel()
	dir := forgejoPostDir(t)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		forgejoPullRoute:     {stdout: `{"head":{"sha":"` + forgejoHead + `"}}`},
		forgejoUserRoute:     {stdout: `{"login":"rev"}`},
		forgejoReviewsRoute:  {stdout: `[{"id":301,"state":"COMMENT"},{"id":302,"state":"PENDING"}]`},
		forgejoCommentsRoute: {stdout: `[{"id":401},{"id":402,"resolver":{"login":"rev"}}]`},
	}}
	host := forgejoHost{}
	sha, err := host.liveSHA(context.Background(), cmdr, forgejoEnv(), "acme/app", "7")
	if err != nil || sha != forgejoHead {
		t.Errorf("liveSHA = %q, %v; want the fixture head", sha, err)
	}
	me, err := host.selfLogin(context.Background(), cmdr, forgejoEnv())
	if err != nil || me != "rev" {
		t.Errorf("selfLogin = %q, %v; want rev", me, err)
	}
	assertForgejoRecheck(t, host, cmdr, dir)
}

// assertForgejoRecheck checks the open-thread count and the pending review the
// recheck must never read.
func assertForgejoRecheck(t *testing.T, host forgejoHost, cmdr *scriptCommander, dir string) {
	t.Helper()
	open, reason, err := host.recheck(context.Background(), cmdr, forgejoEnv(), "acme/app", "7", dir)
	if err != nil || reason != "" {
		t.Fatalf("recheck = %d, %q, %v", open, reason, err)
	}
	if open != 1 {
		t.Errorf("open threads = %d, want the one unresolved comment", open)
	}
	for _, call := range cmdr.calls {
		if strings.Contains(strings.Join(call, " "), "reviews/302/comments") {
			t.Errorf("recheck read a pending review's comments: %q", call)
		}
	}
}

// TestForgejoHostSeam proves the provider switch returns the curl writer and a
// reader that gates the environment before any call.
func TestForgejoHostSeam(t *testing.T) {
	t.Parallel()
	writer, reader := hostFor(providerForgejo)
	if got := writer.tool(); got != "curl" {
		t.Errorf("forgejo tool = %q, want curl", got)
	}
	if ref := reader.auth(forgejoEnv()); ref != nil {
		t.Errorf("auth refused a complete environment: %v", ref)
	}
	ref := reader.auth(map[string]string{})
	if ref == nil || ref.Code != "auth-missing" {
		t.Fatalf("auth = %v, want auth-missing", ref)
	}
	if want := "forgejo needs FORGEJO_HOST=https://host and FORGEJO_TOKEN set"; ref.Message != want {
		t.Errorf("auth message = %q, want %q", ref.Message, want)
	}
}

// TestForgejoFlattenCountIsLiteral pins the multi-line warning's count, so a
// flatten that silently keeps its range cannot pass on a zero.
func TestForgejoFlattenCountIsLiteral(t *testing.T) {
	t.Parallel()
	dir := forgejoPostDir(t)
	part, ok := parseForgejoPayload(dir, forgejoPayload(eventComment,
		`{"path":"notes.txt","body":"a","line":2,"start_line":1,"side":"RIGHT"},`+
			`{"path":"notes.txt","body":"b","line":3,"start_line":1,"side":"RIGHT"}`))
	if !ok {
		t.Fatal("payload unreadable")
	}
	want := "forgejo anchors are single-line — 2 multi-line anchor(s) anchored at their end line"
	if len(part.warnings) != 1 || part.warnings[0] != want {
		t.Errorf("warnings = %q, want %q", part.warnings, want)
	}
	if len(part.comments) != 2 || part.comments[1].line != 3 {
		t.Errorf("comments = %+v, want both anchored at their end line", part.comments)
	}
}
