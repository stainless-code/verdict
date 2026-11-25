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

// nameWorktree names a working-tree bundle's content — the index paths with
// their working-tree bytes, the same tree verify snapshots — once, before
// anything else is read. The diff, the file list, and the stats are then all
// read from that immutable tree (contentRefs), so the bundle can never
// describe content other than the tree it records, however the working tree
// changes during the capture. Naming is best effort: content git cannot name
// leaves the bundle on the live working-tree diff with no content_tree,
// which costs only its VERIFIED claims, never the review.
func nameWorktree(ctx context.Context, r *session, mode *localMode) {
	if mode.kind != modeWorktree {
		return
	}
	tree, err := contentGit(r).Worktree(ctx)
	if err != nil {
		r.Warn(fmt.Sprintf("could not name the reviewed content (%v) — source.json records no content_tree, "+
			"so no VERIFIED claim can land on this bundle", err))
		return
	}
	mode.contentTree = tree
}

// contentRefs is the diff range a named working-tree bundle reads — its base
// against the named tree — or nil when it has none.
func (m *localMode) contentRefs() []string {
	if m.kind != modeWorktree || m.contentTree == "" {
		return nil
	}
	base := m.baseSHA
	if base == "" {
		base = gitEmptyTree
	}
	return []string{base, m.contentTree}
}

// captureContentTree names the content a committed local bundle reviews by
// git tree id, so record can grade a VERIFIED claim on exactly that content:
// the head commit's tree. A working-tree bundle was named before its diff
// (nameWorktree). A --staged bundle reviews the index, which verify never
// runs, so it records no tree rather than one no verify run could ever match.
func captureContentTree(ctx context.Context, r *session, mode *localMode) {
	switch mode.kind {
	case modeWorktree:
		return
	case modeStaged:
		r.Warn("a --staged bundle reviews the index, which verdict verify does not run — source.json records " +
			"no content_tree, so no VERIFIED claim can land on this bundle; fetch-local without --staged to verify fixes")
		return
	}
	tree, err := contentGit(r).Commit(ctx, mode.headSHA)
	if err != nil {
		r.Warn(fmt.Sprintf("could not name the reviewed content (%v) — source.json records no content_tree, "+
			"so no VERIFIED claim can land on this bundle", err))
		return
	}
	mode.contentTree = tree
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
