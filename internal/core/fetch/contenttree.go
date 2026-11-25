package fetchcore

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/stainless-code/verdict/internal/contenttree"
)

// modeWorktree is the kind of a bundle that reviews the working tree.
const modeWorktree = "worktree"

// contentBefore names the working tree's content before a working-tree
// bundle reads its diff (and before an isolated checkout snapshots it), so
// captureContentTree can tell whether the content moved while the bundle was
// captured. Other kinds name committed or staged content and need no bracket.
func contentBefore(ctx context.Context, r *session, mode *localMode) {
	if mode.kind != modeWorktree {
		return
	}
	mode.treeBefore, mode.treeErr = contentGit(r).Worktree(ctx)
}

// captureContentTree names the content a local bundle reviews by git tree
// id, so record can grade a VERIFIED claim on exactly that content: the head
// commit's tree for a committed review, and otherwise the working tree's
// content over the index paths — the same tree verify snapshots, and the
// paths the bundle's diff covers (untracked files are in neither). A --staged
// bundle reviews the index, which verify never runs, so it records no tree
// rather than one no verify run could ever match.
//
// A working-tree bundle reads its diff between two namings of the content
// (contentBefore and here). When they differ the content changed during the
// capture, so the diff may describe content the tree does not, and the
// bundle records no tree. Naming is best effort either way: content git
// cannot name costs the bundle only its VERIFIED claims, never the review.
func captureContentTree(ctx context.Context, r *session, mode *localMode) {
	if mode.kind == modeStaged {
		r.Warn("a --staged bundle reviews the index, which verdict verify does not run — source.json records " +
			"no content_tree, so no VERIFIED claim can land on this bundle; fetch-local without --staged to verify fixes")
		return
	}
	tree, err := committedTree(ctx, r, mode)
	if err == nil && mode.kind == modeWorktree {
		err = mode.treeErr
	}
	switch {
	case err != nil:
		r.Warn(fmt.Sprintf("could not name the reviewed content (%v) — source.json records no content_tree, "+
			"so no VERIFIED claim can land on this bundle", err))
	case mode.kind == modeWorktree && tree != mode.treeBefore:
		r.Warn("the working tree changed while the bundle was captured — source.json records no " +
			"content_tree, so no VERIFIED claim can land on this bundle; re-run fetch-local on a quiet tree")
	default:
		mode.contentTree = tree
	}
}

func committedTree(ctx context.Context, r *session, mode *localMode) (string, error) {
	switch mode.kind {
	case modeStash, modeRange, modeBranch:
		return contentGit(r).Commit(ctx, mode.headSHA) //nolint:wrapcheck // the warning names git's reason
	}
	return contentGit(r).Worktree(ctx) //nolint:wrapcheck // the warning names git's reason
}

func contentGit(r *session) contenttree.Git {
	root := r.repoRoot
	if root == "" {
		root = "."
	}
	return contenttree.Git{Cmdr: r.commander, Root: root, Env: envList(r.env)}
}

// envList is the injected environment as KEY=value entries, sorted so every
// spawn sees the same order.
func envList(env map[string]string) []string {
	if env == nil {
		return nil
	}
	out := make([]string, 0, len(env))
	for _, k := range slices.Sorted(maps.Keys(env)) {
		out = append(out, k+"="+env[k])
	}
	return out
}
