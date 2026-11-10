package postcore

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgejo"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// Forgejo has no CLI: every read and write is one API call through `curl`. The
// write is a single atomic review, so the anchor mapping, the event spelling,
// and the post-condition on the answer are the whole submit path.
const (
	toolCurl       = "curl"
	curlFail       = "-sf"
	curlShowStatus = "-sS"
	curlInclude    = "-i"
	curlRequest    = "-X"
	curlDataBinary = "--data-binary"
	curlStdin      = "@-"
	curlJSONHeader = "Content-Type: application/json"

	// forgejoSideLeft is the payload's old-side marker (`side: "LEFT"`).
	forgejoSideLeft = "LEFT"

	// statusMin and statusMax bound what a status line can carry before the
	// parsed number is a status code at all.
	statusMin = 100
	statusMax = 599
)

// Forgejo-specific refusals. A reason recorded through postFailure reads inside
// msgPostFailed; a sentence recorded through postRefusal is the whole message.
const (
	msgForgejoPayload      = "forgejo post payload unreadable"
	msgForgejoLost         = "forgejo did not answer — the review may exist; check the PR before re-running"
	msgForgejoPartial      = "forgejo may have kept %d inline comment(s) on an unsubmitted review — a re-run appends them again"
	msgForgejoDropped      = "forgejo anchors need a numeric line — %d finding(s) withheld from the review"
	msgForgejoDeleted      = "forgejo cannot anchor a comment on a file the change deletes — %d finding(s) withheld at submit"
	msgForgejoMultiLine    = "forgejo anchors are single-line — %d multi-line anchor(s) anchored at their end line"
	msgForgejoDraftUnknown = "forgejo could not name the token user — an unsubmitted review draft on this PR would receive these comments"

	msgForgejoNoCommit = "post failed: the payload carries no commit_id — forgejo would pin the review " +
		"to the current head instead of the reviewed one; re-fetch to record the head"
	msgForgejoEvent = "post failed: forgejo has no review event named %q — " +
		"post a comment, an approval, or a request for changes"
	msgForgejoState = "post failed: forgejo answered state %s, not the requested %s — " +
		"a draft review is not feedback; submit or discard it, then post again"
	msgForgejoDraft = "post failed: an unsubmitted review draft exists on this PR — verdict's comments " +
		"would join it and publish it under this review's body; submit or discard the draft, then post again"
	msgForgejoDraftUnread = "post failed: forgejo did not answer the review-list read, so an unsubmitted " +
		"draft could not be ruled out — nothing was written; re-run when the read works"
)

// forgejoHost is the `curl` host: Forgejo ships no CLI, so a read is one
// authenticated GET and a write is one authenticated POST.
type forgejoHost struct{}

func (forgejoHost) tool() string { return toolCurl }

// auth gates the environment before the first call: no other host reads its
// credentials from an environment verdict owns.
func (forgejoHost) auth(env map[string]string) *core.RefusalError {
	return forgejo.Auth(env)
}

// forgejoRefusal re-states the shared package's refusal as this package's own,
// so the published code and the operator sentence cross the seam unchanged and
// stay inspectable without a wrap.
func forgejoRefusal(ref *core.RefusalError) *core.RefusalError {
	return &core.RefusalError{Code: ref.Code, Message: ref.Message}
}

// forgejoPostPayload is the part of the hosted payload a Forgejo write reads:
// the review body and event, the reviewed commit, and the inline findings that
// survive the anchor rules.
type forgejoPostPayload struct {
	body     string
	event    string
	rawEvent string
	commitID string
	comments []forgejoComment
	warnings []string
}

// forgejoComment is one finding reduced to what a Forgejo review comment needs:
// a path, a body, and the line on one side.
type forgejoComment struct {
	path  string
	body  string
	left  bool
	line  int
	start int
}

// forgejoHTTP is one `curl -i` answer: the status code and whatever follows the
// headers.
type forgejoHTTP struct {
	body   string
	status int
}

// post reads the payload, refuses what Forgejo cannot place locally, checks for
// a pre-existing draft, and then submits the one review.
func (forgejoHost) post(ctx context.Context, cmdr Commander, env map[string]string,
	_ core.HostCapabilities, repo, pr, dir, payload string,
) postAttempt {
	auth, url, ref := forgejo.Resolve(env, repo, pr)
	if ref != nil {
		return postRefusal(dir, ref.Message, nil)
	}
	part, ok := parseForgejoPayload(dir, payload)
	if !ok {
		return postFailure(dir, msgForgejoPayload, false, nil)
	}
	warnings := part.warnings
	if part.commitID == "" {
		return postRefusal(dir, msgForgejoNoCommit, warnings)
	}
	if part.event == "" {
		return postRefusal(dir, fmt.Sprintf(msgForgejoEvent, part.rawEvent), warnings)
	}
	if attempt, ok := forgejoDraftGate(ctx, cmdr, auth, url, dir, &warnings); !ok {
		return attempt
	}
	return forgejoSubmit(ctx, cmdr, auth, url, &part, dir, warnings)
}

// forgejoSubmit makes the one write and holds it to the answer: the created
// review's state must be the state the run asked for, because Forgejo creates a
// PENDING draft with a 200 when the event is not one it knows, and a draft is
// feedback nobody sees.
func forgejoSubmit(ctx context.Context, cmdr Commander, auth []string, url string,
	part *forgejoPostPayload, dir string, warnings []string,
) postAttempt {
	res, err := forgejoCall(ctx, cmdr, forgejoWriteArgv(auth, url+forgejo.ReviewsPath), part.requestBody())
	if err != nil {
		return postFailure(dir, msgForgejoLost, true, append(warnings, forgejoLandedWarning(len(part.comments))...))
	}
	answer, ok := parseForgejoResponse(res.Stdout)
	if !ok {
		return postFailure(dir, msgForgejoLost, true, append(warnings, forgejoLandedWarning(len(part.comments))...))
	}
	if answer.status != http.StatusOK {
		return postFailure(dir, forgejoFailureReason(res, answer), true,
			append(warnings, forgejoLandedWarning(len(part.comments))...))
	}
	review, ok := forgejo.DecodeReview(answer.body)
	if !ok {
		return postFailure(dir, msgForgejoPayload, true, append(warnings, forgejoLandedWarning(len(part.comments))...))
	}
	if review.State != part.event {
		return postRefusal(dir, fmt.Sprintf(msgForgejoState, review.State, part.event),
			append(warnings, forgejoLandedWarning(len(part.comments))...))
	}
	return postAccepted(dir, forgejoReceiptObject(&review), res.Stderr, warnings)
}

// forgejoDraftGate refuses a PR whose token user already holds an unsubmitted
// review: Forgejo attaches each new comment to that draft and submits the whole
// thing, so verdict would publish someone's in-progress review under its own
// body. A review list that cannot be read fails closed — the risk it guards
// against is exactly the one that would go unnoticed.
func forgejoDraftGate(ctx context.Context, cmdr Commander, auth []string, url, dir string,
	warnings *[]string,
) (postAttempt, bool) {
	me, err := forgejoSelfLogin(ctx, cmdr, auth, forgejo.UserURL(url))
	if err != nil {
		*warnings = append(*warnings, msgForgejoDraftUnknown)
		return postAttempt{}, true
	}
	reviews, err := forgejoReviews(ctx, cmdr, auth, url)
	if err != nil {
		return postRefusal(dir, msgForgejoDraftUnread, *warnings), false
	}
	for _, review := range reviews {
		if review.State == forgejo.StatePending && review.User.Login == me {
			return postRefusal(dir, msgForgejoDraft, *warnings), false
		}
	}
	return postAttempt{}, true
}

// forgejoLandedWarning names what a failed write may already have created:
// Forgejo creates one comment per entry before submitting, so a failure on a
// later step leaves comments on an unsubmitted review.
func forgejoLandedWarning(n int) []string {
	if n == 0 {
		return nil
	}
	return []string{fmt.Sprintf(msgForgejoPartial, n)}
}

// forgejoFailureReason prefers the host's own message, then its stderr, then
// the status line, so a refusal never quotes an empty reason.
func forgejoFailureReason(res core.Result, answer forgejoHTTP) string {
	if message := forgejoMessage(answer.body); message != "" {
		return message
	}
	if res.Stderr != "" {
		return res.Stderr
	}
	return "HTTP " + strconv.Itoa(answer.status)
}

// forgejoMessage reads the `message` field of a Forgejo error body.
func forgejoMessage(body string) string {
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

// parseForgejoResponse splits one `curl -i` document at its first blank line
// and reads the status code out of the status line. A line feed and a CRLF
// document parse the same, and a response with headers but no body is a valid
// answer.
func parseForgejoResponse(raw string) (forgejoHTTP, bool) {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	head, body, _ := strings.Cut(text, "\n\n")
	fields := strings.Fields(strings.SplitN(head, "\n", 2)[0])
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "HTTP/") {
		return forgejoHTTP{}, false
	}
	status, err := strconv.Atoi(fields[1])
	if err != nil || status < statusMin || status > statusMax {
		return forgejoHTTP{}, false
	}
	return forgejoHTTP{status: status, body: body}, true
}

// forgejoWriteArgv is the one write a Forgejo post makes: an explicit method,
// both headers, and the JSON body on stdin. `-i` carries the status line and
// the host's own error body on stdout where `-f` would throw both away on 4xx.
func forgejoWriteArgv(auth []string, url string) []string {
	argv := append([]string{toolCurl, curlShowStatus, curlInclude}, auth...)
	argv = append(argv, "-H", curlJSONHeader, curlRequest, methodPost, curlDataBinary, curlStdin)
	return append(argv, url)
}

// forgejoReadArgv is one authenticated GET, in fetch's shape.
func forgejoReadArgv(auth []string, url string) []string {
	return append(append([]string{toolCurl, curlFail}, auth...), url)
}

// forgejoCall runs one Forgejo write and seals a spawn failure; the caller
// reports the result's own stdout and stderr.
func forgejoCall(ctx context.Context, cmdr Commander, argv []string, body string) (core.Result, error) {
	cmd := &core.Command{Argv: argv}
	if body != "" {
		cmd.Stdin = strings.NewReader(body)
	}
	res, err := cmdr.Run(ctx, cmd)
	if err != nil {
		return res, jq.Sealed("forgejo call", err)
	}
	return res, nil
}

// forgejoRead runs one authenticated GET and hands back the body with the
// host's stderr, so a caller that owns the recheck sidecar records the host's
// own words.
func forgejoRead(ctx context.Context, cmdr Commander, auth []string, url string) (body, stderr string, err error) {
	if cmdr == nil {
		return "", errNoCommander.Error(), errNoCommander
	}
	res, err := runReadRetry(ctx, cmdr, &core.Command{Argv: forgejoReadArgv(auth, url)})
	if err != nil {
		return "", res.Stderr, err
	}
	return res.Stdout, res.Stderr, nil
}

// forgejoSelfLogin reads the login the draft check and the authorship gate
// compare against.
func forgejoSelfLogin(ctx context.Context, cmdr Commander, auth []string, userURL string) (string, error) {
	raw, _, err := forgejoRead(ctx, cmdr, auth, userURL)
	if err != nil {
		return "", err
	}
	user, ok := forgejo.DecodeUser(raw)
	if !ok || user.Login == "" {
		return "", shapeError("forgejo user read unreadable")
	}
	return user.Login, nil
}

// forgejoReviews reads the reviews page the draft check scans.
func forgejoReviews(ctx context.Context, cmdr Commander, auth []string, base string) ([]forgejo.Review, error) {
	raw, _, err := forgejoRead(ctx, cmdr, auth, forgejo.ReviewListURL(base))
	if err != nil {
		return nil, err
	}
	reviews, ok := forgejo.DecodeReviews(raw)
	if !ok {
		return nil, shapeError("forgejo review list unreadable")
	}
	return reviews, nil
}

// forgejoPostPayload renders CreatePullReviewOptions: one comment per finding,
// anchored by file line number on the side the finding states. A multi-line
// range is flattened to its end line, and extra_lines_count stays unset.
func (part *forgejoPostPayload) requestBody() string {
	comments := make([]any, 0, len(part.comments))
	for _, c := range part.comments {
		obj := jq.NewObj(fieldPath, c.path, fieldBody, c.body)
		if c.left {
			obj.Set("old_position", c.line)
		} else {
			obj.Set("new_position", c.line)
		}
		comments = append(comments, obj)
	}
	return jq.NewObj(
		fieldBody, part.body,
		"event", part.event,
		"commit_id", part.commitID,
		fieldComments, comments,
	).MarshalPretty()
}

// parseForgejoPayload reads the hosted payload and applies the v1 anchor rules:
// a finding with no numeric line cannot be placed and is withheld, a LEFT
// anchor on a file the change deletes is withheld, and a multi-line range is
// flattened to its end line. Every loss is counted into an envelope warning, so
// a shorter review never lands silently.
func parseForgejoPayload(dir, payload string) (forgejoPostPayload, bool) {
	doc, err := jq.DecodeOrdered([]byte(payload))
	if err != nil {
		return forgejoPostPayload{}, false
	}
	obj, ok := doc.(*jq.Obj)
	if !ok || obj == nil {
		return forgejoPostPayload{}, false
	}
	rawEvent := orderedString(obj, "event")
	part := forgejoPostPayload{
		body:     orderedString(obj, fieldBody),
		event:    forgejoEvent(rawEvent),
		rawEvent: rawEvent,
		commitID: orderedString(obj, "commit_id"),
	}
	comments, _ := obj.Get(fieldComments)
	kept, losses := forgejoKeptComments(jq.Elements(comments), forgejoNewSidePaths(dir))
	part.comments = kept
	part.warnings = forgejoAnchorWarnings(losses)
	return part, true
}

// forgejoLosses is what the anchor rules withheld from one payload, counted by
// rule.
type forgejoLosses struct {
	dropped int
	deleted int
	multi   int
}

// forgejoClass is what the anchor rules decide about one payload comment.
type forgejoClass int

const (
	forgejoKeep forgejoClass = iota
	forgejoDrop
	forgejoDelete
)

// forgejoKeptComments applies the v1 anchor rules in order: a finding with no
// numeric line cannot be placed at all, a LEFT anchor on a file the change
// deletes cannot resolve, and a multi-line range is flattened to its end line
// by the caller that writes the position.
func forgejoKeptComments(raw []any, newSide map[string]bool) ([]forgejoComment, forgejoLosses) {
	kept := make([]forgejoComment, 0, len(raw))
	var losses forgejoLosses
	for _, el := range raw {
		comment, class := classifyForgejoComment(el, newSide)
		switch class {
		case forgejoDrop:
			losses.dropped++
		case forgejoDelete:
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

// classifyForgejoComment decides one payload comment: no placeable anchor is
// dropped, a LEFT anchor on a path with no new-side row is a deleted file, and
// everything else keeps the line the merge already validated.
func classifyForgejoComment(raw any, newSide map[string]bool) (forgejoComment, forgejoClass) {
	comment, ok := forgejoCommentOf(raw)
	switch {
	case !ok:
		return forgejoComment{}, forgejoDrop
	case comment.left && !newSide[comment.path]:
		return forgejoComment{}, forgejoDelete
	default:
		return comment, forgejoKeep
	}
}

// forgejoCommentOf reduces one payload comment, reporting false when the anchor
// cannot be placed: Forgejo needs a path, a body, and a 1-based line.
func forgejoCommentOf(raw any) (forgejoComment, bool) {
	m, ok := raw.(*jq.Obj)
	if !ok || m == nil {
		return forgejoComment{}, false
	}
	comment := forgejoComment{
		path:  forgejoPath(orderedString(m, fieldPath)),
		body:  orderedString(m, fieldBody),
		left:  strings.EqualFold(orderedString(m, "side"), forgejoSideLeft),
		line:  orderedInt(m, fieldLine),
		start: orderedInt(m, fieldStartLine),
	}
	if comment.path == "" || comment.body == "" || comment.line <= 0 {
		return forgejoComment{}, false
	}
	return comment, true
}

// forgejoPath drops the diff's a/ or b/ prefix: Forgejo matches a comment path
// against the tree, where the prefix never appears.
func forgejoPath(path string) string {
	for _, prefix := range []string{"a/", "b/"} {
		if rest, ok := strings.CutPrefix(path, prefix); ok {
			return rest
		}
	}
	return path
}

// forgejoNewSidePaths is the set of paths the change leaves at head, read from
// the bundle's hunk index: a path with no NEW row exists only on the old side.
func forgejoNewSidePaths(dir string) map[string]bool {
	hs, err := hunks.Load(dir)
	if err != nil {
		return nil
	}
	paths := make(map[string]bool, len(hs))
	for _, h := range hs {
		if h.Side == "NEW" {
			paths[h.File] = true
		}
	}
	return paths
}

// forgejoAnchorWarnings states the anchor losses this payload takes, one
// sentence per rule that fired.
func forgejoAnchorWarnings(losses forgejoLosses) []string {
	var warnings []string
	for _, loss := range []struct {
		message string
		n       int
	}{
		{message: msgForgejoDropped, n: losses.dropped},
		{message: msgForgejoDeleted, n: losses.deleted},
		{message: msgForgejoMultiLine, n: losses.multi},
	} {
		if loss.n > 0 {
			warnings = append(warnings, fmt.Sprintf(loss.message, loss.n))
		}
	}
	return warnings
}

// forgejoEvent maps verdict's payload event onto the one Forgejo spells
// differently: sending APPROVE creates a PENDING draft with a 200, so the
// approval must go out as APPROVED. An event outside the three is unknown, and
// the caller refuses rather than guess.
func forgejoEvent(event string) string {
	switch event {
	case eventApprove:
		return forgejo.EventApproved
	case eventRequestChanges:
		return forgejo.EventRequestChanges
	case eventComment:
		return forgejo.EventComment
	default:
		return ""
	}
}

// forgejoReceiptObject is the accepted review in the receipt shape post.json
// carries, matching what every host records.
func forgejoReceiptObject(review *forgejo.Review) string {
	obj := jq.NewObj(
		"id", review.ID,
		"state", review.State,
		"html_url", review.HTMLURL,
		"submitted_at", review.SubmittedAt,
	)
	return strings.TrimRight(jq.PrettyOrdered(obj), newline)
}

// liveSHA reads the PR's current head from the same payload fetch records, so
// the drift check costs one read and no new logic.
func (forgejoHost) liveSHA(ctx context.Context, cmdr Commander, env map[string]string, repo, pr string) (string, error) {
	auth, base, ref := forgejo.Resolve(env, repo, pr)
	if ref != nil {
		return "", forgejoRefusal(ref)
	}
	raw, _, err := forgejoRead(ctx, cmdr, auth, base)
	if err != nil {
		return "", err
	}
	return forgejo.DecodePull(raw).Head.SHA, nil
}

// selfLogin reads the token user's login, which the authorship gate compares
// against the bundle author.
func (forgejoHost) selfLogin(ctx context.Context, cmdr Commander, env map[string]string) (string, error) {
	auth, api, ref := forgejo.APIBase(env)
	if ref != nil {
		return "", forgejoRefusal(ref)
	}
	return forgejoSelfLogin(ctx, cmdr, auth, api+"/user")
}

// recheck counts the unresolved inline comments on every submitted review: a
// review comment with no resolver is one open thread, and a pending review's
// comments are a draft that never counts. A read that fails leaves the count
// unknown instead of short, so an approval is never certified on half a read.
func (forgejoHost) recheck(ctx context.Context, cmdr Commander, env map[string]string,
	repo, pr, dir string,
) (int, string, error) {
	auth, base, ref := forgejo.Resolve(env, repo, pr)
	var (
		open   int
		stderr string
		err    error
	)
	switch {
	case ref != nil:
		err, stderr = forgejoRefusal(ref), ref.Message
	case cmdr == nil:
		err, stderr = errNoCommander, errNoCommander.Error()
	default:
		open, stderr, err = countForgejoOpen(ctx, cmdr, auth, base)
	}
	if werr := atomic.WriteErrSidecar(dir+recheckErrSuffix, []byte(stderr)); werr != nil {
		return 0, "", jq.Sealed("write .recheck.err", werr)
	}
	if err != nil {
		return 0, firstReason(stderr), err
	}
	return open, "", nil
}

// countForgejoOpen walks the review list and one comment page per submitted
// review, counting the comments no resolver closed.
func countForgejoOpen(ctx context.Context, cmdr Commander, auth []string, base string) (int, string, error) {
	reviews, err := forgejoReviews(ctx, cmdr, auth, base)
	if err != nil {
		return 0, "", err
	}
	open := 0
	for _, review := range reviews {
		if review.State == forgejo.StatePending {
			continue
		}
		count, stderr, err := forgejoOpenInReview(ctx, cmdr, auth, base, review.ID)
		if err != nil {
			return 0, stderr, err
		}
		open += count
	}
	return open, "", nil
}

// forgejoOpenInReview counts the comments no resolver closed on one submitted
// review.
func forgejoOpenInReview(ctx context.Context, cmdr Commander, auth []string, base string, id int) (int, string, error) {
	raw, stderr, err := forgejoRead(ctx, cmdr, auth, forgejo.CommentsURL(base, strconv.Itoa(id)))
	if err != nil {
		return 0, stderr, err
	}
	comments, ok := forgejo.DecodeComments(raw)
	if !ok {
		return 0, stderr, shapeError("forgejo review comments unreadable")
	}
	open := 0
	for _, comment := range comments {
		if !forgejo.Resolved(comment) {
			open++
		}
	}
	return open, stderr, nil
}
