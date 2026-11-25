package verifycore

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// snapshot is the content a command runs against: the checkout's tree id and
// a detached checkout of exactly that tree. The command runs there, never in
// the user's checkout, so editing, checking out, or restoring files there
// while it runs cannot change what the receipt says was tested.
type snapshot struct {
	tree string
	dir  string
	git  contenttree.Git
}

// takeSnapshot names root's content and checks it out under the bundle (in
// verdict's own state, so the snapshot is never part of the content). A
// checkout git cannot snapshot — not a repository, no git — runs in place and
// its receipt records no tree, so it cannot back a VERIFIED claim.
func (st *state) takeSnapshot(ctx context.Context, commander core.Commander, root, bundleDir string) *snapshot {
	g := contenttree.Git{Cmdr: commander, Root: root, Env: st.req.Environ}
	snap, err := checkoutSnapshot(ctx, g, bundleDir)
	if err != nil {
		st.warn(fmt.Sprintf("could not snapshot the content at %s (%v) — the command runs in place and %s "+
			"records no tree, so this run cannot back a VERIFIED claim", root, err, ReceiptFile))
		return nil
	}
	return snap
}

func checkoutSnapshot(ctx context.Context, g contenttree.Git, bundleDir string) (*snapshot, error) {
	tree, err := g.Worktree(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller names the root around git's reason
	}
	return checkoutTree(ctx, g, tree, bundleDir)
}

// checkoutTree checks tree out into a fresh directory under the bundle.
func checkoutTree(ctx context.Context, g contenttree.Git, tree, bundleDir string) (*snapshot, error) {
	dir, err := os.MkdirTemp(bundleDir, ".snapshot-")
	if err != nil {
		return nil, jq.Sealed("create the snapshot directory", err)
	}
	// The command and the sandbox name the snapshot by its physical path.
	if physical, ref := canonicalRoot(dir); ref == nil {
		dir = physical
	}
	if err := g.Checkout(ctx, tree, dir); err != nil {
		// A failed checkout can leave its directory or registration behind.
		//nolint:wrapcheck // the caller names the root around git's reasons
		return nil, errors.Join(err, g.RemoveCheckout(context.WithoutCancel(ctx), dir))
	}
	return &snapshot{git: g, tree: tree, dir: dir}, nil
}

// runIn returns where the command runs and the tree its receipt names: the
// snapshot, or root itself with no tree when git could not snapshot it.
func (s *snapshot) runIn(root string) (dir, tree string) {
	if s == nil {
		return root, ""
	}
	return s.dir, s.tree
}

// remove deletes the snapshot checkout and its registration, warning when
// any of it survives so a stranded directory is visible.
func (st *state) removeSnapshot(ctx context.Context, snap *snapshot) {
	if snap == nil {
		return
	}
	if err := snap.git.RemoveCheckout(context.WithoutCancel(ctx), snap.dir); err != nil {
		st.warn(fmt.Sprintf("snapshot cleanup: %s was not fully removed (%v); remove it by hand", snap.dir, err))
	}
}
