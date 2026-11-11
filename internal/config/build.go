package config

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	// fieldNameKey is the required human label on scopes and clauses.
	fieldNameKey = "name"
	// msgMissingField names a missing required key.
	msgMissingField = "%s: missing required field %q"
	// keySep joins a parent path to a child field name.
	keySep = "."
)

// buildConfig validates the semantic rules and constructs the typed
// configuration. Unknown fields are already refused; every value below is
// checked for null placement, range, enum, glob, and cross-field shape
// before it reaches the typed struct, so the runtime never casts blindly.
// rootLocation names the document root in field-path refusals.
const rootLocation = "$"

func buildConfig(root map[string]any) (*core.Config, *core.RefusalError) {
	cfg := &core.Config{SchemaVersion: "1"}
	if raw, ok := root["$schema"]; ok {
		str, isStr := raw.(string)
		if !isStr {
			return nil, refuse(CodeBadConfig, "$schema: want a string, got %s", jsonKind(raw))
		}
		cfg.SchemaRef = str
	}
	var ref *core.RefusalError
	if cfg.Defaults, ref = presentNested(root, defaultsKey, rootLocation, buildDefaults); ref != nil {
		return nil, ref
	}
	if cfg.Requirements, ref = presentNested(root, requirementsKey, rootLocation, buildRequirements); ref != nil {
		return nil, ref
	}
	if cfg.Index, ref = presentNested(root, "index", rootLocation, buildIndex); ref != nil {
		return nil, ref
	}
	if ref = buildConfigTail(cfg, root); ref != nil {
		return nil, ref
	}
	return cfg, nil
}

// buildConfigTail validates scopes, profiles, the default profile, and
// the cross-field references between them.
func buildConfigTail(cfg *core.Config, root map[string]any) *core.RefusalError {
	var ref *core.RefusalError
	if cfg.Scopes, ref = presentNested(root, "scopes", rootLocation, func(raw any, _ string) ([]core.ConfigScope, *core.RefusalError) {
		return buildScopes(raw)
	}); ref != nil {
		return ref
	}
	profiles := func(raw any, _ string) (map[string]core.ConfigProfile, *core.RefusalError) {
		return buildProfiles(raw)
	}
	if cfg.Profiles, ref = presentNested(root, "profiles", rootLocation, profiles); ref != nil {
		return ref
	}
	if raw, ok := root["default_profile"]; ok {
		name, ref := nonEmptyString(raw, "$.default_profile")
		if ref != nil {
			return ref
		}
		if _, known := cfg.Profiles[name]; !known {
			return refuse(CodeBadConfig, "$.default_profile: unknown profile %q", name)
		}
		cfg.DefaultProfile = name
	}
	return checkCrossRefs(cfg)
}

// checkCrossRefs enforces the name contracts spanning sections: unique
// clause names, unique scope names, and resolvable profile references.
func checkCrossRefs(cfg *core.Config) *core.RefusalError {
	if ref := checkClauseNames(cfg); ref != nil {
		return ref
	}
	if ref := checkScopeNames(cfg); ref != nil {
		return ref
	}
	return checkScopeProfileRefs(cfg)
}

// presentNested parses one optional nested object or array: absent reads
// the zero value, present parses through the caller's builder — including
// an explicit null, which refuses as misplaced through the builder's
// shape check.
func presentNested[T any](obj map[string]any, key, path string, build func(any, string) (T, *core.RefusalError)) (T, *core.RefusalError) {
	var zero T
	raw, ok := obj[key]
	if !ok {
		return zero, nil
	}
	return build(raw, path+keySep+key)
}

// presentValue parses one optional scalar key: absent reads nil, present
// parses through the caller's validator.
func presentValue[T any](obj map[string]any, key, path string, parse func(any, string) (T, *core.RefusalError)) (*T, *core.RefusalError) {
	raw, ok := obj[key]
	if !ok {
		return nil, nil
	}
	val, ref := parse(raw, path+keySep+key)
	if ref != nil {
		return nil, ref
	}
	return &val, nil
}

// stringSliceParser validates one string-slice value at its path.
type stringSliceParser = func(any, string) ([]string, *core.RefusalError)

// presentStrings parses one optional string-slice key through the caller's
// element validator: absent reads nil.
func presentStrings(obj map[string]any, key, path string, parse stringSliceParser) ([]string, *core.RefusalError) {
	raw, ok := obj[key]
	if !ok {
		return nil, nil
	}
	return parse(raw, path+keySep+key)
}

// presentEnum parses one optional vocabulary key.
func presentEnum(obj map[string]any, key, path string, vocab []string) (*string, *core.RefusalError) {
	return presentValue(obj, key, path, func(raw any, at string) (string, *core.RefusalError) {
		return asEnum(raw, at, vocab)
	})
}

// requiredString reads one required non-empty string key.
func requiredString(obj map[string]any, key, path, code string) (string, *core.RefusalError) {
	raw, ok := obj[key]
	if !ok {
		return "", refuse(code, msgMissingField, path, key)
	}
	val, ref := nonEmptyString(raw, path+keySep+key)
	if ref != nil {
		return "", remap(raw, path+keySep+key, ref, code)
	}
	return val, nil
}

// buildIndex validates the local index caps.
func buildIndex(raw any, path string) (*core.ConfigIndex, *core.RefusalError) {
	obj, ref := asObject(raw, path)
	if ref != nil {
		return nil, ref
	}
	out := &core.ConfigIndex{}
	var err *core.RefusalError
	if out.Files, err = presentValue(obj, "files", path, countAtLeastOne); err != nil {
		return nil, err
	}
	if out.FileBytes, err = presentValue(obj, "file_bytes", path, positiveInt); err != nil {
		return nil, err
	}
	if out.TreeBytes, err = presentValue(obj, "tree_bytes", path, positiveInt); err != nil {
		return nil, err
	}
	if out.ShardsBytes, err = presentValue(obj, "shards_bytes", path, positiveInt); err != nil {
		return nil, err
	}
	if out.KeepTrees, err = presentValue(obj, "keep_trees", path, countAtLeastOne); err != nil {
		return nil, err
	}
	out.RefsPerFile, err = presentValue(obj, "refs_per_file", path, countAtLeastOne)
	return out, err
}

// countAtLeastOne reads one positive count.
func countAtLeastOne(raw any, path string) (int, *core.RefusalError) {
	return boundedCount(raw, path, 1)
}

// buildDefaults validates the optional defaults object.
func buildDefaults(raw any, path string) (*core.ConfigDefaults, *core.RefusalError) {
	obj, ref := asObject(raw, path)
	if ref != nil {
		return nil, ref
	}
	out := &core.ConfigDefaults{}
	if ref := buildDefaultsHead(obj, path, out); ref != nil {
		return nil, ref
	}
	return out, buildDefaultsTail(obj, path, out)
}

// buildDefaultsHead validates the spend and capacity defaults: panel,
// budget, byte cap, chunk opt-in, and the knowledge-base policy.
func buildDefaultsHead(obj map[string]any, path string, out *core.ConfigDefaults) *core.RefusalError {
	var ref, err *core.RefusalError
	if out.Panel, ref = presentStrings(obj, "panel", path, func(raw any, at string) ([]string, *core.RefusalError) {
		return nonEmptyStringArray(raw, at, 1)
	}); ref != nil {
		return ref
	}
	if out.BudgetUSD, err = presentValue(obj, keyBudgetUSD, path, positiveFloat); err != nil {
		return err
	}
	if out.MaxBytes, err = presentValue(obj, "max_bytes", path, positiveInt); err != nil {
		return err
	}
	if out.Chunk, err = presentValue(obj, "chunk", path, asBool); err != nil {
		return err
	}
	out.KBPolicy, ref = presentNested(obj, "kb_policy", path, buildKBPolicy)
	return ref
}

// buildDefaultsTail validates the advisory and display defaults: effort
// and the two severity floors.
func buildDefaultsTail(obj map[string]any, path string, out *core.ConfigDefaults) *core.RefusalError {
	var err *core.RefusalError
	if out.Effort, err = presentEnum(obj, "effort", path, effortEnum); err != nil {
		return err
	}
	if out.DisplayMinSeverity, err = presentEnum(obj, "display_min_severity", path, severityEnum); err != nil {
		return err
	}
	out.PostMinSeverity, err = presentEnum(obj, "post_min_severity", path, severityEnum)
	return err
}

// buildKBPolicy validates the nested retirement quorum and ceiling.
// Absent reads nil; present must be an object.
func buildKBPolicy(raw any, path string) (*core.ConfigKBPolicy, *core.RefusalError) {
	obj, ref := asObject(raw, path)
	if ref != nil {
		return nil, ref
	}
	out := &core.ConfigKBPolicy{}
	var err *core.RefusalError
	if out.MinJudged, err = presentValue(obj, "min_judged", path, func(raw any, at string) (int, *core.RefusalError) {
		return boundedCount(raw, at, 1)
	}); err != nil {
		return nil, err
	}
	if out.MaxFPRate, err = presentValue(obj, "max_fp_rate", path, func(raw any, at string) (float64, *core.RefusalError) {
		return boundedFloat(raw, at, 0, 1)
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// buildRequirements validates the optional requirements object.
func buildRequirements(raw any, path string) (*core.ConfigRequirements, *core.RefusalError) {
	obj, ref := asObject(raw, path)
	if ref != nil {
		return nil, remap(raw, path, ref, CodeBadRequirement)
	}
	out := &core.ConfigRequirements{}
	if out.BlockClauses, ref = presentNested(obj, "block_clauses", path, buildClauseList); ref != nil {
		return nil, ref
	}
	if out.RequireChecks, ref = presentStrings(obj, "require_checks", path, checkIDArray); ref != nil {
		return nil, remap(obj["require_checks"], path+".require_checks", ref, CodeBadRequirement)
	}
	if out.RequireBind, ref = presentValue(obj, "require_bind", path, asBool); ref != nil {
		return nil, remap(obj["require_bind"], path+".require_bind", ref, CodeBadRequirement)
	}
	return out, nil
}

// buildClauseList validates an optional non-empty clause array. Absent
// reads nil; present must hold at least one clause.
func buildClauseList(raw any, path string) ([]core.BlockClause, *core.RefusalError) {
	items, ref := asArray(raw, path)
	if ref != nil {
		return nil, remap(raw, path, ref, CodeBadRequirement)
	}
	if len(items) == 0 {
		return nil, refuse(CodeBadRequirement, "%s: want 1 or more clauses", path)
	}
	out := make([]core.BlockClause, 0, len(items))
	for i := range items {
		clause, ref := buildClause(items[i], fmt.Sprintf("%s[%d]", path, i))
		if ref != nil {
			return nil, ref
		}
		out = append(out, clause)
	}
	return out, nil
}

// buildClause validates one blocking clause.
func buildClause(raw any, path string) (core.BlockClause, *core.RefusalError) {
	var zero core.BlockClause
	obj, ref := asObject(raw, path)
	if ref != nil {
		return zero, remap(raw, path, ref, CodeBadRequirement)
	}
	name, ref := requiredString(obj, fieldNameKey, path, CodeBadRequirement)
	if ref != nil {
		return zero, ref
	}
	out := core.BlockClause{Name: name}
	threshold, ref := requiredEnum(obj, "threshold", path, severityEnum, CodeBadRequirement)
	if ref != nil {
		return zero, ref
	}
	out.Threshold = threshold
	if out.Categories, ref = presentStrings(obj, "categories", path, func(raw any, at string) ([]string, *core.RefusalError) {
		return enumArray(raw, at, categoryEnum, 1)
	}); ref != nil {
		return zero, remap(obj["categories"], path+".categories", ref, CodeBadRequirement)
	}
	if out.Paths, ref = presentStrings(obj, "paths", path, func(raw any, at string) ([]string, *core.RefusalError) {
		return globArray(raw, at, 1)
	}); ref != nil {
		return zero, remap(obj["paths"], path+".paths", ref, CodeBadRequirement)
	}
	return out, nil
}

// requiredEnum reads one required vocabulary key.
func requiredEnum(obj map[string]any, key, path string, vocab []string, code string) (string, *core.RefusalError) {
	raw, ok := obj[key]
	if !ok {
		return "", refuse(code, msgMissingField, path, key)
	}
	val, ref := asEnum(raw, path+keySep+key, vocab)
	if ref != nil {
		return "", remap(raw, path+keySep+key, ref, code)
	}
	return val, nil
}

// buildScopes validates the optional scope array, possibly empty.
func buildScopes(raw any) ([]core.ConfigScope, *core.RefusalError) {
	items, ref := asArray(raw, "$.scopes")
	if ref != nil {
		return nil, remap(raw, "$.scopes", ref, CodeBadScope)
	}
	var out []core.ConfigScope
	for i := range items {
		scope, ref := buildScope(items[i], scopePath(i))
		if ref != nil {
			return nil, ref
		}
		out = append(out, scope)
	}
	return out, nil
}

// buildScope validates one path-scoped entry and its presence rules: at
// least one of profile, defaults, or requirements must be present, and a
// named profile never combines with inline defaults.
func buildScope(raw any, path string) (core.ConfigScope, *core.RefusalError) {
	var zero core.ConfigScope
	obj, ref := scopeObject(raw, path)
	if ref != nil {
		return zero, ref
	}
	name, ref := requiredString(obj, fieldNameKey, path, CodeBadScope)
	if ref != nil {
		return zero, ref
	}
	include, ref := requiredGlobs(obj, "include", path, 1)
	if ref != nil {
		return zero, ref
	}
	out := core.ConfigScope{Name: name, Include: include}
	if ref := buildScopeInputs(obj, path, &out); ref != nil {
		return zero, ref
	}
	return out, checkScopeRules(&out, path)
}

func scopeObject(raw any, path string) (map[string]any, *core.RefusalError) {
	obj, ref := asObject(raw, path)
	if ref != nil {
		return nil, remap(raw, path, ref, CodeBadScope)
	}
	return obj, nil
}

// checkScopeRules enforces the scope presence contract: at least one of
// profile, defaults, or requirements, and never profile with inline
// defaults.
func checkScopeRules(out *core.ConfigScope, path string) *core.RefusalError {
	if out.Profile == nil && out.Defaults == nil && out.Requirements == nil {
		return refuse(CodeBadScope, "%s: a scope needs one of profile, defaults, or requirements", path)
	}
	if out.Profile != nil && out.Defaults != nil {
		return refuse(CodeBadScope, "%s: profile and inline defaults are exclusive", path)
	}
	return nil
}

// buildScopeInputs validates the optional resolution inputs: inline
// exclude globs, the named profile, inline defaults, and inline
// requirements.
func buildScopeInputs(obj map[string]any, path string, out *core.ConfigScope) *core.RefusalError {
	var ref *core.RefusalError
	if out.Exclude, ref = presentStrings(obj, "exclude", path, func(raw any, at string) ([]string, *core.RefusalError) {
		return globArray(raw, at, 0)
	}); ref != nil {
		return remap(obj["exclude"], path+".exclude", ref, CodeBadScope)
	}
	profile, ref := presentValue(obj, "profile", path, nonEmptyString)
	if ref != nil {
		return remap(obj["profile"], path+".profile", ref, CodeBadScope)
	}
	out.Profile = profile
	if out.Defaults, ref = presentNested(obj, defaultsKey, path, buildDefaults); ref != nil {
		return remapScope(obj[defaultsKey], path+".defaults", ref)
	}
	out.Requirements, ref = presentNested(obj, requirementsKey, path, buildRequirements)
	return ref
}

// requiredGlobs reads one required portable-glob array with at least
// minLen entries.
func requiredGlobs(obj map[string]any, key, path string, minLen int) ([]string, *core.RefusalError) {
	raw, ok := obj[key]
	if !ok {
		return nil, refuse(CodeBadScope, msgMissingField, path, key)
	}
	paths, ref := globArray(raw, path+keySep+key, minLen)
	if ref != nil {
		return nil, remap(raw, path+keySep+key, ref, CodeBadScope)
	}
	return paths, nil
}

// remapScope keeps bad-requirement for inline requirement problems inside
// a scope and maps every other inline-defaults problem onto bad-scope.
func remapScope(_ any, _ string, ref *core.RefusalError) *core.RefusalError {
	if ref.Code == CodeBadRequirement {
		return ref
	}
	return remap(nil, "", ref, CodeBadScope)
}

// buildProfiles validates the optional profile map. Member keys are
// profile names; empty names refuse.
func buildProfiles(raw any) (map[string]core.ConfigProfile, *core.RefusalError) {
	obj, ref := asObject(raw, "$.profiles")
	if ref != nil {
		return nil, ref
	}
	var out map[string]core.ConfigProfile
	for _, name := range sortedObjectKeys(obj) {
		prof, ref := addProfile(obj, name)
		if ref != nil {
			return nil, ref
		}
		if out == nil {
			out = map[string]core.ConfigProfile{}
		}
		out[name] = prof
	}
	return out, nil
}

// addProfile validates one named profile value, refusing empty names.
func addProfile(obj map[string]any, name string) (core.ConfigProfile, *core.RefusalError) {
	var zero core.ConfigProfile
	if len(name) == 0 {
		return zero, refuse(CodeBadConfig, "$.profiles: profile name must not be empty")
	}
	return buildProfile(obj[name], "$.profiles."+name)
}

// buildProfile validates one named work profile.
func buildProfile(raw any, path string) (core.ConfigProfile, *core.RefusalError) {
	var zero core.ConfigProfile
	obj, ref := asObject(raw, path)
	if ref != nil {
		return zero, ref
	}
	out := core.ConfigProfile{}
	var err *core.RefusalError
	if out.Depth, err = presentEnum(obj, "depth", path, depthEnum); err != nil {
		return zero, err
	}
	if out.Checks, err = presentStrings(obj, "checks", path, checkIDArray); err != nil {
		return zero, err
	}
	if out.MaxLenses, err = presentValue(obj, "max_lenses", path, func(raw any, at string) (int, *core.RefusalError) {
		return boundedCount(raw, at, 0)
	}); err != nil {
		return zero, err
	}
	return out, nil
}

// checkClauseNames enforces file-wide clause-name uniqueness across the
// root and every scope's inline requirements.
func checkClauseNames(cfg *core.Config) *core.RefusalError {
	seen := map[string]string{}
	if cfg.Requirements != nil {
		if ref := noteClauseNames(seen, cfg.Requirements.BlockClauses, "$.requirements"); ref != nil {
			return ref
		}
	}
	for si := range cfg.Scopes {
		if ref := noteScopeClauseNames(seen, &cfg.Scopes[si], si); ref != nil {
			return ref
		}
	}
	return nil
}

func noteScopeClauseNames(seen map[string]string, scope *core.ConfigScope, si int) *core.RefusalError {
	if scope.Requirements == nil {
		return nil
	}
	return noteClauseNames(seen, scope.Requirements.BlockClauses, scopePath(si)+".requirements")
}

// noteClauseNames records one clause list, refusing on the first repeat.
func noteClauseNames(seen map[string]string, clauses []core.BlockClause, base string) *core.RefusalError {
	for i := range clauses {
		path := fmt.Sprintf("%s.block_clauses[%d]", base, i)
		if first, dup := seen[clauses[i].Name]; dup {
			return refuse(CodeBadRequirement, "%s: clause name %q is already used at %s", path, clauses[i].Name, first)
		}
		seen[clauses[i].Name] = path
	}
	return nil
}

// checkScopeNames enforces scope-name uniqueness.
func checkScopeNames(cfg *core.Config) *core.RefusalError {
	seen := map[string]int{}
	for i := range cfg.Scopes {
		if first, dup := seen[cfg.Scopes[i].Name]; dup {
			return refuse(CodeBadScope, "%s: scope name %q is already used at %s", scopePath(i), cfg.Scopes[i].Name, scopePath(first))
		}
		seen[cfg.Scopes[i].Name] = i
	}
	return nil
}

// checkScopeProfileRefs refuses a scope naming a profile the file never
// defines. The contract resolves `profile` as a name from `profiles`, so
// a dangling reference is a scope problem, not a silent default.
func checkScopeProfileRefs(cfg *core.Config) *core.RefusalError {
	for i := range cfg.Scopes {
		profile := cfg.Scopes[i].Profile
		if profile == nil {
			continue
		}
		if _, ok := cfg.Profiles[*profile]; !ok {
			return refuse(CodeBadScope, "%s: unknown profile %q", scopePath(i)+".profile", *profile)
		}
	}
	return nil
}

// remap rewrites a generic shape refusal onto the owning layer's code,
// preserving refusals that already carry a layer code.
func remap(_ any, _ string, ref *core.RefusalError, code string) *core.RefusalError {
	if ref.Code == CodeBadRequirement || ref.Code == CodeBadScope {
		return ref
	}
	return refuse(code, "%s", ref.Message)
}
