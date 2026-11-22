package pack

import (
	"fmt"
	"slices"
	"strings"
)

// Toggles is the trusted-base pack configuration: whether the builtin layer
// merges at all, plus the builtin pack, lens, and rule ids a repository turns
// off. Every list holds one id per entry — a pack name (`verdict/shell`), a
// pack slug (`shell`), a lens id (`go`), or a rule id (`go-context-lifecycle`)
// — and only the builtin layer is reachable, because org and repo packs are
// the operator's and the repository's own choice.
type Toggles struct {
	MergeBuiltin  *bool
	DisablePacks  []string
	DisableLenses []string
	DisableRules  []string
}

// ToggleResult is one application of [Toggles] to a loaded origin list.
type ToggleResult struct {
	Origins []Origin
	// Applied names every toggle that changed the set, prefixed by kind, in
	// sorted order, so a check row can report exactly what the base config did.
	Applied []string
	// Warnings name toggles that matched nothing. They are surfaced, never
	// fatal: a renamed builtin id must not refuse a review.
	Warnings []string
}

// ApplyToggles filters the builtin layer through toggles. The returned
// origins keep their input order, org and repo layers are never touched, and
// the input packs are copied rather than edited, so a caller holding the
// loaded tree still sees everything the binary ships.
//
//nolint:gocritic // toggles is an 80-byte read-only value the CLI passes once
func ApplyToggles(origins []Origin, toggles Toggles) ToggleResult {
	if toggles.MergeBuiltin != nil && !*toggles.MergeBuiltin {
		return ToggleResult{Origins: withoutBuiltin(origins), Applied: []string{"builtin:off"}}
	}
	packs := newIDSet(toggles.DisablePacks)
	lenses := newIDSet(toggles.DisableLenses)
	rules := newIDSet(toggles.DisableRules)
	out, fired := toggleBuiltin(origins, packs, lenses, rules)
	applied := mapKeys(fired)
	slices.Sort(applied)
	return ToggleResult{
		Origins:  out,
		Applied:  applied,
		Warnings: unmatchedToggles(origins, packs, lenses, rules),
	}
}

// toggleBuiltin applies the disable lists to every builtin origin, keeping the
// other layers and the input order, and collects the toggles that fired.
func toggleBuiltin(origins []Origin, packs, lenses, rules map[string]bool) ([]Origin, map[string]bool) {
	out := make([]Origin, 0, len(origins))
	fired := map[string]bool{}
	for i := range origins {
		origin, dropped, kept := toggleOrigin(origins[i], packs, lenses, rules)
		if kept {
			out = append(out, origin)
		}
		for _, kind := range dropped {
			fired[kind] = true
		}
	}
	return out, fired
}

// toggleOrigin applies the disable lists to one origin, returning whether it
// survives the layer and the toggles that fired.
func toggleOrigin(origin Origin, packs, lenses, rules map[string]bool) (Origin, []string, bool) {
	if origin.Layer != LayerBuiltin || origin.Pack == nil {
		return origin, nil, true
	}
	filtered, dropped := filterBuiltin(origin, packs, lenses, rules)
	return filtered, dropped, filtered.Pack != nil
}

// withoutBuiltin drops the builtin layer and keeps the rest in order.
func withoutBuiltin(origins []Origin) []Origin {
	out := make([]Origin, 0, len(origins))
	for _, origin := range origins {
		if origin.Layer != LayerBuiltin {
			out = append(out, origin)
		}
	}
	return out
}

// filterBuiltin applies the three disable lists to one builtin origin,
// returning the surviving origin (nil pack when the whole pack is off) and the
// toggles that fired.
func filterBuiltin(
	origin Origin, packs, lenses, rules map[string]bool,
) (Origin, []string) {
	var fired []string
	if packToggled(origin, packs) {
		return Origin{}, []string{"pack:" + origin.Pack.Name}
	}
	p := *origin.Pack
	keptLenses, lensDropped := filterLenses(p.Lenses, lenses)
	keptRules, ruleDropped := filterRules(p.Rules, rules)
	fired = append(fired, lensDropped...)
	fired = append(fired, ruleDropped...)
	if len(keptLenses) == 0 && len(keptRules) == 0 {
		return Origin{}, fired
	}
	p.Lenses, p.Rules = keptLenses, keptRules
	origin.Pack = &p
	return origin, fired
}

// packToggled reports whether a builtin pack is named by its full name or its
// slug.
func packToggled(origin Origin, packs map[string]bool) bool {
	if len(packs) == 0 {
		return false
	}
	return packs[origin.Pack.Name] || packs[builtinSlug(origin)]
}

// filterLenses copies the lenses a disable list keeps.
func filterLenses(all []Lens, disabled map[string]bool) ([]Lens, []string) {
	if len(disabled) == 0 {
		return all, nil
	}
	out := make([]Lens, 0, len(all))
	var fired []string
	for i := range all {
		if disabled[all[i].ID] {
			fired = append(fired, "lens:"+all[i].ID)
			continue
		}
		out = append(out, all[i])
	}
	return out, fired
}

// filterRules copies the rules a disable list keeps.
func filterRules(all []Rule, disabled map[string]bool) ([]Rule, []string) {
	if len(disabled) == 0 {
		return all, nil
	}
	out := make([]Rule, 0, len(all))
	var fired []string
	for i := range all {
		if disabled[all[i].ID] {
			fired = append(fired, "rule:"+all[i].ID)
			continue
		}
		out = append(out, all[i])
	}
	return out, fired
}

// unmatchedToggles names every configured id no builtin pack, lens, or rule
// declares.
func unmatchedToggles(origins []Origin, packs, lenses, rules map[string]bool) []string {
	known := knownBuiltinIDs(origins)
	entries := toggleEntries(packs, lenses, rules)
	out := make([]string, 0, len(entries))
	for i := range entries {
		if known[entries[i].key] {
			continue
		}
		out = append(out, fmt.Sprintf("pack toggle %q names no builtin %s — ignored",
			entries[i].id, entries[i].kind))
	}
	return out
}

// knownBuiltinIDs indexes every id the builtin layer declares, keyed by kind so
// a toggle can be checked without walking the layer again.
func knownBuiltinIDs(origins []Origin) map[string]bool {
	known := map[string]bool{}
	for i := range origins {
		recordKnownIDs(known, &origins[i])
	}
	return known
}

// recordKnownIDs indexes one origin's ids when it belongs to the builtin layer.
func recordKnownIDs(known map[string]bool, origin *Origin) {
	if origin.Layer != LayerBuiltin || origin.Pack == nil {
		return
	}
	known["pack:"+origin.Pack.Name] = true
	known["pack:"+builtinSlug(*origin)] = true
	for i := range origin.Pack.Lenses {
		known["lens:"+origin.Pack.Lenses[i].ID] = true
	}
	for i := range origin.Pack.Rules {
		known["rule:"+origin.Pack.Rules[i].ID] = true
	}
}

// toggleEntry is one configured toggle split into its kind and id.
type toggleEntry struct {
	kind string
	id   string
	key  string
}

// toggleEntries renders every configured toggle as a kind, an id, and the
// prefixed key the known set indexes.
func toggleEntries(packs, lenses, rules map[string]bool) []toggleEntry {
	out := make([]toggleEntry, 0, len(packs)+len(lenses)+len(rules))
	out = appendToggleEntries(out, "pack", packs)
	out = appendToggleEntries(out, "lens", lenses)
	out = appendToggleEntries(out, "rule", rules)
	slices.SortFunc(out, func(a, b toggleEntry) int { return strings.Compare(a.key, b.key) })
	return out
}

// appendToggleEntries appends one kind's configured ids.
func appendToggleEntries(out []toggleEntry, kind string, ids map[string]bool) []toggleEntry {
	for id := range ids {
		out = append(out, toggleEntry{kind: kind, id: id, key: kind + ":" + id})
	}
	return out
}

// newIDSet indexes one configured list.
func newIDSet(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// mapKeys lists a set's keys, the one place a set becomes a sorted slice.
func mapKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	return out
}
