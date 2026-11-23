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
	bitbucketHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bitbucketAPI  = "https://api.bitbucket.org/2.0/repositories/acme/app/pullrequests/7"

	// The exact argv a Bitbucket post fires. Every one is a literal: a
	// rename in the submit path fails here instead of following the
	// helper that built it. Reads carry the credential on stdin; writes
	// stage it in a private 0600 file, so its path is the CONFIG
	// placeholder rather than a random temp name.
	bitbucketReadPrefix   = "curl\x00--config\x00-\x00-sf\x00"
	bitbucketFollowPrefix = "curl\x00--config\x00-\x00-sfL\x00"
	bitbucketPRRoute      = bitbucketReadPrefix + bitbucketAPI
	bitbucketUserRoute    = bitbucketReadPrefix + "https://api.bitbucket.org/2.0/user"
	bitbucketListRoute    = bitbucketFollowPrefix + bitbucketAPI + "/comments?pagelen=100"
	bitbucketWritePrefix  = "curl\x00--config\x00" + configPlaceholder + "\x00-sS\x00-i\x00" +
		"-H\x00Content-Type: application/json\x00-X\x00POST\x00--data-binary\x00@-\x00"
	bitbucketWriteRoute   = bitbucketWritePrefix + bitbucketAPI + "/comments"
	bitbucketApproveRoute = bitbucketWritePrefix + bitbucketAPI + "/approve"
	bitbucketUpdateRoute  = "curl\x00--config\x00" + configPlaceholder + "\x00-sS\x00-i\x00" +
		"-H\x00Content-Type: application/json\x00-X\x00PUT\x00--data-binary\x00@-\x00" +
		bitbucketAPI + "/comments/771"
)

// bitbucketTestDiff is the reviewed change: one modified file with both
// sides indexed, and one deleted file that carries an OLD row alone.
const bitbucketTestDiff = "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n" +
	" top\n-old line\n+new line\n bottom\n" +
	"diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\n--- a/gone.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n"

// bitbucketEnv is the environment a Bitbucket post resolves its host
// from.
func bitbucketEnv() map[string]string {
	return map[string]string{"BITBUCKET_TOKEN": "eval-token"}
}

// bitbucketPostDir writes the bundle files a Bitbucket post reads and
// returns the directory: the provider marker, the reviewed diff, and
// its hunk index.
func bitbucketPostDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"PR.json":   `{"provider":"bitbucket","number":7,"state":"open","head_sha":"` + bitbucketHead + `","author":"mate"}`,
		"full.diff": bitbucketTestDiff,
		"hunks.tsv": "gone.txt\tOLD\t1\t2\nnotes.txt\tOLD\t1\t3\nnotes.txt\tNEW\t1\t3\n",
	}
	for name, body := range files {
		if err := writeTestFile(dir+"/"+name, body); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// bitbucketPayload renders one hosted payload with the given event and
// comments.
func bitbucketPayload(event, comments string) string {
	return `{"body":"> ✅ Looks good","event":"` + event + `","commit_id":"` + bitbucketHead + `","comments":[` + comments + `]}`
}

// bitbucketAnswer renders one `curl -i` answer with a CRLF header block.
func bitbucketAnswer(status, body string) string {
	return "HTTP/1.1 " + status + "\r\nContent-Type: application/json\r\n\r\n" + body + "\n"
}

// bitbucketCreated renders one created comment in Bitbucket's own shape.
func bitbucketCreated(id int) string {
	return `{"id":` + intStr(id) + `,"content":{"raw":"x"},` +
		`"links":{"html":{"href":"https://bitbucket.org/acme/app/pullrequests/7/comments/` + intStr(id) + `"}},` +
		`"created_on":"2026-11-02T12:00:00Z"}`
}

func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// bitbucketPRJSON is the PR read the head comes from.
func bitbucketPRJSON(head string) string {
	return `{"source":{"commit":{"hash":"` + head + `"}},` +
		`"links":{"html":{"href":"https://bitbucket.org/acme/app/pullrequests/7"}}}`
}

// bitbucketCommander scripts the reads and the writes a Bitbucket post
// fires. Inline and summary writes share one argv, so they share one
// answer.
func bitbucketCommander(t *testing.T, write string) *scriptCommander {
	t.Helper()
	out := map[string]scriptResult{
		bitbucketPRRoute:      {stdout: bitbucketPRJSON(bitbucketHead)},
		bitbucketUserRoute:    {stdout: `{"nickname":"rev"}`},
		bitbucketListRoute:    {stdout: `{"values":[],"next":null}`},
		bitbucketWriteRoute:   {stdout: write},
		bitbucketApproveRoute: {stdout: "HTTP/1.1 204 No Content\r\n\r\n"},
	}
	return &scriptCommander{t: t, out: out}
}

// bitbucketPost runs one payload through the Bitbucket writer.
func bitbucketPost(t *testing.T, cmdr Commander, dir, payload string) postAttempt {
	t.Helper()
	return bitbucketHost{}.post(context.Background(), cmdr, bitbucketEnv(),
		core.CapabilitiesFor(providerBitbucket), "acme/app", "7", dir, payload)
}

// bitbucketWriteDoc decodes the nth comments write call's stdin.
func bitbucketWriteDoc(t *testing.T, cmdr *scriptCommander, nth int) map[string]any {
	t.Helper()
	seen := 0
	for i, call := range cmdr.calls {
		if routeKey(call) != bitbucketWriteRoute {
			continue
		}
		if seen != nth {
			seen++
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(cmdr.stdin[i]), &doc); err != nil {
			t.Fatalf("write stdin is not one JSON object: %v\n%s", err, cmdr.stdin[i])
		}
		return doc
	}
	t.Fatalf("no write call %d fired on %q: %q", nth, bitbucketWriteRoute, callKeys(cmdr))
	return nil
}

// TestBitbucketInlineAndSummary proves the wire order and bodies: one
// inline comment per finding, the summary comment carrying the review
// body, then the approval last, with the receipt recording the summary.
func TestBitbucketInlineAndSummary(t *testing.T) {
	t.Parallel()
	dir := bitbucketPostDir(t)
	cmdr := bitbucketCommander(t, bitbucketAnswer("201 Created", bitbucketCreated(501)))
	comment := `{"path":"notes.txt","body":"right side","line":2,"start_line":1,"side":"RIGHT"},` +
		`{"path":"b/notes.txt","body":"old side","line":2,"side":"LEFT"}`
	attempt := bitbucketPost(t, cmdr, dir, bitbucketPayload(eventApprove, comment))
	if !attempt.ok {
		t.Fatalf("post refused: %s", attempt.message)
	}
	if got := countCalls(cmdr, bitbucketWriteRoute); got != 3 {
		t.Fatalf("comment writes = %d, want 3 (two inline, one summary): %q", got, callKeys(cmdr))
	}
	if got := countCalls(cmdr, bitbucketApproveRoute); got != 1 {
		t.Fatalf("approve writes = %d, want 1: %q", got, callKeys(cmdr))
	}
	right := bitbucketWriteDoc(t, cmdr, 0)
	assertBitbucketInline(t, right, "to", 2)
	left := bitbucketWriteDoc(t, cmdr, 1)
	assertBitbucketInline(t, left, "from", 2)
	summary := bitbucketWriteDoc(t, cmdr, 2)
	if body := bitbucketCommentRaw(t, summary); !strings.Contains(body, "Looks good") {
		t.Errorf("summary body = %q, want the review prose", body)
	}
	if _, inline := summary["inline"]; inline {
		t.Errorf("summary carries an inline anchor: %v", summary)
	}
	assertBitbucketReceipt(t, &attempt)
}

// assertBitbucketReceipt pins the accepted review: the summary comment
// id, the approved state, and the pull-request page.
func assertBitbucketReceipt(t *testing.T, attempt *postAttempt) {
	t.Helper()
	var receipt map[string]any
	if err := json.Unmarshal([]byte(attempt.posted), &receipt); err != nil {
		t.Fatalf("receipt is not one JSON object: %v\n%s", err, attempt.posted)
	}
	if receipt["state"] != "approved" {
		t.Errorf("receipt state = %v, want approved", receipt["state"])
	}
	if receipt["id"] != float64(501) {
		t.Errorf("receipt id = %v, want the summary comment id 501", receipt["id"])
	}
	if receipt["html_url"] != "https://bitbucket.org/acme/app/pullrequests/7" {
		t.Errorf("receipt html_url = %v, want the PR page", receipt["html_url"])
	}
	if receipt["submitted_at"] != "2026-11-02T12:00:00Z" {
		t.Errorf("receipt submitted_at = %v, want the summary timestamp", receipt["submitted_at"])
	}
	if len(attempt.inlineLinks) != 2 {
		t.Errorf("inline links = %d, want 2", len(attempt.inlineLinks))
	}
}

// assertBitbucketInline pins one anchor: the reviewed path, one side
// field, never the other side's field.
func assertBitbucketInline(t *testing.T, doc map[string]any, side string, line int) {
	t.Helper()
	inline, ok := doc["inline"].(map[string]any)
	if !ok {
		t.Fatalf("write carries no inline anchor: %v", doc)
	}
	if inline["path"] != "notes.txt" {
		t.Errorf("inline path = %v, want notes.txt", inline["path"])
	}
	if inline[side] != float64(line) {
		t.Errorf("inline %s = %v, want %d", side, inline[side], line)
	}
	other := "to"
	if side == "to" {
		other = "from"
	}
	if _, has := inline[other]; has {
		t.Errorf("anchor carries both sides: %v", inline)
	}
}

// bitbucketCommentRaw reads the raw body of one posted comment document.
func bitbucketCommentRaw(t *testing.T, doc map[string]any) string {
	t.Helper()
	content, ok := doc["content"].(map[string]any)
	if !ok || content == nil {
		return ""
	}
	return jq.StrOf(content["raw"])
}

// TestBitbucketUnanchorableWithheld proves the anchor honesty: a finding
// with no numeric line never lands inline, and the loss is counted
// instead of mis-anchored. The summary still posts.
func TestBitbucketUnanchorableWithheld(t *testing.T) {
	t.Parallel()
	dir := bitbucketPostDir(t)
	cmdr := bitbucketCommander(t, bitbucketAnswer("201 Created", bitbucketCreated(501)))
	comment := `{"path":"notes.txt","body":"no line","line":"abc","side":"RIGHT"},` +
		`{"path":"gone.txt","body":"deleted","line":1,"side":"LEFT"},` +
		`{"path":"notes.txt","body":"kept","line":2,"side":"RIGHT"}`
	attempt := bitbucketPost(t, cmdr, dir, bitbucketPayload(eventComment, comment))
	if !attempt.ok {
		t.Fatalf("post refused: %s", attempt.message)
	}
	if got := countCalls(cmdr, bitbucketWriteRoute); got != 2 {
		t.Fatalf("on the wire = %d, want 2 (one inline, one summary): %q", got, callKeys(cmdr))
	}
	assertBitbucketNoDeletedAnchor(t, cmdr)
	want := []string{
		"bitbucket anchors need a path, numeric line, and body — 1 finding(s) withheld from the review",
		"bitbucket cannot anchor a comment on a file the change deletes — 1 finding(s) withheld at submit",
	}
	if strings.Join(attempt.warnings, "\n") != strings.Join(want, "\n") {
		t.Errorf("warnings = %q, want %q", attempt.warnings, want)
	}
}

// assertBitbucketNoDeletedAnchor proves no write anchored the deleted-file
// finding.
func assertBitbucketNoDeletedAnchor(t *testing.T, cmdr *scriptCommander) {
	t.Helper()
	for i, raw := range cmdr.stdin {
		if bitbucketWriteIsDeletedAnchor(raw) {
			t.Errorf("write %d anchored a deleted-file finding: %s", i, raw)
		}
	}
}

// bitbucketWriteIsDeletedAnchor reports whether one stdin document
// anchors the deleted file.
func bitbucketWriteIsDeletedAnchor(raw string) bool {
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return false
	}
	inline, ok := doc["inline"].(map[string]any)
	return ok && inline["path"] == "gone.txt"
}

// TestBitbucketStaleApproveRefuses proves the stale-head refusal: an
// approval carries no commit pin, so a head that moved since the review
// refuses before any write instead of approving unreviewed code.
func TestBitbucketStaleApproveRefuses(t *testing.T) {
	t.Parallel()
	dir := bitbucketPostDir(t)
	cmdr := bitbucketCommander(t, bitbucketAnswer("201 Created", bitbucketCreated(501)))
	cmdr.out[bitbucketPRRoute] = scriptResult{stdout: bitbucketPRJSON("ffffffffffffffffffffffffffffffffffffffff")}
	attempt := bitbucketPost(t, cmdr, dir, bitbucketPayload(eventApprove, ""))
	if attempt.ok {
		t.Fatal("a stale approval was accepted")
	}
	if !strings.Contains(attempt.message, "head moved since fetch") {
		t.Errorf("refusal = %q, want the stale head named", attempt.message)
	}
	if got := countCalls(cmdr, bitbucketWriteRoute); got != 0 {
		t.Errorf("comment writes = %d, want none before the stale guard", got)
	}
	if got := countCalls(cmdr, bitbucketApproveRoute); got != 0 {
		t.Errorf("approve writes = %d, want none on a stale head", got)
	}
}

// TestBitbucketStaleCommentPosts proves a comment post on a moved head
// still proceeds: the drift warning is the service's, and the host
// writes the comments plus the summary without approving.
func TestBitbucketStaleCommentPosts(t *testing.T) {
	t.Parallel()
	dir := bitbucketPostDir(t)
	cmdr := bitbucketCommander(t, bitbucketAnswer("201 Created", bitbucketCreated(501)))
	cmdr.out[bitbucketPRRoute] = scriptResult{stdout: bitbucketPRJSON("ffffffffffffffffffffffffffffffffffffffff")}
	attempt := bitbucketPost(t, cmdr, dir, bitbucketPayload(eventComment, ""))
	if !attempt.ok {
		t.Fatalf("comment post refused on a moved head: %s", attempt.message)
	}
	if got := countCalls(cmdr, bitbucketWriteRoute); got != 1 {
		t.Errorf("summary writes = %d, want 1: %q", got, callKeys(cmdr))
	}
	if got := countCalls(cmdr, bitbucketApproveRoute); got != 0 {
		t.Errorf("a comment approved the moved head (%q)", callKeys(cmdr))
	}
}

// TestBitbucketNoCommitRefuses proves the reviewed commit is never
// substituted: an empty commit_id would let the approval cover the
// current head instead of the reviewed one.
func TestBitbucketNoCommitRefuses(t *testing.T) {
	t.Parallel()
	dir := bitbucketPostDir(t)
	cmdr := bitbucketCommander(t, bitbucketAnswer("201 Created", bitbucketCreated(501)))
	payload := `{"body":"x","event":"COMMENT","commit_id":null,"comments":[]}`
	attempt := bitbucketPost(t, cmdr, dir, payload)
	if !strings.Contains(attempt.message, "carries no commit_id") {
		t.Fatalf("refusal = %q, want the missing commit_id", attempt.message)
	}
	if got := countCalls(cmdr, bitbucketWriteRoute); got != 0 {
		t.Errorf("writes = %d, want none before the commit guard", got)
	}
}

// TestBitbucketDuplicatePayloadRefuses proves the duplicate-post
// refusal: a general comment already carrying the same payload marker
// refuses with duplicate-payload and no write.
func TestBitbucketDuplicatePayloadRefuses(t *testing.T) {
	t.Parallel()
	dir := bitbucketPostDir(t)
	cmdr := bitbucketCommander(t, bitbucketAnswer("201 Created", bitbucketCreated(501)))
	st := &state{
		req: &Request{Runtime: &core.Runtime{Env: bitbucketEnv(), Commander: cmdr}, Repo: "acme/app", Pr: "7"},
		dir: dir, commitID: bitbucketHead, flags: Flags{},
	}
	// Build the same body the service would post, marker included.
	st.body, st.comments = "> ✅ Looks good", nil
	st.payloads = []string{postPayload(st.body, eventComment, bitbucketHead, st.comments)}
	st.phash = contentDigest(st.body, st.commitID, st.comments)
	st.body += "\n\n" + "<!-- verdict-review payload=" + st.phash + " head=" + bitbucketHead + " -->"
	st.payloads = []string{postPayload(st.body, eventComment, bitbucketHead, st.comments)}
	marked := st.body
	// The live list carries that exact general comment.
	cmdr.out[bitbucketListRoute] = scriptResult{stdout: `{"values":[{"id":900,"content":{"raw":` + quotedJSON(t, marked) +
		`},"user":{"nickname":"rev"},"created_on":"2026-11-02T12:00:00Z"}],"next":null}`}
	if ref := st.refreshRemote(context.Background()); ref != nil {
		t.Fatalf("refreshRemote refusal: %v", ref)
	}
	if ref := st.remoteDiff(); ref == nil || ref.Code != "duplicate-payload" {
		t.Fatalf("remoteDiff = %v, want duplicate-payload", ref)
	}
	if got := countCalls(cmdr, bitbucketApproveRoute); got != 0 {
		t.Errorf("approve writes = %d, want none on a duplicate", got)
	}
}

// quotedJSON renders one string as a JSON string literal.
func quotedJSON(t *testing.T, s string) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestBitbucketAuthFailure proves a 401 on the PR read refuses
// post-failed with the host's own message and no write, and that a
// missing credential refuses auth-missing before any call.
func TestBitbucketAuthFailure(t *testing.T) {
	t.Parallel()
	dir := bitbucketPostDir(t)
	cmdr := bitbucketCommander(t, bitbucketAnswer("201 Created", bitbucketCreated(501)))
	cmdr.out[bitbucketPRRoute] = scriptResult{stderr: "curl: (22) The requested URL returned error: 401 authentication required\n", fail: true}
	attempt := bitbucketPost(t, cmdr, dir, bitbucketPayload(eventComment, ""))
	if attempt.ok {
		t.Fatal("a 401 was accepted")
	}
	if !strings.Contains(attempt.message+attempt.errText, "authentication required") {
		t.Errorf("refusal = %q, want the host's message", attempt.message+attempt.errText)
	}
	if got := countCalls(cmdr, bitbucketWriteRoute); got != 0 {
		t.Errorf("writes = %d, want none on a 401", got)
	}
	host := bitbucketHost{}
	if ref := host.auth(map[string]string{}); ref == nil || ref.Code != "auth-missing" {
		t.Fatalf("auth = %v, want auth-missing", ref)
	}
}

// TestBitbucketMultiLineFlattens proves a range anchors at its end line
// and is counted, so the flatten is visible instead of silent.
func TestBitbucketMultiLineFlattens(t *testing.T) {
	t.Parallel()
	dir := bitbucketPostDir(t)
	cmdr := bitbucketCommander(t, bitbucketAnswer("201 Created", bitbucketCreated(501)))
	comment := `{"path":"notes.txt","body":"a","line":2,"start_line":1,"side":"RIGHT"},` +
		`{"path":"notes.txt","body":"b","line":3,"start_line":1,"side":"RIGHT"}`
	attempt := bitbucketPost(t, cmdr, dir, bitbucketPayload(eventComment, comment))
	if !attempt.ok {
		t.Fatalf("post refused: %s", attempt.message)
	}
	want := "bitbucket anchors are single-line — 2 multi-line anchor(s) anchored at their end line"
	if len(attempt.warnings) != 1 || attempt.warnings[0] != want {
		t.Errorf("warnings = %q, want %q", attempt.warnings, want)
	}
	first := bitbucketWriteDoc(t, cmdr, 0)
	assertBitbucketInline(t, first, "to", 2)
	second := bitbucketWriteDoc(t, cmdr, 1)
	assertBitbucketInline(t, second, "to", 3)
}

// TestBitbucketCommentPagination proves the comment list walks every page:
// page one carries a same-origin next URL whose page holds a second open
// thread, and a next URL off origin refuses instead of following it.
func TestBitbucketCommentPagination(t *testing.T) {
	t.Parallel()
	dir := bitbucketPostDir(t)
	next := bitbucketAPI + "/comments?pagelen=100&page=2"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		bitbucketListRoute: {stdout: `{"values":[` +
			`{"id":1,"content":{"raw":"open one"},"inline":{"path":"a.go","to":4}}` +
			`],"next":"` + next + `"}`},
		bitbucketFollowPrefix + next: {stdout: `{"values":[` +
			`{"id":2,"content":{"raw":"open two"},"inline":{"path":"a.go","to":5}}` +
			`],"next":null}`},
	}}
	host := bitbucketHost{}
	open, _, err := host.recheck(context.Background(), cmdr, bitbucketEnv(), "acme/app", "7", dir)
	if err != nil {
		t.Fatalf("recheck: %v", err)
	}
	if open != 2 {
		t.Errorf("open threads = %d, want one per page", open)
	}
	if got := countCalls(cmdr, bitbucketFollowPrefix+next); got != 1 {
		t.Errorf("second page reads = %d, want 1: %q", got, callKeys(cmdr))
	}

	off := &scriptCommander{t: t, out: map[string]scriptResult{
		bitbucketListRoute: {stdout: `{"values":[` +
			`{"id":1,"content":{"raw":"open one"},"inline":{"path":"a.go","to":4}}` +
			`],"next":"https://evil.invalid/comments"}`},
	}}
	_, _, offErr := host.recheck(context.Background(), off, bitbucketEnv(), "acme/app", "7", dir)
	if offErr == nil || !strings.Contains(offErr.Error(), "off origin") {
		t.Errorf("off-origin next error = %v, want the off-origin refusal", offErr)
	}
	if got := countCalls(off, bitbucketFollowPrefix+"https://evil.invalid/comments"); got != 0 {
		t.Errorf("off-origin page reads = %d, want 0", got)
	}
}

// TestBitbucketReads proves the drift read, the token-user read, and the
// open-thread recheck.
func TestBitbucketReads(t *testing.T) {
	t.Parallel()
	dir := bitbucketPostDir(t)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		bitbucketPRRoute:   {stdout: bitbucketPRJSON(bitbucketHead)},
		bitbucketUserRoute: {stdout: `{"nickname":"rev"}`},
		bitbucketListRoute: {stdout: `{"values":[` +
			`{"id":1,"content":{"raw":"open"},"inline":{"path":"a.go","to":4}},` +
			`{"id":2,"content":{"raw":"done"},"inline":{"path":"a.go","to":5},"resolution":{"user":{"nickname":"rev"}}},` +
			`{"id":3,"parent":{"id":1},"content":{"raw":"reply"},"inline":{"path":"a.go","to":4}},` +
			`{"id":4,"content":{"raw":"summary"},"resolution":null}` +
			`],"next":null}`},
	}}
	host := bitbucketHost{}
	sha, err := host.liveSHA(context.Background(), cmdr, bitbucketEnv(), "acme/app", "7")
	if err != nil || sha != bitbucketHead {
		t.Errorf("liveSHA = %q, %v; want the fixture head", sha, err)
	}
	me, err := host.selfLogin(context.Background(), cmdr, bitbucketEnv())
	if err != nil || me != "rev" {
		t.Errorf("selfLogin = %q, %v; want rev", me, err)
	}
	open, reason, err := host.recheck(context.Background(), cmdr, bitbucketEnv(), "acme/app", "7", dir)
	if err != nil || reason != "" {
		t.Fatalf("recheck = %d, %q, %v", open, reason, err)
	}
	if open != 1 {
		t.Errorf("open threads = %d, want the one unresolved root", open)
	}
}

// TestBitbucketUnreadableHunkIndex proves an unreadable bundle hunk
// index refuses before any write, rather than withholding every LEFT
// anchor as a deleted file.
func TestBitbucketUnreadableHunkIndex(t *testing.T) {
	t.Parallel()
	dir := bitbucketPostDir(t)
	if err := os.Remove(dir + "/hunks.tsv"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir+"/hunks.tsv", 0o755); err != nil {
		t.Fatal(err)
	}
	cmdr := bitbucketCommander(t, bitbucketAnswer("201 Created", bitbucketCreated(10)))
	comment := `{"path":"notes.txt","body":"x","line":2,"side":"LEFT"}`
	attempt := bitbucketPost(t, cmdr, dir, bitbucketPayload(eventComment, comment))
	if attempt.ok {
		t.Fatal("post landed with an unreadable hunk index")
	}
	want := "post failed: bitbucket could not read the bundle hunk index — nothing was written"
	if attempt.message != want {
		t.Errorf("message = %q, want %q", attempt.message, want)
	}
	if got := countCalls(cmdr, bitbucketWriteRoute); got != 0 {
		t.Errorf("comment writes = %d, want 0: %q", got, callKeys(cmdr))
	}
}

// TestBitbucketHostSeam proves the provider switch returns the curl
// writer and a reader that gates the environment before any call.
func TestBitbucketHostSeam(t *testing.T) {
	t.Parallel()
	writer, reader, _ := hostFor(providerBitbucket)
	if got := writer.tool(); got != "curl" {
		t.Errorf("bitbucket tool = %q, want curl", got)
	}
	if ref := reader.auth(bitbucketEnv()); ref != nil {
		t.Errorf("auth refused a complete environment: %v", ref)
	}
	ref := reader.auth(map[string]string{})
	if ref == nil || ref.Code != "auth-missing" {
		t.Fatalf("auth = %v, want auth-missing", ref)
	}
	if want := "bitbucket needs BITBUCKET_TOKEN (Bearer) or BITBUCKET_USERNAME+BITBUCKET_PASSWORD"; ref.Message != want {
		t.Errorf("auth message = %q, want %q", ref.Message, want)
	}
}

// TestBitbucketSummaryCRUD proves the summary seam: list with a marker
// match, create, and update.
func TestBitbucketSummaryCRUD(t *testing.T) {
	t.Parallel()
	host := bitbucketHost{}
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		bitbucketListRoute: {stdout: `{"values":[` +
			`{"id":771,"content":{"raw":"old\n\n<!-- verdict-summary head=abc -->"},"user":{"nickname":"rev"},` +
			`"created_on":"2026-11-02T12:00:00Z","links":{"html":{"href":"https://bitbucket.org/acme/app/pullrequests/7/comments/771"}}}` +
			`],"next":null}`},
		bitbucketWriteRoute:  {stdout: bitbucketAnswer("201 Created", bitbucketCreated(772))},
		bitbucketUpdateRoute: {stdout: bitbucketAnswer("200 OK", bitbucketCreated(771))},
	}}
	assertBitbucketSummaryList(t, host, cmdr)
	assertBitbucketSummaryWrite(t, host, cmdr)
	if got := countCalls(cmdr, bitbucketUpdateRoute); got != 1 {
		t.Errorf("update writes = %d, want 1: %q", got, callKeys(cmdr))
	}
	assertBitbucketEmptySummaryRefuses(t)
}

// assertBitbucketSummaryList proves the list reads the marked summary.
func assertBitbucketSummaryList(t *testing.T, host bitbucketHost, cmdr *scriptCommander) {
	t.Helper()
	listed, err := host.listSummaryComments(context.Background(), cmdr, bitbucketEnv(), "acme/app", "7")
	if err != nil || len(listed) != 1 || listed[0].ID != "771" || listed[0].Author != "rev" {
		t.Fatalf("listed = %+v, %v; want the one marked summary", listed, err)
	}
	if listed[0].URL != "https://bitbucket.org/acme/app/pullrequests/7/comments/771" {
		t.Errorf("summary URL = %q", listed[0].URL)
	}
}

// assertBitbucketSummaryWrite proves create and update round-trip their ids.
func assertBitbucketSummaryWrite(t *testing.T, host bitbucketHost, cmdr *scriptCommander) {
	t.Helper()
	created, err := host.createSummaryComment(context.Background(), cmdr, bitbucketEnv(), "acme/app", "7", "hello")
	if err != nil || created.ID != "772" {
		t.Fatalf("created = %+v, %v; want comment 772", created, err)
	}
	updated, err := host.updateSummaryComment(context.Background(), cmdr, bitbucketEnv(), "acme/app", "7", "771", "world")
	if err != nil || updated.ID != "771" {
		t.Fatalf("updated = %+v, %v; want comment 771", updated, err)
	}
}

// assertBitbucketEmptySummaryRefuses proves an empty body refuses before
// any call.
func assertBitbucketEmptySummaryRefuses(t *testing.T) {
	t.Helper()
	clean := &scriptCommander{t: t, out: map[string]scriptResult{}}
	host := bitbucketHost{}
	if _, serr := host.createSummaryComment(context.Background(), clean, bitbucketEnv(), "acme/app", "7", "   "); serr == nil {
		t.Error("create accepted a whitespace body")
	}
	if len(clean.calls) != 0 {
		t.Errorf("empty body fired %d call(s)", len(clean.calls))
	}
}

// TestBitbucketPublicationSplits proves the duplicate read: a marked
// general comment becomes the review, an inline anchor becomes the
// remote comment, and an outdated anchor stays unpaired.
func TestBitbucketPublicationSplits(t *testing.T) {
	t.Parallel()
	marked := "> ✅ Looks good\n\n<!-- verdict-review payload=" + st0Hash + " head=" + bitbucketHead + " -->"
	items := []map[string]any{
		{"id": float64(1), "content": map[string]any{"raw": marked}, "user": map[string]any{"nickname": "rev"}, "created_on": "2026-11-02T12:00:00Z"},
		{"id": float64(2), "content": map[string]any{"raw": "finding"}, "inline": map[string]any{"path": "a.go", "to": float64(4)}, "user": map[string]any{"nickname": "rev"}},
		{"id": float64(3), "content": map[string]any{"raw": "stale finding"}, "inline": map[string]any{"path": "a.go", "to": float64(9), "outdated": true}, "user": map[string]any{"nickname": "rev"}},
		{"id": float64(4), "content": map[string]any{"raw": "draft"}, "pending": true},
	}
	remote := bitbucketPublication(items, bitbucketHead)
	if len(remote.reviews) != 1 || remote.reviews[0].commitID != bitbucketHead {
		t.Errorf("reviews = %+v, want the one marked review on the head", remote.reviews)
	}
	if len(remote.comments) != 1 {
		t.Fatalf("comments = %d, want one (outdated and pending drop)", len(remote.comments))
	}
	paired := missingComments(bitbucketHead, []any{identifyPayloadComment(t, "a.go", 4, "RIGHT", "finding")}, []commentIdentity{
		identifyComment(bitbucketHead, remoteCommentObj(remote.comments[0])),
	})
	if len(paired) != 0 {
		t.Errorf("a live anchor read as missing: %d", len(paired))
	}
	_ = marked
}

// st0Hash is a 40-hex placeholder the publication split test keys on.
const st0Hash = "0123456789abcdef0123456789abcdef01234567"

// identifyPayloadComment builds the verdict-side comment object a repost
// would carry.
func identifyPayloadComment(t *testing.T, path string, line int, side, body string) *jq.Obj {
	t.Helper()
	return jq.NewObj(fieldPath, path, fieldLine, line, fieldSide, side, fieldBody, body)
}

// remoteCommentObj rebuilds the remote-comment object from its pieces.
func remoteCommentObj(m map[string]any) *jq.Obj {
	_, obj := remoteComment(m)
	return obj
}
