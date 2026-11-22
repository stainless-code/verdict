package pack

import (
	"context"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// BaseConfigPrefix labels a check row whose toggles came from the trusted base
// configuration.
const BaseConfigPrefix = "base@"

// gitHeadRef is the revision the default-branch merge base is measured
// against; the checked-out commit is the only head a local read has.
const gitHeadRef = "HEAD"

// baseRefCandidates is the ordered default-branch spelling search: the
// remote's HEAD first, then the names a default branch takes on both sides of
// a clone. A candidate that does not resolve is skipped, so a clone with no
// remote still finds its local default branch.
//
// The branch's own upstream (`@{upstream}`) is deliberately absent: it names
// the tracking ref of whatever branch is checked out — `origin/feature` for a
// pushed PR branch — so merging against it would return the PR head and let a
// PR disable the built-in packs that review it.
var baseRefCandidates = []string{
	"origin/HEAD", "origin/main", "origin/master", "main", "master",
}

// baseCommandCap bounds one git read's captured output: every command here
// answers with a single line, and a bounded capture refuses a repository that
// streams instead.
const baseCommandCap = 64 * 1024

// TogglesFromConfig reads the built-in pack composition out of a parsed
// configuration. A configuration with no `packs` section leaves every built-in
// pack on.
func TogglesFromConfig(cfg *core.Config) Toggles {
	if cfg == nil || cfg.Packs == nil {
		return Toggles{}
	}
	return Toggles{
		MergeBuiltin:  cfg.Packs.MergeBuiltin,
		DisablePacks:  cfg.Packs.DisablePacks,
		DisableLenses: cfg.Packs.DisableLenses,
		DisableRules:  cfg.Packs.DisableRules,
	}
}

// BaseToggles reads the pack composition `.verdict/config.json` declares at
// the checkout's trusted base: the merge base of HEAD and the default branch,
// never the working tree and never a branch commit. A branch therefore cannot
// disable the built-in packs that review it. The source label names the commit
// that was read (`base@<12 hex>`; an absent document at that commit turns
// nothing off), or [ConfigSourceNone] when no base could be resolved.
func BaseToggles(ctx context.Context, cmdr core.Commander, root string) (Toggles, string, *core.RefusalError) {
	if cmdr == nil {
		return Toggles{}, ConfigSourceNone, nil
	}
	sha, ref := baseCommit(ctx, cmdr, root)
	if ref != nil {
		return Toggles{}, "", ref
	}
	if sha == "" {
		return Toggles{}, ConfigSourceNone, nil
	}
	base, ref := config.ReadBase(ctx, cmdr, "", root, sha)
	if ref != nil {
		return Toggles{}, "", ref
	}
	cfg, ref := config.ParseBase(base)
	if ref != nil {
		return Toggles{}, "", ref
	}
	return TogglesFromConfig(cfg), BaseConfigPrefix + shortHash(sha), nil
}

// baseCommit resolves the trusted base commit: the merge base of HEAD and the
// first default-branch candidate that exists. An empty result means the
// checkout has no resolvable default branch, which reads as no base
// configuration rather than a refusal — a repository with no base branch has
// no head-versus-base distinction to protect. A canceled context refuses
// instead: an interrupted read must never read as "nothing is disabled".
func baseCommit(ctx context.Context, cmdr core.Commander, root string) (string, *core.RefusalError) {
	for _, ref := range baseRefCandidates {
		if ref := baseRefusal(ctx); ref != nil {
			return "", ref
		}
		if !resolves(ctx, cmdr, root, ref) {
			continue
		}
		sha, ok := gitLine(ctx, cmdr, root, "merge-base", gitHeadRef, ref)
		if ok {
			return sha, nil
		}
	}
	return "", baseRefusal(ctx)
}

// baseRefusal reports whether the base read was canceled, so the caller refuses
// rather than reporting a checkout with no default branch.
func baseRefusal(ctx context.Context) *core.RefusalError {
	if ctx.Err() == nil {
		return nil
	}
	return &core.RefusalError{
		Code:    canceledCode,
		Message: "canceled before the trusted-base pack configuration was read",
	}
}

// resolves reports whether one revision name peels to a commit in this
// checkout.
func resolves(ctx context.Context, cmdr core.Commander, root, ref string) bool {
	_, ok := gitLine(ctx, cmdr, root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return ok
}

// gitLine runs one read-only git command and returns its trimmed single line.
// Any failure — a canceled context, a missing git, a non-zero exit, an empty
// answer — reports false, so the caller decides between degrading and
// refusing.
func gitLine(ctx context.Context, cmdr core.Commander, root string, args ...string) (string, bool) {
	if ctx.Err() != nil {
		return "", false
	}
	argv := append([]string{"git"}, args...)
	res, err := cmdr.Run(ctx, &core.Command{Dir: root, Argv: argv, OutputCap: baseCommandCap})
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	line := strings.TrimSpace(res.Stdout)
	return line, line != ""
}
