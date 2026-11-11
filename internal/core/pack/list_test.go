package pack

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

const builtinListManifest = `{"schema_version":"1","name":"verdict/builtin","version":"1.0.0","lenses":[{"id":"perf"},{"id":"test"}]}`

// listFixtures writes the org and repo layer packs and returns the repo root
// and the personal settings path.
func listFixtures(t *testing.T, repoExtraManifest string) (string, string) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "org-packs"), map[string]string{
		"pack.json": `{"schema_version":"1","name":"acme/org","version":"2.0.0",` +
			`"severity":{"security":"critical"},"lenses":[{"id":"security"}],` +
			`"rules":[{"id":"no-secrets","severity":"security","globs":["**/*.go"]}]}`,
		"lenses/security.md":  "security body\n",
		"rules/no-secrets.md": "never log a secret\n",
	})
	writeFixture(t, filepath.Join(root, ".verdict", "packs", "repo"), map[string]string{
		"pack.json": `{"schema_version":"1","name":"acme/repo","version":"0.1.0",` +
			repoExtraManifest +
			`"severity":{"style":"important"},"lenses":[{"id":"security"},{"id":"style"}],` +
			`"rules":[{"id":"repo-style","severity":"style","globs":["**/*.go"],"tools":["gofmt"]}]}`,
		"lenses/security.md":  "repo security body\n",
		"lenses/style.md":     "repo style body\n",
		"rules/repo-style.md": "run gofmt\n",
	})
	home := t.TempDir()
	settings := config.SettingsPath(home, "")
	writeJSON(t, settings, `{"packs":["org-packs"]}`)
	return root, settings
}

// listBuiltin is the embedded-layer fixture the list tests share.
func listBuiltin() fstest.MapFS {
	return fstest.MapFS{
		"packs/builtin/pack.json":      {Data: []byte(builtinListManifest)},
		"packs/builtin/lenses/perf.md": {Data: []byte("perf body\n")},
		"packs/builtin/lenses/test.md": {Data: []byte("test body\n")},
	}
}

// TestListThreeLayers pins the rows for a fixture with all three layers:
// names, versions, layers, sources, tree hashes, and declared ids.
func TestListThreeLayers(t *testing.T) {
	t.Parallel()
	root, settings := listFixtures(t, `"replace":["acme/org"],`)
	resp, ref := RunList(t.Context(), &Request{
		core.ReadRequest{Runtime: &core.Runtime{PackFS: listBuiltin()}, RepoRoot: root},
		settings,
		true,
	})
	if ref != nil {
		t.Fatalf("RunList: %s: %s", ref.Code, ref.Message)
	}
	want := []Entry{
		{
			Name: "verdict/builtin", Version: "1.0.0", Layer: "builtin", Source: "builtin",
			Hash:   "0fd2392e7627c05b9589946f78037d7c9dd3feeb61bb43b846c5f3bb529767d2",
			Lenses: []string{"perf", "test"}, Rules: []string{},
		},
		{
			Name: "acme/org", Version: "2.0.0", Layer: "org", Source: "dir:org-packs",
			Hash:   "ec7b01d4ea7b319a383c633bb8c30b464ec5c9734c9b06c6db0ef6dec8218d7b",
			Lenses: []string{"security"}, Rules: []string{"no-secrets"},
		},
		{
			Name: "acme/repo", Version: "0.1.0", Layer: "repo", Source: ".verdict/packs/repo",
			Hash:   "6017f42729f688b65d17401a27200f0406c1cf8b75d588ce65788850d7817edf",
			Lenses: []string{"security", "style"}, Rules: []string{"repo-style"},
		},
	}
	if len(resp.Data.Packs) != len(want) {
		t.Fatalf("rows = %d, want %d: %+v", len(resp.Data.Packs), len(want), resp.Data.Packs)
	}
	for i := range want {
		requirePackRow(t, i, &resp.Data.Packs[i], &want[i])
	}
	if got, want := resp.Human[0], "builtin  verdict/builtin@1.0.0  source=builtin  "+
		"sha256:0fd2392e7627  lenses=perf,test  rules=-"; got != want {
		t.Errorf("human row = %q, want %q", got, want)
	}
}

// TestListWithoutRuntime proves a caller with no injected runtime reads as an
// absent built-in layer instead of dereferencing a nil Runtime.
func TestListWithoutRuntime(t *testing.T) {
	t.Parallel()
	resp, ref := RunList(t.Context(), &Request{
		core.ReadRequest{RepoRoot: t.TempDir()}, "", true,
	})
	if ref != nil {
		t.Fatalf("RunList: %s: %s", ref.Code, ref.Message)
	}
	if len(resp.Data.Packs) != 0 {
		t.Fatalf("packs = %v, want none", resp.Data.Packs)
	}
}

// TestListMissingOrgPathRefuses proves a personal settings path that carries
// no pack refuses typed instead of reading as an empty org layer.
func TestListMissingOrgPathRefuses(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settings := config.SettingsPath(home, "")
	writeJSON(t, settings, `{"packs":["absent"]}`)
	_, ref := RunList(t.Context(), &Request{
		core.ReadRequest{Runtime: &core.Runtime{PackFS: listBuiltin()}, RepoRoot: t.TempDir()},
		settings,
		true,
	})
	if ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("missing org path = %v, want bad-pack", ref)
	}
}

// TestListRefusesOrgEntryInsideRepoPacks proves a packs[] entry pointing into
// the repo layer refuses bad-pack and names the entry: that tree is read from
// the PR base, so listing it as the org layer would read head bytes.
func TestListRefusesOrgEntryInsideRepoPacks(t *testing.T) {
	t.Parallel()
	root, _ := listFixtures(t, "")
	home := t.TempDir()
	settings := config.SettingsPath(home, "")
	writeJSON(t, settings, `{"packs":[".verdict/packs/repo"]}`)
	_, ref := RunList(t.Context(), &Request{
		core.ReadRequest{Runtime: &core.Runtime{PackFS: listBuiltin()}, RepoRoot: root},
		settings,
		true,
	})
	if ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("org entry inside repo packs = %v, want bad-pack", ref)
	}
	want := `org pack path ".verdict/packs/repo" is inside .verdict/packs`
	if !strings.Contains(ref.Message, want) {
		t.Fatalf("message = %q, want it to contain %q", ref.Message, want)
	}
}

// TestListUndeclaredOverrideRefuses proves pack list surfaces the same
// pack-conflict fetch does.
func TestListUndeclaredOverrideRefuses(t *testing.T) {
	t.Parallel()
	root, settings := listFixtures(t, `"replace":["acme/org"],`)
	if err := os.WriteFile(filepath.Join(root, ".verdict", "packs", "repo", "pack.json"), []byte(
		`{"schema_version":"1","name":"acme/repo","version":"0.1.0","lenses":[{"id":"perf"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".verdict", "packs", "repo", "lenses", "security.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, ".verdict", "packs", "repo", "lenses", "style.md")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(root, ".verdict", "packs", "repo"), map[string]string{
		"lenses/perf.md": "repo perf\n",
	})
	_, ref := RunList(t.Context(), &Request{
		core.ReadRequest{Runtime: &core.Runtime{PackFS: listBuiltin()}, RepoRoot: root},
		settings,
		true,
	})
	if ref == nil || ref.Code != CodePackConflict {
		t.Fatalf("undeclared override = %v, want pack-conflict", ref)
	}
}

// TestListIgnoresStrayFileUnderRepoPacks proves a regular file beside the pack
// directories is not a pack root: the report warns once, names it, and still
// lists every directory pack.
func TestListIgnoresStrayFileUnderRepoPacks(t *testing.T) {
	t.Parallel()
	root, settings := listFixtures(t, `"replace":["acme/org"],`)
	writeFixture(t, root, map[string]string{".verdict/packs/README.md": "not a pack\n"})
	resp, ref := RunList(t.Context(), &Request{
		core.ReadRequest{Runtime: &core.Runtime{PackFS: listBuiltin()}, RepoRoot: root},
		settings,
		true,
	})
	if ref != nil {
		t.Fatalf("RunList: %s: %s", ref.Code, ref.Message)
	}
	wantWarnings := []string{".verdict/packs/README.md is not a pack directory — ignored"}
	if !slices.Equal(resp.Warnings, wantWarnings) {
		t.Fatalf("warnings = %q, want %q", resp.Warnings, wantWarnings)
	}
	if got, want := len(resp.Data.Packs), 3; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
}

// requirePackRow compares one list row with its literal expectation.
func requirePackRow(t *testing.T, i int, got, want *Entry) {
	t.Helper()
	if got.Name != want.Name || got.Version != want.Version || got.Layer != want.Layer ||
		got.Source != want.Source || got.Hash != want.Hash {
		t.Errorf("row %d = %+v, want %+v", i, got, want)
	}
	if !slices.Equal(got.Lenses, want.Lenses) {
		t.Errorf("row %d lenses = %v, want %v", i, got.Lenses, want.Lenses)
	}
	if !slices.Equal(got.Rules, want.Rules) {
		t.Errorf("row %d rules = %v, want %v", i, got.Rules, want.Rules)
	}
}

// writeFixture writes a map of slash-relative files under dir.
func writeFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// writeJSON writes one fixture file, creating parents.
func writeJSON(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
