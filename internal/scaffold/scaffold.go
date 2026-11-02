// Package scaffold is the init verb's CLI adapter: argv parsing, the stdin
// confirm seam, one call into the initcore service, envelope rendering, and
// exit mapping. Domain logic — the preset matrix, control-file writes, and
// check-tree validation — lives in internal/core/init.
package scaffold

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	initcore "github.com/stainless-code/verdict/internal/core/init"
)

// Request carries the resolved init invocation (init takes no positionals).
type Request struct {
	Env map[string]string
}

// Run executes `init --preset <chill|standard|strict> [--yes] [--json]`,
// returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = "", ""
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	r.Rentry = "verdict init --preset " + flags.preset
	if flags.yes {
		r.Rentry += " --yes"
	}
	if r.JSONMode {
		r.Rentry += " --json"
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{
			Code:    "canceled",
			Message: "canceled before init completed",
		}, cli.ExitBroke)
	}
	resp, ref := initcore.Run(ctx, initRequest(r, req, flags))
	if ref != nil {
		return refuse(r, ref)
	}
	r.Warnings = resp.Warnings
	if resp.ExitCode != 0 {
		return resp.ExitCode
	}
	if r.JSONMode {
		env := core.RenderEnvelope("init", "", "", resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprintln(r.Out, env.MarshalPretty())
	}
	return cli.ExitOK
}

// refuse replays the service's accumulated warnings before rendering.
func refuse(r *cli.Runner, ref *core.RefusalError) int {
	if ref.Warnings != nil {
		r.Warnings = ref.Warnings
	}
	return r.Refuse(ref)
}

// initRequest injects the runner's clock, streams, stdin seam, and the
// production commander into the typed service request.
func initRequest(r *cli.Runner, req Request, f flags) *initcore.Request {
	out := &initcore.Request{
		Preset:      f.preset,
		Yes:         f.yes,
		Interactive: !r.JSONMode && cli.IsTerminal(os.Stdin),
		Human:       r.Human,
		Diag:        r.Diag,
		ReadAnswer:  readAnswer,
	}
	out.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: runnerProgress{r},
			Commander: coreexec.SplitCommander{},
		},
	}
	return out
}

// readAnswer reads one stdin line for the human confirm (errors read empty,
// matching `read -r ans || ans=""`, and only the trailing newline is cut).
func readAnswer() string {
	rd := bufio.NewReader(os.Stdin)
	line, err := rd.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(line, "\n")
}

// runnerProgress routes core's warning observations onto the runner's frozen
// diagnostic stream.
type runnerProgress struct {
	r *cli.Runner
}

// Human writes one rendered human line (init writes its human bytes through
// the request's writer, so this is only a fallback).
func (p runnerProgress) Human(line string) { fmt.Fprintln(p.r.Human, line) }

// Warn records and streams one warning.
func (p runnerProgress) Warn(msg string) { p.r.Warn(msg) }

// Diag writes one raw diagnostic line.
func (p runnerProgress) Diag(line string) { fmt.Fprintln(p.r.Diag, line) }

// flags carries the parsed init options.
type flags struct {
	preset string
	yes    bool
}

// parseFlags strips init's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	f := flags{preset: "standard"}
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--preset":
			f.preset = presetValue(args, &i)
		case strings.HasPrefix(a, "--preset="):
			f.preset = strings.TrimPrefix(a, "--preset=")
		case a == "--yes" || a == "-y" || a == "--force":
			f.yes = true
		case a == "--json":
		default:
			msg := fmt.Sprintf(
				"unknown flag '%s' for init (want --preset <chill|standard|strict>, "+
					"--yes, --force, --json)",
				a,
			)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
		i++
	}
	return f, 0, false
}

// presetValue reads the --preset operand, advancing i past it.
func presetValue(args []string, i *int) string {
	if *i+1 < len(args) {
		*i++
		return args[*i]
	}
	return ""
}
