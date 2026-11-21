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
	Env      map[string]string
	RepoRoot string
}

// Run executes
// `init [--preset <chill|standard|strict>] [--ci github [--agent <name>]
// [--key-secret <NAME>] [--print]] [--yes] [--json]`, returning the process
// exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = "", ""
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	r.Rentry = flags.rentry(flags.yes, r.JSONMode)
	if code, done := r.Canceled(ctx, "init"); done {
		return code
	}
	resp, ref := initcore.Run(ctx, initRequest(r, req, flags))
	if ref != nil {
		if ref.Code == initcore.CodeNeedsConfirm {
			r.Rentry = flags.rentry(true, r.JSONMode)
		}
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if resp.ExitCode != 0 {
		return resp.ExitCode
	}
	r.Emit("init", "", "", resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire())
	return cli.ExitOK
}

// initRequest injects the runner's clock, streams, stdin seam, and the
// production commander into the typed service request.
func initRequest(r *cli.Runner, req Request, f flags) *initcore.Request {
	out := &initcore.Request{
		Preset:      f.preset,
		CI:          f.ci,
		Agent:       f.agent,
		KeySecret:   f.keySecret,
		Yes:         f.yes,
		Interactive: !r.JSONMode && cli.IsTerminal(os.Stdin),
		Print:       f.print,
		Machine:     r.JSONMode,
		Human:       r.Human,
		Diag:        r.Diag,
		ReadAnswer:  readAnswer,
	}
	out.ReadRequest = r.Read(core.ReadRequest{
		Runtime: &core.Runtime{
			Env: req.Env,
		},
		RepoRoot: req.RepoRoot,
	}, coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true})
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
	preset    string
	ci        string
	agent     string
	keySecret string
	yes       bool
	print     bool
}

// rentry rebuilds the invocation for a refusal's re-enter line: the same
// flags, plus the confirm the refusing run did not carry.
func (f flags) rentry(confirm, machine bool) string {
	out := "verdict init --preset " + f.preset
	if f.ci != "" {
		out += " --ci " + f.ci
	}
	if f.agent != "" {
		out += " --agent " + f.agent
	}
	if f.keySecret != "" {
		out += " --key-secret " + f.keySecret
	}
	if f.print {
		out += " --print"
	}
	if confirm {
		out += " --yes"
	}
	if machine {
		out += " --json"
	}
	return out
}

// The flag names the --ci-scoped parser reads, spelled once so the parser and
// the hints it prints cannot drift apart.
const (
	flagAgent     = "--agent"
	flagKeySecret = "--key-secret"
)

// parseFlags strips init's flags, reporting unknown ones. The --ci-scoped
// flags are usage errors on their own: they describe a workflow nothing else
// would render.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	f := flags{preset: "standard"}
	for i := 0; i < len(args); {
		next, u, handled := parseOne(args, i, &f)
		if u != nil {
			return f, r.ReportUsage(u), true
		}
		if !handled {
			return f, r.ReportUsage(unknownInitFlag(args[i])), true
		}
		i = next
	}
	if u := scopedFlagError(&f); u != nil {
		return f, r.ReportUsage(u), true
	}
	return f, 0, false
}

// unknownInitFlag is the usage error for a flag init does not take.
func unknownInitFlag(name string) *cli.UsageError {
	return &cli.UsageError{Message: fmt.Sprintf(
		"unknown flag '%s' for init (want --preset <chill|standard|strict>, "+
			"--ci github, --agent <name>, --key-secret <NAME>, --print, "+
			"--yes, --force, --json)", name)}
}

// parseOne consumes the flag at args[i] and answers the next index; handled is
// false for a name no init flag carries.
func parseOne(args []string, i int, f *flags) (int, *cli.UsageError, bool) {
	a := args[i]
	switch a {
	case "--print":
		f.print = true
	case "--yes", "-y", "--force":
		f.yes = true
	default:
		if name, value, ok := strings.Cut(a, "="); ok {
			if u, known := equalFlag(name, value, f); known {
				return i + 1, u, true
			}
		}
		return parseValueFlag(args, i, f)
	}
	return i + 1, nil, true
}

// equalFlag stores one `--name=value` flag. known is false for a name no init
// flag carries. An empty value is the missing-value usage error in both forms:
// `--ci=` used to run a plain init, and `--ci ""` still would.
func equalFlag(name, value string, f *flags) (u *cli.UsageError, known bool) {
	switch name {
	case "--preset":
		return storeEqual(value, name, "chill|standard|strict", &f.preset), true
	case "--ci":
		return storeEqual(value, name, "github", &f.ci), true
	case flagAgent:
		return storeEqual(value, name, "<name>", &f.agent), true
	case flagKeySecret:
		return storeEqual(value, name, "<NAME>", &f.keySecret), true
	default:
		return nil, false
	}
}

// storeEqual assigns one `--name=value` operand, answering the missing-value
// usage error when the value is empty.
func storeEqual(value, name, want string, dst *string) *cli.UsageError {
	if value == "" {
		return cli.MissingValue(name, want)
	}
	*dst = value
	return nil
}

// parseValueFlag reads one `--name value` pair, reporting the missing-value
// usage error cli.FlagValue raises.
func parseValueFlag(args []string, i int, f *flags) (int, *cli.UsageError, bool) {
	switch args[i] {
	case "--preset":
		return flagValue(args, i, "--preset", "chill|standard|strict", &f.preset)
	case "--ci":
		return flagValue(args, i, "--ci", "github", &f.ci)
	case flagAgent:
		return flagValue(args, i, flagAgent, "<name>", &f.agent)
	case flagKeySecret:
		return flagValue(args, i, flagKeySecret, "<NAME>", &f.keySecret)
	}
	return i, nil, false
}

// flagValue stores the value following args[i] in dst and answers the index
// after it. An empty operand is the missing-value usage error, as in the
// `--name=` form: `--ci ""` otherwise runs a plain init, `--agent ""` silently
// defaults to claude, and `--key-secret ""` is dropped without a word.
func flagValue(args []string, i int, name, want string, dst *string) (int, *cli.UsageError, bool) {
	v, next, u := cli.FlagValue(args, i, name, want)
	if u != nil {
		return i, u, true
	}
	if u := storeEqual(v, name, want, dst); u != nil {
		return i, u, true
	}
	return next + 1, nil, true
}

// scopedFlagError rejects the --ci-scoped flags on their own and fills the
// default agent: they describe a workflow nothing else would render.
func scopedFlagError(f *flags) *cli.UsageError {
	if f.ci == "" {
		return unscopeError(f)
	}
	if f.ci != "github" {
		return &cli.UsageError{Message: fmt.Sprintf("unknown --ci '%s' for init (want github)", f.ci)}
	}
	if f.agent == "" {
		f.agent = "claude"
	}
	return nil
}

// unscopeError names the first --ci-scoped flag given without --ci.
func unscopeError(f *flags) *cli.UsageError {
	for _, scoped := range []struct {
		name  string
		given bool
	}{{flagAgent, f.agent != ""}, {flagKeySecret, f.keySecret != ""}, {"--print", f.print}} {
		if scoped.given {
			return &cli.UsageError{Message: scoped.name + " needs --ci github"}
		}
	}
	return nil
}
