package config

import (
	"regexp"

	"github.com/stainless-code/verdict/internal/core"
)

// The built-in pack toggle grammars: a pack entry is a full
// `<namespace>/<slug>` name or a bare slug, a lens id is the pack loader's
// lowercase slug, and a rule id is the knowledge-base grammar the loader
// enforces.
var (
	packToggleNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`)
	packToggleSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	packToggleRuleRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,64}$`)
)

// buildPacks validates the built-in pack composition section.
func buildPacks(raw any, path string) (*core.ConfigPacks, *core.RefusalError) {
	obj, ref := asObject(raw, path)
	if ref != nil {
		return nil, ref
	}
	out := &core.ConfigPacks{}
	var err *core.RefusalError
	if out.MergeBuiltin, err = presentValue(obj, "merge_builtin", path, asBool); err != nil {
		return nil, err
	}
	if out.DisablePacks, err = presentStrings(obj, "disable_packs", path, func(raw any, at string) ([]string, *core.RefusalError) {
		return toggleList(raw, at, packToggleNameRE, packToggleSlugRE)
	}); err != nil {
		return nil, err
	}
	if out.DisableLenses, err = presentStrings(obj, "disable_lenses", path, func(raw any, at string) ([]string, *core.RefusalError) {
		return toggleList(raw, at, packToggleSlugRE)
	}); err != nil {
		return nil, err
	}
	if out.DisableRules, err = presentStrings(obj, "disable_rules", path, func(raw any, at string) ([]string, *core.RefusalError) {
		return toggleList(raw, at, packToggleRuleRE)
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// toggleList reads one non-empty array of ids, each matching at least one of
// the grammars the list accepts, with no duplicates: a repeated id would make
// the applied-toggle report ambiguous.
func toggleList(raw any, path string, accept ...*regexp.Regexp) ([]string, *core.RefusalError) {
	items, ok := raw.([]any)
	if !ok {
		return nil, refuse(CodeBadConfig, "%s: want an array of ids, got %s", path, jsonKind(raw))
	}
	if len(items) == 0 {
		return nil, refuse(CodeBadConfig, "%s: want at least one id; omit the key to disable nothing", path)
	}
	out := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		id, ref := toggleID(item, path, accept)
		if ref != nil {
			return nil, ref
		}
		if seen[id] {
			return nil, refuse(CodeBadConfig, "%s: %q is listed twice", path, id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// toggleID validates one entry against the accepted grammars.
func toggleID(item any, path string, accept []*regexp.Regexp) (string, *core.RefusalError) {
	id, ok := item.(string)
	if !ok {
		return "", refuse(CodeBadConfig, "%s: want a string id, got %s", path, jsonKind(item))
	}
	for _, re := range accept {
		if re.MatchString(id) {
			return id, nil
		}
	}
	return "", refuse(CodeBadConfig, "%s: %q is not a lowercase slug%s", path, id, toggleShape(accept))
}

// toggleShape renders the accepted spelling for the refusal.
func toggleShape(accept []*regexp.Regexp) string {
	if len(accept) > 1 {
		return " or a <namespace>/<slug> pack name"
	}
	return ""
}
