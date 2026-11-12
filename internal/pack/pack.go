// Package pack is the pack verb's CLI adapter: argv parsing, one call into
// the pack service, and envelope rendering. Domain logic lives in
// internal/core/pack.
package pack

import (
	"context"
	"fmt"
	"strings"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/packtest"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// verbPack is the verb name every envelope carries.
const verbPack = "pack"

// Request wires a pack run.
type Request = readcommon.Request

const (
	subList         = "list"
	subAdd          = "add"
	subTest         = "test"
	testDirArgCount = 3
)

const packUsage = "usage: verdict pack <list|add|test> [--json]\n" +
	"       verdict pack add <dir|git-url>[@ref] [--json]\n" +
	"       verdict pack test <name> [--json]\n" +
	"       verdict pack test --dir <path> [--json]\n" +
	"       git sources: https://host/path (no userinfo), ssh://user@host/path, or user@host:path"

// Run executes a pack command and renders its result.
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
	if inv.sub == subList {
		return runList(ctx, r, read)
	}
	if inv.sub == subTest {
		return runTest(ctx, r, read, inv.name, inv.dir)
	}
	return runAdd(ctx, r, read, inv.source)
}

// invocation is one parsed pack command line.
type invocation struct {
	sub    string
	source string
	name   string
	dir    string
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
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	for _, line := range resp.Human {
		fmt.Fprintln(r.Human, line)
	}
	return cli.ExitOK
}

func checkArgs(r *cli.Runner, args []string) (invocation, int, bool) {
	if len(args) == 0 {
		return packUsageError(r)
	}
	switch args[0] {
	case subList:
		return checkListArgs(r, args)
	case subAdd:
		return checkAddArgs(r, args)
	case subTest:
		return checkTestArgs(r, args)
	default:
		return invocation{}, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
			"unknown pack subcommand '%s' (want list, add, or test)", args[0])}), true
	}
}

func packUsageError(r *cli.Runner) (invocation, int, bool) {
	return invocation{}, r.ReportUsage(&cli.UsageError{Message: packUsage}), true
}

func checkListArgs(r *cli.Runner, args []string) (invocation, int, bool) {
	if len(args) != 1 {
		return packUsageError(r)
	}
	return invocation{sub: subList}, 0, false
}

func checkAddArgs(r *cli.Runner, args []string) (invocation, int, bool) {
	if len(args) != 2 || args[1] == "" {
		return packUsageError(r)
	}
	return invocation{sub: subAdd, source: args[1]}, 0, false
}

func checkTestArgs(r *cli.Runner, args []string) (invocation, int, bool) {
	if len(args) == 2 && args[1] != "" && !strings.HasPrefix(args[1], "-") {
		return invocation{sub: subTest, name: args[1]}, 0, false
	}
	if len(args) == testDirArgCount && args[1] == "--dir" && args[2] != "" {
		return invocation{sub: subTest, dir: args[2]}, 0, false
	}
	return packUsageError(r)
}

func runTest(ctx context.Context, r *cli.Runner, read Request, name, dir string) int {
	p, set, warnings, ref := resolveTestPack(ctx, read, name, dir)
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	for _, warning := range warnings {
		r.Warn(warning)
	}
	resp, ref := packtest.Run(ctx, p, set, read.TimeUTC())
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	renderTestResponse(r, resp)
	if resp.Data.Summary.Fail > 0 || resp.Data.Summary.Skip > 0 {
		return cli.ExitRefused
	}
	return cli.ExitOK
}

func resolveTestPack(ctx context.Context, read Request, name, dir string) (*packcore.Pack, *packcore.Set, []string, *core.RefusalError) {
	read.PackFS = skillfs.FS
	if dir != "" {
		return resolveDirectoryPack(dir)
	}
	return resolveInstalledPack(ctx, read, name)
}

func resolveDirectoryPack(dir string) (*packcore.Pack, *packcore.Set, []string, *core.RefusalError) {
	p, ref := packcore.LoadDir(dir)
	if ref != nil {
		return nil, nil, nil, ref
	}
	if p == nil {
		return nil, nil, nil, &core.RefusalError{
			Code: packcore.CodeBadPack,
			Message: fmt.Sprintf("pack directory %q carries no %s", dir,
				packcore.ManifestFile),
		}
	}
	set, ref := packcore.Merge([]packcore.Origin{{Layer: packcore.LayerBuiltin, Source: "dir:" + dir, Pack: p}})
	return p, set, nil, ref
}

func resolveInstalledPack(ctx context.Context, read Request, name string) (*packcore.Pack, *packcore.Set, []string, *core.RefusalError) {
	set, warnings, ref := packcore.Resolve(ctx, &packcore.Request{
		ReadRequest: read, SettingsPath: settingsPath(read),
	})
	if ref != nil {
		return nil, nil, nil, ref
	}
	var p *packcore.Pack
	for i := range set.Packs {
		origin := &set.Packs[i]
		if origin.Pack != nil && origin.Pack.Name == name {
			p = origin.Pack
		}
	}
	if p == nil {
		return nil, nil, nil, &core.RefusalError{
			Code:    packcore.CodeBadPack,
			Message: fmt.Sprintf("pack %q is not installed", name),
		}
	}
	return p, set, warnings, nil
}

func renderTestResponse(r *cli.Runner, resp *packtest.TestResponse) {
	if r.JSONMode {
		env := core.RenderEnvelope(verbPack, "", "", resp.At, resp.Next, r.Warnings, core.Wire(resp.Data), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	} else {
		for _, line := range resp.Human {
			fmt.Fprintln(r.Human, line)
		}
	}
}

// settingsPath resolves the operator settings file from the injected env.
func settingsPath(req Request) string {
	if req.Runtime == nil {
		return ""
	}
	return config.SettingsPath(req.Env["HOME"], req.Env["XDG_CONFIG_HOME"])
}
