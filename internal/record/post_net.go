package record

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/cli"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/sig"
)

// postJQFilter is the reviews-POST response projection, byte-identical to
// the shell's --jq argument (the stub logs argv verbatim, so the parity
// harness compares these bytes).
const postJQFilter = "{id, state, html_url, submitted_at}"

// ghLiveSHA ports the live-SHA lookup: the fetched PR head, or an error
// when the lookup fails (stderr is discarded, like the shell's 2>/dev/null;
// callers warn and proceed on the fetched hunks).
func ghLiveSHA(ctx context.Context, repo, pr string) (string, error) {
	res, err := readcommon.Gh(ctx, "api", "repos/"+repo+"/pulls/"+pr, "--jq", ".head.sha")
	if err != nil {
		return "", jq.Sealed("live sha", err)
	}
	return strings.TrimRight(res.Stdout, newline), nil
}

// ghSelfLogin ports the authorship lookup: the authenticated login, or an
// error when it cannot be determined (callers warn and proceed).
func ghSelfLogin(ctx context.Context) (string, error) {
	res, err := readcommon.Gh(ctx, "api", "user", "--jq", ".login")
	if err != nil {
		return "", jq.Sealed("self login", err)
	}
	return strings.TrimRight(res.Stdout, newline), nil
}

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
func postRecheck(ctx context.Context, repo, pr, dir string) (int, string, error) {
	owner, name := readcommon.SplitOwner(repo)
	res, err := readcommon.Gh(ctx, readcommon.ThreadsArgv(owner, name, pr, readcommon.QueryLiveCount)...)
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
func postOnce(ctx context.Context, repo, pr, dir, payload string) (posted, errText string, ok bool) {
	res, err := readcommon.GhStdin(ctx, payload+newline,
		"api", "repos/"+repo+"/pulls/"+pr+"/reviews",
		"--method", "POST", "--input", "-", "--jq", postJQFilter)
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
func (p *postRun) submit(ctx context.Context) (int, error) {
	posted, ref := p.postWithRetry(ctx)
	if ref != nil {
		return 0, ref
	}
	doc, err := jq.DecodeOrdered([]byte(posted))
	obj, ok := doc.(*jq.Obj)
	if err != nil || !ok || obj == nil {
		return 0, &cli.RefusalError{Code: codePostFailed, Message: fmt.Sprintf(msgPostFailed, headReason(p.dir))}
	}
	if err := atomic.WriteFile(p.dir+"/.posted_"+p.phash, []byte{}); err != nil {
		return 0, err
	}
	if err := writePostReceipt(p, obj); err != nil {
		return 0, err
	}
	return p.successReport(obj, posted)
}

// postWithRetry ports the retry loop: up to 3 attempts, warning before each
// wait, then a typed post-failed carrying the last attempt's head.
func (p *postRun) postWithRetry(ctx context.Context) (string, error) {
	attempt := 0
	for {
		posted, errText, ok := postOnce(ctx, p.req.Repo, p.req.Pr, p.dir, p.payload)
		if ok {
			return posted, nil
		}
		wait, retry := retryDelay(errText, attempt)
		if !retry || attempt >= postMaxRetries-1 {
			break
		}
		attempt++
		p.runner.Warn(fmt.Sprintf("transient GitHub error — retry %d/2, comments untouched", attempt))
		time.Sleep(wait)
	}
	return "", &cli.RefusalError{Code: codePostFailed, Message: fmt.Sprintf(msgPostFailed, headReason(p.dir))}
}

// postMaxRetries caps POST attempts (the shell's `while (( attempt < 3 ))`).
const postMaxRetries = 3

// headReason ports `$(head -c 300 "$dir/.post.err" | tr '\n' ' ')`: the last
// attempt's head with newlines flattened.
func headReason(dir string) string {
	raw, _ := recordcore.ReadOptional(dir + "/.post.err")
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
func writePostReceipt(p *postRun, obj *jq.Obj) error {
	doc := jq.NewObj(
		"schema_version", "1",
		"review", obj,
		"head", p.commitID,
		"posted_at", p.now,
		"confidence", p.merged.Confidence,
		"security_summary", p.merged.Security,
	)
	prov, err := jq.DecodeOrdered([]byte(provenance.BundleProvenance(p.req.Root, p.dir)))
	if err != nil {
		return jq.Sealed("bundle provenance", err)
	}
	if provObj, ok := prov.(*jq.Obj); ok {
		for _, k := range provObj.Keys {
			v, _ := provObj.Get(k)
			doc.Set(k, v)
		}
	}
	if err := atomic.WriteFile(p.dir+"/post.json", []byte(jq.PrettyOrdered(doc))); err != nil {
		return &cli.RefusalError{Code: codePostFailed, Message: fmt.Sprintf("could not write %s/post.json: %v", p.dir, err)}
	}
	return nil
}

// hostingBodyLimit caps a posted review body: GitHub rejects review bodies
// past 65,536 characters wholesale (422), so the Go port trims with a notice
// instead of submitting a doomed review. The shell keeps today's behavior
// (audit 02 folds into this port; graded Go-only until ticket 08).
const hostingBodyLimit = 65536

// trimNotice names the trim so the posted body stays actionable: the full
// text lives in findings.json, which the run already persisted.
const trimNotice = "\n\n> ⚠️ Note: body exceeded the 65,536-char hosting limit and was trimmed — see findings.json for the full text."

// trimmedBody enforces the hosting body limit as the shared merge's
// pre-footer hook: it runs after the diagram strips (entries already derive
// from the full body) and before the footer receipt, reserving the footer
// block plus its separator so the appended receipt always survives intact.
func (p *postRun) trimmedBody(body string) string {
	if len(body) <= hostingBodyLimit {
		return body
	}
	block, ferr := provenance.RenderFooter(recordcore.FooterOpts(
		&p.runner.Effective, p.req.Forces, p.req.Root, p.dir))
	if ferr != nil {
		block = ""
	}
	sep := ""
	if block != "" {
		sep = "\n\n"
	}
	budget := hostingBodyLimit - len(sep) - len(block) - len(trimNotice)
	head := body
	if len(head) > budget {
		head = head[:budget]
	}
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	p.runner.Warn(fmt.Sprintf("body exceeds the 65,536-char hosting limit — trimmed with a notice (%d chars kept)", len(head)))
	return head + trimNotice
}

// buildPostComments ports the post_comments projection: only GitHub-known
// comment keys ship, suggestions render as fenced blocks inside body, and
// null-valued keys drop (false values ship, like the shell's with_entries).
func buildPostComments(kept []any) []any {
	out := make([]any, 0, len(kept))
	for _, c := range kept {
		m, ok := c.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		out = append(out, projectComment(m))
	}
	return out
}

// projectComment projects one kept comment onto the posted keys.
func projectComment(m *jq.Obj) *jq.Obj {
	out := jq.NewObj()
	for _, k := range []string{fieldPath, fieldLine, "side", fieldBody, fieldStartLine} {
		if v, has := m.Get(k); has && v != nil {
			out.Set(k, v)
		}
	}
	sugg, _ := m.Get(fieldSuggestion)
	if jq.R(jq.Alt(sugg, "")) != "" {
		bd, _ := m.Get(fieldBody)
		out.Set(fieldBody, jq.R(jq.Alt(bd, ""))+"\n\n```suggestion\n"+jq.R(sugg)+"\n```")
	}
	return out
}

// countEmbedded ports the embedded-suggestion count over kept comments.
func countEmbedded(kept []any) int {
	n := 0
	for _, c := range kept {
		m, ok := c.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		sugg, _ := m.Get(fieldSuggestion)
		if jq.R(jq.Alt(sugg, "")) != "" {
			n++
		}
	}
	return n
}

// postDiagramValue ports the dry-run diagram disposition: null when the body
// carried no candidates, else the validated shape with null keyword/note.
func postDiagramValue(dstat *diagram.Status) any {
	if dstat.State == "absent" {
		return nil
	}
	var keyword, note any
	if dstat.Keyword != "" {
		keyword = dstat.Keyword
	}
	if dstat.Note != "" {
		note = dstat.Note
	}
	return jq.NewObj(
		"status", dstat.State,
		"blocks", dstat.Blocks,
		"keyword", keyword,
		"wrapped", dstat.Wrapped,
		"collapsed", dstat.Collapsed,
		"detail", dstat.Detail,
		"note", note,
	)
}

// stringSlice converts warnings to JSON string values.
func stringSlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// postCommentKeys ports the dry-run comment-keys summary: the first
// comment's unsorted keys joined by commas ("" when no comments posted).
func postCommentKeys(comments []any) string {
	if len(comments) == 0 {
		return ""
	}
	m, ok := comments[0].(*jq.Obj)
	if !ok || m == nil {
		return ""
	}
	return strings.Join(m.Keys, ",")
}

// postProvider ports the provider read with the shell's github default:
// missing, unreadable, or undecodable PR.json reads github (only the
// explicit provider marker routes elsewhere).
func postProvider(dir string) string {
	raw, ok := recordcore.ReadOptional(dir + prSuffix)
	if !ok {
		return providerGitHub
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return providerGitHub
	}
	m, ok := doc.(*jq.Obj)
	if !ok || m == nil {
		return providerGitHub
	}
	prov, _ := m.Get("provider")
	return jq.R(jq.Alt(prov, providerGitHub))
}

// providerGitHub is the default provider marker.
const providerGitHub = "github"

// prState ports pr_field's state read: GitHub states lowercase, provider
// states verbatim, "" when unknown.
func prState(dir string) string {
	m := prDoc(dir)
	if m == nil {
		return ""
	}
	prov, _ := m.Get("provider")
	if s, ok := prov.(string); ok && s != "" {
		state, _ := m.Get("state")
		return prScalar(state)
	}
	state, _ := m.Get("state")
	return strings.ToLower(prScalar(state))
}

// prAuthor ports pr_field's author read: `.author.login` on GitHub bundles,
// `.author` on provider bundles, "" when unknown.
func prAuthor(dir string) string {
	m := prDoc(dir)
	if m == nil {
		return ""
	}
	prov, _ := m.Get("provider")
	if s, ok := prov.(string); ok && s != "" {
		author, _ := m.Get("author")
		return prScalar(author)
	}
	return prAuthorLogin(m)
}

// prAuthorLogin reads the GitHub author login from a decoded bundle record.
func prAuthorLogin(m *jq.Obj) string {
	author, _ := m.Get("author")
	am, ok := author.(*jq.Obj)
	if !ok || am == nil {
		return ""
	}
	login, _ := am.Get("login")
	return prScalar(login)
}

// prDoc decodes the bundle PR record, or nil when it is missing or
// undecodable (callers degrade to defaults, like the shell's `|| true`).
func prDoc(dir string) *jq.Obj {
	raw, ok := recordcore.ReadOptional(dir + prSuffix)
	if !ok {
		return nil
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil
	}
	m, ok := doc.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	return m
}

// prScalar renders a pr_field value the way `jq -r '... // empty'` does:
// missing, null, and false read "", everything else renders -r style.
func prScalar(v any) string {
	if v == nil || v == false {
		return ""
	}
	return jq.R(v)
}

// postHead ports `jq -r '.head // empty' post.json`: the stored reviewed
// head, with readable reporting whether the record decoded at all (missing,
// unreadable, empty, and non-object records never arm the finalizer).
func postHead(path string) (string, bool) {
	raw, ok := recordcore.ReadOptional(path)
	if !ok {
		return "", false
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return "", false
	}
	if doc == nil {
		return "", true
	}
	m, ok := doc.(*jq.Obj)
	if !ok || m == nil {
		return "", false
	}
	head, _ := m.Get("head")
	return prScalar(head), true
}

// postTime ports `jq -r '.posted_at // "unknown time"' post.json` for the
// already-posted refusal.
func postTime(path string) string {
	raw, ok := recordcore.ReadOptional(path)
	if !ok {
		return unknownTime
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return unknownTime
	}
	m, ok := doc.(*jq.Obj)
	if !ok || m == nil {
		return unknownTime
	}
	posted, _ := m.Get("posted_at")
	return jq.R(jq.Alt(posted, unknownTime))
}

// decodeProvenance parses the bundle provenance triple for the envelopes.
func decodeProvenance(root, dir string) any {
	doc, err := jq.DecodeOrdered([]byte(provenance.BundleProvenance(root, dir)))
	if err != nil {
		return nil
	}
	return doc
}

// postCounts tallies the merged findings for preflight and envelopes.
func postCounts(entries []any) (open, deferred, addressed int) {
	return recordcore.CountStatus(entries, statusOpen),
		recordcore.CountStatus(entries, "deferred"),
		recordcore.CountStatus(entries, "addressed")
}

// dryRunReport ports the dry-run exit: the payload preview on the human
// stream, or the dry-run envelope (payload hash, footer and diagram
// disposition included) in --json mode. Nothing posts, nothing arms.
func (p *postRun) dryRunReport() (int, error) {
	r := p.runner
	footerText, ferr := provenance.RenderFooter(recordcore.FooterOpts(
		&p.runner.Effective, p.req.Forces, p.req.Root, p.dir))
	if ferr != nil {
		footerText = ""
	}
	var footerVal any
	if footerText != "" {
		footerVal = footerText
	}
	if r.JSONMode {
		p.phash = sig.Sum([]byte(p.payload))
		nOpen, nDeferred, nAddressed := postCounts(p.merged.Entries)
		env := jq.NewObj(
			"schema_version", "1",
			"verb", "post",
			"repo", r.Repo,
			"pr", cli.PrJSON(r.Pr),
			"at", p.now,
			"next", "payload NOT posted (dry-run)",
			"warnings", stringSlice(r.Warnings),
			"data", jq.NewObj(
				"dry_run", true,
				"event", p.event,
				"commit_id", p.commitID,
				"body", jq.NewObj(
					"marker", p.merged.Marker,
					"footer", footerVal,
					"diagram", postDiagramValue(&p.diagram),
				),
				"anchors", jq.NewObj("kept", p.merged.NKept, "moved", p.merged.NMoved),
				"suggestions", jq.NewObj("embedded", p.embedded),
				"findings", jq.NewObj(statusOpen, nOpen, "deferred", nDeferred, "addressed", nAddressed, "file", p.findingsFile),
				"confidence", p.merged.Confidence,
				"security_summary", p.merged.Security,
				"items", recordcore.EnvelopeItems(p.merged.Entries),
				"payload_hash", p.phash,
				"provenance", decodeProvenance(p.req.Root, p.dir),
			),
			"error", nil,
		)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return 0, nil
	}
	fmt.Fprintf(r.Human, "dry-run: payload NOT posted. body chars: %d, payload comment keys: %s\n",
		len(p.body), postCommentKeys(p.comments))
	if footerText != "" {
		fmt.Fprintf(r.Human, "footer preview:\n%s\n", footerText)
	}
	if p.diagram.State != "absent" {
		fmt.Fprintf(r.Human, "diagram: %s — %s\n", p.diagram.State, p.diagram.Detail)
	}
	return 0, nil
}

// successReport ports the real-post exit: the accepted review on the human
// stream, or the post envelope in --json mode.
func (p *postRun) successReport(obj *jq.Obj, posted string) (int, error) {
	r := p.runner
	if r.JSONMode {
		nOpen, nDeferred, nAddressed := postCounts(p.merged.Entries)
		var next string
		if nOpen > 0 {
			next = "address open threads"
		} else {
			next = "none open — nothing to address"
		}
		env := jq.NewObj(
			"schema_version", "1",
			"verb", "post",
			"repo", r.Repo,
			"pr", cli.PrJSON(r.Pr),
			"at", p.now,
			"next", next,
			"warnings", stringSlice(r.Warnings),
			"data", jq.NewObj(
				"dry_run", false,
				"event", p.event,
				"commit_id", p.commitID,
				"body", jq.NewObj("marker", p.merged.Marker),
				"anchors", jq.NewObj("kept", p.merged.NKept, "moved", p.merged.NMoved),
				"suggestions", jq.NewObj("embedded", p.embedded),
				"findings", jq.NewObj(statusOpen, nOpen, "deferred", nDeferred, "addressed", nAddressed),
				"items", recordcore.EnvelopeItems(p.merged.Entries),
				"confidence", p.merged.Confidence,
				"security_summary", p.merged.Security,
				"payload_hash", p.phash,
				"review", obj,
				"provenance", decodeProvenance(p.req.Root, p.dir),
			),
			"error", nil,
		)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return 0, nil
	}
	fmt.Fprintln(r.Human, strings.TrimRight(posted, newline))
	return 0, nil
}
