package indexcore

import (
	"bytes"
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
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

// newFixture creates an empty repository on main.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), day: 1}
	f.git("init", "-q", "-b", "main")
	return f
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
	f.git("update-index", "--add", "--cacheinfo", "100644,"+blob+","+name)
	f.git("commit", "-qm", msg)
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

// callers runs one callers query against the fixture.
func (f *fixture) callers(symbol string) *Response {
	f.t.Helper()
	req := f.request(QueryCallers, nil)
	req.Symbol = symbol
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		f.t.Fatalf("index callers refused: %v", ref)
	}
	return resp
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
// for a git stream larger than the runner keeps.
type cappedCommander struct{}

func (cappedCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{}, &core.OutputCappedError{Stream: "stdout", Limit: 101711872}
}

// TestGitBatchCaptureCapRefuses proves an over-cap cat-file stream refuses as
// index-capped naming the cap, never index-failed pointing at a rebuild.
func TestGitBatchCaptureCapRefuses(t *testing.T) {
	st := &state{req: &Request{ReadRequest: core.ReadRequest{Runtime: &core.Runtime{
		Commander: cappedCommander{},
		Env:       map[string]string{},
	}}}}
	_, ref := st.gitBatch(context.Background(), ".", []string{"0f1e2d3c"})
	want := "git cat-file output exceeded the 101711872-byte capture cap"
	if ref == nil {
		t.Fatal("over-cap batch succeeded")
	}
	if ref.Code != "index-capped" || ref.Message != want {
		t.Fatalf("refusal = %q: %q; want index-capped: %q", ref.Code, ref.Message, want)
	}
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
	truncated, ref := readTree(store, low.Data.Tree)
	if ref != nil {
		t.Fatalf("read capped tree: %v", ref)
	}
	if len(truncated.Files) != 1 || !truncated.Files[0].RefsTruncated || len(truncated.Files[0].Refs) != 1 {
		t.Fatalf("capped tree file = %+v; want one truncated reference", truncated.Files)
	}

	full := f.build()
	if full.Data.Parsed != 1 {
		t.Errorf("raising the cap parsed %d shards; want 1", full.Data.Parsed)
	}
	reparsed, ref := readTree(store, full.Data.Tree)
	if ref != nil {
		t.Fatalf("read full tree: %v", ref)
	}
	refs := reparsed.Files[0].Refs
	if reparsed.Files[0].RefsTruncated || len(refs) != 2 || refs[0].Name != "One" || refs[1].Name != "Two" {
		t.Errorf("reparsed refs = %+v (truncated %t); want One and Two", refs, reparsed.Files[0].RefsTruncated)
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
	resp := f.callers("a.Target")
	want := []Row{
		{Path: "a/a.go", Line: 6, Kind: KindCall, Resolution: "exact"},
		{Path: "b/b.go", Line: 6, Kind: KindCall, Resolution: "lexical"},
	}
	if len(resp.Data.Rows) != len(want) {
		t.Fatalf("rows = %+v; want %+v", resp.Data.Rows, want)
	}
	for i := range want {
		if resp.Data.Rows[i] != want[i] {
			t.Errorf("row %d = %+v; want %+v", i, resp.Data.Rows[i], want[i])
		}
	}
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
	resp := f.callers("a.Target")
	for _, row := range resp.Data.Rows {
		if strings.HasPrefix(row.Path, "d/") {
			t.Errorf("package d matched without an import edge: %+v", row)
		}
	}
	if resp.Data.Callers != 2 {
		t.Errorf("callers = %d; want 2", resp.Data.Callers)
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
	resp := f.callers("a.Target")
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
		if _, ref := Run(context.Background(), req); ref == nil {
			t.Fatal("unmatched qualifier returned a response")
		} else if ref.Code != "bad-flags" ||
			ref.Message != "package qualifier nosuchpkg matches no package directory or import path" {
			t.Fatalf("refusal = %q: %q; want bad-flags naming the qualifier", ref.Code, ref.Message)
		}
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
		if _, ref := Run(context.Background(), req); ref == nil {
			t.Fatal("ambiguous qualifier returned a response")
		} else if ref.Code != "bad-flags" || ref.Message != "package qualifier x is ambiguous" {
			t.Fatalf("refusal = %q: %q; want bad-flags ambiguous", ref.Code, ref.Message)
		}
	})
}

// TestIndexCapRefusal proves the file-count cap names the measured number and
// an oversized file is skipped and counted.
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
		want := "3 package files exceeds the 2-file cap"
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
		want := "3 package files exceeds the 2-file cap"
		if ref.Code != "index-capped" || ref.Message != want {
			t.Fatalf("refusal = %q: %q; want index-capped: %q", ref.Code, ref.Message, want)
		}
	})
	t.Run("oversized file skipped", func(t *testing.T) {
		f := newFixture(t)
		f.write("go.mod", "module example.com/m\n")
		f.write("a/a.go", "package a\n\nfunc A() {}\n")
		f.write("a/big.go", "package a\n\n//"+strings.Repeat(" pad", 600000)+"\n")
		f.commit("one")
		resp := f.build()
		if resp.Data.Skipped != 1 {
			t.Errorf("skipped = %d; want 1", resp.Data.Skipped)
		}
		if resp.Data.Files != 1 {
			t.Errorf("files_indexed = %d; want 1", resp.Data.Files)
		}
	})
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
	resp := f.callers("a.Target")
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
