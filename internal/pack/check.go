package pack

import (
	"context"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/cli"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
)

// runCheck resolves one path through the same layers, trusted-base pack
// toggles, and globs a review uses, and reports the ordered result.
func runCheck(ctx context.Context, r *cli.Runner, read Request, path string) int {
	read.PackFS = skillfs.FS
	read.Commander = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}
	toggles, source, ref := packcore.BaseToggles(ctx, read.Commander, read.RepoRootPath())
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	pin, _, ref := packcore.BaseOrgPin(ctx, read.Commander, read.RepoRootPath())
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	resp, ref := packcore.RunCheck(ctx, &packcore.CheckRequest{
		ReadRequest:  read,
		SettingsPath: settingsPath(read),
		Toggles:      toggles,
		OrgPin:       pin,
		ConfigSource: source,
		Path:         path,
	})
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	if r.JSONMode {
		r.Emit(verbPack, "", "", resp.At, resp.Next, resp.Warnings, resp.Data.Wire())
		return cli.ExitOK
	}
	r.HumanLines(resp.Human)
	return cli.ExitOK
}
