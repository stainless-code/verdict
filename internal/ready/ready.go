// Package ready ports the `ready` verb: the readiness advisory over live
// PR state (checks, threads, draft/conflict, description). Advisory runs
// exit 0; --check gates for hooks and CI.
package ready

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a ready run.
type Request = readcommon.Request

// Run executes `ready <owner/repo> <pr> [--json, --check]`, returning the
// exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	check, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before ready completed"}, cli.ExitBroke)
	}
	st, err := loadState(ctx, r, req)
	if err != nil {
		if check {
			return r.RefuseExit(err, cli.ExitBroke)
		}
		return r.Refuse(err)
	}
	return finishRun(st, check)
}

// finishRun ports the advisory emit plus the --check gate.
func finishRun(st *state, check bool) int {
	emitHuman(st)
	if st.runner.JSONMode {
		emitEnvelope(st)
	}
	if check && !st.ready {
		return 1
	}
	return 0
}

// parseFlags parses the ready flags.
func parseFlags(r *cli.Runner, args []string) (bool, int, bool) {
	check := false
	for _, a := range args {
		switch a {
		case "--json":
			r.JSONMode = true
			routeHuman(r)
		case "--check":
			check = true
		default:
			return false, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for ready (want --json, --check)", a)}), true
		}
	}
	return check, 0, false
}

// routeHuman sends human progress to stderr in JSON mode.
func routeHuman(r *cli.Runner) {
	if r.JSONMode {
		r.Human = r.Diag
	} else {
		r.Human = r.Out
	}
}

// state carries the loaded ready run.
type state struct {
	live        map[string]any
	runner      *cli.Runner
	pr          string
	rec         string
	mergeable   string
	mergeState  string
	prState     string
	repo        string
	blockers    []blocker
	threadsOpen int
	bodyLen     int
	ciTotal     int
	ciFail      int
	ready       bool
	draft       bool
}

// recWait holds the review instead of reviewing.
const recWait = "wait"

// mergeUnknown reads when GitHub has not computed mergeability yet.
const mergeUnknown = "UNKNOWN"

// newline splits description lines.
const newline = "\n"

// commentOpen and commentClose bracket HTML comment spans.
const (
	commentOpen  = "<!--"
	commentClose = "-->"
)

// blocker is one readiness blocker with its recovery action.
type blocker struct {
	kind   string
	detail string
	action string
}

// loadState resolves the bundle and reads every live dimension: PR state,
// checks, threads, and the description strip.
func loadState(ctx context.Context, r *cli.Runner, req Request) (*state, error) {
	st := &state{runner: r, repo: req.Repo, pr: req.Pr}
	dir := readcommon.ResolveBundleDir(req.Pr)
	if isLocalDir(dir) {
		return nil, &cli.RefusalError{
			Code:    "no-pr-target",
			Message: "ready needs a PR bundle (fetch owner/repo N first); local bundles have no live checks to advise on",
		}
	}
	if prov := readcommon.ProviderOf(dir); prov != "" && prov != "github" {
		return nil, &cli.RefusalError{
			Code:    "provider-unimplemented",
			Message: fmt.Sprintf("ready on %s is not implemented yet (no live gh reads for it)", prov),
		}
	}
	if !isRegular(dir + "/PR.json") {
		return nil, &cli.RefusalError{
			Code:    "missing-bundle",
			Message: fmt.Sprintf("missing %s/PR.json (run fetch %s %s first)", dir, req.Repo, req.Pr),
		}
	}
	if err := loadLive(ctx, st, dir); err != nil {
		return nil, err
	}
	loadChecks(ctx, st)
	if err := loadThreads(ctx, st); err != nil {
		return nil, err
	}
	computeReadiness(st)
	return st, nil
}

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
func loadLive(ctx context.Context, st *state, dir string) error {
	res, err := readcommon.Gh(ctx, "pr", "view", st.pr, "--repo", st.repo,
		"--json", "isDraft,mergeable,mergeStateStatus,body,state,headRefOid")
	writeErrFile(st.runner, dir+"/.ready.err", res.Stderr)
	if err != nil {
		return &cli.RefusalError{
			Code:    "pr-read-failed",
			Message: fmt.Sprintf("could not read live PR state for %s#%s: %s", st.repo, st.pr, firstLine(res.Stderr)),
		}
	}
	doc, derr := jq.Decode([]byte(res.Stdout))
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
		st.runner.Warn(fmt.Sprintf("bundle stale (fetched %s, live %s) — re-fetch before review, advice reads live",
			readcommon.ShortSHA(fetched), readcommon.ShortSHA(liveHead)))
	}
	st.draft = liveString(st.live, "isDraft") == "true"
	st.mergeable = liveOr(st.live, "mergeable", mergeUnknown)
	st.mergeState = liveOr(st.live, "mergeStateStatus", mergeUnknown)
	st.prState = liveOr(st.live, "state", mergeUnknown)
	st.bodyLen = bodyLength(liveBody(st.live))
	if st.mergeable == mergeUnknown && st.mergeState == mergeUnknown {
		st.runner.Warn("mergeability UNKNOWN (GitHub still computing?) — conflict treated as absent, re-run ready if this persists")
	}
	return nil
}

// probeStagePerm stages probe stderr readable only by the owner: the
// content can carry token-adjacent gh diagnostics.
const probeStagePerm = 0o600

// writeErrFile stages probe stderr beside the bundle. The write is
// best-effort like the shell's 2> redirect, so a failure warns instead of
// refusing: the file is a debug side channel and the run must not fail
// over garnish.
func writeErrFile(r *cli.Runner, path, stderr string) {
	if err := os.WriteFile(path, []byte(stderr), probeStagePerm); err != nil {
		// Un-actionable beyond the notice: the live data already loaded.
		fmt.Fprintf(r.Diag, "ready: could not stage probe stderr in %s: %v\n", path, err)
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
	res, err := readcommon.Gh(ctx, "pr", "checks", st.pr, "--repo", st.repo, "--json", "bucket,name,state")
	raw := "[]"
	if err == nil {
		raw = res.Stdout
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
		st.blockers = append(st.blockers, blocker{
			kind:   "ci-failing",
			detail: fmt.Sprintf("%d failing check(s): %s", st.ciFail, strings.Join(failNames, ", ")),
			action: "fix CI, then re-run ready",
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
// only live, so the bundle's thread files never count.
func loadThreads(ctx context.Context, st *state) error {
	owner, name := readcommon.SplitOwner(st.repo)
	res, err := readcommon.Gh(ctx, readcommon.ThreadsArgv(owner, name, st.pr, readcommon.QueryReady)...)
	if err != nil {
		return &cli.RefusalError{
			Code:    "threads-failed",
			Message: fmt.Sprintf("could not read live review threads for %s#%s", st.repo, st.pr),
		}
	}
	nodes, err := readcommon.SlurpThreads(res.Stdout)
	if err != nil {
		return &cli.RefusalError{
			Code:    "threads-failed",
			Message: fmt.Sprintf("could not read live review threads for %s#%s", st.repo, st.pr),
		}
	}
	st.threadsOpen = countOpenThreads(nodes)
	if st.threadsOpen > 0 {
		st.blockers = append(st.blockers, blocker{
			kind:   "threads-open",
			detail: fmt.Sprintf("%d unresolved thread(s)", st.threadsOpen),
			action: "address or resolve open threads, then re-run ready",
		})
	}
	return nil
}

// countOpenThreads counts nodes with isResolved exactly false, zeroing the
// whole read when any node is a non-object (the shell's jq errors and the
// `|| echo 0` fallback zeroes the count).
func countOpenThreads(nodes []any) int {
	n := 0
	for _, node := range nodes {
		add, stop := countThreadNode(node)
		if stop {
			return 0
		}
		n += add
	}
	return n
}

// countThreadNode ports one thread node: nil reads skipped, non-objects
// zero the whole read (stop), unresolved reads counted.
func countThreadNode(node any) (add int, stop bool) {
	if node == nil {
		return 0, false
	}
	m, ok := node.(map[string]any)
	if !ok {
		return 0, true
	}
	if resolved, ok := m["isResolved"].(bool); !ok || !resolved {
		return 1, false
	}
	return 0, false
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
		st.blockers = append(st.blockers, blocker{
			kind:   "draft",
			detail: "PR is still a draft",
			action: "mark ready for review when done, then re-run ready",
		})
	}
}

// appendConflictBlocker ports the merge-conflict blocker.
func appendConflictBlocker(st *state) {
	if st.mergeable == "CONFLICTING" || st.mergeState == "DIRTY" {
		st.blockers = append(st.blockers, blocker{
			kind:   "conflict",
			detail: "mergeable=" + st.mergeable + " mergeStateStatus=" + st.mergeState,
			action: "rebase onto base and resolve conflicts, then re-run ready",
		})
	}
}

// appendDescriptionBlocker ports the description-thin blocker.
func appendDescriptionBlocker(st *state) {
	if st.bodyLen < descriptionThin {
		st.blockers = append(st.blockers, blocker{
			kind:   "description-thin",
			detail: "description ~" + strconv.Itoa(st.bodyLen) + " chars (<50)",
			action: "expand the description (what/why/testing), then re-run ready",
		})
	}
}

// descriptionThin is the description completeness floor.
const descriptionThin = 50

// emitHuman ports the advisory lines plus the recommendation.
func emitHuman(st *state) {
	r := st.runner
	if st.ready {
		fmt.Fprintf(r.Human, "ready: yes — %s#%s looks reviewable "+
			"(CI %d checks, 0 failing; %d open threads; mergeable %s; body %d chars)\n",
			st.repo, st.pr, st.ciTotal, st.threadsOpen, st.mergeable, st.bodyLen)
		fmt.Fprintln(r.Human, "recommendation: review — fetch + review")
		return
	}
	if st.prState != "OPEN" {
		fmt.Fprintf(r.Human, "ready: no — %s#%s is %s, nothing to review\n", st.repo, st.pr, st.prState)
	} else {
		fmt.Fprintf(r.Human, "ready: no — %s#%s has %d blocker(s):\n", st.repo, st.pr, len(st.blockers))
		for _, b := range st.blockers {
			fmt.Fprintf(r.Human, "  - [%s] %s — action: %s\n", b.kind, b.detail, b.action)
		}
	}
	switch st.rec {
	case recWait:
		fmt.Fprintln(r.Human, "recommendation: wait — hold the review, re-run ready later")
	default:
		fmt.Fprintln(r.Human, "recommendation: nudge-author — nudge the author instead of reviewing")
	}
}

// emitEnvelope ports the ready --json envelope.
func emitEnvelope(st *state) {
	r := st.runner
	blockers := make([]any, 0, len(st.blockers))
	for _, b := range st.blockers {
		blockers = append(blockers, jq.NewObj(
			"kind", b.kind,
			"detail", b.detail,
			"action", b.action,
		))
	}
	next := "nudge the author instead of reviewing"
	switch st.rec {
	case "review":
		next = "fetch + review"
	case recWait:
		next = "hold the review — re-run ready later"
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "ready",
		"repo", r.Repo,
		"pr", cli.PrJSON(r.Pr),
		"at", cli.NowUTC(r.Now()),
		"next", next,
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj(
			"ready", st.ready,
			"recommendation", st.rec,
			"blockers", blockers,
			"state", st.prState,
			"draft", st.draft,
			"mergeable", st.mergeable,
			"mergeStateStatus", st.mergeState,
			"ci", jq.NewObj("failing", st.ciFail, "total", st.ciTotal),
			"threads_open", st.threadsOpen,
			"description_chars", st.bodyLen,
		),
		"error", nil,
	)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
}

// sortedKeys returns the sorted keys of m (jq object iteration order).
func sortedKeys(m map[string]any) []string {
	return cli.SortedKeys(m)
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
