package fetchcore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/hunks"
)

// scanFixture writes one file under dir, creating parents, and returns its
// repository-relative slash path.
func scanFixture(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestScanDiffSynthesizesAddedFileSections pins the exact diff bytes a scan
// hands the rest of the pipeline: git's added-file shape, the no-newline
// marker, the binary marker, and the empty file's header-only section. The
// expected text is literal so a reformatted section fails here.
func TestScanDiffSynthesizesAddedFileSections(t *testing.T) {
	root := physicalTempDir(t)
	scanFixture(t, root, "a.txt", "one\ntwo\n")
	scanFixture(t, root, "b.bin", "x\x00y")
	scanFixture(t, root, "c.txt", "no-newline")
	scanFixture(t, root, "empty.txt", "")
	files := []scanFile{
		{rel: "a.txt", size: 8},
		{rel: "b.bin", size: 3},
		{rel: "c.txt", size: 10},
		{rel: "empty.txt", size: 0},
	}
	diff, listed, numstat, ref := scanDiff(context.Background(), root, files)
	if ref != nil {
		t.Fatalf("scanDiff refusal: %v", ref)
	}
	wantDiff := `diff --git a/a.txt b/a.txt
new file mode 100644
--- /dev/null
+++ b/a.txt
@@ -0,0 +1,2 @@
+one
+two
diff --git a/b.bin b/b.bin
new file mode 100644
Binary files /dev/null and b/b.bin differ
diff --git a/c.txt b/c.txt
new file mode 100644
--- /dev/null
+++ b/c.txt
@@ -0,0 +1,1 @@
+no-newline
\ No newline at end of file
diff --git a/empty.txt b/empty.txt
new file mode 100644
`
	if diff != wantDiff {
		t.Errorf("scan diff =\n%s\nwant\n%s", diff, wantDiff)
	}
	if want := "a.txt\nb.bin\nc.txt\nempty.txt"; listed != want {
		t.Errorf("listed = %q; want %q", listed, want)
	}
	wantNumstat := "2\t0\ta.txt\n-\t-\tb.bin\n1\t0\tc.txt\n0\t0\tempty.txt"
	if numstat != wantNumstat {
		t.Errorf("numstat = %q; want %q", numstat, wantNumstat)
	}
	assertScanHunkIndex(t, diff)
}

// assertScanHunkIndex proves the same bytes every reader sees parse the way the
// hunk index reads them: one section per file and the expected added spans.
func assertScanHunkIndex(t *testing.T, diff string) {
	t.Helper()
	sections := hunks.Files(diff)
	if len(sections) != 4 {
		t.Fatalf("hunks.Files found %d sections; want 4", len(sections))
	}
	wantAdds := []int{2, 0, 1, 0}
	for i, want := range wantAdds {
		if sections[i].Additions != want {
			t.Errorf("%s additions = %d; want %d", sections[i].Path, sections[i].Additions, want)
		}
	}
	if got := hunks.Snapshot(diff); got != "a.txt\tNEW\t1\t2\nc.txt\tNEW\t1\t1\n" {
		t.Errorf("hunk snapshot = %q; want the two text hunks", got)
	}
}

// TestScanAddedLinesKeepsEmptyLinesAndTheMarker pins the incremental hunk body:
// every line is prefixed, an empty line survives, and the no-newline marker
// lands only when the content does not end with one.
func TestScanAddedLinesKeepsEmptyLinesAndTheMarker(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"two lines", "one\ntwo\n", "+one\n+two\n"},
		{"empty line", "a\n\nb\n", "+a\n+\n+b\n"},
		{"no final newline", "a\nb", "+a\n+b\n\\ No newline at end of file\n"},
		{"one blank line", "\n", "+\n"},
	}
	for _, c := range cases {
		var out strings.Builder
		scanAddedLines(&out, []byte(c.content))
		if out.String() != c.want {
			t.Errorf("%s = %q; want %q", c.name, out.String(), c.want)
		}
	}
}

// TestScanGitPathQuotesAmbiguousPaths proves a path that cannot stand unquoted
// is C-quoted, and that the reader's own header parser recovers it byte for
// byte: a space would otherwise make the two sides of the header ambiguous.
func TestScanGitPathQuotesAmbiguousPaths(t *testing.T) {
	cases := []struct {
		rel  string
		want string
	}{
		{"plain.go", "a/plain.go"},
		{"a b.txt", `"a/a b.txt"`},
		{`q"uote.go`, `"a/q\"uote.go"`},
		{"héllo.go", `"a/h\303\251llo.go"`},
	}
	for _, c := range cases {
		got := scanGitPath(scanSideAdd, c.rel)
		if got != c.want {
			t.Errorf("scanGitPath(%q) = %s; want %s", c.rel, got, c.want)
		}
		header := "diff --git " + got + " " + scanGitPath(scanSideDelete, c.rel)
		_, newPath := hunks.HeaderPaths(header)
		if newPath != c.rel {
			t.Errorf("HeaderPaths(%q) new side = %q; want %q", header, newPath, c.rel)
		}
	}
}

// TestScanLineCountAndBinarySniff pins the two heuristics the synthesized diff
// rests on: a trailing newline terminates a line instead of opening another,
// and a NUL inside git's 8000-byte window marks the file binary.
func TestScanLineCountAndBinarySniff(t *testing.T) {
	lines := []struct {
		body string
		want int
	}{
		{"", 0},
		{"\n", 1},
		{"a", 1},
		{"a\n", 1},
		{"a\nb", 2},
		{"a\nb\n", 2},
		{"\n\n", 2},
	}
	for _, c := range lines {
		if got := scanLineCount([]byte(c.body)); got != c.want {
			t.Errorf("scanLineCount(%q) = %d; want %d", c.body, got, c.want)
		}
	}
	binary := []struct {
		name string
		body []byte
		want bool
	}{
		{"text", []byte("plain text\n"), false},
		{"nul first", []byte("a\x00b"), true},
		{"nul at window end", append(bytes.Repeat([]byte("a"), scanSniffBytes-1), 0), true},
		{"nul past window", append(bytes.Repeat([]byte("a"), scanSniffBytes), 0), false},
	}
	for _, c := range binary {
		if got := scanIsBinary(c.body); got != c.want {
			t.Errorf("scanIsBinary(%s) = %v; want %v", c.name, got, c.want)
		}
	}
}

// TestScanCollectSkipsALinkedWorktreeGitFile proves a `.git` regular file — how
// a linked worktree or a submodule names its real git directory — never enters
// the set at any depth, so a scan can neither review it nor publish the host
// path it names.
func TestScanCollectSkipsALinkedWorktreeGitFile(t *testing.T) {
	root := physicalTempDir(t)
	scanFixture(t, root, "src/a.go", "package a\n")
	scanFixture(t, root, ".git", "gitdir: /host/checkout/.git/worktrees/wt\n")
	scanFixture(t, root, "src/.git", "gitdir: /host/checkout/.git/modules/sub\n")

	files, skips, ref := collectScanFiles(context.Background(), root, []string{root})
	if ref != nil {
		t.Fatalf("collectScanFiles refusal: %v", ref)
	}
	assertScanFiles(t, files, []string{"src/a.go"})
	if skips.special != 0 {
		t.Errorf("skipped special entries = %d; want 0 (a managed name is not counted)", skips.special)
	}
	diff, _, _, ref := scanDiff(context.Background(), root, files)
	if ref != nil {
		t.Fatalf("scanDiff refusal: %v", ref)
	}
	if strings.Contains(diff, "gitdir") || strings.Contains(diff, "host/checkout") {
		t.Errorf("the synthesized diff carries a linked worktree's git file:\n%s", diff)
	}
}

// TestResolveScanRefusesNestedManagedEntries proves a managed name is refused
// at any depth, so a nested `.git` or `.verdict` cannot be scanned by naming
// it from the root.
func TestResolveScanRefusesNestedManagedEntries(t *testing.T) {
	root := physicalTempDir(t)
	scanFixture(t, root, "sub/.verdict/note.md", "{}\n")
	scanFixture(t, root, "sub/.git/HEAD", "ref: refs/heads/main\n")
	t.Chdir(root)

	for _, operand := range []string{"sub/.verdict", "sub/.git", "sub/.verdict/note.md", "sub/.git/HEAD"} {
		assertScanOperandRefusal(t, root, operand, "which a review never reads")
	}
}

// TestResolveScanRefusesCaseVariantManagedEntries proves a case variant that
// aliases a managed entry refuses on a case-insensitive volume. The same
// spelling is an ordinary name on a case-sensitive volume, where it either
// exists as its own entry or does not exist at all.
func TestResolveScanRefusesCaseVariantManagedEntries(t *testing.T) {
	root := physicalTempDir(t)
	scanFixture(t, root, ".git/config", "[core]\n")
	scanFixture(t, root, ".verdict/config.json", "{}\n")
	t.Chdir(root)
	if !scanCaseInsensitive(t, root) {
		t.Skip("this volume is case-sensitive, so a case variant is an ordinary name here")
	}
	for _, operand := range []string{".GIT/config", ".VERDICT/config.json"} {
		assertScanOperandRefusal(t, root, operand, "which a review never reads")
	}
}

// scanCaseInsensitive reports whether the fixture's volume folds the case of
// `.git`, which is what makes a case variant an alias of the managed entry.
func scanCaseInsensitive(t *testing.T, root string) bool {
	t.Helper()
	return scanAliasesName(t, root, ".git")
}

// scanAliasesName reports whether the volume folds one entry's case: the
// upper-cased spelling resolves to the same entry.
func scanAliasesName(t *testing.T, dir, name string) bool {
	t.Helper()
	lower, err := os.Lstat(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	upper, err := os.Lstat(filepath.Join(dir, strings.ToUpper(name)))
	if err != nil {
		return false
	}
	return os.SameFile(lower, upper)
}

// TestResolveScanRefusesAManagedWorkingDirectoryOperand proves the managed rule
// reads the resolved path, not only the operand's own components: scanning from
// a working directory inside `.verdict` refuses an operand that stays there,
// while an operand that steps back out to an ordinary directory is reviewable.
func TestResolveScanRefusesAManagedWorkingDirectoryOperand(t *testing.T) {
	root := physicalTempDir(t)
	scanFixture(t, root, ".verdict/notes.txt", "review state\n")
	scanFixture(t, root, "src/main.go", "package main\n")
	t.Chdir(filepath.Join(root, ".verdict"))

	assertScanOperandRefusal(t, root, "notes.txt", "which a review never reads")
	target, ref := resolveScanOperand(root, "../src/main.go")
	if ref != nil || target.rel != "src/main.go" {
		t.Fatalf("resolveScanOperand(../src/main.go) from .verdict = %+v, %v; want src/main.go", target, ref)
	}
}

// TestScanRootPrefixRefusalNeedsAConfirmedDirectory proves a case-insensitive
// prefix match alone never places an operand inside the root: an exact prefix
// needs no metadata, while a prefix that only folds to the root refuses unless
// a stat confirms the same directory. Neither path below exists, so the check
// is the same on every volume.
func TestScanRootPrefixRefusalNeedsAConfirmedDirectory(t *testing.T) {
	root := filepath.FromSlash("/nonexistent-verdict-root/repo")
	rootParts := []string{"nonexistent-verdict-root", "repo"}
	folded := []string{"nonexistent-verdict-root", "REPO"}
	operand := filepath.FromSlash("/nonexistent-verdict-root/REPO/notes.txt")

	if ref := scanRootPrefixRefusal(operand, rootParts, rootParts, root); ref != nil {
		t.Fatalf("exact prefix refused: %v", ref)
	}
	ref := scanRootPrefixRefusal(operand, folded, rootParts, root)
	if ref == nil || ref.Code != codeBadScan || !strings.Contains(ref.Message, "resolves outside the repository root") {
		t.Fatalf("folded prefix = %v; want a bad-scan outside-root refusal", ref)
	}
}

// TestResolveScanOperandRefusesACaseVariantRootPrefix proves a case variant
// that names another directory refuses: on a case-sensitive volume the sibling
// root is a different tree, so nothing under it is reviewable.
func TestResolveScanOperandRefusesACaseVariantRootPrefix(t *testing.T) {
	parent := physicalTempDir(t)
	root := filepath.Join(parent, "repo")
	sibling := filepath.Join(parent, "REPO")
	for _, dir := range []string{root, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if siblingInfo, serr := os.Stat(sibling); serr == nil && os.SameFile(rootInfo, siblingInfo) {
		t.Skip("this volume folds case, so the two spellings are one directory")
	}
	assertScanOperandRefusal(t, root, filepath.Join(sibling, "notes.txt"), "resolves outside the repository root")
}

// TestResolveScanOperandAcceptsACaseFoldedRootPrefix proves the folded spelling
// stays usable where the volume folds case, so a path a shell completed keeps
// working: the same directory under another spelling is the repository root.
func TestResolveScanOperandAcceptsACaseFoldedRootPrefix(t *testing.T) {
	root := filepath.Join(physicalTempDir(t), "repo")
	scanFixture(t, root, "notes.txt", "one\n")
	t.Chdir(root)
	if !scanAliasesName(t, root, "notes.txt") {
		t.Skip("this volume is case-sensitive, so a case variant names another directory")
	}
	folded := filepath.Join(filepath.Dir(root), "REPO", "notes.txt")
	target, ref := resolveScanOperand(root, folded)
	if ref != nil || target.rel != "notes.txt" {
		t.Fatalf("resolveScanOperand(folded root prefix) = %+v, %v; want notes.txt", target, ref)
	}
}

// TestScanCollectDedupesCaseAliasedOperands proves one file reached through two
// spellings lands in the scan once where the volume aliases them, while a
// case-sensitive volume keeps both names as the distinct files they are.
func TestScanCollectDedupesCaseAliasedOperands(t *testing.T) {
	root := physicalTempDir(t)
	scanFixture(t, root, "notes.txt", "lower\n")
	t.Chdir(root)
	if !scanAliasesName(t, root, "notes.txt") {
		scanFixture(t, root, "NOTES.txt", "upper\n")
		files, _, ref := collectScanFiles(t.Context(), root, []string{scanRootName, "NOTES.txt"})
		if ref != nil {
			t.Fatalf("collectScanFiles: %v", ref)
		}
		if got := scanFileNames(files); !slices.Equal(got, []string{"NOTES.txt", "notes.txt"}) {
			t.Errorf("files = %q; want both distinct names", got)
		}
		return
	}
	files, _, ref := collectScanFiles(t.Context(), root, []string{scanRootName, "NOTES.txt"})
	if ref != nil {
		t.Fatalf("collectScanFiles: %v", ref)
	}
	if got := scanFileNames(files); !slices.Equal(got, []string{"notes.txt"}) {
		t.Errorf("files = %q; want the one stored spelling", got)
	}
}

// scanFileNames lists the collected relative paths in the order they carry.
func scanFileNames(files []scanFile) []string {
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.rel)
	}
	return names
}

// TestResolveScanRefusesIntermediateSymlink proves a link anywhere between the
// root and the operand refuses, including one a `..` would otherwise erase:
// every raw component is validated in order, so `dir/link/../file` never
// collapses away the link it crossed.
func TestResolveScanRefusesIntermediateSymlink(t *testing.T) {
	root := physicalTempDir(t)
	scanFixture(t, root, "app/main.go", "package main\n")
	scanFixture(t, root, "dir/file.txt", "inside\n")
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../app", filepath.Join(root, "a", "link")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	if err := os.Symlink("../app", filepath.Join(root, "dir", "link")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	assertScanOperandRefusal(t, root, "a/link", "is a symlink")
	assertScanOperandRefusal(t, root, "a/link/main.go", "is a symlink")
	assertScanOperandRefusal(t, root, "dir/link/../file.txt", "is a symlink")
}

// TestScanDiffRefusesAFileReplacedByALink proves the read re-verifies the leaf:
// a file swapped for a link between the walk and the read refuses instead of
// publishing whatever the link points at.
func TestScanDiffRefusesAFileReplacedByALink(t *testing.T) {
	root := physicalTempDir(t)
	scanFixture(t, root, "notes.txt", "inside\n")
	files := []scanFile{{rel: "notes.txt", size: 7}}
	target := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(target, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "notes.txt")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	_, _, _, ref := scanDiff(context.Background(), root, files)
	if ref == nil || ref.Code != codeBadScan {
		t.Fatalf("scanDiff = %v; want a bad-scan refusal", ref)
	}
	if !strings.Contains(ref.Message, "symlink") {
		t.Errorf("refusal = %q; want it to name the link", ref.Message)
	}
}

// TestScanDiffRefusesAMissingFile proves a selected file that disappears before
// the read refuses on every platform, not only where permissions can be
// revoked.
func TestScanDiffRefusesAMissingFile(t *testing.T) {
	root := physicalTempDir(t)
	files := []scanFile{{rel: "gone.txt", size: 7}}
	_, _, _, ref := scanDiff(context.Background(), root, files)
	if ref == nil || ref.Code != codeBadScan {
		t.Fatalf("scanDiff = %v; want a bad-scan refusal", ref)
	}
	if !strings.Contains(ref.Message, "could not read gone.txt") {
		t.Errorf("refusal = %q; want it to name the missing file", ref.Message)
	}
}

// TestScanProjectionBoundsOneSection pins the projection's arithmetic: two
// bytes per content byte (a "+" and its newline), twelve bytes per path byte
// (the three quoted path tokens a section can spell), and the fixed section
// overhead.
func TestScanProjectionBoundsOneSection(t *testing.T) {
	got := scanProjected(scanFile{rel: "ab", size: 10})
	if want := int64(2*10 + 12*2 + scanProjectedPerFile); got != want {
		t.Errorf("scanProjected = %d; want %d", got, want)
	}
}

// TestScanCollectSkipsManagedAndLinks proves a directory walk leaves the
// managed trees, symlinks, and non-regular entries alone while keeping every
// regular file, deduped and sorted, with a slash-form path.
func TestScanCollectSkipsManagedAndLinks(t *testing.T) {
	root := physicalTempDir(t)
	scanFixture(t, root, "src/a.go", "package a\n")
	scanFixture(t, root, "src/nested/b.go", "package b\n")
	scanFixture(t, root, ".git/objects/obj", "git internals\n")
	scanFixture(t, root, ".verdict/state/local-x/source.json", "{}\n")
	scanFixture(t, root, "src/.verdict/note.md", "nested managed dir\n")
	if err := os.Symlink("a.go", filepath.Join(root, "src", "link.go")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	// A directory link must be counted, not followed: following it would
	// enumerate every file a second time under the link's name.
	if err := os.Symlink("src", filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}

	files, skips, ref := collectScanFiles(context.Background(), root, []string{root, filepath.Join(root, "src/a.go")})
	if ref != nil {
		t.Fatalf("collectScanFiles refusal: %v", ref)
	}
	assertScanFiles(t, files, []string{"src/a.go", "src/nested/b.go"})
	if skips.symlinks != 2 {
		t.Errorf("skipped symlinks = %d; want 2 (one file link, one directory link)", skips.symlinks)
	}
	if skips.special != 0 {
		t.Errorf("skipped special entries = %d; want 0", skips.special)
	}
}

// assertScanFiles pins the selected paths, in order.
func assertScanFiles(t *testing.T, files []scanFile, want []string) {
	t.Helper()
	if len(files) != len(want) {
		t.Fatalf("files = %v; want %v", files, want)
	}
	for i := range want {
		if files[i].rel != want[i] {
			t.Errorf("files[%d] = %q; want %q", i, files[i].rel, want[i])
		}
	}
}

// TestResolveScanOperandRefusals pins the fail-closed selector: a path outside
// the root, a symlink, a missing path, and a managed directory each refuse
// typed, before a single file is read.
func TestResolveScanOperandRefusals(t *testing.T) {
	root := physicalTempDir(t)
	scanFixture(t, root, "src/a.go", "package a\n")
	scanFixture(t, root, ".verdict/config.json", "{}\n")
	t.Chdir(root)

	target, ref := resolveScanOperand(root, "src/a.go")
	if ref != nil || target.rel != "src/a.go" || !target.info.Mode().IsRegular() {
		t.Fatalf("resolveScanOperand(src/a.go) = %+v, %v; want the regular file", target, ref)
	}
	if err := os.Symlink("src/a.go", filepath.Join(root, "inside-link.go")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	outsideTarget := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsideTarget, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideTarget, filepath.Join(root, "outside-link.txt")); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		operand string
		want    string
	}{
		{"parent escape", "../" + filepath.Base(root) + "-elsewhere", "resolves outside the repository root"},
		{"absolute escape", outsideTarget, "resolves outside the repository root"},
		{"missing", "missing.go", "no such path"},
		{"managed", ".verdict", "which a review never reads"},
		{"inside symlink", "inside-link.go", "is a symlink"},
		{"escaping symlink", "outside-link.txt", "is a symlink"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertScanOperandRefusal(t, root, c.operand, c.want)
		})
	}
}

// assertScanOperandRefusal pins one fail-closed selector refusal.
func assertScanOperandRefusal(t *testing.T, root, operand, want string) {
	t.Helper()
	_, ref := resolveScanOperand(root, operand)
	if ref == nil || ref.Code != codeBadScan {
		t.Fatalf("resolveScanOperand(%q) = %v; want a bad-scan refusal", operand, ref)
	}
	if !strings.Contains(ref.Message, want) {
		t.Errorf("refusal = %q; want it to carry %q", ref.Message, want)
	}
}

// TestScanRelConfines proves the relative-path check: a sibling sharing the
// root's prefix is outside, and the root itself is inside with no path to name.
func TestScanRelConfines(t *testing.T) {
	root := filepath.FromSlash("/repo")
	if rel, ok := scanRel(root, filepath.FromSlash("/repo/src/a.go")); !ok || rel != "src/a.go" {
		t.Errorf("scanRel inside = %q, %v; want src/a.go, true", rel, ok)
	}
	for _, outside := range []string{"/repo-other/a.go", "/other/a.go"} {
		if rel, ok := scanRel(root, filepath.FromSlash(outside)); ok {
			t.Errorf("scanRel(%q) = %q, true; want outside", outside, rel)
		}
	}
	if rel, ok := scanRel(root, root); !ok || rel != "" {
		t.Errorf("scanRel(root) = %q, %v; want the empty root itself", rel, ok)
	}
}

// TestScanManagedNameOfMatchesCanonicalAndAliasedEntries proves the canonical
// names are managed wherever they appear, and that a case variant is managed
// only where it aliases the canonical entry.
func TestScanManagedNameOfMatchesCanonicalAndAliasedEntries(t *testing.T) {
	root := physicalTempDir(t)
	scanFixture(t, root, ".git/config", "[core]\n")
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFS.Close()

	for _, name := range []string{".git", ".verdict"} {
		if got, managed := scanManagedNameOf(rootFS, name); !managed || got != name {
			t.Errorf("scanManagedNameOf(%q) = %q, %v; want the canonical name", name, got, managed)
		}
	}
	if _, managed := scanManagedNameOf(rootFS, "src"); managed {
		t.Error("an ordinary name is not managed")
	}
	// The case variant is managed exactly when the volume folds it onto the
	// canonical entry, which is what makes it the same directory.
	_, aliased := scanManagedNameOf(rootFS, ".GIT")
	if want := scanCaseInsensitive(t, root); aliased != want {
		t.Errorf("scanManagedNameOf(.GIT) managed = %v; want %v on this volume", aliased, want)
	}
}

// TestScanRefusalCarriesTheBadScanCode proves the refusal constructor is the
// one code every selector failure renders, so the troubleshooting entry and
// the hint table cover all of them.
func TestScanRefusalCarriesTheBadScanCode(t *testing.T) {
	ref := scanRefusal("--scan %s: nope", "x")
	if ref.Code != codeBadScan || ref.Message != "--scan x: nope" {
		t.Fatalf("refusal = %+v; want bad-scan naming the operand", ref)
	}
}
