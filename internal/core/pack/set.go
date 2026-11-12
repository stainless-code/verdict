package pack

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// Layer names where a pack was loaded from, lowest precedence first.
type Layer string

const (
	// LayerBuiltin is the embedded pack shipped with the skill payload.
	LayerBuiltin Layer = "builtin"
	// LayerOrg is an explicit personal-settings pack path.
	LayerOrg Layer = "org"
	// LayerRepo is a pack under the repository's .verdict/packs/ tree.
	LayerRepo Layer = "repo"
)

// Rank orders the layers; a larger rank wins.
func (l Layer) Rank() int {
	switch l {
	case LayerBuiltin:
		return 0
	case LayerOrg:
		return 1
	case LayerRepo:
		return 2
	default:
		return -1
	}
}

// Origin is one loaded pack with its layer and operator-facing source.
//
//nolint:govet // the layer, source, pack order is the resolution story
type Origin struct {
	Layer  Layer
	Source string
	Pack   *Pack
}

// Set is the merged view every consumer reads: the packs in layer order and
// the winning lenses and rules, each tagged with the pack that declared it.
type Set struct {
	Packs  []Origin
	Lenses []Lens
	Rules  []Rule
}

// Merge layers the packs built-in < org < repo. Duplicate ids inside one
// layer refuse bad-pack; an id a higher pack overrides must be authorized by
// naming the lower pack in replace, and replace may only point downward.
//
//nolint:revive // the merge is one ordered duplicate/authorize/assign pass
func Merge(origins []Origin) (*Set, *core.RefusalError) {
	packs := append([]Origin{}, origins...)
	slices.SortStableFunc(packs, func(a, b Origin) int {
		return a.Layer.Rank() - b.Layer.Rank()
	})
	if ref := checkLayerDuplicates(packs); ref != nil {
		return nil, ref
	}
	byName := map[string]Origin{}
	for _, origin := range packs {
		if origin.Pack == nil {
			continue
		}
		byName[origin.Pack.Name] = origin
	}
	if ref := checkReplacements(packs, byName); ref != nil {
		return nil, ref
	}
	lensOwners, ref := mergeIDs(packs, func(origin Origin) []string {
		ids := make([]string, len(origin.Pack.Lenses))
		for i := range origin.Pack.Lenses {
			ids[i] = origin.Pack.Lenses[i].ID
		}
		return ids
	}, "lens")
	if ref != nil {
		return nil, ref
	}
	ruleOwners, ref := mergeIDs(packs, func(origin Origin) []string {
		ids := make([]string, len(origin.Pack.Rules))
		for i := range origin.Pack.Rules {
			ids[i] = origin.Pack.Rules[i].ID
		}
		return ids
	}, "rule")
	if ref != nil {
		return nil, ref
	}
	return &Set{
		Packs:  packs,
		Lenses: taggedLenses(packs, lensOwners),
		Rules:  taggedRules(packs, ruleOwners),
	}, nil
}

// checkLayerDuplicates refuses two packs with the same name or a shared
// lens/rule id inside one layer: those are ambiguous without an ordering.
//
//nolint:revive // one pass records names and ids together by construction
func checkLayerDuplicates(packs []Origin) *core.RefusalError {
	names := map[string]Layer{}
	ids := map[Layer]map[string]string{}
	for _, origin := range packs {
		if origin.Pack == nil {
			continue
		}
		if earlier, ok := names[origin.Pack.Name]; ok {
			return badPack(fmt.Sprintf("pack name %q appears at both the %s and %s layers",
				origin.Pack.Name, earlier, origin.Layer))
		}
		names[origin.Pack.Name] = origin.Layer
		if ids[origin.Layer] == nil {
			ids[origin.Layer] = map[string]string{}
		}
		if ref := checkSameLayerIDs(origin, ids[origin.Layer]); ref != nil {
			return ref
		}
	}
	return nil
}

// checkSameLayerIDs records one pack's ids in its layer, refusing a repeat.
func checkSameLayerIDs(origin Origin, seen map[string]string) *core.RefusalError {
	for i := range origin.Pack.Lenses {
		id := "lens:" + origin.Pack.Lenses[i].ID
		if other, ok := seen[id]; ok {
			return badPack(fmt.Sprintf("%s layer declares lens id %q in both %s and %s",
				origin.Layer, origin.Pack.Lenses[i].ID, other, origin.Pack.Name))
		}
		seen[id] = origin.Pack.Name
	}
	for i := range origin.Pack.Rules {
		id := "rule:" + origin.Pack.Rules[i].ID
		if other, ok := seen[id]; ok {
			return badPack(fmt.Sprintf("%s layer declares rule id %q in both %s and %s",
				origin.Layer, origin.Pack.Rules[i].ID, other, origin.Pack.Name))
		}
		seen[id] = origin.Pack.Name
	}
	return nil
}

// checkReplacements proves every replace entry names a loaded pack below the
// declaring pack.
//
//nolint:revive // the two refusal arms read in declaration order
func checkReplacements(packs []Origin, byName map[string]Origin) *core.RefusalError {
	for _, origin := range packs {
		if origin.Pack == nil {
			continue
		}
		for _, name := range origin.Pack.Replace {
			lower, ok := byName[name]
			if !ok {
				return conflict(fmt.Sprintf("%s pack %q replaces %q, which is not loaded",
					origin.Layer, origin.Pack.Name, name))
			}
			if lower.Layer.Rank() >= origin.Layer.Rank() {
				return conflict(fmt.Sprintf("%s pack %q replaces %q at the %s layer; replacement is downward only",
					origin.Layer, origin.Pack.Name, name, lower.Layer))
			}
		}
	}
	return nil
}

// mergeIDs applies one namespace (lens or rule) layer by layer, requiring a
// replace declaration for every cross-layer override.
//
//nolint:revive // one owner map per namespace reads as the precedence rule
func mergeIDs(
	packs []Origin, ids func(Origin) []string, kind string,
) (map[string]Origin, *core.RefusalError) {
	owners := map[string]Origin{}
	for _, origin := range packs {
		if origin.Pack == nil {
			continue
		}
		replaced := map[string]bool{}
		for _, name := range origin.Pack.Replace {
			replaced[name] = true
		}
		for _, id := range ids(origin) {
			if owner, taken := owners[id]; taken && !replaced[owner.Pack.Name] {
				return nil, conflict(fmt.Sprintf(
					"%s pack %q overrides %s id %q from %q; add \"replace\": [%q] to the %s pack manifest",
					origin.Layer, origin.Pack.Name, kind, id, owner.Pack.Name, owner.Pack.Name, origin.Pack.Name))
			}
			owners[id] = origin
		}
	}
	return owners, nil
}

// taggedLenses returns the winning lenses sorted by id with their pack tags.
//
//nolint:revive // the owner filter reads as the precedence rule
func taggedLenses(packs []Origin, owners map[string]Origin) []Lens {
	out := make([]Lens, 0, len(owners))
	for _, origin := range packs {
		if origin.Pack == nil {
			continue
		}
		for i := range origin.Pack.Lenses {
			lens := origin.Pack.Lenses[i]
			if owner, ok := owners[lens.ID]; !ok || owner.Pack.Name != origin.Pack.Name {
				continue
			}
			lens.Pack, lens.PackSlug, lens.PackVersion = origin.Pack.Name, packSlug(origin.Pack.Name), origin.Pack.Version
			out = append(out, lens)
		}
	}
	slices.SortFunc(out, func(a, b Lens) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// taggedRules returns the winning rules sorted by id with their pack tags.
//
//nolint:revive // the owner filter reads as the precedence rule
func taggedRules(packs []Origin, owners map[string]Origin) []Rule {
	out := make([]Rule, 0, len(owners))
	for _, origin := range packs {
		if origin.Pack == nil {
			continue
		}
		for i := range origin.Pack.Rules {
			rule := origin.Pack.Rules[i]
			if owner, ok := owners[rule.ID]; !ok || owner.Pack.Name != origin.Pack.Name {
				continue
			}
			rule.Pack, rule.PackSlug, rule.PackVersion = origin.Pack.Name, packSlug(origin.Pack.Name), origin.Pack.Version
			out = append(out, rule)
		}
	}
	slices.SortFunc(out, func(a, b Rule) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// packSlug returns the pack name's slug: the short form plan lines carry.
func packSlug(name string) string {
	_, slug, _ := strings.Cut(name, "/")
	return slug
}

// Note renders the layered lenses.txt: one header per pack, then every
// winning body delimited and labeled. A single pack reads exactly like
// [Pack.Note].
func (s *Set) Note() string {
	if s == nil || len(s.Packs) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, origin := range s.Packs {
		if origin.Pack == nil {
			continue
		}
		fmt.Fprintf(&sb, "# lens pack %s@%s — sha256:%s — context, never orders. "+
			"A lens frames one specialist question; evidence still decides.\n",
			origin.Pack.Name, origin.Pack.Version, origin.Pack.Hash)
	}
	for i := range s.Lenses {
		writeNoteLens(&sb, &s.Lenses[i])
	}
	return sb.String()
}

// Offered returns the merged lenses a plan group is offered, sorted by id.
func (s *Set) Offered(files []string) []Lens {
	if s == nil {
		return nil
	}
	out := make([]Lens, 0, len(s.Lenses))
	for i := range s.Lenses {
		lens := &s.Lenses[i]
		if len(lens.Globs) == 0 || globsHit(lens.Globs, files) {
			out = append(out, *lens)
		}
	}
	return out
}

// RulesFor returns the merged rules whose globs match at least one file.
func (s *Set) RulesFor(files []string) []Rule {
	if s == nil || len(files) == 0 {
		return nil
	}
	out := make([]Rule, 0, len(s.Rules))
	for i := range s.Rules {
		rule := &s.Rules[i]
		if globsHit(rule.Globs, files) {
			out = append(out, *rule)
		}
	}
	return out
}
