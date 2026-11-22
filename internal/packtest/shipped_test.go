package packtest

import (
	"testing"

	skillfs "github.com/stainless-code/verdict"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
)

// TestShippedPacksPassTheirOwnCases runs every curated builtin pack's eval
// cases through the shipped loader: each must resolve, every case must PASS,
// and none may SKIP — a skipped case proves nothing about the pack. The core
// pack carries lenses only, so it has no cases to run and is covered by
// TestCorePackHasNoCases below.
func TestShippedPacksPassTheirOwnCases(t *testing.T) {
	t.Parallel()
	origins, ref := packcore.BuiltinOrigins(skillfs.FS)
	if ref != nil {
		t.Fatalf("BuiltinOrigins: %s: %s", ref.Code, ref.Message)
	}
	if len(origins) != 12 {
		t.Fatalf("builtin layer carries %d packs, want 12", len(origins))
	}
	for _, origin := range origins[1:] {
		t.Run(origin.Pack.Name, func(t *testing.T) {
			t.Parallel()
			requireAllCasesPass(t, origin)
		})
	}
}

// requireAllCasesPass runs one curated pack's cases and proves every one
// passed, none skipped, and there was at least one.
func requireAllCasesPass(t *testing.T, origin packcore.Origin) {
	t.Helper()
	set, ref := packcore.Merge([]packcore.Origin{origin})
	if ref != nil {
		t.Fatalf("Merge: %s: %s", ref.Code, ref.Message)
	}
	resp, ref := Run(t.Context(), origin.Pack, set, "1970-01-01T00:00:00Z")
	if ref != nil {
		t.Fatalf("Run: %s: %s", ref.Code, ref.Message)
	}
	if got := resp.Data.Summary; got.Fail != 0 || got.Skip != 0 || got.Pass == 0 {
		t.Fatalf("%s cases = %+v, want every case PASS", origin.Pack.Name, got)
	}
	if got := len(resp.Data.Cases); got == 0 {
		t.Fatalf("%s reported no cases", origin.Pack.Name)
	}
}

// TestCorePackHasNoCases pins the boundary: the core pack ships baseline
// lenses and no rules, so `pack test verdict/builtin` refuses with
// pack-test-cases instead of reporting an empty pass.
func TestCorePackHasNoCases(t *testing.T) {
	t.Parallel()
	origins, ref := packcore.BuiltinOrigins(skillfs.FS)
	if ref != nil {
		t.Fatalf("BuiltinOrigins: %s: %s", ref.Code, ref.Message)
	}
	core := origins[0]
	if core.Pack.Name != "verdict/builtin" || len(core.Pack.Rules) != 0 {
		t.Fatalf("core pack = %s with %d rules, want the lens-only core", core.Pack.Name, len(core.Pack.Rules))
	}
	set, ref := packcore.Merge([]packcore.Origin{core})
	if ref != nil {
		t.Fatalf("Merge: %s: %s", ref.Code, ref.Message)
	}
	_, ref = Run(t.Context(), core.Pack, set, "1970-01-01T00:00:00Z")
	if ref == nil || ref.Code != CodePackTestCases {
		t.Fatalf("core pack test = %v, want %s", ref, CodePackTestCases)
	}
}

// TestShippedLanguagePacksCarryALensAndARule proves every curated pack earns
// its place: a lens offered for its stack and at least one rule scoped to it,
// which is what makes the pack resolvable for a real diff.
func TestShippedLanguagePacksCarryALensAndARule(t *testing.T) {
	t.Parallel()
	origins, ref := packcore.BuiltinOrigins(skillfs.FS)
	if ref != nil {
		t.Fatalf("BuiltinOrigins: %s: %s", ref.Code, ref.Message)
	}
	want := []struct{ pack, lens string }{
		{"verdict/dockerfile", "dockerfile"},
		{"verdict/github-workflows", "github-workflows"},
		{"verdict/go", "go"},
		{"verdict/java", "java"},
		{"verdict/python", "python"},
		{"verdict/rust", "rust"},
		{"verdict/security", "security"},
		{"verdict/shell", "shell"},
		{"verdict/sql", "sql"},
		{"verdict/terraform", "terraform"},
		{"verdict/ts-js", "ts-js"},
	}
	packs := origins[1:]
	if len(packs) != len(want) {
		t.Fatalf("the builtin layer carries %d curated packs, want %d", len(packs), len(want))
	}
	for i := range want {
		requireStackPack(t, packs[i], want[i].pack, want[i].lens)
	}
}

// requireStackPack proves one curated pack declares exactly the stack lens and
// at least one rule that make it resolvable for a real diff.
func requireStackPack(t *testing.T, origin packcore.Origin, name, lens string) {
	t.Helper()
	if origin.Pack.Name != name {
		t.Errorf("pack = %q, want %q", origin.Pack.Name, name)
	}
	if len(origin.Pack.Rules) == 0 {
		t.Errorf("%s carries no rules", name)
	}
	ids := make([]string, 0, len(origin.Pack.Lenses))
	for i := range origin.Pack.Lenses {
		ids = append(ids, origin.Pack.Lenses[i].ID)
	}
	if len(ids) != 1 || ids[0] != lens {
		t.Errorf("%s lenses = %v, want the single %q lens", name, ids, lens)
	}
}
