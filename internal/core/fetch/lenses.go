package fetchcore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/pack"
)

// resolveLensPacks loads the built-in, org, and repo layers and merges them.
// The repo layer comes from the trusted-base controls snapshot, never the
// worktree: a PR cannot edit the pack that reviews it.
func resolveLensPacks(ctx2 *fetchRunCtx) *core.RefusalError {
	origins, warnings, ref := builtinPackOrigins(ctx2)
	if ref != nil {
		return ref
	}
	for _, warning := range warnings {
		ctx2.runner.Warn(warning)
	}
	org, ref := orgPackOrigins(ctx2)
	if ref != nil {
		return ref
	}
	repo, ref := repoPackOrigins(ctx2.runner.controls)
	if ref != nil {
		return ref
	}
	if ref := pack.CheckLock(ctx2.runner.controls); ref != nil {
		return ref
	}
	origins = append(origins, org...)
	origins = append(origins, repo...)
	set, ref := pack.Merge(origins)
	if ref != nil {
		return ref
	}
	ctx2.packs = set
	return nil
}

// builtinPackOrigins reads the embedded core and language packs through the
// injected skill FS and applies the trusted-base composition: a repository can
// turn a builtin pack, lens, or rule off from its base `.verdict/config.json`,
// while a head edit to that file never reaches this read.
func builtinPackOrigins(ctx2 *fetchRunCtx) ([]pack.Origin, []string, *core.RefusalError) {
	if ctx2.runner.packFS == nil {
		return nil, nil, nil
	}
	origins, ref := pack.BuiltinOrigins(ctx2.runner.packFS)
	if ref != nil {
		return nil, nil, ref
	}
	var cfg *core.Config
	if ctx2.runCfg != nil {
		cfg = ctx2.runCfg.repo
	}
	toggled := pack.ApplyToggles(origins, pack.TogglesFromConfig(cfg))
	return toggled.Origins, toggled.Warnings, nil
}

// orgPackOrigins reads the explicit org paths from personal settings.
func orgPackOrigins(ctx2 *fetchRunCtx) ([]pack.Origin, *core.RefusalError) {
	if ctx2.runCfg == nil || ctx2.runCfg.personal == nil {
		return nil, nil
	}
	root := ctx2.runCfg.root
	if root == "" {
		root = "."
	}
	out := make([]pack.Origin, 0, len(ctx2.runCfg.personal.Packs))
	for _, listed := range ctx2.runCfg.personal.Packs {
		origin, ref := orgPackOrigin(root, listed)
		if ref != nil {
			return nil, ref
		}
		out = append(out, origin)
	}
	return out, nil
}

// orgPackOrigin loads one explicitly listed org pack directory. An entry
// inside the repo layer's tree is refused: that tree is read from the base.
func orgPackOrigin(root, listed string) (pack.Origin, *core.RefusalError) {
	if ref := pack.CheckOrgEntry(root, listed); ref != nil {
		return pack.Origin{}, ref
	}
	full := listed
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, filepath.FromSlash(listed))
	}
	p, ref := pack.LoadDir(full)
	if ref != nil {
		return pack.Origin{}, ref
	}
	if p == nil {
		return pack.Origin{}, &core.RefusalError{
			Code:    pack.CodeBadPack,
			Message: fmt.Sprintf("org pack path %q carries no %s", listed, pack.ManifestFile),
		}
	}
	return pack.Origin{Layer: pack.LayerOrg, Source: "dir:" + filepath.ToSlash(listed), Pack: p}, nil
}

// repoPackOrigins reads every pack directory the controls snapshot carries.
func repoPackOrigins(snap *controls.Snapshot) ([]pack.Origin, *core.RefusalError) {
	if snap == nil {
		return nil, nil
	}
	names := snap.PackNames()
	out := make([]pack.Origin, 0, len(names))
	for _, name := range names {
		origin, ref := repoPackOrigin(snap, name)
		if ref != nil {
			return nil, ref
		}
		out = append(out, origin)
	}
	return out, nil
}

// repoPackOrigin loads one pack directory from the controls snapshot.
func repoPackOrigin(snap *controls.Snapshot, name string) (pack.Origin, *core.RefusalError) {
	p, ref := pack.LoadAt(snap.Dir, "packs/"+name)
	if ref != nil {
		return pack.Origin{}, ref
	}
	if p == nil {
		return pack.Origin{}, &core.RefusalError{
			Code:    pack.CodeBadPack,
			Message: fmt.Sprintf("%s/%s carries no %s", pack.RepoPacksDir, name, pack.ManifestFile),
		}
	}
	return pack.Origin{Layer: pack.LayerRepo, Source: pack.RepoPacksDir + "/" + name, Pack: p}, nil
}

// writeLensBodies writes lenses.txt: the bodies of the lenses offered for the
// bundle's changed files, delimited and labeled with their id and hash8. A
// curated language lens therefore reaches only a bundle whose diff can dispatch
// to it. A pack that cannot be written is advisory context, so the run warns
// and keeps the bundle.
func writeLensBodies(ctx2 *fetchRunCtx, extra string) {
	path := ctx2.dir + suffixLensesTxt
	if ctx2.packs == nil || len(ctx2.packs.Lenses) == 0 {
		_ = os.Remove(path)
		return
	}
	files := recallFiles(ctx2.dir, withoutExcluded(ctx2, extra))
	note := ctx2.packs.NoteFor(files)
	if note == "" {
		_ = os.Remove(path)
		return
	}
	if err := atomic.WriteFile(path, []byte(note)); err != nil {
		ctx2.runner.Warn(fmt.Sprintf("lens pack skipped: could not write %s (%v)", path, err))
	}
}

// lensPlanFields appends the pack identities and the group's offered lens
// ids with their eight-hex body hashes, sorted by id. A layered set tags each
// lens with its pack so an override is visible on the plan line.
func lensPlanFields(set *pack.Set, group *config.PlanGroup) string {
	if set == nil || len(set.Packs) == 0 {
		return ""
	}
	offered := set.Offered(group.Files)
	ids := make([]string, len(offered))
	for i := range offered {
		ids[i] = lensField(set, &offered[i])
	}
	return fmt.Sprintf(" lens_pack=%s lenses=[%s]",
		strings.Join(packIdentities(set), listSep), strings.Join(ids, listSep))
}

// lensField renders one offered lens: a single pack keeps the historical
// id:hash form; a layered set adds the winning pack's full name, so the tag
// names the layer that supplied the body.
func lensField(set *pack.Set, lens *pack.Lens) string {
	if len(set.Packs) == 1 {
		return lens.ID + ":" + lens.Hash8
	}
	return lens.ID + "@" + lens.Pack + ":" + lens.Hash8
}

// packIdentities renders the loaded packs in precedence order.
func packIdentities(set *pack.Set) []string {
	packs := make([]string, 0, len(set.Packs))
	for _, origin := range set.Packs {
		if origin.Pack != nil {
			packs = append(packs, origin.Pack.Name+"@"+origin.Pack.Version)
		}
	}
	return packs
}
