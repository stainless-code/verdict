package postcore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/azure"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	azureHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	azureOrg  = "https://dev.azure.com/acme"
	azureBase = azureOrg + "/acme/_apis/git/repositories/app/pullRequests/7"

	// The exact argv an Azure post fires. Every one is a literal: a rename in
	// the submit path fails here instead of following the helper that built
	// it. Reads and writes both state the Azure DevOps resource a token is
	// bought for; writes stage the body in a private 0600 file, so its path is
	// the BODY placeholder rather than a random temp name.
	azureReadPrefix    = "az\x00rest\x00--method\x00get\x00--resource\x00499b84ac-1321-427f-aa17-267ca6975798\x00--url\x00"
	azurePullRoute     = azureReadPrefix + azureBase + "?api-version=7.1"
	azureThreadsRoute  = azureReadPrefix + azureBase + "/threads?api-version=7.1"
	azureIdentityRoute = azureReadPrefix + azureOrg + "/_apis/connectionData?api-version=7.1-preview.1"
	azureWritePrefix   = "az\x00rest\x00--method\x00post\x00--resource\x00499b84ac-1321-427f-aa17-267ca6975798\x00--url\x00" + azureBase + "/threads?api-version=7.1" +
		"\x00--headers\x00Content-Type=application/json\x00--body\x00@" + bodyPlaceholder
	azurePatchPrefix = "az\x00rest\x00--method\x00patch\x00--resource\x00499b84ac-1321-427f-aa17-267ca6975798\x00--url\x00" + azureBase + "/threads/501/comments/1?api-version=7.1" +
		"\x00--headers\x00Content-Type=application/json\x00--body\x00@" + bodyPlaceholder
)

// azureTestDiff is the reviewed change: one modified file with both sides
// indexed, one deleted file with an OLD row alone, and one added file with a
// NEW row alone.
const azureTestDiff = "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n" +
	" top\n-old line\n+new line\n bottom\n" +
	"diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\n--- a/gone.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n" +
	"diff --git a/added.txt b/added.txt\nnew file mode 100644\n--- /dev/null\n+++ b/added.txt\n@@ -0,0 +1,2 @@\n+x\n+y\n"

// azureEnv is the environment an Azure post resolves its organization from.
func azureEnv() map[string]string {
	return map[string]string{azure.EnvOrg: azureOrg}
}

// azurePostDir writes the bundle files an Azure post reads and returns the
// directory: the provider marker, the reviewed diff, and its hunk index.
func azurePostDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"PR.json":   `{"provider":"azure","number":7,"state":"open","head_sha":"` + azureHead + `","author":"mate"}`,
		"full.diff": azureTestDiff,
		"hunks.tsv": "added.txt\tNEW\t1\t2\ngone.txt\tOLD\t1\t2\nnotes.txt\tOLD\t1\t3\nnotes.txt\tNEW\t1\t3\n",
	}
	for name, body := range files {
		if err := writeTestFile(dir+"/"+name, body); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// azurePayload renders one hosted payload with the given event and comments.
func azurePayload(event, comments string) string {
	return `{"body":"> ✅ Looks good","event":"` + event + `","commit_id":"` + azureHead + `","comments":[` + comments + `]}`
}

// azurePRJSON is the pull read the head comes from.
func azurePRJSON(head string) string {
	return `{"pullRequestId":7,"lastMergeSourceCommit":{"commitId":"` + head + `"}}`
}

// azureCreatedAnswer renders one created thread in Azure's own shape.
func azureCreatedAnswer(id int) string {
	return `{"id":` + intStr(id) + `,"publishedDate":"2026-11-02T12:00:00Z",` +
		`"lastUpdatedDate":"2026-11-02T12:00:00Z","status":"active",` +
		`"comments":[{"id":1,"parentCommentId":0,"content":"x","commentType":"text",` +
		`"author":{"id":"guid","displayName":"rev","uniqueName":"rev"}}]}`
}

// azureCommander scripts the reads and the writes an Azure post fires. The
// anchored and summary writes share one argv, so they share one answer.
func azureCommander(t *testing.T) *scriptCommander {
	t.Helper()
	return &scriptCommander{t: t, out: map[string]scriptResult{
		azurePullRoute:     {stdout: azurePRJSON(azureHead)},
		azureIdentityRoute: {stdout: `{"authenticatedUser":{"id":"guid","providerDisplayName":"rev"}}`},
		azureThreadsRoute:  {stdout: `{"count":0,"value":[]}`},
		azureWritePrefix:   {stdout: azureCreatedAnswer(501)},
	}}
}

// azurePost runs one payload through the Azure writer.
func azurePost(t *testing.T, cmdr Commander, dir, payload string) postAttempt {
	t.Helper()
	return azureHost{}.post(context.Background(), cmdr, azureEnv(),
		core.CapabilitiesFor(providerAzure), "acme/app", "7", dir, payload)
}

// azureWriteDocs decodes every thread write's request body.
func azureWriteDocs(t *testing.T, cmdr *scriptCommander) []map[string]any {
	t.Helper()
	var out []map[string]any
	for i, call := range cmdr.calls {
		if routeKey(call) != azureWritePrefix {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(cmdr.bodies[i]), &doc); err != nil {
			t.Fatalf("thread write body is not one JSON object: %v\n%s", err, cmdr.bodies[i])
		}
		out = append(out, doc)
	}
	return out
}

// TestAzureInlineThread proves one finding becomes one anchored thread with
// the right-side position Azure requires, with the created page on the link
// list.
func TestAzureInlineThread(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := azureCommander(t)
	comment := `{"path":"notes.txt","body":"Consider this","line":2,"side":"RIGHT"}`
	attempt := azurePost(t, cmdr, dir, azurePayload(eventComment, comment))
	if !attempt.ok {
		t.Fatalf("post refused: %q", attempt.errText)
	}
	if len(attempt.warnings) != 0 {
		t.Errorf("warnings = %q, want none", attempt.warnings)
	}
	docs := azureWriteDocs(t, cmdr)
	if len(docs) != 2 {
		t.Fatalf("thread writes = %d, want the finding and the body", len(docs))
	}
	assertAzureAnchor(t, docs[0], "notes.txt", "right", 2)
	if got := azureThreadContent(docs[0]); got != "Consider this" {
		t.Errorf("anchored comment content = %q, want the finding body", got)
	}
	if len(attempt.inlineLinks) != 1 || attempt.inlineLinks[0].Path != "notes.txt" ||
		!strings.Contains(attempt.inlineLinks[0].URL, "discussionId=501") {
		t.Errorf("inline links = %+v, want the created thread's page", attempt.inlineLinks)
	}
	assertAzureRoutes(t, cmdr)
}

// TestAzureSummaryThread proves the body becomes one general thread — no
// threadContext, so Azure reads it as a pull-request comment — and that the
// receipt names that thread.
//
//nolint:revive // the receipt matrix reads as one block
func TestAzureSummaryThread(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := azureCommander(t)
	comment := `{"path":"notes.txt","body":"Consider this","line":2,"side":"RIGHT"}`
	attempt := azurePost(t, cmdr, dir, azurePayload(eventComment, comment))
	if !attempt.ok {
		t.Fatalf("post refused: %q", attempt.errText)
	}
	docs := azureWriteDocs(t, cmdr)
	if len(docs) != 2 {
		t.Fatalf("thread writes = %d, want the finding and the body", len(docs))
	}
	if _, general := docs[1]["threadContext"]; general {
		t.Error("the body write carried a threadContext, so Azure reads it inline")
	}
	if got := azureThreadContent(docs[1]); got != "> ✅ Looks good" {
		t.Errorf("body thread content = %q, want the review body", got)
	}
	receipt := decodeAzureReceipt(t, attempt.posted)
	for _, field := range []struct{ key, want string }{
		{"state", "commented"},
		{"id", "501"},
		{"submitted_at", "2026-11-02T12:00:00Z"},
	} {
		if got := jq.StrOf(receipt[field.key]); got != field.want {
			t.Errorf("receipt %s = %q, want %q", field.key, got, field.want)
		}
	}
	if got := jq.StrOf(receipt["html_url"]); !strings.HasSuffix(got, "pullrequest/7?discussionId=501") {
		t.Errorf("receipt html_url = %q, want the PR page with the discussion id", got)
	}
}

// azureThreadContent reads the opening comment's content of one thread body.
func azureThreadContent(doc map[string]any) string {
	comments := jq.Elements(doc[fieldComments])
	if len(comments) == 0 {
		return ""
	}
	return jq.StrOf(conformanceMap(comments[0])["content"])
}

// assertAzureRoutes proves every call the run made is one the pinned route set
// names, so a stray host call cannot hide behind a passing count.
func assertAzureRoutes(t *testing.T, cmdr *scriptCommander) {
	t.Helper()
	for i, call := range cmdr.calls {
		switch routeKey(call) {
		case azureWritePrefix, azurePullRoute, azureThreadsRoute, azureIdentityRoute:
		default:
			t.Errorf("call %d argv = %q, want a pinned route", i, call)
		}
	}
}

// assertAzureAnchor checks one thread body's threadContext.
//
//nolint:revive // the anchor matrix reads as one assertion block
func assertAzureAnchor(t *testing.T, doc map[string]any, path, side string, line int) {
	t.Helper()
	ctx, ok := doc["threadContext"].(map[string]any)
	if !ok {
		t.Fatalf("thread body carries no threadContext: %v", doc)
	}
	if got, want := jq.StrOf(ctx["filePath"]), "/"+path; got != want {
		t.Errorf("filePath = %q, want %q (Azure requires the leading slash)", got, want)
	}
	start, ok := ctx[side+"FileStart"].(map[string]any)
	if !ok {
		t.Fatalf("threadContext carries no %sFileStart: %v", side, ctx)
	}
	if got, want := conformanceInt(start["line"]), line; got != want {
		t.Errorf("%sFileStart.line = %d, want %d", side, got, want)
	}
	if got, want := conformanceInt(start["offset"]), azurePositionOffset; got != want {
		t.Errorf("%sFileStart.offset = %d, want %d", side, got, want)
	}
	end, ok := ctx[side+"FileEnd"].(map[string]any)
	if !ok {
		t.Fatalf("threadContext carries no %sFileEnd: %v", side, ctx)
	}
	if got, want := conformanceInt(end["line"]), line; got != want {
		t.Errorf("%sFileEnd.line = %d, want %d", side, got, want)
	}
	if _, other := ctx[otherAzureSide(side)+"FileStart"]; other {
		t.Errorf("threadContext carries both sides: %v", ctx)
	}
}

// otherAzureSide names the side an anchor must not carry.
func otherAzureSide(side string) string {
	if side == "right" {
		return "left"
	}
	return "right"
}

// decodeAzureReceipt decodes one accepted review object.
func decodeAzureReceipt(t *testing.T, posted string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(posted), &doc); err != nil {
		t.Fatalf("receipt is not one JSON object: %v\n%s", err, posted)
	}
	return doc
}

// TestAzureLeftAnchor proves a deleted file's old side anchors through the
// leftFile pair, which is the only place Azure says where an old line lives.
func TestAzureLeftAnchor(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := azureCommander(t)
	comment := `{"path":"gone.txt","body":"Why remove this?","line":2,"side":"LEFT"}`
	attempt := azurePost(t, cmdr, dir, azurePayload(eventComment, comment))
	if !attempt.ok {
		t.Fatalf("post refused: %q", attempt.errText)
	}
	docs := azureWriteDocs(t, cmdr)
	if len(docs) != 2 {
		t.Fatalf("thread writes = %d, want the finding and the body", len(docs))
	}
	assertAzureAnchor(t, docs[0], "gone.txt", "left", 2)
}

// TestAzureWithheldAndFlattened proves the three anchor losses: a finding
// with no numeric line is withheld, an anchor whose side the change does not
// have on that file is withheld, and a multi-line range lands on its end
// line. Every loss is counted in a warning, and nothing is mis-anchored.
func TestAzureWithheldAndFlattened(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := azureCommander(t)
	comments := `{"path":"notes.txt","body":"no line","line":0,"side":"RIGHT"},` +
		`{"path":"gone.txt","body":"wrong side","line":2,"side":"RIGHT"},` +
		`{"path":"notes.txt","body":"range","line":3,"start_line":1,"side":"RIGHT"}`
	attempt := azurePost(t, cmdr, dir, azurePayload(eventComment, comments))
	if !attempt.ok {
		t.Fatalf("post refused: %q", attempt.errText)
	}
	docs := azureWriteDocs(t, cmdr)
	if len(docs) != 2 {
		t.Fatalf("thread writes = %d, want the one anchor plus the body", len(docs))
	}
	assertAzureAnchor(t, docs[0], "notes.txt", "right", 3)
	if got := jq.StrOf(conformanceMap(jq.Elements(docs[0]["comments"])[0])["content"]); got != "range" {
		t.Errorf("anchored finding = %q, want the flattened range", got)
	}
	sent := strings.Join(attempt.warnings, "\n")
	for _, want := range []string{
		"azure anchors need a path, a body, and a numeric line — 1 finding(s) withheld",
		"azure cannot anchor a comment on the side the change does not have for that file — 1 finding(s) withheld",
		"azure anchors are single-line — 1 multi-line anchor(s) anchored at their end line",
	} {
		if !strings.Contains(sent, want) {
			t.Errorf("warnings %q miss %q", sent, want)
		}
	}
}

// TestAzureBodyStagingRefuses proves a body that cannot be staged refuses
// locally, before any call: the review may not exist, so reporting a lost
// write would send the operator looking for one.
func TestAzureBodyStagingRefuses(t *testing.T) {
	dir := azurePostDir(t)
	blocked := dir + "/not-a-dir"
	if err := writeTestFile(blocked, "file"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", blocked)
	cmdr := azureCommander(t)
	comment := `{"path":"notes.txt","body":"x","line":2,"side":"RIGHT"}`
	attempt := azurePost(t, cmdr, dir, azurePayload(eventComment, comment))
	if attempt.ok {
		t.Fatal("post landed with an unusable body directory")
	}
	want := "post failed: azure could not stage the request body file — nothing was written"
	if attempt.message != want {
		t.Errorf("message = %q, want %q", attempt.message, want)
	}
	if got := countCalls(cmdr, azureWritePrefix); got != 0 {
		t.Errorf("thread writes = %d, want none: %q", got, callKeys(cmdr))
	}
}

// TestAzureAuthFailureBeforeWrite proves a 401 on the head read refuses with
// the host's own words and no write: the review cannot exist, so the operator
// is never sent looking for one.
func TestAzureAuthFailureBeforeWrite(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := azureCommander(t)
	cmdr.out[azurePullRoute] = scriptResult{
		stderr: "ERROR: Please run 'az login' to setup account.", fail: true,
	}
	comment := `{"path":"notes.txt","body":"x","line":2,"side":"RIGHT"}`
	attempt := azurePost(t, cmdr, dir, azurePayload(eventComment, comment))
	if attempt.ok {
		t.Fatal("a 401 read was accepted")
	}
	want := "post failed: azure could not read the pull request head — nothing was written: " +
		"ERROR: Please run 'az login' to setup account."
	if attempt.errText != want {
		t.Errorf("reason = %q, want %q", attempt.errText, want)
	}
	if attempt.partial {
		t.Error("a pre-write failure read as partial")
	}
	if got := countCalls(cmdr, azureWritePrefix); got != 0 {
		t.Errorf("thread writes = %d, want none after a failed head read", got)
	}
}

// TestAzureMidwayFailureIsPartial proves a write that fails after an earlier
// thread landed reports partial with the count: a replay is a known duplicate
// instead of a surprise, and the host's own reason is the one recorded.
func TestAzureMidwayFailureIsPartial(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := &azureFailCommander{
		scriptCommander: azureCommander(t), failAt: 2,
		stderr: "ERROR: TF401019: The Git repository does not exist",
	}
	comments := `{"path":"notes.txt","body":"one","line":2,"side":"RIGHT"},` +
		`{"path":"notes.txt","body":"two","line":3,"side":"RIGHT"}`
	attempt := azurePost(t, cmdr, dir, azurePayload(eventComment, comments))
	if attempt.ok || !attempt.partial {
		t.Fatalf("attempt = %+v, want a partial failure", attempt)
	}
	if want := "ERROR: TF401019: The Git repository does not exist"; attempt.errText != want {
		t.Errorf("reason = %q, want the host's own line %q", attempt.errText, want)
	}
	if stop := "azure post stopped after 1 of 2 anchored thread(s) — a re-run reposts them"; !slices.Contains(attempt.warnings, stop) {
		t.Errorf("warnings = %q, want %q", attempt.warnings, stop)
	}
	if got := countCalls(cmdr.scriptCommander, azureWritePrefix); got != 2 {
		t.Errorf("thread writes = %d, want the landed one plus the failed one", got)
	}
	if raw, err := os.ReadFile(dir + postErrSuffix); err != nil || string(raw) != attempt.errText {
		t.Errorf("sidecar = %q, %v; want the reason", raw, err)
	}
}

// TestAzureLostAnswerIsPartial proves a write that never answered reports the
// may-exist warning once an earlier thread landed: `az` died, so the review
// may be half-written and the operator checks the PR before re-running.
func TestAzureLostAnswerIsPartial(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := &azureFailCommander{scriptCommander: azureCommander(t), failAt: 2, spawn: true}
	comments := `{"path":"notes.txt","body":"one","line":2,"side":"RIGHT"},` +
		`{"path":"notes.txt","body":"two","line":3,"side":"RIGHT"}`
	attempt := azurePost(t, cmdr, dir, azurePayload(eventComment, comments))
	if attempt.ok || !attempt.partial {
		t.Fatalf("attempt = %+v, want a partial lost answer", attempt)
	}
	want := "azure did not answer — the review may exist; check the PR before re-running"
	if attempt.errText != want {
		t.Errorf("reason = %q, want %q", attempt.errText, want)
	}
	if stop := "azure post stopped after 1 of 2 anchored thread(s) — a re-run reposts them"; !slices.Contains(attempt.warnings, stop) {
		t.Errorf("warnings = %q, want %q", attempt.warnings, stop)
	}
}

// azureFailCommander scripts the nth `az rest` write: the reads come from the
// shared script, and the write at `failAt` answers with the given stderr or —
// with spawn — with a spawn error, which is the path where `az` never
// answered at all.
type azureFailCommander struct {
	*scriptCommander
	stderr string
	failAt int // 1-based write ordinal; 0 fails nothing
	writes int
	spawn  bool
}

func (c *azureFailCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if routeKey(cmd.Argv) != azureWritePrefix {
		return c.scriptCommander.Run(ctx, cmd)
	}
	c.writes++
	if c.writes != c.failAt {
		return c.scriptCommander.Run(ctx, cmd)
	}
	c.calls = append(c.calls, append([]string{}, cmd.Argv...))
	if c.spawn {
		return core.Result{}, errors.New(`exec: "az": executable file not found in $PATH`)
	}
	return core.Result{ExitCode: 1, Stderr: c.stderr}, nil
}

// TestAzureBodyFileIsPrivate proves the staged request body has mode 0600
// while the write runs and is gone afterwards, and that no credential rides
// an argv: the review text is readable only by its owner, and no path outlives
// the call.
func TestAzureBodyFileIsPrivate(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	const secret = "pat-topsecret-value"
	watcher := &azureBodyWatcher{scriptCommander: azureCommander(t)}
	env := azureEnv()
	env["AZURE_DEVOPS_EXT_PAT"] = secret
	comment := `{"path":"notes.txt","body":"x","line":2,"side":"RIGHT"}`
	attempt := azureHost{}.post(context.Background(), watcher, env,
		core.CapabilitiesFor(providerAzure), "acme/app", "7", dir,
		azurePayload(eventComment, comment))
	if !attempt.ok {
		t.Fatalf("post refused: %q", attempt.errText)
	}
	assertAzureBodiesPrivate(t, watcher)
	assertAzureArgvClean(t, watcher.calls, secret)
}

// assertAzureBodiesPrivate checks every staged body was 0600 while its write
// ran and is gone afterwards.
func assertAzureBodiesPrivate(t *testing.T, watcher *azureBodyWatcher) {
	t.Helper()
	if len(watcher.paths) == 0 {
		t.Fatal("no write staged a body file")
	}
	for i, path := range watcher.paths {
		if runtime.GOOS != "windows" && watcher.modes[i].Perm() != azureBodyFileMode {
			t.Errorf("staged body %s mode = %v, want %v", path, watcher.modes[i].Perm(), azureBodyFileMode)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("staged body %s survived the call: %v", path, err)
		}
	}
}

// assertAzureArgvClean checks no argv verdict fired carries the secret an
// operator exported for the fetch path.
func assertAzureArgvClean(t *testing.T, calls [][]string, secret string) {
	t.Helper()
	for _, call := range calls {
		if strings.Contains(strings.Join(call, " "), secret) {
			t.Errorf("argv carries the credential: %q", call)
		}
	}
}

// azureBodyWatcher records every staged body file's mode while its write runs.
type azureBodyWatcher struct {
	*scriptCommander
	paths []string
	modes []os.FileMode
}

//nolint:revive // one scripted exchange keeps the staged-file observation and the delegated answer together
func (c *azureBodyWatcher) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	key := routeKey(cmd.Argv)
	if key == azureWritePrefix || key == azurePatchPrefix {
		for i := 0; i+1 < len(cmd.Argv); i++ {
			if cmd.Argv[i] != azFlagBody || !strings.HasPrefix(cmd.Argv[i+1], azBodyFilePrefix) {
				continue
			}
			path := strings.TrimPrefix(cmd.Argv[i+1], azBodyFilePrefix)
			info, err := os.Stat(path)
			if err != nil {
				c.t.Errorf("stat staged body: %v", err)
				break
			}
			c.paths = append(c.paths, path)
			c.modes = append(c.modes, info.Mode())
		}
	}
	return c.scriptCommander.Run(ctx, cmd)
}

// TestAzureStaleHeadRefuses proves the head guard: a thread carries no
// commit pin, so a pull that moved since fetch refuses the whole post before
// any write.
func TestAzureStaleHeadRefuses(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := azureCommander(t)
	cmdr.out[azurePullRoute] = scriptResult{stdout: azurePRJSON(strings.Repeat("b", 40))}
	comment := `{"path":"notes.txt","body":"x","line":2,"side":"RIGHT"}`
	attempt := azurePost(t, cmdr, dir, azurePayload(eventComment, comment))
	if attempt.ok {
		t.Fatal("a moved head was accepted")
	}
	if !strings.Contains(attempt.message, "head moved since fetch") ||
		!strings.Contains(attempt.message, "no commit pin") {
		t.Errorf("message = %q, want the stale-head refusal", attempt.message)
	}
	if got := countCalls(cmdr, azureWritePrefix); got != 0 {
		t.Errorf("thread writes = %d, want none before the head guard", got)
	}
}

// TestAzureNoCommitRefuses proves a payload without the reviewed head is
// refused before the read: Azure would anchor on whatever head is live.
func TestAzureNoCommitRefuses(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := azureCommander(t)
	payload := `{"body":"> ok","event":"COMMENT","comments":[]}`
	attempt := azurePost(t, cmdr, dir, payload)
	if attempt.ok {
		t.Fatal("a payload with no commit_id was accepted")
	}
	if want := "post failed: the payload carries no commit_id — azure would anchor on the current head " +
		"instead of the reviewed one; re-fetch to record the head"; attempt.message != want {
		t.Errorf("message = %q, want %q", attempt.message, want)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none before the commit guard", callKeys(cmdr))
	}
}

// TestAzureUnknownEventRefuses proves a payload whose event Azure has no
// write for is refused before any call instead of posting silently.
func TestAzureUnknownEventRefuses(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := azureCommander(t)
	attempt := azurePost(t, cmdr, dir, azurePayload("MERGE", ""))
	if attempt.ok {
		t.Fatal("an unknown event was accepted")
	}
	if !strings.Contains(attempt.message, `azure has no review event named "MERGE"`) {
		t.Errorf("message = %q, want the event refusal", attempt.message)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none before the event guard", callKeys(cmdr))
	}
}

// azureTargetCase is one target-gate refusal: the environment and position the
// gate reads, and the code and phrase the refusal must carry.
type azureTargetCase struct {
	name, org, repo, pr, code, contains string
}

// TestAzureTargetRefusals proves the organization and target gates refuse
// typed before any call: no organization, a non-https, credential-bearing, or
// off-allowlist URL, a traversing repo, and a non-numeric pull id.
func TestAzureTargetRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range azureTargetCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertAzureTargetRefusal(t, &tc)
		})
	}
}

// azureTargetCases are the refusals the two gates owe the operator.
func azureTargetCases() []azureTargetCase {
	return []azureTargetCase{
		{"missing org", "", "acme/app", "7", azure.CodeAuthMissing, "AZURE_DEVOPS_ORG"},
		{"http org", "http://dev.azure.com/acme", "acme/app", "7", azure.CodeBadTarget, "https"},
		{"credential org", "https://user:topsecret@dev.azure.com/acme", "acme/app", "7", azure.CodeBadTarget, "az login"},
		{"off host org", "https://evil.example/acme", "acme/app", "7", azure.CodeBadTarget, "dev.azure.com"},
		{"traversal repo", azureOrg, "../..", "7", azure.CodeBadTarget, "../.."},
		{"nested repo", azureOrg, "acme/app/extra", "7", azure.CodeBadTarget, "project/repository"},
		{"non numeric pr", azureOrg, "acme/app", "seven", azure.CodeBadTarget, "numeric pull-request id"},
		{"traversing pr", azureOrg, "acme/app", "7/../../user", azure.CodeBadTarget, "numeric pull-request id"},
	}
}

// assertAzureTargetRefusal drives one refusal through the shared gate and the
// writer: the code is the gate's, the sentence the writer's, and no call fires.
func assertAzureTargetRefusal(t *testing.T, tc *azureTargetCase) {
	t.Helper()
	dir := azurePostDir(t)
	cmdr := azureCommander(t)
	env := map[string]string{azure.EnvOrg: tc.org}
	if _, ref := azure.Resolve(env, tc.repo, tc.pr); ref == nil || ref.Code != tc.code {
		t.Fatalf("Resolve(%q, %q) code = %+v, want %s", tc.repo, tc.pr, ref, tc.code)
	}
	attempt := azureHost{}.post(context.Background(), cmdr, env,
		core.CapabilitiesFor(providerAzure), tc.repo, tc.pr, dir,
		azurePayload(eventComment, ""))
	if attempt.ok || attempt.message == "" {
		t.Fatalf("post = %+v, want a local refusal", attempt)
	}
	assertAzureRefusal(t, attempt.message, tc.contains)
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none before the gate", callKeys(cmdr))
	}
}

// assertAzureRefusal checks one local refusal names the gap and echoes no
// value it rejected.
func assertAzureRefusal(t *testing.T, message, contains string) {
	t.Helper()
	if !strings.Contains(message, contains) {
		t.Errorf("message = %q, want %q", message, contains)
	}
	for _, echoed := range []string{"topsecret", "evil.example"} {
		if strings.Contains(message, echoed) {
			t.Errorf("refusal echoes the rejected value %q: %q", echoed, message)
		}
	}
}

// TestAzureAuthGate proves the reader half gates the environment before any
// call, with the shared refusal code.
func TestAzureAuthGate(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	ref := azureHost{}.auth(map[string]string{})
	if ref == nil || ref.Code != azure.CodeAuthMissing {
		t.Fatalf("auth = %+v, want %s", ref, azure.CodeAuthMissing)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none", callKeys(cmdr))
	}
}

// TestAzureReads proves the drift read, the identity read, and the
// open-thread recheck: only anchored threads that are still active or
// pending count, so a merge notice, a vote, a resolved thread, a deleted
// thread, and verdict's own summary thread are all skipped while an anchored
// code-change thread counts.
func TestAzureReads(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		azurePullRoute:     {stdout: azurePRJSON(azureHead)},
		azureIdentityRoute: {stdout: `{"authenticatedUser":{"id":"guid","properties":{"Account":{"$value":"rev@example.com"}}}}`},
		azureThreadsRoute: {stdout: `{"count":7,"value":[` +
			`{"id":1,"status":"active","threadContext":{"filePath":"/a.go","rightFileStart":{"line":4}},` +
			`"comments":[{"id":1,"content":"open","commentType":"text"}]},` +
			`{"id":2,"status":"pending","threadContext":{"filePath":"/a.go","leftFileStart":{"line":2}},` +
			`"comments":[{"id":1,"content":"pending","commentType":"text"}]},` +
			`{"id":3,"status":"fixed","threadContext":{"filePath":"/a.go","rightFileStart":{"line":5}},` +
			`"comments":[{"id":1,"content":"resolved","commentType":"text"}]},` +
			`{"id":4,"status":"active","isDeleted":true,"threadContext":{"filePath":"/a.go","rightFileStart":{"line":6}},` +
			`"comments":[{"id":1,"content":"gone","commentType":"text"}]},` +
			`{"id":5,"status":"active","threadContext":null,` +
			`"comments":[{"id":1,"content":"<!-- verdict-summary head=aaaa -->","commentType":"text"}]},` +
			`{"id":6,"status":"active","threadContext":null,` +
			`"comments":[{"id":1,"content":"rev voted 10","commentType":"system"}]},` +
			`{"id":7,"status":"active","threadContext":{"filePath":"/a.go","rightFileStart":{"line":9}},` +
			`"comments":[{"id":1,"content":"reviewer note","commentType":"codeChange"}]}` +
			`]}`},
	}}
	host := azureHost{}
	sha, err := host.liveSHA(context.Background(), cmdr, azureEnv(), "acme/app", "7")
	if err != nil || sha != azureHead {
		t.Errorf("liveSHA = %q, %v; want the fixture head", sha, err)
	}
	me, err := host.selfLogin(context.Background(), cmdr, azureEnv())
	if err != nil || me != "rev@example.com" {
		t.Errorf("selfLogin = %q, %v; want the account name", me, err)
	}
	open, reason, err := host.recheck(context.Background(), cmdr, azureEnv(), "acme/app", "7", dir)
	if err != nil || reason != "" {
		t.Fatalf("recheck = %d, %q, %v", open, reason, err)
	}
	if open != 3 {
		t.Errorf("open threads = %d, want the three live anchored threads", open)
	}
}

// TestAzureRecheckUnreadable proves a failed read leaves the count unknown
// instead of short, and records the host's words in the sidecar.
func TestAzureRecheckUnreadable(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		azureThreadsRoute: {stdout: "not a thread list"},
	}}
	open, reason, err := azureHost{}.recheck(context.Background(), cmdr, azureEnv(), "acme/app", "7", dir)
	if err == nil || open != 0 {
		t.Fatalf("recheck = %d, %v; want an unreadable read", open, err)
	}
	if reason == "" {
		t.Error("an unreadable recheck reported no reason")
	}
	if _, serr := os.Stat(dir + recheckErrSuffix); serr != nil {
		t.Errorf("recheck sidecar missing: %v", serr)
	}
}

// TestAzureUnreadableHunkIndex proves an unreadable bundle hunk index
// refuses before any write, rather than withholding every anchor as a side
// mismatch.
func TestAzureUnreadableHunkIndex(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	if err := os.Remove(dir + "/hunks.tsv"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir+"/hunks.tsv", 0o755); err != nil {
		t.Fatal(err)
	}
	cmdr := azureCommander(t)
	comment := `{"path":"notes.txt","body":"x","line":2,"side":"LEFT"}`
	attempt := azurePost(t, cmdr, dir, azurePayload(eventComment, comment))
	if attempt.ok {
		t.Fatal("post landed with an unreadable hunk index")
	}
	want := "post failed: azure could not read the bundle hunk index — nothing was written"
	if attempt.message != want {
		t.Errorf("message = %q, want %q", attempt.message, want)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none before the hunk guard", callKeys(cmdr))
	}
}

// TestAzureDuplicatePayloadRefuses proves the duplicate-post refusal: a
// general thread already carrying the same payload marker refuses with
// duplicate-payload and no write.
func TestAzureDuplicatePayloadRefuses(t *testing.T) {
	t.Parallel()
	dir := azurePostDir(t)
	cmdr := azureCommander(t)
	st := &state{
		req: &Request{Runtime: &core.Runtime{Env: azureEnv(), Commander: cmdr}, Repo: "acme/app", Pr: "7"},
		dir: dir, commitID: azureHead, flags: Flags{},
	}
	st.body, st.comments = "> ✅ Looks good", nil
	st.phash = contentDigest(st.body, st.commitID, st.comments)
	st.body += "\n\n<!-- verdict-review payload=" + st.phash + " head=" + azureHead + " -->"
	st.payloads = []string{postPayload(st.body, eventComment, azureHead, st.comments)}
	cmdr.out[azureThreadsRoute] = scriptResult{stdout: `{"count":1,"value":[{"id":900,"status":"active",` +
		`"threadContext":null,"comments":[{"id":1,"content":` + quotedJSON(t, st.body) +
		`,"commentType":"text","author":{"uniqueName":"rev"}}]}]}`}
	if ref := st.refreshRemote(context.Background()); ref != nil {
		t.Fatalf("refreshRemote refusal: %v", ref)
	}
	if ref := st.remoteDiff(); ref == nil || ref.Code != "duplicate-payload" {
		t.Fatalf("remoteDiff = %v, want duplicate-payload", ref)
	}
	if got := countCalls(cmdr, azureWritePrefix); got != 0 {
		t.Errorf("thread writes = %d, want none on a duplicate", got)
	}
}

// TestAzureSummaryCRUD proves the summary seam: the list reads the general
// threads a summary can live in, the create posts one, and the update
// patches the opening comment of the thread the handle names.
//
//nolint:cyclop,revive // the summary matrix reads as one block
func TestAzureSummaryCRUD(t *testing.T) {
	t.Parallel()
	const marker = "<!-- verdict-summary head=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa -->"
	const page = azureOrg + "/acme/_git/app/pullrequest/7?discussionId=501"
	cmdr := azureCommander(t)
	cmdr.out[azureThreadsRoute] = scriptResult{stdout: `{"count":3,"value":[` +
		`{"id":501,"status":"active","threadContext":null,"publishedDate":"2026-11-02T12:00:00Z",` +
		`"lastUpdatedDate":"2026-11-02T12:00:00Z","comments":[{"id":1,"content":` + quotedJSON(t, "old summary\n\n"+marker) +
		`,"commentType":"text","author":{"uniqueName":"rev"}}]},` +
		`{"id":502,"status":"active","threadContext":{"filePath":"/a.go","rightFileStart":{"line":4}},` +
		`"comments":[{"id":1,"content":"anchored","commentType":"text"}]},` +
		`{"id":503,"status":"active","threadContext":null,"comments":[{"id":1,"content":"voted 10",` +
		`"commentType":"system"}]}` + `]}`}
	cmdr.out[azurePatchPrefix] = scriptResult{stdout: `{"id":1,"content":"new summary",` +
		`"publishedDate":"2026-11-02T12:00:00Z","lastUpdatedDate":"2026-11-02T13:00:00Z"}`}
	host := azureHost{}
	comments, err := host.listSummaryComments(context.Background(), cmdr, azureEnv(), "acme/app", "7")
	if err != nil {
		t.Fatalf("listSummaryComments: %v", err)
	}
	if len(comments) != 1 || comments[0].ID != "501/1" || comments[0].Author != "rev" {
		t.Fatalf("comments = %+v, want the one general thread by rev", comments)
	}
	created, err := host.createSummaryComment(context.Background(), cmdr, azureEnv(), "acme/app", "7", "new summary")
	if err != nil {
		t.Fatalf("createSummaryComment: %v", err)
	}
	if created.ID != "501/1" || created.URL != page {
		t.Errorf("created = %+v, want the created thread handle and page %q", created, page)
	}
	updated, err := host.updateSummaryComment(context.Background(), cmdr, azureEnv(), "acme/app", "7", "501/1", "new summary")
	if err != nil {
		t.Fatalf("updateSummaryComment: %v", err)
	}
	if updated.ID != "501/1" || updated.Body != "new summary" || updated.UpdatedAt != "2026-11-02T13:00:00Z" {
		t.Errorf("updated = %+v, want the patched comment", updated)
	}
	if _, err := host.updateSummaryComment(context.Background(), cmdr, azureEnv(), "acme/app", "7", "501", "x"); err == nil {
		t.Error("a summary handle without a comment id was patched")
	}
}

// TestAzureSummaryIDGate proves the handle is two positive integer ids: a
// crafted handle cannot address another endpoint or another comment.
func TestAzureSummaryIDGate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		id string
		ok bool
	}{
		{"501/1", true},
		{"1/501", true},
		{"501", false},
		{"501/", false},
		{"/1", false},
		{"501/1/2", false},
		{"501/x", false},
		{"x/1", false},
		{"0/1", false},
		{"501/0", false},
		{"-1/1", false},
		{"501/../../user", false},
	} {
		threadID, commentID, ok := splitAzureSummaryID(tc.id)
		if ok != tc.ok {
			t.Errorf("splitAzureSummaryID(%q) ok = %v, want %v", tc.id, ok, tc.ok)
			continue
		}
		if !tc.ok {
			continue
		}
		if threadID+azureSummaryIDJoin+commentID != tc.id {
			t.Errorf("splitAzureSummaryID(%q) = %q/%q, want the two ids back", tc.id, threadID, commentID)
		}
	}
}

// TestAzurePublicationSplits proves the live-content read: an anchored
// thread is a remote comment, a general thread carrying the marker is a
// review, and a system notice is neither.
func TestAzurePublicationSplits(t *testing.T) {
	t.Parallel()
	cmdr := azureCommander(t)
	cmdr.out[azureThreadsRoute] = scriptResult{stdout: `{"count":3,"value":[` +
		`{"id":501,"status":"active","threadContext":null,"comments":[{"id":1,` +
		`"content":"body<!-- verdict-review payload=hash head=aaaa -->","commentType":"text"}]},` +
		`{"id":502,"status":"active","threadContext":{"filePath":"/a.go","leftFileStart":{"line":2}},` +
		`"comments":[{"id":1,"content":"anchored","commentType":"text"}]},` +
		`{"id":503,"status":"active","threadContext":null,"comments":[{"id":1,"content":"rev voted 10",` +
		`"commentType":"system"}]}` + `]}`}
	remote, err := azureHost{}.publication(context.Background(), cmdr, azureEnv(), "acme/app", "7")
	if err != nil {
		t.Fatalf("publication: %v", err)
	}
	if len(remote.reviews) != 1 || remote.reviews[0].state != "commented" {
		t.Errorf("reviews = %+v, want the one marked general thread", remote.reviews)
	}
	if len(remote.comments) != 1 {
		t.Fatalf("comments = %+v, want the one anchored thread", remote.comments)
	}
	if got := jq.StrOf(remote.comments[0][fieldSide]); got != "LEFT" {
		t.Errorf("remote comment side = %q, want LEFT", got)
	}
	if got := jq.StrOf(remote.comments[0]["commit_id"]); got != azureHead {
		t.Errorf("remote comment head = %q, want the live pull head", got)
	}
}

// TestAzureHostSeam proves the provider switch returns the az writer, no
// resolver, and the summary seam: a host with no resolve call must not
// advertise one.
func TestAzureHostSeam(t *testing.T) {
	t.Parallel()
	writer, reader, resolver := hostFor(providerAzure)
	if writer.tool() != toolAz {
		t.Errorf("writer tool = %q, want %q", writer.tool(), toolAz)
	}
	if _, ok := reader.(azureHost); !ok {
		t.Errorf("reader = %T, want azureHost", reader)
	}
	if resolver != nil {
		t.Errorf("resolver = %T, want none", resolver)
	}
	host, err := summaryHostFor(providerAzure)
	if err != nil {
		t.Fatalf("summaryHostFor(azure): %v", err)
	}
	if _, ok := host.(azureHost); !ok {
		t.Errorf("summary host = %T, want azureHost", host)
	}
	if replier := replierFor(providerAzure); replier != nil {
		t.Errorf("replier = %T, want none", replier)
	}
}
