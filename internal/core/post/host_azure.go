package postcore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/azure"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// Azure DevOps has no review CLI of its own, and verdict never reads its
// credential, so every call is one `az rest` invocation: `az login` holds the
// identity and `--resource` names the Azure DevOps Entra application, which
// is the token `az` buys and sends to the organization's endpoints. The PAT
// `AZURE_DEVOPS_EXT_PAT` is not read here — the `az devops` extension reads
// it, core `az rest` does not — so no token ever reaches an argv or an error
// sentence.
//
// One write primitive exists — create a thread — and the review reuses it
// twice: one inline thread per finding (a `threadContext` with a side and a
// line), then the general thread that carries the review body. Azure pins a
// thread to the pull request, not to a commit, so the head read before the
// first write is the only thing standing between a stale review and an
// anchor on code it never covered; a moved head refuses instead of posting.
const (
	// toolAz is the CLI every Azure read and write goes through.
	toolAz = "az"

	azRestCmd      = "rest"
	azFlagURL      = "--url"
	azFlagBody     = "--body"
	azFlagHeader   = "--headers"
	azFlagResource = "--resource"

	// azResourceDevOps is the Azure DevOps Entra application id a token is
	// bought for. `az` cannot derive a resource from a `dev.azure.com` URL,
	// which is not an Azure cloud endpoint, so every call states it: without
	// it `az rest` refuses to acquire a token at all.
	azResourceDevOps = "499b84ac-1321-427f-aa17-267ca6975798"

	azMethodGet   = "get"
	azMethodPost  = "post"
	azMethodPatch = "patch"

	// azBodyFilePrefix marks the one argv element that names the staged body
	// file, the `@file` convention every `az` argument accepts.
	azBodyFilePrefix = "@"

	// azJSONHeader is the content type a write states; a JSON body would
	// default to it, and stating it keeps the contract visible in the argv.
	azJSONHeader = "Content-Type=application/json"

	// azureBodyFileMode is the one permission the staged body file gets: the
	// owner reads it, nobody else does.
	azureBodyFileMode = 0o600

	// azureSideLeft is the payload's old-side marker (`side: "LEFT"`).
	azureSideLeft = "LEFT"

	// azurePathPrefix is the slash Azure requires in front of a filePath.
	azurePathPrefix = "/"

	// azureCommentParentRoot is how a comment states it opens a thread.
	azureCommentParentRoot = 0

	// azureCommentTypeText is the comment type a review comment carries.
	azureCommentTypeText = 1

	// azureThreadActive is the thread status a new thread is created with.
	azureThreadActive = 1

	// azurePositionOffset is the character offset a thread anchors at: the
	// first character of the line.
	azurePositionOffset = 1

	// azureSummaryIDJoin separates the thread id from the comment id in one
	// summary comment handle. Azure restarts comment ids at 1 on every thread,
	// so a patch needs both numbers.
	azureSummaryIDJoin = "/"

	// azureStateCommented is the receipt state an Azure post records: Azure
	// takes no review event, so every write reads as a comment.
	azureStateCommented = "commented"
)

// Azure-specific refusals. A reason recorded through postFailure reads
// inside msgPostFailed; a sentence recorded through postRefusal is the whole
// message.
const (
	msgAzurePayload = "azure post payload unreadable"
	msgAzureLost    = "azure did not answer — the review may exist; check the PR before re-running"
	msgAzureNoHead  = "post failed: azure could not read the pull request head — nothing was written"
	msgAzureStale   = "post failed: the pull request head moved since fetch (review covers %s, live HEAD is %s) — " +
		"an azure thread carries no commit pin, so it would comment on code this review never covered; " +
		"re-fetch and re-run"
	msgAzureNoCommit = "post failed: the payload carries no commit_id — azure would anchor on the " +
		"current head instead of the reviewed one; re-fetch to record the head"
	msgAzureNoHunks = "post failed: azure could not read the bundle hunk index — nothing was written"
	msgAzureDropped = "azure anchors need a path, a body, and a numeric line — %d finding(s) withheld from the review"
	msgAzureSide    = "azure cannot anchor a comment on the side the change does not have for that file — " +
		"%d finding(s) withheld at submit"
	msgAzureMulti = "azure anchors are single-line — %d multi-line anchor(s) anchored at their end line"
	msgAzureBody  = "post failed: azure could not stage the request body file — nothing was written"
	msgAzureEvent = "post failed: azure has no review event named %q — post a comment or a request " +
		"for changes; azure takes no approval write"
	msgAzureSummaryID = "azure summary comment handle %q is not thread/comment — refusing to patch an " +
		"unaddressed comment"
)

// hunkSideNew and hunkSideOld are the hunk index's side names, shared by
// every host that reads the index to tell an added file from a deleted one.
const (
	hunkSideNew = "NEW"
	hunkSideOld = "OLD"
)

// azureHost is the `az rest` host for Azure DevOps.
type azureHost struct{}

func (azureHost) tool() string { return toolAz }

// auth gates the organization URL before the first call. `az` itself carries
// the credential, so no other variable is required.
func (azureHost) auth(env map[string]string) *core.RefusalError {
	return azure.Auth(env)
}

// azureRefusalOf re-states the shared package's refusal as this package's
// own, so the published code and the operator sentence cross the seam
// unchanged and stay inspectable without a wrap.
func azureRefusalOf(ref *core.RefusalError) *core.RefusalError {
	return &core.RefusalError{Code: ref.Code, Message: core.Msgf("%s", core.Trusted(ref.Message))}
}

// azurePostPayload is the part of the hosted payload an Azure write reads:
// the summary body, the binding event, the reviewed head, and the inline
// findings that survive the anchor rules.
type azurePostPayload struct {
	body     string
	event    string
	rawEvent string
	commitID string
	comments []azureComment
	warnings []string
}

// azureComment is one finding reduced to what an Azure thread needs: a path,
// a body, and the line on one side.
type azureComment struct {
	path  string
	body  string
	left  bool
	line  int
	start int
}

// azurePull is the slice of one PR read a post needs: the live head and the
// page the receipt records.
type azurePull struct {
	head    string
	htmlURL string
}

// post replays one payload: the head guard, one inline thread per finding,
// then the general thread that carries the review body. Azure has no atomic
// review and no approval write, so a mid-way failure names what landed and
// is never replayed.
func (azureHost) post(ctx context.Context, cmdr Commander, env map[string]string,
	_ core.HostCapabilities, repo, pr, dir, payload string,
) postAttempt {
	target, ref := azure.Resolve(env, repo, pr)
	if ref != nil {
		return postRefusal(dir, ref.Message, nil)
	}
	sides, herr := azureSideIndex(dir)
	if herr != nil {
		return postRefusal(dir, msgAzureNoHunks, nil)
	}
	part, ok := parseAzurePayload(payload, sides)
	if !ok {
		return postFailure(dir, msgAzurePayload, false, nil)
	}
	live, attempt, ok := azurePostHead(ctx, cmdr, target, &part, dir)
	if !ok {
		return attempt
	}
	landed, links, attempt, ok := azureInlineThreads(ctx, cmdr, target, &part, dir)
	if !ok {
		return attempt
	}
	summary, attempt, ok := azureSummaryLeg(ctx, cmdr, target, &part, landed, dir)
	if !ok {
		return attempt
	}
	resAttempt := postAccepted(dir, azureReceiptObject(&live, &summary), "", part.warnings)
	resAttempt.inlineLinks = links
	return resAttempt
}

// azureCreatedThread is one created thread a write reads back: the thread
// id, its opening comment, and the timestamps a receipt records.
type azureCreatedThread struct {
	id        string
	commentID string
	body      string
	published string
	updated   string
}

// azureReadArgv is one authenticated read: the token is bought for the Azure
// DevOps resource and sent to the one URL verdict built.
func azureReadArgv(url string) []string {
	return []string{
		toolAz, azRestCmd, "--method", azMethodGet,
		azFlagResource, azResourceDevOps, azFlagURL, url,
	}
}

// azureBodyArgv stages the JSON body in a private 0600 file and returns the
// one write argv plus its cleanup. The body never rides the argv: it is
// review text, it can be large, and a file keeps it out of process listings
// and logs.
func azureBodyArgv(method, url, body string) (argv []string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "verdict-azure-")
	if err != nil {
		return nil, nil, jq.Sealed("azure body dir", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "body.json")
	if werr := os.WriteFile(path, []byte(body), azureBodyFileMode); werr != nil {
		cleanup()
		return nil, nil, jq.Sealed("azure body file", werr)
	}
	return []string{
		toolAz, azRestCmd, "--method", method, azFlagResource, azResourceDevOps, azFlagURL, url,
		azFlagHeader, azJSONHeader, azFlagBody, azBodyFilePrefix + path,
	}, cleanup, nil
}

// azureCall runs one `az rest` call and seals a spawn failure; the caller
// reports the result's own stdout and stderr.
func azureCall(ctx context.Context, cmdr Commander, argv []string) (core.Result, error) {
	if cmdr == nil {
		return core.Result{}, errNoCommander
	}
	res, err := cmdr.Run(ctx, &core.Command{Argv: argv})
	if err != nil {
		return res, jq.Sealed("azure call", err)
	}
	return res, nil
}

// azureWrite posts or patches one body and hands back the answer.
func azureWrite(ctx context.Context, cmdr Commander, method, url, body string) (core.Result, error) {
	argv, cleanup, err := azureBodyArgv(method, url, body)
	if err != nil {
		return core.Result{}, err
	}
	defer cleanup()
	return azureCall(ctx, cmdr, argv)
}

// azureRead runs one authenticated GET and hands back the body with the
// host's stderr, so a caller that owns a sidecar records the host's own
// words.
func azureRead(ctx context.Context, cmdr Commander, url string) (body, stderr string, err error) {
	if cmdr == nil {
		return "", errNoCommander.Error(), errNoCommander
	}
	res, err := runReadRetry(ctx, cmdr, &core.Command{Argv: azureReadArgv(url)})
	if err != nil {
		return "", res.Stderr, err
	}
	return res.Stdout, res.Stderr, nil
}

// azureFailureReason prefers the host's own message and falls back to its
// stderr, so a refusal never quotes an empty reason.
func azureFailureReason(res core.Result) string {
	if detail := firstReason(res.Stderr); detail != "" {
		return detail
	}
	if message := azureErrorMessage(res.Stdout); message != "" {
		return message
	}
	return "az exited " + strconv.Itoa(res.ExitCode)
}

// azureErrorMessage reads the `message` field Azure puts in an error body.
func azureErrorMessage(body string) string {
	doc, err := jq.Decode([]byte(body))
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	return jq.StrOf(m["message"])
}

// azurePostHead runs the pre-write guards: the reviewed commit, a known
// event, and a live head that still matches it.
func azurePostHead(
	ctx context.Context, cmdr Commander, target azure.Target,
	part *azurePostPayload, dir string,
) (azurePull, postAttempt, bool) {
	warnings := part.warnings
	if part.commitID == "" {
		return azurePull{}, postRefusal(dir, msgAzureNoCommit, warnings), false
	}
	if part.event == "" {
		return azurePull{}, postRefusal(dir, fmt.Sprintf(msgAzureEvent, part.rawEvent), warnings), false
	}
	live, attempt, ok := azurePullRead(ctx, cmdr, target, dir, warnings)
	if !ok {
		return azurePull{}, attempt, false
	}
	if live.head != part.commitID {
		stale := fmt.Sprintf(msgAzureStale, shortOrUnknown(part.commitID), shortOrUnknown(live.head))
		return azurePull{}, postRefusal(dir, stale, warnings), false
	}
	return live, postAttempt{}, true
}

// azurePullRead reads the live PR once and hands back the head with the page
// URL, so a caller that owns the .post.err sidecar can record the host's own
// words.
func azurePullRead(
	ctx context.Context, cmdr Commander, target azure.Target,
	dir string, warnings []string,
) (azurePull, postAttempt, bool) {
	raw, stderr, err := azureRead(ctx, cmdr, target.PullURL())
	if err != nil {
		reason := msgAzureNoHead
		if detail := firstReason(stderr); detail != "" {
			reason += ": " + detail
		}
		return azurePull{}, postFailure(dir, reason, false, warnings), false
	}
	pull := azure.DecodePull(raw).Head()
	if pull == "" {
		return azurePull{}, postFailure(dir, msgAzureNoHead, false, warnings), false
	}
	return azurePull{head: pull, htmlURL: target.WebURL()}, postAttempt{}, true
}

// azureInlineThreads posts one anchored thread per finding in payload order
// and stops at the first failure; the caller's retry policy reads the landed
// count instead of replaying it.
func azureInlineThreads(
	ctx context.Context, cmdr Commander, target azure.Target,
	part *azurePostPayload, dir string,
) (int, []summaryInlineLink, postAttempt, bool) {
	links := make([]summaryInlineLink, 0, len(part.comments))
	warnings := part.warnings
	for k, c := range part.comments {
		created, attempt, ok := azureThreadWrite(ctx, cmdr, target.ThreadsURL(), azureInlineThreadBody(c),
			dir, warnings, k, len(part.comments))
		if !ok {
			return k, nil, attempt, false
		}
		links = append(links, azureInlineLink(target, c, &created))
	}
	return len(part.comments), links, postAttempt{}, true
}

// azureThreadWrite posts one thread document and decodes the created thread.
// A failure is partial once any earlier write landed, so a replay never
// duplicates the landed prefix.
func azureThreadWrite(
	ctx context.Context, cmdr Commander, url, body, dir string,
	warnings []string, landed, total int,
) (azureCreatedThread, postAttempt, bool) {
	partial := landed > 0
	// slices.Clip caps the caller's slice, so this append allocates a fresh
	// array: the progress line never lands in a backing array the payload's
	// warnings still share.
	warnings = append(slices.Clip(warnings), azureProgress(landed, total)...)
	argv, cleanup, cerr := azureBodyArgv(azMethodPost, url, body)
	if cerr != nil {
		return azureCreatedThread{}, postRefusal(dir, msgAzureBody, warnings), false
	}
	defer cleanup()
	res, err := azureCall(ctx, cmdr, argv)
	if err != nil {
		return azureCreatedThread{}, postFailure(dir, msgAzureLost, partial, warnings), false
	}
	if res.ExitCode != 0 {
		return azureCreatedThread{}, postFailure(dir, azureFailureReason(res), partial, warnings), false
	}
	created, ok := decodeAzureCreatedThread(res.Stdout)
	if !ok {
		return azureCreatedThread{}, postFailure(dir, msgAzurePayload, partial, warnings), false
	}
	return created, postAttempt{}, true
}

// azureProgress names what a stopped replay already wrote, so a re-run is a
// known duplicate instead of a surprise.
func azureProgress(landed, total int) []string {
	if landed == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"azure post stopped after %d of %d anchored thread(s) — a re-run reposts them", landed, total)}
}

// decodeAzureCreatedThread reads one created thread back into the handle a
// summary patch and the receipt need.
func decodeAzureCreatedThread(raw string) (azureCreatedThread, bool) {
	created, decoded := azure.DecodeCreated(raw)
	if !decoded {
		return azureCreatedThread{}, false
	}
	return azureCreatedThread{
		id:        strconv.Itoa(created.ID),
		commentID: strconv.Itoa(created.FirstCommentID()),
		body:      created.FirstCommentBody(),
		published: created.PublishedDate, updated: created.LastUpdatedDate,
	}, true
}

// azureInlineThreadBody renders one thread comment anchored on a file line:
// the right side for a new line, the left side for a deleted one, never
// both.
func azureInlineThreadBody(c azureComment) string {
	start := jq.NewObj("line", c.line, "offset", azurePositionOffset)
	end := jq.NewObj("line", c.line, "offset", azurePositionOffset)
	threadContext := jq.NewObj("filePath", azurePathPrefix+strings.TrimPrefix(c.path, azurePathPrefix))
	if c.left {
		threadContext.Set("leftFileStart", start)
		threadContext.Set("leftFileEnd", end)
	} else {
		threadContext.Set("rightFileStart", start)
		threadContext.Set("rightFileEnd", end)
	}
	return jq.NewObj(
		"comments", []any{azureThreadComment(c.body)},
		"status", azureThreadActive,
		"threadContext", threadContext,
	).MarshalPretty()
}

// azureThreadComment renders the opening comment of one thread.
func azureThreadComment(body string) *jq.Obj {
	return jq.NewObj(
		"parentCommentId", azureCommentParentRoot,
		"content", body,
		"commentType", azureCommentTypeText,
	)
}

// azureSummaryThreadBody renders the general thread that carries the review
// body: no threadContext, so the host reads it as a pull-request comment.
func azureSummaryThreadBody(body string) string {
	return jq.NewObj(
		"comments", []any{azureThreadComment(body)},
		"status", azureThreadActive,
	).MarshalPretty()
}

// azureInlineLink reads the created thread back into the summary links.
func azureInlineLink(target azure.Target, c azureComment, created *azureCreatedThread) summaryInlineLink {
	return summaryInlineLink{
		Path: c.path, Line: c.line, Body: c.body,
		URL: target.ThreadWebURL(created.id),
	}
}

// azureSummaryLeg posts the general thread that carries the review body,
// after the inline threads landed. An empty body posts nothing: a follow-up
// part carries its own header and no marker to restore.
func azureSummaryLeg(
	ctx context.Context, cmdr Commander, target azure.Target,
	part *azurePostPayload, posted int, dir string,
) (azureCreatedThread, postAttempt, bool) {
	if strings.TrimSpace(part.body) == "" {
		return azureCreatedThread{}, postAttempt{}, true
	}
	created, attempt, ok := azureThreadWrite(ctx, cmdr, target.ThreadsURL(),
		azureSummaryThreadBody(part.body), dir, part.warnings, posted, len(part.comments))
	if !ok {
		return azureCreatedThread{}, attempt, false
	}
	return created, postAttempt{}, true
}

// azureReceiptObject is the accepted review in the receipt shape post.json
// carries, matching what every host records: the write's id, the state, and
// the pull-request page.
func azureReceiptObject(pr *azurePull, summary *azureCreatedThread) string {
	state := azureStateCommented
	id, html, submitted := summary.id, pr.htmlURL, summary.published
	if id != "" {
		html = pr.htmlURL + "?discussionId=" + id
	}
	obj := jq.NewObj(
		"id", id,
		"state", state,
		"html_url", html,
		"submitted_at", submitted,
	)
	return strings.TrimRight(jq.PrettyOrdered(obj), "\n")
}

// azureSideIndex is the set of paths the change leaves at head and the set
// it removes, read from the bundle's hunk index: a path with no NEW row
// exists only on the old side and vice versa. An unreadable index is an
// error, never an empty pair of sets: an empty index would withhold every
// anchor.
func azureSideIndex(dir string) (map[string]map[string]bool, error) {
	hs, err := hunks.Load(dir)
	if err != nil {
		return nil, jq.Sealed("read azure hunk index", err)
	}
	paths := map[string]map[string]bool{
		hunkSideNew: make(map[string]bool, len(hs)),
		hunkSideOld: make(map[string]bool, len(hs)),
	}
	for _, h := range hs {
		paths[h.Side][h.File] = true
	}
	return paths, nil
}

// parseAzurePayload reads the hosted payload and applies the v1 anchor
// rules: a finding with no placeable anchor cannot be placed and is
// withheld, an anchor whose side the change does not have on that path is
// withheld, and a multi-line range is flattened to its end line. Every loss
// is counted into an envelope warning, so a shorter review never lands
// silently. sides is the hunk side index the caller read first; it refuses
// an unreadable bundle before any write rather than withholding every
// anchor.
func parseAzurePayload(payload string, sides map[string]map[string]bool) (azurePostPayload, bool) {
	doc, err := jq.DecodeOrdered([]byte(payload))
	if err != nil {
		return azurePostPayload{}, false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok || obj == nil {
		return azurePostPayload{}, false
	}
	rawEvent := orderedString(obj, "event")
	part := azurePostPayload{
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
	kept, losses := azureKeptComments(jq.Elements(comments), sides)
	part.comments = kept
	part.warnings = azureAnchorWarnings(losses)
	return part, true
}

// azureLosses is what the anchor rules withheld from one payload, counted by
// rule.
type azureLosses struct {
	dropped int
	missing int
	multi   int
}

// azureKeptComments applies the anchor rules in order: a finding with no
// placeable anchor cannot be placed at all, an anchor on a side the change
// does not have on that path cannot resolve, and a multi-line range is
// flattened to its end line by the caller that writes the anchor.
func azureKeptComments(raw []any, sides map[string]map[string]bool) ([]azureComment, azureLosses) {
	kept := make([]azureComment, 0, len(raw))
	var losses azureLosses
	for _, el := range raw {
		comment, ok := azureCommentOf(el)
		switch {
		case !ok:
			losses.dropped++
		case !azureSidePresent(comment, sides):
			losses.missing++
		default:
			if comment.start > 0 && comment.start != comment.line {
				losses.multi++
			}
			kept = append(kept, comment)
		}
	}
	return kept, losses
}

// azureSidePresent reports whether the change carries the anchor's side on
// the finding's path. A path the hunk index does not name at all stays
// inline: the merge keeps an ambiguous anchor and warns, and withholding it
// here would silently shorten a review the diff never ruled out.
func azureSidePresent(c azureComment, sides map[string]map[string]bool) bool {
	side := hunkSideNew
	if c.left {
		side = hunkSideOld
	}
	if !sides[hunkSideNew][c.path] && !sides[hunkSideOld][c.path] {
		return true
	}
	return sides[side][c.path]
}

// azureCommentOf reduces one payload comment, reporting false when the
// anchor cannot be placed: Azure needs a path, a body, and a 1-based line.
func azureCommentOf(raw any) (azureComment, bool) {
	m, ok := raw.(*jq.Obj)
	if !ok || m == nil {
		return azureComment{}, false
	}
	comment := azureComment{
		path:  azurePath(orderedString(m, fieldPath)),
		body:  orderedString(m, fieldBody),
		left:  strings.EqualFold(orderedString(m, fieldSide), azureSideLeft),
		line:  orderedInt(m, fieldLine),
		start: orderedInt(m, fieldStartLine),
	}
	if comment.path == "" || comment.body == "" || comment.line <= 0 {
		return azureComment{}, false
	}
	return comment, true
}

// azurePath drops the diff's a/ or b/ prefix: Azure matches a thread's
// filePath against the tree, where the prefix never appears.
func azurePath(path string) string {
	for _, prefix := range []string{"a/", "b/"} {
		if rest, ok := strings.CutPrefix(path, prefix); ok {
			return rest
		}
	}
	return path
}

// azureAnchorWarnings states the anchor losses this payload takes, one
// sentence per rule that fired.
func azureAnchorWarnings(losses azureLosses) []string {
	var warnings []string
	for _, loss := range []struct {
		message string
		n       int
	}{
		{message: msgAzureDropped, n: losses.dropped},
		{message: msgAzureSide, n: losses.missing},
		{message: msgAzureMulti, n: losses.multi},
	} {
		if loss.n > 0 {
			warnings = append(warnings, fmt.Sprintf(loss.message, loss.n))
		}
	}
	return warnings
}

// liveSHA reads the pull request's current head from the same payload the
// drift check compares.
func (azureHost) liveSHA(ctx context.Context, cmdr Commander, env map[string]string, repo, pr string) (string, error) {
	target, ref := azure.Resolve(env, repo, pr)
	if ref != nil {
		return "", azureRefusalOf(ref)
	}
	raw, _, err := azureRead(ctx, cmdr, target.PullURL())
	if err != nil {
		return "", err
	}
	return azure.DecodePull(raw).Head(), nil
}

// selfLogin reads the authenticated account name, which the authorship gate
// and the summary comment lookup compare against a thread author.
func (azureHost) selfLogin(ctx context.Context, cmdr Commander, env map[string]string) (string, error) {
	org, ref := azure.Org(env)
	if ref != nil {
		return "", azureRefusalOf(ref)
	}
	raw, _, err := azureRead(ctx, cmdr, azure.ConnectionDataURL(org))
	if err != nil {
		return "", err
	}
	me, ok := azure.DecodeIdentity(raw)
	if !ok {
		return "", shapeError("azure connection read carries no authenticated user")
	}
	return me.Name, nil
}

// azureThreadList reads every thread on the pull request. Azure answers one
// `{count, value}` document — no next cursor — so the whole list arrives in
// one read.
func azureThreadList(ctx context.Context, cmdr Commander, target azure.Target) ([]map[string]any, error) {
	raw, _, err := azureRead(ctx, cmdr, target.ThreadsURL())
	if err != nil {
		return nil, err
	}
	items, ok := azure.DecodeThreads(raw)
	if !ok {
		return nil, shapeError("azure thread list unreadable")
	}
	return items, nil
}

// recheck counts the open inline threads on the pull request: an anchored
// thread that is still active or pending and whose author has not resolved
// it. A general thread — verdict's own summary comment, a vote, a merge
// notice — carries no threadContext and is never a thread, so a second run
// is not blocked by the summary this tool wrote. A read that fails leaves
// the count unknown instead of short, so an approval is never certified on
// half a read.
func (azureHost) recheck(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr, dir string,
) (int, string, error) {
	target, ref := azure.Resolve(env, repo, pr)
	var (
		open   int
		stderr string
		err    error
	)
	switch {
	case ref != nil:
		err, stderr = azureRefusalOf(ref), ref.Message
	case cmdr == nil:
		err, stderr = errNoCommander, errNoCommander.Error()
	default:
		open, err = countAzureOpen(ctx, cmdr, target)
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

// countAzureOpen counts the open inline threads.
func countAzureOpen(ctx context.Context, cmdr Commander, target azure.Target) (int, error) {
	items, err := azureThreadList(ctx, cmdr, target)
	if err != nil {
		return 0, err
	}
	open := 0
	for _, m := range items {
		if azureOpenThread(m) {
			open++
		}
	}
	return open, nil
}

// azureOpenThread reports one thread a reviewer still has to answer:
// anchored, not deleted, not a system message, and still active or pending.
func azureOpenThread(m map[string]any) bool {
	if azure.Deleted(m) || azure.System(m) || !azure.Inline(m) {
		return false
	}
	return azure.Open(m)
}

// publication reads the live pull head and the whole thread list and splits
// it into the duplicate-check reviews (general threads, whose bodies carry
// the payload marker) and the inline threads the missing check pairs
// against. A thread carries no commit, so the reviewed head stands in for it
// — which is the head on a same-head repost.
func (azureHost) publication(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr string,
) (remotePublication, error) {
	target, ref := azure.Resolve(env, repo, pr)
	if ref != nil {
		return remotePublication{}, azureRefusalOf(ref)
	}
	raw, _, err := azureRead(ctx, cmdr, target.PullURL())
	if err != nil {
		return remotePublication{}, err
	}
	head := azure.DecodePull(raw).Head()
	items, err := azureThreadList(ctx, cmdr, target)
	if err != nil {
		return remotePublication{}, err
	}
	return azurePublication(items, head, target), nil
}

// azurePublication splits one thread list into reviews and remote comments
// in the shapes the shared duplicate and missing checks read.
func azurePublication(items []map[string]any, head string, target azure.Target) remotePublication {
	var remote remotePublication
	for _, m := range items {
		azurePublicationItem(&remote, m, head, target)
	}
	return remote
}

// azurePublicationItem routes one thread to its side of the publication.
func azurePublicationItem(remote *remotePublication, m map[string]any, head string, target azure.Target) {
	if azure.Deleted(m) || azure.System(m) {
		return
	}
	if !azure.Inline(m) {
		if body := azure.Body(m); body != "" {
			remote.reviews = append(remote.reviews, azureReviewOf(m, body))
		}
		return
	}
	if translated, ok := translateAzureInline(m, head, target); ok {
		remote.comments = append(remote.comments, translated)
	}
}

// azureReviewOf maps one general thread onto the live-review shape: the body
// carries the payload marker, and the marker head is the reviewed head.
func azureReviewOf(m map[string]any, body string) liveReview {
	_, markerHead, _ := reviewMarker(body)
	return liveReview{
		body: body, commitID: markerHead,
		submittedAt: strOf(m["publishedDate"]), state: azureStateCommented,
	}
}

// translateAzureInline maps one inline thread onto the remote-comment shape
// the missing check pairs: a right-side or left-side position becomes path,
// side, and line, and the body stays the thread's opening comment.
func translateAzureInline(m map[string]any, head string, target azure.Target) (map[string]any, bool) {
	anchor, ok := azure.AnchorOf(m)
	if !ok {
		return nil, false
	}
	body := azure.Body(m)
	if body == "" {
		return nil, false
	}
	translated := map[string]any{
		fieldPath: anchor.Path, fieldSide: anchor.Side, fieldLine: anchor.Start,
		fieldBody: body, "commit_id": head,
	}
	if id := azure.ThreadID(m); id != "" {
		translated[fieldID] = id
		translated["html_url"] = target.ThreadWebURL(id)
	}
	return translated, true
}

// listSummaryComments reads the general threads that could carry a verdict
// summary: anchored threads are reviews, and system threads are host
// notices.
func (azureHost) listSummaryComments(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr string,
) ([]summaryRemoteComment, error) {
	target, ref := azure.Resolve(env, repo, pr)
	if ref != nil {
		return nil, azureRefusalOf(ref)
	}
	items, err := azureThreadList(ctx, cmdr, target)
	if err != nil {
		return nil, err
	}
	var out []summaryRemoteComment
	for _, m := range items {
		if comment, ok := azureSummaryComment(target, m); ok {
			out = append(out, comment)
		}
	}
	return out, nil
}

// azureSummaryComment translates one general thread into the summary shape
// pickSummaryTarget reads, reporting false for an inline or system thread.
func azureSummaryComment(target azure.Target, m map[string]any) (summaryRemoteComment, bool) {
	if azure.Deleted(m) || azure.System(m) || azure.Inline(m) {
		return summaryRemoteComment{}, false
	}
	handle := azureSummaryID(azure.ThreadID(m), azure.CommentID(m))
	body := azure.Body(m)
	if handle == "" || body == "" {
		return summaryRemoteComment{}, false
	}
	return summaryRemoteComment{
		ID: handle, Author: azure.Author(m), Body: body,
		URL:       target.ThreadWebURL(azure.ThreadID(m)),
		CreatedAt: strOf(m["publishedDate"]),
		UpdatedAt: strOf(m["lastUpdatedDate"]),
	}, true
}

// azureSummaryID is the handle one summary comment is patched by: the thread
// id and the opening comment id, because Azure restarts comment ids on every
// thread.
func azureSummaryID(threadID, commentID string) string {
	if threadID == "" || commentID == "" {
		return ""
	}
	return threadID + azureSummaryIDJoin + commentID
}

// splitAzureSummaryID reads one summary comment handle: two positive integer
// ids joined by a slash, so a crafted handle cannot address another endpoint.
func splitAzureSummaryID(id string) (threadID, commentID string, ok bool) {
	threadID, commentID, ok = strings.Cut(id, azureSummaryIDJoin)
	if !ok || !positiveID(threadID) || !positiveID(commentID) {
		return "", "", false
	}
	return threadID, commentID, true
}

// positiveID reports one handle segment Azure reads as an id: a positive
// integer, so `501/1` patches thread 501's first comment and `x/../../y`
// refuses before any call.
func positiveID(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n > 0
}

// createSummaryComment posts the one general thread that carries the verdict
// summary.
func (azureHost) createSummaryComment(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr, body string,
) (summaryRemoteComment, error) {
	target, ref := azure.Resolve(env, repo, pr)
	if ref != nil {
		return summaryRemoteComment{}, azureRefusalOf(ref)
	}
	if strings.TrimSpace(body) == "" {
		return summaryRemoteComment{}, shapeError(errEmptySummary)
	}
	res, err := azureWrite(ctx, cmdr, azMethodPost, target.ThreadsURL(), azureSummaryThreadBody(body))
	if err != nil {
		return summaryRemoteComment{}, err
	}
	if res.ExitCode != 0 {
		return summaryRemoteComment{}, shapeError(azureFailureReason(res))
	}
	return azureWrittenSummary(target, res.Stdout)
}

// updateSummaryComment patches the opening comment of the thread the handle
// names.
func (azureHost) updateSummaryComment(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr, commentID, body string,
) (summaryRemoteComment, error) {
	target, ref := azure.Resolve(env, repo, pr)
	if ref != nil {
		return summaryRemoteComment{}, azureRefusalOf(ref)
	}
	if strings.TrimSpace(body) == "" {
		return summaryRemoteComment{}, shapeError(errEmptySummary)
	}
	threadID, threadCommentID, ok := splitAzureSummaryID(commentID)
	if !ok {
		return summaryRemoteComment{}, shapeError(fmt.Sprintf(msgAzureSummaryID, commentID))
	}
	res, err := azureWrite(ctx, cmdr, azMethodPatch,
		target.ThreadCommentURL(threadID, threadCommentID), jq.NewObj("content", body).MarshalPretty())
	if err != nil {
		return summaryRemoteComment{}, err
	}
	if res.ExitCode != 0 {
		return summaryRemoteComment{}, shapeError(azureFailureReason(res))
	}
	updated, ok := decodeAzurePatch(res.Stdout)
	if !ok {
		return summaryRemoteComment{}, shapeError(msgAzurePayload)
	}
	return summaryRemoteComment{
		ID: commentID, Body: updated.body,
		URL:       target.ThreadWebURL(threadID),
		CreatedAt: updated.published, UpdatedAt: updated.updated,
	}, nil
}

// azureWrittenSummary reads a created summary thread back as the summary
// shape.
func azureWrittenSummary(target azure.Target, raw string) (summaryRemoteComment, error) {
	created, ok := decodeAzureCreatedThread(raw)
	if !ok {
		return summaryRemoteComment{}, shapeError(msgAzurePayload)
	}
	return summaryRemoteComment{
		ID: azureSummaryID(created.id, created.commentID), Body: created.body,
		URL:       target.ThreadWebURL(created.id),
		CreatedAt: created.published, UpdatedAt: created.updated,
	}, nil
}

// decodeAzurePatch reads the comment document a patch answers with.
func decodeAzurePatch(raw string) (azureCreatedThread, bool) {
	var doc struct {
		Content         string `json:"content"`
		PublishedDate   string `json:"publishedDate"`
		LastUpdatedDate string `json:"lastUpdatedDate"`
		ID              int    `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || doc.ID <= 0 {
		return azureCreatedThread{}, false
	}
	return azureCreatedThread{
		commentID: strconv.Itoa(doc.ID), body: doc.Content,
		published: doc.PublishedDate, updated: doc.LastUpdatedDate,
	}, true
}

// postedInlineURLs reads the anchored threads back into the summary links,
// for a caller that posted and did not collect them.
func (azureHost) postedInlineURLs(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr string, _ *jq.Obj,
) ([]summaryInlineLink, error) {
	target, ref := azure.Resolve(env, repo, pr)
	if ref != nil {
		return nil, azureRefusalOf(ref)
	}
	items, err := azureThreadList(ctx, cmdr, target)
	if err != nil {
		return nil, err
	}
	var out []summaryInlineLink
	for _, m := range items {
		anchor, ok := azure.AnchorOf(m)
		if !ok || azure.Deleted(m) {
			continue
		}
		out = append(out, summaryInlineLink{
			Path: anchor.Path, Body: azure.Body(m),
			URL: target.ThreadWebURL(azure.ThreadID(m)), Line: anchor.Start,
		})
	}
	return out, nil
}
