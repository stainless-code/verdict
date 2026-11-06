package config

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
)

// EvidenceValue is one captured effective value with its origin.
type EvidenceValue struct {
	Key    string      `json:"key"`
	Value  any         `json:"value"`
	Origin FieldOrigin `json:"origin"`
}

// EvidenceRequirement is one captured requirement with its fetch-time
// satisfaction status and contributing sources.
type EvidenceRequirement struct {
	Key       string   `json:"key"`
	Threshold *string  `json:"threshold,omitempty"`
	Status    string   `json:"status"`
	Sources   []string `json:"sources"`
}

// Evidence is the fetch-time capture: effective values, origins, shadowed
// values, applicable clauses with pending status, the selected plan, the
// base commit identity, and the configuration hash. The allowlist is
// closed: effective values, origins, consumed input names (never whole
// environment dumps), base identity, labeled hashes, scopes, constraints,
// and the plan. Credentials are never captured. Later verbs append
// outcomes by reference; stage flags and consents stay in their own
// invocation records and never rewrite captured inputs.
//
// PatchSHA256 hashes the raw host diff the patch identity covers, while
// FilteredPatchSHA256 hashes the same diff with privacy-excluded files
// excised — the model-facing review input. The two labeled hashes prove
// the filter mapping without carrying excluded content.
type Evidence struct {
	Plan                Plan                  `json:"plan"`
	SchemaVersion       string                `json:"schema_version"`
	CapturedAt          string                `json:"captured_at"`
	BaseSHA             string                `json:"base_sha"`
	ConfigSHA256        string                `json:"config_sha256"`
	PatchSHA256         string                `json:"patch_sha256,omitempty"`
	FilteredPatchSHA256 string                `json:"filtered_patch_sha256,omitempty"`
	ConsumedInputs      []string              `json:"consumed_inputs"`
	Values              []EvidenceValue       `json:"values"`
	Shadowed            []ShadowedValue       `json:"shadowed"`
	Requirements        []EvidenceRequirement `json:"requirements"`
}

// Capture builds the evidence record for one resolution: configRaw is the
// exact trusted-base bytes (nil without base policy); capturedAt is the
// fetch-time timestamp the caller formats.
func Capture(in *Inputs, res *Resolved, configRaw []byte, capturedAt string) Evidence {
	out := Evidence{
		SchemaVersion: "1",
		CapturedAt:    capturedAt,
		BaseSHA:       in.BaseSHA,
		Plan:          res.Plan,
		Shadowed:      slices.Clone(res.Defaults.Shadowed),
	}
	if len(configRaw) > 0 {
		sum := sha256.Sum256(configRaw)
		out.ConfigSHA256 = hex.EncodeToString(sum[:])
	}
	out.Values = evidenceValues(&res.Defaults)
	out.ConsumedInputs = consumedInputs(in, res)
	out.Requirements = evidenceRequirements(in, res)
	return out
}

// evidenceValues flattens the global effective defaults into captured
// values with origins.
func evidenceValues(defaults *ResolvedDefaults) []EvidenceValue {
	values := defaults.Values
	origins := defaults.Origins
	get := func(key string) FieldOrigin { return origins[key] }
	return []EvidenceValue{
		{Key: keyPanel, Value: slices.Clone(values.Panel), Origin: get(keyPanel)},
		{Key: keyBudgetUSD, Value: values.BudgetUSD, Origin: get(keyBudgetUSD)},
		{Key: keyMaxBytes, Value: values.MaxBytes, Origin: get(keyMaxBytes)},
		{Key: keyChunk, Value: values.Chunk, Origin: get(keyChunk)},
		{Key: keyKBMinJudged, Value: values.KBMinJudged, Origin: get(keyKBMinJudged)},
		{Key: keyKBMaxFPRate, Value: values.KBMaxFPRate, Origin: get(keyKBMaxFPRate)},
		{Key: keyEffort, Value: values.Effort, Origin: get(keyEffort)},
		{Key: keyDisplayFloor, Value: values.DisplayMinSeverity, Origin: get(keyDisplayFloor)},
		{Key: keyPostFloor, Value: values.PostMinSeverity, Origin: get(keyPostFloor)},
	}
}

// consumedInputs derives the allowlisted input names from the resolution
// origins: the exact sources that supplied values, plus the tracked
// configuration path when a base policy exists and the operator settings
// path when one was read. Whole-environment dumps never appear.
func consumedInputs(in *Inputs, res *Resolved) []string {
	seen := map[string]bool{}
	var out []string
	note := func(name string) {
		if name == "" || name == LayerBuiltIn || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	noteOriginSources(res.Defaults.Origins, note)
	for i := range res.Files {
		noteOriginSources(res.Files[i].Defaults.Origins, note)
	}
	if in.Repo != nil {
		note(configSourceName)
	}
	if in.SettingsPath != "" && in.Personal != nil {
		note("operator settings")
	}
	slices.Sort(out)
	return out
}

// noteOriginSources records every non-built-in source in one origin map.
func noteOriginSources(origins map[string]FieldOrigin, note func(string)) {
	for _, origin := range origins {
		note(origin.Source)
	}
}

// evidenceRequirements merges every file's requirement set with the root
// set behind it: clauses keyed by name with merged sources, checks keyed
// by id, and the bind requirement when any applicable source sets it.
// Every entry reports pending status: nothing is satisfied before its
// evidence exists.
func evidenceRequirements(in *Inputs, res *Resolved) []EvidenceRequirement {
	merger := newRequirementMerger()
	root := rootRequirements(requirementsOf(in.Repo), in.BaseSHA)
	merger.merge(&root)
	for i := range res.Files {
		merger.merge(&res.Files[i].Requirements)
	}
	return merger.ordered()
}

// requirementMerger accumulates clauses, checks, and bind sources across
// the root and file sets, keying each entry for stable output.
type requirementMerger struct {
	clauses map[string]*EvidenceRequirement
	checks  map[string]*EvidenceRequirement
	bind    *EvidenceRequirement
	order   []string
}

func newRequirementMerger() *requirementMerger {
	return &requirementMerger{clauses: map[string]*EvidenceRequirement{}, checks: map[string]*EvidenceRequirement{}}
}

func (m *requirementMerger) merge(set *RequirementSet) {
	for i := range set.Clauses {
		m.mergeClause(&set.Clauses[i])
	}
	for _, check := range set.Checks {
		m.mergeCheck(check, set.CheckOrigin[check])
	}
	if set.RequireBind {
		m.mergeBind(set.BindSources)
	}
}

// mergeClause keys one clause by name, merging sources on repeats.
func (m *requirementMerger) mergeClause(clause *ClauseProvenance) {
	key := "block_clauses/" + clause.Clause.Name
	entry, ok := m.clauses[key]
	if !ok {
		threshold := clause.Clause.Threshold
		entry = &EvidenceRequirement{Key: key, Threshold: &threshold, Status: PendingStatus}
		m.clauses[key] = entry
		m.order = append(m.order, key)
	}
	entry.Sources = unionOrdered(entry.Sources, clause.Sources)
}

// mergeCheck keys one required check by id, merging sources on repeats.
func (m *requirementMerger) mergeCheck(check string, sources []string) {
	key := "require_checks/" + check
	entry, ok := m.checks[key]
	if !ok {
		entry = &EvidenceRequirement{Key: key, Status: PendingStatus}
		m.checks[key] = entry
		m.order = append(m.order, key)
	}
	entry.Sources = unionOrdered(entry.Sources, sources)
}

// mergeBind records the bind requirement once, merging sources.
func (m *requirementMerger) mergeBind(sources []string) {
	if m.bind == nil {
		m.bind = &EvidenceRequirement{Key: "require_bind", Status: PendingStatus}
		m.order = append(m.order, "require_bind")
	}
	m.bind.Sources = unionOrdered(m.bind.Sources, sources)
}

// ordered renders the merged entries in stable key order.
func (m *requirementMerger) ordered() []EvidenceRequirement {
	slices.Sort(m.order)
	out := make([]EvidenceRequirement, 0, len(m.order))
	for _, key := range m.order {
		switch {
		case m.clauses[key] != nil:
			out = append(out, *m.clauses[key])
		case m.checks[key] != nil:
			out = append(out, *m.checks[key])
		default:
			out = append(out, *m.bind)
		}
	}
	return out
}
