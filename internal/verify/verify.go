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
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before verify completed"}, cli.ExitBroke)
	}
	resp, ref := verifycore.Run(ctx, verifyRequest(r, req, timeoutS))
	if ref != nil {
		return refuseVerify(r, ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("verify", resp.Repo, resp.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	}
	return 0
}

// refuseVerify renders a verify refusal: warnings carry onto the runner,
// a cancel exits 3 (scripts treat it as broke), anything else exits 1.
func refuseVerify(r *cli.Runner, ref *core.RefusalError) int {
	if ref.Warnings != nil {
		r.Warnings = ref.Warnings
	}
	if ref.Code == "canceled" {
		return r.RefuseExit(ref, cli.ExitBroke)
	}
	return r.Refuse(ref)
}

// verifyRequest builds the core request: the injected runtime (full process
// environ snapshot for the child and redaction, the production watchdog,
// the progress observer), the raw timeout string, and the JSON-mode flag
// for the child's NO_COLOR default.
func verifyRequest(r *cli.Runner, req Request, timeoutS string) *core.VerifyRequest {
	root := req.Root
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: runnerProgress{r},
			Commander: coreexec.OSCommander{},
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

// defaultTimeoutS is the watchdog budget when --timeout is absent.
const defaultTimeoutS = "300"

// parseFlags strips verify's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (string, int, bool) {
	timeoutS := defaultTimeoutS
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--timeout":
			timeoutS, i = parseTimeoutFlag(args, i)
		case "--json":
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for verify (want --timeout <secs>, --json)", a)}), true
		}
	}
	return timeoutS, 0, false
}

// parseTimeoutFlag reads the --timeout value at args[i], returning the
// value and the index of the last consumed argument.
func parseTimeoutFlag(args []string, i int) (string, int) {
	if i+1 >= len(args) {
		return defaultTimeoutS, i
	}
	// Mirror ${2:-300}: an explicit empty value falls back
	// to the default rather than refusing.
	if v := args[i+1]; v != "" {
		return v, i + 1
	}
	return defaultTimeoutS, i + 1
}
