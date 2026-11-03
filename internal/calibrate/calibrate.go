// Package calibrate is the calibrate verb's CLI adapter: argv parsing, one
// call into the calibratecore service, warning routing, envelope
// rendering, and exit mapping. Domain logic lives in internal/core/calibrate.
package calibrate

import (
	"context"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a calibrate run.
type Request = readcommon.Request

// Run executes `calibrate <owner/repo> <pr> [--probe ...] [--json]`,
// returning the exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	st, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "calibrate"); done {
		return code
	}
	if !st.probe && (len(st.specs) > 0 || st.judgeFile != "") {
		return r.ReportUsage(&cli.UsageError{Message: "--judge and --judge-file need --probe (judgments only merge on a probe run)"})
	}
	resp, ref := calibratecore.Run(ctx, calibrateRequest(r, req, st))
	if ref != nil {
		return r.Refuse(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("calibrate", req.Repo, req.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return 0
}

// calibrateRequest injects the runner's clock, progress observer, and
// command runner into the typed service request.
func calibrateRequest(r *cli.Runner, req Request, st flagState) *calibratecore.Request {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = r.Now
	read.Progress = cli.Progress{R: r}
	read.Commander = coreexec.Runner{}
	return &calibratecore.Request{
		ReadRequest: read,
		Probe:       st.probe,
		Specs:       st.specs,
		JudgeFile:   st.judgeFile,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// flagState carries the calibrate flag parse.
type flagState struct {
	judgeFile string
	specs     []string
	probe     bool
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
