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
	emitHuman(st)
	if r.JSONMode {
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
			return false, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for ready (want --json, --check)", a)}), true
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
	runner      *cli.Runner
	repo        string
	pr          string
	live        map[string]any
	ready       bool
	rec         string
	blockers    []blocker
	draft       bool
	mergeable   string
	mergeState  string
	prState     string
	ciFail      int
	ciTotal     int
	threadsOpen int
	bodyLen     int
}

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
		return nil, &cli.RefusalError{Code: "no-pr-target", Message: "ready needs a PR bundle (fetch owner/repo N first); local bundles have no live checks to advise on"}
	}
	if prov := readcommon.ProviderOf(dir); prov != "" && prov != "github" {
		return nil, &cli.RefusalError{Code: "provider-unimplemented", Message: fmt.Sprintf("ready on %s is not implemented yet (no live gh reads for it)", prov)}
	}
	if !isRegular(dir + "/PR.json") {
		return nil, &cli.RefusalError{Code: "missing-bundle", Message: fmt.Sprintf("missing %s/PR.json (run fetch %s %s first)", dir, req.Repo, req.Pr)}
	}
	if err := loadLive(ctx, st, dir); err != nil {
		return nil, err
	}
	if err := loadChecks(ctx, st); err != nil {
		return nil, err
	}
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
	writeErrFile(dir+"/.ready.err", res.Stderr)
	if err != nil {
		return &cli.RefusalError{Code: "pr-read-failed",
			Message: fmt.Sprintf("could not read live PR state for %s#%s: %s", st.repo, st.pr, firstLine(res.Stderr))}
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
	st.mergeable = liveOr(st.live, "mergeable", "UNKNOWN")
	st.mergeState = liveOr(st.live, "mergeStateStatus", "UNKNOWN")
	st.prState = liveOr(st.live, "state", "UNKNOWN")
	st.bodyLen = bodyLength(liveBody(st.live))
	if st.mergeable == "UNKNOWN" && st.mergeState == "UNKNOWN" {
		st.runner.Warn("mergeability UNKNOWN (GitHub still computing?) — conflict treated as absent, re-run ready if this persists")
	}
	return nil
}

// writeErrFile stages probe stderr beside the bundle.
func writeErrFile(path, stderr string) {
	_ = os.WriteFile(path, []byte(stderr), 0o644)
}

// firstLine ports `head -c 200 | tr '\n' ' '`: the first 200 bytes with
// newlines flattened.
func firstLine(s string) string {
	if len(s) > errHeadBytes {
		s = s[:errHeadBytes]
	}
	return strings.ReplaceAll(s, "\n", " ")
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
	for _, line := range strings.Split(body, "\n") {
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
			if i := strings.Index(rest, "-->"); i >= 0 {
				rest = rest[i+3:]
				*inComment = false
			} else {
				rest = ""
			}
			continue
		}
		if i := strings.Index(rest, "<!--"); i >= 0 {
			kept.WriteString(rest[:i])
			rest = rest[i+4:]
			*inComment = true
			continue
		}
		kept.WriteString(rest)
		rest = ""
	}
	return kept.String()
}

// squeezeSpace ports `tr -s '[:space:]' ' '`: whitespace runs collapse to
// one space (C-locale set: space, tab, newline, vertical tab, form feed,
// carriage return).
func squeezeSpace(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	inRun := false
	for i := 0; i < len(s); i++ {
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
func loadChecks(ctx context.Context, st *state) error {
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
		if bucket, _ := m["bucket"].(string); bucket != "fail" {
			continue
		}
		st.ciFail++
		failNames = append(failNames, checkName(m))
	}
	if anomaly {
		st.ciFail = 0
		st.ciTotal = 0
		return nil
	}
	st.ciTotal = len(list)
	if st.ciFail > 0 {
		st.blockers = append(st.blockers, blocker{
			kind:   "ci-failing",
			detail: fmt.Sprintf("%d failing check(s): %s", st.ciFail, strings.Join(failNames, ", ")),
			action: "fix CI, then re-run ready",
		})
	}
	return nil
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
		return &cli.RefusalError{Code: "threads-failed",
			Message: fmt.Sprintf("could not read live review threads for %s#%s", st.repo, st.pr)}
	}
	nodes, err := readcommon.SlurpThreads(res.Stdout)
	if err != nil {
		return &cli.RefusalError{Code: "threads-failed",
			Message: fmt.Sprintf("could not read live review threads for %s#%s", st.repo, st.pr)}
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
		if node == nil {
			continue
		}
		m, ok := node.(map[string]any)
		if !ok {
			return 0
		}
		if resolved, _ := m["isResolved"].(bool); !resolved {
			n++
		}
	}
	return n
}

// computeReadiness ports the ready/rec decision plus the draft, conflict,
// and description-thin blockers.
func computeReadiness(st *state) {
	if st.draft {
		st.blockers = append(st.blockers, blocker{
			kind:   "draft",
			detail: "PR is still a draft",
			action: "mark ready for review when done, then re-run ready",
		})
	}
	if st.mergeable == "CONFLICTING" || st.mergeState == "DIRTY" {
		st.blockers = append(st.blockers, blocker{
			kind:   "conflict",
			detail: "mergeable=" + st.mergeable + " mergeStateStatus=" + st.mergeState,
			action: "rebase onto base and resolve conflicts, then re-run ready",
		})
	}
	if st.bodyLen < descriptionThin {
		st.blockers = append(st.blockers, blocker{
			kind:   "description-thin",
			detail: "description ~" + strconv.Itoa(st.bodyLen) + " chars (<50)",
			action: "expand the description (what/why/testing), then re-run ready",
		})
	}
	st.ready = true
	st.rec = "review"
	if st.prState != "OPEN" {
		st.ready = false
		st.rec = "wait"
		return
	}
	if len(st.blockers) > 0 {
		st.ready = false
		if st.draft {
			st.rec = "wait"
		} else {
			st.rec = "nudge-author"
		}
	}
}

// descriptionThin is the description completeness floor.
const descriptionThin = 50

// emitHuman ports the advisory lines plus the recommendation.
func emitHuman(st *state) {
	r := st.runner
	if st.ready {
		fmt.Fprintf(r.Human, "ready: yes — %s#%s looks reviewable (CI %d checks, 0 failing; %d open threads; mergeable %s; body %d chars)\n",
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
	case "wait":
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
	if st.rec == "review" {
		next = "fetch + review"
	} else if st.rec == "wait" {
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
