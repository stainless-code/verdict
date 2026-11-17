package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// SettingsPath returns the operator settings file path from HOME and
// XDG_CONFIG_HOME: the one spelling the prefs layer and the pack reader use.
func SettingsPath(home, xdg string) string {
	base := home + "/.config"
	if xdg != "" {
		base = xdg
	}
	return filepath.Join(base, "verdict", "settings.json")
}

// Personal settings keep the existing file path (the operator settings
// file) and gain four optional sections: review, spend, privacy, and the
// explicit org pack list. This foundation slice models and parses those
// sections and wires them into the resolver; the CLI prefs layer keeps its
// existing behavior and adopts these sections in a later consumer slice.

// PersonalSpend holds the operator authorization limit and the strictness
// guards. The repository can never raise the personal limit: spend is a
// constraint, never a default.
type PersonalSpend struct {
	BudgetUSD            *float64
	RequireKnownEstimate bool
	EnforceCap           bool
}

// PersonalPrivacy holds the exclusion globs fetch enforces before any
// model-facing export is produced. Repository scopes and flags can never
// narrow, remove, or except these exclusions.
type PersonalPrivacy struct {
	Exclusions []string
}

// PersonalAgent is one operator-defined run agent from the prefs file. It has
// the same launch fields as a shipped row; Source is set by the run service,
// never by the document.
type PersonalAgent struct {
	Name       string
	Binary     string
	PromptFlag string
	ModelFlag  string
	Install    string
	ProbeMatch string
	Args       []string
	ProbeArgs  []string
}

// PersonalSettings is the parsed personal layer: review defaults (every
// defaults key except budget_usd, which lives in spend), the spend
// constraint, the privacy constraint, the explicit org pack paths, and the
// operator-defined run agents.
//
//nolint:govet // the sections read in resolver order
type PersonalSettings struct {
	Review  Partial
	Spend   PersonalSpend
	Privacy PersonalPrivacy
	Packs   []string
	Agents  []PersonalAgent
}

// spendPath addresses the personal spend constraint section.
const spendPath = "$.spend"

// packsPath addresses the explicit org pack list: directories, never a scan.
const packsPath = "$.packs"

// agentsPath addresses the operator-defined run agents: prefs only.
const (
	agentsPath = "$.agents"
	pathDot    = "."
)

// Shared diagnostics for the closed agents shape.
const (
	wantObjectMsg = "want an object, got %s"
	binaryField   = ".binary"
)

// agentNamePattern is the published name vocabulary: a lowercase slug that
// starts with an alphanumeric byte.
var agentNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ParsePersonal reads the personal sections in the same fixed order as
// the repository parser: size, JSON shape, credential scan, unknown
// fields, then semantic validation. A budget_usd inside review refuses as
// misplaced: it belongs under spend.
func ParsePersonal(raw []byte) (*PersonalSettings, *core.RefusalError) {
	if len(raw) > MaxConfigBytes {
		return nil, refuse(CodeTooLarge, "personal settings are %d bytes, over the %d-byte cap", len(raw), MaxConfigBytes)
	}
	doc, ref := decodeStrict(raw)
	if ref != nil {
		return nil, ref
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil, refuse(CodeBadConfig, "$: want a JSON object, got %s", jsonKind(doc))
	}
	if ref := credentialScanPersonal(root); ref != nil {
		return nil, ref
	}
	if ref := checkPersonalUnknown(root); ref != nil {
		return nil, ref
	}
	return buildPersonal(root)
}

// checkPersonalUnknown refuses unknown fixed-shape keys and a non-string
// $schema. It runs after the credential scan, matching the repository
// parser's fixed order.
func checkPersonalUnknown(root map[string]any) *core.RefusalError {
	if raw, ok := root["$schema"]; ok {
		if _, isStr := raw.(string); !isStr {
			return refuse(CodeBadConfig, "$schema: want a string, got %s", jsonKind(raw))
		}
	}
	if ref := checkKeys("$", root, personalTopFields); ref != nil {
		return ref
	}
	return checkPersonalSections(root)
}

// checkPersonalSections walks the three personal sections when present. The
// review section carries the same defaults shape as the repository layer, so it
// gets the same recursive walk: checking only its top level let a misspelled
// kb_policy member keep the built-in threshold while the identical typo in
// .verdict/config.json refused typed.
func checkPersonalSections(root map[string]any) *core.RefusalError {
	if ref := checkPresent(root, personalSectionReview, func(raw any) *core.RefusalError {
		return checkDefaultsKeys(raw, "$."+personalSectionReview)
	}); ref != nil {
		return ref
	}
	for _, section := range []struct {
		key    string
		fields []string
	}{
		{personalSectionSpend, personalSpendFields},
		{personalSectionPrivacy, personalPrivacyFields},
	} {
		if ref := checkPresent(root, section.key, flatKeys("$."+section.key, section.fields)); ref != nil {
			return ref
		}
	}
	return checkPresent(root, personalSectionAgents, checkAgentKeys)
}

// checkAgentKeys refuses unknown keys and wrong shapes inside the agents
// section before the semantic pass reads any field.
func checkAgentKeys(raw any) *core.RefusalError {
	obj, ok := raw.(map[string]any)
	if !ok {
		return agentRefuse(agentsPath, wantObjectMsg, jsonKind(raw))
	}
	for _, name := range sortedObjectKeys(obj) {
		value := obj[name]
		path := agentsPath + pathDot + name
		entry, ok := value.(map[string]any)
		if !ok {
			return agentRefuse(path, wantObjectMsg, jsonKind(value))
		}
		if ref := checkKeys(path, entry, personalAgentFields); ref != nil {
			return refuse(CodeBadSettings, "%s", ref.Message)
		}
	}
	return nil
}

// agentRefuse keeps every agents-section diagnostic in the settings refusal
// family, with the JSON path the operator has to edit.
func agentRefuse(path, format string, args ...any) *core.RefusalError {
	return refuse(CodeBadSettings, "%s: %s", path, fmt.Sprintf(format, args...))
}

// checkPresent runs one section check when the key is present; absent reads
// clean and the semantic pass names anything else.
func checkPresent(root map[string]any, key string, check func(any) *core.RefusalError) *core.RefusalError {
	raw, ok := root[key]
	if !ok {
		return nil
	}
	return check(raw)
}

// flatKeys refuses unknown keys at one level, deferring shape errors to the
// semantic pass.
func flatKeys(path string, fields []string) func(any) *core.RefusalError {
	return func(raw any) *core.RefusalError {
		obj, isObj := raw.(map[string]any)
		if !isObj {
			return nil
		}
		return checkKeys(path, obj, fields)
	}
}

// buildPersonal validates the three sections and constructs the typed
// personal layer.
func buildPersonal(root map[string]any) (*PersonalSettings, *core.RefusalError) {
	out := &PersonalSettings{}
	builders := []struct {
		build func(raw any) *core.RefusalError
		key   string
	}{
		{key: personalSectionReview, build: func(raw any) *core.RefusalError {
			val, ref := buildPersonalReview(raw)
			return assignSection(&out.Review, val, ref)
		}},
		{key: personalSectionSpend, build: func(raw any) *core.RefusalError {
			val, ref := buildPersonalSpend(raw)
			return assignSection(&out.Spend, val, ref)
		}},
		{key: personalSectionPrivacy, build: func(raw any) *core.RefusalError {
			val, ref := buildPersonalPrivacy(raw)
			return assignSection(&out.Privacy, val, ref)
		}},
		{key: personalSectionPacks, build: func(raw any) *core.RefusalError {
			val, ref := buildPersonalPacks(raw)
			return assignSection(&out.Packs, val, ref)
		}},
		{key: personalSectionAgents, build: func(raw any) *core.RefusalError {
			val, ref := buildPersonalAgents(raw)
			return assignSection(&out.Agents, val, ref)
		}},
	}
	// The section order is fixed: it decides which refusal a document
	// with several violations reports first.
	for _, b := range builders {
		raw, ok := root[b.key]
		if !ok {
			continue
		}
		if ref := b.build(raw); ref != nil {
			return nil, ref
		}
	}
	return out, nil
}

// assignSection stores one successfully parsed personal section and
// forwards its refusal: a refused section contributes nothing.
func assignSection[T any](dst *T, val T, ref *core.RefusalError) *core.RefusalError {
	if ref != nil {
		return ref
	}
	*dst = val
	return nil
}

// buildPersonalReview validates the personal review defaults: every
// defaults key except budget_usd, which refuses here as misplaced.
// Absent reads as no personal review layer.
func buildPersonalReview(raw any) (Partial, *core.RefusalError) {
	var zero Partial
	obj, ref := asObject(raw, "$.review")
	if ref != nil {
		return zero, ref
	}
	if _, misplaced := obj["budget_usd"]; misplaced {
		return zero, refuse(CodeBadConfig, "$.review.budget_usd: misplaced, the authorization limit lives under spend")
	}
	defaults, ref := buildDefaults(raw, "$.review")
	if ref != nil {
		return zero, ref
	}
	if defaults == nil {
		return zero, nil
	}
	return partialFromDefaults(defaults), nil
}

// buildPersonalSpend validates the spend constraint section. Absent
// reads as no spend constraint.
func buildPersonalSpend(raw any) (PersonalSpend, *core.RefusalError) {
	var zero PersonalSpend
	obj, ref := asObject(raw, spendPath)
	if ref != nil {
		return zero, ref
	}
	out := PersonalSpend{}
	var err *core.RefusalError
	if out.BudgetUSD, err = presentValue(obj, "budget_usd", spendPath, positiveFloat); err != nil {
		return zero, err
	}
	if out.RequireKnownEstimate, err = presentBool(obj, "require_known_estimate", spendPath); err != nil {
		return zero, err
	}
	if out.EnforceCap, err = presentBool(obj, "enforce_cap", spendPath); err != nil {
		return zero, err
	}
	return out, nil
}

// presentBool reads one optional boolean key: absent reads false.
func presentBool(obj map[string]any, key, path string) (bool, *core.RefusalError) {
	val, ref := presentValue(obj, key, path, asBool)
	if ref != nil || val == nil {
		return false, ref
	}
	return *val, nil
}

// BuiltinAgentNames lists the names a prefs-defined agent may not shadow.
// The run table is the authority; a test in that package keeps this list equal
// to it, so a new shipped row cannot become a silently shadowed operator name.
func BuiltinAgentNames() []string {
	return []string{
		"adal", "agy", "amp", "auggie", "autohand", "claude",
		"cline", "codearts", "codebuddy", "codex", "commandcode", "continue",
		"copilot", "cortex", "crush", "cursor", "dcode", "devin", "dexto",
		"droid", "forge", "fx", "gemini", "goose", "grok", "hermes", "jazz",
		"junie", "kilo", "kimchi", "kimi", "kiro", "kode", "mcode",
		"neovate", "openclaw", "opencode", "pa", "pi", "qoder", "qodercn",
		"qwen", "rovodev", "vibe", "zcode",
	}
}

// buildPersonalAgents validates the closed operator-agent shape. Names are
// read in sorted order so one malformed document always yields the same first
// diagnostic. Every failure names the agents key and the offending field.
func buildPersonalAgents(raw any) ([]PersonalAgent, *core.RefusalError) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, agentRefuse(agentsPath, wantObjectMsg, jsonKind(raw))
	}
	builtins := BuiltinAgentNames()
	out := make([]PersonalAgent, 0, len(obj))
	for _, name := range slices.Sorted(maps.Keys(obj)) {
		row, ref := buildAgentRow(name, obj[name], builtins)
		if ref != nil {
			return nil, ref
		}
		out = append(out, row)
	}
	return out, nil
}

// buildAgentRow validates one named row and fills its defaults.
func buildAgentRow(name string, raw any, builtins []string) (PersonalAgent, *core.RefusalError) {
	path := agentsPath + pathDot + name
	if !agentNamePattern.MatchString(name) {
		return PersonalAgent{}, agentRefuse(path, "want a name matching [a-z0-9][a-z0-9-]*")
	}
	if slices.Contains(builtins, name) {
		return PersonalAgent{}, agentRefuse(path, "collides with a built-in agent; pick another name")
	}
	entry, ok := raw.(map[string]any)
	if !ok {
		return PersonalAgent{}, agentRefuse(path, wantObjectMsg, jsonKind(raw))
	}
	binary, ref := agentBinary(entry, path)
	if ref != nil {
		return PersonalAgent{}, ref
	}
	args, ref := agentArgs(entry, path)
	if ref != nil {
		return PersonalAgent{}, ref
	}
	prompt, model, install, ref := agentOptionalFields(entry, path)
	if ref != nil {
		return PersonalAgent{}, ref
	}
	probeArgs, probeMatch, ref := agentProbeFields(entry, path)
	if ref != nil {
		return PersonalAgent{}, ref
	}
	return PersonalAgent{
		Name: name, Binary: binary, Args: args, PromptFlag: prompt,
		ModelFlag: model, Install: install, ProbeArgs: probeArgs,
		ProbeMatch: probeMatch,
	}, nil
}

func agentOptionalFields(entry map[string]any, path string) (prompt, model, install string, ref *core.RefusalError) {
	if prompt, ref = agentOptionalString(entry, "prompt_flag", path); ref != nil {
		return "", "", "", ref
	}
	if model, ref = agentOptionalString(entry, "model_flag", path); ref != nil {
		return "", "", "", ref
	}
	install, ref = agentOptionalString(entry, "install", path)
	return prompt, model, install, ref
}

// agentBinary requires a non-blank name on PATH or an absolute path, so a
// prefs file cannot name a relative executable the agent might resolve inside
// a reviewed repository.
func agentBinary(entry map[string]any, path string) (string, *core.RefusalError) {
	raw, ok := entry["binary"]
	if !ok {
		return "", agentRefuse(path+binaryField, "is required")
	}
	binary, ok := raw.(string)
	if !ok {
		return "", agentRefuse(path+binaryField, "want a string, got %s", jsonKind(raw))
	}
	if strings.TrimSpace(binary) == "" {
		return "", agentRefuse(path+binaryField, "want a non-blank string")
	}
	if strings.ContainsAny(binary, `/\`) && !filepath.IsAbs(binary) {
		return "", agentRefuse(path+binaryField, "must be absolute when it names a path")
	}
	return binary, nil
}

// agentArgs reads the optional argument list, defaulting to an empty one.
func agentArgs(entry map[string]any, path string) ([]string, *core.RefusalError) {
	return agentStringArray(entry, "args", path)
}

// agentProbeFields reads the optional identity check. A probe without a match
// cannot identify anything, so the pair is validated at the prefs boundary.
func agentProbeFields(entry map[string]any, path string) (args []string, match string, ref *core.RefusalError) {
	_, present := entry["probe_args"]
	if present {
		args, ref = agentStringArray(entry, "probe_args", path)
		if ref != nil {
			return nil, "", ref
		}
	}
	match, ref = agentOptionalString(entry, "probe_match", path)
	if ref != nil {
		return nil, "", ref
	}
	if present && match == "" {
		return nil, "", agentRefuse(path+".probe_match", "is required when probe_args is set")
	}
	return args, match, nil
}

// agentStringArray reads one optional array of strings, defaulting to empty.
func agentStringArray(entry map[string]any, key, path string) ([]string, *core.RefusalError) {
	raw, ok := entry[key]
	if !ok {
		return []string{}, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, agentRefuse(path+"."+key, "want an array of strings, got %s", jsonKind(raw))
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		arg, ok := item.(string)
		if !ok {
			return nil, agentRefuse(arrayPath(path+"."+key, i), "want a string, got %s", jsonKind(item))
		}
		out = append(out, arg)
	}
	return out, nil
}

// agentOptionalString reads one optional string field, defaulting to empty.
func agentOptionalString(entry map[string]any, key, path string) (string, *core.RefusalError) {
	raw, ok := entry[key]
	if !ok {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", agentRefuse(path+"."+key, "want a string, got %s", jsonKind(raw))
	}
	return value, nil
}

// buildPersonalPacks validates the explicit org pack directory list. An
// absent key reads as no org layer; there is no directory scan.
func buildPersonalPacks(raw any) ([]string, *core.RefusalError) {
	items, ref := asArray(raw, packsPath)
	if ref != nil {
		return nil, ref
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		path, ref := nonEmptyString(item, arrayPath(packsPath, i))
		if ref != nil {
			return nil, ref
		}
		out = append(out, path)
	}
	return out, nil
}

// buildPersonalPrivacy validates the privacy exclusion globs. Absent
// reads as no exclusions.
func buildPersonalPrivacy(raw any) (PersonalPrivacy, *core.RefusalError) {
	var zero PersonalPrivacy
	obj, ref := asObject(raw, "$.privacy")
	if ref != nil {
		return zero, ref
	}
	raw, ok := obj["exclusions"]
	if !ok {
		return PersonalPrivacy{}, nil
	}
	exclusions, ref := globArray(raw, "$.privacy.exclusions", 1)
	if ref != nil {
		return zero, ref
	}
	return PersonalPrivacy{Exclusions: exclusions}, nil
}

// LoadPersonal reads the operator settings file at path without creating
// it: absent reads as no personal layer. Parse refusals pass through.
func LoadPersonal(path string) (*PersonalSettings, *core.RefusalError) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &PersonalSettings{}, nil
	}
	if err != nil {
		return nil, refuse(CodeBadConfig, "could not read personal settings at %s", path)
	}
	return ParsePersonal(raw)
}

// personalSections are the settings sections this parser owns.
var personalSections = []string{
	personalSectionReview, personalSectionSpend, personalSectionPrivacy,
	personalSectionPacks, personalSectionAgents,
}

// ValidatePersonalSections checks the four typed personal sections of an
// already-decoded settings document: unknown fields inside them, a
// misplaced budget_usd, and semantic violations refuse with the typed
// configuration codes. Presentation sections and unknown top-level
// sections are the caller's concern. The parse owns the contract so the
// settings loader and the fetch-time personal read can never disagree.
func ValidatePersonalSections(doc map[string]any) *core.RefusalError {
	root := make(map[string]any, len(personalSections))
	for _, key := range personalSections {
		if raw, ok := doc[key]; ok {
			root[key] = raw
		}
	}
	if ref := checkPersonalSections(root); ref != nil {
		return ref
	}
	_, ref := buildPersonal(root)
	return ref
}

// credentialScanPersonal runs the credential scan over a personal document.
// Agent names are user-defined keys: their direct keys are exempt, but each
// definition still scans as a fixed-shape object.
func credentialScanPersonal(root map[string]any) *core.RefusalError {
	creds := make(map[string]bool, len(CredentialKeys))
	for _, k := range CredentialKeys {
		creds[k] = true
	}
	for _, key := range sortedObjectKeys(root) {
		if ref := scanPersonalCredentialKey(key, root[key], creds); ref != nil {
			return ref
		}
	}
	return nil
}

func scanPersonalCredentialKey(key string, value any, creds map[string]bool) *core.RefusalError {
	path := "$" + pathDot + key
	if creds[key] {
		return refuse(CodeBadCredentials, "%s: credential key %q is never stored in configuration", path, key)
	}
	if key == personalSectionAgents {
		return scanAgentCredentialDefinitions(value, path, creds)
	}
	return scanCredentialValue(value, path, creds, false)
}

func scanAgentCredentialDefinitions(raw any, path string, creds map[string]bool) *core.RefusalError {
	defs, ok := raw.(map[string]any)
	if !ok {
		return scanCredentialValue(raw, path, creds, false)
	}
	for _, name := range sortedObjectKeys(defs) {
		if ref := scanAgentDefinitionCredentials(name, defs[name], path, creds); ref != nil {
			return ref
		}
	}
	return nil
}

func scanAgentDefinitionCredentials(name string, raw any, path string, creds map[string]bool) *core.RefusalError {
	childPath := path + pathDot + name
	def, ok := raw.(map[string]any)
	if !ok {
		return scanCredentialValue(raw, childPath, creds, false)
	}
	return scanCredentialObject(def, childPath, creds, false)
}

// RegistryPath returns the derived discovery registry path.
func RegistryPath(home, xdg string) string {
	return filepath.Join(filepath.Dir(SettingsPath(home, xdg)), "projects.json")
}
