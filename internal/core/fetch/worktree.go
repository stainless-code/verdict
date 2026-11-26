package fetchcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// isolation is one fetch-local --worktree checkout: a temporary detached
// git worktree holding the review target plus the explicit commit pair the
// bundle reads diff against. The session carries it (nil when the flag is
// absent, so default runs never prefix argv); localDiff and localFileStats
// read through it, everything else stays on the live tree.
type isolation struct {
	dir  string
	base string
	head string
}

// setupIsolation materializes the review target into a temporary detached
// worktree: the mode's head commit for stash/range/branch, a synthesized
// commit-tree commit for staged, a stash-create commit for the default
// dirty tree. It reports created=false on a clean tree (stash create
// prints nothing), so the run proceeds classically with no checkout and
// no cleanup. A worktree that cannot be created refuses typed
// worktree-failed and writes nothing.
// stage is the directory the checkout is reserved under, empty for the
// repository's own state directory; a preview passes its scratch dir so an
// isolated checkout never creates `.verdict/state` inside a repository it
// promised not to write.
func setupIsolation(ctx context.Context, s *session, slug string, mode *localMode, stage string) (*isolation, bool, error) {
	target, skip, ref := isolationTarget(ctx, s, mode)
	if ref != nil {
		return nil, false, ref
	}
	if skip {
		return nil, false, nil
	}
	dir, ref := resolveIsolationDir(s.repoRoot, slug, stage)
	if ref != nil {
		return nil, false, ref
	}
	if err := addIsolation(ctx, s, dir, target); err != nil {
		return nil, false, err
	}
	return &isolation{dir: dir, base: isolationBase(mode), head: target}, true, nil
}

// addIsolation checks target out at dir with every filter driver off
// ([checkoutOptions]). A missing git passes through; any other failure
// scrubs the half-made checkout and refuses typed worktree-failed.
func addIsolation(ctx context.Context, s *session, dir, target string) error {
	opts, err := checkoutOptions(ctx, s)
	if mt, ok := errors.AsType[*core.MissingToolError](err); ok {
		return mt
	}
	if err != nil {
		return &core.RefusalError{
			Code:    codeWorktreeFailed,
			Message: core.Msgf("could not read the filter configuration an isolated worktree must switch off: %v", err),
		}
	}
	res, err := s.run(ctx, slices.Concat(opts, []string{gitCmdWorktree, gitWorktreeAdd, flagDetach, dir, target})...)
	if mt, ok := errors.AsType[*core.MissingToolError](err); ok {
		return mt
	}
	if err != nil {
		scrubIsolation(ctx, s, dir)
		return &core.RefusalError{
			Code: codeWorktreeFailed,
			Message: core.Msgf("could not check out isolated worktree at %s: %s",
				readcommon.ShortSHA(strings.TrimSpace(target)), headBytes(res.Stderr, numExcerpt)),
		}
	}
	return nil
}

// checkoutOptions switches off, for the isolated checkout, every filter
// driver the repository's configuration names: the checkout writes reviewed
// content, whose attributes could otherwise route a file through any smudge
// or process filter the operator configured — git-lfs among them, fetching
// from a URL the content's .lfsconfig picks. Files land as their blob bytes
// (an LFS file as its pointer). git exits 1 when no driver is configured.
func checkoutOptions(ctx context.Context, s *session) ([]string, error) {
	res, err := s.run(ctx, gitcmd.ListFilters...)
	if err != nil && (res.ExitCode != 1 || res.Stdout != "") {
		return nil, err
	}
	return gitcmd.FilterOptions(res.Stdout), nil
}

// isolationBase is the explicit diff base for the checkout reads: the
// mode's base commit, or the empty tree on an unborn HEAD where the staged
// tree has no parent to diff against (matching the classic --cached read).
func isolationBase(mode *localMode) string {
	if mode.baseSHA == "" {
		return gitEmptyTree
	}
	return mode.baseSHA
}

// scrubIsolation best-effort removes a half-created checkout after a
// failed worktree add: the remove runs detached from any canceled run
// context, and a scrub that itself fails warns instead of hiding the
// refusal that carries the cause.
func scrubIsolation(ctx context.Context, s *session, dir string) {
	ctx = context.WithoutCancel(ctx)
	if _, err := s.run(ctx, gitCmdWorktree, gitWorktreeRemove, flagForce, dir); err != nil {
		s.Warn(fmt.Sprintf("isolated worktree %s was not scrubbed after a failed checkout (%v)", dir, err))
	}
	_ = os.RemoveAll(dir)
}

// isolationTarget resolves the commit the temporary worktree checks out:
// the mode's head commit where one already exists, a commit-tree commit
// binding the staged tree to HEAD where it does not (parentless on an
// unborn HEAD), and a stash-create commit snapshotting the dirty tree
// otherwise. A clean tree reports skip, so the run proceeds classically.
func isolationTarget(ctx context.Context, s *session, mode *localMode) (string, bool, error) {
	switch mode.kind {
	case modeStash, modeRange, modeBranch:
		return committedTarget(mode)
	case modeStaged:
		return stagedTarget(ctx, s, mode)
	default:
		return dirtyTarget(ctx, s)
	}
}

func committedTarget(mode *localMode) (string, bool, error) {
	if mode.headSHA == "" {
		return "", false, &core.RefusalError{
			Code:    codeWorktreeFailed,
			Message: "could not check out isolated worktree: the review target has no head commit",
		}
	}
	return mode.headSHA, false, nil
}

func stagedTarget(ctx context.Context, s *session, mode *localMode) (string, bool, error) {
	args := []string{gitCmdCommitTree, mode.headSHA}
	if mode.baseSHA != "" {
		args = append(args, "-p", mode.baseSHA)
	}
	args = append(args, "-m", msgWorktreeCommit)
	head, err := s.gitLine(ctx, args...)
	if err != nil {
		if mt, ok := errors.AsType[*core.MissingToolError](err); ok {
			return "", false, mt
		}
		return "", false, &core.RefusalError{
			Code:    codeWorktreeFailed,
			Message: core.Msgf("could not materialize the staged tree for an isolated worktree: %s", head),
		}
	}
	if head == "" {
		return "", false, &core.RefusalError{
			Code:    codeWorktreeFailed,
			Message: "could not materialize the staged tree for an isolated worktree: empty commit id",
		}
	}
	return head, false, nil
}

// dirtyTarget snapshots the dirty tree via a read-only stash create. An
// empty snapshot (clean tree) reports skip: nothing to isolate, so the
// run proceeds classically with no checkout and no cleanup.
func dirtyTarget(ctx context.Context, s *session) (string, bool, error) {
	target := s.gitLineOrEmpty(ctx, gitCmdStash, gitStashCreate)
	if target == "" {
		return "", true, nil
	}
	return target, false, nil
}

// resolveIsolationDir picks the worktree path under the shared bundle home
// (suffixing on collision, so two concurrent fetches never share a
// checkout). The path is deterministic per slug, which keeps the
// scripted-argv contract exact; it is slash-form on every OS, so git argv,
// human lines, and goldens never carry a native separator. git itself
// creates the directory, so this only probes. The worktree add is the
// atomic claim: a lost race refuses typed with git's own already-exists
// excerpt instead of sharing a checkout. The joined name needs no bundle-name
// guard: slug is localSlug's output, `[a-z0-9-]` and never empty, so it is one
// path element by construction.
func resolveIsolationDir(root, slug, stage string) (string, error) {
	state := stage
	if state == "" {
		state = readcommon.StateDir(root)
	}
	if err := os.MkdirAll(state, dirPerm); err != nil {
		return "", &core.RefusalError{
			Code:    codeWorktreeFailed,
			Message: core.Msgf("could not create %s for an isolated worktree: %v", state, err),
		}
	}
	dir := filepath.ToSlash(filepath.Join(state, ".worktree-"+slug))
	for i := 2; ; i++ {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return dir, nil
		}
		if i > numWorktreeSuffixes {
			return "", &core.RefusalError{
				Code:    codeWorktreeFailed,
				Message: core.Msgf("could not reserve an isolated worktree path under %s (too many stale checkouts)", state),
			}
		}
		dir = fmt.Sprintf("%s/.worktree-%s-%d", state, slug, i)
	}
}

// cleanup removes the temporary worktree after the bundle build, on
// success and on every failure path (the deferred call derives a fresh
// context from the run's, so a canceled fetch still removes its checkout).
// A removal that fails warns with the manual recovery instead of failing
// the run: the bundle already built, and the leftover is one inert,
// detached checkout.
func (iso *isolation) cleanup(ctx context.Context, s *session) {
	if iso == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	if _, err := s.run(ctx, gitCmdWorktree, gitWorktreeRemove, flagForce, iso.dir); err != nil {
		s.Warn(fmt.Sprintf("isolated worktree %s was not removed (%v) — remove it with: git worktree remove --force %s",
			iso.dir, err, iso.dir))
		return
	}
	_ = os.RemoveAll(iso.dir)
}

// git runs git against the isolated checkout when one is active and against
// the live tree otherwise. Only the bundle's diff and file-list reads go
// through it; mode resolution, the untracked-files advisory, and teardown
// stay live by calling run directly.
func (s *session) git(ctx context.Context, args ...string) (core.Result, error) {
	if s.iso != nil {
		return s.runIn(ctx, s.iso.dir, args...)
	}
	return s.run(ctx, args...)
}
