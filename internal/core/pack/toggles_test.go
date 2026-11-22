package pack

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stainless-code/verdict/internal/core"
)

// toggleFixture is a small two-pack builtin layer plus one repo pack, so a
// toggle's reach is visible: it may drop a builtin pack, a builtin lens, or a
// builtin rule, and never an org or repo one.
func toggleFixture(t *testing.T) []Origin {
	t.Helper()
	return []Origin{
		{Layer: LayerBuiltin, Source: "builtin", Pack: loadFixture(t, ruleFixture("verdict/builtin", "", "core body\n"))},
		{Layer: LayerBuiltin, Source: "builtin:go", Pack: togglePack(t, "verdict/go", "go", "go-rule")},
		{Layer: LayerBuiltin, Source: "builtin:rust", Pack: togglePack(t, "verdict/rust", "rust", "rust-rule")},
		{Layer: LayerRepo, Source: RepoPacksDir + "/acme", Pack: togglePack(t, "acme/repo", "go", "go-rule")},
	}
}

// TestApplyTogglesMergeBuiltinOff proves `merge_builtin: false` drops the whole
// builtin layer, keeps org and repo packs, and reports the one toggle it
// applied.
func TestApplyTogglesMergeBuiltinOff(t *testing.T) {
	t.Parallel()
	off := false
	got := ApplyToggles(toggleFixture(t), Toggles{MergeBuiltin: &off})
	if names := originNames(got.Origins); !slices.Equal(names, []string{"acme/repo"}) {
		t.Fatalf("origins = %v, want the repo pack alone", names)
	}
	if !slices.Equal(got.Applied, []string{"builtin:off"}) {
		t.Errorf("applied = %v, want [builtin:off]", got.Applied)
	}
}

// TestApplyTogglesPackNamesAndSlugs proves a pack toggle answers to its full
// name and its slug, and leaves every other layer alone.
func TestApplyTogglesPackNamesAndSlugs(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"verdict/go", "go"} {
		got := ApplyToggles(toggleFixture(t), Toggles{DisablePacks: []string{id}})
		if names := originNames(got.Origins); !slices.Equal(names, []string{"verdict/builtin", "verdict/rust", "acme/repo"}) {
			t.Errorf("disable %q origins = %v", id, names)
		}
		if !slices.Equal(got.Applied, []string{"pack:verdict/go"}) {
			t.Errorf("disable %q applied = %v", id, got.Applied)
		}
	}
}

// TestApplyTogglesLensAndRule proves the finer toggles: a lens id drops only
// that lens, and a rule id drops only that rule.
func TestApplyTogglesLensAndRule(t *testing.T) {
	t.Parallel()
	lensOff := ApplyToggles(toggleFixture(t), Toggles{DisableLenses: []string{"go"}})
	if got := lensIDsOfOrigin(lensOff.Origins, "verdict/go"); len(got) != 0 {
		t.Errorf("go lenses = %v, want none", got)
	}
	if got := ruleIDsOfOrigin(lensOff.Origins, "verdict/go"); !slices.Equal(got, []string{"go-rule"}) {
		t.Errorf("go rules = %v, want the rule kept", got)
	}
	ruleOff := ApplyToggles(toggleFixture(t), Toggles{DisableRules: []string{"rust-rule"}})
	if got := ruleIDsOfOrigin(ruleOff.Origins, "verdict/rust"); len(got) != 0 {
		t.Errorf("rust rules = %v, want none", got)
	}
	if !slices.Equal(ruleOff.Applied, []string{"rule:rust-rule"}) {
		t.Errorf("applied = %v, want [rule:rust-rule]", ruleOff.Applied)
	}
}

// TestApplyTogglesDropsEmptiedPack proves a pack left with no lens and no rule
// stops existing, so a disabled-out pack cannot linger as an empty row.
func TestApplyTogglesDropsEmptiedPack(t *testing.T) {
	t.Parallel()
	origins := []Origin{{Layer: LayerBuiltin, Source: "builtin:sql", Pack: togglePack(t, "verdict/sql", "sql", "")}}
	got := ApplyToggles(origins, Toggles{DisableLenses: []string{"sql"}})
	if len(got.Origins) != 0 {
		t.Fatalf("origins = %v, want the emptied pack dropped", originNames(got.Origins))
	}
}

// TestApplyTogglesWarnsOnUnknownIDs proves a stale toggle is surfaced, never
// fatal: a renamed builtin id must not refuse a review.
func TestApplyTogglesWarnsOnUnknownIDs(t *testing.T) {
	t.Parallel()
	got := ApplyToggles(toggleFixture(t), Toggles{
		DisablePacks:  []string{"verdict/absent"},
		DisableRules:  []string{"absent-rule"},
		DisableLenses: []string{"absent-lens"},
	})
	if len(got.Applied) != 0 {
		t.Errorf("applied = %v, want nothing", got.Applied)
	}
	want := []string{
		`pack toggle "absent-lens" names no builtin lens — ignored`,
		`pack toggle "verdict/absent" names no builtin pack — ignored`,
		`pack toggle "absent-rule" names no builtin rule — ignored`,
	}
	if !slices.Equal(got.Warnings, want) {
		t.Errorf("warnings = %v, want %v", got.Warnings, want)
	}
}

// TestApplyTogglesCopiesTheLoadedTree proves the toggle filter never edits the
// packs the caller holds: the same loaded tree still lists everything.
func TestApplyTogglesCopiesTheLoadedTree(t *testing.T) {
	t.Parallel()
	origins := toggleFixture(t)
	ApplyToggles(origins, Toggles{DisablePacks: []string{"verdict/go", "go"}})
	if got := ruleIDsOfOrigin(origins, "verdict/go"); !slices.Equal(got, []string{"go-rule"}) {
		t.Errorf("input rules = %v, want the input untouched", got)
	}
}

// TestTogglesFromConfig proves the configuration section maps onto the loader
// toggles, and an absent section turns nothing off.
func TestTogglesFromConfig(t *testing.T) {
	t.Parallel()
	if got := TogglesFromConfig(nil); got.MergeBuiltin != nil || len(got.DisablePacks) != 0 {
		t.Errorf("nil config toggles = %+v, want zero", got)
	}
	cfg := &core.Config{Packs: &core.ConfigPacks{
		DisablePacks: []string{"verdict/shell"}, DisableLenses: []string{"go"}, DisableRules: []string{"sec-ssrf"},
	}}
	got := TogglesFromConfig(cfg)
	if !slices.Equal(got.DisablePacks, []string{"verdict/shell"}) ||
		!slices.Equal(got.DisableLenses, []string{"go"}) ||
		!slices.Equal(got.DisableRules, []string{"sec-ssrf"}) {
		t.Errorf("toggles = %+v", got)
	}
}

// togglePack builds a one-lens/one-rule builtin pack fixture.
func togglePack(t *testing.T, name, lens, rule string) *Pack {
	t.Helper()
	manifest := `{"schema_version":"1","name":"` + name + `","version":"1.0.0",` +
		`"severity":{"important":"important"},"lenses":[{"id":"` + lens + `"}]`
	files := fstest.MapFS{
		"lenses/" + lens + ".md": {Data: []byte("body\n")},
	}
	if rule != "" {
		manifest += `,"rules":[{"id":"` + rule + `","severity":"important","globs":["**/*` + lens + `"]}]`
		files["rules/"+rule+".md"] = &fstest.MapFile{Data: []byte("rule body\n")}
	}
	files["pack.json"] = &fstest.MapFile{Data: []byte(manifest + "}")}
	return loadFixture(t, files)
}

// originNames renders the loaded pack names in order.
func originNames(origins []Origin) []string {
	out := make([]string, 0, len(origins))
	for _, origin := range origins {
		if origin.Pack != nil {
			out = append(out, origin.Pack.Name)
		}
	}
	return out
}

// packNamed finds one origin's pack by name.
func packNamed(origins []Origin, name string) *Pack {
	for _, origin := range origins {
		if origin.Pack != nil && origin.Pack.Name == name {
			return origin.Pack
		}
	}
	return nil
}

// lensIDsOfOrigin renders one pack's lens ids after a toggle pass.
func lensIDsOfOrigin(origins []Origin, name string) []string {
	p := packNamed(origins, name)
	if p == nil {
		return nil
	}
	return lensIDsOf(p)
}

// ruleIDsOfOrigin renders one pack's rule ids after a toggle pass.
func ruleIDsOfOrigin(origins []Origin, name string) []string {
	p := packNamed(origins, name)
	if p == nil {
		return nil
	}
	return ruleIDsOf(p)
}

// TestUnmatchedToggleWarningHasNoTypo proves the warning text names the entry
// once and in its own kind's words.
func TestUnmatchedToggleWarningHasNoTypo(t *testing.T) {
	t.Parallel()
	got := ApplyToggles(toggleFixture(t), Toggles{DisablePacks: []string{"verdict/nope"}})
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], `"verdict/nope" names no builtin pack`) {
		t.Fatalf("warnings = %v", got.Warnings)
	}
}
