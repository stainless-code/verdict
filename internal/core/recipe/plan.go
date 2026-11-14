package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/stainless-code/verdict/internal/core"
)

// DefaultName is the recipe a run resolves when no name is asked for, so even
// the implicit pipeline is a file.
const DefaultName = "default"

// Source names: the base snapshot carries a repository entry, and the embedded
// set answers the rest.
const (
	SourceRepo    = "repo"
	SourceBuiltin = "builtin"
)

// Stage kinds, in pipeline order.
const (
	StageLenses     = "lenses"
	StageSynthesize = "synthesize"
	StageDedupe     = "dedupe"
	StageRank       = "rank"
)

// Lens source names: the entry listed the lens ids, or it listed none and the
// bundle's loaded ids stand in, in file order.
const (
	LensSourceDeclared = "declared"
	LensSourceBundle   = "bundle"
)

// Plan is the resolved pipeline: the record of what a run declared, and the
// published `data.recipe` object and `recipe` key of launcher.json. The wire
// type is the contract root's (core.RecipePlan) because the envelope has to
// name it; these aliases keep this package's readers on the recipe vocabulary,
// and an alias means one type rather than a copy that could drift.
type (
	Plan    = core.RecipePlan
	Stage   = core.RecipeStage
	LensUse = core.RecipeLens
)

// Builtin returns the embedded pipeline set a repository entry falls back to.
func Builtin() (*Document, *core.RefusalError) { return builtin() }

// builtin parses the embedded set once: the bytes are fixed at build time, so a
// fault is a build defect every run reports identically.
var builtin = sync.OnceValues(func() (*Document, *core.RefusalError) {
	doc, ref := Parse(builtinDefault)
	if ref != nil {
		return nil, refuse(CodeBad, "builtin/default.json: %s", ref.Message)
	}
	return doc, nil
})

// Resolve picks the named entry — the repository document first, then the
// built-in — and compiles it into the four-stage plan. installed carries the
// bundle's loaded lens ids in file order: it is the lens list a recipe that
// declares none inherits, and it decides the absent-lens warnings.
func Resolve(repo *Document, name string, installed []string) (*Plan, *core.RefusalError) {
	entry, source, ref := lookup(repo, name)
	if ref != nil {
		return nil, ref
	}
	digest, ref := canonical(&entry, name)
	if ref != nil {
		return nil, ref
	}
	lenses, lensSource, warnings := lensStage(&entry, name, installed)
	return &Plan{
		Name:      name,
		Version:   entry.Version,
		Source:    source,
		SHA256:    digest,
		BudgetUSD: entry.BudgetUSD,
		Stages: []Stage{
			{Kind: StageLenses, LensSource: lensSource, Lenses: lenses},
			{Kind: StageSynthesize, Model: entry.Synthesize.Model},
			{Kind: StageDedupe},
			{Kind: StageRank},
		},
		Warnings: warnings,
	}, nil
}

// lookup finds one name: a repository entry wins, then the built-in, and a miss
// names every recipe that exists. A name is a map key, never a path.
func lookup(repo *Document, name string) (Entry, string, *core.RefusalError) {
	if repo != nil {
		if entry, ok := repo.Recipes[name]; ok {
			return entry, SourceRepo, nil
		}
	}
	fallback, ref := Builtin()
	if ref != nil {
		return Entry{}, "", ref
	}
	if entry, ok := fallback.Recipes[name]; ok {
		return entry, SourceBuiltin, nil
	}
	return Entry{}, "", refuse(CodeUnknown, "no recipe %q; available: %s",
		name, strings.Join(available(repo, fallback), ", "))
}

// available lists the names a miss could have meant: every repository key plus
// every built-in, sorted.
func available(repo, fallback *Document) []string {
	names := map[string]bool{}
	if repo != nil {
		for name := range repo.Recipes {
			names[name] = true
		}
	}
	for name := range fallback.Recipes {
		names[name] = true
	}
	return slices.Sorted(maps.Keys(names))
}

// canonical hashes the entry's canonical JSON: fixed field order, arrays in
// declaration order, no map iteration. Any field change moves the digest a run
// records, and the same document always yields the same one.
func canonical(entry *Entry, name string) (string, *core.RefusalError) {
	raw, err := json.Marshal(entry)
	if err != nil {
		return "", refuse(CodeBad, "$.recipes.%s: cannot encode the entry: %v", name, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// lensStage resolves the lenses stage and its warnings.
func lensStage(entry *Entry, name string, installed []string) ([]LensUse, string, []string) {
	if len(entry.Lenses) == 0 {
		lenses, source := bundleLensStage(installed)
		return lenses, source, nil
	}
	return declaredLensStage(entry.Lenses, name, installed)
}

// bundleLensStage stands the bundle's loaded ids in for a recipe that declares
// none, in file order. No warning: a bundle that loads no lens is the ordinary
// default path, so a recipe that asks for nothing gets the empty set silently.
func bundleLensStage(installed []string) ([]LensUse, string) {
	lenses := make([]LensUse, 0, len(installed))
	for _, id := range installed {
		lenses = append(lenses, LensUse{ID: id})
	}
	return lenses, LensSourceBundle
}

// declaredLensStage keeps the declared rows in declaration order. A lens the
// bundle does not load warns and never refuses: lens offers are per plan group,
// so fetch is the authority on them.
func declaredLensStage(declared []Lens, name string, installed []string) ([]LensUse, string, []string) {
	loaded := make(map[string]bool, len(installed))
	for _, id := range installed {
		loaded[id] = true
	}
	lenses := make([]LensUse, 0, len(declared))
	var warnings []string
	for _, lens := range declared {
		lenses = append(lenses, LensUse(lens))
		if !loaded[lens.ID] {
			warnings = append(warnings, fmt.Sprintf(
				"recipe %s declares lens %q, which the bundle does not load", name, lens.ID))
		}
	}
	return lenses, LensSourceDeclared, warnings
}
