package postcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/bitbucket"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/curlauth"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/untrusted"
)

// Bitbucket Cloud has no CLI: every read and write is one API call through
// `curl`, like Forgejo. The write replays three primitives in GitLab's
// order — one inline comment per finding, the summary comment, then the
// approval last — because Bitbucket takes no atomic review and its
// approval carries no commit pin.
const (
	// bitbucketSideLeft is the payload's old-side marker (`side: "LEFT"`).
	bitbucketSideLeft = "LEFT"

	// bitbucketFlagFollow tells a paged comment read to follow the next
	// URL's redirect, matching the fetch comment walk.
	bitbucketFlagFollow = "-sfL"

	// fieldID is the comment row's numeric id, as the wire spells it.
	fieldID = "id"

	// bitbucketStateCommented is the receipt state a Bitbucket approval
	// that did not fire records.
	bitbucketStateCommented = "commented"

	// bitbucketStatusOKMin and bitbucketStatusOKMax bound a 2xx answer.
	bitbucketStatusOKMin = 200
	bitbucketStatusOKMax = 299

	// bitbucketMaxCommentPages bounds the comment-list walk so a next
	// cursor that never stops cannot loop forever.
	bitbucketMaxCommentPages = 1000
)

// Bitbucket-specific refusals. A reason recorded through postFailure reads
// inside msgPostFailed; a sentence recorded through postRefusal is the
// whole message.
const (
	msgBitbucketPayload = "bitbucket post payload unreadable"
	msgBitbucketLost    = "bitbucket did not answer — the review may exist; check the PR before re-running"
	msgBitbucketDropped = "bitbucket anchors need a path, numeric line, and body — %d finding(s) withheld from the review"
	msgBitbucketDeleted = "bitbucket cannot anchor a comment on a file the change deletes — %d finding(s) withheld at submit"
	msgBitbucketMulti   = "bitbucket anchors are single-line — %d multi-line anchor(s) anchored at their end line"
	msgBitbucketConfig  = "post failed: bitbucket could not stage the curl credential file — nothing was written"
	msgBitbucketNoHead  = "post failed: bitbucket could not read the PR head — nothing was written"
	msgBitbucketStale   = "post failed: the PR head moved since fetch (review covers %s, live HEAD is %s) — " +
		"a Bitbucket approval carries no commit pin, so it would approve code this review never covered; " +
		"re-fetch and re-run"
	msgBitbucketNoCommit = "post failed: the payload carries no commit_id — bitbucket would approve the " +
		"current head instead of the reviewed one; re-fetch to record the head"
	msgBitbucketNoHunks = "post failed: bitbucket could not read the bundle hunk index — nothing was written"
	msgBitbucketEvent   = "post failed: bitbucket has no review event named %q — post a comment, an approval, " +
		"or nothing; a request for changes posts as findings plus a summary note"
)

// bitbucketHost is the `curl` host for Bitbucket Cloud.
type bitbucketHost struct{}

func (bitbucketHost) tool() string { return toolCurl }

// auth gates the environment before the first call.
func (bitbucketHost) auth(env map[string]string) *core.RefusalError {
	return bitbucket.Auth(env)
}

// bitbucketRefusalOf re-states the shared package's refusal as this
// package's own, so the published code and the operator sentence cross
// the seam unchanged and stay inspectable without a wrap.
func bitbucketRefusalOf(ref *core.RefusalError) *core.RefusalError {
	return &core.RefusalError{Code: ref.Code, Message: ref.Message}
}

// bitbucketPostPayload is the part of the hosted payload a Bitbucket
// write reads: the summary body, the binding event, the reviewed head,
// and the inline findings that survive the anchor rules.
type bitbucketPostPayload struct {
	body     string
	event    string
	rawEvent string
	commitID string
	comments []bitbucketComment
	warnings []string
}

// bitbucketComment is one finding reduced to what a Bitbucket comment
// needs: a path, a body, and the line on one side.
type bitbucketComment struct {
	path  string
	body  string
	left  bool
	line  int
	start int
}

// bitbucketHTTP is one `curl -i` answer: the status code and whatever
// follows the headers. Bitbucket writes answer 201, and the approval
// answers 204 with no body.
type bitbucketHTTP struct {
	body   string
	status int
}

// post replays one payload: inline comments, the summary comment, then
// the approval last. Nothing there is atomic, so a mid-way failure names
// what landed and is never replayed.
func (bitbucketHost) post(ctx context.Context, cmdr Commander, env map[string]string,
	caps core.HostCapabilities, repo, pr, dir, payload string,
) postAttempt {
	auth, base, ref := bitbucket.Resolve(env, repo, pr)
	if ref != nil {
		return postRefusal(dir, ref.Message, nil)
	}
	newSide, lerr := bitbucketNewSidePaths(dir)
	if lerr != nil {
		return postRefusal(dir, msgBitbucketNoHunks, nil)
	}
	part, ok := parseBitbucketPayload(payload, newSide)
	if !ok {
		return postFailure(dir, msgBitbucketPayload, false, nil)
	}
	live, attempt, ok := bitbucketPostHead(ctx, cmdr, auth, base, &part, dir)
	if !ok {
		return attempt
	}
	posted, links, attempt, ok := bitbucketInlineComments(ctx, cmdr, auth, base, &part, dir, part.warnings)
	if !ok {
		return attempt
	}
	summary, attempt, ok := bitbucketSummaryLeg(ctx, cmdr, auth, base, &part, posted, dir, part.warnings)
	if !ok {
		return attempt
	}
	approved, attempt, ok := bitbucketApproveLeg(ctx, cmdr, auth, base, caps, &part, dir, part.warnings)
	if !ok {
		return attempt
	}
	resAttempt := postAccepted(dir, bitbucketReceiptObject(&live, &summary, approved), "", part.warnings)
	resAttempt.inlineLinks = links
	return resAttempt
}

// bitbucketPostHead runs the pre-write guards: the reviewed commit, a
// known event, and — for an approval — a live head that still matches.
func bitbucketPostHead(
	ctx context.Context, cmdr Commander, auth curlauth.Credential, base string,
	part *bitbucketPostPayload, dir string,
) (bitbucketPR, postAttempt, bool) {
	warnings := part.warnings
	if part.commitID == "" {
		return bitbucketPR{}, postRefusal(dir, msgBitbucketNoCommit, warnings), false
	}
	if part.event == "" {
		return bitbucketPR{}, postRefusal(dir, fmt.Sprintf(msgBitbucketEvent, part.rawEvent), warnings), false
	}
	live, attempt, ok := bitbucketPRRead(ctx, cmdr, auth, base, dir, warnings)
	if !ok {
		return bitbucketPR{}, attempt, false
	}
	if part.event == eventApprove && live.head != part.commitID {
		stale := fmt.Sprintf(msgBitbucketStale, shortOrUnknown(part.commitID), shortOrUnknown(live.head))
		return bitbucketPR{}, postRefusal(dir, stale, warnings), false
	}
	return live, postAttempt{}, true
}

// bitbucketPR is the slice of one PR read a post needs: the live head
// the approval is pinned to, and the page URL the receipt records.
type bitbucketPR struct {
	head    string
	htmlURL string
}

// bitbucketPRRead reads the live PR once and hands back the head with the
// page URL, so a caller that owns the .post.err sidecar can record the
// host's own words.
func bitbucketPRRead(
	ctx context.Context, cmdr Commander, auth curlauth.Credential,
	base, dir string, warnings []string,
) (bitbucketPR, postAttempt, bool) {
	raw, stderr, err := bitbucketRead(ctx, cmdr, auth, base)
	if err != nil {
		reason := msgBitbucketNoHead
		if detail := firstReason(stderr); detail != "" {
			reason += ": " + detail
		}
		return bitbucketPR{}, postFailure(dir, reason, false, warnings), false
	}
	pull := bitbucket.DecodePull(raw)
	if pull.Source.Commit.Hash == "" {
		return bitbucketPR{}, postFailure(dir, msgBitbucketNoHead, false, warnings), false
	}
	return bitbucketPR{head: pull.Source.Commit.Hash, htmlURL: bitbucket.DecodePullHTML(raw)}, postAttempt{}, true
}

// shortOrUnknown shortens one SHA for an operator sentence. The live head is
// the host's text, so it is folded onto the sentence's line.
func shortOrUnknown(sha string) string {
	if sha == "" {
		return "unknown"
	}
	return untrusted.Line(readcommon.ShortSHA(sha))
}

// bitbucketInlineComments posts one anchored comment per finding in
// payload order and stops at the first failure; the caller's retry
// policy reads the landed count instead of replaying it.
func bitbucketInlineComments(
	ctx context.Context, cmdr Commander, auth curlauth.Credential, base string,
	part *bitbucketPostPayload, dir string, warnings []string,
) (int, []summaryInlineLink, postAttempt, bool) {
	links := make([]summaryInlineLink, 0, len(part.comments))
	for k, c := range part.comments {
		body := bitbucketInlineBody(c)
		created, attempt, ok := bitbucketCommentWrite(ctx, cmdr, auth, base+bitbucket.CommentsPath, body,
			dir, warnings, k, len(part.comments))
		if !ok {
			return k, nil, attempt, false
		}
		links = append(links, bitbucketInlineLink(c, &created))
	}
	return len(part.comments), links, postAttempt{}, true
}

// bitbucketInlineBody renders one inline comment: a new-side `to` or an
// old-side `from`, never both.
func bitbucketInlineBody(c bitbucketComment) string {
	inline := jq.NewObj(fieldPath, c.path)
	if c.left {
		inline.Set("from", c.line)
	} else {
		inline.Set("to", c.line)
	}
	return jq.NewObj("content", jq.NewObj("raw", c.body), "inline", inline).MarshalPretty()
}

// bitbucketInlineLink reads the created comment's page back into the
// summary links.
func bitbucketInlineLink(c bitbucketComment, created *bitbucket.Comment) summaryInlineLink {
	link := summaryInlineLink{Path: c.path, Line: c.line, Body: c.body}
	if created.Links.HTML.Href != "" {
		link.URL = created.Links.HTML.Href
	}
	return link
}

// bitbucketSummaryLeg posts the summary comment that carries the review
// body, after the inline comments landed. An empty body posts nothing:
// a follow-up part carries its own header and no marker to restore.
func bitbucketSummaryLeg(
	ctx context.Context, cmdr Commander, auth curlauth.Credential, base string,
	part *bitbucketPostPayload, posted int, dir string, warnings []string,
) (bitbucket.Comment, postAttempt, bool) {
	if strings.TrimSpace(part.body) == "" {
		return bitbucket.Comment{}, postAttempt{}, true
	}
	body := jq.NewObj("content", jq.NewObj("raw", part.body)).MarshalPretty()
	created, attempt, ok := bitbucketCommentWrite(ctx, cmdr, auth, base+bitbucket.CommentsPath, body,
		dir, warnings, posted, len(part.comments))
	if !ok {
		return bitbucket.Comment{}, attempt, false
	}
	return created, postAttempt{}, true
}

// bitbucketApproveLeg performs the approval a bound APPROVE payload
// carries, and only on a host whose capability advertises it: a host
// that cannot approve has already warned that the verdict did not bind.
// The PR head was read above, so an approval never lands on a head this
// review never covered.
func bitbucketApproveLeg(
	ctx context.Context, cmdr Commander, auth curlauth.Credential, base string,
	caps core.HostCapabilities, part *bitbucketPostPayload, dir string, warnings []string,
) (bool, postAttempt, bool) {
	if part.event != eventApprove || !caps.Approve {
		return false, postAttempt{}, true
	}
	landed := len(part.comments)
	if _, attempt, ok := bitbucketApproveCall(ctx, cmdr, auth, base, dir, warnings, landed, len(part.comments)); !ok {
		return false, attempt, false
	}
	return true, postAttempt{}, true
}

// bitbucketApproveCall submits the binding approval. Bitbucket takes no
// commit pin, so the caller pinned the head by reading the PR first; a
// head that moved since the read still fails the approval rather than
// covering unreviewed code.
func bitbucketApproveCall(
	ctx context.Context, cmdr Commander, auth curlauth.Credential, base, dir string,
	warnings []string, landed, total int,
) (bitbucketHTTP, postAttempt, bool) {
	argv, cleanup, cerr := bitbucketMethodArgv(auth, methodPost, base+bitbucket.ApprovePath)
	if cerr != nil {
		return bitbucketHTTP{}, postRefusal(dir, msgBitbucketConfig, warnings), false
	}
	defer cleanup()
	res, err := bitbucketCall(ctx, cmdr, argv, `{}`)
	if err != nil {
		return bitbucketHTTP{}, postFailure(dir, msgBitbucketLost, landed > 0,
			append(warnings, bitbucketProgress(landed, total)...)), false
	}
	answer, ok := parseBitbucketResponse(res.Stdout)
	if !ok {
		return bitbucketHTTP{}, postFailure(dir, msgBitbucketLost, landed > 0,
			append(warnings, bitbucketProgress(landed, total)...)), false
	}
	if !bitbucketOK(answer.status) {
		return bitbucketHTTP{}, postFailure(dir, bitbucketFailureReason(res, answer), landed > 0,
			append(warnings, bitbucketProgress(landed, total)...)), false
	}
	return answer, postAttempt{}, true
}

// bitbucketCommentWrite posts one comment document and decodes the
// created comment. A failure is partial once any earlier write landed,
// so a replay never duplicates the landed prefix.
func bitbucketCommentWrite(
	ctx context.Context, cmdr Commander, auth curlauth.Credential, url, body, dir string,
	warnings []string, landed, total int,
) (bitbucket.Comment, postAttempt, bool) {
	argv, cleanup, cerr := bitbucketMethodArgv(auth, methodPost, url)
	if cerr != nil {
		return bitbucket.Comment{}, postRefusal(dir, msgBitbucketConfig, warnings), false
	}
	defer cleanup()
	res, err := bitbucketCall(ctx, cmdr, argv, body)
	partial := landed > 0
	warnings = append(warnings, bitbucketProgress(landed, total)...)
	if err != nil {
		return bitbucket.Comment{}, postFailure(dir, msgBitbucketLost, partial, warnings), false
	}
	answer, ok := parseBitbucketResponse(res.Stdout)
	if !ok {
		return bitbucket.Comment{}, postFailure(dir, msgBitbucketLost, partial, warnings), false
	}
	if !bitbucketOK(answer.status) {
		return bitbucket.Comment{}, postFailure(dir, bitbucketFailureReason(res, answer), partial, warnings), false
	}
	created, decoded := bitbucket.DecodeComment(answer.body)
	if !decoded {
		return bitbucket.Comment{}, postFailure(dir, msgBitbucketPayload, partial, warnings), false
	}
	return created, postAttempt{}, true
}

// bitbucketOK reports a 2xx answer.
func bitbucketOK(status int) bool {
	return status >= bitbucketStatusOKMin && status <= bitbucketStatusOKMax
}

// bitbucketProgress names what a stopped replay already wrote, so a
// re-run is a known duplicate instead of a surprise.
func bitbucketProgress(landed, total int) []string {
	if landed == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"bitbucket post stopped after %d of %d anchored comment(s) — a re-run reposts them", landed, total)}
}

// bitbucketFailureReason prefers the host's own message, then its
// stderr, then the status line, so a refusal never quotes an empty
// reason.
func bitbucketFailureReason(res core.Result, answer bitbucketHTTP) string {
	if message := bitbucketMessage(answer.body); message != "" {
		return message
	}
	if res.Stderr != "" {
		return strings.TrimSpace(res.Stderr)
	}
	return "HTTP " + strconv.Itoa(answer.status)
}

// bitbucketMessage reads the `message` or `error` field of a Bitbucket
// error body.
func bitbucketMessage(body string) string {
	doc, err := jq.Decode([]byte(body))
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	if msg := jq.StrOf(m["message"]); msg != "" {
		return msg
	}
	return jq.StrOf(m["error"])
}

// parseBitbucketResponse splits one `curl -i` document at its first
// blank line and reads the status code out of the status line.
func parseBitbucketResponse(raw string) (bitbucketHTTP, bool) {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	head, body, _ := strings.Cut(text, "\n\n")
	fields := strings.Fields(strings.SplitN(head, "\n", 2)[0])
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "HTTP/") {
		return bitbucketHTTP{}, false
	}
	status, err := strconv.Atoi(fields[1])
	if err != nil || status < statusMin || status > statusMax {
		return bitbucketHTTP{}, false
	}
	return bitbucketHTTP{status: status, body: body}, true
}

// bitbucketMethodArgv is one write a Bitbucket post makes: an explicit
// method, both headers, and the JSON body on stdin. `-i` carries the
// status line and the host's own error body on stdout where `-f` would
// throw both away on 4xx. The credential comes from a 0600 file because
// stdin already carries the body.
func bitbucketMethodArgv(auth curlauth.Credential, method, url string) (argv []string, cleanup func(), err error) {
	argv, cleanup, err = auth.FileArgv(curlShowStatus, curlInclude, "-H", curlJSONHeader,
		curlRequest, method, curlDataBinary, curlStdin, url)
	if err != nil {
		return nil, nil, jq.Sealed("bitbucket credential file", err)
	}
	return append([]string{toolCurl}, argv...), cleanup, nil
}

// bitbucketReadArgv is one authenticated GET, in fetch's shape: curl
// reads the credential from stdin.
func bitbucketReadArgv(auth curlauth.Credential, url string) (argv []string, stdin string) {
	argv, stdin = auth.StdinArgv("-sf", url)
	return append([]string{toolCurl}, argv...), stdin
}

// bitbucketFollowArgv is one authenticated GET that follows the next
// URL's redirect.
func bitbucketFollowArgv(auth curlauth.Credential, url string) (argv []string, stdin string) {
	argv, stdin = auth.StdinArgv(bitbucketFlagFollow, url)
	return append([]string{toolCurl}, argv...), stdin
}

// bitbucketCall runs one Bitbucket write and seals a spawn failure; the
// caller reports the result's own stdout and stderr.
func bitbucketCall(ctx context.Context, cmdr Commander, argv []string, body string) (core.Result, error) {
	cmd := &core.Command{Argv: argv}
	if body != "" {
		cmd.Stdin = strings.NewReader(body)
	}
	res, err := cmdr.Run(ctx, cmd)
	if err != nil {
		return res, jq.Sealed("bitbucket call", err)
	}
	return res, nil
}

// bitbucketRead runs one authenticated GET and hands back the body with
// the host's stderr, so a caller that owns the recheck sidecar records
// the host's own words.
func bitbucketRead(ctx context.Context, cmdr Commander, auth curlauth.Credential, url string) (body, stderr string, err error) {
	if cmdr == nil {
		return "", errNoCommander.Error(), errNoCommander
	}
	argv, stdin := bitbucketReadArgv(auth, url)
	res, err := runReadRetry(ctx, cmdr, &core.Command{Argv: argv, Stdin: strings.NewReader(stdin)})
	if err != nil {
		return "", res.Stderr, err
	}
	return res.Stdout, res.Stderr, nil
}

// bitbucketFollowRead runs one authenticated GET that follows redirects.
func bitbucketFollowRead(ctx context.Context, cmdr Commander, auth curlauth.Credential, url string) (body, stderr string, err error) {
	if cmdr == nil {
		return "", errNoCommander.Error(), errNoCommander
	}
	argv, stdin := bitbucketFollowArgv(auth, url)
	res, err := runReadRetry(ctx, cmdr, &core.Command{Argv: argv, Stdin: strings.NewReader(stdin)})
	if err != nil {
		return "", res.Stderr, err
	}
	return res.Stdout, res.Stderr, nil
}

// bitbucketSelfLogin reads the login the authorship gate compares
// against.
func bitbucketSelfLogin(ctx context.Context, cmdr Commander, auth curlauth.Credential) (string, error) {
	raw, _, err := bitbucketRead(ctx, cmdr, auth, bitbucket.UserURL())
	if err != nil {
		return "", err
	}
	user, ok := bitbucket.DecodeUser(raw)
	if !ok || user.Login() == "" {
		return "", shapeError("bitbucket user read unreadable")
	}
	return user.Login(), nil
}

// bitbucketCommentList reads every comments page, following the
// server-supplied next URL while it stays on the API origin.
func bitbucketCommentList(ctx context.Context, cmdr Commander, auth curlauth.Credential, base string) ([]map[string]any, error) {
	var out []map[string]any
	url := bitbucket.CommentsURL(base)
	for range bitbucketMaxCommentPages {
		page, next, err := bitbucketCommentPage(ctx, cmdr, auth, url)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if next, err = bitbucketCommentNext(base, next); err != nil {
			return nil, err
		}
		if next == "" {
			return out, nil
		}
		url = next
	}
	return nil, shapeError("bitbucket comments pagination exceeded limit")
}

// bitbucketCommentPage reads one comments page: its values and its next
// URL.
func bitbucketCommentPage(
	ctx context.Context, cmdr Commander, auth curlauth.Credential, url string,
) ([]map[string]any, string, error) {
	raw, _, err := bitbucketFollowRead(ctx, cmdr, auth, url)
	if err != nil {
		return nil, "", err
	}
	return decodeBitbucketPage(raw)
}

// bitbucketCommentNext validates the next URL before following it.
func bitbucketCommentNext(base, next string) (string, error) {
	if next == "" || bitbucket.SameOrigin(base, next) {
		return next, nil
	}
	return "", shapeError("bitbucket next url off origin")
}

// decodeBitbucketPage reads one comments page: its values and its next
// URL.
func decodeBitbucketPage(raw string) ([]map[string]any, string, error) {
	values, ok := bitbucket.DecodeComments(raw)
	if !ok {
		return nil, "", shapeError("bitbucket comments page unreadable")
	}
	return values, bitbucketPageNext(raw), nil
}

// bitbucketPageNext reads the next URL of one comments page.
func bitbucketPageNext(raw string) string {
	var page struct {
		Next string `json:"next"`
	}
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		return ""
	}
	return page.Next
}

// liveSHA reads the PR's current head from the same payload fetch
// records, so the drift check costs one read and no new logic.
func (bitbucketHost) liveSHA(ctx context.Context, cmdr Commander, env map[string]string, repo, pr string) (string, error) {
	auth, base, ref := bitbucket.Resolve(env, repo, pr)
	if ref != nil {
		return "", bitbucketRefusalOf(ref)
	}
	raw, _, err := bitbucketRead(ctx, cmdr, auth, base)
	if err != nil {
		return "", err
	}
	return bitbucket.DecodePull(raw).Source.Commit.Hash, nil
}

// selfLogin reads the token user's login, which the authorship gate
// compares against the bundle author.
func (bitbucketHost) selfLogin(ctx context.Context, cmdr Commander, env map[string]string) (string, error) {
	auth, _, ref := bitbucket.APIBase(env)
	if ref != nil {
		return "", bitbucketRefusalOf(ref)
	}
	return bitbucketSelfLogin(ctx, cmdr, auth)
}

// recheck counts the unresolved inline threads on the pull request: an
// inline comment whose root carries no resolution is one open thread. A
// read that fails leaves the count unknown instead of short, so an
// approval is never certified on half a read.
func (bitbucketHost) recheck(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr, dir string,
) (int, string, error) {
	auth, base, ref := bitbucket.Resolve(env, repo, pr)
	var (
		open   int
		stderr string
		err    error
	)
	switch {
	case ref != nil:
		err, stderr = bitbucketRefusalOf(ref), ref.Message
	case cmdr == nil:
		err, stderr = errNoCommander, errNoCommander.Error()
	default:
		open, err = countBitbucketOpen(ctx, cmdr, auth, base)
		if err != nil {
			stderr = err.Error()
		}
	}
	if werr := atomic.WriteErrSidecar(dir+recheckErrSuffix, []byte(stderr)); werr != nil {
		return 0, "", jq.Sealed("write .recheck.err", werr)
	}
	if err != nil {
		return 0, firstReason(stderr), err
	}
	return open, "", nil
}

// countBitbucketOpen counts the unresolved inline threads. A general
// comment carries no anchor and is never a thread: verdict's own summary
// comment must not read as an unresolved thread and block an approval.
func countBitbucketOpen(ctx context.Context, cmdr Commander, auth curlauth.Credential, base string) (int, error) {
	items, err := bitbucketCommentList(ctx, cmdr, auth, base)
	if err != nil {
		return 0, err
	}
	open := 0
	for _, root := range bitbucketThreadRoots(items) {
		if !bitbucket.IsInline(root) {
			continue
		}
		if !bitbucketCommentResolved(root) {
			open++
		}
	}
	return open, nil
}

// bitbucketThreadRoots returns the root comment of every thread.
func bitbucketThreadRoots(items []map[string]any) []map[string]any {
	byID := make(map[string]map[string]any, len(items))
	kept := make([]map[string]any, 0, len(items))
	for _, m := range items {
		if bitbucketCommentSkipped(m) {
			continue
		}
		kept = append(kept, m)
		byID[bitbucket.CommentID(m)] = m
	}
	var roots []map[string]any
	for _, m := range kept {
		if bitbucket.CommentID(bitbucketRootOf(m, byID)) == bitbucket.CommentID(m) {
			roots = append(roots, m)
		}
	}
	return roots
}

// bitbucketCommentSkipped reports a deleted or unsubmitted comment: a
// draft never counts as a thread.
func bitbucketCommentSkipped(m map[string]any) bool {
	if deleted, ok := m["deleted"].(bool); ok && deleted {
		return true
	}
	pending, ok := m["pending"].(bool)
	return ok && pending
}

// bitbucketRootOf walks a comment's parent chain to its root.
func bitbucketRootOf(m map[string]any, byID map[string]map[string]any) map[string]any {
	current := m
	for range len(byID) + 1 {
		next, ok := bitbucketParentOf(current, byID)
		if !ok {
			return current
		}
		current = next
	}
	return current
}

// bitbucketParentOf reads one comment's parent row, reporting false when
// the comment has none or its parent never appeared in the list.
func bitbucketParentOf(m map[string]any, byID map[string]map[string]any) (map[string]any, bool) {
	parent, ok := m["parent"].(map[string]any)
	if !ok || parent == nil {
		return nil, false
	}
	id, ok := parent[fieldID].(float64)
	if !ok {
		return nil, false
	}
	next, ok := byID[strconv.Itoa(int(id))]
	return next, ok
}

// bitbucketCommentResolved reports whether a root comment carries a
// resolution: resolution lives on the root, so a missing or null
// resolution is an open thread.
func bitbucketCommentResolved(m map[string]any) bool {
	return m["resolution"] != nil
}

// publication reads one paged comment list and splits it into the
// duplicate-check reviews (general comments, whose bodies carry the
// payload marker) and the inline comments the missing check pairs
// against. An inline anchor carries no host head, so a non-outdated
// anchor reads on the live head — which is the reviewed head on a
// same-head repost — while an outdated anchor is stale and stays
// unpaired so the repost lands instead of reading as duplicate.
func (bitbucketHost) publication(ctx context.Context, cmdr Commander, env map[string]string, repo, pr string) (remotePublication, error) {
	auth, base, ref := bitbucket.Resolve(env, repo, pr)
	if ref != nil {
		return remotePublication{}, bitbucketRefusalOf(ref)
	}
	raw, _, err := bitbucketRead(ctx, cmdr, auth, base)
	if err != nil {
		return remotePublication{}, err
	}
	head := bitbucket.DecodePull(raw).Source.Commit.Hash
	items, err := bitbucketCommentList(ctx, cmdr, auth, base)
	if err != nil {
		return remotePublication{}, err
	}
	return bitbucketPublication(items, head), nil
}

// bitbucketPublication splits one comment list into reviews and remote
// comments in the shapes the shared duplicate and missing checks read.
func bitbucketPublication(items []map[string]any, head string) remotePublication {
	var remote remotePublication
	for _, m := range items {
		bitbucketPublicationItem(&remote, m, head)
	}
	return remote
}

// bitbucketPublicationItem routes one comment row to its side of the
// publication.
func bitbucketPublicationItem(remote *remotePublication, m map[string]any, head string) {
	if bitbucketCommentSkipped(m) {
		return
	}
	if bitbucket.IsInline(m) {
		if translated, ok := translateBitbucketInline(m, head); ok {
			remote.comments = append(remote.comments, translated)
		}
		return
	}
	remote.reviews = append(remote.reviews, bitbucketReviewOf(m))
}

// translateBitbucketInline maps one Bitbucket inline row onto the
// remote-comment shape the missing check pairs: a new-side `to` or an
// old-side `from` becomes path, side, and line, and the body stays the
// raw content. An outdated anchor stays unpaired.
func translateBitbucketInline(m map[string]any, head string) (map[string]any, bool) {
	inline, ok := m["inline"].(map[string]any)
	if !ok || inline == nil || bitbucketInlineOutdated(inline) {
		return nil, false
	}
	path, side, line, ok := bitbucketInlinePlaceable(inline, bitbucket.CommentBody(m))
	if !ok {
		return nil, false
	}
	translated := map[string]any{
		fieldPath: path, fieldSide: side, fieldLine: line,
		fieldBody: bitbucket.CommentBody(m), "commit_id": head,
		fieldID: m[fieldID],
	}
	if url := bitbucketCommentURL(m); url != "" {
		translated["html_url"] = url
	}
	return translated, true
}

// bitbucketInlineOutdated reports a stale anchor: the host moved it off
// its line, so it stays unpaired for the repost.
func bitbucketInlineOutdated(inline map[string]any) bool {
	outdated, ok := inline["outdated"].(bool)
	return ok && outdated
}

// bitbucketInlinePlaceable reads the anchor's place on the file: a path,
// a side, and a 1-based line, or unplaceable.
func bitbucketInlinePlaceable(inline map[string]any, body string) (string, string, int, bool) {
	path, ok := inline["path"].(string)
	if !ok || path == "" || body == "" {
		return "", "", 0, false
	}
	side, line := bitbucketInlineSide(inline)
	if line <= 0 {
		return "", "", 0, false
	}
	return path, side, line, true
}

// bitbucketInlineSide reads the anchored line and its side: a new-side
// `to` or an old-side `from`.
func bitbucketInlineSide(inline map[string]any) (string, int) {
	if to, ok := inline["to"].(float64); ok && to > 0 {
		return "RIGHT", int(to)
	}
	from, ok := inline["from"].(float64)
	if !ok {
		return "LEFT", 0
	}
	return "LEFT", int(from)
}

// bitbucketReviewOf maps one general comment onto the live-review
// shape: the body carries the payload marker, and the marker head is
// the reviewed head.
func bitbucketReviewOf(m map[string]any) liveReview {
	body := bitbucket.CommentBody(m)
	_, head, _ := reviewMarker(body)
	return liveReview{
		body: body, commitID: head,
		submittedAt: strOf(m["created_on"]), state: bitbucketStateCommented,
	}
}

// bitbucketCommentURL reads the comment's page URL.
func bitbucketCommentURL(m map[string]any) string {
	links, ok := m["links"].(map[string]any)
	if !ok || links == nil {
		return ""
	}
	html, ok := links["html"].(map[string]any)
	if !ok || html == nil {
		return ""
	}
	href, ok := html["href"].(string)
	if !ok {
		return ""
	}
	return href
}

// strOf reads one string field, reporting "" when absent.
func strOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// bitbucketReceiptObject is the accepted review in the receipt shape
// post.json carries, matching what every host records: the summary
// comment id, the verdict state, and the pull-request page.
func bitbucketReceiptObject(pr *bitbucketPR, summary *bitbucket.Comment, approved bool) string {
	state := bitbucketStateCommented
	if approved {
		state = "approved"
	}
	id := summary.RemoteID
	html := pr.htmlURL
	if summary.Links.HTML.Href != "" && html == "" {
		html = summary.Links.HTML.Href
	}
	obj := jq.NewObj(
		fieldID, id,
		"state", state,
		"html_url", html,
		"submitted_at", summary.Created,
	)
	return strings.TrimRight(jq.PrettyOrdered(obj), newline)
}

func (bitbucketHost) listSummaryComments(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr string,
) ([]summaryRemoteComment, error) {
	auth, base, ref := bitbucket.Resolve(env, repo, pr)
	if ref != nil {
		return nil, bitbucketRefusalOf(ref)
	}
	items, err := bitbucketCommentList(ctx, cmdr, auth, base)
	if err != nil {
		return nil, err
	}
	var out []summaryRemoteComment
	for _, m := range items {
		if bitbucket.IsInline(m) {
			continue
		}
		if c, ok := decodeBitbucketSummary(m); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (bitbucketHost) createSummaryComment(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr, body string,
) (summaryRemoteComment, error) {
	auth, base, ref := bitbucket.Resolve(env, repo, pr)
	if ref != nil {
		return summaryRemoteComment{}, bitbucketRefusalOf(ref)
	}
	return bitbucketSummaryWrite(ctx, cmdr, auth, methodPost, base+bitbucket.CommentsPath, body)
}

func (bitbucketHost) updateSummaryComment(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr, commentID, body string,
) (summaryRemoteComment, error) {
	auth, base, ref := bitbucket.Resolve(env, repo, pr)
	if ref != nil {
		return summaryRemoteComment{}, bitbucketRefusalOf(ref)
	}
	return bitbucketSummaryWrite(ctx, cmdr, auth, methodPut, base+bitbucket.CommentsPath+"/"+commentID, body)
}

// decodeBitbucketSummary translates one general comment row into the
// summary shape pickSummaryTarget reads.
func decodeBitbucketSummary(m map[string]any) (summaryRemoteComment, bool) {
	id := bitbucket.CommentID(m)
	if id == "" {
		return summaryRemoteComment{}, false
	}
	return summaryRemoteComment{
		ID:        id,
		Author:    bitbucket.CommentAuthor(m),
		Body:      bitbucket.CommentBody(m),
		URL:       bitbucketCommentURL(m),
		CreatedAt: strOf(m["created_on"]),
		UpdatedAt: strOf(m["updated_on"]),
	}, true
}

func bitbucketSummaryWrite(ctx context.Context, cmdr Commander, auth curlauth.Credential,
	method, url, body string,
) (summaryRemoteComment, error) {
	if strings.TrimSpace(body) == "" {
		return summaryRemoteComment{}, shapeError(errEmptySummary)
	}
	if cmdr == nil {
		return summaryRemoteComment{}, errNoCommander
	}
	argv, cleanup, err := bitbucketMethodArgv(auth, method, url)
	if err != nil {
		return summaryRemoteComment{}, err
	}
	defer cleanup()
	res, callErr := bitbucketCall(ctx, cmdr, argv, jq.NewObj("content", jq.NewObj("raw", body)).MarshalPretty())
	if callErr != nil {
		return summaryRemoteComment{}, callErr
	}
	answer, ok := parseBitbucketResponse(res.Stdout)
	if !ok {
		return summaryRemoteComment{}, shapeError(msgBitbucketLost)
	}
	if !bitbucketOK(answer.status) {
		return summaryRemoteComment{}, shapeError(bitbucketFailureReason(res, answer))
	}
	created, decoded := bitbucket.DecodeComment(answer.body)
	if !decoded {
		return summaryRemoteComment{}, shapeError(msgBitbucketPayload)
	}
	return summaryRemoteComment{
		ID:        strconv.Itoa(created.RemoteID),
		Author:    "",
		Body:      created.Content.Raw,
		URL:       created.Links.HTML.Href,
		CreatedAt: created.Created,
		UpdatedAt: created.Updated,
	}, nil
}

func (bitbucketHost) postedInlineURLs(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr string, _ *jq.Obj,
) ([]summaryInlineLink, error) {
	auth, base, ref := bitbucket.Resolve(env, repo, pr)
	if ref != nil {
		return nil, bitbucketRefusalOf(ref)
	}
	items, err := bitbucketCommentList(ctx, cmdr, auth, base)
	if err != nil {
		return nil, err
	}
	_, publication := items, bitbucketPublication(items, "")
	var out []summaryInlineLink
	for _, m := range publication.comments {
		out = append(out, summaryInlineLink{
			Path: strOf(m[fieldPath]),
			Body: strOf(m["body"]),
			URL:  strOf(m["html_url"]),
			Line: intFromAny(m[fieldLine]),
		})
	}
	return out, nil
}

// parseBitbucketPayload reads the hosted payload and applies the v1
// anchor rules: a finding with no placeable anchor cannot be placed and
// is withheld, a LEFT anchor on a file the change deletes is withheld,
// and a multi-line range is flattened to its end line. Every loss is
// counted into an envelope warning, so a shorter review never lands
// silently. newSide is the set of paths the change leaves at head; the
// caller reads it first and refuses an unreadable bundle hunk index
// before any write, rather than withholding every LEFT anchor as a
// deleted file.
func parseBitbucketPayload(payload string, newSide map[string]bool) (bitbucketPostPayload, bool) {
	doc, err := jq.DecodeOrdered([]byte(payload))
	if err != nil {
		return bitbucketPostPayload{}, false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok || obj == nil {
		return bitbucketPostPayload{}, false
	}
	rawEvent := orderedString(obj, "event")
	part := bitbucketPostPayload{
		body:     orderedString(obj, fieldBody),
		rawEvent: rawEvent,
		commitID: orderedString(obj, "commit_id"),
	}
	switch rawEvent {
	case eventApprove, eventRequestChanges, eventComment:
		part.event = rawEvent
	default:
		part.event = ""
	}
	comments, _ := obj.Get(fieldComments)
	kept, losses := bitbucketKeptComments(jq.Elements(comments), newSide)
	part.comments = kept
	part.warnings = bitbucketAnchorWarnings(losses)
	return part, true
}

// bitbucketLosses is what the anchor rules withheld from one payload,
// counted by rule.
type bitbucketLosses struct {
	dropped int
	deleted int
	multi   int
}

// bitbucketClass is what the anchor rules decide about one payload
// comment.
type bitbucketClass int

const (
	bitbucketKeep bitbucketClass = iota
	bitbucketDrop
	bitbucketDelete
)

// bitbucketKeptComments applies the v1 anchor rules in order: a finding
// with no placeable anchor cannot be placed at all, a LEFT anchor on a
// file the change deletes cannot resolve, and a multi-line range is
// flattened to its end line by the caller that writes the anchor.
func bitbucketKeptComments(raw []any, newSide map[string]bool) ([]bitbucketComment, bitbucketLosses) {
	kept := make([]bitbucketComment, 0, len(raw))
	var losses bitbucketLosses
	for _, el := range raw {
		comment, class := classifyBitbucketComment(el, newSide)
		switch class {
		case bitbucketDrop:
			losses.dropped++
		case bitbucketDelete:
			losses.deleted++
		default:
			if comment.start > 0 && comment.start != comment.line {
				losses.multi++
			}
			kept = append(kept, comment)
		}
	}
	return kept, losses
}

// classifyBitbucketComment decides one payload comment: no placeable
// anchor is dropped, a LEFT anchor on a path with no new-side row is a
// deleted file, and everything else keeps the line the merge already
// validated.
func classifyBitbucketComment(raw any, newSide map[string]bool) (bitbucketComment, bitbucketClass) {
	comment, ok := bitbucketCommentOf(raw)
	switch {
	case !ok:
		return bitbucketComment{}, bitbucketDrop
	case comment.left && !newSide[comment.path]:
		return bitbucketComment{}, bitbucketDelete
	default:
		return comment, bitbucketKeep
	}
}

// bitbucketCommentOf reduces one payload comment, reporting false when
// the anchor cannot be placed: Bitbucket needs a path, a body, and a
// 1-based line.
func bitbucketCommentOf(raw any) (bitbucketComment, bool) {
	m, ok := raw.(*jq.Obj)
	if !ok || m == nil {
		return bitbucketComment{}, false
	}
	comment := bitbucketComment{
		path:  bitbucketPath(orderedString(m, fieldPath)),
		body:  orderedString(m, fieldBody),
		left:  strings.EqualFold(orderedString(m, "side"), bitbucketSideLeft),
		line:  orderedInt(m, fieldLine),
		start: orderedInt(m, fieldStartLine),
	}
	if comment.path == "" || comment.body == "" || comment.line <= 0 {
		return bitbucketComment{}, false
	}
	return comment, true
}

// bitbucketPath drops the diff's a/ or b/ prefix: Bitbucket matches a
// comment path against the tree, where the prefix never appears.
func bitbucketPath(path string) string {
	for _, prefix := range []string{"a/", "b/"} {
		if rest, ok := strings.CutPrefix(path, prefix); ok {
			return rest
		}
	}
	return path
}

// bitbucketNewSidePaths is the set of paths the change leaves at head,
// read from the bundle's hunk index: a path with no NEW row exists only
// on the old side. An unreadable index is an error, never an empty set:
// an empty set would withhold every LEFT anchor as a deleted file.
func bitbucketNewSidePaths(dir string) (map[string]bool, error) {
	hs, err := hunks.Load(dir)
	if err != nil {
		return nil, jq.Sealed("read bitbucket hunk index", err)
	}
	paths := make(map[string]bool, len(hs))
	for _, h := range hs {
		if h.Side == hunkSideNew {
			paths[h.File] = true
		}
	}
	return paths, nil
}

// bitbucketAnchorWarnings states the anchor losses this payload takes,
// one sentence per rule that fired.
func bitbucketAnchorWarnings(losses bitbucketLosses) []string {
	var warnings []string
	for _, loss := range []struct {
		message string
		n       int
	}{
		{message: msgBitbucketDropped, n: losses.dropped},
		{message: msgBitbucketDeleted, n: losses.deleted},
		{message: msgBitbucketMulti, n: losses.multi},
	} {
		if loss.n > 0 {
			warnings = append(warnings, fmt.Sprintf(loss.message, loss.n))
		}
	}
	return warnings
}
