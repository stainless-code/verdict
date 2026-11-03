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
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before doctor completed"}, cli.ExitBroke)
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
	read.Progress = runnerProgress{r}
	read.Commander = coreexec.Runner{}
	return &doctorcore.Request{
		ReadRequest: read,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// runnerProgress routes core's progress observations onto the runner's
// frozen streams: human lines and diagnostics keep their streams,
// warnings keep the `warn: ` prefix and accumulate.
type runnerProgress struct {
	r *cli.Runner
}

// Human writes one rendered human line.
func (p runnerProgress) Human(line string) { fmt.Fprintln(p.r.Human, line) }

// Warn records and streams one warning.
func (p runnerProgress) Warn(msg string) { p.r.Warn(msg) }

// Diag writes one raw diagnostic line.
func (p runnerProgress) Diag(line string) { fmt.Fprintln(p.r.Diag, line) }

// parseFlags parses the doctor flags.
func parseFlags(r *cli.Runner, args []string) (int, bool) {
	for _, a := range args {
		switch a {
		case "--json":
			r.JSONMode = true
			routeHuman(r)
		default:
			return r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown flag '%s' for doctor (want --json)", a)}), true
		}
	}
	return 0, false
}

// routeHuman sends human progress to stderr in JSON mode.
func routeHuman(r *cli.Runner) {
	if r.JSONMode {
		r.Human = r.Diag
	} else {
		r.Human = r.Out
	}
}
