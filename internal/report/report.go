// Package report is the report family's CLI adapter: argv parsing (same
// usage text and exit 2), one call into the reportcore service, artifact
// writes, envelope/human rendering, and refusal-vs-gate exit mapping.
// Domain logic lives in internal/core/report.
package report

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
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
	tierCritical   = "critical"
	codeCanceled   = "canceled"
	flagJSON       = "--json"
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
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: codeCanceled, Message: "canceled before report completed"}, cli.ExitBroke)
	}
	gate := flags.Check != "" || flags.Completeness
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

// readRequest injects the runner's clock and progress observer into the
// typed read request.
func readRequest(r *cli.Runner, req Request) core.ReadRequest {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = runnerProgress{r}
	return read
}

// runnerProgress routes core's progress observations onto the runner's
// streams: human lines to Human, warnings and diagnostics to Diag.
type runnerProgress struct {
	r *cli.Runner
}

// Human writes one human output line.
func (p runnerProgress) Human(line string) { fmt.Fprintln(p.r.Human, line) }

// Warn writes one warn: line and accumulates it like every CLI warning.
func (p runnerProgress) Warn(msg string) { p.r.Warn(msg) }

// Diag writes one raw diagnostic line.
func (p runnerProgress) Diag(line string) { fmt.Fprintln(p.r.Diag, line) }

// writeArtifact writes one requested output file (0600: the report can
// carry reviewed-diff excerpts).
func writeArtifact(a *core.FileArtifact) *cli.RefusalError {
	if a == nil {
		return nil
	}
	if err := os.WriteFile(a.Path, a.Content, reportFilePerm); err != nil {
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

// parseFlags parses the report flags in shell order. A missing option value
// reads the flag default (the shell's ${2:-default} with a Blind shift 2);
// an invalid value is a usage error (exit 2, no envelope).
func parseFlags(r *cli.Runner, args []string) (reportcore.Flags, int, bool) {
	flags := reportcore.Flags{Status: statusAll, Kind: statusAll, Live: liveAuto}
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
func parseFlag(r *cli.Runner, flags *reportcore.Flags, a, next string) (int, int, bool) {
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

// parseCheckFlag consumes --check, validating the gate tier.
func parseCheckFlag(r *cli.Runner, flags *reportcore.Flags, next string) (int, int, bool) {
	flags.Check = next
	if !validTier(flags.Check) {
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
	case tierCritical, "important", "informational":
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
