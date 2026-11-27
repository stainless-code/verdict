package run

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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
// agent file under.
const maxArtifactBytes = 32 << 20

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

// The reasons an artifact is not copied back, as its warning names them.
const (
	whyUnreadable = "unreadable"
	whyOversized  = "over 32 MiB"
)

// workspace is one execution's reviewed tree: snapshotted once, on the
// execution's first review launch, and never handed to an agent itself. Each
// launch works in its own copy of it (a view), so no launch can change what
// a later one reads. Copies, not hard links: a link shares the file, and an
// agent running as root ignores a read-only bit, so one launch could rewrite
// the tree every later launch reads.
type workspace struct {
	err      error
	deadline time.Time
	tmp      string
	tree     string
	name     string
	git      contenttree.Git
	mu       sync.Mutex
	taken    bool
	// copied marks a tree outside any git checkout, snapshotted by a plain
	// copy (takeCopy).
	copied bool
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
	return &workspace{
		git:      contenttree.Git{Cmdr: st.commander(), Root: root, Env: st.req.Environ},
		deadline: time.Now().Add(time.Duration(2*rounds+2)*spawn + snapshotSlack),
		name:     viewName(root),
	}
}

// viewName names the view's tree directory after the reviewed checkout, so
// the agent's working directory still reads as that project.
func viewName(root string) string {
	name := filepath.Base(contenttree.Resolved(root))
	if name == "" || name == here || name == up || strings.ContainsAny(name, `/\:`) {
		return "tree"
	}
	return name
}

// canonical takes the execution's snapshot on the first call and returns it
// on every call: concurrent finders wait for the one snapshot.
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
// under the temp root verify validates: never inside the checkout.
func (w *workspace) take(ctx context.Context) (string, error) {
	tmp, err := contenttree.TempRoot(w.git.Root)
	if err != nil {
		return "", jq.Sealed("choose the snapshot directory", err)
	}
	w.tmp = tmp
	if !insideGitCheckout(w.git.Root) {
		w.copied = true
		return w.takeCopy()
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
	contenttree.SweepSnapshots(gctx, w.git, tmp, time.Now())
	dir, _, err := w.git.SnapshotUnder(gctx, tmp, w.deadline)
	if err != nil {
		return "", jq.Sealed("snapshot the reviewed tree", err)
	}
	return dir, nil
}

// insideGitCheckout reports whether root or a directory above it holds a
// .git entry, the way git itself finds a checkout.
func insideGitCheckout(root string) bool {
	for dir := contenttree.Resolved(root); ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}

// takeCopy snapshots a tree outside any git checkout — the temp root a
// `demo` of a pull request URL reviews in — by copying it as it stands,
// verdict's state left out: there is no index to name content by, and the
// launch is confined to its copy all the same. A tree inside a checkout
// always takes the contenttree snapshot, and a failure there refuses.
func (w *workspace) takeCopy() (string, error) {
	dir, err := contenttree.NewSnapshotDir(w.git.Root, w.tmp, w.deadline)
	if err != nil {
		return "", jq.Sealed("create the snapshot directory", err)
	}
	src := contenttree.Resolved(w.git.Root)
	copied := walkRel(src, func(p, rel string, d fs.DirEntry) error {
		if rel == stateRel {
			return skipEntry(d)
		}
		return copyEntry(p, filepath.Join(dir, filepath.FromSlash(rel)), rel, d, map[string]stamp{}, nil)
	})
	if copied != nil {
		return "", jq.Sealed("copy the reviewed tree", errors.Join(copied, contenttree.RemoveTree(dir)))
	}
	return dir, nil
}

// close removes the snapshot, once; a later call is a no-op.
func (w *workspace) close(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	tree := w.tree
	w.tree = ""
	var err error
	switch {
	case tree == "":
		return nil
	case w.copied:
		err = contenttree.RemoveTree(tree)
	default:
		err = w.git.RemoveCheckout(context.WithoutCancel(ctx), tree)
	}
	if err != nil {
		return jq.Sealed("remove "+tree, err)
	}
	return nil
}

// view is one launch's disposable copy of the tree. The agent starts in cwd,
// a directory of the wrapper, so even a write to `../` lands in a directory
// verdict owns and deletes.
type view struct {
	// tree and seed stamp every entry verdict copied in: the tree's
	// non-directory entries by cwd-relative path, the bundle's files by
	// bundle-relative path. A stamp that changed is a write.
	tree    map[string]stamp
	seed    map[string]stamp
	wrapper string
	cwd     string
	bundle  string
	rel     string
	// dropped names the tree's symlinks the view leaves out: each one
	// resolved outside the view.
	dropped []string
}

// stamp is what an entry looked like when verdict copied it in. sum is set
// only for an expected artifact, whose rewrite with the same size in the
// same timestamp tick must still read as a write.
type stamp struct {
	mtime time.Time
	size  int64
	mode  fs.FileMode
	sum   [sha256.Size]byte
}

// open makes one launch's view: a fresh wrapper named for the launch's own
// deadline, the tree copied into it, and the bundle at its relative path.
// own is the launch's output directory relative to the bundle ("." for the
// bundle itself); expect names the artifacts the launch may write.
func (w *workspace) open(ctx context.Context, bundle, own string, expect func(string) bool, timeout int) (*view, error) {
	tree, err := w.canonical(ctx)
	if err != nil {
		return nil, err
	}
	rel, err := bundleRel(bundle)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(time.Duration(timeout)*time.Second + agentGracePeriod + snapshotSlack)
	wrapper, err := contenttree.NewSnapshotDir(w.git.Root, w.tmp, deadline)
	if err != nil {
		return nil, jq.Sealed("create the view directory", err)
	}
	v := &view{wrapper: wrapper, cwd: filepath.Join(wrapper, w.name), rel: rel}
	v.bundle = filepath.Join(v.cwd, filepath.FromSlash(rel))
	if err := v.fill(tree, bundle, own, expect); err != nil {
		return nil, jq.Sealed("fill the view", errors.Join(err, contenttree.RemoveTree(wrapper)))
	}
	return v, nil
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
// directory of its own.
func (v *view) fill(tree, bundle, own string, expect func(string) bool) error {
	if err := v.copyTree(tree); err != nil {
		return err
	}
	v.dropEscapingLinks()
	if err := standaloneGit(v.cwd); err != nil {
		return err
	}
	if err := realDirs(v.cwd, v.rel); err != nil {
		return err
	}
	return v.copyBundle(bundle, own, expect)
}

// walkRel walks src and hands visit each entry below it with its slash-form
// path relative to src; a walk error stops the walk.
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

// gitDir is a checkout's git entry.
const gitDir = ".git"

// copyTree copies the snapshot into cwd, stamping each non-directory entry.
// Verdict's state directory stays out — the snapshot holds only its index
// version, never a bundle — and so does a tracked .verdict that is not a
// real directory, which the bundle copy below it would otherwise follow. The
// snapshot's .git stays out too: it is a linked worktree's pointer into the
// operator's repository, through whose hooks and config an agent with plain
// file tools could reach code that runs on the operator's next commit. A
// symlink whose target, read as a path, leaves the view is left out
// (escapes) and named, never followed.
func (v *view) copyTree(src string) error {
	v.tree = map[string]stamp{}
	if err := os.Mkdir(v.cwd, viewDirMode); err != nil {
		return jq.Sealed(opCreate+v.cwd, err)
	}
	return walkRel(src, func(p, rel string, d fs.DirEntry) error {
		switch {
		case rel == stateRel || rel == gitDir || (rel == config.VerdictDir && !d.IsDir()):
			return skipEntry(d)
		case d.Type()&fs.ModeSymlink != 0 && escapes(p, rel):
			v.dropped = append(v.dropped, rel)
			return nil
		}
		return copyEntry(p, filepath.Join(v.cwd, filepath.FromSlash(rel)), rel, d, v.tree, nil)
	})
}

// escapes reports whether the symlink at p, at rel below the view's root,
// names a target outside the view: an absolute or rooted target, one whose
// `..` climbs above the root, or one that cannot be read.
func escapes(p, rel string) bool {
	target, err := os.Readlink(p)
	return err != nil || targetEscapes(rel, target)
}

// targetEscapes reports whether a symlink at rel naming target leads outside
// the tree it sits in, read as a path.
func targetEscapes(rel, target string) bool {
	if filepath.IsAbs(target) || filepath.VolumeName(target) != "" || strings.HasPrefix(target, slash) ||
		strings.HasPrefix(target, `\`) {
		return true
	}
	joined := path.Join(path.Dir(rel), filepath.ToSlash(target))
	return joined == up || strings.HasPrefix(joined, up+slash)
}

// dropEscapingLinks removes every copied symlink that, resolved through the
// view's other links, still lands outside the view: a target that reads as
// inside can climb out through another link's `..`. A link that does not
// resolve stays only while the path it names is inside the view.
func (v *view) dropEscapingLinks() {
	for rel, st := range v.tree {
		if st.mode&fs.ModeSymlink != 0 && v.leadsOut(rel) {
			delete(v.tree, rel)
			v.dropped = append(v.dropped, rel)
		}
	}
	slices.Sort(v.dropped)
}

// leadsOut removes the copied symlink at rel when it resolves outside the
// view and reports whether it did.
func (v *view) leadsOut(rel string) bool {
	link := filepath.Join(v.cwd, filepath.FromSlash(rel))
	resolved, err := filepath.EvalSymlinks(link)
	return err == nil && !contenttree.Inside(v.cwd, resolved) && os.Remove(link) == nil
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
func (v *view) copyBundle(src, own string, expect func(string) bool) error {
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
		return copyEntry(p, filepath.Join(v.bundle, filepath.FromSlash(rel)), rel, d, v.seed, expect)
	})
}

// skipEntry leaves one walked entry out: a directory with everything in it.
func skipEntry(d fs.DirEntry) error {
	if d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

// realDirs creates rel's directories under root and checks each is a real
// directory, so the bundle copy can never land through a link.
func realDirs(root, rel string) error {
	dir := root
	for part := range strings.SplitSeq(rel, slash) {
		dir = filepath.Join(dir, part)
		if err := os.Mkdir(dir, viewDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
			return jq.Sealed(opCreate+dir, err)
		}
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			return jq.NewErrf("%s is not a directory in the review snapshot", dir)
		}
	}
	return nil
}

// copyEntry copies one walked entry to dst — a directory, a symlink as a
// symlink, a regular file with its mode and modification time — and stamps
// it in stamps under rel. A socket, FIFO, or device is left out. expect, when
// set, selects the files whose bytes are summed too.
func copyEntry(src, dst, rel string, d fs.DirEntry, stamps map[string]stamp, expect func(string) bool) error {
	info, err := d.Info()
	if err != nil {
		return jq.Sealed("stat "+src, err)
	}
	switch {
	case d.IsDir():
		if err := os.Mkdir(dst, info.Mode().Perm()|viewDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
			return jq.Sealed(opCreate+dst, err)
		}
	case d.Type()&fs.ModeSymlink != 0:
		return copyLink(src, dst, rel, stamps)
	case d.Type().IsRegular():
		sum, err := copyFile(src, dst, info, expect != nil && expect(rel))
		stamps[rel] = stamp{mtime: info.ModTime(), size: info.Size(), sum: sum}
		return err
	}
	return nil
}

// copyLink recreates one symlink as a symlink, never following it.
func copyLink(src, dst, rel string, stamps map[string]stamp) error {
	target, err := os.Readlink(src)
	if err != nil {
		return jq.Sealed("read link "+src, err)
	}
	if err := os.Symlink(target, dst); err != nil {
		return jq.Sealed("link "+dst, err)
	}
	stamps[rel] = stamp{mode: fs.ModeSymlink, size: int64(len(target))}
	return nil
}

// copyFile copies one regular file into a fresh path, keeping its permission
// bits and modification time, and returns its sum when asked.
func copyFile(src, dst string, info fs.FileInfo, summed bool) (sum [sha256.Size]byte, err error) {
	in, err := os.Open(src)
	if err != nil {
		return sum, jq.Sealed("open "+src, err)
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

// env is the launch environment with PWD naming the view, as a shell
// starting there would set it, no OLDPWD — the operator's own PWD names their
// checkout — and none of git's location variables (GIT_DIR and friends),
// which would point git inside the view back at the operator's repository.
func (v *view) env(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if name != "PWD" && name != "OLDPWD" && !gitcmd.Inherited(name) {
			out = append(out, kv)
		}
	}
	return append(out, "PWD="+v.cwd)
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
// the size cap.
func (v *view) settle(bundle string, expect func(string) bool) settled {
	var out settled
	v.settleBundle(bundle, expect, &out)
	v.treeStrays(&out)
	v.wrapperStrays(&out)
	slices.Sort(out.strays)
	return out
}

// settleBundle walks the bundle copy. An unchanged file is the seed verdict
// put there; a changed one is copied back when expected and named otherwise.
func (v *view) settleBundle(bundle string, expect func(string) bool, out *settled) {
	//nolint:errcheck // best effort: an entry the walk cannot read is never copied back
	_ = walkRel(v.bundle, func(p, rel string, d fs.DirEntry) error {
		shown := v.rel + slash + rel
		switch {
		case d.IsDir(), !v.changed(p, rel, d, expect(rel)):
		case !expect(rel):
			out.strays = append(out.strays, shown)
		default:
			if why := copyBack(p, filepath.Join(bundle, filepath.FromSlash(rel)), d); why != "" {
				out.refused = append(out.refused, shown+" ("+why+")")
			}
		}
		return nil
	})
}

// changed reports whether the bundle copy's file at p differs from what
// verdict seeded there: new, of another kind, size or time, or — for an
// expected artifact — other bytes.
func (v *view) changed(p, rel string, d fs.DirEntry, summed bool) bool {
	seed, ok := v.seed[rel]
	info, err := d.Info()
	if !ok || err != nil || !d.Type().IsRegular() || info.Size() != seed.size || !info.ModTime().Equal(seed.mtime) {
		return true
	}
	if !summed {
		return false
	}
	raw, err := os.ReadFile(p)
	return err != nil || sha256.Sum256(raw) != seed.sum
}

// copyBack copies one expected artifact from the view into the real bundle
// and says why it would not, "" on success. The source must be a lone
// regular file within the cap, read without following a link; the copy is
// atomic and keeps the agent's modification time, so the freshness check
// reads the agent's write, not the copy's. Its directory must already be a
// real directory of the bundle — verdict made every one a launch writes in.
func copyBack(src, dst string, d fs.DirEntry) string {
	info, err := d.Info()
	switch {
	case err != nil:
		return whyUnreadable
	case d.Type()&fs.ModeSymlink != 0:
		return "a symlink"
	case !d.Type().IsRegular():
		return "not a regular file"
	case readcommon.HasHardLinks(src, info):
		return "a hard link"
	case info.Size() > maxArtifactBytes:
		return whyOversized
	}
	raw, why := readArtifact(src)
	if why != "" {
		return why
	}
	if dirInfo, err := os.Lstat(filepath.Dir(dst)); err != nil || !dirInfo.IsDir() {
		return "its bundle directory is missing"
	}
	if err := atomic.WriteFileMode(dst, raw, roundEvidenceMode); err != nil {
		return "could not be written to the bundle"
	}
	if err := os.Chtimes(dst, info.ModTime(), info.ModTime()); err != nil {
		return "could not be stamped in the bundle"
	}
	return ""
}

// readArtifact reads one artifact without following a final link, refusing
// one that grew past the cap or turned into something else since its stat.
func readArtifact(src string) ([]byte, string) {
	root, err := os.OpenRoot(filepath.Dir(src))
	if err != nil {
		return nil, whyUnreadable
	}
	defer func() { _ = root.Close() }()
	f, err := readcommon.OpenNoFollowIn(root, filepath.Base(src))
	if err != nil {
		return nil, whyUnreadable
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, "not a regular file"
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxArtifactBytes+1))
	switch {
	case err != nil:
		return nil, whyUnreadable
	case len(raw) > maxArtifactBytes:
		return nil, whyOversized
	}
	return raw, ""
}

// treeStrays names every tree entry the agent added, changed, or removed
// outside the bundle copy. Directories are not compared: an empty directory
// carries nothing.
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
	if !ok || err != nil {
		return true
	}
	if want.mode&fs.ModeSymlink != 0 {
		return d.Type()&fs.ModeSymlink == 0
	}
	return !d.Type().IsRegular() || info.Size() != want.size || !info.ModTime().Equal(want.mtime)
}

// wrapperStrays names what the agent wrote beside its working directory,
// spelled the way the agent reached it: `../<name>`.
func (v *view) wrapperStrays(out *settled) {
	entries, err := os.ReadDir(v.wrapper)
	if err != nil {
		return
	}
	for _, e := range entries {
		if filepath.Join(v.wrapper, e.Name()) != v.cwd {
			out.strays = append(out.strays, up+slash+e.Name())
		}
	}
}

// expectedArtifacts names the bundle-relative files one review launch may
// write, which are the only files copied back: the launch's artifact in its
// output directory, plus — for a review written at the bundle's root — the
// files the Procedure has the reviewer write beside it (its cost record and
// per-chunk cost records, a validation pass, verification claims). Anything
// verdict itself writes (findings.json, verify-receipt.json, the launch
// record) is never one of them.
func expectedArtifacts(own, artifact string) func(string) bool {
	main := path.Join(own, artifact)
	review := own == here && artifact == reviewFile
	return func(rel string) bool {
		switch {
		case rel == main:
			return true
		case !review:
			return false
		case rel == readcommon.CostFile, rel == "validation.json", rel == "verification.json":
			return true
		}
		dir, name := path.Split(rel)
		return dir == "chunks/" && strings.HasPrefix(name, "chunk-") && strings.HasSuffix(name, ".cost.json")
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
// leaves out because they lead outside it.
func (st *state) warnDropped(dropped []string) {
	if len(dropped) == 0 {
		return
	}
	msg := core.Msgf("the review snapshot leaves out %d symlink(s) that lead outside it: %s — "+
		"a write through one would leave the snapshot", len(dropped), capList(dropped))
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
	expect := expectedArtifacts(own, args.target.artifact)
	v, err := st.ws.open(ctx, bundle, own, expect, args.timeout)
	if err != nil {
		return nil, st.viewFailure(ctx, err)
	}
	st.warnDropped(v.dropped)
	res, err := st.spawnBoxed(ctx, args, argv, tap, v)
	if res != nil {
		s := v.settle(bundle, expect)
		st.warnSettled(args.def.Name, &s)
	}
	if rerr := contenttree.RemoveTree(v.wrapper); rerr != nil {
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
	env := v.env(st.childEnv())
	if st.box == nil && core.StartsHostProcesses(st.commander()) {
		return nil, &core.StartError{Err: errNoBox}
	}
	if !st.box.sandboxed() {
		return st.spawn(ctx, argv, args.timeout, tap, v.cwd, env)
	}
	w, err := st.box.wrap(v.wrapper, resolvedArgv(args, argv), env)
	if err != nil {
		return nil, &core.StartError{Err: jq.NewErrf("could not build its review sandbox (%v) — nothing ran", err)}
	}
	defer st.removePrivate(w.private)
	return st.spawn(ctx, w.argv, args.timeout, tap, v.cwd, w.env)
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
func (st *state) closeWorkspace(ctx context.Context) {
	if st.ws == nil {
		return
	}
	if err := st.ws.close(ctx); err != nil {
		st.warn(fmt.Sprintf("review snapshot cleanup: the snapshot was not fully removed (%v); "+
			"a later run sweeps it", err))
	}
}
