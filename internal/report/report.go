// Package report is the report family's CLI adapter: argv parsing (same
// usage text and exit 2), one call into the reportcore service, artifact
// writes, envelope/human rendering, and refusal-vs-gate exit mapping.
// Domain logic lives in internal/core/report.
package report

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/shellquote"
)

// Request wires a report-family run.
type Request = readcommon.Request

// Shared vocabulary: filter values and flags repeated across the report
// family parsers.
const (
	statusAll      = "all"
	statusOpen     = "open"
	kindInline     = "inline"
	kindSection    = "section"
	reportFilePerm = 0o600
	cmdReport      = "report"
	pathOperand    = "<path>"
)

// Run executes `report <owner/repo> <pr> [flags]`, returning the exit code.
// Human progress goes to the runner's human stream; --json mode prints
// exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	setupRunner(r, req)
	setRentry(r, cmdReport, req, args)
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, cmdReport); done {
		return code
	}
	if flags.Policy != "" {
		return runReplay(ctx, r, req, &flags)
	}
	resp, ref := reportcore.Run(ctx, reportRequest(r, req, &flags))
	if ref != nil {
		return refuse(r, ref)
	}
	r.Warnings = resp.Warnings
	if ref := writeOutputs(resp); ref != nil {
		return refuse(r, ref)
	}
	emitReport(r, req, resp)
	return cli.ExitOK
}

// replayFlagRefusal explains why a replay refuses --compare: it prints its own
// two-sided comparison, so a second baseline would be ignored.
const replayFlagRefusal = "--policy reports a comparison and exits 0; --compare would be ignored, " +
	"so drop it"

// runReplay answers `report --policy`: it prints the decision the captured
// policy derives and the one the candidate file derives over the same stored
// findings. A replay writes nothing, so it refuses the artifact paths rather
// than silently ignore them; it exits 0 whatever the candidate decides, because
// it reports a comparison and does not gate this run.
func runReplay(ctx context.Context, r *cli.Runner, req Request, flags *reportcore.Flags) int {
	if flags.Output != "" || flags.Sarif != "" {
		return r.ReportUsage(&cli.UsageError{Message: "--policy is read-only; it writes no --output or --sarif"})
	}
	if flags.CompareSet {
		return r.ReportUsage(&cli.UsageError{Message: replayFlagRefusal})
	}
	replay, ref := reportcore.Replay(ctx, reportRequest(r, req, flags))
	if ref != nil {
		return refuse(r, ref)
	}
	if r.JSONMode {
		return emitReplayEnvelope(ctx, r, req, flags, replay)
	}
	renderReplay(r, flags.Policy, replay)
	return cli.ExitOK
}

// emitReplayEnvelope prints the ordinary report envelope for the captured
// policy with the comparison attached under `replay`, so a replay stays a
// schema-valid report and can never be mistaken for it: the key is absent
// unless --policy named a file.
//
// The envelope re-reads the bundle where Replay already did. That is a second
// read, not a second decision: the store has no snapshot handle to share, and a
// re-fetch landing between the two reads can only make the envelope's rows
// newer than the decisions beside them, never the reverse. A caller that needs
// both from one revision replays after the fetch settles.
func emitReplayEnvelope(ctx context.Context, r *cli.Runner, req Request, flags *reportcore.Flags, replay *reportcore.PolicyReplay) int {
	resp, ref := reportcore.Run(ctx, reportRequest(r, req, flags))
	if ref != nil {
		return refuse(r, ref)
	}
	if resp.Data == nil {
		return refuse(r, &core.RefusalError{Code: "internal", Message: "report built no data to carry the replay"})
	}
	r.Warnings = resp.Warnings
	resp.Data.Replay = replayWire(flags.Policy, replay)
	r.Emit(cmdReport, req.Repo, req.Pr, resp.At, resp.Next, resp.Warnings, resp.Data.Wire())
	return cli.ExitOK
}

// replayWire maps the replay onto the published envelope shape, keeping the
// captured-side dimension order so the `changed` list reads in gate order.
func replayWire(path string, replay *reportcore.PolicyReplay) *core.ReportReplay {
	moves := make([]core.ReportReplayMove, 0, len(replay.Captured.Dimensions))
	for _, move := range replay.Moves() {
		moves = append(moves, core.ReportReplayMove{ID: move.ID, Before: move.Before, After: move.After})
	}
	return &core.ReportReplay{
		PolicyPath: path,
		Captured:   replaySide(replay.Captured),
		Candidate:  replaySide(replay.Candidate),
		Changed:    moves,
	}
}

// replaySide maps one decision onto its wire side.
func replaySide(d reportcore.Decision) core.ReportReplaySide {
	return core.ReportReplaySide{Reason: d.Reason, GatePassed: d.GatePassed, Approved: d.Approved}
}

// renderReplay prints the two decisions and the dimensions whose status moved.
func renderReplay(r *cli.Runner, path string, replay *reportcore.PolicyReplay) {
	fmt.Fprint(r.Human, core.Msgf("policy replay (read-only): captured policy vs %s\n", path))
	fmt.Fprint(r.Human, core.Msgf("  gate      %s -> %s\n",
		passLabel(replay.Captured.GatePassed), passLabel(replay.Candidate.GatePassed)))
	fmt.Fprint(r.Human, core.Msgf("  approval  %s -> %s\n",
		approvalLabel(replay.Captured), approvalLabel(replay.Candidate)))
	if replay.Candidate.Reason != "" {
		fmt.Fprint(r.Human, core.Msgf("  reason    %s\n", replay.Candidate.Reason))
	}
	for _, move := range replay.Moves() {
		fmt.Fprint(r.Human, core.Msgf("  changed   %s %s -> %s\n", move.ID, move.Before, move.After))
	}
}

// passLabel names a gate decision.
func passLabel(passed bool) string {
	if passed {
		return "pass"
	}
	return "fail"
}

// approvalLabel names an approval decision.
func approvalLabel(decision reportcore.Decision) string {
	if decision.Approved {
		return "approved"
	}
	return "withheld"
}

// writeOutputs writes the requested artifacts in shell order: --output
// first, then --sarif.
func writeOutputs(resp *reportcore.Response) *cli.RefusalError {
	if ref := writeArtifact(resp.Output); ref != nil {
		return ref
	}
	return writeArtifact(resp.Sarif)
}

// emitReport writes the artifact notices, then the envelope or the human
// summary.
func emitReport(r *cli.Runner, req Request, resp *reportcore.Response) {
	if resp.Sarif != nil {
		fmt.Fprint(r.Human, core.Msgf("wrote %s\n", resp.Sarif.Path))
	}
	if r.JSONMode {
		if resp.Output != nil {
			fmt.Fprint(r.Human, core.Msgf("wrote %s\n", resp.Output.Path))
		}
		r.Emit(cmdReport, req.Repo, req.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire())
		return
	}
	r.HumanLines(resp.Human)
}

// reportRequest builds the core request from the parsed flags and runner.
func reportRequest(r *cli.Runner, req Request, flags *reportcore.Flags) *reportcore.Request {
	return &reportcore.Request{
		ReadRequest: readRequest(r, req),
		Flags:       *flags,
		JSONOnly:    r.JSONMode,
		Effective:   r.Effective,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// readRequest injects the runner's clock, progress observer, and command
// runner into the typed read request.
func readRequest(r *cli.Runner, req Request) core.ReadRequest {
	read := r.Read(req, coreexec.Runner{})
	return read
}

// writeArtifact writes one requested output file atomically (0600 when it
// creates the file: the report can carry reviewed-diff excerpts; an existing
// file keeps its own mode). A symlink is resolved first so the link survives
// and only its target is replaced; a device or FIFO keeps the direct write.
func writeArtifact(a *core.FileArtifact) *cli.RefusalError {
	if a == nil {
		return nil
	}
	if err := atomic.WriteThrough(a.Path, a.Content, reportFilePerm); err != nil {
		return &cli.RefusalError{
			Code:    "report-failed",
			Message: core.Msgf("could not write %s: %v", a.Path, err),
		}
	}
	return nil
}

// setupRunner records the invocation (no re-entry line: report sets none).
func setupRunner(r *cli.Runner, req Request) {
	r.Repo, r.Pr = req.Repo, req.Pr
}

// setRentry records the re-run line every refusal of this family prints: the
// invocation the operator typed, so the way forward after the named cause is
// the same command instead of a bare `try:`.
func setRentry(r *cli.Runner, verb string, req Request, args []string) {
	words := []string{"verdict", verb, shellquote.Arg(req.Repo), shellquote.Arg(req.Pr)}
	for _, a := range args {
		words = append(words, shellArg(a))
	}
	if r.JSONMode {
		words = append(words, "--json")
	}
	r.Rentry = strings.Join(words, " ")
}

// shellArg renders one argument for the re-enter line. [shellquote.Arg] drops
// an empty word, which would close the gap and hand the value to the next
// token (`--json` included), so an empty argument keeps its place as a pair of
// single quotes.
func shellArg(arg string) string {
	if arg == "" {
		return "''"
	}
	return shellquote.Arg(arg)
}

// refuse renders a typed refusal, keeping the warnings it carries.
func refuse(r *cli.Runner, err error) int {
	if ref, ok := errors.AsType[*cli.RefusalError](err); ok && ref.Warnings != nil {
		r.Warnings = ref.Warnings
	}
	return r.Refuse(err)
}

// parseFlags parses the report flags left to right. A value flag with no
// operand is a usage error (exit 2, no envelope); an invalid value is too.
func parseFlags(r *cli.Runner, args []string) (reportcore.Flags, int, bool) {
	flags := reportcore.Flags{Status: statusAll, Kind: statusAll}
	i := 0
	for i < len(args) {
		consumed, code, done := parseFlag(r, &flags, args, i)
		if done {
			return flags, code, true
		}
		i += consumed
	}
	r.RouteHuman()
	return flags, 0, false
}

// flagStatus names the --status filter shared by report and next.
const (
	flagStatus   = "--status"
	flagCoverage = "--coverage"
)

// reportValueWants names each report value flag's operand for the
// missing-value usage error.
var reportValueWants = map[string]string{
	flagStatus:   "all|open|addressed|deferred|wont-fix|false-positive",
	"--kind":     "all|inline|section",
	"--output":   pathOperand,
	"--sarif":    pathOperand,
	"--category": "logic|edge-case|security|performance|accidental-commit|style|docs|other",
	"--compare":  "<bundle-dir|pr|slug>",
	"--policy":   pathOperand,
}

var reportBoolSetters = map[string]func(*reportcore.Flags){
	"--include-withheld": func(f *reportcore.Flags) { f.IncludeWithheld = true },
	"--cost":             func(f *reportcore.Flags) { f.Cost = true },
	"--provenance":       func(f *reportcore.Flags) { f.Provenance = true },
	"--risk":             func(f *reportcore.Flags) { f.Risk = true },
	"--validation":       func(f *reportcore.Flags) { f.Validation = true },
}

func reportWantedFlags() string {
	names := make([]string, 0, len(reportValueWants)+len(reportBoolSetters)+1)
	for k := range reportValueWants {
		names = append(names, k)
	}
	for k := range reportBoolSetters {
		names = append(names, k)
	}
	names = append(names, "--json")
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// parseFlag handles one report flag, returning how many args it consumed.
// An unknown flag or a missing/invalid value is a usage error (exit 2, no
// envelope).
func parseFlag(r *cli.Runner, flags *reportcore.Flags, args []string, i int) (int, int, bool) {
	a := args[i]
	if want, ok := reportValueWants[a]; ok {
		return parseReportValue(r, flags, args, i, want)
	}
	if set, ok := reportBoolSetters[a]; ok {
		set(flags)
		return 1, 0, false
	}
	return 0, r.ReportUsage(&cli.UsageError{Message: core.Msgf("unknown flag '%s' for report (want %s)", a, reportWantedFlags())}), true
}

// parseReportValue consumes one report value flag and applies its validation.
func parseReportValue(r *cli.Runner, flags *reportcore.Flags, args []string, i int, want string) (int, int, bool) {
	v, next, u := cli.FlagValue(args, i, args[i], want)
	if u != nil {
		return 0, r.ReportUsage(u), true
	}
	switch args[i] {
	case flagStatus:
		return parseStatusFlag(r, flags, v)
	case "--kind":
		return parseKindFlag(r, flags, v)
	case "--category":
		return parseCategoryFlag(r, flags, v)
	case "--compare":
		flags.Compare, flags.CompareSet = v, true
		return next - i + 1, 0, false
	case "--policy":
		if v == "" {
			return 0, r.ReportUsage(&cli.UsageError{
				Message: "--policy needs a policy file path (an empty value would run an ordinary report)",
			}), true
		}
		flags.Policy = v
		return next - i + 1, 0, false
	default: // --output, --sarif
		setReportPath(flags, args[i], v)
		return next - i + 1, 0, false
	}
}

// setReportPath applies the --output/--sarif artifact paths.
func setReportPath(flags *reportcore.Flags, name, value string) {
	switch name {
	case "--output":
		flags.Output = value
	case "--sarif":
		flags.Sarif = value
	}
}

// parseStatusFlag consumes --status, defaulting to all like ${2:-all}.
func parseStatusFlag(r *cli.Runner, flags *reportcore.Flags, next string) (int, int, bool) {
	flags.Status = orDefault(next, statusAll)
	if !validStatus(flags.Status) {
		return 0, r.ReportUsage(usageErr(
			"unknown --status '%s' for report (want all|open|addressed|deferred|wont-fix|false-positive)",
			flags.Status)), true
	}
	return 2, 0, false
}

// parseKindFlag consumes --kind, defaulting to all like ${2:-all}.
func parseKindFlag(r *cli.Runner, flags *reportcore.Flags, next string) (int, int, bool) {
	flags.Kind = orDefault(next, statusAll)
	if !validKind(flags.Kind) {
		return 0, r.ReportUsage(usageErr(
			"unknown --kind '%s' for report (want all|inline|section)",
			flags.Kind)), true
	}
	return 2, 0, false
}

// parseCategoryFlag consumes --category, refusing an unknown filter value as
// the bad-category usage error (a typo would otherwise silently show zero
// findings).
func parseCategoryFlag(r *cli.Runner, flags *reportcore.Flags, next string) (int, int, bool) {
	flags.Category = next
	if !core.ValidCategory(flags.Category) {
		return 0, r.ReportUsage(usageErr(
			"unknown --category '%s' for report (bad-category; try: pass %s)",
			flags.Category, reportValueWants["--category"])), true
	}
	return 2, 0, false
}

// orDefault reads a missing or empty value as the default (the flag is
// consumed either way).
func orDefault(v, dflt string) string {
	if v == "" {
		return dflt
	}
	return v
}

// usageErr builds a verb usage error.
func usageErr(format string, args ...any) *cli.UsageError {
	return &cli.UsageError{Message: core.Msgf(format, args...)}
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
