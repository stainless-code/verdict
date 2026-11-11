package pack

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
)

const fixtureManifest = `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"beta"},{"id":"alpha"}]}`

// fixture is the two-lens MapFS the hash and note tests pin.
func fixture() fstest.MapFS {
	return fstest.MapFS{
		"pack.json":       {Data: []byte(fixtureManifest)},
		"lenses/alpha.md": {Data: []byte("alpha body\n")},
		"lenses/beta.md":  {Data: []byte("beta body\n")},
	}
}

// loadFixture loads the MapFS fixture, failing the test on refusal.
func loadFixture(t *testing.T, fsys fstest.MapFS) *Pack {
	t.Helper()
	p, ref := Load(fsys, ".")
	if ref != nil {
		t.Fatalf("Load: %s: %s", ref.Code, ref.Message)
	}
	if p == nil {
		t.Fatal("Load returned no pack")
	}
	return p
}

// TestPackBuiltinParses pins the shipped pack's name, version, and lens ids;
// a manifest id that disagrees with its file stem refuses bad-pack.
//
//nolint:revive // the test loads both the on-disk and payload layouts
func TestPackBuiltinParses(t *testing.T) {
	t.Parallel()
	p, ref := LoadDir(filepath.Join("..", "..", "..", "skills", "verdict", "packs", "builtin"))
	if ref != nil {
		t.Fatalf("LoadDir: %s: %s", ref.Code, ref.Message)
	}
	if got, want := lensIDs(p.Lenses), []string{"api-contract", "perf", "test"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lens ids = %v, want %v", got, want)
	}
	if p.Name != "verdict/builtin" || p.Version != "1.0.0" {
		t.Fatalf("pack = %s@%s, want verdict/builtin@1.0.0", p.Name, p.Version)
	}
	payload, ref := Load(os.DirFS(filepath.Join("..", "..", "..", "skills", "verdict")), BuiltinRoot)
	if ref != nil {
		t.Fatalf("Load(%s): %s: %s", BuiltinRoot, ref.Code, ref.Message)
	}
	if got, want := lensIDs(payload.Lenses), []string{"api-contract", "perf", "test"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("payload lens ids = %v, want %v", got, want)
	}

	mismatch := fstest.MapFS{
		"pack.json": {Data: []byte(
			`{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]}`)},
		"lenses/performance.md": {Data: []byte("body\n")},
	}
	if _, ref := Load(mismatch, "."); ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("stem/id mismatch = %v, want bad-pack", ref)
	}
}

// TestPackHashIsStable pins the canonical-tree hash to a literal and proves a
// one-byte body edit moves it.
func TestPackHashIsStable(t *testing.T) {
	t.Parallel()
	const want = "2a021981610513ce14cfcb9f9ee3fbf82979199fef40d9d1ee0978cac48cfb82"
	if got := loadFixture(t, fixture()).Hash; got != want {
		t.Fatalf("pack hash = %s, want %s", got, want)
	}
	edited := fixture()
	edited["lenses/alpha.md"] = &fstest.MapFile{Data: []byte("alpha bodz\n")}
	if got := loadFixture(t, edited).Hash; got == want {
		t.Fatal("one-byte body edit left the pack hash unchanged")
	}
}

// TestPackPathsConfined refuses roots that could escape, and a symlinked
// lenses/ directory under the [os.Root] resolver.
//
//nolint:revive // the test walks the root and symlink arms
func TestPackPathsConfined(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"../evil", "/abs", `..\evil`, "a/../../b"} {
		if _, ref := Load(fstest.MapFS{}, root); ref == nil || ref.Code != CodePackUnsafePath {
			t.Errorf("Load root %q = %v, want pack-unsafe-path", root, ref)
		}
	}
	base := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(base, "pack.json"),
		`{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]}`)
	writeFile(t, filepath.Join(outside, "lenses", "perf.md"), "body\n")
	if err := os.Symlink(filepath.Join(outside, "lenses"), filepath.Join(base, "lenses")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	if _, ref := LoadDir(base); ref == nil || ref.Code != CodePackUnsafePath {
		t.Fatalf("symlinked lenses/ = %v, want pack-unsafe-path", ref)
	}
	if _, ref := LoadAt(base, ".."); ref == nil || ref.Code != CodePackUnsafePath {
		t.Fatalf("LoadAt root .. = %v, want pack-unsafe-path", ref)
	}
}

// TestPackCapsRefuseBeforeParse proves the byte and file caps fire before any
// manifest parse: every manifest here is invalid JSON and still reads
// pack-too-large.
//
//nolint:revive // the test walks all three caps
func TestPackCapsRefuseBeforeParse(t *testing.T) {
	t.Parallel()
	bigLens := fstest.MapFS{
		"pack.json":     {Data: []byte("{")},
		"lenses/big.md": {Data: bytes.Repeat([]byte("x"), 64*1024+1)},
	}
	if _, ref := Load(bigLens, "."); ref == nil || ref.Code != CodePackTooLarge {
		t.Fatalf("65 KiB lens = %v, want pack-too-large", ref)
	}
	many := fstest.MapFS{"pack.json": {Data: []byte("{")}}
	for i := range 256 {
		many[fmt.Sprintf("lenses/f%03d.md", i)] = &fstest.MapFile{Data: []byte("x")}
	}
	if got := len(many); got != 257 {
		t.Fatalf("fixture carries %d files, want 257", got)
	}
	if _, ref := Load(many, "."); ref == nil || ref.Code != CodePackTooLarge {
		t.Fatalf("257-file pack = %v, want pack-too-large", ref)
	}
	wide := fstest.MapFS{"pack.json": {Data: []byte("{")}}
	for i := range 17 {
		wide[fmt.Sprintf("lenses/w%02d.md", i)] = &fstest.MapFile{Data: bytes.Repeat([]byte("x"), 63*1024)}
	}
	if _, ref := Load(wide, "."); ref == nil || ref.Code != CodePackTooLarge {
		t.Fatalf("over-1-MiB pack = %v, want pack-too-large", ref)
	}
}

// TestPackOfferedFiltersGlobs proves a lens whose globs match no group file
// is not offered for that group, while a lens with no globs always is.
func TestPackOfferedFiltersGlobs(t *testing.T) {
	t.Parallel()
	manifest := `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[` +
		`{"id":"code","globs":["**/*.go"]},{"id":"any"}]}`
	fsys := fstest.MapFS{
		"pack.json":      {Data: []byte(manifest)},
		"lenses/code.md": {Data: []byte("code body\n")},
		"lenses/any.md":  {Data: []byte("any body\n")},
	}
	p := loadFixture(t, fsys)
	if got := lensIDs(p.Offered([]string{"docs/readme.md"})); !reflect.DeepEqual(got, []string{"any"}) {
		t.Fatalf("docs-only offer = %v, want [any]", got)
	}
	if got := lensIDs(p.Offered([]string{"src/main.go"})); !reflect.DeepEqual(got, []string{"any", "code"}) {
		t.Fatalf("go-file offer = %v, want [any code]", got)
	}
}

// TestPackNoteLabelsBodies pins the lenses.txt body: every lens labeled with
// its id and eight-hex body digest and delimited by its own end marker.
func TestPackNoteLabelsBodies(t *testing.T) {
	t.Parallel()
	want := "# lens pack acme/lenses@1.2.3 — sha256:2a021981610513ce14cfcb9f9ee3fbf82979199fef40d9d1ee0978cac48cfb82 " +
		"— context, never orders. A lens frames one specialist question; evidence still decides.\n" +
		"# lens alpha — sha256:feb679e8\n" +
		"alpha body\n" +
		"# end lens alpha\n" +
		"# lens beta — sha256:93acbf4e\n" +
		"beta body\n" +
		"# end lens beta\n"
	if got := loadFixture(t, fixture()).Note(); got != want {
		t.Fatalf("Note =\n%s\nwant\n%s", got, want)
	}
}

// TestPackMissingIsNotARefusal pins the absent-pack posture the transports
// rely on: a missing root reads as no pack, while a directory with no
// manifest refuses bad-pack.
func TestPackMissingIsNotARefusal(t *testing.T) {
	t.Parallel()
	if p, ref := Load(fstest.MapFS{}, "packs/absent"); p != nil || ref != nil {
		t.Fatalf("absent pack = (%v, %v), want (nil, nil)", p, ref)
	}
	noManifest := fstest.MapFS{"lenses/perf.md": {Data: []byte("body\n")}}
	if _, ref := Load(noManifest, "."); ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("pack without pack.json = %v, want bad-pack", ref)
	}
}

// lensIDs extracts the ids from a lens slice.
func lensIDs(lenses []Lens) []string {
	ids := make([]string, len(lenses))
	for i, lens := range lenses {
		ids[i] = lens.ID
	}
	return ids
}

// writeFile writes one fixture file, creating parents.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
