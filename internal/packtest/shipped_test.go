package packtest

import (
	"encoding/json"
	"strings"
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
	if len(origins) != 13 {
		t.Fatalf("builtin layer carries %d packs, want 13", len(origins))
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
		{"verdict/react", "react"},
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

// TestShippedPacksCarryPositiveAndNegativeTrapCases proves every curated pack
// ships at least two eval cases: one positive case that cites a rule, and one
// negative-trap case whose safe fixture must yield no finding. It also pins
// the rule-body contract every rule shares: a concrete "When to flag" section
// and an explicit "Do NOT flag" boundary.
func TestShippedPacksCarryPositiveAndNegativeTrapCases(t *testing.T) {
	t.Parallel()
	origins, ref := packcore.BuiltinOrigins(skillfs.FS)
	if ref != nil {
		t.Fatalf("BuiltinOrigins: %s: %s", ref.Code, ref.Message)
	}
	for _, origin := range origins[1:] {
		t.Run(origin.Pack.Name, func(t *testing.T) {
			t.Parallel()
			requireCaseMix(t, origin)
			requireRuleSections(t, origin)
		})
	}
}

// caseExpect is the subset of expect.json the coverage assertion reads.
type caseExpect struct {
	Rules []string `json:"rules"`
}

// requireCaseMix proves one pack carries a positive case and a negative-trap
// case, each with the fixtures the pack test runner grades.
func requireCaseMix(t *testing.T, origin packcore.Origin) {
	t.Helper()
	set, ref := packcore.Merge([]packcore.Origin{origin})
	if ref != nil {
		t.Fatalf("Merge: %s: %s", ref.Code, ref.Message)
	}
	cases := packCases(origin)
	if len(cases) < 2 {
		t.Fatalf("%s has %d eval cases, want at least 2", origin.Pack.Name, len(cases))
	}
	var positive, negative int
	for name, files := range cases {
		if len(caseRules(t, origin.Pack.Name, name, files)) > 0 {
			positive++
			continue
		}
		negative++
		requireNoFindings(t, origin.Pack, set, name, files)
	}
	if positive == 0 || negative == 0 {
		t.Errorf("%s cases = %d positive, %d negative-trap, want at least one of each", origin.Pack.Name, positive, negative)
	}
}

// packCases groups a pack's eval payload by case name, with case-relative
// file names as keys.
func packCases(origin packcore.Origin) map[string]map[string][]byte {
	cases := map[string]map[string][]byte{}
	for _, file := range origin.Pack.PayloadFiles() {
		parts := strings.Split(file.Path, "/")
		if len(parts) < 3 || parts[0] != "evals" {
			continue
		}
		if cases[parts[1]] == nil {
			cases[parts[1]] = map[string][]byte{}
		}
		cases[parts[1]][strings.Join(parts[2:], "/")] = file.Body
	}
	return cases
}

// caseRules decodes one case's expected rules and requires both fixtures the
// runner grades.
func caseRules(t *testing.T, pack, name string, files map[string][]byte) []string {
	t.Helper()
	expect, ok := files["expect.json"]
	if !ok {
		t.Fatalf("%s case %s has no expect.json", pack, name)
	}
	if _, ok := files["findings.json"]; !ok {
		t.Fatalf("%s case %s has no findings.json", pack, name)
	}
	var want caseExpect
	if err := json.Unmarshal(expect, &want); err != nil {
		t.Fatalf("%s case %s expect.json: %v", pack, name, err)
	}
	return want.Rules
}

// requireNoFindings proves a negative-trap case expects no finding and that
// its diff still exercises the pack's globs, so it is a real safe-idiom
// fixture rather than a path the pack never sees.
func requireNoFindings(t *testing.T, p *packcore.Pack, set *packcore.Set, name string, files map[string][]byte) {
	t.Helper()
	diff, ok := files["diff.patch"]
	if !ok {
		t.Fatalf("%s case %s has no diff.patch", p.Name, name)
	}
	if len(selectRules(p, set, diffPaths(string(diff)))) == 0 {
		t.Errorf("%s negative-trap case %s selects none of the pack's rules; its diff does not exercise the pack", p.Name, name)
	}
	var rows []map[string]any
	if err := json.Unmarshal(files["findings.json"], &rows); err != nil {
		t.Fatalf("%s case %s findings.json: %v", p.Name, name, err)
	}
	if len(rows) != 0 {
		t.Errorf("%s negative-trap case %s cites %d finding(s), want none", p.Name, name, len(rows))
	}
}

// requireRuleSections proves every shipped rule body carries both halves of
// the precision contract.
func requireRuleSections(t *testing.T, origin packcore.Origin) {
	t.Helper()
	for i := range origin.Pack.Rules {
		rule := &origin.Pack.Rules[i]
		body := string(rule.Body)
		flag := strings.Index(body, "\n## When to flag\n")
		avoid := strings.Index(body, "\n## Do NOT flag\n")
		evidence := strings.Index(body, "\n## Evidence\n")
		if flag < 0 || avoid < 0 || evidence < 0 {
			t.Errorf("%s rule %s lacks the When-to-flag/Do-NOT-flag/Evidence sections", origin.Pack.Name, rule.ID)
			continue
		}
		if avoid < flag || evidence < avoid {
			t.Errorf("%s rule %s states its sections out of order", origin.Pack.Name, rule.ID)
		}
	}
}
