package contenttree_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/contenttree"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// commandConfig sets git configuration at command scope for the test, after
// the pinned testenv entries (core.autocrlf=false among them), so it holds
// for the fixture's git and the code under test alike, as an operator's own
// setting would.
func commandConfig(t *testing.T, kv ...string) {
	t.Helper()
	n, err := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	if err != nil {
		t.Fatalf("GIT_CONFIG_COUNT: %v", err)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		t.Setenv("GIT_CONFIG_KEY_"+strconv.Itoa(n), kv[i])
		t.Setenv("GIT_CONFIG_VALUE_"+strconv.Itoa(n), kv[i+1])
		n++
	}
	t.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(n))
}

// checkoutAfresh deletes rels and checks them out again, so git writes them
// with the conversions now in force, as a fresh clone would.
func checkoutAfresh(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	git(t, root, append([]string{"checkout", "--"}, rels...)...)
}

// addedTree is the tree `git add -u` makes of root's checkout, in a copy of
// its index: the independent oracle for what a commit of it would hold.
func addedTree(t *testing.T, root string) string {
	t.Helper()
	index := copyIndex(t, root)
	runIndexed(t, root, index, "add", "-u")
	return runIndexed(t, root, index, "write-tree")
}

// copyIndex copies root's index to a fresh path with its mtime: git trusts
// an entry's stat data only when the entry is older than the index file, so
// a copy stamped later would let `git add` skip a same-size edit made in the
// index's second.
func copyIndex(t *testing.T, root string) string {
	t.Helper()
	index := filepath.Join(t.TempDir(), "index")
	src := filepath.Join(root, ".git", "index")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(index, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	return index
}

// runIndexed runs git in root against index and returns its trimmed stdout.
func runIndexed(t *testing.T, root, index string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index)
	cmd.WaitDelay = 10 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return string(bytes.TrimSpace(out))
}

// TestCRLFCheckoutNamesTheCommittedTree pins Git for Windows' default: with
// core.autocrlf=true a commit of LF files checks out as CRLF, and the
// content must still be named as that commit's tree, so a PR's VERIFIED
// claim and a group run can bind. The snapshot holds the committed LF bytes.
// A file committed with CRLF stays CRLF (git leaves an indexed CRLF alone),
// and a `binary` or `-text` file is named byte for byte.
func TestCRLFCheckoutNamesTheCommittedTree(t *testing.T) {
	root, _ := repo(t)
	committed := map[string]string{
		"lf.txt":         "one\ntwo\n",
		"win.bat":        "@echo off\r\nexit 0\r\n",
		"blob.dat":       "x\r\ny\r\n",
		"raw.txt":        "r\r\ns\r\n",
		".gitattributes": "*.dat binary\nraw.txt -text\n",
		"dir/nested.go":  "package dir\n",
	}
	for rel, body := range committed {
		write(t, root, rel, body)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "lf")
	head := git(t, root, "rev-parse", "HEAD^{tree}")
	commandConfig(t, "core.autocrlf", "true")
	checkoutAfresh(t, root, "lf.txt", "app.go", "dir/nested.go", "win.bat", "blob.dat", "raw.txt")
	// The fixture is the case under test only if git wrote CRLF.
	requireBytes(t, filepath.Join(root, "lf.txt"), "one\r\ntwo\r\n")
	g := contenttree.Git{Cmdr: coreexec.Runner{}, Root: root}
	if got := mustTree(t, g); got != head {
		t.Fatalf("CRLF checkout named %s, want the commit's tree %s", got, head)
	}
	snap := snapshot(t, g)
	if snap.content.Tree != head {
		t.Fatalf("CRLF snapshot named %s, want the commit's tree %s", snap.content.Tree, head)
	}
	for rel, body := range committed {
		requireBytes(t, filepath.Join(snap.dir, filepath.FromSlash(rel)), body)
	}
	requireBytes(t, filepath.Join(snap.dir, "app.go"), "package app\n")
	// An edit saved with CRLF is named as `git add` would commit it.
	write(t, root, "lf.txt", "one\r\nthree\r\n")
	write(t, root, "blob.dat", "x\r\nz\r\n")
	edited := mustTree(t, g)
	if want := addedTree(t, root); edited != want {
		t.Errorf("edited CRLF checkout named %s, want git add's %s", edited, want)
	}
	if blob := gitRaw(t, root, "cat-file", "blob", edited+":lf.txt"); blob != "one\nthree\n" {
		t.Errorf("edited lf.txt named as %q, want LF", blob)
	}
	if blob := gitRaw(t, root, "cat-file", "blob", edited+":blob.dat"); blob != "x\r\nz\r\n" {
		t.Errorf("edited binary named as %q, want its bytes", blob)
	}
}

// TestEolAttributeCheckoutNamesTheCommittedTree pins the attribute route to
// CRLF on disk: `text eol=crlf` checks an LF commit out as CRLF on every
// platform, with core.autocrlf off, and the content is still the commit's,
// snapshotted as LF. A UTF-16 working-tree-encoding file is named by its
// UTF-8 blob but snapshotted as git's checkout writes it, UTF-16, so a
// command reads what a checkout holds; its re-check cleans it back.
func TestEolAttributeCheckoutNamesTheCommittedTree(t *testing.T) {
	root, g := repo(t)
	write(t, root, ".gitattributes", "*.txt text eol=crlf\nwide.txt working-tree-encoding=UTF-16LE\n")
	write(t, root, "notes.txt", "a\nb\n")
	write(t, root, "wide.txt", "h\x00i\x00\r\x00\n\x00")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "eol")
	head := git(t, root, "rev-parse", "HEAD^{tree}")
	checkoutAfresh(t, root, "notes.txt", "wide.txt")
	requireBytes(t, filepath.Join(root, "notes.txt"), "a\r\nb\r\n")
	if got := mustTree(t, g); got != head {
		t.Fatalf("eol=crlf checkout named %s, want the commit's tree %s", got, head)
	}
	snap := snapshot(t, g)
	if snap.content.Tree != head {
		t.Fatalf("eol=crlf snapshot named %s, want the commit's tree %s", snap.content.Tree, head)
	}
	requireBytes(t, filepath.Join(snap.dir, "notes.txt"), "a\nb\n")
	requireBytes(t, filepath.Join(snap.dir, "wide.txt"), "h\x00i\x00\r\x00\n\x00")
	if blob := gitRaw(t, root, "cat-file", "blob", head+":wide.txt"); blob != "hi\n" {
		t.Errorf("wide.txt committed as %q, want its UTF-8 form", blob)
	}
}

// TestIdentPathIsSnapshottedAsCheckoutWritesIt pins the false VERIFIED the
// committed form allowed: with `ident`, a checkout holds `$Id: <blob> $`,
// so a command that greps for the collapsed `$Id$` must fail in the
// snapshot as it would anywhere. The tree is still the commit's.
func TestIdentPathIsSnapshottedAsCheckoutWritesIt(t *testing.T) {
	root, g := repo(t)
	write(t, root, ".gitattributes", "id.txt ident\n")
	write(t, root, "id.txt", "hello $Id$ world\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "ident")
	head := git(t, root, "rev-parse", "HEAD^{tree}")
	checkoutAfresh(t, root, "id.txt")
	blob := git(t, root, "rev-parse", "HEAD:id.txt")
	checkedOut := "hello $Id: " + blob + " $ world\n"
	requireBytes(t, filepath.Join(root, "id.txt"), checkedOut)
	snap := snapshot(t, g)
	if snap.content.Tree != head {
		t.Fatalf("ident snapshot named %s, want the commit's tree %s", snap.content.Tree, head)
	}
	requireBytes(t, filepath.Join(snap.dir, "id.txt"), checkedOut)
}

// TestMissingAttributesFileIsReadFromItsIndexedCopy pins the attribute
// sources: a tracked .gitattributes deleted from disk still applies, read
// from the index, as `git add` reads it — here `text`, which cleans the
// CRLF an edit saved.
func TestMissingAttributesFileIsReadFromItsIndexedCopy(t *testing.T) {
	root, g := repo(t)
	write(t, root, ".gitattributes", "*.txt text\n")
	write(t, root, "a.txt", "x\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "attrs")
	if err := os.Remove(filepath.Join(root, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "a.txt", "x\r\ny\r\n")
	tree := mustTree(t, g)
	if blob := gitRaw(t, root, "cat-file", "blob", tree+":a.txt"); blob != "x\ny\n" {
		t.Errorf("a.txt named as %q, want the indexed .gitattributes' `text` to clean it", blob)
	}
	if want := addedBlob(t, root, "a.txt"); git(t, root, "rev-parse", tree+":a.txt") != want {
		t.Errorf("a.txt named differently from `git add a.txt`'s %s", want)
	}
}

// addedBlob is the blob `git add rel` makes, in a copy of root's index.
func addedBlob(t *testing.T, root, rel string) string {
	t.Helper()
	index := copyIndex(t, root)
	runIndexed(t, root, index, "add", "--", rel)
	return runIndexed(t, root, index, "rev-parse", ":"+rel)
}

// TestFailedCleanNamesGitsReason pins the refusal text under verify's
// combined-capture watchdog, which delivers git's stderr in Stdout: an
// encoding git cannot convert names git's own fatal line, not an empty
// reason.
func TestFailedCleanNamesGitsReason(t *testing.T) {
	root, _ := repo(t)
	write(t, root, ".gitattributes", "x.txt working-tree-encoding=NO-SUCH-ENCODING\n")
	write(t, root, "x.txt", "hi\n")
	git(t, root, "add", "-N", ".gitattributes", "x.txt")
	g := contenttree.Git{Cmdr: coreexec.Watchdog{}, Root: root}
	_, err := g.Worktree(t.Context())
	want := "git update-index exited 128: fatal: failed to encode 'x.txt' from NO-SUCH-ENCODING to UTF-8"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

// lfsPointer is a Git LFS pointer file, as a commit stores one.
const lfsPointer = "version https://git-lfs.github.com/spec/v1\n" +
	"oid sha256:4d7a214614ab2935c943f9e0ff69d22eadbb8f32b1258daaa5e2ca24d17e2393\nsize 12\n"

// TestLFSContentRunsNoGitLFSAndIsReported pins Git LFS: with `git lfs
// install`'s configuration and a git-lfs on PATH that leaves a marker,
// naming and snapshotting run it never, name the smudged file by its bytes
// (the pointer cannot be re-derived without git-lfs, so the tree is not the
// commit's), and report the path under the lfs driver. A pointer left
// unsmudged, or one whose index entry differs but whose bytes are HEAD's,
// is named as the commit names it and not reported.
func TestLFSContentRunsNoGitLFSAndIsReported(t *testing.T) {
	root, _ := repo(t)
	write(t, root, ".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n")
	write(t, root, "big.bin", lfsPointer)
	write(t, root, "kept.bin", lfsPointer)
	write(t, root, "restored.bin", lfsPointer)
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "lfs")
	head := git(t, root, "rev-parse", "HEAD^{tree}")
	// A change staged and then undone on disk: the index differs from the
	// file, but the file is HEAD's, so nothing changes the outcome there.
	write(t, root, "restored.bin", "staged\n")
	git(t, root, "add", "restored.bin")
	write(t, root, "restored.bin", lfsPointer)
	shim, markers := t.TempDir(), t.TempDir()
	write(t, shim, "git-lfs", "#!/bin/sh\ntouch '"+filepath.ToSlash(filepath.Join(markers, "git-lfs"))+"'\ncat\n")
	if err := os.Chmod(filepath.Join(shim, "git-lfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	git(t, root, "config", "filter.lfs.clean", "git-lfs clean -- %f")
	git(t, root, "config", "filter.lfs.smudge", "git-lfs smudge -- %f")
	git(t, root, "config", "filter.lfs.process", "git-lfs filter-process")
	git(t, root, "config", "filter.lfs.required", "true")
	write(t, root, "big.bin", "real content")
	g := contenttree.Git{Cmdr: coreexec.Runner{}, Root: root}
	tree := mustTree(t, g)
	snap := snapshot(t, g)
	if left, err := os.ReadDir(markers); err != nil || len(left) != 0 {
		t.Fatalf("git-lfs ran: markers %v (%v)", left, err)
	}
	if tree == head || snap.content.Tree != tree {
		t.Errorf("trees named %s, snapshot %s, commit %s: want the smudged content named, not the commit", tree, snap.content.Tree, head)
	}
	requireBytes(t, filepath.Join(snap.dir, "big.bin"), "real content")
	requireBytes(t, filepath.Join(snap.dir, "kept.bin"), lfsPointer)
	want := []contenttree.Filtered{{Driver: "lfs", Paths: []string{"big.bin"}}}
	if !slices.EqualFunc(snap.content.Filtered, want, equalFiltered) {
		t.Errorf("Filtered = %+v, want %+v", snap.content.Filtered, want)
	}
	// The shim is live: git's own add runs it, so the marker check above can fail.
	cmd := exec.CommandContext(t.Context(), "git", "add", "big.bin")
	cmd.Dir = root
	cmd.WaitDelay = 10 * time.Second
	_ = cmd.Run() //nolint:errcheck // the shim speaks no filter protocol; only the marker matters
	if _, err := os.Stat(filepath.Join(markers, "git-lfs")); err != nil {
		t.Errorf("git add did not run the git-lfs shim (%v): the no-marker check proves nothing", err)
	}
}
