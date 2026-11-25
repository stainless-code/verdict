package fetchcore

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/stainless-code/verdict/internal/contenttree"
)

// captureContentTree names the content a local bundle reviews by git tree
// id, so record can grade a VERIFIED claim on exactly that content: the
// staged tree, the head commit's tree for a committed review, and the
// working tree's content — the same tree verify snapshots — otherwise.
// Naming is best effort: content git cannot index (an untracked nested
// repository with no commit) leaves source.json without a tree, which costs
// the bundle only its VERIFIED claims, never the review.
func captureContentTree(ctx context.Context, r *session, mode *localMode) {
	if mode.kind == modeStaged {
		mode.contentTree = mode.headSHA
		return
	}
	root := r.repoRoot
	if root == "" {
		root = "."
	}
	g := contenttree.Git{Cmdr: r.commander, Root: root, Env: envList(r.env)}
	var tree string
	var err error
	switch mode.kind {
	case modeStash, modeRange, modeBranch:
		tree, err = g.Commit(ctx, mode.headSHA)
	default:
		tree, err = g.Worktree(ctx)
	}
	if err != nil {
		r.Warn(fmt.Sprintf("could not name the reviewed content (%v) — source.json records no content_tree, "+
			"so no VERIFIED claim can land on this bundle", err))
		return
	}
	mode.contentTree = tree
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
