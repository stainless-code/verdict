// Package report ports the `report`, `next`, and `show` verbs: ranked
// reads over the findings store with byte-identical human output and
// machine envelopes. Report owns the three-dimension gate (tier,
// completeness, live-thread), the --output/--sarif renderers, and the cost
// rollup; next replays report internally; show renders one finding.
package report

import (
	"context"
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
	Cost         bool
	CheckSev     string
	Completeness bool
	LiveMode     string
}

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
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before report completed"}, cli.ExitBroke)
	}
	gate := flags.CheckSev != "" || flags.Completeness
	st, err := loadState(r, req, flags)
	if err != nil {
		return refuseGate(r, err, gate)
	}
	if err := runLiveProbe(ctx, st); err != nil {
		return refuseGate(r, err, gate)
	}
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
	flags := Flags{StatusF: "all", KindF: "all", LiveMode: "auto"}
	i := 0
	for i < len(args) {
		a := args[i]
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		consumed := 1
		switch a {
		case "--status":
			flags.StatusF = orDefault(next, "all")
			consumed = 2
			if !validStatus(flags.StatusF) {
				return flags, r.ReportUsage(usageErr("unknown --status '%s' for report (want all|open|addressed|deferred|wont-fix|false-positive)", flags.StatusF)), true
			}
		case "--kind":
			flags.KindF = orDefault(next, "all")
			consumed = 2
			if !validKind(flags.KindF) {
				return flags, r.ReportUsage(usageErr("unknown --kind '%s' for report (want all|inline|section)", flags.KindF)), true
			}
		case "--output":
			flags.Output = next
			consumed = 2
		case "--sarif":
			flags.Sarif = next
			consumed = 2
		case "--cost":
			flags.Cost = true
		case "--check":
			flags.CheckSev = next
			consumed = 2
			if !validTier(flags.CheckSev) {
				return flags, r.ReportUsage(usageErr("unknown --check tier '%s' for report (want critical|important|informational)", flags.CheckSev)), true
			}
		case "--completeness":
			flags.Completeness = true
		case "--live":
			flags.LiveMode = orDefault(next, "")
			consumed = 2
			if !validLive(flags.LiveMode) {
				return flags, r.ReportUsage(usageErr("unknown --live posture '%s' for report (want auto|strict|skip)", flags.LiveMode)), true
			}
		case "--json":
			r.JSONMode = true
		default:
			return flags, r.ReportUsage(usageErr("unknown flag '%s' for report (want --status, --kind, --output, --sarif, --cost, --check, --completeness, --live, --json)", a)), true
		}
		i += consumed
	}
	routeHuman(r)
	return flags, 0, false
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
	case "all", "open", "addressed", "deferred", "wont-fix", "false-positive":
		return true
	default:
		return false
	}
}

// validKind reports whether s is a known kind filter.
func validKind(s string) bool {
	switch s {
	case "all", "inline", "section":
		return true
	default:
		return false
	}
}

// validTier reports whether s is a known gate tier.
func validTier(s string) bool {
	switch s {
	case "critical", "important", "informational":
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
	gate       bool
	dir        string
	now        string
	headSHA    string
	baseSHA    string
	entries    []any
	ranked     []readcommon.Ranked
	nOpen      int
	nAllOpen   int
	counts     *jq.Obj
	panel      []any
	panelSrc   string
	patch      any
	cost       *jq.Obj
	receipts   []any
	comp       any
	compOK     bool
	compPosted string
	live       liveState
}

// liveState carries the live-thread probe outcome.
type liveState struct {
	status string
	open   int
	err    string
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
func loadState(r *cli.Runner, req Request, flags Flags) (*state, error) {
	st := &state{runner: r, req: req, flags: flags}
	st.gate = flags.CheckSev != "" || flags.Completeness
	st.dir = readcommon.ResolveBundleDir(req.Pr)
	entries, err := readcommon.LoadFindings(st.dir)
	if err != nil {
		return nil, err
	}
	if err := readcommon.ValidateOverrides(); err != nil {
		return nil, err
	}
	st.now = cli.NowUTC(r.Now())
	st.entries = entries
	st.headSHA, st.baseSHA = bundleHeads(st.dir)
	st.panel, st.panelSrc = bundlePanel(st.dir)
	patch, err := bundlePatch(st.dir)
	if err != nil {
		return nil, err
	}
	st.patch = patch
	st.ranked = readcommon.Rank(entries, flags.StatusF, flags.KindF)
	st.nOpen = readcommon.CountStatus(st.ranked, "open")
	st.nAllOpen = readcommon.CountOpen(entries)
	st.counts = storeCounts(entries)
	if flags.Cost {
		res := readcommon.CostRollup(st.dir)
		for _, w := range res.Warns {
			r.Warn(w)
		}
		st.cost = res.Cost
		st.receipts = res.Receipts
	}
	st.comp = nil
	if flags.Completeness {
		st.comp, st.compOK, st.compPosted = completeness(st.dir, st.headSHA, req)
	}
	return st, nil
}

// storeCounts ports the counts object over the whole findings store.
func storeCounts(entries []any) *jq.Obj {
	count := func(match func(any) bool) int {
		n := 0
		for _, e := range entries {
			if match(e) {
				n++
			}
		}
		return n
	}
	isKind := func(kind string) func(any) bool {
		return func(e any) bool { return readcommon.KindOf(e) == kind }
	}
	isInlineAnchored := func(e any) bool {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			return false
		}
		if readcommon.KindOf(e) != "inline" {
			return false
		}
		anchored, _ := m.Get("anchored")
		return anchored == true
	}
	isInlineLoose := func(e any) bool {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			return false
		}
		if readcommon.KindOf(e) != "inline" {
			return false
		}
		anchored, _ := m.Get("anchored")
		return anchored != true
	}
	return jq.NewObj(
		"inline", count(isKind("inline")),
		"section", count(isKind("section")),
		"anchored", count(isInlineAnchored),
		"unanchored", count(isInlineLoose),
	)
}

// bundleHeads reads the fetched/base heads from PR.json or source.json.
func bundleHeads(dir string) (head, base string) {
	if isRegular(dir + "/PR.json") {
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
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" {
			continue
		}
		members = append(members, line)
	}
	return members, "panel"
}

// bundlePatch reads the patch record (nil when absent). R1: an unreadable
// patch store refuses typed corrupt-store instead of dying in the envelope
// jq (the shell gained the same guard with this port).
func bundlePatch(dir string) (any, error) {
	path := dir + "/patch.json"
	if !isRegular(path) {
		return nil, nil
	}
	raw, err := readFile(path)
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
	return &cli.RefusalError{Code: "corrupt-store",
		Message: fmt.Sprintf("patch store %s/patch.json is unreadable (re-run post to rebuild, or restore the file)", dir)}
}
