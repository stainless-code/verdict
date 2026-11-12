package indexcore

import (
	"bytes"
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestMain pins the git environment every fixture assumes.
func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }

// fixture is one hermetic git repository with pinned identity and dates.
type fixture struct {
	t   *testing.T
	dir string
	day int
}

// newFixture creates an empty repository on main. The repository's own exclude
// file keeps the workspace a build writes out of every commit, so a fixture
// that builds before its last commit has a window of its own paths only.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), day: 1}
	f.git("init", "-q", "-b", "main")
	f.ignoreVerdict()
	return f
}

// gitWaitDelay bounds every git command a fixture captures pipes from
// (GO-CONC-06): a descendant that keeps the pipe open costs a wait, not a
// hung test.
const gitWaitDelay = time.Minute

// ignoreVerdict appends the workspace to the repository's own exclude file,
// never to a tracked .gitignore, so no fixture path is added to a commit.
// git init skips .git/info when its template is empty, so the directory is
// created rather than assumed.
func (f *fixture) ignoreVerdict() {
	f.t.Helper()
	path := filepath.Join(f.dir, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(".verdict/\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// date renders the fixture's next pinned commit date.
func (f *fixture) date() string {
	return time.Date(2024, 3, f.day, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
}

// run runs one git command in the fixture with stdin and returns its stdout.
func (f *fixture) run(stdin string, args ...string) string {
	f.t.Helper()
	cmd := exec.CommandContext(f.t.Context(), "git", args...)
	cmd.Dir = f.dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.WaitDelay = gitWaitDelay
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_DATE":    f.date(),
		"GIT_COMMITTER_DATE": f.date(),
	})
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// git runs one git command in the fixture.
func (f *fixture) git(args ...string) { f.run("", args...) }

// gitNoProtect runs one git command with the pathname-protection heuristics
// off. Git for Windows rejects NTFS-invalid names (core.protectNTFS) even in
// the index, and the index under test only reads tree objects, which git never
// validates.
func (f *fixture) gitNoProtect(args ...string) {
	f.t.Helper()
	f.git(append([]string{"-c", "core.protectNTFS=false", "-c", "core.protectHFS=false"}, args...)...)
}

// write writes one file under the repository.
func (f *fixture) write(name, body string) {
	f.t.Helper()
	path := filepath.Join(f.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// commit stages everything and commits it.
func (f *fixture) commit(msg string) {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "-qm", msg)
	f.day++
}

// commitObject commits the written files plus one path held only by a git
// object, so a name no filesystem accepts — a newline — survives every OS.
func (f *fixture) commitObject(name, body, msg string) {
	f.t.Helper()
	f.git("add", "-A")
	blob := f.run(body, "hash-object", "-w", "--stdin")
	f.gitNoProtect("update-index", "--add", "--cacheinfo", "100644,"+blob+","+name)
	f.gitNoProtect("commit", "-qm", msg)
	f.day++
}

// env converts the hermetic child environment to the runtime snapshot shape.
func envMap() map[string]string {
	out := map[string]string{}
	for _, kv := range testenv.Base(nil) {
		name, value, ok := strings.Cut(kv, "=")
		if ok {
			out[name] = value
		}
	}
	return out
}

// request builds one index request against the fixture.
func (f *fixture) request(query string, limits *Limits) *Request {
	return &Request{
		Query:    query,
		Runtime:  &core.Runtime{Commander: coreexec.Runner{}, Env: envMap()},
		RepoRoot: f.dir,
		Limits:   limits,
	}
}

// build runs index build and requires success.
func (f *fixture) build() *Response {
	f.t.Helper()
	resp, ref := Run(context.Background(), f.request(QueryBuild, nil))
	if ref != nil {
		f.t.Fatalf("index build refused: %v", ref)
	}
	return resp
}

// buildRef runs index build and returns its refusal.
func (f *fixture) buildRef(limits *Limits) *core.RefusalError {
	f.t.Helper()
	_, ref := Run(context.Background(), f.request(QueryBuild, limits))
	return ref
}

// callers runs the fixture's a.Target callers query.
func (f *fixture) callers() *Response {
	f.t.Helper()
	req := f.request(QueryCallers, nil)
	req.Symbol = "a.Target"
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		f.t.Fatalf("index callers refused: %v", ref)
	}
	return resp
}

// assertRows compares caller rows against literals, including order.
func assertRows(t *testing.T, got, want []Row) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("rows = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// assertRefusal compares one typed refusal against literals.
func assertRefusal(t *testing.T, ref *core.RefusalError, code, message string) {
	t.Helper()
	if ref == nil {
		t.Fatalf("want %s: %q, got a response", code, message)
	}
	if ref.Code != code || ref.Message != message {
		t.Fatalf("refusal = %q: %q; want %s: %q", ref.Code, ref.Message, code, message)
	}
}

// snapshotStore reads every file under .verdict/index keyed by relative path.
//
//nolint:revive // the fixture walk reads as one block
func snapshotStore(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	store := filepath.Join(root, ".verdict", "index")
	err := filepath.Walk(store, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(store, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// storeFiles counts the tree or shard files under the store.
func storeFiles(t *testing.T, root, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".verdict", "index", dir))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// writeCallFixture writes the three-package or four-package fixture.
func writeCallFixture(f *fixture, extra string) {
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("a/a.go", "package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n")
	f.write("b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc Uses() {\n\ta.Target()\n}\n")
	f.write("c/c.go", "package c\n\nfunc Target() {}\n\nfunc Local() {\n\tTarget()\n}\n")
	if extra != "" {
		f.write("d/d.go", extra)
	}
}

// cappedCommander returns the capture-cap error for every command, standing in
// for a git stream larger than the runner keeps. readBytes is the count the
// runner measured; 0 stands in for a commander that keeps no count.
type cappedCommander struct{ readBytes int }

func (c cappedCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{}, &core.OutputCappedError{Stream: "stdout", Limit: 101711872, ReadBytes: c.readBytes}
}

// TestGitBatchCaptureCapRefuses proves an over-cap cat-file stream refuses as
// index-capped naming the cap and, once the commander counted the stream, the
// bytes git delivered — never index-failed pointing at a rebuild.
func TestGitBatchCaptureCapRefuses(t *testing.T) {
	t.Run("measured", func(t *testing.T) {
		assertGitBatchCapRefusal(t, cappedCommander{readBytes: 118111600},
			"git cat-file output exceeded the 101711872-byte capture cap after 118111600 bytes")
	})
	t.Run("unmeasured", func(t *testing.T) {
		assertGitBatchCapRefusal(t, cappedCommander{},
			"git cat-file output exceeded the 101711872-byte capture cap")
	})
}

// assertGitBatchCapRefusal drives gitBatch with a commander that reports the
// capture cap and asserts the refusal.
func assertGitBatchCapRefusal(t *testing.T, cmdr core.Commander, want string) {
	t.Helper()
	req := &Request{}
	req.Runtime = &core.Runtime{Commander: cmdr, Env: map[string]string{}}
	st := &state{req: req}
	_, ref := st.gitBatch(context.Background(), ".", []string{"0f1e2d3c"})
	assertRefusal(t, ref, "index-capped", want)
}

// TestIndexBytesAreDeterministic proves two builds of the same tree write
// byte-identical stores.
func TestIndexBytesAreDeterministic(t *testing.T) {
	f := newFixture(t)
	writeCallFixture(f, "")
	f.commit("one")
	f.build()
	first := snapshotStore(t, f.dir)
	f.build()
	second := snapshotStore(t, f.dir)
	if !maps.Equal(first, second) {
		for _, name := range unionKeys(first, second) {
			if first[name] != second[name] {
				t.Errorf("%s differs:\nfirst:  %q\nsecond: %q", name, first[name], second[name])
			}
		}
	}
}

// unionKeys returns the sorted union of two maps' keys.
func unionKeys(a, b map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]string{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

// TestIndexTreeKeyIgnoresCommit proves two commits with the same content share
// one tree file, and the second build parses nothing.
func TestIndexTreeKeyIgnoresCommit(t *testing.T) {
	f := newFixture(t)
	writeCallFixture(f, "")
	f.commit("one")
	first := f.build()
	f.git("commit", "--allow-empty", "-qm", "same content")
	second := f.build()
	if second.Data.Tree != first.Data.Tree {
		t.Fatalf("tree changed across an empty commit: %s -> %s", first.Data.Tree, second.Data.Tree)
	}
	if second.Data.Parsed != 0 {
		t.Errorf("second build parsed %d files; want 0", second.Data.Parsed)
	}
	if got := storeFiles(t, f.dir, "trees"); got != 1 {
		t.Errorf("tree files = %d; want 1", got)
	}
}

// TestIndexReparsesOnlyChangedBlob proves an edit parses one blob and a pure
// rename parses none.
func TestIndexReparsesOnlyChangedBlob(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n")
	f.write("a/a.go", "package a\n\nfunc A() {}\n")
	f.write("b/b.go", "package b\n\nfunc B() {}\n")
	f.write("c/c.go", "package c\n\nfunc C() {}\n")
	f.commit("one")
	first := f.build()
	if first.Data.Parsed != 3 {
		t.Fatalf("first build parsed %d; want 3", first.Data.Parsed)
	}
	f.write("a/a.go", "package a\n\nfunc A() {}\n\nfunc A2() {}\n")
	f.commit("edit one")
	edited := f.build()
	if edited.Data.Parsed != 1 {
		t.Errorf("edit parsed %d files; want 1", edited.Data.Parsed)
	}
	beforeRename := storeFiles(t, f.dir, "shards")
	f.git("mv", "a/a.go", "a/renamed.go")
	f.commit("rename")
	renamed := f.build()
	if renamed.Data.Parsed != 0 {
		t.Errorf("rename parsed %d files; want 0", renamed.Data.Parsed)
	}
	if got := storeFiles(t, f.dir, "shards"); got != beforeRename {
		t.Errorf("shards = %d after rename; want %d", got, beforeRename)
	}
}

// TestCallersCorruptTreeRefuses proves a truncated tree file refuses
// index-corrupt instead of answering from a partial store.
func TestCallersCorruptTreeRefuses(t *testing.T) {
	f := newFixture(t)
	writeCallFixture(f, "")
	f.commit("one")
	tree := f.run("", "rev-parse", "HEAD^{tree}")
	f.build()
	path := filepath.Join(f.dir, ".verdict", "index", "trees", tree+".json")
	if err := os.Truncate(path, 5); err != nil {
		t.Fatal(err)
	}
	req := f.request(QueryCallers, nil)
	req.Symbol = "a.Target"
	_, ref := Run(context.Background(), req)
	assertRefusal(t, ref, "index-corrupt", "tree "+tree+".json is truncated or invalid")
}

// TestIndexVersionResetRebuilds proves a store written by another version is
// reset and reparsed, never partially read.
func TestIndexVersionResetRebuilds(t *testing.T) {
	f := newFixture(t)
	writeCallFixture(f, "")
	f.commit("one")
	first := f.build()
	if first.Data.Parsed != 3 {
		t.Fatalf("first build parsed %d; want 3", first.Data.Parsed)
	}
	version := filepath.Join(f.dir, ".verdict", "index", "version")
	if err := os.WriteFile(version, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := f.build()
	if second.Data.Parsed != 3 {
		t.Errorf("version reset parsed %d; want 3", second.Data.Parsed)
	}
	raw, err := os.ReadFile(version)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "2\n" {
		t.Errorf("version file = %q; want %q", raw, "2\n")
	}
	if got := storeFiles(t, f.dir, "trees"); got != 1 {
		t.Errorf("tree files = %d; want 1", got)
	}
}

// TestCallersMissingIndexRefuses proves a caller query before a build refuses
// missing-index with the build instruction, not an empty answer.
func TestCallersMissingIndexRefuses(t *testing.T) {
	f := newFixture(t)
	writeCallFixture(f, "")
	f.commit("one")
	tree := f.run("", "rev-parse", "HEAD^{tree}")
	req := f.request(QueryCallers, nil)
	req.Symbol = "a.Target"
	_, ref := Run(context.Background(), req)
	assertRefusal(t, ref, "missing-index", "no index for tree "+tree+"; run verdict index build")
}

// TestShardIdentityIncludesRefsPerFile proves a shard parsed under a lower
// reference cap is reparsed when the cap rises: reusing it would answer with
// the truncated reference list.
func TestShardIdentityIncludesRefsPerFile(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n")
	f.write("a/a.go", "package a\n\nfunc Caller() {\n\tOne()\n\tTwo()\n}\n")
	f.commit("one")
	low, ref := Run(context.Background(), f.request(QueryBuild, &Limits{RefsPerFile: 1}))
	if ref != nil {
		t.Fatalf("capped build refused: %v", ref)
	}
	if low.Data.Parsed != 1 || low.Data.Skipped != 1 {
		t.Fatalf("capped build = parsed %d, skipped %d; want parsed 1, skipped 1", low.Data.Parsed, low.Data.Skipped)
	}
	store := storePath(f.dir)
	assertTreeRefs(t, store, low.Data.Tree, true, "One")

	full := f.build()
	if full.Data.Parsed != 1 {
		t.Errorf("raising the cap parsed %d shards; want 1", full.Data.Parsed)
	}
	assertTreeRefs(t, store, full.Data.Tree, false, "One", "Two")
}

// assertTreeRefs pins the references one single-file tree carries.
func assertTreeRefs(t *testing.T, store, tree string, truncated bool, names ...string) {
	t.Helper()
	got, ref := readTree(store, tree)
	if ref != nil {
		t.Fatalf("read tree %s: %v", tree, ref)
	}
	if len(got.Files) != 1 {
		t.Fatalf("tree %s files = %d; want 1", tree, len(got.Files))
	}
	file := got.Files[0]
	if file.RefsTruncated != truncated {
		t.Errorf("tree %s refs_truncated = %t; want %t", tree, file.RefsTruncated, truncated)
	}
	if len(file.Refs) != len(names) {
		t.Fatalf("tree %s refs = %d; want %d", tree, len(file.Refs), len(names))
	}
	for i, name := range names {
		if file.Refs[i].Name != name {
			t.Errorf("tree %s ref %d = %q; want %q", tree, i, file.Refs[i].Name, name)
		}
	}
}

// TestShardCapCountsOverwritesOnce proves a shard rewritten under a new cap
// is charged once, so raising the cap cannot refuse a store that ends within
// it.
func TestShardCapCountsOverwritesOnce(t *testing.T) {
	store := t.TempDir()
	if err := os.MkdirAll(filepath.Join(store, shardsDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	blob := "0f1e2d3c"
	if err := os.WriteFile(shardPath(store, blob), []byte(strings.Repeat("x", 1<<20)), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := map[string]*shard{blob: {SchemaVersion: "1", Blob: blob, RefsPerFile: 5000}}
	count, got, ref := checkShardCaps(store, fresh, Limits{ShardsBytes: 3000})
	if ref != nil {
		t.Fatalf("overwrite refusal: %v", ref)
	}
	if count != 1 {
		t.Errorf("shard count = %d; want 1", count)
	}
	if got > 3000 {
		t.Errorf("shard bytes = %d; want within 3000", got)
	}
}

// TestTruncatedShardRefuses proves a truncated shard is a typed refusal, never
// read as empty.
func TestTruncatedShardRefuses(t *testing.T) {
	f := newFixture(t)
	writeCallFixture(f, "")
	f.commit("one")
	f.build()
	shardsDir := filepath.Join(f.dir, ".verdict", "index", "shards")
	entries, err := os.ReadDir(shardsDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("shards: %v (%d entries)", err, len(entries))
	}
	blob := strings.TrimSuffix(entries[0].Name(), ".json")
	path := filepath.Join(shardsDir, entries[0].Name())
	if err := os.Truncate(path, 5); err != nil {
		t.Fatal(err)
	}
	ref := f.buildRef(nil)
	if ref == nil {
		t.Fatal("truncated shard build succeeded")
	}
	want := "shard " + blob + ".json is truncated or invalid"
	if ref.Code != "index-corrupt" || ref.Message != want {
		t.Fatalf("refusal = %q: %q; want index-corrupt: %q", ref.Code, ref.Message, want)
	}
}

// writeShadowFixture writes the exactness fixture: two real call sites of
// a.Target plus the four function-scope shadows that must contribute nothing.
func writeShadowFixture(f *fixture) {
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("a/a.go", `package a

func Target() {}

func Caller() {
	Target()
}

func LocalVar() {
	Target := func() {}
	Target()
}

func Param(Target func()) {
	Target()
}

func LocalType() {
	type Target struct{}
	_ = Target{}
}
`)
	f.write("b/b.go", `package b

import "example.com/m/a"

func Uses() {
	a.Target()
}

type Holder struct{ Target int }

func QualifierShadow() {
	a := Holder{}
	_ = a.Target
}
`)
	f.write("c/c.go", "package c\n\nfunc Target() {}\n\nfunc Local() {\n\tTarget()\n}\n")
}

// TestCallersExactnessLitmus proves same-package call sites are exact and a
// same-named local in a non-importing package contributes no rows, and that
// no function-scope shadow — a local, a parameter, a local type, or a variable
// masking an import qualifier — is ever recorded as a caller.
func TestCallersExactnessLitmus(t *testing.T) {
	f := newFixture(t)
	writeShadowFixture(f)
	f.commit("one")
	f.build()
	resp := f.callers()
	assertRows(t, resp.Data.Rows, []Row{
		{Path: "a/a.go", Line: 6, Kind: KindCall, Resolution: "exact", Language: "go"},
		{Path: "b/b.go", Line: 6, Kind: KindCall, Resolution: "lexical", Language: "go"},
	})
	if resp.Data.Callers != 2 {
		t.Errorf("callers = %d; want 2", resp.Data.Callers)
	}
	if resp.Data.Unresolved != 0 {
		t.Errorf("unresolved = %d; want 0", resp.Data.Unresolved)
	}
	for _, row := range resp.Data.Rows {
		if strings.HasPrefix(row.Path, "c/") {
			t.Errorf("non-importing package c contributed row %+v", row)
		}
	}
}

// TestCallersCrossPackageNeedsImport proves a bare same-named reference in a
// package with no import edge is never a lexical result.
func TestCallersCrossPackageNeedsImport(t *testing.T) {
	f := newFixture(t)
	writeCallFixture(f, "package d\n\nfunc Uses() {\n\tTarget()\n}\n")
	f.commit("one")
	f.build()
	resp := f.callers()
	for _, row := range resp.Data.Rows {
		if strings.HasPrefix(row.Path, "d/") {
			t.Errorf("package d matched without an import edge: %+v", row)
		}
	}
	if resp.Data.Callers != 2 {
		t.Errorf("callers = %d; want 2", resp.Data.Callers)
	}
}

// TestCallersValueSelectorIsUnresolved proves a selector written on a value is
// counted as a candidate that needs type information outside the declaring
// package too: the `a.Touch` query answers no rows at one unresolved and says
// so on the human line, while a selector naming a same-named function is not
// a candidate at all.
func TestCallersValueSelectorIsUnresolved(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("a/a.go", "package a\n\ntype Base struct{}\n\nfunc (b *Base) Touch() {}\n\nfunc Target() {}\n")
	f.write("b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc Bump(v *a.Base) {\n\tv.Touch()\n}\n\n"+
		"func Value(h Holder) {\n\th.Target\n}\n\ntype Holder struct{ Target int }\n")
	f.commit("one")
	f.build()
	touch := callersOf(t, f, "a.Touch")
	if len(touch.Data.Rows) != 0 || touch.Data.Unresolved != 1 {
		t.Errorf("rows = %+v, unresolved = %d; want none and 1", touch.Data.Rows, touch.Data.Unresolved)
	}
	want := "index: 0 caller(s) of a.Touch at " + touch.Data.Tree + " (1 unresolved)"
	if !slices.Contains(touch.Human, want) {
		t.Errorf("human lines = %q; want one equal to %q", touch.Human, want)
	}
	if got := callersOf(t, f, "a.Target"); got.Data.Unresolved != 0 || len(got.Data.Rows) != 0 {
		t.Errorf("a.Target: rows = %+v, unresolved = %d; want none and 0", got.Data.Rows, got.Data.Unresolved)
	}
}

// TestCallersCountsEachUnresolvedSiteOnce proves one selector matching two
// package declarations is one unresolved source site.
func TestCallersCountsEachUnresolvedSiteOnce(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("a/a.go", "package a\n\ntype A struct{}\nfunc (A) Touch() {}\n")
	f.write("b/b.go", "package b\n\ntype B struct{}\nfunc (B) Touch() {}\n")
	f.write("c/c.go", "package c\n\nfunc Use(v any) { v.Touch() }\n")
	f.commit("one")
	f.build()
	resp := callersOf(t, f, "Touch")
	if len(resp.Data.Rows) != 0 || resp.Data.Unresolved != 1 {
		t.Errorf("rows = %+v, unresolved = %d; want none and 1", resp.Data.Rows, resp.Data.Unresolved)
	}
}

// TestCallersGenericInstantiationIsUnresolved proves a generic instantiation
// is counted unresolved on both tiers: in the declaring package and through
// an import edge.
func TestCallersGenericInstantiationIsUnresolved(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n\ngo 1.27\n")
	f.write("a/a.go", "package a\n\nfunc Target[T any](v T) {}\n\nfunc Caller() {\n\tTarget[int](0)\n}\n")
	f.write("b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc Uses() {\n\ta.Target[int](0)\n}\n")
	f.commit("one")
	f.build()
	resp := f.callers()
	if len(resp.Data.Rows) != 0 {
		t.Errorf("rows = %+v; want none for generic instantiations", resp.Data.Rows)
	}
	if resp.Data.Callers != 0 {
		t.Errorf("callers = %d; want 0", resp.Data.Callers)
	}
	if resp.Data.Unresolved != 2 {
		t.Errorf("unresolved = %d; want 2", resp.Data.Unresolved)
	}
}

// TestCallersSymbolTypoIsAnEmptyAnswer pins the manual's scope: only the
// qualifier is checked, so a mistyped symbol answers zero rows instead of a
// refusal and the reader has to check the name against the declarations.
func TestCallersSymbolTypoIsAnEmptyAnswer(t *testing.T) {
	f := newFixture(t)
	writeCallFixture(f, "")
	f.commit("one")
	f.build()
	req := f.request(QueryCallers, nil)
	req.Symbol = "a.Nope"
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index callers refused a mistyped symbol: %v", ref)
	}
	if resp.Data.Callers != 0 || len(resp.Data.Rows) != 0 || resp.Data.Unresolved != 0 {
		t.Errorf("callers = %d, rows = %d, unresolved = %d; want an empty answer",
			resp.Data.Callers, len(resp.Data.Rows), resp.Data.Unresolved)
	}
}

// TestCallersQualifierRefusals proves an unmatched package qualifier refuses
// instead of reporting no callers, and an ambiguous one still refuses.
func TestCallersQualifierRefusals(t *testing.T) {
	t.Run("unmatched", func(t *testing.T) {
		f := newFixture(t)
		writeCallFixture(f, "")
		f.commit("one")
		f.build()
		req := f.request(QueryCallers, nil)
		req.Symbol = "nosuchpkg.Target"
		_, ref := Run(context.Background(), req)
		assertRefusal(t, ref, "bad-flags", "package qualifier nosuchpkg matches no package directory or import path")
	})
	t.Run("ambiguous", func(t *testing.T) {
		f := newFixture(t)
		f.write("go.mod", "module example.com/m\n")
		f.write("one/x/x.go", "package x\n\nfunc Target() {}\n")
		f.write("two/x/x.go", "package x\n\nfunc Target() {}\n")
		f.commit("one")
		f.build()
		req := f.request(QueryCallers, nil)
		req.Symbol = "x.Target"
		_, ref := Run(context.Background(), req)
		assertRefusal(t, ref, "bad-flags", "package qualifier x is ambiguous")
	})
}

// TestCallersLimitTruncates proves rows are truncated to the limit and the
// human stream names what was withheld, never silently.
func TestCallersLimitTruncates(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n")
	f.write("a/a.go", `package a

func Target() {}

func A() {
	Target()
}

func B() {
	Target()
}

func C() {
	Target()
}
`)
	f.commit("one")
	f.build()
	req := f.request(QueryCallers, nil)
	req.Symbol, req.Limit = "a.Target", 2
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index callers refused: %v", ref)
	}
	if resp.Data.Callers != 3 || resp.Data.Truncated != 1 || resp.Data.Limit != 2 {
		t.Fatalf("limit data = callers %d, truncated %d, limit %d; want 3, 1, 2",
			resp.Data.Callers, resp.Data.Truncated, resp.Data.Limit)
	}
	wantRows := []Row{
		{Path: "a/a.go", Line: 6, Kind: KindCall, Resolution: "exact", Language: "go"},
		{Path: "a/a.go", Line: 10, Kind: KindCall, Resolution: "exact", Language: "go"},
	}
	assertRows(t, resp.Data.Rows, wantRows)
	wantLine := "index: showing 2 of 3 callers (1 not shown; raise --limit up to 200)"
	if !slices.Contains(resp.Human, wantLine) {
		t.Errorf("human lines = %q; want one equal to %q", resp.Human, wantLine)
	}
}

// TestCallersLimitBoundsRefuse proves an out-of-range limit refuses on every
// transport instead of rendering a negative truncation.
func TestCallersLimitBoundsRefuse(t *testing.T) {
	f := newFixture(t)
	writeCallFixture(f, "")
	f.commit("one")
	f.build()
	for _, limit := range []int{-1, 201} {
		req := f.request(QueryCallers, nil)
		req.Symbol, req.Limit = "a.Target", limit
		_, ref := Run(context.Background(), req)
		assertRefusal(t, ref, "bad-flags", "index callers wants --limit from 1 to 200")
	}
}

// TestIndexCapRefusal proves the file-count cap names the measured number.
//
//nolint:revive // the fixture walk reads as one block
func TestIndexCapRefusal(t *testing.T) {
	t.Run("repo over cap", func(t *testing.T) {
		f := newFixture(t)
		writeCallFixture(f, "")
		f.commit("one")
		ref := f.buildRef(&Limits{Files: 2})
		if ref == nil {
			t.Fatal("over-cap build succeeded")
		}
		want := "3 source files exceeds the 2-file cap"
		if ref.Code != "index-capped" || ref.Message != want {
			t.Fatalf("refusal = %q: %q; want index-capped: %q", ref.Code, ref.Message, want)
		}
	})
	t.Run("config cap", func(t *testing.T) {
		f := newFixture(t)
		f.write(".verdict/config.json", `{"schema_version":"1","index":{"files":2}}`)
		writeCallFixture(f, "")
		f.commit("one")
		ref := f.buildRef(nil)
		if ref == nil {
			t.Fatal("config-capped build succeeded")
		}
		want := "3 source files exceeds the 2-file cap"
		if ref.Code != "index-capped" || ref.Message != want {
			t.Fatalf("refusal = %q: %q; want index-capped: %q", ref.Code, ref.Message, want)
		}
	})
}

// TestIndexOversizeFileCountedOnce proves an oversize file counts as skipped
// once and takes no language with it: the language breakdown lists only the
// files the build indexed.
func TestIndexOversizeFileCountedOnce(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n")
	f.write("a/a.go", "package a\n\nfunc A() {}\n")
	f.write("a/big.go", "package a\n\n//"+strings.Repeat(" pad", 600000)+"\n")
	f.write("t.ts", "export const v = 1;\n")
	f.commit("one")
	resp := f.build()
	if resp.Data.Skipped != 1 {
		t.Errorf("skipped = %d; want 1", resp.Data.Skipped)
	}
	if resp.Data.Files != 2 {
		t.Errorf("files_indexed = %d; want 2", resp.Data.Files)
	}
	want := []LangRow{{Language: "go", Files: 1}, {Language: "typescript", Files: 1}}
	if !slices.Equal(resp.Data.Languages, want) {
		t.Errorf("languages = %+v; want %+v", resp.Data.Languages, want)
	}
}

// TestIndexPruneKeepsNewestFour proves prune keeps the four newest trees and
// only the shards they reference.
func TestIndexPruneKeepsNewestFour(t *testing.T) {
	f, trees := fiveTreeFixture(t)
	req := f.request(QueryPrune, nil)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("index prune refused: %v", ref)
	}
	if resp.Data.RemovedTree != 1 {
		t.Errorf("removed trees = %d; want 1", resp.Data.RemovedTree)
	}
	if len(resp.Data.Kept) != 4 {
		t.Errorf("kept = %v; want 4 trees", resp.Data.Kept)
	}
	if got := storeFiles(t, f.dir, "trees"); got != 4 {
		t.Errorf("tree files = %d; want 4", got)
	}
	if got := storeFiles(t, f.dir, "shards"); got != 4 {
		t.Errorf("shard files = %d; want 4", got)
	}
	assertPruneKept(t, trees, resp.Data.Kept)
}

// fiveTreeFixture builds five single-file trees with distinct mtimes.
func fiveTreeFixture(t *testing.T) (*fixture, []string) {
	t.Helper()
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n")
	var trees []string
	for i := range 5 {
		f.write("a/a.go", "package a\n\nfunc A"+strconv.Itoa(i)+"() {}\n")
		f.commit("tree " + strconv.Itoa(i))
		trees = append(trees, f.build().Data.Tree)
	}
	for i, tree := range trees {
		stamp := time.Unix(int64(1000+i), 0)
		path := filepath.Join(f.dir, ".verdict", "index", "trees", tree+".json")
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	return f, trees
}

// assertPruneKept checks the oldest tree was pruned and the rest survived.
func assertPruneKept(t *testing.T, trees, kept []string) {
	t.Helper()
	joined := strings.Join(kept, ",")
	for i, tree := range trees {
		contained := strings.Contains(joined, tree)
		if i == 0 && contained {
			t.Errorf("oldest tree %s survived prune", tree)
		}
		if i > 0 && !contained {
			t.Errorf("newer tree %s was pruned", tree)
		}
	}
}

// TestIndexRenderedPathsAreEscaped proves a path with a newline and a fence
// delimiter renders safely in human output.
func TestIndexRenderedPathsAreEscaped(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n")
	f.write("a/a.go", "package a\n\nfunc Target() {}\n\nfunc Caller() {\n\tTarget()\n}\n")
	f.commitObject("a/a\nb```.go", "package a\n\nfunc Other() {\n\tTarget()\n}\n", "one")
	f.build()
	resp := f.callers()
	if len(resp.Human) != 3 {
		t.Fatalf("human lines = %q; want summary and two rows", resp.Human)
	}
	want := "\"a/a\\nb```.go\":4 call (exact)"
	found := false
	for _, line := range resp.Human {
		if strings.Contains(line, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("human lines = %q; want one containing %q", resp.Human, want)
	}
}

// TestIndexStatusNextFollowsTheBuildState proves status never sends the
// operator back to a build that already happened: no tree for HEAD names the
// build, an indexed HEAD names the query the index can answer.
func TestIndexStatusNextFollowsTheBuildState(t *testing.T) {
	f := newFixture(t)
	f.write("go.mod", "module example.com/m\n")
	f.write("a/a.go", "package a\n\nfunc A() {}\n")
	f.commit("one")
	before, ref := Run(context.Background(), f.request(QueryStatus, nil))
	if ref != nil {
		t.Fatalf("index status refused: %v", ref)
	}
	if before.Data.Built {
		t.Fatal("Built = true before any build")
	}
	if before.Next != "build it with verdict index build" {
		t.Errorf("unbuilt next = %q; want the build step", before.Next)
	}
	f.build()
	after, ref := Run(context.Background(), f.request(QueryStatus, nil))
	if ref != nil {
		t.Fatalf("index status refused after build: %v", ref)
	}
	if !after.Data.Built {
		t.Fatal("Built = false after a build of HEAD's tree")
	}
	if after.Next != "query it with verdict index callers <symbol>" {
		t.Errorf("built next = %q; want the query step", after.Next)
	}
}
