package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/agentusage"
	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/hostsandbox"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/storelock"
)

// A review launch never runs in the operator's checkout (STA-537). Most
// agents keep their own file-editing tools during review, and the bundle
// they read is untrusted PR content, so a prompt-injected diff could steer
// one into editing the checkout. Every review launch — a one-shot review, a
// finder, a cross check, a repair round, a drain draft — runs instead with
// its working directory in a disposable view of the reviewed tree: verify's
// content-bound snapshot (contenttree), copied afresh for each launch, with a
// copy of the bundle at its usual relative path, so every relative path a
// prompt names resolves unchanged. When the agent exits, verdict copies back
// only the artifacts that launch is expected to write, validated, and deletes
// the view with everything else the agent wrote in it.

// stateRel is verdict's state directory relative to a tree's root, in slash
// form; a view holds no tree content there, only the bundle copy.
const stateRel = config.VerdictDir + "/" + config.StateDirName

// slash separates the parts of a slash-form path; here is a walk's own
// root, and up the parent a write to `../` reaches.
const (
	slash = "/"
	here  = "."
	up    = ".."
)

// maxArtifactBytes caps one artifact copied back, the cap record reads an
// agent file under; maxChunkCostBytes caps one chunk's cost record.
const (
	maxArtifactBytes  = 32 << 20
	maxChunkCostBytes = 1 << 20
	mebibyte          = 1 << 20
)

// viewDirMode and viewFileMode are the owner bits every copied directory and
// file keeps on top of its own, so verdict can fill and later remove it.
const (
	viewDirMode  = 0o700
	viewFileMode = 0o600
)

// opCreate leads the reason a view path could not be made.
const opCreate = "create "

// shownStrays caps the paths one confinement warning names.
const shownStrays = 10

// snapshotSlack is how long past its spawns' budgets an execution may still
// be setting up, recording, or cleaning up its snapshot, verify's margin.
const snapshotSlack = time.Hour

// gitDir is a checkout's git entry; privateTmp is the view's own temp
// directory beside its tree.
const (
	gitDir     = ".git"
	privateTmp = "tmp"
)

// The reasons an artifact is not copied back, as its warning names them.
const (
	whyUnreadable = "unreadable"
	whyNotRegular = "not a regular file"
)

// workspace is one execution's reviewed tree: snapshotted once, on the
// execution's first review launch, and never handed to an agent itself. Each
// launch works in its own copy of it (a view), so no launch can change what
// a later one reads. Copies, not hard links: a link shares the file, and an
// agent running as root ignores a read-only bit, so one launch could rewrite
// the tree every later launch reads.
type workspace struct {
	err      error
	env      map[string]string
	deadline time.Time
	base     string
	tree     string
	name     string
	// print fingerprints the snapshot as it was taken; dropped names the
	// tree's symlinks the snapshot left out.
	print   string
	dropped []string
	git     contenttree.Git
	mu      sync.Mutex
	taken   bool
}

// newWorkspace is the execution's workspace over the tree the agent starts
// in, nil for a render, which launches nothing. Its deadline outlives every
// spawn the execution can make — up to two finder and two cross phases, each
// with its repair rounds — so a concurrent sweep never takes a live
// snapshot. It is wall-clock time, like verify's, whatever clock the request
// injects.
func (st *state) newWorkspace(timeout, rounds int) *workspace {
	if st.req.Print {
		return nil
	}
	root := st.workDir()
	spawn := time.Duration(timeout)*time.Second + agentGracePeriod
	var env map[string]string
	if st.req.Runtime != nil {
		env = st.req.Env
	}
	return &workspace{
		git:      contenttree.Git{Cmdr: st.commander(), Root: root, Env: st.req.Environ},
		env:      env,
		deadline: time.Now().Add(time.Duration(2*rounds+2)*spawn + snapshotSlack),
		name:     viewName(root),
	}
}

// viewName names the view's tree directory after the reviewed checkout, so
// the agent's working directory still reads as that project.
func viewName(root string) string {
	name := filepath.Base(contenttree.Resolved(root))
	if name == "" || name == here || name == up || strings.ContainsAny(name, `/\:`) ||
		strings.EqualFold(name, privateTmp) {
		return "tree"
	}
	return name
}

// canonical takes the execution's snapshot on the first call and returns it
// on every call: concurrent finders wait for the one snapshot. A tree
// outside any git checkout is "": its views hold the bundle alone.
func (w *workspace) canonical(ctx context.Context) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.taken {
		w.taken = true
		w.tree, w.err = w.take(ctx)
	}
	return w.tree, w.err
}

// take sweeps the snapshots earlier runs stranded and snapshots the tree,
// under a parent that holds neither the checkout nor is held by it.
//
// Outside any git checkout — a review run from a plain directory, the temp
// root a `demo` of a pull request URL reviews in — there is no index to
// name content by, and copying the directory as it stands could copy a
// whole home directory into every launch: the view holds the bundle alone,
// from which a remote review reads its diff.
func (w *workspace) take(ctx context.Context) (string, error) {
	git := insideGitCheckout(w.git.Root)
	base, err := snapshotBase(w.git.Root, contenttree.Resolved(tempDir(w.env, runtime.GOOS)), w.env, git)
	if err != nil {
		return "", err
	}
	w.base = base
	if !git {
		contenttree.SweepStranded(w.git.Root, base, time.Now())
		return "", nil
	}
	if w.git.Cmdr == nil {
		return "", errNoCommander
	}
	// The watchdog commander polls a command whose context carries a
	// deadline once a second, so each short git read of the snapshot would
	// cost a second under a budgeted run; the run's end still cancels them.
	gctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer context.AfterFunc(ctx, cancel)()
	defer cancel()
	contenttree.SweepSnapshots(gctx, w.git, base, time.Now())
	dir, _, err := w.git.SnapshotUnder(gctx, base, w.deadline)
	if err != nil {
		return "", jq.Sealed("snapshot the reviewed tree", err)
	}
	if err := w.detach(gctx, dir); err != nil {
		return "", jq.Sealed("detach the snapshot", errors.Join(err, w.git.RemoveCheckout(context.WithoutCancel(ctx), dir)))
	}
	if err := w.seal(dir); err != nil {
		return "", jq.Sealed("seal the snapshot", errors.Join(err, contenttree.RemoveTree(dir)))
	}
	return dir, nil
}

// seal readies the snapshot to be shared by every launch's copy: it drops
// every committed symlink — the snapshot sits beside each copy, where a
// write through `../../<snapshot>/<link>` would follow a link to the
// operator's own files — makes the rest read-only and owner-only
// (directories 0500, files at most 0500) so a plain file tool's write is
// refused and no other user reads the copy, and fingerprints it, so a
// change any launch made despite that is caught before the next copy.
func (w *workspace) seal(dir string) error {
	dropped, err := stripLinks(dir)
	if err != nil {
		return err
	}
	w.dropped = dropped
	if err := readOnly(dir); err != nil {
		return err
	}
	w.print, err = fingerprint(dir)
	return err
}

// stripLinks removes every symlink below dir, never following one, and
// names them, sorted.
func stripLinks(dir string) ([]string, error) {
	var links []string
	err := walkRel(dir, func(p, rel string, d fs.DirEntry) error {
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		links = append(links, rel)
		return os.Remove(p)
	})
	slices.Sort(links)
	if err != nil {
		return nil, jq.Sealed("drop the snapshot's symlinks", err)
	}
	return links, nil
}

// readOnly takes write access to every file and directory below dir, and to
// dir itself, deepest directories last so the walk can still descend.
func readOnly(dir string) error {
	dirs := []string{dir}
	err := walkRel(dir, func(p, _ string, d fs.DirEntry) error {
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err //nolint:wrapcheck // sealed below
		}
		return os.Chmod(p, info.Mode().Perm()&ownerBits&^writeBits)
	})
	for i := len(dirs) - 1; i >= 0 && err == nil; i-- {
		err = os.Chmod(dirs[i], readOnlyDirMode)
	}
	if err != nil {
		return jq.Sealed("make the snapshot read-only", err)
	}
	return nil
}

// readOnlyDirMode is a sealed snapshot directory: listed and entered by its
// owner alone, never written; ownerBits keep a sealed file's owner bits and
// drop the group's and others', and writeBits are every class's write
// permission. Windows applies no directory mode, and only the read-only
// attribute of a file.
const (
	readOnlyDirMode = 0o500
	ownerBits       = 0o700
	writeBits       = 0o222
)

// fingerprint names every entry below dir by path, mode, size, modification
// time, and — on unix — inode and change time, all from the lstat the walk
// already makes. A write that restores an entry's size, mode, and
// modification time still moves its change time, which no user-space call
// can set; on Windows such a rewrite passes.
func fingerprint(dir string) (string, error) {
	h := sha256.New()
	err := walkRel(dir, func(_, rel string, d fs.DirEntry) error {
		info, err := d.Info()
		if err != nil {
			return err //nolint:wrapcheck // sealed below
		}
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d\x00%s\n", rel, info.Mode(), info.Size(), info.ModTime().UnixNano(),
			identity(info))
		return nil
	})
	if err != nil {
		return "", jq.Sealed("fingerprint the snapshot", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// detach turns the snapshot into a plain directory as soon as it is named:
// its .git — a linked worktree's pointer into the operator's repository — is
// removed and the registration pruned, so nothing beside a view leads back to
// the checkout.
func (w *workspace) detach(ctx context.Context, dir string) error {
	pointer := filepath.Join(dir, gitDir)
	if info, err := os.Lstat(pointer); err != nil || !info.Mode().IsRegular() {
		return jq.NewErrf("%s is not a worktree's .git file", pointer)
	}
	if err := os.Remove(pointer); err != nil {
		return jq.Sealed("remove "+pointer, err)
	}
	if err := w.git.Prune(ctx); err != nil {
		return jq.Sealed("prune the snapshot's registration", err)
	}
	return nil
}

// snapshotBase is the parent every snapshot and view of a run lives under:
// the host temp root, validated as verify validates it (never inside the
// checkout). A git checkout that itself lies inside the temp root — a
// clone in /tmp — would sit beside every view and inside the temp roots an
// agent's own sandbox opens, so its snapshots go to verdict's directory
// under the user cache directory instead, which must neither hold the
// checkout nor lie inside it; without one the run refuses.
func snapshotBase(root, tmp string, env map[string]string, git bool) (string, error) {
	if contenttree.Inside(root, tmp) {
		return "", jq.NewErrf("the temp directory %s is inside the checkout — set TMPDIR outside it", tmp)
	}
	if !git || !contenttree.Inside(tmp, root) {
		return tmp, nil
	}
	dir, err := plannedSnapshotDir(root, tmp, cacheDir(env, runtime.GOOS))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, viewDirMode); err != nil {
		return "", jq.Sealed(opCreate+dir, err)
	}
	dir = contenttree.Resolved(dir)
	if apart := apartFrom(dir, root, tmp); apart != "" {
		return "", jq.NewErrf("the snapshot directory %s %s", dir, apart)
	}
	return dir, nil
}

// plannedSnapshotDir is the snapshot directory verdict would create under
// cache, checked before anything is created there: a cache under the checkout
// (HOME unset reads as the working directory) must not grow a directory
// there, and no component verdict would create below the cache may be a link
// that leads somewhere else.
func plannedSnapshotDir(root, tmp, cache string) (string, error) {
	if cache == "" {
		return "", jq.NewErrf("the checkout %s is inside the temp directory %s, and no cache directory "+
			"(XDG_CACHE_HOME, else HOME, or LOCALAPPDATA on Windows) is set to hold its review snapshots "+
			"apart from it", root, tmp)
	}
	base, err := physical(cache)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "verdict", "snapshots")
	if err := noLinkBelow(base, dir); err != nil {
		return "", err
	}
	if apart := apartFrom(dir, root, tmp); apart != "" {
		return "", jq.NewErrf("the snapshot directory %s %s — set XDG_CACHE_HOME outside both the checkout "+
			"and the temp directory", dir, apart)
	}
	return dir, nil
}

// physical is name with its deepest existing ancestor resolved through every
// link and the components below it, which do not exist yet, joined back: the
// directory MkdirAll would really create. A missing tail is never resolved
// by itself, so a link above it (a cache under `<outside>/link/missing`,
// link pointing into the checkout) is followed here, before anything is
// created through it.
func physical(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", jq.Sealed("resolve "+name, err)
	}
	at, missing := deepestExisting(abs)
	if at == "" {
		return abs, nil
	}
	resolved, err := filepath.EvalSymlinks(at)
	if err != nil {
		return "", jq.Sealed("resolve "+at, err)
	}
	return filepath.Join(append([]string{resolved}, missing...)...), nil
}

// deepestExisting is the deepest ancestor of abs (abs itself included) that
// exists, and the components below it that do not, in order; "" when none
// exists.
func deepestExisting(abs string) (string, []string) {
	var missing []string
	for at := abs; ; at = filepath.Dir(at) {
		if _, err := os.Lstat(at); err == nil {
			slices.Reverse(missing)
			return at, missing
		}
		if filepath.Dir(at) == at {
			return "", nil
		}
		missing = append(missing, filepath.Base(at))
	}
}

// noLinkBelow refuses when any existing component of dir below base is a
// link or not a directory: MkdirAll would follow it.
func noLinkBelow(base, dir string) error {
	rel, err := filepath.Rel(base, dir)
	if err != nil {
		return jq.Sealed("place "+dir, err)
	}
	at := base
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		at = filepath.Join(at, part)
		info, err := os.Lstat(at)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil || !info.IsDir():
			return jq.NewErrf("%s is a link or not a directory — verdict creates its review snapshots below it", at)
		}
	}
	return nil
}

// apartFrom says how dir fails to stand apart from the checkout at root and
// the temp directory tmp, "" when it holds neither and lies in neither.
func apartFrom(dir, root, tmp string) string {
	switch {
	case contenttree.Inside(root, dir), contenttree.Inside(dir, root):
		return "and the checkout hold one another"
	case contenttree.Inside(tmp, dir):
		return "lies in the temp directory beside the checkout"
	}
	return ""
}

// goosWindows is Windows' GOOS, whose temp and cache variables differ.
const goosWindows = "windows"

// tempDir is the host temp root the injected environment names on goos —
// TMPDIR, or TMP and TEMP on Windows, as [os.TempDir] reads them — else the
// process's own.
func tempDir(env map[string]string, goos string) string {
	keys := []string{"TMPDIR"}
	if goos == goosWindows {
		keys = []string{"TMP", "TEMP"}
	}
	for _, key := range keys {
		if dir := env[key]; filepath.IsAbs(dir) {
			return dir
		}
	}
	return os.TempDir()
}

// cacheDir is the user cache directory the injected environment names on
// goos: XDG_CACHE_HOME when absolute, else the platform's own, as
// [os.UserCacheDir] names it — %LocalAppData% on Windows, under HOME
// elsewhere — "" when none is set.
func cacheDir(env map[string]string, goos string) string {
	if xdg := env["XDG_CACHE_HOME"]; filepath.IsAbs(xdg) {
		return xdg
	}
	home := env["HOME"]
	switch {
	case goos == goosWindows:
		if local := env["LOCALAPPDATA"]; filepath.IsAbs(local) {
			return local
		}
		return ""
	case !filepath.IsAbs(home):
		return ""
	case goos == "darwin":
		return filepath.Join(home, "Library", "Caches")
	}
	return filepath.Join(home, ".cache")
}

// insideGitCheckout reports whether root or a directory above it holds a
// .git entry, the way git itself finds a checkout.
func insideGitCheckout(root string) bool {
	for dir := contenttree.Resolved(root); ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, gitDir)); err == nil {
			return true
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}

// close removes the snapshot, once; a later call is a no-op.
func (w *workspace) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	tree := w.tree
	w.tree = ""
	if tree == "" {
		return nil
	}
	if err := contenttree.RemoveTree(tree); err != nil {
		return jq.Sealed("remove "+tree, err)
	}
	return nil
}

// view is one launch's disposable copy of the tree. The agent starts in cwd,
// a directory of the wrapper, so even a write to `../` lands in a directory
// verdict owns and deletes; tmp beside it is the launch's private TMPDIR.
type view struct {
	// tree and seed stamp every entry verdict copied in: the tree's files by
	// cwd-relative path, the bundle's files by bundle-relative path. A
	// stamp that changed is a write.
	tree    map[string]stamp
	seed    map[string]stamp
	wrapper string
	cwd     string
	tmp     string
	bundle  string
	rel     string
	// dropped names the tree's symlinks the view leaves out: every one, as
	// the snapshot dropped them.
	dropped []string
	// root is the bundle copy, opened before the agent starts: settlement
	// reads only through it, so a directory the agent swaps for a link
	// cannot redirect the copy-back. pins are the bundle copy and its
	// ancestors (.verdict, .verdict/state) as they were then.
	root *os.Root
	pins []pin
}

// pin is one directory of the view as it was before the agent started.
type pin struct {
	info fs.FileInfo
	path string
}

// stamp is what a file looked like when verdict copied it in. sum is set
// only for an expected artifact, whose rewrite with the same size in the
// same timestamp tick must still read as a write.
type stamp struct {
	mtime time.Time
	size  int64
	sum   [sha256.Size]byte
}

// open makes one launch's view: a fresh wrapper named for the launch's own
// deadline, the tree copied into it, and the bundle at its relative path.
// own is the launch's output directory relative to the bundle ("." for the
// bundle itself); expect names the artifacts the launch may write.
func (w *workspace) open(ctx context.Context, bundle, own string, expect expectation, timeout int) (*view, error) {
	tree, err := w.canonical(ctx)
	if err != nil {
		return nil, err
	}
	if err := w.unchanged(tree); err != nil {
		return nil, err
	}
	rel, err := bundleRel(bundle)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(time.Duration(timeout)*time.Second + agentGracePeriod + snapshotSlack)
	wrapper, err := contenttree.NewSnapshotDir(w.git.Root, w.base, deadline)
	if err != nil {
		return nil, jq.Sealed("create the view directory", err)
	}
	v := &view{
		wrapper: wrapper, cwd: filepath.Join(wrapper, w.name), tmp: filepath.Join(wrapper, privateTmp), rel: rel,
		dropped: slices.Clone(w.dropped),
	}
	v.bundle = filepath.Join(v.cwd, filepath.FromSlash(rel))
	if err := v.fill(tree, bundle, own, expect); err != nil {
		return nil, jq.Sealed("fill the view", errors.Join(err, contenttree.RemoveTree(wrapper)))
	}
	if err := v.pinBundle(); err != nil {
		return nil, jq.Sealed("pin the view", errors.Join(err, contenttree.RemoveTree(wrapper)))
	}
	return v, nil
}

// pinBundle records the bundle copy and its ancestors below the view's root
// and opens the bundle copy as a root, all before the agent starts.
func (v *view) pinBundle() error {
	dir := v.cwd
	for part := range strings.SplitSeq(v.rel, slash) {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			return jq.NewErrf("%s is not a directory in the review snapshot", dir)
		}
		v.pins = append(v.pins, pin{path: dir, info: info})
	}
	root, err := pinnedDir(v.bundle)
	if err != nil {
		return err
	}
	v.root = root
	return nil
}

// replaced names the first pinned directory the agent replaced — moved,
// removed, or swapped for a link — "" when every one is the directory it was
// before the agent started.
func (v *view) replaced() string {
	for _, p := range v.pins {
		now, err := os.Lstat(p.path)
		if err != nil || !now.IsDir() || !os.SameFile(p.info, now) {
			rel, _ := filepath.Rel(v.cwd, p.path) //nolint:errcheck // p.path lies below v.cwd
			return filepath.ToSlash(rel)
		}
	}
	return ""
}

// closeRoot releases the pinned bundle copy.
func (v *view) closeRoot() {
	if v.root != nil {
		_ = v.root.Close()
		v.root = nil
	}
}

// discard removes the view, its pinned bundle copy closed first: Windows
// refuses to delete a directory a process holds open.
func (v *view) discard() error {
	v.closeRoot()
	return contenttree.RemoveTree(v.wrapper) //nolint:wrapcheck // the caller names the view around the reason
}

// unchanged refuses a snapshot whose fingerprint moved since it was taken: a
// launch wrote outside its own copy, into the read-only snapshot every later
// copy is made from.
func (w *workspace) unchanged(tree string) error {
	if tree == "" {
		return nil
	}
	if now, err := fingerprint(tree); err != nil || now != w.print {
		return jq.NewErrf("the shared review snapshot at %s changed after it was taken — a launch wrote "+
			"outside its own copy, so no later launch is copied from it", tree)
	}
	return nil
}

// bundleRel is the bundle's path below the reviewed tree's root, in slash
// form: always .verdict/state/<name>, which a view keeps free of tree
// content. The bundle lives under the repository root even when the agent
// starts in another checkout, so it is resolved against that root's state
// directory, not the checkout's.
func bundleRel(bundle string) (string, error) {
	name := filepath.Base(bundle)
	parent := filepath.Dir(contenttree.Resolved(bundle))
	if filepath.Base(parent) != config.StateDirName || filepath.Base(filepath.Dir(parent)) != config.VerdictDir ||
		name == here || name == up || name == "" {
		return "", jq.NewErrf("the bundle %s is not under %s", bundle, stateRel)
	}
	return stateRel + slash + name, nil
}

// fill copies the tree, then the bundle, into the view, and gives it a git
// directory and a temp directory of its own.
func (v *view) fill(tree, bundle, own string, expect expectation) error {
	if err := os.Mkdir(v.tmp, viewDirMode); err != nil {
		return jq.Sealed(opCreate+v.tmp, err)
	}
	if err := v.copyTree(tree); err != nil {
		return err
	}
	if err := standaloneGit(v.cwd); err != nil {
		return err
	}
	if err := freshBundleDirs(v.cwd, v.rel); err != nil {
		return err
	}
	return v.copyBundle(bundle, own, expect)
}

// walkRel walks src and hands visit each entry below it with its slash-form
// path relative to src; a walk error stops the walk. It never follows a
// link.
func walkRel(src string, visit func(p, rel string, d fs.DirEntry) error) error {
	//nolint:wrapcheck // the caller names the walked directory around the reason
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil || rel == here {
			return err
		}
		return visit(p, filepath.ToSlash(rel), d)
	})
}

// treeExcluded reports a tree path the view never holds, its segments
// compared whatever their case (APFS and NTFS fold them): any `.git` at any
// depth — a linked worktree's pointer, a nested repository's hooks and
// config — verdict's state directory, where the bundle copy goes, and a
// `.verdict` that is not a real directory, which that copy would follow.
func treeExcluded(rel string, d fs.DirEntry) bool {
	if slices.ContainsFunc(strings.Split(rel, slash), func(seg string) bool { return strings.EqualFold(seg, gitDir) }) {
		return true
	}
	return contenttree.HasPathPrefixFold(rel, stateRel) || (strings.EqualFold(rel, config.VerdictDir) && !d.IsDir())
}

// copyTree copies the snapshot into cwd, stamping each file, outside the
// excluded paths (treeExcluded). No symlink is copied: a link can lead out
// of the view, directly or through another, and a review reads code, so
// every committed symlink is left out, never followed, and named.
func (v *view) copyTree(src string) error {
	v.tree = map[string]stamp{}
	if err := os.Mkdir(v.cwd, viewDirMode); err != nil {
		return jq.Sealed(opCreate+v.cwd, err)
	}
	if src == "" {
		return nil
	}
	return walkRel(src, func(p, rel string, d fs.DirEntry) error {
		switch {
		case treeExcluded(rel, d):
			return skipEntry(d)
		case d.Type()&fs.ModeSymlink != 0:
			v.dropped = append(v.dropped, rel)
			return nil
		}
		return copyEntry(p, filepath.Join(v.cwd, filepath.FromSlash(rel)), rel, d, v.tree, nil)
	})
}

// gitFiles are the files of an empty repository: enough for an agent CLI
// that looks for a git checkout (codex's trust check, opencode's project
// root) to find the view, and nothing that names the operator's repository.
var gitFiles = map[string]string{
	"HEAD":   "ref: refs/heads/main\n",
	"config": "[core]\n\trepositoryformatversion = 0\n\tbare = false\n",
}

// standaloneGit gives the view a git directory of its own: an empty
// repository with no history, no hooks, and no path outside the view.
func standaloneGit(cwd string) error {
	dir := filepath.Join(cwd, gitDir)
	for _, sub := range []string{"", "objects", "refs", filepath.Join("refs", "heads"), filepath.Join("refs", "tags")} {
		if err := os.Mkdir(filepath.Join(dir, sub), viewDirMode); err != nil {
			return jq.Sealed(opCreate+dir, err)
		}
	}
	for name, body := range gitFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), viewFileMode); err != nil {
			return jq.Sealed(opCreate+dir, err)
		}
	}
	return nil
}

// copyBundle copies the bundle into the view: its inputs, its earlier
// outputs, and the launch's own output directory, but no other phase
// directory (x-*) — a parallel phase is writing its own while this launch
// starts, and this launch reads none of it. Lock files stay out.
func (v *view) copyBundle(src, own string, expect expectation) error {
	v.seed = map[string]stamp{}
	ownTop, _, _ := strings.Cut(own, slash)
	return walkRel(src, func(p, rel string, d fs.DirEntry) error {
		top, _, _ := strings.Cut(rel, slash)
		switch {
		case strings.HasPrefix(top, "x-") && top != ownTop:
			return skipEntry(d)
		case strings.HasSuffix(d.Name(), storelock.LockSuffix):
			return nil
		}
		return copyEntry(p, filepath.Join(v.bundle, filepath.FromSlash(rel)), rel, d,
			v.seed, func(rel string) bool { return expect(rel) > 0 })
	})
}

// skipEntry leaves one walked entry out: a directory with everything in it.
func skipEntry(d fs.DirEntry) error {
	if d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

// freshBundleDirs creates rel's directories under root, each a real
// directory: the top one (.verdict) may already hold tree content, but every
// one below it must be new, so the bundle copy holds exactly what verdict
// copied into it, never a tree file that reached the same path through a
// case-folding filesystem.
func freshBundleDirs(root, rel string) error {
	dir := root
	for i, part := range strings.Split(rel, slash) {
		dir = filepath.Join(dir, part)
		if err := realDir(dir, i == 0); err != nil {
			return err
		}
	}
	return nil
}

// realDir creates dir — or, when mayExist, keeps one already there — and
// checks it is a real directory, not a link to one.
func realDir(dir string, mayExist bool) error {
	err := os.Mkdir(dir, viewDirMode)
	switch {
	case errors.Is(err, fs.ErrExist) && !mayExist:
		return jq.NewErrf("%s already exists in the review snapshot before verdict copied the bundle there", dir)
	case err != nil && !errors.Is(err, fs.ErrExist):
		return jq.Sealed(opCreate+dir, err)
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return jq.NewErrf("%s is not a directory in the review snapshot", dir)
	}
	return nil
}

// copyEntry copies one walked directory or regular file to dst, a file with
// its mode and modification time, and stamps a file in stamps under rel.
// Anything else — a symlink, socket, FIFO, or device — is left out. summed,
// when set, selects the files whose bytes are summed too.
func copyEntry(src, dst, rel string, d fs.DirEntry, stamps map[string]stamp, summed func(string) bool) error {
	info, err := d.Info()
	if err != nil {
		return jq.Sealed("stat "+src, err)
	}
	switch {
	case d.IsDir():
		if err := os.Mkdir(dst, info.Mode().Perm()|viewDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
			return jq.Sealed(opCreate+dst, err)
		}
	case d.Type().IsRegular():
		sum, err := copyFile(src, dst, info, summed != nil && summed(rel))
		stamps[rel] = stamp{mtime: info.ModTime(), size: info.Size(), sum: sum}
		return err
	}
	return nil
}

// copyFile copies one regular file into a fresh path, keeping its permission
// bits and modification time, and returns its sum when asked.
func copyFile(src, dst string, info fs.FileInfo, summed bool) (sum [sha256.Size]byte, err error) {
	in, err := openWalked(src, info)
	if err != nil {
		return sum, err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm()|viewFileMode)
	if err != nil {
		return sum, jq.Sealed(opCreate+dst, err)
	}
	hash := sha256.New()
	var w io.Writer = out
	if summed {
		w = io.MultiWriter(out, hash)
	}
	_, err = io.Copy(w, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return sum, jq.Sealed("copy "+src, err)
	}
	copy(sum[:], hash.Sum(nil))
	if err := os.Chtimes(dst, info.ModTime(), info.ModTime()); err != nil {
		return sum, jq.Sealed("stamp "+dst, err)
	}
	return sum, nil
}

// openWalked opens the regular file at src without blocking, refusing it
// unless it is still the file the walk saw (info).
func openWalked(src string, info fs.FileInfo) (*os.File, error) {
	in, err := readcommon.OpenNoBlock(src)
	if err != nil {
		return nil, jq.Sealed("open "+src, err)
	}
	if opened, err := in.Stat(); err != nil || !opened.Mode().IsRegular() || !os.SameFile(opened, info) {
		_ = in.Close()
		return nil, jq.NewErrf("%s changed while it was copied", src)
	}
	return in, nil
}

// droppedEnv are the variables a review launch never inherits, each naming
// a directory of the operator's that the view replaces: the working
// directory a shell or package manager recorded (PWD and OLDPWD, npm's and
// yarn's INIT_CWD, PROJECT_CWD and npm_config_local_prefix, claude's
// CLAUDE_PROJECT_DIR), git's ceiling and command-scope configuration, and
// the temp directory, which the view's private one replaces.
var droppedEnv = []string{
	"PWD", "OLDPWD", "INIT_CWD", "PROJECT_CWD", "npm_config_local_prefix", "CLAUDE_PROJECT_DIR",
	"GIT_CEILING_DIRECTORIES", "GIT_CONFIG_COUNT", "TMPDIR", "TMP", "TEMP",
}

// droppedEnvPrefixes are the families of droppedEnv: git's numbered
// command-scope configuration.
var droppedEnvPrefixes = []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"}

// configHomes are the agent configuration directories a relative value of
// which would resolve inside the view instead of where the operator meant.
var configHomes = []string{
	"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
	"XDG_CONFIG_HOME",
}

// env is the launch environment for the view: PWD names the view and
// TMPDIR, TMP and TEMP its private temp directory; the variables naming the
// operator's own directories (droppedEnv) and git's location variables
// (GIT_DIR and friends), which would point git inside the view back at the
// operator's repository, are dropped; and a relative agent configuration
// directory is resolved where the operator ran verdict. Names match
// whatever their case, as Windows reads them.
func (v *view) env(env []string) []string {
	out := make([]string, 0, len(env)+len(droppedEnv))
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		switch {
		case dropsEnv(name):
		case slices.ContainsFunc(configHomes, func(h string) bool { return strings.EqualFold(h, name) }) &&
			value != "" && !filepath.IsAbs(value):
			if abs, err := filepath.Abs(value); err == nil {
				out = append(out, name+"="+abs)
			}
		default:
			out = append(out, kv)
		}
	}
	return append(out, "PWD="+v.cwd, "TMPDIR="+v.tmp, "TMP="+v.tmp, "TEMP="+v.tmp)
}

// dropsEnv reports a variable a review launch never inherits.
func dropsEnv(name string) bool {
	upper := strings.ToUpper(name)
	return gitcmd.Inherited(name) ||
		slices.ContainsFunc(droppedEnv, func(d string) bool { return strings.EqualFold(d, name) }) ||
		slices.ContainsFunc(droppedEnvPrefixes, func(p string) bool { return strings.HasPrefix(upper, p) })
}

// settled is what one launch's view gave back: everything the agent wrote
// that was not copied back, by the path it had from its working directory,
// and the artifacts refused with the reason.
type settled struct {
	strays  []string
	refused []string
}

// settle copies the launch's changed artifacts back into the real bundle and
// lists every other write: a file outside the bundle copy, an unexpected
// file inside it, and an artifact that is not a lone regular file within
// its cap.
func (v *view) settle(bundle string, expect expectation) settled {
	var out settled
	v.settleBundle(bundle, expect, &out)
	v.treeStrays(&out)
	v.wrapperStrays(&out)
	slices.Sort(out.strays)
	return out
}

// pinnedDir opens dir as a root after checking it is a real directory and
// that the root opened is that directory, so a directory swapped for a link
// between the check and the open is refused, never written or read through.
func pinnedDir(dir string) (*os.Root, error) {
	before, err := os.Lstat(dir)
	if err != nil || !before.IsDir() {
		return nil, jq.NewErrf("%s is not a directory", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, jq.Sealed("open "+dir, err)
	}
	if after, err := root.Stat(here); err != nil || !os.SameFile(before, after) {
		_ = root.Close()
		return nil, jq.NewErrf("%s changed while it was opened", dir)
	}
	return root, nil
}

// settleBundle walks the bundle copy through the root pinned before the
// agent started, refusing outright when the agent replaced that directory or
// an ancestor of it. An unchanged file
// is the seed verdict put there; a changed one is copied back into the
// pinned real bundle when expected and named otherwise.
func (v *view) settleBundle(bundle string, expect expectation, out *settled) {
	if dir := v.replaced(); dir != "" {
		out.refused = append(out.refused, v.rel+" (its directory "+dir+" was replaced while the agent ran; "+
			"nothing was copied back)")
		return
	}
	from := v.root
	to, err := pinnedDir(bundle)
	if err != nil {
		out.refused = append(out.refused, v.rel+" (the bundle changed while the agent ran)")
		return
	}
	defer func() { _ = to.Close() }()
	seen, complete := map[string]bool{}, true
	//nolint:errcheck // best effort: an entry the walk cannot read is never copied back
	_ = fs.WalkDir(from.FS(), here, func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			complete = false
			out.refused = append(out.refused, path.Join(v.rel, rel)+" (unreadable)")
			return nil //nolint:nilerr // an unreadable entry is never copied back, only named
		}
		if !d.IsDir() {
			seen[rel] = true
			v.settleOne(from, to, rel, d, expect(rel), out)
		}
		return nil
	})
	if complete {
		v.deletedSeeds(seen, out)
	}
}

// deletedSeeds names every file verdict seeded in the bundle copy that the
// agent removed: a deletion is a write too, and it never reaches the bundle.
// It runs only after a walk that read every directory: a seed in a subtree
// the walk could not read is unseen, not deleted.
func (v *view) deletedSeeds(seen map[string]bool, out *settled) {
	for rel := range v.seed {
		if !seen[rel] {
			out.strays = append(out.strays, v.rel+slash+rel)
		}
	}
}

// settleOne settles one file of the bundle copy: an unchanged seed is left
// alone, an unexpected change named, an expected one copied back.
func (v *view) settleOne(from, to *os.Root, rel string, d fs.DirEntry, limit int64, out *settled) {
	shown := v.rel + slash + rel
	info, err := d.Info()
	switch {
	case err != nil:
		out.refused = append(out.refused, shown+" ("+whyUnreadable+")")
	case !v.changed(from, rel, info, limit):
	case limit == 0:
		out.strays = append(out.strays, shown)
	default:
		if why := copyBack(from, to, rel, info, limit); why != "" {
			out.refused = append(out.refused, shown+" ("+why+")")
		}
	}
}

// changed reports whether the bundle copy's file at rel, as the walk saw it
// (info), differs from what verdict seeded there: new, of another kind, size
// or time, or — for an expected artifact — other bytes, read as copy-back
// reads them.
func (v *view) changed(from *os.Root, rel string, info fs.FileInfo, limit int64) bool {
	seed, ok := v.seed[rel]
	if !ok || !info.Mode().IsRegular() || info.Size() != seed.size || !info.ModTime().Equal(seed.mtime) {
		return true
	}
	if limit == 0 {
		return false
	}
	raw, _, why := readPinned(from, rel, info, limit)
	return why != "" || sha256.Sum256(raw) != seed.sum
}

// readPinned reads one file below root as the walk saw it (walked): opened
// without following a link and without blocking (a FIFO cannot hold the
// read), and only when the opened file is a regular file with one name, the
// very file the walk saw, within limit — read through a cap of limit+1, so a
// file that grew is refused, never loaded whole. why is "" on success.
func readPinned(root *os.Root, rel string, walked fs.FileInfo, limit int64) (raw []byte, opened fs.FileInfo, why string) {
	f, err := readcommon.OpenNoFollowIn(root, filepath.FromSlash(rel))
	if err != nil {
		return nil, nil, whyUnreadable
	}
	defer func() { _ = f.Close() }()
	opened, err = f.Stat()
	switch {
	case err != nil:
		return nil, nil, whyUnreadable
	case !opened.Mode().IsRegular():
		return nil, nil, whyNotRegular
	case readcommon.OpenedHasHardLinks(f, opened):
		return nil, nil, "a hard link"
	case !os.SameFile(opened, walked):
		return nil, nil, "replaced while it was read"
	case opened.Size() > limit:
		return nil, nil, oversized(limit)
	}
	raw, err = io.ReadAll(io.LimitReader(f, limit+1))
	switch {
	case err != nil:
		return nil, nil, whyUnreadable
	case int64(len(raw)) > limit:
		return nil, nil, oversized(limit)
	}
	return raw, opened, ""
}

// oversized names a cap an artifact crossed.
func oversized(limit int64) string {
	return fmt.Sprintf("over %d MiB", max(1, limit/mebibyte))
}

// copyBack copies one expected artifact from the pinned bundle copy into the
// pinned real bundle and says why it would not, "" on success. The source is
// read as readPinned reads it; the copy is atomic and carries the agent's
// modification time from before its rename, so the freshness check reads the
// agent's write, not the copy's. Its directory must already be a real
// directory of the bundle — verdict made every one a launch writes in.
func copyBack(from, to *os.Root, rel string, walked fs.FileInfo, limit int64) string {
	switch {
	case walked.Mode()&fs.ModeSymlink != 0:
		return "a symlink"
	case !walked.Mode().IsRegular():
		return whyNotRegular
	}
	raw, opened, why := readPinned(from, rel, walked, limit)
	if why != "" {
		return why
	}
	name := filepath.FromSlash(rel)
	if dirInfo, err := to.Lstat(filepath.Dir(name)); err != nil || !dirInfo.IsDir() {
		return "its bundle directory is missing"
	}
	if err := atomic.WriteFileModeInAt(to, name, raw, roundEvidenceMode, opened.ModTime()); err != nil {
		return "could not be written to the bundle"
	}
	return ""
}

// treeStrays names every tree file the agent added, changed, or removed
// outside the bundle copy and the view's own .git. Directories are not
// compared: an empty directory carries nothing.
func (v *view) treeStrays(out *settled) {
	seen := map[string]bool{}
	//nolint:errcheck // best effort: a write the walk cannot read is still discarded
	_ = walkRel(v.cwd, func(_, rel string, d fs.DirEntry) error {
		switch {
		case rel == v.rel, rel == gitDir:
			return filepath.SkipDir
		case d.IsDir():
			return nil
		}
		seen[rel] = true
		if v.treeChanged(rel, d) {
			out.strays = append(out.strays, rel)
		}
		return nil
	})
	for rel := range v.tree {
		if !seen[rel] {
			out.strays = append(out.strays, rel)
		}
	}
}

// treeChanged reports whether one tree entry differs from its copy-time
// stamp.
func (v *view) treeChanged(rel string, d fs.DirEntry) bool {
	want, ok := v.tree[rel]
	info, err := d.Info()
	return !ok || err != nil || !d.Type().IsRegular() || info.Size() != want.size || !info.ModTime().Equal(want.mtime)
}

// wrapperStrays names what the agent wrote beside its working directory,
// spelled the way the agent reached it: `../<name>`. The private temp
// directory is the agent's to use.
func (v *view) wrapperStrays(out *settled) {
	entries, err := os.ReadDir(v.wrapper)
	if err != nil {
		return
	}
	for _, e := range entries {
		if p := filepath.Join(v.wrapper, e.Name()); p != v.cwd && p != v.tmp {
			out.strays = append(out.strays, up+slash+e.Name())
		}
	}
}

// expectation is the cap of the artifact at a bundle-relative path one review
// launch may write — the only files copied back — and 0 for any other path.
type expectation func(rel string) int64

// expectedArtifacts names the bundle-relative files one review launch may
// write: the launch's artifact in its output directory, plus — for a review
// written at the bundle's root — the files the Procedure has the reviewer
// write beside it (its cost record, a validation pass, verification claims,
// and one cost record per chunk the bundle's manifest lists, each under a
// small cap). Anything verdict itself writes (findings.json,
// verify-receipt.json, the launch record) is never one of them.
func expectedArtifacts(own, artifact string, chunks []string) expectation {
	main := path.Join(own, artifact)
	review := own == here && artifact == reviewFile
	return func(rel string) int64 {
		switch {
		case rel == main:
			return maxArtifactBytes
		case !review:
			return 0
		case rel == readcommon.CostFile, rel == "validation.json", rel == "verification.json":
			return maxArtifactBytes
		}
		dir, name := path.Split(rel)
		if stem, ok := strings.CutSuffix(name, ".cost.json"); ok && dir == "chunks/" && slices.Contains(chunks, stem) {
			return maxChunkCostBytes
		}
		return 0
	}
}

// warnSettled names, in one warning each, the writes the confinement
// discarded and the artifacts it refused to copy back.
func (st *state) warnSettled(agent string, s *settled) {
	if len(s.strays) > 0 {
		st.warn(core.Msgf("%s wrote outside its review artifacts: %s — none of it was copied back; "+
			"it stayed in the disposable review snapshot, which is deleted", agent, capList(s.strays)))
	}
	if len(s.refused) > 0 {
		st.warn(core.Msgf("%s left %s in the review snapshot — not copied back to the bundle",
			agent, capList(s.refused)))
	}
}

// warnDropped names, once per run, the tree's symlinks the review snapshot
// leaves out.
func (st *state) warnDropped(dropped []string) {
	if len(dropped) == 0 {
		return
	}
	msg := core.Msgf("the review snapshot leaves out the tree's %d symlink(s): %s — a review reads files, "+
		"and a link could lead out of the snapshot", len(dropped), capList(dropped))
	if !slices.Contains(st.warnings, msg) {
		st.warn(msg)
	}
}

// capList joins items, naming at most shownStrays of them.
func capList(items []string) string {
	if len(items) <= shownStrays {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:shownStrays], ", ") + fmt.Sprintf(", and %d more", len(items)-shownStrays)
}

// errNoWorkspace is a review launch reached without its execution's
// workspace: a wiring bug, refused rather than run in the checkout.
var errNoWorkspace = jq.NewErr("no review snapshot for this launch")

// spawnReview runs one review launch in its own view and copies its
// artifacts back. A view that cannot be made refuses the launch as one that
// never started: a review never falls back to the checkout.
func (st *state) spawnReview(ctx context.Context, args *launchArgs, argv []string, tap *agentusage.Tap) (*core.Result, error) {
	bundle, own, err := reviewDirs(&args.target)
	if err != nil {
		return nil, &core.StartError{Err: err}
	}
	if st.ws == nil {
		return nil, &core.StartError{Err: errNoWorkspace}
	}
	expect := expectedArtifacts(own, args.target.artifact, readcommon.ManifestChunks(bundle))
	v, err := st.ws.open(ctx, bundle, own, expect, args.timeout)
	if err != nil {
		return nil, st.viewFailure(ctx, err)
	}
	defer v.closeRoot()
	st.warnDropped(v.dropped)
	res, err := st.spawnBoxed(ctx, args, argv, tap, v)
	if res != nil {
		s := v.settle(bundle, expect)
		st.warnSettled(args.def.Name, &s)
	}
	if rerr := v.discard(); rerr != nil {
		st.warn(fmt.Sprintf("review snapshot cleanup: %s was not fully removed (%v); remove it by hand", v.wrapper, rerr))
	}
	return res, err
}

// spawnBoxed runs one review launch in its view, wrapped in the execution's
// OS sandbox when it has one. A sandbox that cannot be built for the launch
// refuses it as never started: a sandboxed execution never falls back to a
// bare launch.
func (st *state) spawnBoxed(ctx context.Context, args *launchArgs, argv []string, tap *agentusage.Tap, v *view,
) (*core.Result, error) {
	env := hostsandbox.OverrideEnv(stripSocketEnv(v.env(st.childEnv())), tempEnv(&args.def, v.tmp))
	if st.box == nil && core.StartsHostProcesses(st.commander()) {
		return nil, &core.StartError{Err: errNoBox}
	}
	env, err := st.throwawayHomes(&args.def, v, env)
	if err != nil {
		return nil, err
	}
	if !st.box.sandboxed() {
		return st.spawnAt(ctx, args.bin, argv, args.timeout, tap, v.cwd, env, false)
	}
	env = hostsandbox.OverrideEnv(env, st.box.trustEnv(env))
	launch, err := st.box.launch(v.cwd)
	if err != nil {
		return nil, &core.StartError{Err: jq.NewErrf("could not build its review sandbox (%v) — nothing ran", err)}
	}
	defer st.dropLaunch(launch)
	wrapped, err := launch.box.wrap(v.wrapper, args.bin, resolvedArgv(args, argv))
	if err != nil {
		return nil, &core.StartError{Err: jq.NewErrf("could not build its review sandbox (%v) — nothing ran", err)}
	}
	// A session of its own: the sandboxed agent has no controlling terminal
	// to push input into (bwrap's --new-session does the same on Linux).
	return st.spawnAt(ctx, "", wrapped, args.timeout, tap, v.cwd, env, true)
}

// resolvedArgv names the binary the resolution found in place of the bare
// name: the wrapper would resolve that name again against the child's PATH,
// whose relative entries reach into the reviewed snapshot.
func resolvedArgv(args *launchArgs, argv []string) []string {
	inner := slices.Clone(argv)
	if args.bin != "" {
		inner[0] = args.bin
	}
	return inner
}

// errNoBox is a host launch reached without its execution's sandbox
// decision: a wiring bug, refused rather than run bare.
var errNoBox = jq.NewErr("no review sandbox decision for this launch")

// reviewDirs is a review launch's bundle and its output directory relative
// to the bundle, in slash form ("." for the bundle itself).
func reviewDirs(target *launchTarget) (bundle, own string, err error) {
	bundle = target.evidence
	if bundle == "" {
		bundle = target.output
	}
	own, err = filepath.Rel(bundle, target.output)
	if err != nil || own == up || strings.HasPrefix(own, up+string(filepath.Separator)) {
		return "", "", jq.NewErrf("its output %s is outside the bundle", target.output)
	}
	return bundle, filepath.ToSlash(own), nil
}

// viewFailure is the start error a view that could not be made reports.
func (st *state) viewFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &core.StartError{Err: ctx.Err()}
	}
	return &core.StartError{Err: jq.NewErrf("could not make its review snapshot of %s (%v) — a review "+
		"runs only in a disposable snapshot of the reviewed tree, never in your checkout; nothing ran",
		contenttree.Resolved(st.workDir()), err)}
}

// closeWorkspace removes the execution's snapshot, warning when any of it
// survives so a stranded directory is visible.
func (st *state) closeWorkspace(context.Context) {
	if st.ws == nil {
		return
	}
	if err := st.ws.close(); err != nil {
		st.warn(fmt.Sprintf("review snapshot cleanup: the snapshot was not fully removed (%v); "+
			"a later run sweeps it", err))
	}
}
