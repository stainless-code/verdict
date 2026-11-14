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
	"github.com/stainless-code/verdict/internal/core/risk"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
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
	liveAuto       = "auto"
	reportFilePerm = 0o600
)

// Run executes `report <owner/repo> <pr> [flags]`, returning the exit code.
// Human progress goes to the runner's human stream; --json mode prints
// exactly one envelope to stdout.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	setupRunner(r, req)
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "report"); done {
		return code
	}
	gate := flags.Check != "" || flags.Completeness || flags.Coverage != "" || flags.MaxRisk != ""
	resp, ref := reportcore.Run(ctx, reportRequest(r, req, &flags))
	if ref != nil {
		return refuseGate(r, ref, gate)
	}
	r.Warnings = resp.Warnings
	if ref := writeOutputs(resp); ref != nil {
		return refuseGate(r, ref, gate)
	}
	emitReport(r, req, resp)
	emitChecks(r, resp)
	if resp.GateFailed {
		return cli.ExitRefused
	}
	return cli.ExitOK
}

// writeOutputs writes the requested artifacts in shell order: --output
// first, then --sarif.
func writeOutputs(resp *core.ReportResponse) *cli.RefusalError {
	if ref := writeArtifact(resp.Output); ref != nil {
		return ref
	}
	return writeArtifact(resp.Sarif)
}

// emitReport writes the artifact notices, then the envelope or the human
// summary.
func emitReport(r *cli.Runner, req Request, resp *core.ReportResponse) {
	if resp.Sarif != nil {
		fmt.Fprintf(r.Human, "wrote %s\n", resp.Sarif.Path)
	}
	if r.JSONMode {
		if resp.Output != nil {
			fmt.Fprintf(r.Human, "wrote %s\n", resp.Output.Path)
		}
		env := core.RenderEnvelope("report", req.Repo, req.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return
	}
	for _, line := range resp.Human {
		fmt.Fprintln(r.Human, line)
	}
}

// emitChecks writes the gate diagnostics after output.
func emitChecks(r *cli.Runner, resp *core.ReportResponse) {
	for _, line := range resp.CheckLines {
		fmt.Fprintln(r.Diag, line)
	}
}

// reportRequest builds the core request from the parsed flags and runner.
func reportRequest(r *cli.Runner, req Request, flags *reportcore.Flags) *core.ReportRequest {
	return &core.ReportRequest{
		ReadRequest: readRequest(r, req),
		Flags:       *flags,
		Effective:   r.Effective,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// readRequest injects the runner's clock, progress observer, and command
// runner into the typed read request.
func readRequest(r *cli.Runner, req Request) core.ReadRequest {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = cli.Progress{R: r}
	read.Commander = coreexec.Runner{}
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
			Message: fmt.Sprintf("could not write %s: %v", a.Path, err),
		}
	}
	return nil
}

// setupRunner records the invocation (no re-entry line: report sets none).
func setupRunner(r *cli.Runner, req Request) {
	r.Repo, r.Pr = req.Repo, req.Pr
}

// refuseGate renders a typed refusal, exiting 3 on gate runs so hooks can
// tell tool failure apart from a failed gate (exit 1).
func refuseGate(r *cli.Runner, err error, gate bool) int {
	if ref, ok := errors.AsType[*cli.RefusalError](err); ok && ref.Warnings != nil {
		r.Warnings = ref.Warnings
	}
	if gate {
		return r.RefuseExit(err, cli.ExitBroke)
	}
	return r.Refuse(err)
}

// parseFlags parses the report flags left to right. A value flag with no
// operand is a usage error (exit 2, no envelope); an invalid value is too.
func parseFlags(r *cli.Runner, args []string) (reportcore.Flags, int, bool) {
	flags := reportcore.Flags{Status: statusAll, Kind: statusAll, Live: liveAuto}
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
	"--output":   "<path>",
	"--sarif":    "<path>",
	"--check":    "critical|important|informational",
	"--live":     "auto|strict|skip",
	"--category": "logic|edge-case|security|performance|accidental-commit|style|docs|other",
	"--max-risk": "low|medium|high|critical",
}

var reportBoolSetters = map[string]func(*reportcore.Flags){
	"--include-withheld": func(f *reportcore.Flags) { f.IncludeWithheld = true },
	"--cost":             func(f *reportcore.Flags) { f.Cost = true },
	"--completeness":     func(f *reportcore.Flags) { f.Completeness = true },
	"--risk":             func(f *reportcore.Flags) { f.Risk = true },
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
	return 0, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for report (want %s)", a, reportWantedFlags())}), true
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
	case "--check":
		return parseCheckFlag(r, flags, v)
	case "--live":
		return parseLiveFlag(r, flags, v)
	case "--max-risk":
		return parseMaxRiskFlag(r, flags, v)
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
			"unknown --category '%s' for report (bad-category; try: pass "+reportValueWants["--category"]+")",
			flags.Category)), true
	}
	return 2, 0, false
}

// parseCheckFlag consumes --check, validating the gate tier.
func parseCheckFlag(r *cli.Runner, flags *reportcore.Flags, next string) (int, int, bool) {
	flags.Check = next
	if !core.ValidTier(flags.Check) {
		return 0, r.ReportUsage(usageErr(
			"unknown --check tier '%s' for report (want critical|important|informational)",
			flags.Check)), true
	}
	return 2, 0, false
}

// parseLiveFlag consumes --live, validating the live posture.
func parseLiveFlag(r *cli.Runner, flags *reportcore.Flags, next string) (int, int, bool) {
	flags.Live = orDefault(next, "")
	if !validLive(flags.Live) {
		return 0, r.ReportUsage(usageErr(
			"unknown --live posture '%s' for report (want auto|strict|skip)",
			flags.Live)), true
	}
	return 2, 0, false
}

// parseMaxRiskFlag consumes --max-risk, validating the risk threshold level.
func parseMaxRiskFlag(r *cli.Runner, flags *reportcore.Flags, next string) (int, int, bool) {
	flags.MaxRisk = next
	if !risk.ValidLevel(flags.MaxRisk) {
		return 0, r.ReportUsage(usageErr(
			"unknown --max-risk level '%s' for report (want low|medium|high|critical)",
			flags.MaxRisk)), true
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

// validLive reports whether s is a known live posture.
func validLive(s string) bool {
	switch s {
	case liveAuto, "strict", "skip":
		return true
	default:
		return false
	}
}
