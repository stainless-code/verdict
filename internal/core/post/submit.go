package postcore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/retry"
)

const postJQFilter = "{id, state, html_url, submitted_at}"

const (
	toolGH = "gh"
	cmdAPI = "api"
	flagJQ = "--jq"

	flagMethod = "--method"
	flagInput  = "--input"
	methodPost = "POST"
	methodPut  = "PUT"

	// postErrSuffix is the sidecar that carries the forge's own stderr, so a
	// post-failed refusal quotes the host instead of an empty reason.
	postErrSuffix = "/.post.err"

	// recheckErrSuffix is the sidecar that carries the failed live-thread
	// read, so a downgrade names what the host said.
	recheckErrSuffix = "/.recheck.err"
)

const queryLiveCount = `
      query($owner:String!, $name:String!, $pr:Int!, $endCursor:String) {
        repository(owner:$owner, name:$name) {
          pullRequest(number:$pr) {
            reviewThreads(first:100, after:$endCursor) {
              nodes { isResolved }
            }
          }
        }
      }`

func threadsArgv(owner, name, pr string) []string {
	return []string{
		cmdAPI, "graphql", "--paginate",
		"-F", "owner=" + owner, "-F", "name=" + name, "-F", "pr=" + pr,
		"-f", "query=" + queryLiveCount,
	}
}

func splitOwner(repo string) (owner, name string) {
	if before, after, ok := strings.Cut(repo, "/"); ok {
		return before, after
	}
	return repo, ""
}

//nolint:revive // the bounded retry loop branches are the read contract
func runReadRetry(ctx context.Context, cmdr Commander, cmd *core.Command) (core.Result, error) {
	var res core.Result
	var err error
	for attempt := range retry.MaxAttempts {
		if rerr := rewindStdin(cmd); rerr != nil {
			return res, rerr
		}
		res, err = cmdr.Run(ctx, cmd)
		if err != nil {
			return res, jq.Sealed("read retry", err)
		}
		if res.ExitCode == 0 {
			return res, nil
		}
		wait, ok := retry.Delay(res.Stderr, attempt)
		if !ok || attempt >= retry.MaxAttempts-1 {
			break
		}
		if waitErr := awaitRead(ctx, wait); waitErr != nil {
			return res, waitErr
		}
	}
	return res, jq.Sealed("read retry", res.ExitFailure(cmd.Argv[0]))
}

// rewindStdin replays a seekable stdin, so a retried read sends the same
// document its first attempt did.
func rewindStdin(cmd *core.Command) error {
	if seeker, ok := cmd.Stdin.(io.Seeker); ok {
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			return jq.Sealed("rewind read stdin", err)
		}
	}
	return nil
}

func awaitRead(ctx context.Context, wait time.Duration) error {
	if retry.Sleep(ctx, wait) {
		return nil
	}
	return ctx.Err() //nolint:wrapcheck // the context error is the cancellation signal
}

func ghLiveSHA(ctx context.Context, cmdr Commander, repo, pr string) (string, error) {
	if cmdr == nil {
		return "", errNoCommander
	}
	res, err := runReadRetry(ctx, cmdr, &core.Command{
		Argv: []string{toolGH, cmdAPI, "repos/" + repo + "/pulls/" + pr, flagJQ, ".head.sha"},
	})
	if err != nil {
		return "", jq.Sealed("live sha", err)
	}
	return strings.TrimRight(res.Stdout, newline), nil
}

func ghSelfLogin(ctx context.Context, cmdr Commander) (string, error) {
	if cmdr == nil {
		return "", errNoCommander
	}
	res, err := runReadRetry(ctx, cmdr, &core.Command{Argv: []string{toolGH, cmdAPI, "user", flagJQ, ".login"}})
	if err != nil {
		return "", jq.Sealed("self login", err)
	}
	return strings.TrimRight(res.Stdout, newline), nil
}

var errNoCommander = commanderError("no commander in runtime")

type commanderError string

func (e commanderError) Error() string { return string(e) }

var errRecheckShape = shapeError("recheck threads unreadable")

type shapeError string

func (e shapeError) Error() string { return string(e) }

func postRecheck(ctx context.Context, cmdr Commander, repo, pr, dir string) (int, string, error) {
	owner, name := splitOwner(repo)
	var res core.Result
	var err error
	if cmdr == nil {
		err = errNoCommander
	} else {
		res, err = runReadRetry(ctx, cmdr, &core.Command{Argv: append([]string{toolGH}, threadsArgv(owner, name, pr)...)})
	}
	if werr := atomic.WriteErrSidecar(dir+recheckErrSuffix, []byte(res.Stderr)); werr != nil {
		return 0, "", jq.Sealed("write .recheck.err", werr)
	}
	if err != nil {
		return 0, firstReason(res.Stderr), err
	}
	nodes, serr := slurpRecheckNodes(res.Stdout)
	if serr != nil {
		return 0, firstReason(res.Stderr), serr
	}
	count, cerr := countRecheckOpen(nodes)
	if cerr != nil {
		return 0, firstReason(res.Stderr), cerr
	}
	return count, "", nil
}

func firstReason(stderr string) string {
	b := []byte(stderr)
	if len(b) > recheckReasonBytes {
		b = b[:recheckReasonBytes]
	}
	return string(bytesReplaceNewline(b))
}

func bytesReplaceNewline(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if c == '\n' {
			c = ' '
		}
		out[i] = c
	}
	return out
}

func slurpRecheckNodes(stdout string) ([]any, error) {
	docs, err := jq.DecodeSlurp(strings.NewReader(stdout))
	if err != nil {
		return nil, jq.Sealed("slurp recheck", err)
	}
	var out []any
	for _, doc := range docs {
		nodes, ok := recheckPageNodes(doc)
		if !ok {
			return nil, errRecheckShape
		}
		out = append(out, nodes...)
	}
	return out, nil
}

func recheckPageNodes(doc any) ([]any, bool) {
	v, ok := navField(doc, "data")
	if !ok {
		return nil, false
	}
	if v, ok = navField(v, "repository"); !ok {
		return nil, false
	}
	if v, ok = navField(v, "pullRequest"); !ok {
		return nil, false
	}
	if v, ok = navField(v, "reviewThreads"); !ok {
		return nil, false
	}
	if v, ok = navField(v, "nodes"); !ok {
		return nil, false
	}
	nodes, ok := v.([]any)
	if !ok {
		return nil, false
	}
	return nodes, true
}

func navField(v any, key string) (any, bool) {
	if v == nil {
		return nil, true
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return m[key], true
}

func countRecheckOpen(nodes []any) (int, error) {
	n := 0
	for _, node := range nodes {
		m, ok := node.(map[string]any)
		if !ok {
			return 0, errRecheckShape
		}
		if v, has := m["isResolved"]; has && v == false {
			n++
		}
	}
	return n, nil
}

const recheckReasonBytes = 120

// postAttempt is one submission attempt's outcome. posted is the accepted
// receipt object on success; errText is the forge's own words, which the
// retry policy classifies. message is the operator sentence a local refusal
// reports instead of the generic post-failed reason, and it ends the retry,
// because a refusal made before any write cannot improve by repeating it.
// partial marks a run whose payload already landed in part, so replaying the
// whole payload would duplicate it. warnings are the progress notes the
// refusal carries back to the operator.
type postAttempt struct {
	posted   string
	errText  string
	message  string
	warnings []string
	partial  bool
	ok       bool
}

// postFailure records one failed attempt: the reason lands in the .post.err
// sidecar the refusal quotes, and the attempt carries it for the retry
// policy.
func postFailure(dir, reason string, partial bool, warnings []string) postAttempt {
	if werr := atomic.WriteErrSidecar(dir+postErrSuffix, []byte(reason)); werr != nil {
		return postAttempt{}
	}
	return postAttempt{errText: reason, partial: partial, warnings: warnings}
}

// postRefusal records one attempt that refused before the host accepted a
// write. The sentence is the whole refusal message, so it does not read inside
// msgPostFailed's reason slot.
func postRefusal(dir, message string, warnings []string) postAttempt {
	if werr := atomic.WriteErrSidecar(dir+postErrSuffix, []byte(message)); werr != nil {
		return postAttempt{}
	}
	return postAttempt{errText: message, message: message, warnings: warnings}
}

// postAccepted records one accepted attempt and the last call's stderr, so a
// successful run clears (or keeps the advisory text of) the sidecar exactly
// like the single-call path did.
func postAccepted(dir, posted, stderr string, warnings []string) postAttempt {
	if werr := atomic.WriteErrSidecar(dir+postErrSuffix, []byte(stderr)); werr != nil {
		return postAttempt{}
	}
	return postAttempt{posted: posted, ok: true, warnings: warnings}
}

// failureReason prefers the forge's stderr and falls back to the sealed
// error's own words or the exit status, so a refusal never quotes an empty
// sidecar.
func failureReason(res core.Result, err error) string {
	if res.Stderr != "" {
		return res.Stderr
	}
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("exit %d with no stderr", res.ExitCode)
}

// hostLabel names a provider the way its users write it; the retry and
// authorship warnings read the same on every host that can post.
func hostLabel(provider string) string {
	switch provider {
	case providerGitHub:
		return "GitHub"
	case providerGitLab:
		return "GitLab"
	case providerForgejo:
		return "Forgejo"
	default:
		return provider
	}
}

// githubReviewArgv is the single atomic review submit GitHub takes.
func githubReviewArgv(repo, pr string) []string {
	return []string{
		toolGH, cmdAPI, "repos/" + repo + "/pulls/" + pr + "/reviews",
		flagMethod, methodPost, flagInput, "-", flagJQ, postJQFilter,
	}
}

// postOnce submits one payload to the bundle's host through the host seam.
func postOnce(ctx context.Context, cmdr Commander, provider string, env map[string]string,
	repo, pr, dir, payload string,
) postAttempt {
	if cmdr == nil {
		return postFailure(dir, errNoCommander.Error(), false, nil)
	}
	writer, _, _ := hostFor(provider)
	return writer.post(ctx, cmdr, env, core.CapabilitiesFor(provider), repo, pr, dir, payload)
}

func (st *state) submit(ctx context.Context) (*core.PostResponse, *core.RefusalError) {
	cmdr, ref := st.commander()
	if ref != nil {
		return nil, ref
	}
	posted, ref := st.postWithRetry(ctx, cmdr)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	doc, err := jq.DecodeOrdered([]byte(posted))
	obj, ok := doc.(*jq.Obj)
	if err != nil || !ok || obj == nil {
		return nil, st.withWarnings(&core.RefusalError{Code: codePostFailed, Message: fmt.Sprintf(msgPostFailed, headReason(st.dir))})
	}
	if err := atomic.WriteFile(st.dir+"/.posted_"+st.phash, []byte{}); err != nil {
		return nil, st.withWarnings(refusalOrFailed(err))
	}
	if err := writePostReceipt(st, obj); err != nil {
		return nil, st.withWarnings(refusalOrFailed(err))
	}
	return st.successResponse(obj, posted), nil
}

func (st *state) postWithRetry(ctx context.Context, cmdr Commander) (string, *core.RefusalError) {
	posted, ref := st.postOneWithRetry(ctx, cmdr, st.payloads[0])
	if ref != nil {
		return "", ref
	}
	for i, payload := range st.payloads[1:] {
		part, ref := st.postOneWithRetry(ctx, cmdr, payload)
		if ref != nil {
			return "", st.followRefusal(i+2, len(st.payloads), posted, ref.RetryAt)
		}
		st.followPosted(i+2, len(st.payloads), part)
	}
	return posted, nil
}

//nolint:revive // the bounded retry loop branches are the post contract
func (st *state) postOneWithRetry(ctx context.Context, cmdr Commander, payload string) (string, *core.RefusalError) {
	st.retryAt = ""
	var last postAttempt
	for attempt := range retry.MaxAttempts {
		last = postOnce(ctx, cmdr, st.provider(), st.env(), st.req.Repo, st.req.Pr, st.dir, payload)
		st.warnings = append(st.warnings, last.warnings...)
		if last.ok {
			return last.posted, nil
		}
		if last.message != "" {
			break
		}
		wait, canRetry := retry.Delay(last.errText, attempt)
		if last.partial {
			// A replayed payload would post the landed part twice; the
			// refusal names it instead of retrying it. GitHub's single
			// call keeps the documented lost-response retry.
			canRetry = false
		}
		if !canRetry {
			break
		}
		done, ref := st.awaitPostRetry(ctx, wait, attempt)
		if ref != nil {
			return "", ref
		}
		if done {
			break
		}
	}
	message := fmt.Sprintf(msgPostFailed, headReason(st.dir))
	if last.message != "" {
		message = last.message
	}
	return "", &core.RefusalError{Code: codePostFailed, Message: message, RetryAt: st.retryAt}
}

func (st *state) awaitPostRetry(ctx context.Context, wait time.Duration, attempt int) (bool, *core.RefusalError) {
	if attempt >= retry.MaxAttempts-1 {
		st.retryAt = st.retryTime(wait)
		return true, nil
	}
	st.warn(fmt.Sprintf("transient %s error — retry %d/2, comments untouched", hostLabel(st.provider()), attempt+1))
	if !retry.Sleep(ctx, wait) {
		return true, &core.RefusalError{Code: codeCanceled, Message: msgCanceledPost}
	}
	return false, nil
}

func (st *state) retryTime(wait time.Duration) string {
	if st.req != nil {
		if rt := st.req.Runtime; rt != nil && rt.Now != nil {
			return retry.At(rt.Now(), wait)
		}
	}
	return retry.At(time.Now(), wait)
}

func (st *state) followPosted(k, n int, part string) {
	if id := reviewID(part); id != "" {
		st.human(fmt.Sprintf("posted follow-up part %d/%d as review %s", k, n, id))
		return
	}
	st.human(fmt.Sprintf("posted follow-up part %d/%d", k, n))
}

func (st *state) followRefusal(k, n int, posted, retryAt string) *core.RefusalError {
	return &core.RefusalError{Code: codePostFailed, RetryAt: retryAt, Message: fmt.Sprintf(
		"post failed: follow-up part %d/%d not posted (review %s landed part 1 — fix the cause and re-run; part 1 reposts as a duplicate)",
		k, n, reviewID(posted))}
}

func reviewID(posted string) string {
	doc, err := jq.DecodeOrdered([]byte(posted))
	if err != nil {
		return ""
	}
	obj, ok := doc.(*jq.Obj)
	if !ok || obj == nil {
		return ""
	}
	id, _ := obj.Get("id")
	return jq.Interp(id)
}

func headReason(dir string) string {
	raw, err := readOptionalPost(dir + postErrSuffix)
	if err != nil {
		return ""
	}
	if len(raw) > postErrHeadBytes {
		raw = raw[:postErrHeadBytes]
	}
	return string(bytesReplaceNewline(raw))
}

const postErrHeadBytes = 300

// orderedString reads one string field from an ordered JSON object,
// reporting "" when the key is missing or not a string.
func orderedString(obj *jq.Obj, key string) string {
	v, ok := obj.Get(key)
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// orderedStringPtr reads one string field, reporting nil unless the key
// carries a string.
func orderedStringPtr(obj *jq.Obj, key string) *string {
	v, ok := obj.Get(key)
	if !ok {
		return nil
	}
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func writePostReceipt(st *state, obj *jq.Obj) error {
	_, floor, withheld := st.floorPayload()
	receipt := &core.Post{
		Rearmed: st.previousHead != "", PreviousHead: st.previousHead,
		Verification:    st.merged.Verification,
		SchemaVersion:   "1",
		Head:            st.commitID,
		PostedAt:        st.now,
		Confidence:      intOrNilPost(st.merged.Confidence),
		SecuritySummary: strOrNilPost(st.merged.Security),
		ReviewRaw:       obj,
	}
	if floor != "" {
		receipt.PostMinSeverity = &floor
	}
	receipt.Coverage = st.merged.Coverage.Receipt()
	receipt.ChunkCoverage = st.merged.Coverage.ChunkReceipt()
	if len(withheld) > 0 {
		receipt.BelowFloorExcluded = len(withheld)
		receipt.PostFloorScoped = scopedFloors(withheld)
	}
	if st.identity != nil {
		receipt.PatchID, receipt.PatchIDKind = st.identity.PatchID, st.identity.PatchIDKind
	}
	if st.forced != nil {
		receipt.Forced = st.forced
	}
	if st.withheld != nil {
		receipt.Withheld = st.withheld
	}
	stampSecretOverride(st, receipt)
	if err := stampProvenance(st, receipt); err != nil {
		return err
	}
	if err := atomic.WriteFile(st.dir+"/post.json", []byte(jq.PrettyOrdered(receipt.Wire()))); err != nil {
		return &core.RefusalError{Code: codePostFailed, Message: fmt.Sprintf("could not write %s/post.json: %v", st.dir, err)}
	}
	return nil
}

// stampSecretOverride records the value shapes --allow-secret-values published,
// so a receipt proves the override and what it let through; a post that
// published none writes nothing.
func stampSecretOverride(st *state, receipt *core.Post) {
	if st.secretShapes != nil {
		receipt.SecretShapesAllowed = st.secretShapes
	}
}

// stampProvenance copies the recorded tool version, prompts hash, and model
// onto the receipt; a decode failure is sealed, never silently dropped.
func stampProvenance(st *state, receipt *core.Post) error {
	prov, err := jq.DecodeOrdered([]byte(provenance.BundleProvenance(st.req.RootPath(), st.dir)))
	if err != nil {
		return jq.Sealed("bundle provenance", err)
	}
	provObj, ok := prov.(*jq.Obj)
	if !ok {
		return nil
	}
	receipt.ToolVersion, receipt.PromptsHash = orderedString(provObj, "tool_version"), orderedString(provObj, "prompts_hash")
	if model := orderedStringPtr(provObj, "model"); model != nil {
		receipt.Model = model
	}
	return nil
}

func (st *state) successResponse(obj *jq.Obj, posted string) *core.PostResponse {
	payload, _, _ := st.floorPayload()
	nOpen, nDeferred, nAddressed := postCounts(payload)
	var next string
	if nOpen > 0 {
		next = "address open threads"
	} else {
		next = "none open — nothing to address"
	}
	data := &core.PostData{
		Rearmed: st.previousHead != "", PreviousHead: st.previousHead,
		Verification: st.merged.Verification,
		DryRun:       false,
		Event:        st.event,
		CommitID:     st.commitID,
		Body:         core.PostBody{Marker: st.merged.Marker},
		Anchors:      core.AnchorsSummary{Kept: st.merged.NKept, Moved: st.merged.NMoved},
		Suggestions: core.Suggestions{
			Embedded: st.embedded,
		},
		Findings: core.PostFindings{
			Open: nOpen, Deferred: nDeferred, Addressed: nAddressed,
		},
		Items:           typedPostItems(payload),
		Coverage:        st.merged.Coverage.Receipt(),
		Confidence:      intOrNilPost(st.merged.Confidence),
		SecuritySummary: strOrNilPost(st.merged.Security),
		PayloadHash:     st.phash,
		ItemsRaw:        recordcore.EnvelopeItems(payload),
		ReviewRaw:       obj,
	}
	if m, err := reviewMap(obj); err == nil {
		data.Review = m
	}
	prov := decodeProvenanceTyped(st.req.RootPath(), st.dir)
	if prov != nil {
		data.Provenance = *prov
	}
	if !st.req.JSONMode {
		st.human(strings.TrimRight(posted, newline))
	}
	return &core.PostResponse{
		Repo: st.req.Repo, Pr: st.req.Pr, At: st.now,
		Data:     data,
		Warnings: st.warnings,
		Next:     next,
	}
}

func reviewMap(obj *jq.Obj) (map[string]any, error) {
	raw := []byte(jq.PrettyOrdered(obj))
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, jq.Sealed("decode review", err)
	}
	return out, nil
}

func readOptionalPost(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, jq.Sealed("read post err", err)
	}
	return raw, nil
}
