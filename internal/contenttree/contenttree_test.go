package contenttree_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestMain isolates every git child from the inherited GIT_DIR, index, and
// config, so a fixture command can never act on the developer's checkout.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(gitRaw(t, root, args...))
}

// gitRaw runs git in root and returns its stdout as is.
func gitRaw(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = root
	cmd.WaitDelay = 10 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return string(out)
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// repo is a committed checkout: a tracked file and a .gitignore.
func repo(t *testing.T) (string, contenttree.Git) {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "t@example.com")
	git(t, root, "config", "user.name", "t")
	write(t, root, "app.go", "package app\n")
	write(t, root, ".gitignore", "build/\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "c")
	return root, contenttree.Git{Cmdr: coreexec.Runner{}, Root: root}
}

// TestWorktreeNamesTheCheckoutContent pins what the tree id covers — the
// index paths, as a working-tree bundle's diff does: a clean checkout is
// HEAD's tree; an unstaged edit and an intent-to-add file change it; an
// untracked file, an ignored file, and verdict's own state do not; and
// computing it leaves the user's index byte for byte as it was.
func TestWorktreeNamesTheCheckoutContent(t *testing.T) {
	root, g := repo(t)
	ctx := context.Background()
	head := git(t, root, "rev-parse", "HEAD^{tree}")
	clean := mustTree(t, g)
	if clean != head {
		t.Fatalf("clean tree = %s, want HEAD's %s", clean, head)
	}
	write(t, root, "build/out.bin", "ignored\n")
	write(t, root, ".verdict/state/7/patch.json", "{}\n")
	write(t, root, ".verdict/loop/1/round.json", "{}\n")
	write(t, root, ".verdict/.gitignore", "/state/\n")
	if got := mustTree(t, g); got != head {
		t.Fatalf("tree with only ignored files and verdict state = %s, want %s", got, head)
	}
	indexBefore := readIndex(t, root)
	write(t, root, "app.go", "package app // edited\n")
	edited := mustTree(t, g)
	write(t, root, "new.go", "package app\n")
	if untracked := mustTree(t, g); edited == head || untracked != edited {
		t.Fatalf("trees head=%s edited=%s untracked=%s, want an edit to count and an untracked file not to",
			head, edited, untracked)
	}
	if !bytes.Equal(indexBefore, readIndex(t, root)) {
		t.Fatal("computing the tree rewrote the user's index")
	}
	git(t, root, "add", "-N", "new.go")
	if intent := mustTree(t, g); intent == edited {
		t.Fatalf("an intent-to-add file left the tree at %s", intent)
	}
	if _, err := g.Commit(ctx, "0123456789012345678901234567890123456789"); !errors.Is(err, contenttree.ErrUnresolved) {
		t.Fatalf("unknown commit: err = %v, want ErrUnresolved", err)
	}
}

// TestWorktreeOnAnUnbornBranch pins the first-commit case: there is no HEAD,
// so the tree is the staged paths' bytes alone, and verdict's state stays
// out even when staged.
func TestWorktreeOnAnUnbornBranch(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "app.go", "package app\n")
	write(t, root, "notes.txt", "untracked\n")
	write(t, root, ".verdict/state/7/patch.json", "{}\n")
	git(t, root, "add", "app.go")
	tree := mustTree(t, contenttree.Git{Cmdr: coreexec.Runner{}, Root: root})
	if files := git(t, root, "ls-tree", "-r", "--name-only", tree); files != "app.go" {
		t.Fatalf("unborn tree holds %q, want only app.go", files)
	}
}

// TestSnapshotCopiesTheContent pins that the snapshot a command runs in is
// exactly the named tree, as a working git checkout: the edited and
// untracked files are there, the ignored file and verdict state are not, git
// sees a clean tree at that commit, and RemoveCheckout leaves no worktree
// registered.
func TestSnapshotCopiesTheContent(t *testing.T) {
	root, g := repo(t)
	write(t, root, "app.go", "package app // edited\n")
	write(t, root, "new.go", "package app\n")
	write(t, root, "build/out.bin", "ignored\n")
	write(t, root, ".verdict/state/7/patch.json", "{}\n")
	git(t, root, "add", "-N", "new.go")
	want := mustTree(t, g)
	dest := filepath.Join(t.TempDir(), "snap")
	ctx := context.Background()
	content, err := g.Snapshot(ctx, dest)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	tree := content.Tree
	if tree != want {
		t.Errorf("snapshot tree = %s, want the checkout's %s", tree, want)
	}
	requireSnapshotFiles(t, dest)
	if got := git(t, dest, "rev-parse", "HEAD^{tree}"); got != tree {
		t.Errorf("snapshot HEAD tree = %s, want %s", got, tree)
	}
	if status := git(t, dest, "status", "--porcelain"); status != "" {
		t.Errorf("snapshot status = %q, want clean", status)
	}
	if err := g.RemoveCheckout(ctx, dest); err != nil {
		t.Fatalf("RemoveCheckout: %v", err)
	}
	requireNoTrace(t, root)
}

// TestSnapshotCleansLikeGitAddAndRunsNoRepoCode pins the two properties a
// content receipt rests on, on a checkout set up to defeat both: `* text=auto`
// cleans line endings, a `filter=mark` attribute names a required
// clean/smudge driver on an `ident` path the snapshot writes as checkout
// would, and fsmonitor and post-checkout name programs. Every
// one of those programs writes a marker. Naming and snapshotting must clean
// the working tree's bytes as git's built-in conversions do — CRLF to LF —
// without running the driver, report the path it would have handled, and
// leave no marker behind.
func TestSnapshotCleansLikeGitAddAndRunsNoRepoCode(t *testing.T) {
	root, g := repo(t)
	// The `ident` path is written as git's checkout writes it, and under
	// `* text=auto` that checkout's line endings follow core.eol, which
	// defaults to CRLF on Windows. This test is about cleaning and running
	// no program, so it pins the checkout's endings to LF on every host.
	commandConfig(t, "core.eol", "lf")
	markers := t.TempDir()
	armRepoCode(t, root, markers)
	write(t, root, "crlf.txt", "one\r\ntwo\r\n")
	write(t, root, "filtered.txt", "raw $Id$ bytes\r\n")
	git(t, root, "add", "-N", ".gitattributes", "crlf.txt", "filtered.txt")
	// The fixture's own git calls ran the hooks; only what follows counts.
	clearDir(t, markers)
	tree := mustTree(t, g)
	dest := snapshot(t, g)
	if dest.content.Tree != tree {
		t.Errorf("snapshot tree = %s, want the named %s", dest.content.Tree, tree)
	}
	cleaned := map[string]string{"crlf.txt": "one\ntwo\n", "filtered.txt": "raw $Id$ bytes\n"}
	for rel, body := range cleaned {
		if blob := gitRaw(t, root, "cat-file", "blob", tree+":"+rel); blob != body {
			t.Errorf("named blob %s = %q, want the cleaned %q", rel, blob, body)
		}
	}
	// filtered.txt is also `ident`, so git's checkout — with the smudge
	// driver switched off — writes it expanded.
	requireBytes(t, filepath.Join(dest.dir, "crlf.txt"), cleaned["crlf.txt"])
	expanded := "raw $Id: " + git(t, root, "rev-parse", tree+":filtered.txt") + " $ bytes\n"
	requireBytes(t, filepath.Join(dest.dir, "filtered.txt"), expanded)
	want := []contenttree.Filtered{{Driver: "mark", Paths: []string{"filtered.txt"}}}
	if !slices.EqualFunc(dest.content.Filtered, want, equalFiltered) {
		t.Errorf("Filtered = %+v, want %+v", dest.content.Filtered, want)
	}
	if left, err := os.ReadDir(markers); err != nil || len(left) != 0 {
		t.Errorf("repository-controlled code ran: markers %v (%v)", left, err)
	}
}

func equalFiltered(a, b contenttree.Filtered) bool {
	return a.Driver == b.Driver && slices.Equal(a.Paths, b.Paths)
}

// armRepoCode sets up every program the checkout could make git run while
// naming or snapshotting it, each writing a marker into markers: a required
// clean/smudge filter, an fsmonitor, and post-checkout and post-index-change
// hooks, beside `* text=auto`.
func armRepoCode(t *testing.T, root, markers string) {
	t.Helper()
	marker := func(name string) string { return filepath.ToSlash(filepath.Join(markers, name)) }
	write(t, root, ".gitattributes", "* text=auto\nfiltered.txt filter=mark ident\n")
	git(t, root, "config", "filter.mark.clean", "sh -c 'touch "+marker("clean")+"; cat'")
	git(t, root, "config", "filter.mark.smudge", "sh -c 'touch "+marker("smudge")+"; cat'")
	git(t, root, "config", "filter.mark.required", "true")
	git(t, root, "config", "core.fsmonitor", filepath.ToSlash(filepath.Join(root, "fsmonitor.sh")))
	programs := map[string]string{
		"fsmonitor.sh":             "fsmonitor",
		".git/hooks/post-checkout": "post-checkout", ".git/hooks/post-index-change": "post-index-change",
	}
	for rel, name := range programs {
		write(t, root, rel, "#!/bin/sh\ntouch "+marker(name)+"\n")
		if err := os.Chmod(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// snapped is a snapshot a test made and will remove.
type snapped struct {
	dir     string
	content contenttree.Content
}

// snapshot snapshots g's checkout into a fresh directory, removed after the
// test.
func snapshot(t *testing.T, g contenttree.Git) snapped {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "snap")
	content, err := g.Snapshot(context.Background(), dest)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	t.Cleanup(func() { removeCheckout(t, g, dest) })
	return snapped{dir: dest, content: content}
}

func removeCheckout(t *testing.T, g contenttree.Git, dest string) {
	t.Helper()
	if err := g.RemoveCheckout(context.Background(), dest); err != nil {
		t.Errorf("RemoveCheckout: %v", err)
	}
}

func requireBytes(t *testing.T, path, want string) {
	t.Helper()
	if got, err := os.ReadFile(path); err != nil || string(got) != want {
		t.Errorf("%s = %q (%v), want %q", path, got, err, want)
	}
}

// TestSnapshotReportsWhatItLeavesOut pins the warnings' inputs: an untracked
// file, the top-level entries .gitignore excludes, and a submodule are named,
// and none of them is copied — the submodule is an empty directory.
func TestSnapshotReportsWhatItLeavesOut(t *testing.T) {
	root, g := repo(t)
	write(t, root, "new.go", "package app\n")
	write(t, root, "build/out.bin", "ignored\n")
	head := git(t, root, "rev-parse", "HEAD")
	git(t, root, "update-index", "--add", "--cacheinfo", "160000,"+head+",sub")
	snap := snapshot(t, g)
	c := snap.content
	if !slices.Equal(c.Untracked, []string{"new.go"}) || !slices.Equal(c.Ignored, []string{"build"}) ||
		!slices.Equal(c.Gitlinks, []string{"sub"}) {
		t.Errorf("left out = untracked %q ignored %q gitlinks %q, want new.go, build, sub", c.Untracked, c.Ignored, c.Gitlinks)
	}
	for _, rel := range []string{"new.go", "build"} {
		if _, err := os.Stat(filepath.Join(snap.dir, rel)); !os.IsNotExist(err) {
			t.Errorf("%s was copied into the snapshot (%v)", rel, err)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(snap.dir, "sub")); err != nil || len(entries) != 0 {
		t.Errorf("submodule dir = %v (%v), want an empty directory", entries, err)
	}
}

// tamperingCommander runs git for real, but deletes a file from the snapshot
// as the snapshot's index is read — standing in for anything that rewrites
// the snapshot between the copy and the command.
type tamperingCommander struct{ dest, victim string }

func (c tamperingCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if root, args, ok := gitcmd.Split(cmd.Argv); ok && root == c.dest && args[0] == "read-tree" {
		_ = os.Remove(filepath.Join(c.dest, c.victim))
	}
	return coreexec.Runner{}.Run(ctx, cmd)
}

// TestSnapshotRefusesContentItDoesNotHold pins the re-hash: a snapshot whose
// files no longer make the tree it was made from fails, so no receipt can
// name content the command did not run on.
func TestSnapshotRefusesContentItDoesNotHold(t *testing.T) {
	root, _ := repo(t)
	dest := filepath.Join(t.TempDir(), "snap")
	g := contenttree.Git{Cmdr: tamperingCommander{dest: dest, victim: "app.go"}, Root: root}
	_, err := g.Snapshot(context.Background(), dest)
	if err == nil || !strings.Contains(err.Error(), "not the") {
		t.Fatalf("err = %v, want the snapshot refused for holding another tree", err)
	}
	removeCheckout(t, g, dest)
}

// clearDir empties dir.
func clearDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
}

// requireSnapshotFiles checks the snapshot holds the edited and untracked
// files and neither the ignored file nor verdict state.
func requireSnapshotFiles(t *testing.T, dest string) {
	t.Helper()
	for rel, want := range map[string]string{"app.go": "package app // edited\n", "new.go": "package app\n"} {
		if got, err := os.ReadFile(filepath.Join(dest, rel)); err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want %q", rel, got, err, want)
		}
	}
	for _, rel := range []string{"build/out.bin", ".verdict/state/7/patch.json"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); !os.IsNotExist(err) {
			t.Errorf("%s exists in the snapshot (%v)", rel, err)
		}
	}
}

// requireNoTrace checks a removed snapshot left no worktree or branch.
func requireNoTrace(t *testing.T, root string) {
	t.Helper()
	if list := git(t, root, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Errorf("worktrees after removal:\n%s", list)
	}
	if branches := git(t, root, "branch", "--list"); strings.Count(branches, "\n") != 0 {
		t.Errorf("a snapshot created a branch: %q", branches)
	}
}

func readIndex(t *testing.T, root string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustTree(t *testing.T, g contenttree.Git) string {
	t.Helper()
	tree, err := g.Worktree(context.Background())
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	return tree
}

// TestSnapshotIgnoresInheritedConfigParameters pins that the hook-free
// configuration holds against GIT_CONFIG_PARAMETERS, which outranks
// GIT_CONFIG_COUNT and which git itself exports to the hooks it runs under
// `git -c`: with it pointing core.hooksPath at a .husky directory holding
// index and checkout hooks, naming and snapshotting run none of them.
func TestSnapshotIgnoresInheritedConfigParameters(t *testing.T) {
	root, g := repo(t)
	markers := t.TempDir()
	for _, hook := range []string{"post-index-change", "post-checkout"} {
		write(t, root, ".husky/"+hook, "#!/bin/sh\ntouch "+filepath.ToSlash(filepath.Join(markers, hook))+"\n")
		if err := os.Chmod(filepath.Join(root, ".husky", hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, root, "app.go", "package app // edited\n")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.hooksPath'='.husky'")
	mustTree(t, g)
	snapshot(t, g)
	if left, err := os.ReadDir(markers); err != nil || len(left) != 0 {
		t.Errorf("hooks ran through GIT_CONFIG_PARAMETERS: %v (%v)", left, err)
	}
}

// TestSnapshotIgnoresAnInheritedIndex pins that a git location variable a
// parent exported cannot retarget a content read: with GIT_INDEX_FILE naming
// the source checkout's own index (as inside a git hook), naming and
// snapshotting leave that index byte for byte as it was — the snapshot's
// read-tree writes only its own.
func TestSnapshotIgnoresAnInheritedIndex(t *testing.T) {
	root, g := repo(t)
	write(t, root, "new.go", "package app\n")
	git(t, root, "add", "new.go")
	before := readIndex(t, root)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(root, ".git", "index"))
	mustTree(t, g)
	snap := snapshot(t, g)
	requireBytes(t, filepath.Join(snap.dir, "new.go"), "package app\n")
	if !bytes.Equal(before, readIndex(t, root)) {
		t.Fatal("an inherited GIT_INDEX_FILE let the snapshot rewrite the user's index")
	}
}

// TestStateIsExcludedWhateverItsCase proves verdict's state keeps its index
// version however its path is cased: on APFS and NTFS `.Verdict/state` is the
// same directory as `.verdict/state`, so an edit there is never content.
func TestStateIsExcludedWhateverItsCase(t *testing.T) {
	t.Parallel()
	root, g := repo(t)
	write(t, root, ".Verdict/State/42/validation.json", "{}\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "state")
	head := git(t, root, "rev-parse", "HEAD^{tree}")
	write(t, root, ".Verdict/State/42/validation.json", "{\"forged\":true}\n")
	if got := mustTree(t, g); got != head {
		t.Errorf("tree = %s after a state edit, want HEAD's %s", got, head)
	}
}
