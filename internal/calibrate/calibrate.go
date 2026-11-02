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

// Refusal codes, outcome and status vocabularies, and store keys shared by
// the live join and the probe, byte-identical to the shell.
const (
	codeNoThreads             = "no-threads"
	codeProviderUnimplemented = "provider-unimplemented"
	codeMissingPost           = "missing-post"
	codeThreadsFailed         = "threads-failed"
	codePRReadFailed          = "pr-read-failed"
	codeBadJudgment           = "bad-judgment"
	codeUnknownJudgment       = "unknown-judgment"
	codeCalibrateFailed       = "calibrate-failed"

	statusAddressed     = "addressed"
	statusFalsePositive = "false-positive"
	statusOpen          = "open"
	statusDeferred      = "deferred"

	outcomeFixed           = "fixed"
	outcomeResolvedUnfixed = "resolved-unfixed"
	outcomeUnknown         = "unknown"
	outcomeUntagged        = "untagged"
	tierResolvedUnfixed    = "resolved_unfixed"

	verdictReal    = "real"
	verdictFalse   = "false"
	verdictUnknown = "unknown"

	helpfulTrue  = "true"
	helpfulFalse = "false"
	helpfulNull  = "null"

	keySig          = "sig"
	keyAgentVerdict = "agent_verdict"
	keyAgentHelpful = "agent_helpful"

	unmarkedValue  = "unmarked"
	postFileSuffix = "/post.json"
)

// Run executes `calibrate <owner/repo> <pr> [--probe ...] [--json]`,
// returning the exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	st, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before calibrate completed"}, cli.ExitBroke)
	}
	if !st.probe && (len(st.specs) > 0 || st.judgeFile != "") {
		return r.ReportUsage(&cli.UsageError{Message: "--judge and --judge-file need --probe (judgments only merge on a probe run)"})
	}
	dir := readcommon.ResolveBundleDir(req.Pr)
	if st.probe {
		return runProbe(ctx, r, req, dir, st.specs, st.judgeFile)
	}
	return runLive(ctx, r, req, dir)
}

// flagState carries the calibrate flag parse.
type flagState struct {
	judgeFile string
	specs     []string
	probe     bool
}

// judge is one agent judgment.
type judge struct {
	helpful any
	sig     string
	verdict string
}

// parseFlags parses the calibrate flags, in shell order: missing values
// and unknown flags are usage errors; spec validation happens on the probe
// run as typed refusals.
func parseFlags(r *cli.Runner, args []string) (flagState, int, bool) {
	var st flagState
	i := 0
	for i < len(args) {
		next, code, done := parseArg(r, &st, args, i)
		if done {
			return flagState{}, code, true
		}
		i = next
	}
	return st, 0, false
}

// parseArg ports one flag step, returning the next index (or the usage
// exit when done).
func parseArg(r *cli.Runner, st *flagState, args []string, i int) (int, int, bool) {
	a := args[i]
	next := ""
	if i+1 < len(args) {
		next = args[i+1]
	}
	switch {
	case a == "--probe":
		st.probe = true
		return i + 1, 0, false
	case a == "--judge":
		return takeJudge(st, r, next, i)
	case strings.HasPrefix(a, "--judge="):
		return takeJudgeEq(st, r, strings.TrimPrefix(a, "--judge="), i)
	case a == "--judge-file":
		return takeJudgeFile(st, r, next, i)
	case strings.HasPrefix(a, "--judge-file="):
		return takeJudgeFileEq(st, r, strings.TrimPrefix(a, "--judge-file="), i)
	case a == "--json":
		r.JSONMode = true
		routeHuman(r)
		return i + 1, 0, false
	default:
		return 0, usage(r, fmt.Sprintf("unknown flag '%s' for calibrate (want --probe, --judge, --judge-file, --json)", a)), true
	}
}

// usage reports one usage error.
func usage(r *cli.Runner, msg string) int {
	return r.ReportUsage(&cli.UsageError{Message: msg})
}

// takeJudge ports one --judge value.
func takeJudge(st *flagState, r *cli.Runner, next string, i int) (int, int, bool) {
	if next == "" {
		return 0, usage(r, "missing value for --judge (want SIG:VERDICT[:HELPFUL], "+
			"verdict real|false|unknown, helpful true|false|null)"), true
	}
	st.specs = append(st.specs, next)
	return i + 2, 0, false
}

// takeJudgeEq ports one --judge= value.
func takeJudgeEq(st *flagState, r *cli.Runner, value string, i int) (int, int, bool) {
	if value == "" {
		return 0, usage(r, "empty value for --judge (want SIG:VERDICT[:HELPFUL])"), true
	}
	st.specs = append(st.specs, value)
	return i + 1, 0, false
}

// takeJudgeFile ports one --judge-file value.
func takeJudgeFile(st *flagState, r *cli.Runner, next string, i int) (int, int, bool) {
	if next == "" {
		return 0, usage(r, "missing value for --judge-file (want PATH to a JSON/JSONL judgments fragment)"), true
	}
	if st.judgeFile != "" {
		return 0, usage(r, "duplicate --judge-file (pass one fragment)"), true
	}
	st.judgeFile = next
	return i + 2, 0, false
}

// takeJudgeFileEq ports one --judge-file= value.
func takeJudgeFileEq(st *flagState, r *cli.Runner, value string, i int) (int, int, bool) {
	if value == "" {
		return 0, usage(r, "empty value for --judge-file (want PATH)"), true
	}
	if st.judgeFile != "" {
		return 0, usage(r, "duplicate --judge-file (pass one fragment)"), true
	}
	st.judgeFile = value
	return i + 1, 0, false
}

// routeHuman sends human progress to stderr in JSON mode.
func routeHuman(r *cli.Runner) {
	if r.JSONMode {
		r.Human = r.Diag
	} else {
		r.Human = r.Out
	}
}
