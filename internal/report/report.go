// Package report ports the `report`, `next`, and `show` verbs: ranked
// reads over the findings store with byte-identical human output and
// machine envelopes. Report owns the three-dimension gate (tier,
// completeness, live-thread), the --output/--sarif renderers, and the cost
// rollup; next replays report internally; show renders one finding.
package report

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a report-family run.
type Request = readcommon.Request

// Flags carries the parsed report flags.
type Flags struct {
	StatusF      string
	KindF        string
	Output       string
	Sarif        string
	CheckSev     string
	LiveMode     string
	Cost         bool
	Completeness bool
}

// Shared vocabulary: filter values, tier names, refusal codes, flags, and
// bundle paths repeated across the report-family verbs.
const (
	statusAll         = "all"
	statusOpen        = "open"
	kindInline        = "inline"
	kindSection       = "section"
	tierCritical      = "critical"
	tierImportant     = "important"
	tierInformational = "informational"
	tierInform        = "informational"
	codeCanceled      = "canceled"
	codeReportFailed  = "report-failed"
	flagJSON          = "--json"
	keyNext           = "next"
	newline           = "\n"
	diffGitPrefix     = "diff --git "
	prJSONSuffix      = "/PR.json"
	fullDiffSuffix    = "/full.diff"
	strTrue           = "true"
	quoteMark         = "\""
	patchSuffix       = "/patch.json"
	liveAuto          = "auto"
	strFalse          = "false"
	hashSpace         = "## "
	dashSpace         = "- "
	bSpace            = " b/"
	bSlash            = "b/"
	spaceSep          = " "
	slashSep          = "/"
	diffPlus          = "+"
	diffPlusPlus      = "+++"
	diffMinus         = "-"
	diffMinusMinus    = "---"
	hunkPrefix        = "@@ "
	tabSep            = "\t"
	newMarker         = "NEW"
	nullPath          = "null"
	hunksTSVSuffix    = "/hunks.tsv"
	rolesTxtSuffix    = "/roles.txt"
)

// reportFilePerm stages report artifacts readable only by the owner: the
// report can carry finding excerpts from the reviewed diff.
const reportFilePerm = 0o600

// Run executes `report <owner/repo> <pr> [flags]`, returning the exit code.
// Human progress goes to the runner's human stream; --json mode prints
// exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	setupRunner(r, req)
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: codeCanceled, Message: "canceled before report completed"}, cli.ExitBroke)
	}
	gate := flags.CheckSev != "" || flags.Completeness
	st, err := loadState(r, req, &flags)
	if err != nil {
		return refuseGate(r, err, gate)
	}
	runLiveProbe(ctx, st)
	if err := renderOutput(st); err != nil {
		return refuseGate(r, err, gate)
	}
	if err := renderSarif(st); err != nil {
		return refuseGate(r, err, gate)
	}
	emitReport(st)
	return gateTail(st)
}

// setupRunner records the invocation (no re-entry line: report sets none).
func setupRunner(r *cli.Runner, req Request) {
	r.Repo, r.Pr = req.Repo, req.Pr
}

// refuseGate renders a typed refusal, exiting 3 on gate runs so hooks can
// tell tool failure apart from a failed gate (exit 1).
func refuseGate(r *cli.Runner, err error, gate bool) int {
	if gate {
		return r.RefuseExit(err, cli.ExitBroke)
	}
	return r.Refuse(err)
}

// parseFlags parses the report flags in shell order. A missing option value
// reads the flag default (the shell's ${2:-default} with a Blind shift 2);
// an invalid value is a usage error (exit 2, no envelope).
func parseFlags(r *cli.Runner, args []string) (Flags, int, bool) {
	flags := Flags{StatusF: statusAll, KindF: statusAll, LiveMode: "auto"}
	i := 0
	for i < len(args) {
		a := args[i]
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		consumed, code, done := parseFlag(r, &flags, a, next)
		if done {
			return flags, code, true
		}
		i += consumed
	}
	routeHuman(r)
	return flags, 0, false
}

// parseFlag handles one report flag, returning how many args it consumed.
// An unknown or invalid value is a usage error (exit 2, no envelope).
func parseFlag(r *cli.Runner, flags *Flags, a, next string) (int, int, bool) {
	switch a {
	case "--status":
		return parseStatusFlag(r, flags, next)
	case "--kind":
		return parseKindFlag(r, flags, next)
	case "--output":
		flags.Output = next
		return 2, 0, false
	case "--sarif":
		flags.Sarif = next
		return 2, 0, false
	case "--cost":
		flags.Cost = true
		return 1, 0, false
	case "--check":
		return parseCheckFlag(r, flags, next)
	case "--completeness":
		flags.Completeness = true
		return 1, 0, false
	case "--live":
		return parseLiveFlag(r, flags, next)
	case flagJSON:
		r.JSONMode = true
		return 1, 0, false
	default:
		return 0, r.ReportUsage(usageErr(
			"unknown flag '%s' for report (want --status, --kind, --output, "+
				"--sarif, --cost, --check, --completeness, --live, --json)",
			a)), true
	}
}

// parseStatusFlag consumes --status, defaulting to all like ${2:-all}.
func parseStatusFlag(r *cli.Runner, flags *Flags, next string) (int, int, bool) {
	flags.StatusF = orDefault(next, statusAll)
	if !validStatus(flags.StatusF) {
		return 0, r.ReportUsage(usageErr(
			"unknown --status '%s' for report (want all|open|addressed|deferred|wont-fix|false-positive)",
			flags.StatusF)), true
	}
	return 2, 0, false
}

// parseKindFlag consumes --kind, defaulting to all like ${2:-all}.
func parseKindFlag(r *cli.Runner, flags *Flags, next string) (int, int, bool) {
	flags.KindF = orDefault(next, statusAll)
	if !validKind(flags.KindF) {
		return 0, r.ReportUsage(usageErr(
			"unknown --kind '%s' for report (want all|inline|section)",
			flags.KindF)), true
	}
	return 2, 0, false
}

// parseCheckFlag consumes --check, validating the gate tier.
func parseCheckFlag(r *cli.Runner, flags *Flags, next string) (int, int, bool) {
	flags.CheckSev = next
	if !validTier(flags.CheckSev) {
		return 0, r.ReportUsage(usageErr(
			"unknown --check tier '%s' for report (want critical|important|informational)",
			flags.CheckSev)), true
	}
	return 2, 0, false
}

// parseLiveFlag consumes --live, validating the live posture.
func parseLiveFlag(r *cli.Runner, flags *Flags, next string) (int, int, bool) {
	flags.LiveMode = orDefault(next, "")
	if !validLive(flags.LiveMode) {
		return 0, r.ReportUsage(usageErr(
			"unknown --live posture '%s' for report (want auto|strict|skip)",
			flags.LiveMode)), true
	}
	return 2, 0, false
}

// routeHuman sends human progress to stderr in JSON mode.
func routeHuman(r *cli.Runner) {
	if r.JSONMode {
		r.Human = r.Diag
	} else {
		r.Human = r.Out
	}
}

// orDefault mirrors ${2:-default}: a missing or empty value reads the
// default (the shell consumes the flag either way).
func orDefault(v, dflt string) string {
	if v == "" {
		return dflt
	}
	return v
}

// usageErr builds a verb usage error.
func usageErr(format, arg string) *cli.UsageError {
	return &cli.UsageError{Message: fmt.Sprintf(format, arg)}
}

// validStatus reports whether s is a known status filter.
func validStatus(s string) bool {
	switch s {
	case statusAll, statusOpen, "addressed", "deferred", "wont-fix", "false-positive":
		return true
	default:
		return false
	}
}

// validKind reports whether s is a known kind filter.
func validKind(s string) bool {
	switch s {
	case statusAll, kindInline, kindSection:
		return true
	default:
		return false
	}
}

// validTier reports whether s is a known gate tier.
func validTier(s string) bool {
	switch s {
	case tierCritical, tierImportant, tierInformational:
		return true
	default:
		return false
	}
}

// validLive reports whether s is a known live posture.
func validLive(s string) bool {
	switch s {
	case "auto", "strict", "skip":
		return true
	default:
		return false
	}
}

// state carries the loaded report run.
type state struct {
	runner     *cli.Runner
	req        Request
	flags      Flags
	counts     *jq.Obj
	cost       *jq.Obj
	entries    []any
	ranked     []readcommon.Ranked
	panel      []any
	receipts   []any
	patch      any
	comp       any
	dir        string
	now        string
	headSHA    string
	baseSHA    string
	panelSrc   string
	compPosted string
	live       liveState
	nOpen      int
	nAllOpen   int
	gate       bool
	compOK     bool
}

// liveState carries the live-thread probe outcome.
type liveState struct {
	status string
	err    string
	open   int
}

// live statuses, mirroring the shell's live_status values.
const (
	liveUnset         = "unset"
	liveFail          = "fail"
	livePass          = "pass"
	liveUnknown       = "unknown"
	liveUnknownStrict = "unknown-strict"
	liveSkip          = "skip"
	liveSkipExplicit  = "skip-explicit"
)

// loadState resolves the bundle, guards the store, and loads every local
// dimension: heads, panel, patch, ranked findings, counts, cost, and the
// completeness record.
func loadState(r *cli.Runner, req Request, flags *Flags) (*state, *cli.RefusalError) {
	st := &state{runner: r, req: req, flags: *flags}
	st.gate = flags.CheckSev != "" || flags.Completeness
	st.dir = readcommon.ResolveBundleDir(req.Pr)
	entries, err := readcommon.LoadFindings(st.dir)
	if err != nil {
		return nil, loadRefusal(err)
	}
	if err := readcommon.ValidateOverrides(); err != nil {
		return nil, loadRefusal(err)
	}
	st.now = cli.NowUTC(r.Now())
	st.entries = entries
	st.headSHA, st.baseSHA = bundleHeads(st.dir)
	st.panel, st.panelSrc = bundlePanel(st.dir)
	if isRegular(st.dir + patchSuffix) {
		patch, err := bundlePatch(st.dir)
		if err != nil {
			return nil, err
		}
		st.patch = patch
	}
	st.ranked = readcommon.Rank(entries, flags.StatusF, flags.KindF)
	st.nOpen = readcommon.CountStatus(st.ranked, statusOpen)
	st.nAllOpen = readcommon.CountOpen(entries)
	st.counts = storeCounts(entries)
	loadStateCost(r, st)
	loadStateCompleteness(st)
	return st, nil
}

// loadRefusal recovers the typed refusal carried by a store-guard error.
// LoadFindings and ValidateOverrides only fail typed, so the As-chain
// always hits; the fallback is defensive and never fires in practice.
func loadRefusal(err error) *cli.RefusalError {
	if ref, ok := errors.AsType[*cli.RefusalError](err); ok {
		return ref
	}
	return &cli.RefusalError{Code: "internal", Message: err.Error()}
}

// loadStateCost fills the cost rollup when --cost is set, warning on
// partial receipts like the shell's cost_rollup warns.
func loadStateCost(r *cli.Runner, st *state) {
	if !st.flags.Cost {
		return
	}
	res := readcommon.CostRollup(st.dir)
	for _, w := range res.Warns {
		r.Warn(w)
	}
	st.cost = res.Cost
	st.receipts = res.Receipts
}

// loadStateCompleteness fills the completeness record when --completeness
// is set.
func loadStateCompleteness(st *state) {
	if !st.flags.Completeness {
		return
	}
	st.comp, st.compOK, st.compPosted = completeness(st.dir, st.headSHA, st.req)
}

// storeCounts ports the counts object over the whole findings store.
func storeCounts(entries []any) *jq.Obj {
	return jq.NewObj(
		kindInline, countKind(entries, kindInline),
		kindSection, countKind(entries, kindSection),
		"anchored", countInlineAnchored(entries),
		"unanchored", countInlineLoose(entries),
	)
}

// countKind counts entries of one kind.
func countKind(entries []any, kind string) int {
	return countWhere(entries, func(e any) bool { return readcommon.KindOf(e) == kind })
}

// countInlineAnchored counts anchored inline findings.
func countInlineAnchored(entries []any) int {
	return countWhere(entries, func(e any) bool { return inlineAnchored(e, true) })
}

// countInlineLoose counts unanchored inline findings.
func countInlineLoose(entries []any) int {
	return countWhere(entries, func(e any) bool { return inlineAnchored(e, false) })
}

// countWhere counts entries matching match.
func countWhere(entries []any, match func(any) bool) int {
	n := 0
	for _, e := range entries {
		if match(e) {
			n++
		}
	}
	return n
}

// inlineAnchored reports whether e is an inline finding whose anchored flag
// equals want.
func inlineAnchored(e any, want bool) bool {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	if readcommon.KindOf(e) != kindInline {
		return false
	}
	anchored, _ := m.Get("anchored")
	return (anchored == true) == want
}

// bundleHeads reads the fetched/base heads from PR.json or source.json.
func bundleHeads(dir string) (head, base string) {
	if isRegular(dir + prJSONSuffix) {
		return readcommon.PrField(dir, "head_sha"), readcommon.PrField(dir, "base_sha")
	}
	if isRegular(dir + "/source.json") {
		return jsonField(dir+"/source.json", "head_sha"), jsonField(dir+"/source.json", "base_sha")
	}
	return "", ""
}

// bundlePanel reads the bundle panel members ([] when absent).
func bundlePanel(dir string) ([]any, string) {
	path := dir + "/panel"
	if !isRegular(path) {
		return nil, ""
	}
	raw, err := readFile(path)
	if err != nil {
		return nil, ""
	}
	var members []any
	for line := range strings.SplitSeq(string(raw), newline) {
		if line == "" {
			continue
		}
		members = append(members, line)
	}
	return members, "panel"
}

// bundlePatch reads the patch record; the caller checks isRegular first, so
// a missing store never reaches here. R1: an unreadable patch store refuses
// typed corrupt-store instead of dying in the envelope jq (the shell gained
// the same guard with this port).
func bundlePatch(dir string) (any, *cli.RefusalError) {
	raw, err := readFile(dir + patchSuffix)
	if err != nil {
		return nil, corruptPatch(dir)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil, corruptPatch(dir)
	}
	return doc, nil
}

// corruptPatch builds the patch-store refusal.
func corruptPatch(dir string) *cli.RefusalError {
	return &cli.RefusalError{
		Code:    "corrupt-store",
		Message: fmt.Sprintf("patch store %s/patch.json is unreadable (re-run post to rebuild, or restore the file)", dir),
	}
}
