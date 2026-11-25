// Package contenttree names checkout content by git tree id and rebuilds it
// elsewhere. A verify receipt is bound to the tree the command ran on, and
// record and post compare that tree with the tree they grade, so "verified"
// means this exact content, whatever HEAD, the index, or the working tree
// say later. Every read goes through a throwaway index file: the user's
// index, refs, and working tree are never written.
package contenttree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// outputCap bounds a git read; a tree id or an error line is all it carries.
const outputCap = 64 << 10

// excluded is verdict's own runtime state, never reviewed content: bundle
// state, `verdict loop` round receipts, and the generated .gitignore. The tree
// keeps HEAD's version of these paths and ignores their working-tree changes,
// so a committed .verdict/.gitignore still matches the commit's tree.
var excluded = []string{
	config.VerdictDir + "/" + config.StateDirName,
	config.VerdictDir + "/loop",
	config.GitignorePath,
}

// ErrUnresolved marks a revision the repository does not hold.
var ErrUnresolved = jq.NewErr("revision not in the local repository")

// Git runs the reads against one checkout root through an injected commander.
// Env is the base child environment (nil inherits); the throwaway index is
// added to it.
type Git struct {
	Cmdr core.Commander
	Root string
	Env  []string
}

// Worktree returns the tree id of the checkout's content: HEAD's tree (none
// on an unborn branch) updated with every change `git add -A` sees — modified,
// deleted, and untracked files, .gitignore respected — minus verdict's state.
func (g Git) Worktree(ctx context.Context) (string, error) {
	index, cleanup, err := tempIndex()
	if err != nil {
		return "", err
	}
	defer cleanup()
	steps, err := g.stageSteps(ctx)
	if err != nil {
		return "", err
	}
	for _, args := range steps {
		if _, err := g.run(ctx, index, args...); err != nil {
			return "", err
		}
	}
	return g.line(ctx, index, "write-tree")
}

// stageSteps are the index writes that stage the checkout's content: start
// from HEAD's tree, add every change, then put verdict's paths back to
// HEAD's version (an exclude pathspec would fail on an ignored path such as
// .verdict/state). An unborn branch starts empty and drops verdict's paths.
func (g Git) stageSteps(ctx context.Context) ([][]string, error) {
	add := []string{"add", "-A", "--", "."}
	_, err := g.Commit(ctx, "HEAD")
	switch {
	case errors.Is(err, ErrUnresolved):
		return [][]string{add, append([]string{"rm", "-r", "-q", "--cached", "--ignore-unmatch", "--"}, excluded...)}, nil
	case err != nil:
		return nil, err
	}
	return [][]string{{"read-tree", "HEAD"}, add, append([]string{"reset", "-q", "HEAD", "--"}, excluded...)}, nil
}

// Commit returns the tree id of rev, or ErrUnresolved when the repository
// does not hold it.
func (g Git) Commit(ctx context.Context, rev string) (string, error) {
	res, err := g.exec(ctx, nil, "rev-parse", "--verify", "-q", rev+"^{tree}")
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", ErrUnresolved
	}
	return strings.TrimSpace(res.Stdout), nil
}

// identity is the fixed author of a snapshot commit, so making one never
// depends on (or prompts for) the user's git identity.
var identity = []string{
	"GIT_AUTHOR_NAME=verdict", "GIT_AUTHOR_EMAIL=verdict@localhost",
	"GIT_COMMITTER_NAME=verdict", "GIT_COMMITTER_EMAIL=verdict@localhost",
}

// Checkout materializes tree at dest (absent or empty) as a detached linked
// worktree of an unreferenced snapshot commit: a real checkout git commands
// work in, holding exactly that content while the user's checkout moves. No
// branch or ref is created; RemoveCheckout undoes it.
func (g Git) Checkout(ctx context.Context, tree, dest string) error {
	res, err := g.runEnv(ctx, identity, "commit-tree", tree, "-m", "verdict verify snapshot")
	if err != nil {
		return err
	}
	commit := strings.TrimSpace(res.Stdout)
	_, err = g.runEnv(ctx, nil, "worktree", "add", "--detach", "--quiet", dest, commit)
	return err
}

// RemoveCheckout removes a Checkout's worktree and its registration, even
// when the command left files in it.
func (g Git) RemoveCheckout(ctx context.Context, dest string) error {
	_, err := g.runEnv(ctx, nil, "worktree", "remove", "--force", dest)
	if rerr := os.RemoveAll(dest); err == nil && rerr != nil {
		err = jq.Sealed("remove "+dest, rerr)
	}
	if _, perr := g.runEnv(ctx, nil, "worktree", "prune"); err == nil {
		err = perr
	}
	return err
}

// tempIndex is a fresh index path in a private directory, and its cleanup.
func tempIndex() (string, func(), error) {
	dir, err := os.MkdirTemp("", "verdict-index-")
	if err != nil {
		return "", nil, jq.Sealed("create a throwaway index", err)
	}
	return filepath.Join(dir, "index"), func() { _ = os.RemoveAll(dir) }, nil
}

func (g Git) line(ctx context.Context, index string, args ...string) (string, error) {
	res, err := g.run(ctx, index, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

// run is exec that turns a nonzero exit into an error naming git's reason.
func (g Git) run(ctx context.Context, index string, args ...string) (core.Result, error) {
	var extra []string
	if index != "" {
		extra = []string{"GIT_INDEX_FILE=" + index}
	}
	return g.runEnv(ctx, extra, args...)
}

// runEnv runs git with extra added to the base environment and turns a
// nonzero exit into an error naming git's reason.
func (g Git) runEnv(ctx context.Context, extra []string, args ...string) (core.Result, error) {
	res, err := g.exec(ctx, extra, args...)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		reason, _, _ := strings.Cut(strings.TrimSpace(res.Stderr), "\n")
		return res, jq.NewErrf("git %s exited %d: %s", args[0], res.ExitCode, reason)
	}
	return res, nil
}

func (g Git) exec(ctx context.Context, extra []string, args ...string) (core.Result, error) {
	cmd := &core.Command{Argv: append([]string{"git", "-C", g.Root}, args...), Env: g.Env, OutputCap: outputCap}
	if len(extra) > 0 {
		cmd.Env = append(slices.Clone(g.Env), extra...)
	}
	res, err := g.Cmdr.Run(ctx, cmd)
	if err != nil {
		return res, jq.Sealed("git "+args[0], err)
	}
	return res, nil
}
