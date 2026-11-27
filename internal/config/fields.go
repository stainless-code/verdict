package config

import (
	"strings"

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

// scopeOnlyNote is the shared tail of the global-only scope refusals.
const scopeOnlyNote = "not inside a scope"

var (
	topFields = []string{
		"$schema", "schema_version", "defaults", "requirements", "trigger", "scopes", "profiles", "default_profile",
		"index", "severity_weights", "rigor_policy", "packs", "tests", "verify",
	}
	defaultsFields = []string{
		keyPanel, keyBudgetUSD, keyMonthlyBudgetUSD, keyMaxBytes, keyChunk, keyKBPolicy, keyEffort,
		keyDisplayFloor, keyPostFloor, keySpecCoverageMin, keyFinders, keyBudget,
		keySensitivePaths,
	}
	kbPolicyFields     = []string{"min_judged", "max_fp_rate", "max_chars"}
	rigorPolicyFields  = append([]string{"mode"}, severityEnum...)
	testsFields        = []string{"draft"}
	verifyFields       = []string{"share"}
	requirementsFields = []string{"block_clauses", "require_checks", "require_bind"}
	triggerFields      = []string{"authors", "paths_include", "paths_exclude", "labels", "title_matches", "base_branches", "draft"}
	clauseFields       = []string{"name", "threshold", "categories", "paths"}
	scopeFields        = []string{"name", "include", "exclude", "profile", "defaults", "requirements"}
	profileFields      = []string{"depth", "checks", "max_lenses"}
	indexFields        = []string{"files", "file_bytes", "tree_bytes", "shards_bytes", "keep_trees", "refs_per_file"}
	packsFields        = []string{"merge_builtin", "disable_packs", "disable_lenses", "disable_rules", "org_pack"}
	orgPackFields      = []string{"repo", "ref", "path"}
	// personalTopFields lists the recognized operator-settings keys: the
	// typed personal sections plus the CLI prefs keys the prefs layer
	// owns (output, layout, color, publish), which parse but never feed
	// the resolver.
	personalTopFields     = core.SettingsTopKeys()
	personalSpendFields   = []string{"budget_usd", "monthly_budget_usd", "require_known_estimate", "enforce_cap"}
	personalPrivacyFields = []string{"exclusions"}
	personalAgentFields   = []string{
		"binary", "args", "prompt_flag", "model_flag", "install", "probe_args", "probe_match",
		"state_locations",
	}
)

// The typed operator-settings sections: named once so the field contract,
// the section walker, and the parser cannot drift apart.
const (
	personalSectionReview  = "review"
	personalSectionSpend   = "spend"
	personalSectionPrivacy = "privacy"
	personalSectionPacks   = "packs"
	personalSectionAgents  = "agents"
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
		{key: "trigger", check: checkKeysSection(triggerFields)},
		{key: "scopes", check: checkScopeListKeys},
		{key: "profiles", check: checkProfilesKeys},
		{key: "severity_weights", check: checkKeysSection(severityEnum)},
		{key: "index", check: checkKeysSection(indexFields)},
		{key: "rigor_policy", check: checkKeysSection(rigorPolicyFields)},
		{key: "packs", check: checkPacksKeys},
		{key: "tests", check: checkKeysSection(testsFields)},
		{key: "verify", check: checkKeysSection(verifyFields)},
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

// checkPacksKeys walks the packs object and its nested org_pack pin.
func checkPacksKeys(raw any, path string) *core.RefusalError {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if ref := checkKeys(path, obj, packsFields); ref != nil {
		return ref
	}
	pin, ok := obj["org_pack"]
	if !ok {
		return nil
	}
	pinObj, ok := pin.(map[string]any)
	if !ok {
		return nil // semantic pass names the misplaced null or wrong type
	}
	return checkKeys(path+".org_pack", pinObj, orgPackFields)
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

// scopeGlobalOnly pairs one global-only default key with its refusal:
// the message carries the scope defaults path and the refusal code.
// scopeGlobalOnly is one key a scope may not carry; msg follows the scope's
// path and a dot in the refusal.
type scopeGlobalOnly struct {
	key  string
	code string
	msg  string
}

var scopeGlobalOnlyKeys = []scopeGlobalOnly{
	{
		key:  keyKBPolicy,
		code: CodeBadScope,
		msg:  "kb_policy: kb_policy is global-only — set it in root defaults or personal settings, " + scopeOnlyNote,
	},
	{
		key:  keyFinders,
		code: CodeBadScope,
		msg:  "finders: finders is global-only — set root defaults or personal settings",
	},
	{
		key:  keyBudget,
		code: CodeBadScope,
		msg:  "budget: budget is global-only — set root defaults or personal settings",
	},
	{
		key:  keySensitivePaths,
		code: CodeBadScope,
		msg: "sensitive_paths: sensitive_paths is global-only — set it in root defaults or personal settings, " +
			scopeOnlyNote,
	},
	{
		key:  keySpecCoverageMin,
		code: CodeBadScope,
		msg: "spec_coverage_min: spec_coverage_min is global-only — set it in root defaults or personal settings, " +
			scopeOnlyNote,
	},
	{
		key:  keyMonthlyBudgetUSD,
		code: CodeBadConfig,
		msg: "monthly_budget_usd: monthly_budget_usd is a repo-wide default only — set it in root defaults or personal settings, " +
			scopeOnlyNote,
	},
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
		return nil
	}
	for _, entry := range scopeGlobalOnlyKeys {
		if _, present := obj[entry.key]; present {
			return refuse(entry.code, "%s.%s", path, entry.msg)
		}
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

const unknownFieldSuffix = ": unknown field"

// IsUnknownField reports whether ref is the strict parser's unknown-key
// refusal. The trigger gate keeps that code verbatim and wraps every other
// parse failure as a trigger failure.
func IsUnknownField(ref *core.RefusalError) bool {
	return ref != nil && ref.Code == CodeBadConfig && strings.HasSuffix(ref.Message, unknownFieldSuffix)
}

// checkKeys refuses the first key outside the allowed set, naming its path.
func checkKeys(path string, obj map[string]any, allowed []string) *core.RefusalError {
	keep := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		keep[k] = true
	}
	for _, key := range sortedObjectKeys(obj) {
		if !keep[key] {
			return refuse(CodeBadConfig, "%s.%s%s", path, key, unknownFieldSuffix)
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
