// Package pack is the pack verb's CLI adapter: argv parsing, one call into
// the pack service, and envelope rendering. Domain logic lives in
// internal/core/pack.
package pack

import (
	"context"
	"fmt"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// verbPack is the verb name every envelope carries.
const verbPack = "pack"

// Request wires a pack run.
type Request = readcommon.Request

const packUsage = "usage: verdict pack <list|add> [--json]\n" +
	"       verdict pack add <dir|git-url>[@ref] [--json]"

// Run executes `pack list [--json]` or `pack add <dir|git-url>[@ref] [--json]`,
// returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	inv, code, done := checkArgs(r, args)
	if done {
		return code
	}
	r.RouteHuman()
	if code, done := r.Canceled(ctx, verbPack); done {
		return code
	}
	read := req
	if read.Runtime == nil {
		read.Runtime = &core.Runtime{}
	}
	read.Progress = cli.Progress{R: r}
	if inv.sub == "list" {
		return runList(ctx, r, read)
	}
	return runAdd(ctx, r, read, inv.source)
}

// invocation is one parsed pack command line.
type invocation struct {
	sub    string
	source string
}

// runList executes the read-only inventory.
func runList(ctx context.Context, r *cli.Runner, read Request) int {
	read.PackFS = skillfs.FS
	resp, ref := packcore.RunList(ctx, &packcore.Request{
		ReadRequest:  read,
		SettingsPath: settingsPath(read),
		List:         true,
	})
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	if r.JSONMode {
		env := core.RenderEnvelope(verbPack, "", "", resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	for _, line := range resp.Human {
		fmt.Fprintln(r.Human, line)
	}
	return cli.ExitOK
}

// runAdd executes the vendoring and locking path.
func runAdd(ctx context.Context, r *cli.Runner, read Request, source string) int {
	read.Commander = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
	resp, ref := packcore.RunAdd(ctx, &packcore.AddRequest{ReadRequest: read, Source: source})
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	if r.JSONMode {
		env := core.RenderEnvelope(verbPack, "", "", resp.At, resp.Next,
			nil, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	for _, line := range resp.Human {
		fmt.Fprintln(r.Human, line)
	}
	return cli.ExitOK
}

// checkArgs accepts exactly `list` or `add <source>`.
func checkArgs(r *cli.Runner, args []string) (invocation, int, bool) {
	if len(args) == 0 {
		return invocation{}, r.ReportUsage(&cli.UsageError{Message: packUsage}), true
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return invocation{}, r.ReportUsage(&cli.UsageError{Message: packUsage}), true
		}
		return invocation{sub: "list"}, 0, false
	case "add":
		if len(args) != 2 || args[1] == "" {
			return invocation{}, r.ReportUsage(&cli.UsageError{Message: packUsage}), true
		}
		return invocation{sub: "add", source: args[1]}, 0, false
	default:
		return invocation{}, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown pack subcommand '%s' (want list or add)", args[0])}), true
	}
}

// settingsPath resolves the operator settings file from the injected env.
func settingsPath(req Request) string {
	if req.Runtime == nil {
		return ""
	}
	return config.SettingsPath(req.Env["HOME"], req.Env["XDG_CONFIG_HOME"])
}
