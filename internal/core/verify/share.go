package verifycore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// sharedDir is one verify.share entry resolved against the checkout: the
// configured slash path, which is where the snapshot exposes it, and the
// physical directory in the checkout it exposes.
type sharedDir struct {
	rel  string
	real string
}

// sharedDirMode is the permission of a directory verify creates in the
// snapshot to hold a share: a bind mount point or a link's parent.
const sharedDirMode = 0o755

// sharedRels lists the paths a run shared, sorted, for its receipt.
func sharedRels(shared []sharedDir) []string {
	if len(shared) == 0 {
		return nil
	}
	rels := make([]string, 0, len(shared))
	for _, s := range shared {
		rels = append(rels, s.rel)
	}
	slices.Sort(rels)
	return rels
}

// resolveShares checks every configured share against the checkout before a
// snapshot exists. A directory that is absent is skipped with a warning, so
// a fresh clone that has not installed yet still verifies cleanly. Anything
// else that is not an ignored, untracked directory physically inside the
// checkout refuses: a shared path that held tracked content would let the
// shared bytes shadow the content the receipt names, and one that resolves
// outside the checkout would expose the host to the command.
func (st *state) resolveShares(ctx context.Context, g contenttree.Git) ([]sharedDir, *core.RefusalError) {
	if len(st.share) == 0 {
		return nil, nil
	}
	root, ref := canonicalRoot(g.Root)
	if ref != nil {
		return nil, ref
	}
	rootID, err := os.Stat(root)
	if err != nil {
		return nil, failed(err)
	}
	g.Root = root
	var shared []sharedDir
	for _, rel := range st.share {
		dir, skip, ref := resolveShare(ctx, g, rootID, rel)
		switch {
		case ref != nil:
			return nil, ref
		case skip:
			st.warn(fmt.Sprintf("verify.share: %s is not in the checkout, so the command runs without it "+
				"(install it, or remove the entry from %s)", rel, config.ConfigPath))
		default:
			shared = append(shared, dir)
		}
	}
	slices.SortFunc(shared, func(a, b sharedDir) int { return strings.Compare(a.rel, b.rel) })
	return shared, nil
}

// resolveShare resolves one entry: skip is true when it does not exist.
func resolveShare(ctx context.Context, g contenttree.Git, rootID os.FileInfo, rel string) (sharedDir, bool, *core.RefusalError) {
	target, err := filepath.EvalSymlinks(filepath.Join(g.Root, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return sharedDir{}, true, nil
	}
	if err != nil {
		return sharedDir{}, false, shareRefusal(rel, fmt.Sprintf("cannot be resolved (%v)", err))
	}
	inRoot, ref := shareInRoot(g.Root, rootID, rel, target)
	if ref != nil {
		return sharedDir{}, false, ref
	}
	for _, path := range uniquePaths(rel, filepath.ToSlash(inRoot)) {
		if ref := shareOutsideContent(ctx, g, rel, path); ref != nil {
			return sharedDir{}, false, ref
		}
	}
	return sharedDir{rel: rel, real: target}, false, nil
}

// shareInRoot proves the resolved target is a directory strictly inside the
// checkout and returns its path there. Identity decides first, as it does
// for the sandbox: a link anywhere on the way may lead out, and only the
// directories the kernel would traverse say where it lands.
func shareInRoot(root string, rootID os.FileInfo, rel, target string) (string, *core.RefusalError) {
	if !sandboxAncestorIs(filepath.Dir(target), rootID) {
		return "", shareRefusal(rel, "resolves outside the checkout, to "+target)
	}
	inRoot, ok := sandboxRelInside(root, target)
	if !ok {
		return "", shareRefusal(rel, "is inside the checkout only under another spelling ("+target+
			"), so no sandbox rule in the checkout's spelling would hold for it")
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return "", shareRefusal(rel, "is not a directory")
	}
	return inRoot, nil
}

func uniquePaths(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}

// shareOutsideContent refuses a path git tracks (or holds tracked paths
// below), or one it does not ignore; path is rel itself or what rel
// resolves to inside the checkout.
func shareOutsideContent(ctx context.Context, g contenttree.Git, rel, path string) *core.RefusalError {
	for seg := range strings.SplitSeq(path, "/") {
		if strings.EqualFold(seg, ".git") {
			return shareRefusal(rel, "resolves into git's own directory "+path)
		}
	}
	tracked, err := g.Tracked(ctx, path)
	if err != nil {
		return failed(err)
	}
	if tracked {
		return shareRefusal(rel, "holds tracked content ("+path+") — shared bytes would shadow what the receipt names; "+
			"share only gitignored dependency directories")
	}
	ignored, err := g.Ignored(ctx, path)
	if err != nil {
		return failed(err)
	}
	if !ignored {
		return shareRefusal(rel, "is not gitignored ("+path+") — share only directories .gitignore excludes")
	}
	return nil
}

func shareRefusal(rel, why string) *core.RefusalError {
	return &core.RefusalError{Code: config.CodeBadConfig, Message: fmt.Sprintf(
		"%s verify.share entry %q %s; nothing ran", config.ConfigPath, rel, why)}
}

// bindsShares reports whether the run's sandbox mounts each share read-only
// (bwrap), which needs an empty directory as its mount point in the snapshot.
// Every other run gets a symbolic link: Seatbelt denies writes through it by
// the real path, and an unsandboxed run cannot be stopped from writing.
func (st *state) bindsShares() bool {
	return st.req.Sandbox && runtime.GOOS == "linux"
}

// exposeShares places each share in the snapshot at its configured path,
// through a root pinned at the snapshot, so a tracked symbolic link on the
// way cannot carry the write out of it.
func exposeShares(snapDir string, shared []sharedDir, mountPoints bool) error {
	if len(shared) == 0 {
		return nil
	}
	root, err := os.OpenRoot(snapDir)
	if err != nil {
		return jq.Sealed("open the snapshot", err)
	}
	defer func() { _ = root.Close() }()
	for _, s := range shared {
		if err := exposeShare(root, s, mountPoints); err != nil {
			return jq.Sealed("share "+s.rel, err)
		}
	}
	return nil
}

// exposeShare makes one share's place: an empty mount point, or a link to
// the checkout's directory.
func exposeShare(root *os.Root, s sharedDir, mountPoint bool) error {
	rel := filepath.FromSlash(s.rel)
	if parent := filepath.Dir(rel); parent != "." {
		if err := root.MkdirAll(parent, sharedDirMode); err != nil {
			return err //nolint:wrapcheck // sealed by exposeShares with the entry's name
		}
	}
	if mountPoint {
		return root.Mkdir(rel, sharedDirMode) //nolint:wrapcheck // sealed by exposeShares
	}
	return root.Symlink(s.real, rel) //nolint:wrapcheck // sealed by exposeShares
}

// bwrapShareBinds binds each share read-only at its place in the reviewed
// root, after the root's own writable bind so the mounts land on top of it.
func bwrapShareBinds(repo string, shared []sharedDir) []string {
	var argv []string
	for _, s := range shared {
		argv = append(argv, bwrapROBind, s.real, filepath.Join(repo, filepath.FromSlash(s.rel)))
	}
	return argv
}

// sharedReals lists the physical directories the shares expose.
func sharedReals(shared []sharedDir) []string {
	reals := make([]string, 0, len(shared))
	for _, s := range shared {
		reals = append(reals, s.real)
	}
	return reals
}
