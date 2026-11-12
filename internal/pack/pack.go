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
	packcore "github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// verbPack is the verb name every envelope carries.
const verbPack = "pack"

// Request wires a pack run.
type Request = readcommon.Request

// Run executes `pack list [--json]`, returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	if code, done := checkArgs(r, args); done {
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

// checkArgs accepts exactly the one served subcommand.
func checkArgs(r *cli.Runner, args []string) (int, bool) {
	if len(args) == 1 && args[0] == "list" {
		return 0, false
	}
	if len(args) == 0 {
		return r.ReportUsage(&cli.UsageError{Message: "usage: verdict pack list [--json]"}), true
	}
	return r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
		"unknown pack subcommand '%s' (want list)", args[0])}), true
}

// settingsPath resolves the operator settings file from the injected env.
func settingsPath(req Request) string {
	if req.Runtime == nil {
		return ""
	}
	return config.SettingsPath(req.Env["HOME"], req.Env["XDG_CONFIG_HOME"])
}
