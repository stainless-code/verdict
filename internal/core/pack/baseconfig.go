package pack

import (
	"context"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gitcmd"
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
	cfg, source, ref := baseConfig(ctx, cmdr, root)
	if ref != nil {
		return Toggles{}, "", ref
	}
	return TogglesFromConfig(cfg), source, nil
}

// baseConfig reads and parses the trusted-base configuration once, shared by
// every base-owned pack read. A missing base reads nil config with
// [ConfigSourceNone]; a present but malformed document refuses.
func baseConfig(ctx context.Context, cmdr core.Commander, root string) (*core.Config, string, *core.RefusalError) {
	if cmdr == nil {
		return nil, ConfigSourceNone, nil
	}
	sha, ref := baseCommit(ctx, cmdr, root)
	if ref != nil {
		return nil, "", ref
	}
	if sha == "" {
		return nil, ConfigSourceNone, nil
	}
	base, ref := config.ReadBase(ctx, cmdr, "", root, sha)
	if ref != nil {
		return nil, "", ref
	}
	cfg, ref := config.ParseBase(base)
	if ref != nil {
		return nil, "", ref
	}
	return cfg, BaseConfigPrefix + shortHash(sha), nil
}

// OrgPin is the base-owned central-pack pin: the repository, the immutable
// ref (a 40-hex commit SHA or a semver tag), and the pack root inside it.
// It is read from the trusted base exactly like [Toggles], so a PR branch —
// or personal settings — can never change or drop it.
type OrgPin struct {
	Repo string
	Ref  string
	Path string
}

// OrgPinFromConfig reads the org pin out of a parsed configuration, or nil
// when the base declares none.
func OrgPinFromConfig(cfg *core.Config) *OrgPin {
	if cfg == nil || cfg.Packs == nil || cfg.Packs.OrgPack == nil {
		return nil
	}
	pin := cfg.Packs.OrgPack
	path := pin.Path
	if path == "" {
		path = rootSelf
	}
	return &OrgPin{Repo: pin.Repo, Ref: pin.Ref, Path: path}
}

// BaseOrgPin reads the org pin `.verdict/config.json` declares at the
// checkout's trusted base, never the working tree and never a branch commit.
// The outcome is carried by the value, not the source label: a resolved base
// whose config declares no org pin returns `(nil, "base@<sha>", nil)`,
// whereas a checkout with no resolvable base returns
// `(nil, ConfigSourceNone, nil)`. Callers must inspect the returned
// [*OrgPin] pointer to know whether a pin was declared — the source label
// names the commit that was read, never whether it carried a pin.
func BaseOrgPin(ctx context.Context, cmdr core.Commander, root string) (*OrgPin, string, *core.RefusalError) {
	cfg, source, ref := baseConfig(ctx, cmdr, root)
	if ref != nil {
		return nil, "", ref
	}
	return OrgPinFromConfig(cfg), source, nil
}

// baseCommit resolves the trusted base commit: the merge base of HEAD and the
// first default-branch candidate that exists. An empty result means the
// checkout has no resolvable default branch, which reads as no base
// configuration rather than a refusal — a repository with no base branch has
// no head-versus-base distinction to protect. A candidate that does resolve
// and then has no merge base with HEAD refuses instead: the base branch is
// there, so a missing merge base is a broken read (a shallow or partial
// clone), and reading it as "nothing is disabled" would let a clone that
// cannot prove its base skip every base-owned pin. A canceled context, or a
// git runner that cannot execute at all, refuses instead too: an
// interrupted or impossible read must never read as "nothing is disabled".
func baseCommit(ctx context.Context, cmdr core.Commander, root string) (string, *core.RefusalError) {
	for _, ref := range baseRefCandidates {
		if ref := baseRefusal(ctx); ref != nil {
			return "", ref
		}
		ok, refErr := resolves(ctx, cmdr, root, ref)
		if refErr != nil {
			return "", refErr
		}
		if !ok {
			continue
		}
		return baseAgainst(ctx, cmdr, root, ref)
	}
	return "", baseRefusal(ctx)
}

// baseAgainst resolves the merge base with one default-branch candidate that
// exists. A candidate with no merge base with HEAD refuses: the base branch is
// there, so the read is broken, not absent.
func baseAgainst(ctx context.Context, cmdr core.Commander, root, candidate string) (string, *core.RefusalError) {
	sha, ok, ref := gitLine(ctx, cmdr, root, "merge-base", gitHeadRef, candidate)
	if ok {
		return sha, nil
	}
	if ref != nil {
		return "", ref
	}
	if ref := baseRefusal(ctx); ref != nil {
		return "", ref
	}
	return "", baseUnresolved(candidate)
}

// baseUnresolved refuses a base branch that resolves but has no merge base
// with HEAD, naming the branch and the read that failed without echoing git's
// own text.
func baseUnresolved(ref string) *core.RefusalError {
	return &core.RefusalError{Code: CodePackGitFailed, Message: fmt.Sprintf(
		"git merge-base failed: %q resolves but shares no merge base with %s — "+
			"check that the checkout is a full clone of its default branch, then re-run",
		ref, gitHeadRef)}
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
// checkout. A runner failure refuses: the caller cannot prove the ref is
// absent when git cannot run at all.
func resolves(ctx context.Context, cmdr core.Commander, root, ref string) (bool, *core.RefusalError) {
	_, ok, refErr := gitLine(ctx, cmdr, root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if refErr != nil {
		return false, refErr
	}
	return ok, nil
}

// gitLine runs one read-only git command and returns its trimmed single line.
// A non-zero exit or an empty answer reports false with no refusal, so the
// caller skips the candidate as absent; a canceled context reports the
// cancellation refusal, so an interrupted read never degrades either; and a
// runner error — a missing git binary or any exec failure reported as an
// error rather than a completed process — instead refuses with
// [CodePackGitFailed], so an unrunnable Git never degrades to "no base".
func gitLine(ctx context.Context, cmdr core.Commander, root string, args ...string) (string, bool, *core.RefusalError) {
	if ctx.Err() != nil {
		return "", false, baseRefusal(ctx)
	}
	argv := gitcmd.Argv(root, args...)
	res, err := cmdr.Run(ctx, &core.Command{Argv: argv, Env: gitcmd.Env(nil), OutputCap: baseCommandCap})
	if err != nil {
		return "", false, baseGitFailed()
	}
	if res.ExitCode != 0 {
		return "", false, nil
	}
	line := strings.TrimSpace(res.Stdout)
	return line, line != "", nil
}

// baseGitFailed refuses a base read whose git runner could not execute at
// all: the checkout is unreadable, not base-less.
func baseGitFailed() *core.RefusalError {
	return &core.RefusalError{Code: CodePackGitFailed, Message: "git could not run — check that git works in this shell, then re-run"}
}
