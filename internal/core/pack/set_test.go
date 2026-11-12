package pack

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// mergePack loads one MapFS fixture at its root.
func mergePack(t *testing.T, fsys fstest.MapFS) *Pack {
	t.Helper()
	p, ref := Load(fsys, ".")
	if ref != nil {
		t.Fatalf("load: %s: %s", ref.Code, ref.Message)
	}
	if p == nil {
		t.Fatal("load: no pack")
	}
	return p
}

// packFixture renders one pack with the given lens ids and optional extra
// manifest fields.
func packFixture(name, version, extra string, lensIDs ...string) fstest.MapFS {
	rows := make([]string, len(lensIDs))
	for i, id := range lensIDs {
		rows[i] = fmt.Sprintf(`{"id":%q}`, id)
	}
	manifest := fmt.Sprintf(
		`{"schema_version":"1","name":%q,"version":%q,%s"lenses":[%s]}`,
		name, version, extra, strings.Join(rows, ","))
	fsys := fstest.MapFS{"pack.json": {Data: []byte(manifest)}}
	for _, id := range lensIDs {
		fsys["lenses/"+id+".md"] = &fstest.MapFile{Data: []byte(id + " body\n")}
	}
	return fsys
}

// TestMergeLayerPrecedence proves a repo pack replaces an org lens it names
// and the merged lens carries the winning pack's tag.
func TestMergeLayerPrecedence(t *testing.T) {
	t.Parallel()
	org := mergePack(t, packFixture("acme/org", "1.0.0", "", "security"))
	repo := mergePack(t, packFixture("acme/repo", "0.1.0", `"replace":["acme/org"],`, "security", "style"))
	set, ref := Merge([]Origin{
		{Layer: LayerOrg, Source: "dir:org", Pack: org},
		{Layer: LayerRepo, Source: ".verdict/packs/repo", Pack: repo},
	})
	if ref != nil {
		t.Fatalf("Merge: %s: %s", ref.Code, ref.Message)
	}
	if got, want := lensIDs(set.Lenses), []string{"security", "style"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lenses = %v, want %v", got, want)
	}
	security := set.Lenses[0]
	if security.Pack != "acme/repo" || security.PackSlug != "repo" || string(security.Body) != "security body\n" {
		t.Fatalf("winning security = %+v, want acme/repo", security)
	}
}

// TestMergeReplaceNamesOnlyLowerLayers proves a lower pack cannot replace a
// higher one.
func TestMergeReplaceNamesOnlyLowerLayers(t *testing.T) {
	t.Parallel()
	org := mergePack(t, packFixture("acme/org", "1.0.0", `"replace":["acme/repo"],`, "perf"))
	repo := mergePack(t, packFixture("acme/repo", "0.1.0", "", "perf"))
	_, ref := Merge([]Origin{
		{Layer: LayerOrg, Source: "dir:org", Pack: org},
		{Layer: LayerRepo, Source: ".verdict/packs/repo", Pack: repo},
	})
	if ref == nil || ref.Code != CodePackConflict {
		t.Fatalf("upward replace = %v, want pack-conflict", ref)
	}
	if !strings.Contains(ref.Message, "downward only") {
		t.Fatalf("message = %q, want the downward-only cause", ref.Message)
	}
}

// TestMergeUndeclaredOverrideRefuses proves a cross-layer id collision
// without replace refuses pack-conflict and names the declaration to add.
func TestMergeUndeclaredOverrideRefuses(t *testing.T) {
	t.Parallel()
	builtin := mergePack(t, packFixture("verdict/builtin", "1.0.0", "", "perf"))
	repo := mergePack(t, packFixture("acme/repo", "0.1.0", "", "perf"))
	_, ref := Merge([]Origin{
		{Layer: LayerBuiltin, Source: "builtin", Pack: builtin},
		{Layer: LayerRepo, Source: ".verdict/packs/repo", Pack: repo},
	})
	if ref == nil || ref.Code != CodePackConflict {
		t.Fatalf("undeclared override = %v, want pack-conflict", ref)
	}
	if !strings.Contains(ref.Message, `"replace": ["verdict/builtin"]`) {
		t.Fatalf("message = %q, want the exact declaration", ref.Message)
	}
}

// TestMergeSameLayerDuplicatesRefuse proves duplicate lens and rule ids inside
// one layer are ambiguous and refuse bad-pack.
func TestMergeSameLayerDuplicatesRefuse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		extra string
	}{
		{name: "lens", extra: ""},
		{name: "rule", extra: `"severity":{"s":"critical"},"rules":[{"id":"r1","severity":"s","globs":["**/*.go"]}],`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			first := mergePack(t, ruleFixture("acme/one", tc.extra, "one body\n"))
			second := mergePack(t, ruleFixture("acme/two", tc.extra, "two body\n"))
			_, ref := Merge([]Origin{
				{Layer: LayerOrg, Source: "dir:one", Pack: first},
				{Layer: LayerOrg, Source: "dir:two", Pack: second},
			})
			if ref == nil || ref.Code != CodeBadPack {
				t.Fatalf("same-layer duplicate = %v, want bad-pack", ref)
			}
		})
	}
}

// TestMergeDuplicatePackNameRefuses proves the same manifest name at two
// layers is ambiguous for replace resolution and refuses bad-pack.
func TestMergeDuplicatePackNameRefuses(t *testing.T) {
	t.Parallel()
	org := mergePack(t, packFixture("acme/lenses", "1.0.0", "", "perf"))
	repo := mergePack(t, packFixture("acme/lenses", "2.0.0", "", "style"))
	if _, ref := Merge([]Origin{
		{Layer: LayerOrg, Source: "dir:org", Pack: org},
		{Layer: LayerRepo, Source: ".verdict/packs/repo", Pack: repo},
	}); ref == nil || ref.Code != CodeBadPack {
		t.Fatalf("duplicate pack name = %v, want bad-pack", ref)
	}
}

// TestMergeUnknownReplaceRefuses proves a replace entry naming no loaded pack
// refuses pack-conflict rather than being ignored.
func TestMergeUnknownReplaceRefuses(t *testing.T) {
	t.Parallel()
	repo := mergePack(t, packFixture("acme/repo", "0.1.0", `"replace":["acme/ghost"],`, "perf"))
	if _, ref := Merge([]Origin{{Layer: LayerRepo, Source: ".verdict/packs/repo", Pack: repo}}); ref == nil ||
		ref.Code != CodePackConflict {
		t.Fatalf("unknown replace = %v, want pack-conflict", ref)
	}
}

// TestMergeRuleOverrideNeedsReplace proves rule ids follow the same downward
// replacement rule as lenses.
func TestMergeRuleOverrideNeedsReplace(t *testing.T) {
	t.Parallel()
	extra := `"severity":{"s":"critical"},"rules":[{"id":"r1","severity":"s","globs":["**/*.go"]}],`
	builtin := mergePack(t, ruleFixture("verdict/builtin", extra, "rule one\n"))
	repo := mergePack(t, ruleFixture("acme/repo", extra, "rule two\n"))
	if _, ref := Merge([]Origin{
		{Layer: LayerBuiltin, Source: "builtin", Pack: builtin},
		{Layer: LayerRepo, Source: ".verdict/packs/repo", Pack: repo},
	}); ref == nil || ref.Code != CodePackConflict {
		t.Fatalf("undeclared rule override = %v, want pack-conflict", ref)
	}
	authorized := mergePack(t, ruleFixture("acme/repo", `"replace":["verdict/builtin"],`+extra, "rule two\n"))
	set, ref := Merge([]Origin{
		{Layer: LayerBuiltin, Source: "builtin", Pack: builtin},
		{Layer: LayerRepo, Source: ".verdict/packs/repo", Pack: authorized},
	})
	if ref != nil {
		t.Fatalf("authorized rule override: %s: %s", ref.Code, ref.Message)
	}
	if len(set.Rules) != 1 || string(set.Rules[0].Body) != "rule two\n" || set.Rules[0].Pack != "acme/repo" {
		t.Fatalf("winning rule = %+v, want acme/repo's body", set.Rules)
	}
}

// ruleFixture renders a one-lens pack with one rule row.
func ruleFixture(name, extra, body string) fstest.MapFS {
	manifest := fmt.Sprintf(
		`{"schema_version":"1","name":%q,"version":"1.0.0",%s"lenses":[{"id":"perf"}]}`,
		name, extra)
	return fstest.MapFS{
		"pack.json":      {Data: []byte(manifest)},
		"lenses/perf.md": {Data: []byte("perf body\n")},
		"rules/r1.md":    {Data: []byte(body)},
	}
}

// TestRulesForFiltersByGlobs proves a pack rule is recalled only when one of
// its globs matches a changed file.
func TestRulesForFiltersByGlobs(t *testing.T) {
	t.Parallel()
	p := mergePack(t, ruleFixture("acme/rules",
		`"severity":{"s":"critical"},"rules":[{"id":"r1","severity":"s","globs":["**/*.go"]}],`,
		"rule body\n"))
	set, ref := Merge([]Origin{{Layer: LayerOrg, Source: "dir:rules", Pack: p}})
	if ref != nil {
		t.Fatalf("Merge: %s: %s", ref.Code, ref.Message)
	}
	if got := set.RulesFor([]string{"docs/readme.md"}); len(got) != 0 {
		t.Fatalf("docs-only rules = %v, want none", got)
	}
	got := set.RulesFor([]string{"src/main.go"})
	if len(got) != 1 || got[0].ID != "r1" || got[0].Level != "critical" {
		t.Fatalf("go-file rules = %+v, want r1 critical", got)
	}
}
