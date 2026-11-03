// Package readycore is the ready verb's domain service: the readiness
// advisory over live PR state (checks, threads, draft/conflict,
// description). Advisory runs exit 0; --check gates for hooks and CI. All
// gh reads go through the injected commander; the probe stderr stages
// beside the bundle like the shell's 2> redirect. internal/ready owns argv
// parsing, progress routing, envelope rendering, and exit mapping.
package readycore

import (
	"context"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request carries the resolved ready invocation: the shared read runtime
// plus the --check gate flag.
type Request struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	Check    bool
	Warnings []string
}

// Response carries the completed advisory: the envelope fields, the gate
// outcome, and the typed data profile. Human lines stream live through the
// runtime progress observer, so the response carries no rendered lines.
type Response struct { //nolint:govet // field grouping documents the response surface
	Repo       string
	Pr         string
	At         string
	Next       string
	Warnings   []string
	Data       *Data
	Ready      bool
	GateFailed bool
}

// Data is the ready verb's --json data profile. Field order is the shell's
// jq construction order.
type Data struct { //nolint:govet // field order is the published property order
	Ready            bool
	Recommendation   string
	Blockers         []Blocker
	State            string
	Draft            bool
	Mergeable        string
	MergeState       string
	CI               CICounts
	ThreadsOpen      int
	DescriptionChars int
}

// Blocker is one readiness blocker with its recovery action.
type Blocker struct {
	Kind   string
	Detail string
	Action string
}

// CICounts is the live CI check tally.
type CICounts struct {
	Failing int
	Total   int
}

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	blockers := make([]any, 0, len(d.Blockers))
	for _, b := range d.Blockers {
		blockers = append(blockers, jq.NewObj(
			"kind", b.Kind,
			"detail", b.Detail,
			"action", b.Action,
		))
	}
	return jq.NewObj(
		"ready", d.Ready,
		"recommendation", d.Recommendation,
		"blockers", blockers,
		"state", d.State,
		"draft", d.Draft,
		"mergeable", d.Mergeable,
		"mergeStateStatus", d.MergeState,
		"ci", jq.NewObj("failing", d.CI.Failing, "total", d.CI.Total),
		"threads_open", d.ThreadsOpen,
		"description_chars", d.DescriptionChars,
	)
}

// state carries the loaded ready run.
type state struct {
	req         *Request
	warnings    []string
	live        map[string]any
	pr          string
	rec         string
	mergeable   string
	mergeState  string
	prState     string
	repo        string
	blockers    []Blocker
	threadsOpen int
	bodyLen     int
	ciTotal     int
	ciFail      int
	ready       bool
	draft       bool
}

// recWait holds the review instead of reviewing.
const recWait = "wait"

// hashSep joins repo and pr in human diagnostics.
const hashSep = "#"

// mergeUnknown reads when GitHub has not computed mergeability yet.
const mergeUnknown = "UNKNOWN"

// newline splits description lines.
const newline = "\n"

// commentOpen and commentClose bracket HTML comment spans.
const (
	commentOpen  = "<!--"
	commentClose = "-->"
)

// Run executes the readiness advisory over live PR state.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{
		req:      req,
		repo:     req.Repo,
		pr:       req.Pr,
		warnings: append([]string{}, req.Warnings...),
	}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before ready completed"}
	}
	if ref := loadState(ctx, st); ref != nil {
		return nil, ref
	}
	return st.finish(), nil
}

// warn records and streams one warning.
func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

// human streams one human output line.
func (st *state) human(line string) {
	if st.req.Runtime != nil {
		st.req.Human(line)
	}
}

// diag streams one raw diagnostic line.
func (st *state) diag(line string) {
	if st.req.Runtime != nil {
		st.req.Diag(line)
	}
}

// withWarnings attaches the accumulated warnings to a refusal so the
// adapter's envelope stays byte-identical to a success run's warning list.
func (st *state) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

// commander returns the injected commander (nil when the caller set none).
func (st *state) commander() core.Commander {
	var commander core.Commander
	if rt := st.runtime(); rt != nil {
		commander = rt.Commander
	}
	return commander
}

// runtime returns the injected runtime (nil when the caller set none).
func (st *state) runtime() *core.Runtime {
	return st.req.Runtime
}

// finish ports the advisory emit plus the --check gate.
func (st *state) finish() *Response {
	st.emitHuman()
	next := "nudge the author instead of reviewing"
	switch st.rec {
	case "review":
		next = "fetch + review"
	case recWait:
		next = "hold the review — re-run ready later"
	}
	return &Response{
		Repo:       st.repo,
		Pr:         st.pr,
		At:         st.req.TimeUTC(),
		Next:       next,
		Warnings:   st.warnings,
		Data:       st.data(),
		Ready:      st.ready,
		GateFailed: st.req.Check && !st.ready,
	}
}

// data assembles the typed data profile in wire order.
func (st *state) data() *Data {
	return &Data{
		Ready:            st.ready,
		Recommendation:   st.rec,
		Blockers:         st.blockers,
		State:            st.prState,
		Draft:            st.draft,
		Mergeable:        st.mergeable,
		MergeState:       st.mergeState,
		CI:               CICounts{Failing: st.ciFail, Total: st.ciTotal},
		ThreadsOpen:      st.threadsOpen,
		DescriptionChars: st.bodyLen,
	}
}

// loadState resolves the bundle and reads every live dimension: PR state,
// checks, threads, and the description strip.
func loadState(ctx context.Context, st *state) *core.RefusalError {
	dir := readcommon.ResolveBundleDir(st.pr)
	if isLocalDir(dir) {
		return st.withWarnings(&core.RefusalError{
			Code:    "no-pr-target",
			Message: "ready needs a PR bundle (fetch owner/repo N first); local bundles have no live checks to advise on",
		})
	}
	if prov := readcommon.ProviderOf(dir); prov != "" && prov != providerGitHub {
		return st.withWarnings(&core.RefusalError{
			Code:    "provider-unimplemented",
			Message: "ready on " + prov + " is not implemented yet (no live gh reads for it)",
		})
	}
	if !isRegular(dir + "/PR.json") {
		return st.withWarnings(&core.RefusalError{
			Code:    "missing-bundle",
			Message: "missing " + dir + "/PR.json (run fetch " + st.repo + " " + st.pr + " first)",
		})
	}
	if ref := loadLive(ctx, st, dir); ref != nil {
		return ref
	}
	loadChecks(ctx, st)
	if ref := loadThreads(ctx, st); ref != nil {
		return ref
	}
	computeReadiness(st)
	return nil
}

// providerGitHub is the only provider with live reads.
const providerGitHub = "github"

// isLocalDir reports whether dir is a local bundle.
func isLocalDir(dir string) bool {
	return strings.HasPrefix(dir, ".review/local-")
}

// isRegular reports whether path is a regular file.
func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// loadLive reads the live PR state, staging gh's stderr beside the bundle
// exactly like the shell's 2> redirect (the file stays: empty on success).
func loadLive(ctx context.Context, st *state, dir string) *core.RefusalError {
	stdout, stderr, ok := coreexec.Gh(ctx, st.commander(), "pr", "view", st.pr, "--repo", st.repo,
		"--json", "isDraft,mergeable,mergeStateStatus,body,state,headRefOid")
	writeErrFile(st, dir+"/.ready.err", stderr)
	if !ok {
		return st.withWarnings(&core.RefusalError{
			Code:    "pr-read-failed",
			Message: "could not read live PR state for " + st.repo + hashSep + st.pr + ": " + firstLine(stderr),
		})
	}
	doc, derr := jq.Decode([]byte(stdout))
	if derr != nil {
		st.live = map[string]any{}
	} else if m, ok := jq.AsMap(doc); ok {
		st.live = m
	} else {
		st.live = map[string]any{}
	}
	fetched := readcommon.PrField(dir, "head_sha")
	liveHead := liveString(st.live, "headRefOid")
	if fetched != "" && liveHead != "" && fetched != liveHead {
		st.warn("bundle stale (fetched " + readcommon.ShortSHA(fetched) + ", live " +
			readcommon.ShortSHA(liveHead) + ") — re-fetch before review, advice reads live")
	}
	st.draft = liveString(st.live, "isDraft") == "true"
	st.mergeable = liveOr(st.live, "mergeable", mergeUnknown)
	st.mergeState = liveOr(st.live, "mergeStateStatus", mergeUnknown)
	st.prState = liveOr(st.live, "state", mergeUnknown)
	st.bodyLen = bodyLength(liveBody(st.live))
	if st.mergeable == mergeUnknown && st.mergeState == mergeUnknown {
		st.warn("mergeability UNKNOWN (GitHub still computing?) — conflict treated as absent, re-run ready if this persists")
	}
	return nil
}

// probeStagePerm stages probe stderr readable only by the owner: the
// content can carry token-adjacent gh diagnostics.
const probeStagePerm = 0o600

// writeErrFile stages probe stderr beside the bundle through the shared
// atomic path. The write is best-effort like the shell's 2> redirect, so a
// failure warns instead of refusing: the file is a debug side channel and
// the run must not fail over garnish.
func writeErrFile(st *state, path, stderr string) {
	if err := atomic.WriteFileMode(path, []byte(stderr), probeStagePerm); err != nil {
		// Un-actionable beyond the notice: the live data already loaded.
		st.diag("ready: could not stage probe stderr in " + path + ": " + err.Error())
	}
}

// firstLine ports `head -c 200 | tr '\n' ' '`: the first 200 bytes with
// newlines flattened.
func firstLine(s string) string {
	if len(s) > errHeadBytes {
		s = s[:errHeadBytes]
	}
	return strings.ReplaceAll(s, newline, " ")
}

// errHeadBytes caps the PR-view error detail.
const errHeadBytes = 200

// liveString renders one live field jq -r style, defaulting to "".
func liveString(live map[string]any, key string) string {
	v, ok := live[key]
	if !ok || v == nil {
		return ""
	}
	return jq.R(v)
}

// liveOr renders one live field with a default for missing values.
func liveOr(live map[string]any, key, dflt string) string {
	v, ok := live[key]
	if !ok || v == nil {
		return dflt
	}
	return jq.R(v)
}

// liveBody reads the live body text ("" when absent).
func liveBody(live map[string]any) string {
	v, ok := live["body"]
	if !ok || v == nil {
		return ""
	}
	return jq.R(v)
}

// bodyLength ports the description completeness measure: HTML-comment spans
// stripped (multiline-aware), whitespace runs squeezed, byte count.
func bodyLength(body string) int {
	var out strings.Builder
	inComment := false
	for line := range strings.SplitSeq(body, newline) {
		out.WriteString(stripComments(line, &inComment))
		out.WriteByte('\n')
	}
	return len(squeezeSpace(out.String()))
}

// stripComments removes <!-- --> spans from one line, carrying the
// in-comment state across lines like the shell's awk.
func stripComments(line string, inComment *bool) string {
	var kept strings.Builder
	rest := line
	for len(rest) > 0 {
		if *inComment {
			rest = skipCommentRest(rest, inComment)
			continue
		}
		rest = takePlainSegment(rest, inComment, &kept)
	}
	return kept.String()
}

// skipCommentRest drops through the closing --> (or the whole line when
// the comment continues past it), reporting the remainder.
func skipCommentRest(rest string, inComment *bool) string {
	if _, after, ok := strings.Cut(rest, commentClose); ok {
		*inComment = false
		return after
	}
	return ""
}

// takePlainSegment copies through the next <!-- opener (or the whole line
// when none opens), flipping into comment state past the opener.
func takePlainSegment(rest string, inComment *bool, kept *strings.Builder) string {
	if before, after, ok := strings.Cut(rest, commentOpen); ok {
		kept.WriteString(before)
		*inComment = true
		return after
	}
	kept.WriteString(rest)
	return ""
}

// squeezeSpace ports `tr -s '[:space:]' ' '`: whitespace runs collapse to
// one space (C-locale set: space, tab, newline, vertical tab, form feed,
// carriage return).
func squeezeSpace(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	inRun := false
	for i := range len(s) {
		if isSpaceByte(s[i]) {
			if !inRun {
				out.WriteByte(' ')
				inRun = true
			}
			continue
		}
		out.WriteByte(s[i])
		inRun = false
	}
	return out.String()
}

// isSpaceByte reports C-locale whitespace.
func isSpaceByte(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

// loadChecks reads the live CI state: unreadable checks read unknown,
// never blocking.
func loadChecks(ctx context.Context, st *state) {
	stdout, _, ok := coreexec.Gh(ctx, st.commander(), "pr", "checks", st.pr, "--repo", st.repo,
		"--json", "bucket,name,state")
	raw := "[]"
	if ok {
		raw = stdout
	}
	list := checkElements(raw)
	failNames := []string{}
	anomaly := false
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			anomaly = true
			break
		}
		failNames = countFailBucket(st, m, failNames)
	}
	if anomaly {
		st.ciFail = 0
		st.ciTotal = 0
		return
	}
	st.ciTotal = len(list)
	if st.ciFail > 0 {
		st.blockers = append(st.blockers, Blocker{
			Kind:   "ci-failing",
			Detail: strconv.Itoa(st.ciFail) + " failing check(s): " + strings.Join(failNames, ", "),
			Action: "fix CI, then re-run ready",
		})
	}
}

// countFailBucket ports one checks element: fail buckets bump the failing
// count and name list, every other bucket reads skipped.
func countFailBucket(st *state, m map[string]any, failNames []string) []string {
	bucket, ok := m["bucket"].(string)
	if !ok || bucket != "fail" {
		return failNames
	}
	st.ciFail++
	return append(failNames, checkName(m))
}

// checkElements ports `[.[]] | length` over the checks payload: arrays
// pass through, objects contribute values, null reads empty, anything
// else reads empty.
func checkElements(raw string) []any {
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		return []any{}
	}
	switch t := doc.(type) {
	case []any:
		return t
	case map[string]any:
		vals := make([]any, 0, len(t))
		for _, k := range sortedKeys(t) {
			vals = append(vals, t[k])
		}
		return vals
	case nil:
		return []any{}
	default:
		return []any{}
	}
}

// checkName renders one failing check name jq -r style.
func checkName(m map[string]any) string {
	v, ok := m["name"]
	if !ok || v == nil {
		return "null"
	}
	return jq.R(v)
}

// loadThreads counts unresolved live threads: resolution state exists
// only live, so the bundle's thread files never count. The shared helper
// applies the fail-closed posture (malformed nodes count as unresolved),
// and a malformed read warns before it can flip the gate.
func loadThreads(ctx context.Context, st *state) *core.RefusalError {
	owner, name := readcommon.SplitOwner(st.repo)
	nodes, _, err := readcommon.Threads(ctx, st.commander(), owner, name, st.pr, false)
	if err != nil {
		return st.withWarnings(&core.RefusalError{
			Code:    "threads-failed",
			Message: "could not read live review threads for " + st.repo + hashSep + st.pr,
		})
	}
	if malformed := readcommon.MalformedThreadCount(nodes); malformed > 0 {
		st.warn(strconv.Itoa(malformed) + " live thread node(s) carry no readable resolution state " +
			"— counted as unresolved (fail-closed)")
	}
	st.threadsOpen = readcommon.OpenThreadCount(nodes)
	if st.threadsOpen > 0 {
		st.blockers = append(st.blockers, Blocker{
			Kind:   "threads-open",
			Detail: strconv.Itoa(st.threadsOpen) + " unresolved thread(s)",
			Action: "address or resolve open threads, then re-run ready",
		})
	}
	return nil
}

// computeReadiness ports the ready/rec decision plus the draft, conflict,
// and description-thin blockers.
func computeReadiness(st *state) {
	appendDraftBlocker(st)
	appendConflictBlocker(st)
	appendDescriptionBlocker(st)
	st.ready = true
	st.rec = "review"
	if st.prState != "OPEN" {
		st.ready = false
		st.rec = recWait
		return
	}
	if len(st.blockers) > 0 {
		st.ready = false
		if st.draft {
			st.rec = recWait
		} else {
			st.rec = "nudge-author"
		}
	}
}

// appendDraftBlocker ports the draft blocker.
func appendDraftBlocker(st *state) {
	if st.draft {
		st.blockers = append(st.blockers, Blocker{
			Kind:   "draft",
			Detail: "PR is still a draft",
			Action: "mark ready for review when done, then re-run ready",
		})
	}
}

// appendConflictBlocker ports the merge-conflict blocker.
func appendConflictBlocker(st *state) {
	if st.mergeable == "CONFLICTING" || st.mergeState == "DIRTY" {
		st.blockers = append(st.blockers, Blocker{
			Kind:   "conflict",
			Detail: "mergeable=" + st.mergeable + " mergeStateStatus=" + st.mergeState,
			Action: "rebase onto base and resolve conflicts, then re-run ready",
		})
	}
}

// appendDescriptionBlocker ports the description-thin blocker.
func appendDescriptionBlocker(st *state) {
	if st.bodyLen < descriptionThin {
		st.blockers = append(st.blockers, Blocker{
			Kind:   "description-thin",
			Detail: "description ~" + strconv.Itoa(st.bodyLen) + " chars (<50)",
			Action: "expand the description (what/why/testing), then re-run ready",
		})
	}
}

// descriptionThin is the description completeness floor.
const descriptionThin = 50

// emitHuman ports the advisory lines plus the recommendation.
func (st *state) emitHuman() {
	if st.ready {
		st.human("ready: yes — " + st.repo + hashSep + st.pr + " looks reviewable " +
			"(CI " + strconv.Itoa(st.ciTotal) + " checks, 0 failing; " +
			strconv.Itoa(st.threadsOpen) + " open threads; mergeable " + st.mergeable +
			"; body " + strconv.Itoa(st.bodyLen) + " chars)")
		st.human("recommendation: review — fetch + review")
		return
	}
	if st.prState != "OPEN" {
		st.human("ready: no — " + st.repo + hashSep + st.pr + " is " + st.prState + ", nothing to review")
	} else {
		st.human("ready: no — " + st.repo + hashSep + st.pr + " has " +
			strconv.Itoa(len(st.blockers)) + " blocker(s):")
		for _, b := range st.blockers {
			st.human("  - [" + b.Kind + "] " + b.Detail + " — action: " + b.Action)
		}
	}
	switch st.rec {
	case recWait:
		st.human("recommendation: wait — hold the review, re-run ready later")
	default:
		st.human("recommendation: nudge-author — nudge the author instead of reviewing")
	}
}

// sortedKeys returns the sorted keys of m (jq object iteration order).
func sortedKeys(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}
