package recipe

import (
	"maps"
	"slices"
	"testing"
)

// TestBuiltinDefault proves the embedded fallback is the same strict format the
// repository file is, and resolves to the four-stage pipeline with the bundle's
// lenses standing in for the entry it does not declare.
func TestBuiltinDefault(t *testing.T) {
	doc, ref := Builtin()
	if ref != nil {
		t.Fatalf("Builtin: %v", ref)
	}
	entry, ok := doc.Recipes[DefaultName]
	if !ok {
		t.Fatalf("built-in recipes = %v, want one named %q", slices.Sorted(maps.Keys(doc.Recipes)), DefaultName)
	}
	if entry.Version != "1.0.0" || len(entry.Lenses) != 0 || entry.Synthesize.Model != "" ||
		entry.BudgetUSD != 0 || entry.Description != "" {
		t.Fatalf("built-in default = %+v, want {version:1.0.0, synthesize:{}}", entry)
	}
}

// TestResolveBuiltinPipeline proves the built-in entry compiles into the four
// stages with the bundle's lens ids standing in, in file order.
func TestResolveBuiltinPipeline(t *testing.T) {
	plan, ref := Resolve(nil, DefaultName, []string{"perf", "test"})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if plan.Name != "default" || plan.Version != "1.0.0" || plan.Source != SourceBuiltin {
		t.Errorf("plan = %+v, want the built-in default@1.0.0", plan)
	}
	if got := plan.Stages[0].LensSource; got != LensSourceBundle {
		t.Errorf("lens_source = %q, want %q", got, LensSourceBundle)
	}
	if got := lensIDs(plan.Stages[0].Lenses); !slices.Equal(got, []string{"perf", "test"}) {
		t.Errorf("lens ids = %v, want the loaded lenses in file order", got)
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", plan.Warnings)
	}
}

// TestResolvePrefersRepoThenBuiltin proves the resolution order per name: the
// repository entry shadows the built-in for the same name, an entry only the
// repository carries resolves, and a name only the built-in carries falls back.
func TestResolvePrefersRepoThenBuiltin(t *testing.T) {
	repo := parsed(t, `{"schema_version":"1","recipes":{"default":{"version":"2.0.0","synthesize":{}},"quick":`+entryValid+`}}`)
	for _, tc := range []struct {
		name    string
		repo    *Document
		recipe  string
		want    string
		version string
	}{
		{"repository shadows the built-in", repo, DefaultName, SourceRepo, "2.0.0"},
		{"repository-only entry", repo, "quick", SourceRepo, "1.0.0"},
		{"built-in only", nil, DefaultName, SourceBuiltin, "1.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, version := resolved(t, tc.repo, tc.recipe)
			if source != tc.want || version != tc.version {
				t.Errorf("%s = %s@%s, want %s@%s", tc.recipe, source, version, tc.want, tc.version)
			}
		})
	}
}

// parsed parses one document or fails the test.
func parsed(t *testing.T, raw string) *Document {
	t.Helper()
	doc, ref := Parse([]byte(raw))
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	return doc
}

// planOf parses one document and resolves the named recipe against the
// installed lens ids, failing the test on any refusal.
func planOf(t *testing.T, raw, name string, installed []string) *Plan {
	t.Helper()
	plan, ref := Resolve(parsed(t, raw), name, installed)
	if ref != nil {
		t.Fatalf("Resolve(%q): %v", name, ref)
	}
	return plan
}

// resolved resolves one name against an installed lens set and returns its
// recorded source and version.
func resolved(t *testing.T, repo *Document, name string) (string, string) {
	t.Helper()
	plan, ref := Resolve(repo, name, nil)
	if ref != nil {
		t.Fatalf("Resolve(%q): %v", name, ref)
	}
	return plan.Source, plan.Version
}

// TestResolveUnknownListsNames proves a miss names every available recipe —
// the repository keys plus the built-in — and binds nothing.
func TestResolveUnknownListsNames(t *testing.T) {
	repo := parsed(t, `{"schema_version":"1","recipes":{"review":{"version":"1.0.0","synthesize":{}},"deep":{"version":"1.0.0","synthesize":{}}}}`)
	for _, tc := range []struct {
		name string
		repo *Document
		want string
	}{
		{"repository document", repo, `no recipe "nope"; available: deep, default, review`},
		{"no repository document", nil, `no recipe "nope"; available: default`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := unknownRefusal(t, tc.repo); got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
		})
	}
}

// unknownRefusal resolves a name nothing declares, fails if that resolved, and
// returns the refusal's message.
func unknownRefusal(t *testing.T, repo *Document) string {
	t.Helper()
	plan, ref := Resolve(repo, "nope", nil)
	if plan != nil {
		t.Fatalf("Resolve returned a plan for an unknown name: %+v", plan)
	}
	if ref == nil || ref.Code != CodeUnknown {
		t.Fatalf("ref = %+v, want %s", ref, CodeUnknown)
	}
	return ref.Message
}

// TestNameIsAMapKey proves a name is never a path: a traversal or a nested
// spelling finds no entry instead of reading one.
func TestNameIsAMapKey(t *testing.T) {
	repo := parsed(t, `{"schema_version":"1","recipes":{"quick":`+entryValid+`}}`)
	for _, name := range []string{"../quick", "quick.json", "recipes/quick", "Quick"} {
		if _, ref := Resolve(repo, name, nil); ref == nil || ref.Code != CodeUnknown {
			t.Errorf("Resolve(%q) = %+v, want %s", name, ref, CodeUnknown)
		}
	}
}
