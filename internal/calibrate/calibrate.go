// Package calibrate ports the `calibrate` verb: the live-ground-truth
// join (thread states plus files changed since the review head) and the
// offline self-grading probe. Read-only except the probe's
// kb-grades.jsonl; never auto-tunes.
package calibrate

import (
	"context"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a calibrate run.
type Request = readcommon.Request

// Run executes `calibrate <owner/repo> <pr> [--probe ...] [--json]`,
// returning the exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	probe, specs, judgeFile, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before calibrate completed"}, cli.ExitBroke)
	}
	if !probe && (len(specs) > 0 || judgeFile != "") {
		return r.ReportUsage(&cli.UsageError{Message: "--judge and --judge-file need --probe (judgments only merge on a probe run)"})
	}
	dir := readcommon.ResolveBundleDir(req.Pr)
	if probe {
		return runProbe(ctx, r, req, dir, specs, judgeFile)
	}
	return runLive(ctx, r, req, dir)
}

// judge is one agent judgment.
type judge struct {
	sig        string
	verdict    string
	helpful    any
	hasHelpful bool
}

// parseFlags parses the calibrate flags, in shell order: missing values
// and unknown flags are usage errors; spec validation happens on the probe
// run as typed refusals.
func parseFlags(r *cli.Runner, args []string) (bool, []string, string, int, bool) {
	probe := false
	var specs []string
	judgeFile := ""
	i := 0
	for i < len(args) {
		a := args[i]
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch {
		case a == "--probe":
			probe = true
			i++
		case a == "--judge":
			if next == "" {
				return false, nil, "", r.ReportUsage(&cli.UsageError{Message: "missing value for --judge (want SIG:VERDICT[:HELPFUL], verdict real|false|unknown, helpful true|false|null)"}), true
			}
			specs = append(specs, next)
			i += 2
		case strings.HasPrefix(a, "--judge="):
			value := strings.TrimPrefix(a, "--judge=")
			if value == "" {
				return false, nil, "", r.ReportUsage(&cli.UsageError{Message: "empty value for --judge (want SIG:VERDICT[:HELPFUL])"}), true
			}
			specs = append(specs, value)
			i++
		case a == "--judge-file":
			if next == "" {
				return false, nil, "", r.ReportUsage(&cli.UsageError{Message: "missing value for --judge-file (want PATH to a JSON/JSONL judgments fragment)"}), true
			}
			if judgeFile != "" {
				return false, nil, "", r.ReportUsage(&cli.UsageError{Message: "duplicate --judge-file (pass one fragment)"}), true
			}
			judgeFile = next
			i += 2
		case strings.HasPrefix(a, "--judge-file="):
			value := strings.TrimPrefix(a, "--judge-file=")
			if value == "" {
				return false, nil, "", r.ReportUsage(&cli.UsageError{Message: "empty value for --judge-file (want PATH)"}), true
			}
			if judgeFile != "" {
				return false, nil, "", r.ReportUsage(&cli.UsageError{Message: "duplicate --judge-file (pass one fragment)"}), true
			}
			judgeFile = value
			i++
		case a == "--json":
			r.JSONMode = true
			routeHuman(r)
			i++
		default:
			return false, nil, "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for calibrate (want --probe, --judge, --judge-file, --json)", a)}), true
		}
	}
	return probe, specs, judgeFile, 0, false
}

// routeHuman sends human progress to stderr in JSON mode.
func routeHuman(r *cli.Runner) {
	if r.JSONMode {
		r.Human = r.Diag
	} else {
		r.Human = r.Out
	}
}
