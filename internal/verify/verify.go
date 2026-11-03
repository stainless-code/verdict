// Package verify is the verify verb's CLI adapter: argv parsing (same
// usage text and exit 2), one call into the verifycore service, envelope
// rendering, and refusal-vs-cancel exit mapping. Domain logic — the
// allowlist gate, the watchdog spawn, and the patch outcome write — lives
// in internal/core/verify.
package verify

import (
	"context"
	"fmt"
	"os"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved verify invocation.
type Request struct {
	Env  map[string]string
	Repo string
	Pr   string
	Root string
}

// Run executes `verify <owner/repo> <slug> [--timeout <secs>] [--json]`,
// returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = ""
	timeoutS, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "verify"); done {
		return code
	}
	resp, ref := verifycore.Run(ctx, verifyRequest(r, req, timeoutS))
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("verify", resp.Repo, resp.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return 0
}

// verifyRequest builds the core request: the injected runtime (full process
// environ snapshot for the child and redaction, the production watchdog,
// the progress observer), the raw timeout string, and the JSON-mode flag
// for the child's NO_COLOR default.
func verifyRequest(r *cli.Runner, req Request, timeoutS string) *core.VerifyRequest {
	root := req.Root
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Watchdog{},
		},
		Repo: req.Repo, Pr: req.Pr, Root: &root,
	}
	return &core.VerifyRequest{
		ReadRequest: read,
		TimeoutRaw:  timeoutS,
		Environ:     os.Environ(),
		JSONMode:    r.JSONMode,
		Warnings:    append([]string{}, r.Warnings...),
	}
}

// defaultTimeoutS is the watchdog budget when --timeout is absent.
const defaultTimeoutS = "300"

// parseFlags strips verify's flags, reporting unknown ones. A value flag with
// no operand is a usage error; an explicit empty --timeout value still falls
// back to the default, mirroring ${2:-300}.
func parseFlags(r *cli.Runner, args []string) (string, int, bool) {
	timeoutS := defaultTimeoutS
	for i := 0; i < len(args); i++ {
		next, code, done := parseToken(r, args, i, &timeoutS)
		if done {
			return "", code, true
		}
		i = next
	}
	return timeoutS, 0, false
}

// parseToken consumes one verify argument, returning the next index plus a
// usage refusal when the token is unknown or --timeout lost its operand.
func parseToken(r *cli.Runner, args []string, i int, timeoutS *string) (int, int, bool) {
	a := args[i]
	if a != "--timeout" {
		return i, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown flag '%s' for verify (want --timeout <secs>, --json)", a)}), true
	}
	v, next, u := cli.FlagValue(args, i, a, "<secs>")
	if u != nil {
		return i, r.ReportUsage(u), true
	}
	if v != "" {
		*timeoutS = v
	}
	return next, 0, false
}
