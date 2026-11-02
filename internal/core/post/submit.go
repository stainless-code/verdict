package postcore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
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

// ghLiveSHA ports the live-SHA lookup: the fetched PR head, or an error
// when the lookup fails (stderr is discarded, like the shell's 2>/dev/null;
// callers warn and proceed on the fetched hunks).
func ghLiveSHA(ctx context.Context, cmdr Commander, repo, pr string) (string, error) {
	if cmdr == nil {
		return "", errNoCommander
	}
	res, err := cmdr.Run(ctx, core.Command{Argv: []string{toolGH, cmdAPI, "repos/" + repo + "/pulls/" + pr, flagJQ, ".head.sha"}})
	if err != nil {
		return "", jq.Sealed("live sha", err)
	}
	return strings.TrimRight(res.Stdout, newline), nil
}

// ghSelfLogin ports the authorship lookup: the authenticated login, or an
// error when it cannot be determined (callers warn and proceed).
func ghSelfLogin(ctx context.Context, cmdr Commander) (string, error) {
	if cmdr == nil {
		return "", errNoCommander
	}
	res, err := cmdr.Run(ctx, core.Command{Argv: []string{toolGH, cmdAPI, "user", flagJQ, ".login"}})
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
// non-object node errors the whole read.
func postRecheck(ctx context.Context, cmdr Commander, repo, pr, dir string) (int, string, error) {
	owner, name := splitOwner(repo)
	var res core.Result
	var err error
	if cmdr == nil {
		err = errNoCommander
	} else {
		res, err = cmdr.Run(ctx, core.Command{Argv: append([]string{toolGH}, threadsArgv(owner, name, pr)...)})
	}
	if werr := atomic.WriteFile(dir+"/.recheck.err", []byte(res.Stderr)); werr != nil {
		return 0, "", werr
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

// maxBackoff caps one retry sleep: retries stay bounded (3 attempts, 60s
// each) so a rate-limited run waits its turn instead of dying, without ever
// hanging the operator.
const maxBackoff = 60 * time.Second

// recheckReasonBytes caps the downgrade reason, like `head -c 120`.
const recheckReasonBytes = 120

// retryAfterRe matches a Retry-After hint (`Retry-After: 12`) and the loose
// `retry after 12 seconds` wording, case-insensitively.
var retryAfterRe = regexp.MustCompile(`(?i)retry-after[\s:="]+(\d+)|retry\s+after\s+(\d+)`)

// resetEpochRe matches a rate-limit reset marker with its epoch.
var resetEpochRe = regexp.MustCompile(`(?i)(?:x-)?rate-?limit-reset[\s:="]+(\d+)`)

// retryDelay ports the bounded transient retry and folds audit 21 in as
// structure: the shell's exact internal-error retry (2 retries, 1s/2s,
// same warning), plus secondary rate-limit responses (403/429 with a
// rate-limit signal) honoring the retry-after hint or the reset marker.
// Only failed calls retry, so a retry can never publish twice (the
// double-post guard holds: the marker lands only after a success).
func retryDelay(stderr string, attempt int) (time.Duration, bool) {
	base := time.Duration(attempt+1) * time.Second
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "internal error occurred, please try again") {
		return base, true
	}
	wait, hinted, ok := rateLimitWait(stderr, lower)
	if !ok {
		return 0, false
	}
	if !hinted {
		wait = base
	}
	return min(wait, maxBackoff), true
}

// rateLimitWait detects a secondary rate-limit response and its wait: the
// retry-after hint wins, else the reset epoch, else no hint (callers fall
// back to the attempt-based sleep).
func rateLimitWait(stderr, lower string) (wait time.Duration, hinted, ok bool) {
	if !isRateLimited(lower) {
		return 0, false, false
	}
	if secs, found := parseRetryAfter(stderr); found {
		return time.Duration(secs) * time.Second, true, true
	}
	if epoch, found := parseResetEpoch(stderr); found {
		wait := max(time.Until(time.Unix(epoch, 0)), 0)
		return wait, true, true
	}
	return 0, false, true
}

// isRateLimited reports whether gh stderr carries a retryable rate-limit
// signal: an explicit secondary-limit note, a retry hint, or a 403/429
// naming the rate limit. Plain auth or validation 403s never match.
func isRateLimited(lower string) bool {
	norm := strings.Map(keepLetters, lower)
	if strings.Contains(norm, "secondaryratelimit") {
		return true
	}
	if strings.Contains(norm, "retryafter") {
		return true
	}
	if strings.Contains(norm, "ratelimitreset") {
		return true
	}
	if strings.Contains(norm, "ratelimit") && (strings.Contains(norm, "429") || strings.Contains(norm, "403")) {
		return true
	}
	return false
}

// keepLetters drops everything but lowercase letters for signal matching
// (`rate limit`, `rate-limit`, and `ratelimit` read identically).
func keepLetters(c rune) rune {
	if c >= 'a' && c <= 'z' {
		return c
	}
	if c >= '0' && c <= '9' {
		return c
	}
	return -1
}

// parseRetryAfter reads a retry-after wait in seconds.
func parseRetryAfter(stderr string) (int64, bool) {
	m := retryAfterRe.FindStringSubmatch(stderr)
	if len(m) == 0 {
		return 0, false
	}
	for _, g := range m[1:] {
		if g == "" {
			continue
		}
		var secs int64
		if _, err := fmt.Sscanf(g, "%d", &secs); err == nil {
			return secs, true
		}
	}
	return 0, false
}

// parseResetEpoch reads a rate-limit reset epoch.
func parseResetEpoch(stderr string) (int64, bool) {
	m := resetEpochRe.FindStringSubmatch(stderr)
	if len(m) < 2 || m[1] == "" {
		return 0, false
	}
	var epoch int64
	if _, err := fmt.Sscanf(m[1], "%d", &epoch); err != nil {
		return 0, false
	}
	return epoch, true
}

// postOnce runs one reviews-POST attempt: the payload plus one newline on
// stdin (exactly what the shell's `echo "$payload" | gh ... --input -`
// pipes), gh stderr truncated into .post.err either way.
func postOnce(ctx context.Context, cmdr Commander, repo, pr, dir, payload string) (posted, errText string, ok bool) {
	if cmdr == nil {
		if werr := atomic.WriteFile(dir+"/.post.err", []byte("no commander in runtime")); werr != nil {
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
	if werr := atomic.WriteFile(dir+"/.post.err", []byte(res.Stderr)); werr != nil {
		return "", "", false
	}
	if err != nil {
		return "", res.Stderr, false
	}
	return strings.TrimRight(res.Stdout, newline), "", true
}

// submit runs the bounded retry loop and publishes: the marker and receipt
// land only after a decoded review object, so failures never arm anything.
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

// postWithRetry ports the retry loop: up to 3 attempts, warning before each
// wait, then a typed post-failed carrying the last attempt's head.
func (st *state) postWithRetry(ctx context.Context, cmdr Commander) (string, *core.RefusalError) {
	attempt := 0
	for {
		posted, errText, ok := postOnce(ctx, cmdr, st.req.Repo, st.req.Pr, st.dir, st.payload)
		if ok {
			return posted, nil
		}
		wait, retry := retryDelay(errText, attempt)
		if !retry || attempt >= postMaxRetries-1 {
			break
		}
		attempt++
		st.warn(fmt.Sprintf("transient GitHub error — retry %d/2, comments untouched", attempt))
		time.Sleep(wait)
	}
	return "", &core.RefusalError{Code: codePostFailed, Message: fmt.Sprintf(msgPostFailed, headReason(st.dir))}
}

// postMaxRetries caps POST attempts (the shell's `while (( attempt < 3 ))`).
const postMaxRetries = 3

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
