package config

import (
	"errors"
	"os"

	"github.com/stainless-code/verdict/internal/core"
)

// Personal settings keep the existing file path (the operator settings
// file) and gain three optional sections. This foundation slice models and
// parses those sections and wires them into the resolver; the CLI prefs
// layer keeps its existing behavior and adopts these sections in a later
// consumer slice.

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

// PersonalSettings is the parsed personal layer: review defaults (every
// defaults key except budget_usd, which lives in spend), the spend
// constraint, and the privacy constraint.
type PersonalSettings struct {
	Spend   PersonalSpend
	Privacy PersonalPrivacy
	Review  Partial
}

// spendPath addresses the personal spend constraint section.
const spendPath = "$.spend"

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
	return nil
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
var personalSections = []string{personalSectionReview, personalSectionSpend, personalSectionPrivacy}

// ValidatePersonalSections checks the three typed personal sections of
// an already-decoded settings document: unknown fields inside them, a
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

// credentialScanPersonal runs the credential scan over a personal
// document with no exemptions: settings carry no user-defined map keys.
func credentialScanPersonal(root map[string]any) *core.RefusalError {
	creds := make(map[string]bool, len(CredentialKeys))
	for _, k := range CredentialKeys {
		creds[k] = true
	}
	return scanCredentialObject(root, "$", creds, false)
}
