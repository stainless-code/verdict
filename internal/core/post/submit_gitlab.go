package postcore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// GitLab carries a review as its own primitives: one MR discussion per
// finding, one summary note, and — for a binding approval — one approve call.
// Nothing there is atomic, so the writes land in that order (comments, body,
// verdict last) and the retry policy never replays a payload whose first
// write already landed.
const (
	// toolGlab is the CLI every GitLab read and write goes through.
	toolGlab = "glab"
	// gitlabPositionText is the only position type verdict anchors with:
	// added, removed, and unchanged text lines, never image boxes.
	gitlabPositionText = "text"
	// gitlabDiscussionsPath is the MR-scoped discussions endpoint.
	gitlabDiscussionsPath = "/discussions"
	// gitlabPageQuery is the discussions page the approval recheck reads;
	// 100 is the API maximum, so one page covers a normal merge request.
	gitlabPageQuery = "?per_page=100"
	// gitlabSideLeft is the payload's old-side marker (`side: "LEFT"`).
	gitlabSideLeft = "LEFT"
	// flagPaginate walks every page of one GET in a single invocation.
	flagPaginate = "--paginate"
	// flagRawField adds one string parameter to a write.
	flagRawField = "-f"
	// approveSHAField names the version an approval covers.
	approveSHAField = "sha="
	// flagHeader adds one explicit request header to a call.
	flagHeader = "--header"
	// gitlabContentType is the content type a `--input` write must carry.
	// glab marks a body as JSON only for a `--field` request (`bodyIsJSON` is
	// set in the `map[string]any` case of `httpRequest` and nowhere on the
	// `io.Reader` path `--input` takes), so without it the body goes out with
	// no content type and GitLab refuses it with 415. glab's own fix for the
	// same gap carries the comment "GitLab rejects a body with no Content-Type
	// with HTTP 415"; an explicit header is added before that default and is
	// never overridden.
	gitlabContentType = "Content-Type: application/json"
)

// errGitLabMRShape refuses a merge-request read that is not one JSON object:
// without it the diff refs a position needs are unknowable.
var errGitLabMRShape = shapeError("gitlab merge request read unreadable")

// gitlabDiffRefs are the three SHAs every GitLab text position is pinned to.
type gitlabDiffRefs struct {
	BaseSHA  string `json:"base_sha"`
	HeadSHA  string `json:"head_sha"`
	StartSHA string `json:"start_sha"`
}

// gitlabMR is the slice of a live merge request a post needs: the diff refs,
// the head the review covers, the page URL the receipt records, and the iid
// the receipt id falls back to.
type gitlabMR struct {
	DiffRefs gitlabDiffRefs `json:"diff_refs"`
	WebURL   string         `json:"web_url"`
	SHA      string         `json:"sha"`
	IID      int            `json:"iid"`
}

// headSHA is the commit the merge request currently points at: the diff refs
// when GitLab filled them, the top-level sha otherwise.
func (mr *gitlabMR) headSHA() string {
	if mr.DiffRefs.HeadSHA != "" {
		return mr.DiffRefs.HeadSHA
	}
	return mr.SHA
}

// diffRefsComplete reports whether the three SHAs a text position needs are
// all present.
func (mr *gitlabMR) diffRefsComplete() bool {
	return mr.DiffRefs.BaseSHA != "" && mr.DiffRefs.StartSHA != "" && mr.headSHA() != ""
}

// gitlabNoteReceipt is the part of a created note the post receipt records.
type gitlabNoteReceipt struct {
	createdAt string
	id        int
}

// gitlabPostPayload is the part of the hosted payload a GitLab replay reads:
// the summary body, the binding event, the reviewed head, and the inline
// findings in payload order.
type gitlabPostPayload struct {
	body     string
	event    string
	commitID string
	comments []gitlabPostComment
	dropped  int
	summary  bool
}

// gitlabPostComment is one inline finding reduced to what a discussion
// position needs.
type gitlabPostComment struct {
	path  string
	body  string
	side  string
	line  int
	start int
}

// gitlabDiff is one diff section's position facts: the pre-change path a
// rename anchors both sides of, and per new-side line whether it is added and
// which old-side line it came from.
type gitlabDiff struct {
	lines   map[int]gitlabDiffLine
	oldPath string
}

// gitlabDiffLine is one new-side diff line: an added line carries no old
// number, an unchanged line carries both.
type gitlabDiffLine struct {
	old   int
	added bool
}

// gitlabEndpoint renders one MR-scoped REST path. The repo rides as a single
// URL-encoded segment, which GitLab accepts wherever a numeric project id
// would go, so a post needs no project-id read of its own.
func gitlabEndpoint(repo, pr, suffix string) string {
	return "projects/" + url.PathEscape(repo) + "/merge_requests/" + pr + suffix
}

// gitlabReadArgv is one project-scoped REST read.
func gitlabReadArgv(repo, pr, suffix string) []string {
	return []string{toolGlab, cmdAPI, gitlabEndpoint(repo, pr, suffix)}
}

// gitlabWriteArgv is one project-scoped REST write: the JSON body arrives on
// stdin exactly like the GitHub review POST, with the content type pinned on
// the argv so the wire shape does not depend on the glab version.
func gitlabWriteArgv(repo, pr, suffix string) []string {
	return []string{
		toolGlab, cmdAPI, gitlabEndpoint(repo, pr, suffix),
		flagMethod, methodPost, flagHeader, gitlabContentType, flagInput, "-",
	}
}

// gitlabMRRead reads the live merge request once and hands the response back
// with it, so a caller that owns the .post.err sidecar can record the forge's
// own words.
func gitlabMRRead(ctx context.Context, cmdr Commander, repo, pr string) (gitlabMR, core.Result, error) {
	res, err := runReadRetry(ctx, cmdr, &core.Command{Argv: gitlabReadArgv(repo, pr, "")})
	if err != nil {
		return gitlabMR{}, res, err
	}
	mr, ok := decodeGitLabMR(res.Stdout)
	if !ok {
		return gitlabMR{}, res, errGitLabMRShape
	}
	return mr, res, nil
}

// decodeGitLabMR decodes one merge-request read; a malformed payload never
// half-populates the refs a position would then guess at.
func decodeGitLabMR(raw string) (gitlabMR, bool) {
	var mr gitlabMR
	if err := json.Unmarshal([]byte(raw), &mr); err != nil {
		return gitlabMR{}, false
	}
	return mr, true
}

// gitlabSelfLogin reads the authenticated GitLab login the authorship gate
// compares the bundle author against.
func gitlabSelfLogin(ctx context.Context, cmdr Commander) (string, error) {
	if cmdr == nil {
		return "", errNoCommander
	}
	res, err := runReadRetry(ctx, cmdr, &core.Command{Argv: []string{toolGlab, cmdAPI, "user"}})
	if err != nil {
		return "", err
	}
	var user struct {
		Username string `json:"username"`
	}
	if uerr := json.Unmarshal([]byte(res.Stdout), &user); uerr != nil {
		return "", jq.Sealed("gitlab self login", uerr)
	}
	return user.Username, nil
}

// gitlabRecheck counts the live unresolved discussions on the merge request.
// GitLab keeps the resolution pair on a discussion's root note, so a
// resolvable root note that is not resolved is one open thread; a plain MR
// comment is not resolvable and never counts.
func gitlabRecheck(ctx context.Context, cmdr Commander, repo, pr, dir string) (int, string, error) {
	var res core.Result
	var err error
	if cmdr == nil {
		err = errNoCommander
	} else {
		argv := append(gitlabReadArgv(repo, pr, gitlabDiscussionsPath+gitlabPageQuery), flagPaginate)
		res, err = runReadRetry(ctx, cmdr, &core.Command{Argv: argv})
	}
	if werr := atomic.WriteErrSidecar(dir+recheckErrSuffix, []byte(res.Stderr)); werr != nil {
		return 0, "", jq.Sealed("write .recheck.err", werr)
	}
	if err != nil {
		return 0, firstReason(res.Stderr), err
	}
	n, cerr := countGitLabOpen(res.Stdout)
	if cerr != nil {
		return 0, firstReason(res.Stderr), cerr
	}
	return n, "", nil
}

// countGitLabOpen counts the open discussions across the paged payload
// `glab api --paginate` emits: one adjacent JSON array per page.
func countGitLabOpen(stdout string) (int, error) {
	docs, err := jq.DecodeSlurp(strings.NewReader(stdout))
	if err != nil {
		return 0, jq.Sealed("slurp gitlab discussions", err)
	}
	n := 0
	for _, doc := range docs {
		page, ok := doc.([]any)
		if !ok {
			return 0, errRecheckShape
		}
		n += countOpenInPage(page)
	}
	return n, nil
}

// countOpenInPage counts the open discussions in one page of the slurped
// payload.
func countOpenInPage(page []any) int {
	n := 0
	for _, discussion := range page {
		if gitlabDiscussionOpen(discussion) {
			n++
		}
	}
	return n
}

// gitlabDiscussionOpen reports whether one discussion is a resolvable thread
// its root note leaves unresolved.
func gitlabDiscussionOpen(discussion any) bool {
	m, ok := discussion.(map[string]any)
	if !ok {
		return false
	}
	for _, note := range jq.Elements(m["notes"]) {
		nm, ok := note.(map[string]any)
		if !ok || !jq.Falsey(nm["system"]) {
			continue
		}
		return !jq.Falsey(nm["resolvable"]) && jq.Falsey(nm["resolved"])
	}
	return false
}

// gitlabPost replays one hosted payload onto the merge request: a discussion
// per inline finding, then the summary note, then the approval. A payload
// that carries no comments is a body-split follow-up part and is a note
// alone; only an APPROVE payload on a host whose capability carries approve
// performs the approval — a host that cannot approve posts the discussions and
// note, and the service has already warned that the verdict did not bind.
func gitlabPost(ctx context.Context, cmdr Commander, caps core.HostCapabilities, repo, pr, dir, payload string) postAttempt {
	part, ok := parseGitLabPayload(payload)
	if !ok {
		return postFailure(dir, msgGitLabPayload, false, nil)
	}
	mr, res, err := gitlabMRRead(ctx, cmdr, repo, pr)
	if err != nil {
		return postFailure(dir, failureReason(res, err), false, nil)
	}
	warnings := gitlabPostWarnings(&part)
	if len(part.comments) > 0 && !mr.diffRefsComplete() {
		return postFailure(dir, msgGitLabNoDiffRefs, false, warnings)
	}
	posted, attempt, ok := gitlabDiscussions(ctx, cmdr, repo, pr, &mr, gitlabDiffIndex(dir), &part, dir, warnings)
	if !ok {
		return attempt
	}
	note, attempt, ok := gitlabSummaryNote(ctx, cmdr, repo, pr, &part, posted, dir, warnings)
	if !ok {
		return attempt
	}
	approved, attempt, ok := gitlabApproveLeg(ctx, cmdr, caps, repo, pr, &part, dir, warnings)
	if !ok {
		return attempt
	}
	return postAccepted(dir, gitlabReceiptObject(&mr, note, approved), "", warnings)
}

// gitlabSummaryNote posts the summary note that carries the review body, after
// the discussions landed. The progress prefix keeps a partial run readable.
func gitlabSummaryNote(
	ctx context.Context, cmdr Commander, repo, pr string, part *gitlabPostPayload,
	posted int, dir string, warnings []string,
) (gitlabNoteReceipt, postAttempt, bool) {
	if !part.summary {
		return gitlabNoteReceipt{}, postAttempt{}, true
	}
	res, err := gitlabCall(ctx, cmdr, gitlabWriteArgv(repo, pr, "/notes"), jq.NewObj(fieldBody, part.body).MarshalPretty())
	if err != nil || res.ExitCode != 0 {
		reason := failureReason(res, err)
		return gitlabNoteReceipt{}, postFailure(dir, reason, posted > 0,
			append(warnings, gitlabProgress(posted, len(part.comments))...)), false
	}
	return decodeGitLabNote(res.Stdout), postAttempt{}, true
}

// gitlabApproveLeg performs the approval a bound APPROVE payload carries, and
// only on a host whose capability advertises it: a host that cannot approve has
// already warned that the verdict did not bind.
func gitlabApproveLeg(
	ctx context.Context, cmdr Commander, caps core.HostCapabilities,
	repo, pr string, part *gitlabPostPayload, dir string, warnings []string,
) (bool, postAttempt, bool) {
	if part.event != eventApprove || !caps.Approve {
		return false, postAttempt{}, true
	}
	ares, err := gitlabApprove(ctx, cmdr, repo, pr, part.commitID)
	if err != nil || ares.ExitCode != 0 {
		return false, postFailure(dir, failureReason(ares, err), true, warnings), false
	}
	return true, postAttempt{}, true
}

// gitlabDiscussions posts one MR discussion per inline finding in payload
// order and stops at the first failure; the caller's retry policy reads the
// landed count instead of replaying it.
func gitlabDiscussions(
	ctx context.Context, cmdr Commander, repo, pr string, mr *gitlabMR,
	diff map[string]gitlabDiff, part *gitlabPostPayload, dir string, warnings []string,
) (int, postAttempt, bool) {
	for k, c := range part.comments {
		body := jq.NewObj(fieldBody, c.body, "position", gitlabPosition(mr, diff[c.path], c)).MarshalPretty()
		res, err := gitlabCall(ctx, cmdr, gitlabWriteArgv(repo, pr, gitlabDiscussionsPath), body)
		if err != nil || res.ExitCode != 0 {
			reason := failureReason(res, err)
			return k, postFailure(dir, reason, k > 0, append(warnings, gitlabProgress(k, len(part.comments))...)), false
		}
	}
	return len(part.comments), postAttempt{}, true
}

// gitlabApprove submits the binding approval for the reviewed head, so a
// commit that landed between the read and the approve fails the call instead
// of approving code this review never covered.
func gitlabApprove(ctx context.Context, cmdr Commander, repo, pr, sha string) (core.Result, error) {
	argv := []string{toolGlab, cmdAPI, gitlabEndpoint(repo, pr, "/approve"), flagMethod, methodPost}
	if sha != "" {
		argv = append(argv, flagRawField, approveSHAField+sha)
	}
	return gitlabCall(ctx, cmdr, argv, "")
}

// gitlabCall runs one GitLab write and seals a spawn failure; the caller
// reports the result's own stdout/stderr.
func gitlabCall(ctx context.Context, cmdr Commander, argv []string, body string) (core.Result, error) {
	cmd := &core.Command{Argv: argv}
	if body != "" {
		cmd.Stdin = strings.NewReader(body)
	}
	res, err := cmdr.Run(ctx, cmd)
	if err != nil {
		return res, jq.Sealed("gitlab call", err)
	}
	return res, nil
}

// gitlabPosition builds the diff position for one finding. GitLab validates
// both paths and needs the MR's three diff SHAs; a line the diff already
// carried keeps its old-side number too, because GitLab resolves a context
// anchor only with both numbers.
func gitlabPosition(mr *gitlabMR, file gitlabDiff, c gitlabPostComment) *jq.Obj {
	oldPath := file.oldPath
	if oldPath == "" {
		oldPath = c.path
	}
	pos := jq.NewObj(
		"position_type", gitlabPositionText,
		"base_sha", mr.DiffRefs.BaseSHA,
		"start_sha", mr.DiffRefs.StartSHA,
		"head_sha", mr.headSHA(),
		"old_path", oldPath,
		"new_path", c.path,
	)
	if strings.EqualFold(c.side, gitlabSideLeft) {
		pos.Set("old_line", c.line)
		return pos
	}
	pos.Set("new_line", c.line)
	if line, ok := file.lines[c.line]; ok && !line.added && line.old > 0 {
		pos.Set("old_line", line.old)
	}
	return pos
}

// gitlabDiffIndex scans the bundle diff once into the position facts every
// finding needs.
func gitlabDiffIndex(dir string) map[string]gitlabDiff {
	raw, err := os.ReadFile(dir + "/full.diff")
	if err != nil {
		return nil
	}
	text := string(raw)
	files := hunks.Files(text)
	lines := strings.Split(text, newline)
	out := make(map[string]gitlabDiff, len(files))
	for i := range files {
		out[files[i].Path] = gitlabDiff{
			oldPath: files[i].OldPath,
			lines:   gitlabLineMap(lines, files[i].StartLine, files[i].EndLine),
		}
	}
	return out
}

// gitlabLineMap numbers one section's hunk bodies the way the diff does and
// records every new-side line, so a position knows whether its line is added
// and which old-side line it came from.
func gitlabLineMap(lines []string, start, end int) map[int]gitlabDiffLine {
	out := map[int]gitlabDiffLine{}
	old, cur, body := 0, 0, false
	for i := start; i < end && i < len(lines); i++ {
		ln := lines[i]
		if strings.HasPrefix(ln, "@@ -") {
			old, cur, body = hunkNumbers(ln)
			continue
		}
		if !body {
			continue
		}
		old, cur = recordLine(out, ln, old, cur)
	}
	return out
}

// hunkHeaderFields is how many fields a hunk header needs before its ranges are
// readable: the `@@` marker, the old range, and the new range.
const hunkHeaderFields = 3

// hunkNumbers reads the old and new start line out of one `@@ -a,b +c,d @@`
// header; the new counter is what a new-side line number counts from.
func hunkNumbers(header string) (old, cur int, ok bool) {
	fields := strings.Fields(header)
	if len(fields) < hunkHeaderFields || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, false
	}
	return rangeStart(fields[1]), rangeStart(fields[2]), true
}

// rangeStart reads the first number of one `-a,b` or `+a,b` hunk range.
func rangeStart(field string) int {
	head, _, _ := strings.Cut(strings.TrimLeft(field, "-+"), ",")
	n, err := strconv.Atoi(head)
	if err != nil {
		return 0
	}
	return n
}

// parseGitLabPayload reads the hosted payload tolerantly: a comment without a
// path, body, or line can never anchor a discussion, so it is counted instead
// of silently dropped.
func parseGitLabPayload(payload string) (gitlabPostPayload, bool) {
	doc, err := jq.DecodeOrdered([]byte(payload))
	if err != nil {
		return gitlabPostPayload{}, false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok || obj == nil {
		return gitlabPostPayload{}, false
	}
	comments, _ := obj.Get(fieldComments)
	_, hasSummary := obj.Get(fieldBody)
	out := gitlabPostPayload{
		summary:  hasSummary,
		body:     orderedString(obj, fieldBody),
		event:    orderedString(obj, "event"),
		commitID: orderedString(obj, "commit_id"),
	}
	for _, c := range jq.Elements(comments) {
		comment, ok := gitlabComment(c)
		if !ok {
			out.dropped++
			continue
		}
		out.comments = append(out.comments, comment)
	}
	return out, true
}

// gitlabComment reduces one payload comment to a position-able finding.
func gitlabComment(c any) (gitlabPostComment, bool) {
	m, ok := c.(*jq.Obj)
	if !ok || m == nil {
		return gitlabPostComment{}, false
	}
	comment := gitlabPostComment{
		path:  orderedString(m, fieldPath),
		body:  orderedString(m, fieldBody),
		side:  orderedString(m, "side"),
		line:  orderedInt(m, fieldLine),
		start: orderedInt(m, fieldStartLine),
	}
	if comment.path == "" || comment.body == "" || comment.line <= 0 {
		return gitlabPostComment{}, false
	}
	return comment, true
}

// orderedInt reads one integer field from an ordered JSON object, reporting 0
// when the key is missing or not a number.
func orderedInt(obj *jq.Obj, key string) int {
	v, ok := obj.Get(key)
	if !ok {
		return 0
	}
	n, _ := jq.IntOf(v)
	return n
}

// gitlabPostWarnings states the mapping gaps this payload hits: multi-line
// anchors the position flattens, and findings that never became a discussion.
// Verdict gaps — a request-changes event, an approval — come from the
// capability table in the service, so one place decides what a host carries.
func gitlabPostWarnings(part *gitlabPostPayload) []string {
	var warnings []string
	if n := countGitLabMultiLine(part.comments); n > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"gitlab positions are single-line — %d multi-line anchor(s) anchored at their end line", n))
	}
	if part.dropped > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"gitlab inline discussions need a path, line, and body — %d finding(s) kept out of the diff", part.dropped))
	}
	return warnings
}

// countGitLabMultiLine counts the findings whose anchor spans lines.
func countGitLabMultiLine(comments []gitlabPostComment) int {
	n := 0
	for _, c := range comments {
		if c.start > 0 && c.start != c.line {
			n++
		}
	}
	return n
}

// gitlabProgress names what a stopped replay already wrote, so a re-run is a
// known duplicate instead of a surprise.
func gitlabProgress(landed, total int) []string {
	if landed == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"gitlab post stopped after %d of %d inline discussion(s) — a re-run reposts them", landed, total)}
}

// gitlabReceiptObject is the accepted review in the receipt shape the
// envelope and post.json carry: the summary note id (or the MR iid when the
// note read was unreadable), the verdict state, and the merge request page.
func gitlabReceiptObject(mr *gitlabMR, note gitlabNoteReceipt, approved bool) string {
	state := "commented"
	if approved {
		state = "approved"
	}
	id := note.id
	if id == 0 {
		id = mr.IID
	}
	obj := jq.NewObj(
		"id", id,
		"state", state,
		"html_url", mr.WebURL,
		"submitted_at", note.createdAt,
	)
	return strings.TrimRight(jq.PrettyOrdered(obj), newline)
}

// decodeGitLabNote reads the id and timestamp of a created summary note; an
// unreadable receipt leaves the MR iid as the recorded id.
func decodeGitLabNote(raw string) gitlabNoteReceipt {
	var note struct {
		CreatedAt string `json:"created_at"`
		ID        int    `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &note); err != nil {
		return gitlabNoteReceipt{}
	}
	return gitlabNoteReceipt{id: note.ID, createdAt: note.CreatedAt}
}

// recordLine maps one hunk body line onto the new-side numbers and returns the
// advanced counters. A blank context line is a context line: git renders it as
// an empty string with no leading space, and skipping it would desync every
// line number after it.
func recordLine(out map[int]gitlabDiffLine, ln string, old, cur int) (int, int) {
	switch {
	case strings.HasPrefix(ln, `\`):
		return old, cur
	case strings.HasPrefix(ln, "+"):
		out[cur] = gitlabDiffLine{added: true}
		return old, cur + 1
	case strings.HasPrefix(ln, "-"):
		return old + 1, cur
	default:
		out[cur] = gitlabDiffLine{old: old}
		return old + 1, cur + 1
	}
}
