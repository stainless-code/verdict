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
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// sharedDir is one verify.share entry resolved against the checkout: the
// configured slash path, which is where the snapshot exposes it, the
// physical directory in the checkout it exposes, and that directory held
// open. bwrap binds the held directory, never the path, so nothing done to
// the checkout after the checks can retarget the bind.
type sharedDir struct {
	dir  *os.File
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
			closeShares(shared)
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

// resolveShare resolves one entry: skip is true when it does not exist. The
// directory is held open before git judges its path, and every component
// on the way must be a real directory, so what the checks approve is what a
// sandbox later binds.
func resolveShare(ctx context.Context, g contenttree.Git, rootID os.FileInfo, rel string) (sharedDir, bool, *core.RefusalError) {
	literal := filepath.Join(g.Root, filepath.FromSlash(rel))
	target, err := filepath.EvalSymlinks(literal)
	if errors.Is(err, fs.ErrNotExist) {
		return sharedDir{}, true, nil
	}
	if err != nil {
		return sharedDir{}, false, shareRefusal(rel, fmt.Sprintf("cannot be resolved (%v)", err))
	}
	if ref := shareInRoot(g.Root, rootID, rel, target); ref != nil {
		return sharedDir{}, false, ref
	}
	if target != literal {
		return sharedDir{}, false, shareRefusal(rel, "passes through a symbolic link (to "+target+
			") — share the directory itself")
	}
	dir, err := pinShare(g.Root, rootID, rel)
	if err != nil {
		return sharedDir{}, false, shareRefusal(rel, fmt.Sprintf("changed while verify checked it (%v)", err))
	}
	if ref := shareOutsideContent(ctx, g, rel); ref != nil {
		_ = dir.Close()
		return sharedDir{}, false, ref
	}
	return sharedDir{dir: dir, rel: rel, real: target}, false, nil
}

// shareInRoot proves the resolved target is a directory strictly inside the
// checkout. Identity decides first, as it does for the sandbox: a link
// anywhere on the way may lead out, and only the directories the kernel
// would traverse say where it lands.
func shareInRoot(root string, rootID os.FileInfo, rel, target string) *core.RefusalError {
	if !sandboxAncestorIs(filepath.Dir(target), rootID) {
		return shareRefusal(rel, "resolves outside the checkout, to "+target)
	}
	if _, ok := sandboxRelInside(root, target); !ok {
		return shareRefusal(rel, "is inside the checkout only under another spelling ("+target+
			"), so no sandbox rule in the checkout's spelling would hold for it")
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return shareRefusal(rel, "is not a directory")
	}
	return nil
}

// pinShare opens rel under the checkout one component at a time and holds
// the last open. Each component must be a real directory (never a link), and
// the directory opened must be the one its entry named a moment before, so a
// rename or a link swapped in during the walk refuses instead of being
// followed.
func pinShare(root string, rootID os.FileInfo, rel string) (*os.File, error) {
	cur, err := os.OpenRoot(root)
	if err != nil {
		return nil, jq.Sealed("open the checkout", err)
	}
	defer func() { _ = cur.Close() }()
	if info, err := cur.Stat("."); err != nil || !os.SameFile(info, rootID) {
		return nil, jq.NewErr("the checkout moved")
	}
	segs := strings.Split(rel, "/")
	for _, seg := range segs[:len(segs)-1] {
		next, err := pinStep(cur, seg)
		if err != nil {
			return nil, err
		}
		_ = cur.Close()
		cur = next
	}
	return pinLast(cur, segs[len(segs)-1])
}

// pinStep opens one intermediate component as a root of its own.
func pinStep(parent *os.Root, seg string) (*os.Root, error) {
	want, err := realDirEntry(parent, seg)
	if err != nil {
		return nil, err
	}
	next, err := parent.OpenRoot(seg)
	if err != nil {
		return nil, jq.Sealed("open "+seg, err)
	}
	if got, err := next.Stat("."); err != nil || !os.SameFile(got, want) {
		_ = next.Close()
		return nil, jq.NewErrf("%s was replaced while it was opened", seg)
	}
	return next, nil
}

// pinLast opens the share's own directory and holds it.
func pinLast(parent *os.Root, seg string) (*os.File, error) {
	want, err := realDirEntry(parent, seg)
	if err != nil {
		return nil, err
	}
	dir, err := parent.Open(seg)
	if err != nil {
		return nil, jq.Sealed("open "+seg, err)
	}
	if got, err := dir.Stat(); err != nil || !os.SameFile(got, want) {
		_ = dir.Close()
		return nil, jq.NewErrf("%s was replaced while it was opened", seg)
	}
	return dir, nil
}

// realDirEntry is the entry seg names under parent, refused unless it is a
// directory itself rather than a link to one.
func realDirEntry(parent *os.Root, seg string) (os.FileInfo, error) {
	info, err := parent.Lstat(seg)
	if err != nil {
		return nil, jq.Sealed("inspect "+seg, err)
	}
	if !info.IsDir() {
		return nil, jq.NewErrf("%s is not a directory", seg)
	}
	return info, nil
}

// closeShares releases every held directory.
func closeShares(shared []sharedDir) {
	for _, s := range shared {
		if s.dir != nil {
			_ = s.dir.Close()
		}
	}
}

// shareOutsideContent refuses a path git tracks (or holds tracked paths
// below), or one it does not ignore.
func shareOutsideContent(ctx context.Context, g contenttree.Git, rel string) *core.RefusalError {
	tracked, err := g.Tracked(ctx, rel)
	if err != nil {
		return failed(err)
	}
	if tracked {
		return shareRefusal(rel, "holds tracked content — shared bytes would shadow what the receipt names; "+
			"share only gitignored dependency directories")
	}
	ignored, err := g.Ignored(ctx, rel)
	if err != nil {
		return failed(err)
	}
	if !ignored {
		return shareRefusal(rel, "is not gitignored — share only directories .gitignore excludes")
	}
	return nil
}

func shareRefusal(rel, why string) *core.RefusalError {
	return &core.RefusalError{Code: config.CodeBadConfig, Message: core.Msgf(
		"%s verify.share entry %q %s; nothing ran", config.ConfigPath, rel, why)}
}

// reportShares says, once the command ran, which directories it saw from
// the checkout and whether they were read-only. A writable share is also a
// warning, so a --json reader sees the weaker guarantee without reading the
// human lines: only the full sandbox stops a stub planted there.
func (st *state) reportShares(sb *sandbox) {
	if len(st.shared) == 0 {
		return
	}
	names := strings.Join(sharedRels(st.shared), ", ")
	if sb.full() {
		st.human(core.Msgf("» verified with shared directories: %s (read-only under --sandbox)", names))
		return
	}
	st.human(core.Msgf("» verified with shared directories: %s (writable: no --sandbox)", names))
	st.warn("verify.share: " + names + " was writable during the run — only --sandbox makes a shared directory " +
		"read-only, so a stub planted there could make the command pass")
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
// The source is the held directory, inherited as descriptor 3+i in
// [shareFiles] order: bwrap mounts what the descriptor is, whatever the path
// now names, and closes it before the command starts, so the command never
// holds a writable handle into the checkout.
func bwrapShareBinds(repo string, shared []sharedDir) []string {
	var argv []string
	for i, s := range shared {
		argv = append(argv, bwrapROBindFD, strconv.Itoa(firstExtraFD+i), filepath.Join(repo, filepath.FromSlash(s.rel)))
	}
	return argv
}

// shareFiles lists the held directories in the order [bwrapShareBinds]
// numbers them.
func shareFiles(shared []sharedDir) []*os.File {
	files := make([]*os.File, 0, len(shared))
	for _, s := range shared {
		files = append(files, s.dir)
	}
	return files
}

// firstExtraFD is the descriptor a child inherits its first extra file as.
const firstExtraFD = 3

// bwrapROBindFD is bwrap's read-only bind of an inherited descriptor.
const bwrapROBindFD = "--ro-bind-fd"

// sharedReals lists the physical directories the shares expose.
func sharedReals(shared []sharedDir) []string {
	reals := make([]string, 0, len(shared))
	for _, s := range shared {
		reals = append(reals, s.real)
	}
	return reals
}
