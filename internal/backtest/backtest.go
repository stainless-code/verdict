// Package backtest is the backtest verb's CLI adapter: argv parsing, one call
// into the core mining service, and envelope rendering. It is CLI-only on
// purpose — replay will spend on model runs, so neither the MCP surface nor
// the SDK exposes it.
package backtest

import (
	"context"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	corebacktest "github.com/stainless-code/verdict/internal/core/backtest"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved invocation.
type Request struct {
	Env      map[string]string
	Root     string
	RepoRoot string
}

// wanted is the flag list an unknown-flag usage error names.
const wanted = "--since <90d|12w|18mo|2y|ref>, --until <ref>, --run <slug>, --json"

// Run executes `backtest mine --since ... [--until ...] [--run ...] [--json]`.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	mode, flags, uerr := parseFlags(args)
	if uerr != nil {
		return r.ReportUsage(uerr)
	}
	r.Rentry = rentry(flags, r.JSONMode)
	if code, done := r.Canceled(ctx, "backtest"); done {
		return code
	}
	resp, ref := corebacktest.Run(ctx, &corebacktest.Request{
		Runtime: &core.Runtime{
			Env: req.Env, Now: r.Now, Progress: cli.Progress{R: r}, Commander: coreexec.Runner{MissingTool: true},
		},
		RepoRoot: req.RepoRoot, Mode: mode, Since: flags.since, Until: flags.until,
		Run: flags.run, Warnings: append([]string{}, r.Warnings...),
	})
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Warnings = resp.Warnings
	if r.JSONMode {
		fmt.Fprint(r.Out, jq.PrettyOrdered(core.RenderEnvelope("backtest", "", "", resp.At,
			resp.Next, resp.Warnings, resp.Data.Wire(), nil)))
	}
	return cli.ExitOK
}

// flags is the parsed mine surface.
type flags struct {
	since string
	until string
	run   string
}

// parseFlags reads the positional mode and the verb's flags.
func parseFlags(args []string) (string, flags, *cli.UsageError) {
	f := flags{until: "HEAD"}
	mode := ""
	for i := 0; i < len(args); i++ {
		next, uerr := consume(&f, &mode, args, i)
		if uerr != nil {
			return "", f, uerr
		}
		i = next
	}
	if mode == "" {
		return "", f, usage("backtest needs the 'mine' mode")
	}
	return mode, f, checkMode(mode)
}

// consume reads one position or flag, returning the next argv index.
func consume(f *flags, mode *string, args []string, i int) (int, *cli.UsageError) {
	a := args[i]
	if !strings.HasPrefix(a, "-") {
		var uerr *cli.UsageError
		if *mode, uerr = takeMode(*mode, a); uerr != nil {
			return i, uerr
		}
		return i, nil
	}
	return parseToken(f, args, i)
}

// checkMode accepts the one shipped mode.
func checkMode(mode string) *cli.UsageError {
	if mode == "mine" {
		return nil
	}
	return usage("unknown mode '" + mode + "' for backtest (want mine)")
}

// takeMode accepts the one positional mode.
func takeMode(mode, arg string) (string, *cli.UsageError) {
	if mode != "" {
		return mode, usage("unexpected argument '" + arg + "' for backtest")
	}
	return arg, nil
}

// parseToken consumes one flag and its operand, returning the next index.
func parseToken(f *flags, args []string, i int) (int, *cli.UsageError) {
	a := args[i]
	switch a {
	case "--since":
		v, next, uerr := cli.FlagValue(args, i, a, "<duration|ref>")
		if uerr != nil {
			return i, uerr
		}
		f.since = v
		return next, nil
	case "--until":
		v, next, uerr := cli.FlagValue(args, i, a, "<ref>")
		if uerr != nil {
			return i, uerr
		}
		f.until = v
		return next, nil
	case "--run":
		v, next, uerr := cli.FlagValue(args, i, a, "<slug>")
		if uerr != nil {
			return i, uerr
		}
		f.run = v
		return next, nil
	default:
		return i, usage("unknown flag '" + a + "' for backtest (want " + wanted + ")")
	}
}

// usage builds the exit-2 flag error.
func usage(msg string) *cli.UsageError {
	return &cli.UsageError{Message: msg}
}

// rentry rebuilds the re-run command after flag parsing.
func rentry(f flags, jsonMode bool) string {
	s := "verdict backtest mine"
	if f.since != "" {
		s += " --since " + f.since
	}
	if f.until != "" && f.until != "HEAD" {
		s += " --until " + f.until
	}
	if f.run != "" {
		s += " --run " + f.run
	}
	if jsonMode {
		s += " --json"
	}
	return s
}
