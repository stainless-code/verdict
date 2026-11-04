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
	if code, done := r.Canceled(ctx, "init"); done {
		return code
	}
	resp, ref := initcore.Run(ctx, initRequest(r, req, flags))
	if ref != nil {
		return r.RefuseTyped(ref)
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
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r},
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
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
			v, next, u := cli.FlagValue(args, i, a, "chill|standard|strict")
			if u != nil {
				return f, r.ReportUsage(u), true
			}
			f.preset = v
			i = next
		case strings.HasPrefix(a, "--preset="):
			f.preset = strings.TrimPrefix(a, "--preset=")
		case a == "--yes" || a == "-y" || a == "--force":
			f.yes = true
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
