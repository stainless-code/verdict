package postcore

import (
	"context"
	"encoding/json"
	"fmt"
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

// postJQFilter is the reviews-POST response projection, byte-identical to
// the shell's --jq argument (the stub logs argv verbatim, so the parity
// harness compares these bytes).
const postJQFilter = "{id, state, html_url, submitted_at}"

// ghArgv spells the forge CLI surface the post seam shells through (the
// eval stub logs argv verbatim, so these bytes are pinned).
const (
	toolGH = "gh"
	cmdAPI = "api"
	flagJQ = "--jq"
)

// queryLiveCount is live_open_thread_count's resolution-only read,
// byte-identical to the shell's -f query= argument (indentation included:
// the eval stub logs argv verbatim, so the parity harness compares these
// bytes between implementations).
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

// threadsArgv builds the shared graphql thread-read argv for owner/name/pr.
func threadsArgv(owner, name, pr string) []string {
	return []string{
		cmdAPI, "graphql", "--paginate",
		"-F", "owner=" + owner, "-F", "name=" + name, "-F", "pr=" + pr,
		"-f", "query=" + queryLiveCount,
	}
}

// splitOwner splits owner/repo into its two halves.
func splitOwner(repo string) (owner, name string) {
	if before, after, ok := strings.Cut(repo, "/"); ok {
		return before, after
	}
	return repo, ""
}

// runReadRetry runs one idempotent gh read with the shared bounded backoff:
// a 403/429 secondary rate-limit response honors the retry-after hint or the
// reset marker, an unhinted transient falls back to the attempt-based sleep,
// and the host's internal-error note retries like the original post path.
// Plain failures return their first error, like the shell. The final result
// and error are what callers write into their sidecars and degrade from, so a
// retried read keeps today's warn-or-downgrade posture.
func runReadRetry(ctx context.Context, cmdr Commander, cmd core.Command) (core.Result, error) {
	var res core.Result
	var err error
	for attempt := range retry.MaxAttempts {
		res, err = cmdr.Run(ctx, cmd)
		if err == nil {
			return res, nil
		}
		wait, ok := retry.Delay(res.Stderr, attempt)
		if !ok || attempt >= retry.MaxAttempts-1 {
			break
		}
		time.Sleep(wait)
	}
	if err == nil {
		return res, nil
	}
	return res, jq.Sealed("read retry", err)
}

// ghLiveSHA ports the live-SHA lookup: the fetched PR head, or an error
// when the lookup fails (stderr is discarded, like the shell's 2>/dev/null;
// callers warn and proceed on the fetched hunks). Secondary rate limits and
// transient host errors retry within the shared bound first.
func ghLiveSHA(ctx context.Context, cmdr Commander, repo, pr string) (string, error) {
	if cmdr == nil {
		return "", errNoCommander
	}
	res, err := runReadRetry(ctx, cmdr, core.Command{Argv: []string{toolGH, cmdAPI, "repos/" + repo + "/pulls/" + pr, flagJQ, ".head.sha"}})
	if err != nil {
		return "", jq.Sealed("live sha", err)
	}
	return strings.TrimRight(res.Stdout, newline), nil
}

// ghSelfLogin ports the authorship lookup: the authenticated login, or an
// error when it cannot be determined (callers warn and proceed). Secondary
// rate limits and transient host errors retry within the shared bound first.
func ghSelfLogin(ctx context.Context, cmdr Commander) (string, error) {
	if cmdr == nil {
		return "", errNoCommander
	}
	res, err := runReadRetry(ctx, cmdr, core.Command{Argv: []string{toolGH, cmdAPI, "user", flagJQ, ".login"}})
	if err != nil {
		return "", jq.Sealed("self login", err)
	}
	return strings.TrimRight(res.Stdout, newline), nil
}

// errNoCommander seals a missing-commander call the callers degrade from.
var errNoCommander = commanderError("no commander in runtime")

// commanderError is a plain error value for missing-commander calls.
type commanderError string

// Error implements error.
func (e commanderError) Error() string { return string(e) }

// errRecheckShape seals recheck slurp failures: the shell's jq chain errors
// here (no envelope from jq itself), and the post path maps every such
// failure to the COMMENT downgrade.
var errRecheckShape = shapeError("recheck threads unreadable")

// shapeError is a plain error value for shape failures.
type shapeError string

// Error implements error.
func (e shapeError) Error() string { return string(e) }

// postRecheck ports live_open_thread_count with the shell's exact failure
// semantics: the gh stderr lands in .recheck.err (empty on success), a gh
// failure or an unnavigable payload errors (callers downgrade to COMMENT
// with the file's head as the reason), and the open count follows jq's
// `select(.isResolved == false)` — missing isResolved never counts, and a
// non-object node errors the whole read. Secondary rate limits retry within
// the shared bound before the downgrade, so a transient limit does not turn
// a bindable APPROVE into a COMMENT.
func postRecheck(ctx context.Context, cmdr Commander, repo, pr, dir string) (int, string, error) {
	owner, name := splitOwner(repo)
	var res core.Result
	var err error
	if cmdr == nil {
		err = errNoCommander
	} else {
		res, err = runReadRetry(ctx, cmdr, core.Command{Argv: append([]string{toolGH}, threadsArgv(owner, name, pr)...)})
	}
	if werr := atomic.WriteErrSidecar(dir+"/.recheck.err", []byte(res.Stderr)); werr != nil {
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

// firstReason ports `head -c 120 file | tr '\n' ' '`: the first 120 bytes
// with newlines flattened, byte-exact (no rune-boundary rounding).
func firstReason(stderr string) string {
	b := []byte(stderr)
	if len(b) > recheckReasonBytes {
		b = b[:recheckReasonBytes]
	}
	return string(bytesReplaceNewline(b))
}

// bytesReplaceNewline flattens newlines to spaces without touching any other
// byte (multibyte sequences split at the cut stay byte-identical).
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

// slurpRecheckNodes ports the paginated `jq -s` navigation: concatenated
// docs (one per gh --paginate page), every page navigating to its
// reviewThreads nodes. A missing node list anywhere errors, like the
// shell's `null[]` — even one unnavigable page downgrades the run.
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

// recheckPageNodes navigates one page to its reviewThreads nodes.
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

// navField ports one jq field step: null (or a missing key) reads null, and
// any other non-object errors the chain, like the shell.
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

// countRecheckOpen ports `[.[] | select(.isResolved == false)] | length`:
// only an explicit false counts; a non-object node errors the read.
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

// recheckReasonBytes caps the downgrade reason, like `head -c 120`.
const recheckReasonBytes = 120

// postOnce runs one reviews-POST attempt: the payload plus one newline on
// stdin (exactly what the shell's `echo "$payload" | gh ... --input -`
// pipes), gh stderr truncated into .post.err either way.
func postOnce(ctx context.Context, cmdr Commander, repo, pr, dir, payload string) (posted, errText string, ok bool) {
	if cmdr == nil {
		if werr := atomic.WriteErrSidecar(dir+"/.post.err", []byte("no commander in runtime")); werr != nil {
			return "", "", false
		}
		return "", "no commander in runtime", false
	}
	res, err := cmdr.Run(ctx, core.Command{
		Argv: []string{
			toolGH, cmdAPI, "repos/" + repo + "/pulls/" + pr + "/reviews",
			"--method", "POST", "--input", "-", flagJQ, postJQFilter,
		},
		Stdin: strings.NewReader(payload + newline),
	})
	if werr := atomic.WriteErrSidecar(dir+"/.post.err", []byte(res.Stderr)); werr != nil {
		return "", "", false
	}
	if err != nil {
		return "", res.Stderr, false
	}
	return strings.TrimRight(res.Stdout, newline), "", true
}

// submit runs the bounded retry loop and publishes: the first part posts as
// the review and each follow-up posts as a COMMENT review. The marker and
// receipt land only after every part decodes, so partial failures never arm
// anything — like every other post failure.
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

// postWithRetry ports the retry loop: the first part posts as the review,
// each follow-up posts as a COMMENT review, up to 3 attempts per part with
// a warning before each wait. A follow-up failure refuses typed, naming the
// landed first review so the operator can finish the remainder by hand.
func (st *state) postWithRetry(ctx context.Context, cmdr Commander) (string, *core.RefusalError) {
	posted, ref := st.postOneWithRetry(ctx, cmdr, st.payloads[0])
	if ref != nil {
		return "", ref
	}
	for i, payload := range st.payloads[1:] {
		part, ref := st.postOneWithRetry(ctx, cmdr, payload)
		if ref != nil {
			return "", st.followRefusal(i+2, len(st.payloads), posted)
		}
		st.followPosted(i+2, len(st.payloads), part)
	}
	return posted, nil
}

// postOneWithRetry ports one part's retry loop: up to retry.MaxAttempts
// attempts, warning before each wait, then a typed post-failed carrying the
// last attempt's head.
func (st *state) postOneWithRetry(ctx context.Context, cmdr Commander, payload string) (string, *core.RefusalError) {
	for attempt := range retry.MaxAttempts {
		posted, errText, ok := postOnce(ctx, cmdr, st.req.Repo, st.req.Pr, st.dir, payload)
		if ok {
			return posted, nil
		}
		wait, canRetry := retry.Delay(errText, attempt)
		if !canRetry || attempt >= retry.MaxAttempts-1 {
			break
		}
		st.warn(fmt.Sprintf("transient GitHub error — retry %d/2, comments untouched", attempt+1))
		time.Sleep(wait)
	}
	return "", &core.RefusalError{Code: codePostFailed, Message: fmt.Sprintf(msgPostFailed, headReason(st.dir))}
}

// followPosted reports one landed follow-up review on the human stream.
func (st *state) followPosted(k, n int, part string) {
	if id := reviewID(part); id != "" {
		st.human(fmt.Sprintf("posted follow-up part %d/%d as review %s", k, n, id))
		return
	}
	st.human(fmt.Sprintf("posted follow-up part %d/%d", k, n))
}

// followRefusal ports the partial-post failure: the first review already
// landed, so the refusal names it and says how to finish — the marker stays
// unarmed (failures never arm), and a re-run reposts every part.
func (st *state) followRefusal(k, n int, posted string) *core.RefusalError {
	return &core.RefusalError{Code: codePostFailed, Message: fmt.Sprintf(
		"post failed: follow-up part %d/%d not posted (review %s landed part 1 — fix the cause and re-run; part 1 reposts as a duplicate)",
		k, n, reviewID(posted))}
}

// reviewID reads one accepted review's id for progress lines, "" when the
// response does not carry one.
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

// headReason ports `$(head -c 300 "$dir/.post.err" | tr '\n' ' ')`: the last
// attempt's head with newlines flattened.
func headReason(dir string) string {
	raw, err := readOptionalPost(dir + "/.post.err")
	if err != nil {
		return ""
	}
	if len(raw) > postErrHeadBytes {
		raw = raw[:postErrHeadBytes]
	}
	return string(bytesReplaceNewline(raw))
}

// postErrHeadBytes caps the post-failed reason, like `head -c 300`.
const postErrHeadBytes = 300

// writePostReceipt ports the post.json writer: the review GitHub accepted,
// the reviewed head, timestamps, confidence, and the provenance triple —
// through the shared atomic path, so an interrupted write keeps the last
// good receipt byte-identical (audit 19).
//
//nolint:gocognit,revive // the receipt assembles the fixed wire order plus the provenance triple in one place
func writePostReceipt(st *state, obj *jq.Obj) error {
	receipt := &core.Post{
		SchemaVersion:   "1",
		Head:            st.commitID,
		PostedAt:        st.now,
		Confidence:      intOrNilPost(st.merged.Confidence),
		SecuritySummary: strOrNilPost(st.merged.Security),
		ReviewRaw:       obj,
	}
	prov, err := jq.DecodeOrdered([]byte(provenance.BundleProvenance(st.req.RootPath(), st.dir)))
	if err != nil {
		return jq.Sealed("bundle provenance", err)
	}
	if provObj, ok := prov.(*jq.Obj); ok {
		if v, _ := provObj.Get("tool_version"); v != nil {
			if s, ok := v.(string); ok {
				receipt.ToolVersion = s
			}
		}
		if v, _ := provObj.Get("prompts_hash"); v != nil {
			if s, ok := v.(string); ok {
				receipt.PromptsHash = s
			}
		}
		if v, _ := provObj.Get("model"); v != nil {
			if s, ok := v.(string); ok {
				receipt.Model = &s
			}
		}
	}
	if err := atomic.WriteFile(st.dir+"/post.json", []byte(jq.PrettyOrdered(receipt.Wire()))); err != nil {
		return &core.RefusalError{Code: codePostFailed, Message: fmt.Sprintf("could not write %s/post.json: %v", st.dir, err)}
	}
	return nil
}

// successResponse ports the real-post exit: the accepted review on the human
// stream, or the post envelope inputs in --json mode.
func (st *state) successResponse(obj *jq.Obj, posted string) *core.PostResponse {
	nOpen, nDeferred, nAddressed := postCounts(st.merged.Entries)
	var next string
	if nOpen > 0 {
		next = "address open threads"
	} else {
		next = "none open — nothing to address"
	}
	data := &core.PostData{
		DryRun:   false,
		Event:    st.event,
		CommitID: st.commitID,
		Body:     core.PostBody{Marker: st.merged.Marker},
		Anchors:  core.AnchorsSummary{Kept: st.merged.NKept, Moved: st.merged.NMoved},
		Suggestions: core.Suggestions{
			Embedded: st.embedded,
		},
		Findings: core.PostFindings{
			Open: stOpen(nOpen), Deferred: nDeferred, Addressed: nAddressed,
		},
		Items:           typedPostItems(st.merged.Entries),
		Confidence:      intOrNilPost(st.merged.Confidence),
		SecuritySummary: strOrNilPost(st.merged.Security),
		PayloadHash:     st.phash,
		ItemsRaw:        recordcore.EnvelopeItems(st.merged.Entries),
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

// reviewMap decodes the accepted review into the typed map for schema
// conformance (the wire keeps the ordered raw object).
func reviewMap(obj *jq.Obj) (map[string]any, error) {
	raw := []byte(jq.PrettyOrdered(obj))
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, jq.Sealed("decode review", err)
	}
	return out, nil
}

// readOptionalPost reads one optional file for the post-failed reason.
func readOptionalPost(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, jq.Sealed("read post err", err)
	}
	return raw, nil
}
