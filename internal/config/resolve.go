package config

import (
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// Ordinary-default layers, highest precedence first. Flags beat
// environment, which beats repository scopes and shared defaults, which
// beat personal review defaults, which beat the built-ins. Personal spend
// and privacy are constraints, never defaults, and resolve separately.
const (
	LayerFlag     = "flag"
	LayerEnv      = "env"
	LayerScope    = "scope"
	LayerRepo     = "repo"
	LayerPersonal = "personal"
	LayerBuiltIn  = "built-in"
)

// Built-in defaults: the floor every resolution starts from.
const (
	BuiltinMaxBytes    = 512000
	BuiltinKBMinJudged = 5
	BuiltinKBMaxFPRate = 0.5
	BuiltinEffort      = "standard"
	BuiltinDepth       = "standard"
)

// Check IDs owned by the requirements contract.
const (
	CheckNoOpenAboveBlock = "no-open-above-block"
	CheckHeadCurrent      = "head-current"
	CheckNoLiveThreads    = "no-live-threads"
)

// PendingStatus marks fetch-time requirement status: declared and planned,
// never satisfied before its evidence exists.
const PendingStatus = "pending"

// keyBudgetUSD names the estimate-guard threshold across layers, origins,
// and evidence.
const keyBudgetUSD = "budget_usd"

// Default value keys shared by the field inventory, the evidence capture,
// and the explain surface, so the three spell each key once.
const (
	keyPanel        = "panel"
	keyMaxBytes     = "max_bytes"
	keyChunk        = "chunk"
	keyEffort       = "effort"
	keyDisplayFloor = "display_min_severity"
	keyPostFloor    = "post_min_severity"
	keyKBPolicy     = "kb_policy"
	keyKBMinJudged  = "kb_policy.min_judged"
	keyKBMaxFPRate  = "kb_policy.max_fp_rate"
)

// Partial is one layer's contribution to the ordinary defaults. Each field
// carries its own presence bit so nested objects merge per field and
// arrays replace: a set-but-empty panel replaces, an unset one inherits.
type Partial struct {
	BudgetUSD          *float64
	BudgetDisplay      string
	MaxBytes           *int64
	Chunk              *bool
	KBMinJudged        *int
	KBMaxFPRate        *float64
	Effort             *string
	DisplayMinSeverity *string
	PostMinSeverity    *string
	Panel              []string
	PanelSet           bool
	KBPolicySet        bool
}

// partialFromDefaults converts a validated defaults object into a set
// layer: every present key is set, absent keys inherit.
func partialFromDefaults(defaults *core.ConfigDefaults) Partial {
	var out Partial
	if defaults == nil {
		return out
	}
	if defaults.Panel != nil {
		out.Panel, out.PanelSet = defaults.Panel, true
	}
	out.BudgetUSD = defaults.BudgetUSD
	if defaults.BudgetUSD != nil {
		out.BudgetDisplay = strconv.FormatFloat(*defaults.BudgetUSD, 'f', -1, floatBits)
	}
	out.MaxBytes = defaults.MaxBytes
	out.Chunk = defaults.Chunk
	if defaults.KBPolicy != nil {
		out.KBPolicySet = true
		out.KBMinJudged = defaults.KBPolicy.MinJudged
		out.KBMaxFPRate = defaults.KBPolicy.MaxFPRate
	}
	out.Effort = defaults.Effort
	out.DisplayMinSeverity = defaults.DisplayMinSeverity
	out.PostMinSeverity = defaults.PostMinSeverity
	return out
}

// Builtins returns the built-in floor as the lowest-precedence layer.
func Builtins() Partial {
	maxBytes := int64(BuiltinMaxBytes)
	chunk := false
	minJudged := BuiltinKBMinJudged
	maxFPRate := BuiltinKBMaxFPRate
	effort := BuiltinEffort
	return Partial{
		MaxBytes: &maxBytes, Chunk: &chunk,
		KBMinJudged: &minJudged, KBMaxFPRate: &maxFPRate, KBPolicySet: true,
		Effort: &effort,
	}
}

// FieldOrigin records the winning layer and the exact source that
// supplied one effective field.
type FieldOrigin struct {
	Layer  string `json:"layer"`
	Source string `json:"source"`
}

// ShadowedValue records one losing layer value: every shadowed value is
// reported with its layer and source, so an override never hides silently.
type ShadowedValue struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Layer  string `json:"layer"`
	Source string `json:"source"`
}

// EffectiveDefaults is the merged ordinary defaults for one file.
type EffectiveDefaults struct {
	BudgetUSD          *float64 `json:"budget_usd,omitempty"`
	BudgetDisplay      string   `json:"-"`
	DisplayMinSeverity *string  `json:"display_min_severity,omitempty"`
	PostMinSeverity    *string  `json:"post_min_severity,omitempty"`
	Effort             string   `json:"effort"`
	Panel              []string `json:"panel,omitempty"`
	MaxBytes           int64    `json:"max_bytes"`
	KBMinJudged        int      `json:"kb_min_judged"`
	KBMaxFPRate        float64  `json:"kb_max_fp_rate"`
	Chunk              bool     `json:"chunk"`
}

// ResolvedDefaults carries the effective values with per-field origins
// and the shadowed values they overrode.
type ResolvedDefaults struct {
	Origins  map[string]FieldOrigin `json:"origins"`
	Shadowed []ShadowedValue        `json:"shadowed"`
	Values   EffectiveDefaults      `json:"values"`
}

// firstBudgetDisplay resolves the winning layer's budget literal for
// human lines: text sources keep their spelling ($1.00 stays $1.00),
// JSON numbers normalize (no literal survives decoding).
func firstBudgetDisplay(layers []layerValue) string {
	for i := range layers {
		if display := layers[i].partial.BudgetDisplay; layers[i].partial.BudgetUSD != nil && display != "" {
			return display
		}
	}
	return ""
}

// layerValue pairs one partial layer with its origin.
type layerValue struct {
	layer   string
	source  string
	partial Partial
}

// mergePartials resolves one defaults set over explicit layers, highest
// precedence first. The first set field wins; every other set value for
// the same field is recorded as shadowed in layer order.
func mergePartials(layers []layerValue) ResolvedDefaults {
	out := ResolvedDefaults{Origins: map[string]FieldOrigin{}}
	out.Values.Panel, out.Origins["panel"], out.Shadowed = pickStrings(layers, "panel",
		func(p Partial) ([]string, bool) { return p.Panel, p.PanelSet })
	out.Values.BudgetUSD, out.Origins[keyBudgetUSD], out.Shadowed = pickPointer(layers, keyBudgetUSD, out.Shadowed,
		func(p Partial) (*float64, bool) { return p.BudgetUSD, p.BudgetUSD != nil })
	out.Values.BudgetDisplay = firstBudgetDisplay(layers)
	out.Values.MaxBytes, out.Origins["max_bytes"], out.Shadowed = pickInt64(layers, "max_bytes", out.Shadowed,
		func(p Partial) (int64, bool) { return derefInt64(p.MaxBytes) })
	out.Values.Chunk, out.Origins["chunk"], out.Shadowed = pickBool(layers, "chunk", out.Shadowed,
		func(p Partial) (bool, bool) { return derefBool(p.Chunk) })
	out.Values.KBMinJudged, out.Origins["kb_policy.min_judged"], out.Shadowed = pickInt(layers, "kb_policy.min_judged", out.Shadowed,
		func(p Partial) (int, bool) {
			if !p.KBPolicySet || p.KBMinJudged == nil {
				return 0, false
			}
			return *p.KBMinJudged, true
		})
	out.Values.KBMaxFPRate, out.Origins["kb_policy.max_fp_rate"], out.Shadowed = pickFloat64(layers, "kb_policy.max_fp_rate", out.Shadowed,
		func(p Partial) (float64, bool) {
			if !p.KBPolicySet || p.KBMaxFPRate == nil {
				return 0, false
			}
			return *p.KBMaxFPRate, true
		})
	out.Values.Effort, out.Origins["effort"], out.Shadowed = pickString(layers, "effort", out.Shadowed,
		func(p Partial) (string, bool) { return derefString(p.Effort) })
	out.Values.DisplayMinSeverity, out.Origins["display_min_severity"], out.Shadowed = pickPointer(layers,
		"display_min_severity", out.Shadowed,
		func(p Partial) (*string, bool) { return p.DisplayMinSeverity, p.DisplayMinSeverity != nil })
	out.Values.PostMinSeverity, out.Origins["post_min_severity"], out.Shadowed = pickPointer(layers, "post_min_severity", out.Shadowed,
		func(p Partial) (*string, bool) { return p.PostMinSeverity, p.PostMinSeverity != nil })
	return out
}

// derefInt64 lifts an optional int64 into a set pair.
func derefInt64(v *int64) (int64, bool) {
	if v == nil {
		return 0, false
	}
	return *v, true
}

// derefBool lifts an optional boolean into a set pair.
func derefBool(v *bool) (bool, bool) {
	if v == nil {
		return false, false
	}
	return *v, true
}

// derefString lifts an optional string into a set pair.
func derefString(v *string) (string, bool) {
	if v == nil {
		return "", false
	}
	return *v, true
}

// Layer field getters lift one optional layer value into a set pair.
type (
	boolField   = func(Partial) (bool, bool)
	stringField = func(Partial) (string, bool)
	intField    = func(Partial) (int, bool)
	int64Field  = func(Partial) (int64, bool)
	floatField  = func(Partial) (float64, bool)
)

// ptrField lifts one optional pointer layer value into a set pair.
type ptrField[T any] = func(Partial) (*T, bool)

// pickStrings resolves one string-slice field: the first set layer wins
// and replaces; the rest shadow.
func pickStrings(layers []layerValue, key string, get func(Partial) ([]string, bool)) ([]string, FieldOrigin, []ShadowedValue) {
	var shadowed []ShadowedValue
	winner := FieldOrigin{Layer: LayerBuiltIn, Source: LayerBuiltIn}
	var value []string
	set := false
	for i := range layers {
		layer := &layers[i]
		got, ok := get(layer.partial)
		if !ok {
			continue
		}
		if !set {
			value, winner, set = slices.Clone(got), FieldOrigin{Layer: layer.layer, Source: layer.source}, true
			continue
		}
		shadowed = append(shadowed, ShadowedValue{Key: key, Value: slices.Clone(got), Layer: layer.layer, Source: layer.source})
	}
	return value, winner, shadowed
}

// pickPointer resolves one optional-pointer field.
func pickPointer[T any](layers []layerValue, key string, shadowed []ShadowedValue, get ptrField[T]) (*T, FieldOrigin, []ShadowedValue) {
	winner := FieldOrigin{Layer: LayerBuiltIn, Source: LayerBuiltIn}
	var value *T
	for i := range layers {
		layer := &layers[i]
		got, set := get(layer.partial)
		if !set {
			continue
		}
		if value == nil {
			value, winner = got, FieldOrigin{Layer: layer.layer, Source: layer.source}
			continue
		}
		shadowed = append(shadowed, ShadowedValue{Key: key, Value: *got, Layer: layer.layer, Source: layer.source})
	}
	return value, winner, shadowed
}

// pickInt resolves one int field; the built-in floor guarantees a winner,
// so the origin never stays zero-valued by accident.
func pickInt(layers []layerValue, key string, shadowed []ShadowedValue, get intField) (int, FieldOrigin, []ShadowedValue) {
	winner := FieldOrigin{Layer: LayerBuiltIn, Source: LayerBuiltIn}
	var value int
	set := false
	for i := range layers {
		layer := &layers[i]
		got, ok := get(layer.partial)
		if !ok {
			continue
		}
		if !set {
			value, winner, set = got, FieldOrigin{Layer: layer.layer, Source: layer.source}, true
			continue
		}
		shadowed = append(shadowed, ShadowedValue{Key: key, Value: got, Layer: layer.layer, Source: layer.source})
	}
	return value, winner, shadowed
}

// pickInt64 resolves one int64 field; the built-in floor guarantees a
// winner, so the origin never stays zero-valued by accident.
func pickInt64(layers []layerValue, key string, shadowed []ShadowedValue, get int64Field) (int64, FieldOrigin, []ShadowedValue) {
	winner := FieldOrigin{Layer: LayerBuiltIn, Source: LayerBuiltIn}
	var value int64
	set := false
	for i := range layers {
		layer := &layers[i]
		got, ok := get(layer.partial)
		if !ok {
			continue
		}
		if !set {
			value, winner, set = got, FieldOrigin{Layer: layer.layer, Source: layer.source}, true
			continue
		}
		shadowed = append(shadowed, ShadowedValue{Key: key, Value: got, Layer: layer.layer, Source: layer.source})
	}
	return value, winner, shadowed
}

// pickFloat64 resolves one float64 field.
func pickFloat64(layers []layerValue, key string, shadowed []ShadowedValue, get floatField) (float64, FieldOrigin, []ShadowedValue) {
	winner := FieldOrigin{Layer: LayerBuiltIn, Source: LayerBuiltIn}
	var value float64
	set := false
	for i := range layers {
		layer := &layers[i]
		got, ok := get(layer.partial)
		if !ok {
			continue
		}
		if !set {
			value, winner, set = got, FieldOrigin{Layer: layer.layer, Source: layer.source}, true
			continue
		}
		shadowed = append(shadowed, ShadowedValue{Key: key, Value: got, Layer: layer.layer, Source: layer.source})
	}
	return value, winner, shadowed
}

// pickBool resolves one boolean field.
func pickBool(layers []layerValue, key string, shadowed []ShadowedValue, get boolField) (bool, FieldOrigin, []ShadowedValue) {
	winner := FieldOrigin{Layer: LayerBuiltIn, Source: LayerBuiltIn}
	var value bool
	set := false
	for i := range layers {
		layer := &layers[i]
		got, ok := get(layer.partial)
		if !ok {
			continue
		}
		if !set {
			value, winner, set = got, FieldOrigin{Layer: layer.layer, Source: layer.source}, true
			continue
		}
		shadowed = append(shadowed, ShadowedValue{Key: key, Value: got, Layer: layer.layer, Source: layer.source})
	}
	return value, winner, shadowed
}

// pickString resolves one string field.
func pickString(layers []layerValue, key string, shadowed []ShadowedValue, get stringField) (string, FieldOrigin, []ShadowedValue) {
	winner := FieldOrigin{Layer: LayerBuiltIn, Source: LayerBuiltIn}
	var value string
	set := false
	for i := range layers {
		layer := &layers[i]
		got, ok := get(layer.partial)
		if !ok {
			continue
		}
		if !set {
			value, winner, set = got, FieldOrigin{Layer: layer.layer, Source: layer.source}, true
			continue
		}
		shadowed = append(shadowed, ShadowedValue{Key: key, Value: got, Layer: layer.layer, Source: layer.source})
	}
	return value, winner, shadowed
}

// ParseEnv reads the consumed environment names and values into a partial
// layer. Only the five contract names are read, never the whole
// environment. Malformed budget and byte-cap values refuse with the shipped
// codes; malformed KB values warn and fall through, matching the shipped
// lenient KB behavior the later fetch consumer keeps.
func ParseEnv(env map[string]string) (Partial, []string, *core.RefusalError) {
	var out Partial
	var warnings []string
	panelEnv(env, &out)
	if ref := budgetEnv(env, &out); ref != nil {
		return Partial{}, nil, ref
	}
	if ref := bytesEnv(env, &out); ref != nil {
		return Partial{}, nil, ref
	}
	warnings = append(warnings, kbEnvs(env, &out)...)
	return out, warnings, nil
}

// envTrim reads one consumed variable, trimming space; blank reads unset.
func envTrim(env map[string]string, name string) (string, bool) {
	raw, ok := env[name]
	if !ok || strings.TrimSpace(raw) == "" {
		return "", false
	}
	return strings.TrimSpace(raw), true
}

func panelEnv(env map[string]string, out *Partial) {
	raw, ok := envTrim(env, "REVIEW_PANEL")
	if !ok {
		return
	}
	out.Panel, out.PanelSet = splitList(raw), true
}

// budgetEnv reads the estimate-guard threshold, refusing malformed input
// with the shipped code.
func budgetEnv(env map[string]string, out *Partial) *core.RefusalError {
	raw, ok := envTrim(env, "REVIEW_BUDGET_USD")
	if !ok {
		return nil
	}
	budget, ref := parseBudgetEnv(raw)
	if ref != nil {
		return ref
	}
	out.BudgetUSD, out.BudgetDisplay = &budget, raw
	return nil
}

// bytesEnv reads the diff byte cap, refusing malformed input with the
// shipped code.
func bytesEnv(env map[string]string, out *Partial) *core.RefusalError {
	raw, ok := envTrim(env, "REVIEW_MAX_BYTES")
	if !ok {
		return nil
	}
	maxBytes, ref := parseBytesEnv(raw)
	if ref != nil {
		return ref
	}
	out.MaxBytes = &maxBytes
	return nil
}

// kbEnvs reads the lenient knowledge-base overrides: malformed values
// warn and fall through, matching the shipped behavior the fetch consumer
// keeps.
func kbEnvs(env map[string]string, out *Partial) []string {
	var warnings []string
	if raw, ok := envTrim(env, "REVIEW_KB_MIN_JUDGED"); ok {
		warnings = append(warnings, kbMinJudgedEnv(raw, out)...)
	}
	if raw, ok := envTrim(env, "REVIEW_KB_MAX_FP_RATE"); ok {
		warnings = append(warnings, kbMaxFPRateEnv(raw, out)...)
	}
	return warnings
}

// kbMinJudgedEnv reads the retirement quorum override, warning past
// malformed values instead of refusing.
func kbMinJudgedEnv(raw string, out *Partial) []string {
	minJudged, err := strconv.Atoi(raw)
	if err != nil || minJudged < 1 {
		return []string{"ignoring invalid REVIEW_KB_MIN_JUDGED '" + raw + "' (want a positive integer)"}
	}
	out.KBMinJudged, out.KBPolicySet = &minJudged, true
	return nil
}

// kbMaxFPRateEnv reads the retirement ceiling override, warning past
// malformed values instead of refusing.
func kbMaxFPRateEnv(raw string, out *Partial) []string {
	rate, err := strconv.ParseFloat(raw, floatBits)
	if err != nil || rate < 0 || rate > 1 {
		return []string{"ignoring invalid REVIEW_KB_MAX_FP_RATE '" + raw + "' (want 0..1)"}
	}
	out.KBMaxFPRate, out.KBPolicySet = &rate, true
	return nil
}

// parseBudgetEnv validates a plain USD number above zero.
func parseBudgetEnv(raw string) (float64, *core.RefusalError) {
	if !isPlainNumber(raw) {
		return 0, refuse("bad-budget",
			"budget '%s' from REVIEW_BUDGET_USD is not a plain USD number (want e.g. REVIEW_BUDGET_USD=0.50)", raw)
	}
	budget, err := strconv.ParseFloat(raw, floatBits)
	if err != nil || budget <= 0 {
		return 0, refuse("bad-budget", "budget '%s' from REVIEW_BUDGET_USD is not above 0", raw)
	}
	return budget, nil
}

// parseBytesEnv validates a positive byte count.
func parseBytesEnv(raw string) (int64, *core.RefusalError) {
	maxBytes, err := strconv.ParseInt(raw, decimalBase, floatBits)
	if err != nil || maxBytes < 1 {
		return 0, refuse("bad-max-bytes", "cap '%s' from REVIEW_MAX_BYTES is not a positive byte count", raw)
	}
	return maxBytes, nil
}

// isPlainNumber matches the shipped budget literal: digits with one
// optional dot fraction, no exponent, no sign, no surrounding space.
func isPlainNumber(raw string) bool {
	intPart, fracPart, hasDot := strings.Cut(raw, ".")
	if hasDot && strings.Contains(fracPart, ".") {
		return false
	}
	if !isDigits(intPart) {
		return false
	}
	return !hasDot || isDigits(fracPart)
}

// isDigits reports whether s is one or more ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// splitList splits on commas and whitespace, dropping blanks.
func splitList(raw string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(c rune) bool { return c == ',' || c == ' ' }) {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// KBPolicy renders the effective retirement thresholds: the judged quorum
// and the false-positive-rate ceiling in the store's display spelling.
func (d *EffectiveDefaults) KBPolicy() (int, string) {
	return d.KBMinJudged, strconv.FormatFloat(d.KBMaxFPRate, 'g', -1, floatBits)
}

// EnvDefaults resolves the consumed environment names over the built-in
// floor. It serves verbs that read a store without a typed run bundle:
// the same environment keys, the same bounds, no repository layer. A
// malformed value falls back to the built-in and reports why through
// warn, matching the resolver's typed refusal at fetch time.
func EnvDefaults(env map[string]string, warn func(string)) EffectiveDefaults {
	partial, warns, ref := ParseEnv(env)
	for _, w := range warns {
		warn(w)
	}
	if ref != nil {
		warn(ref.Message)
		partial = Partial{}
	}
	out := mergePartials([]layerValue{
		{partial: partial, layer: LayerEnv, source: "environment"},
		{partial: Builtins(), layer: LayerBuiltIn, source: LayerBuiltIn},
	})
	return out.Values
}
