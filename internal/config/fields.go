package config

import (
	"github.com/stainless-code/verdict/internal/core"
)

// Fixed-shape field inventories. User-defined map keys are exempt by
// construction: profile names under `profiles`, and `name` values (never
// keys) on scopes and clauses. Absent optional keys take defaults; presence
// is never repaired or rewritten.
// defaultsKey addresses the shared defaults object.
const defaultsKey = "defaults"

// requirementsKey addresses the shared requirements object.
const requirementsKey = "requirements"

var (
	topFields      = []string{"$schema", "schema_version", "defaults", "requirements", "scopes", "profiles", "default_profile", "index"}
	defaultsFields = []string{
		keyPanel, keyBudgetUSD, keyMaxBytes, keyChunk, keyKBPolicy, keyEffort,
		keyDisplayFloor, keyPostFloor,
	}
	kbPolicyFields     = []string{"min_judged", "max_fp_rate"}
	requirementsFields = []string{"block_clauses", "require_checks", "require_bind"}
	clauseFields       = []string{"name", "threshold", "categories", "paths"}
	scopeFields        = []string{"name", "include", "exclude", "profile", "defaults", "requirements"}
	profileFields      = []string{"depth", "checks", "max_lenses"}
	indexFields        = []string{"files", "file_bytes", "tree_bytes", "shards_bytes", "keep_trees", "refs_per_file"}
	// personalTopFields lists the recognized operator-settings keys: the
	// typed personal sections plus the CLI prefs keys the prefs layer
	// owns (output, layout, color, publish), which parse but never feed
	// the resolver.
	personalTopFields = []string{
		"$schema", personalSectionReview, personalSectionSpend, personalSectionPrivacy,
		"output", "layout", "color", "publish",
	}
	personalSpendFields   = []string{"budget_usd", "require_known_estimate", "enforce_cap"}
	personalPrivacyFields = []string{"exclusions"}
)

// The typed operator-settings sections: named once so the field contract,
// the section walker, and the parser cannot drift apart.
const (
	personalSectionReview  = "review"
	personalSectionSpend   = "spend"
	personalSectionPrivacy = "privacy"
)

// checkUnknownFields refuses any key outside the contract on fixed-shape
// objects, naming the exact field path. The `$schema` key is recognized
// metadata: present but non-string refuses bad-config here.
func checkUnknownFields(root map[string]any) *core.RefusalError {
	if raw, ok := root["$schema"]; ok {
		if _, isStr := raw.(string); !isStr {
			return refuse(CodeBadConfig, "$schema: want a string, got %s", jsonKind(raw))
		}
	}
	if ref := checkKeys("$", root, topFields); ref != nil {
		return ref
	}
	for _, section := range []struct {
		check func(any, string) *core.RefusalError
		key   string
	}{
		{key: defaultsKey, check: checkDefaultsKeys},
		{key: requirementsKey, check: checkRequirementsKeys},
		{key: "scopes", check: checkScopeListKeys},
		{key: "profiles", check: checkProfilesKeys},
		{key: "index", check: checkKeysSection(indexFields)},
	} {
		if ref := checkSectionUnknown(root, section.key, section.check); ref != nil {
			return ref
		}
	}
	return nil
}

// checkSectionUnknown runs one section's key check when present. Absent
// reads clean; present but misshapen defers to the semantic pass, which
// names the misplaced null or wrong type.
func checkSectionUnknown(root map[string]any, key string, check func(any, string) *core.RefusalError) *core.RefusalError {
	raw, ok := root[key]
	if !ok {
		return nil
	}
	return check(raw, "$."+key)
}

// checkKeysSection returns a section walker over a fixed field list.
func checkKeysSection(fields []string) func(any, string) *core.RefusalError {
	return func(raw any, path string) *core.RefusalError {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil
		}
		return checkKeys(path, obj, fields)
	}
}

// checkDefaultsKeys walks the defaults object and its nested kb_policy.
func checkDefaultsKeys(raw any, path string) *core.RefusalError {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil // semantic pass names the misplaced null or wrong type
	}
	if ref := checkKeys(path, obj, defaultsFields); ref != nil {
		return ref
	}
	if kb, ok := obj[keyKBPolicy]; ok {
		kbObj, isObj := kb.(map[string]any)
		if !isObj {
			return nil
		}
		return checkKeys(path+".kb_policy", kbObj, kbPolicyFields)
	}
	return nil
}

// checkRequirementsKeys walks requirements and every clause object.
func checkRequirementsKeys(raw any, path string) *core.RefusalError {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if ref := checkKeys(path, obj, requirementsFields); ref != nil {
		return ref
	}
	clauses, ok := obj["block_clauses"].([]any)
	if !ok {
		return nil
	}
	for i := range clauses {
		if ref := checkClauseUnknown(clauses[i], clausePath(path, i)); ref != nil {
			return ref
		}
	}
	return nil
}

// checkClauseUnknown walks one clause object when it is one.
func checkClauseUnknown(raw any, path string) *core.RefusalError {
	clause, isObj := raw.(map[string]any)
	if !isObj {
		return nil
	}
	return checkKeys(path, clause, clauseFields)
}

// checkScopeListKeys walks every scope entry, including inline defaults,
// inline kb_policy, and inline requirements with their clauses.
func checkScopeListKeys(raw any, path string) *core.RefusalError {
	scopes, ok := raw.([]any)
	if !ok {
		return nil
	}
	for i := range scopes {
		if ref := checkScopeUnknown(scopes[i], path+"["+itoa(i)+"]"); ref != nil {
			return ref
		}
	}
	return nil
}

// checkScopeUnknown walks one scope entry and its inline objects.
func checkScopeUnknown(raw any, path string) *core.RefusalError {
	scope, isObj := raw.(map[string]any)
	if !isObj {
		return nil
	}
	if ref := checkKeys(path, scope, scopeFields); ref != nil {
		return ref
	}
	if ref := checkScopeDefaults(scope["defaults"], path+".defaults"); ref != nil {
		return ref
	}
	return checkPresentSection(scope, requirementsKey, path, checkRequirementsKeys)
}

// checkScopeDefaults walks one scope's inline defaults and refuses the KB
// policy there: knowledge-base thresholds are global-only, so a scope may carry
// a budget, a chunk size or an effort tier but never its own kb_policy.
func checkScopeDefaults(raw any, path string) *core.RefusalError {
	if raw == nil {
		return nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil // semantic pass names the misplaced null or wrong type
	}
	if _, present := obj[keyKBPolicy]; present {
		return refuse(CodeBadScope,
			"%s.kb_policy: kb_policy is global-only — set it in root defaults or personal settings, "+
				"not inside a scope", path)
	}
	return checkDefaultsKeys(raw, path)
}

// checkPresentSection runs one inline section check when present. Absent
// reads clean; present but misshapen defers to the semantic pass.
func checkPresentSection(obj map[string]any, key, path string, check func(any, string) *core.RefusalError) *core.RefusalError {
	raw, ok := obj[key]
	if !ok {
		return nil
	}
	return check(raw, path+"."+key)
}

// checkProfilesKeys walks every named profile. Member keys are profile
// names and exempt; their values are fixed-shape and checked.
func checkProfilesKeys(raw any, path string) *core.RefusalError {
	profiles, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	for _, name := range sortedObjectKeys(profiles) {
		if ref := checkProfileUnknown(profiles[name], path+"."+name); ref != nil {
			return ref
		}
	}
	return nil
}

// checkProfileUnknown walks one named profile value when it is an object.
func checkProfileUnknown(raw any, path string) *core.RefusalError {
	prof, isObj := raw.(map[string]any)
	if !isObj {
		return nil
	}
	return checkKeys(path, prof, profileFields)
}

// checkKeys refuses the first key outside the allowed set, naming its path.
func checkKeys(path string, obj map[string]any, allowed []string) *core.RefusalError {
	keep := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		keep[k] = true
	}
	for _, key := range sortedObjectKeys(obj) {
		if !keep[key] {
			return refuse(CodeBadConfig, "%s.%s: unknown field", path, key)
		}
	}
	return nil
}

// clausePath renders the canonical clause path inside requirements.
func clausePath(base string, i int) string {
	return base + ".block_clauses[" + itoa(i) + "]"
}

// scopePath renders the canonical scope path.
func scopePath(i int) string {
	return "$.scopes[" + itoa(i) + "]"
}
