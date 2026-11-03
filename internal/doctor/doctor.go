// Package doctor is the doctor verb's CLI adapter: argv parsing, one call
// into the doctorcore service, warning routing, envelope rendering, and
// exit mapping. Domain logic lives in internal/core/doctor.
package doctor

import (
	"context"
	"fmt"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	doctorcore "github.com/stainless-code/verdict/internal/core/doctor"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a doctor run.
type Request = readcommon.Request

// Run executes `doctor [--json]`, returning the exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	if code, done := parseFlags(r, args); done {
		return code
	}
	if code, done := r.Canceled(ctx, "doctor"); done {
		return code
	}
	now := runTime(r)
	writeStamp(r, now)
	resp, ref := doctorcore.Run(ctx, doctorRequest(r, req, now))
	if ref != nil {
		return r.Refuse(ref)
	}
	r.Warnings = resp.Warnings
	writeResponse(r, resp)
	if resp.Healthy {
		return 0
	}
	return 1
}

// writeStamp prints the human run stamp first; --json carries `at` in the
// envelope instead, so its streams stay byte-identical.
func writeStamp(r *cli.Runner, now time.Time) {
	if r.JSONMode {
		return
	}
	fmt.Fprintf(r.Human, "doctor @ %s\n", core.NowUTC(now))
}

// writeResponse renders one surface: the single envelope for --json, or the
// trailing human summary built from the existing check rows.
func writeResponse(r *cli.Runner, resp *doctorcore.Response) {
	if r.JSONMode {
		env := core.RenderEnvelope("doctor", "", "", resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return
	}
	ok, warn, fail := resp.Data.Counts()
	verdict := "unhealthy"
	if resp.Healthy {
		verdict = "healthy"
	}
	fmt.Fprintf(r.Human, "Summary: %s — %d ok, %d warn, %d fail\n", verdict, ok, warn, fail)
}

// runTime captures one clock read so the human run stamp and the envelope's
// `at` name the same instant even across a second boundary.
func runTime(r *cli.Runner) time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

// doctorRequest injects the runner's clock, progress observer, and command
// runner into the typed service request.
func doctorRequest(r *cli.Runner, req Request, now time.Time) *doctorcore.Request {
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Now = func() time.Time { return now }
	read.Progress = cli.Progress{R: r}
	read.Commander = coreexec.Runner{}
	return &doctorcore.Request{
		ReadRequest: read,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// parseFlags parses the doctor flags.
func parseFlags(r *cli.Runner, args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	return r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for doctor (want --json)", args[0])}), true
}
