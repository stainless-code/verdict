package pack

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	if _, ref := LoadDir(base); ref == nil || ref.Code != "pack-unsafe-path" {
		t.Errorf("symlinked lenses/ = %v, want pack-unsafe-path", ref)
	}
	if _, ref := LoadAt(base, ".."); ref == nil || ref.Code != CodePackUnsafePath {
		t.Fatalf("LoadAt root .. = %v, want pack-unsafe-path", ref)
	}
}

// TestPackSymlinkFilesRefuse pins the file-level symlink refusal under the
// [os.Root] resolver: a symlinked manifest or lens body is pack-unsafe-path,
// never a silent read through the link.
func TestPackSymlinkFilesRefuse(t *testing.T) {
	t.Parallel()
	manifestBase := t.TempDir()
	manifestOutside := filepath.Join(t.TempDir(), "pack.json")
	writeFile(t, manifestOutside,
		`{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]}`)
	if err := os.Symlink(manifestOutside, filepath.Join(manifestBase, "pack.json")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	writeFile(t, filepath.Join(manifestBase, "lenses", "perf.md"), "body\n")
	if _, ref := LoadDir(manifestBase); ref == nil || ref.Code != "pack-unsafe-path" {
		t.Errorf("symlinked pack.json = %v, want pack-unsafe-path", ref)
	}
	lensBase := t.TempDir()
	writeFile(t, filepath.Join(lensBase, "pack.json"),
		`{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]}`)
	lensOutside := filepath.Join(t.TempDir(), "perf.md")
	writeFile(t, lensOutside, "body\n")
	if err := os.MkdirAll(filepath.Join(lensBase, "lenses"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(lensOutside, filepath.Join(lensBase, "lenses", "perf.md")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	if _, ref := LoadDir(lensBase); ref == nil || ref.Code != "pack-unsafe-path" {
		t.Errorf("symlinked lens body = %v, want pack-unsafe-path", ref)
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

// TestCheckOrgEntryRefusesOnlyRepoPacks proves an org packs[] entry is refused
// exactly when it resolves inside the repo layer's tree; a sibling name with
// the same prefix and an outside path stay legal.
func TestCheckOrgEntryRefusesOnlyRepoPacks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, listed := range []string{
		".verdict/packs",
		".verdict/packs/acme",
		".verdict/other/../packs/acme",
	} {
		ref := CheckOrgEntry(root, listed)
		if ref == nil || ref.Code != CodeBadPack {
			t.Errorf("%s = %v, want bad-pack", listed, ref)
			continue
		}
		if want := fmt.Sprintf("%q", listed); !strings.Contains(ref.Message, want) {
			t.Errorf("message = %q, want it to name %s", ref.Message, want)
		}
	}
	for _, listed := range []string{"org-packs", ".verdict/packs-extra/acme"} {
		if ref := CheckOrgEntry(root, listed); ref != nil {
			t.Errorf("%s = %s: %s, want no refusal", listed, ref.Code, ref.Message)
		}
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
	if p, ref := Load(fstest.MapFS{}, "."); p != nil || ref != nil {
		t.Fatalf("empty pack dir = (%v, %v), want (nil, nil)", p, ref)
	}
	if p, ref := Load(fstest.MapFS{}, "packs/absent"); p != nil || ref != nil {
		t.Fatalf("absent pack = (%v, %v), want (nil, nil)", p, ref)
	}
	noManifest := fstest.MapFS{"lenses/perf.md": {Data: []byte("body\n")}}
	if _, ref := Load(noManifest, "."); ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("pack without pack.json = %v, want bad-pack", ref)
	}
}

// TestPackRejectsMalformedManifest pins every manifest rule the published
// schema states: unknown fields refuse through the strict decoder, trailing
// data refuses, identity fields keep their formats, lens ids stay unique
// lowercase slugs, and a present cap stays inside 1..24 / 1..20.
//
//nolint:revive // the table is the manifest rule matrix
func TestPackRejectsMalformedManifest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name:     "unknown top-level field",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}],"extra":true}`,
			want:     `carries key "extra"`,
		},
		{
			name:     "unknown lens field",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf","scope":"hot"}]}`,
			want:     `carries key "scope"`,
		},
		{
			name:     "trailing data",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]} {}`,
			want:     "trailing data",
		},
		{
			name:     "duplicate lens id",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"},{"id":"perf"}]}`,
			want:     `repeats lens id "perf"`,
		},
		{
			name:     "non-slug lens id",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"Perf Lens"}]}`,
			want:     `lens id "Perf Lens" is not a lowercase slug`,
		},
		{
			name:     "bad name",
			manifest: `{"schema_version":"1","name":"acme","version":"1.2.3","lenses":[{"id":"perf"}]}`,
			want:     `name "acme" is not <namespace>/<slug>`,
		},
		{
			name:     "bad version",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2","lenses":[{"id":"perf"}]}`,
			want:     `version "1.2" is not semver`,
		},
		{
			name:     "bad schema_version",
			manifest: `{"schema_version":"2","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]}`,
			want:     `schema_version is "2", want "1"`,
		},
		{
			name:     "max_files below one",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf","max_files":0}]}`,
			want:     `lens "perf" max_files must be 1..24`,
		},
		{
			name:     "max_files above twenty-four",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf","max_files":25}]}`,
			want:     `lens "perf" max_files must be 1..24`,
		},
		{
			name:     "max_findings below one",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf","max_findings":0}]}`,
			want:     `lens "perf" max_findings must be 1..20`,
		},
		{
			name:     "max_findings above twenty",
			manifest: `{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf","max_findings":21}]}`,
			want:     `lens "perf" max_findings must be 1..20`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{
				"pack.json":      {Data: []byte(tc.manifest)},
				"lenses/perf.md": {Data: []byte("perf body\n")},
			}
			_, ref := Load(fsys, ".")
			if ref == nil || ref.Code != "bad-pack" {
				t.Fatalf("Load = %v, want bad-pack", ref)
			}
			if !strings.Contains(ref.Message, tc.want) {
				t.Fatalf("message = %q, want it to contain %q", ref.Message, tc.want)
			}
		})
	}
}

// TestPackRejectsMalformedRules pins the rule manifest rules: a KB id, a
// severity key that exists in the pack map, required globs, unique ids, and a
// body at rules/<id>.md.
//
//nolint:revive // the table is the rule manifest matrix
func TestPackRejectsMalformedRules(t *testing.T) {
	t.Parallel()
	validHeader := `"schema_version":"1","name":"acme/rules","version":"1.0.0",` +
		`"severity":{"secrets":"critical"},`
	base := `, "lenses":[{"id":"perf"}]`
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name:     "unknown severity key",
			manifest: `{` + validHeader + `"rules":[{"id":"r1","severity":"missing","globs":["**/*.go"]}]` + base + `}`,
			want:     `severity "missing" is not in the severity map`,
		},
		{
			name:     "bad severity value",
			manifest: `{"schema_version":"1","name":"acme/rules","version":"1.0.0","severity":{"s":"high"},"lenses":[{"id":"perf"}],"rules":[{"id":"r1","severity":"s","globs":["**/*.go"]}]}`,
			want:     `severity "s" is "high"`,
		},
		{
			name:     "no globs",
			manifest: `{` + validHeader + `"rules":[{"id":"r1","severity":"secrets"}]` + base + `}`,
			want:     `rule "r1" names no globs`,
		},
		{
			name:     "empty glob",
			manifest: `{` + validHeader + `"rules":[{"id":"r1","severity":"secrets","globs":[""]}]` + base + `}`,
			want:     `rule "r1" carries an empty glob`,
		},
		{
			name:     "duplicate id",
			manifest: `{` + validHeader + `"rules":[{"id":"r1","severity":"secrets","globs":["**/*.go"]},{"id":"r1","severity":"secrets","globs":["**/*.go"]}]` + base + `}`,
			want:     `repeats rule id "r1"`,
		},
		{
			name:     "bad id",
			manifest: `{` + validHeader + `"rules":[{"id":"R1","severity":"secrets","globs":["**/*.go"]}]` + base + `}`,
			want:     `rule id "R1" is not a KB id`,
		},
		{
			name:     "empty tool",
			manifest: `{` + validHeader + `"rules":[{"id":"r1","severity":"secrets","globs":["**/*.go"],"tools":[""]}]` + base + `}`,
			want:     `rule "r1" carries an empty tool name`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{
				"pack.json":      {Data: []byte(tc.manifest)},
				"lenses/perf.md": {Data: []byte("perf body\n")},
				"rules/r1.md":    {Data: []byte("rule body\n")},
			}
			_, ref := Load(fsys, ".")
			if ref == nil || ref.Code != CodeBadPack {
				t.Fatalf("Load = %v, want bad-pack", ref)
			}
			if !strings.Contains(ref.Message, tc.want) {
				t.Fatalf("message = %q, want it to contain %q", ref.Message, tc.want)
			}
		})
	}
}

// TestPackRefusesMissingOrEmptyRuleBody proves a declared rule needs its own
// non-empty markdown body.
//
//nolint:revive // the two-body matrix reads as one block
func TestPackRefusesMissingOrEmptyRuleBody(t *testing.T) {
	t.Parallel()
	manifest := `{"schema_version":"1","name":"acme/rules","version":"1.0.0",` +
		`"severity":{"s":"critical"},"lenses":[{"id":"perf"}],` +
		`"rules":[{"id":"r1","severity":"s","globs":["**/*.go"]}]}`
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "missing", body: "", want: `rule "r1" has no rules/r1.md`},
		{name: "empty", body: " \n\t\n", want: `rule "r1" has an empty body at rules/r1.md`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{
				"pack.json":      {Data: []byte(manifest)},
				"lenses/perf.md": {Data: []byte("perf body\n")},
			}
			if tc.body != "" {
				fsys["rules/r1.md"] = &fstest.MapFile{Data: []byte(tc.body)}
			}
			_, ref := Load(fsys, ".")
			if ref == nil || ref.Code != CodeBadPack {
				t.Fatalf("Load = %v, want bad-pack", ref)
			}
			if ref.Message != tc.want {
				t.Fatalf("message = %q, want %q", ref.Message, tc.want)
			}
		})
	}
}

// TestPackRefusesEmptyLensBody pins the refusal for a body that carries no
// content: a manifest row with only whitespace has nothing to dispatch.
func TestPackRefusesEmptyLensBody(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", " \n\t\n"} {
		fsys := fstest.MapFS{
			"pack.json": {Data: []byte(
				`{"schema_version":"1","name":"acme/lenses","version":"1.2.3","lenses":[{"id":"perf"}]}`)},
			"lenses/perf.md": {Data: []byte(body)},
		}
		_, ref := Load(fsys, ".")
		if ref == nil || ref.Code != "bad-pack" {
			t.Fatalf("body %q = %v, want bad-pack", body, ref)
		}
		if want := `lens "perf" has an empty body at lenses/perf.md`; ref.Message != want {
			t.Fatalf("message = %q, want %q", ref.Message, want)
		}
	}
}

// lensIDs extracts the ids from a lens slice.
func lensIDs(lenses []Lens) []string {
	ids := make([]string, len(lenses))
	for i := range lenses {
		ids[i] = lenses[i].ID
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
