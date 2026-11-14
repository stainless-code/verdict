// Package recipe loads the declared review pipeline: one strict
// `.verdict/recipes.json` at the PR base, resolved by name into the four-stage
// plan a run records and renders. Verdict never dispatches the stages and never
// selects a model — every field here is a declaration, and the driver may honor
// its hints or not.
package recipe

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/strictjson"
)

// SchemaVersion is the only recipe-document format this build reads.
const SchemaVersion = "1"

// Refusal codes this package emits. Both are registered in core.RefusalCodes:
// the archtest guard fails in both directions when the set drifts.
const (
	// CodeBad refuses an invalid recipe document or entry.
	CodeBad = "bad-recipe"
	// CodeUnknown refuses a name no repository entry and no built-in answers.
	CodeUnknown = "unknown-recipe"
)

const (
	// maxDescription is the description cap, in characters.
	maxDescription = 280
	// maxVersion is the version cap, in characters: the directive renders the
	// version beside the recipe name, so it is bounded like the name is.
	maxVersion = 64
	// maxLenses is the lens-count cap, the same one a pack manifest may ship
	// (pack.MaxLenses): one recipe declares no more rows than one pack holds.
	maxLenses = 64
	// Model hints are at most 64 characters: enough for provider/model.
	maxModelLength = 64
	// the dispatch caps a lens row may declare, shared with the pack manifest.
	minLensFiles    = 1
	maxLensFiles    = 24
	minLensFindings = 1
	maxLensFindings = 20

	namePattern   = `^[a-z0-9][a-z0-9-]{0,63}$`
	lensIDPattern = `^[a-z0-9][a-z0-9-]{0,63}$`
	semverPattern = `^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`
	// modelPattern admits no whitespace, quote, or control character, so a hint
	// can never reshape the directive it is rendered into.
	modelPattern = `^[A-Za-z0-9][A-Za-z0-9._:/@+~-]{0,63}$`
)

var (
	nameRE   = regexp.MustCompile(namePattern)
	lensIDRE = regexp.MustCompile(lensIDPattern)
	semverRE = regexp.MustCompile(semverPattern)
	modelRE  = regexp.MustCompile(modelPattern)
)

// rootKeys, entryKeys, lensKeys, and synthesizeKeys are the closed key sets the
// published schema documents.
var (
	rootKeys       = []string{"schema_version", "recipes"}
	entryKeys      = []string{"version", "description", "budget_usd", "lenses", "synthesize"}
	lensKeys       = []string{"id", "model", "max_files", "max_findings"}
	synthesizeKeys = []string{"model"}
)

// Document is the published .verdict/recipes.json shape: named entries in one
// strict, hash-pinned control document. The map's keys are the recipe names, so
// the generator publishes their grammar as propertyNames.
type Document struct { //nolint:govet // field order is the published property order
	SchemaVersion string           `const:"1"      json:"schema_version"`
	Recipes       map[string]Entry `json:"recipes" propertyNames:"string"`
}

// Entry is one named pipeline declaration. BudgetUSD (absent reads as zero) and
// Description are recorded, never enforced; only Version, Synthesize, and the
// lens rows reach the directive.
type Entry struct { //nolint:govet // field order is the published property order
	Version     string     `json:"version"               maxLength:"64"`
	Description string     `json:"description,omitempty" maxLength:"280"`
	BudgetUSD   float64    `exclusiveMin:"0"             json:"budget_usd,omitempty"`
	Lenses      []Lens     `json:"lenses,omitempty"      maxItems:"64"               minItems:"1"`
	Synthesize  Synthesize `json:"synthesize"`
}

// Lens is one declared lens row: the id it selects, its model hint, and the
// per-lens dispatch caps.
type Lens struct {
	ID          string `json:"id"`
	Model       string `json:"model,omitempty"`
	MaxFiles    int    `json:"max_files,omitempty"    max:"24" min:"1"`
	MaxFindings int    `json:"max_findings,omitempty" max:"20" min:"1"`
}

// Synthesize declares the synthesizer model hint; the empty object is the
// "no hint" spelling.
type Synthesize struct {
	Model string `json:"model,omitempty"`
}

// Parse decodes and validates one recipes document. Every fault is one
// bad-recipe diagnostic naming the JSON path and the offending key.
func Parse(raw []byte) (*Document, *core.RefusalError) {
	tree, err := strictjson.Decode(raw)
	if err != nil {
		return nil, refuse(CodeBad, "%s", err)
	}
	root, ok := tree.(map[string]any)
	if !ok {
		return nil, refuse(CodeBad, "$: want an object, got %s", strictjson.Kind(tree))
	}
	if ref := checkKeys(root, "$", rootKeys); ref != nil {
		return nil, ref
	}
	doc := &Document{Recipes: map[string]Entry{}}
	if ref := doc.rootAt(root); ref != nil {
		return nil, ref
	}
	return doc, nil
}

// rootAt validates the format marker and every named entry.
func (d *Document) rootAt(root map[string]any) *core.RefusalError {
	version, ref := requiredString(root, "$", "schema_version")
	if ref != nil {
		return ref
	}
	if version != SchemaVersion {
		return refuse(CodeBad, "schema_version: want the string %q, got %q", SchemaVersion, version)
	}
	d.SchemaVersion = version
	recipes, ref := member(root, "$", "recipes")
	if ref != nil {
		return ref
	}
	named, ref := objectAt(recipes, "$.recipes")
	if ref != nil {
		return ref
	}
	for _, name := range slices.Sorted(maps.Keys(named)) {
		if ref := d.entryAt(name, named[name]); ref != nil {
			return ref
		}
	}
	return nil
}

// entryAt validates one named entry and records it: the required version, then
// the optional description, budget, and lens rows, then the required
// synthesizer hint, in published order.
func (d *Document) entryAt(name string, raw any) *core.RefusalError {
	if !nameRE.MatchString(name) {
		return refuse(CodeBad, "$.recipes: recipe key %q is not a lowercase slug", name)
	}
	path := "$.recipes." + name
	obj, ref := objectAt(raw, path)
	if ref != nil {
		return ref
	}
	if ref := checkKeys(obj, path, entryKeys); ref != nil {
		return ref
	}
	entry, ref := entryOf(obj, path)
	if ref != nil {
		return ref
	}
	d.Recipes[name] = entry
	return nil
}

// entryOf reads one entry: the required version, then each optional key the
// document spells, then the required synthesizer hint, in published order.
func entryOf(obj map[string]any, path string) (Entry, *core.RefusalError) {
	entry, ref := versionAt(obj, path)
	if ref != nil {
		return Entry{}, ref
	}
	if entry.Description, ref = descriptionAt(obj, path); ref != nil {
		return Entry{}, ref
	}
	if entry.BudgetUSD, ref = budgetAt(obj, path); ref != nil {
		return Entry{}, ref
	}
	if entry.Lenses, ref = lensesFieldAt(obj, path); ref != nil {
		return Entry{}, ref
	}
	synthesize, ref := synthesizeAt(obj, path)
	if ref != nil {
		return Entry{}, ref
	}
	entry.Synthesize = synthesize
	return entry, nil
}

// versionAt requires the entry's semver, capped so the version the directive
// renders beside the recipe name stays bounded.
func versionAt(obj map[string]any, path string) (Entry, *core.RefusalError) {
	version, ref := requiredString(obj, path, "version")
	if ref != nil {
		return Entry{}, ref
	}
	if n := utf8.RuneCountInString(version); n > maxVersion {
		return Entry{}, refuse(CodeBad, "%s.version: %d characters, over the %d-character cap",
			path, n, maxVersion)
	}
	if !semverRE.MatchString(version) {
		return Entry{}, refuse(CodeBad, "%s.version: %q is not semver", path, version)
	}
	return Entry{Version: version}, nil
}

// descriptionAt reads the human prose when the entry declares it: at most 280
// characters, and never rendered — only validated ids, enums, numbers, and
// hints reach a prompt.
func descriptionAt(obj map[string]any, path string) (string, *core.RefusalError) {
	raw, ok := obj["description"]
	if !ok {
		return "", nil
	}
	description, ref := stringAt(raw, path+".description")
	if ref != nil {
		return "", ref
	}
	if n := utf8.RuneCountInString(description); n > maxDescription {
		return "", refuse(CodeBad, "%s.description: %d characters, over the %d-character cap",
			path, n, maxDescription)
	}
	return description, nil
}

// budgetAt reads the declared amount when the entry declares one: a projection
// run records and the driver may honor, so zero and negative are not amounts.
func budgetAt(obj map[string]any, path string) (float64, *core.RefusalError) {
	raw, ok := obj["budget_usd"]
	if !ok {
		return 0, nil
	}
	num, ok := raw.(json.Number)
	if !ok {
		return 0, refuse(CodeBad, "%s.budget_usd: want a number, got %s", path, strictjson.Kind(raw))
	}
	amount, err := strconv.ParseFloat(num.String(), floatBits)
	if err != nil {
		return 0, refuse(CodeBad, "%s.budget_usd: want a number, got %s", path, num)
	}
	if amount <= 0 {
		return 0, refuse(CodeBad, "%s.budget_usd: want a number above 0, got %s", path, num)
	}
	return amount, nil
}

// lensesFieldAt reads the declared lens rows when the entry declares any.
func lensesFieldAt(obj map[string]any, path string) ([]Lens, *core.RefusalError) {
	raw, ok := obj["lenses"]
	if !ok {
		return nil, nil
	}
	return lensRowsAt(raw, path+".lenses")
}

// synthesizeAt validates the required synthesizer hint: one closed object with
// an optional model hint.
func synthesizeAt(obj map[string]any, path string) (Synthesize, *core.RefusalError) {
	raw, ref := member(obj, path, "synthesize")
	if ref != nil {
		return Synthesize{}, ref
	}
	nodePath := path + ".synthesize"
	node, ref := objectAt(raw, nodePath)
	if ref != nil {
		return Synthesize{}, ref
	}
	if ref := checkKeys(node, nodePath, synthesizeKeys); ref != nil {
		return Synthesize{}, ref
	}
	model, ref := modelAt(node, nodePath)
	if ref != nil {
		return Synthesize{}, ref
	}
	return Synthesize{Model: model}, nil
}

// lensRowsAt validates the declared rows: one to maxLenses, no repeated id, and
// every row's id, hint, and caps.
func lensRowsAt(raw any, path string) ([]Lens, *core.RefusalError) {
	items, ok := raw.([]any)
	if !ok {
		return nil, refuse(CodeBad, "%s: want an array, got %s", path, strictjson.Kind(raw))
	}
	if len(items) == 0 {
		return nil, refuse(CodeBad, "%s: want at least one lens", path)
	}
	if len(items) > maxLenses {
		return nil, refuse(CodeBad, "%s: %d lenses, over the %d-lens cap", path, len(items), maxLenses)
	}
	lenses := make([]Lens, 0, len(items))
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		lens, ref := lensAt(item, fmt.Sprintf("%s[%d]", path, i), seen)
		if ref != nil {
			return nil, ref
		}
		lenses = append(lenses, lens)
	}
	return lenses, nil
}

// lensAt validates one lens row: its id, its model hint, and its dispatch caps.
func lensAt(raw any, path string, seen map[string]bool) (Lens, *core.RefusalError) {
	obj, ref := objectAt(raw, path)
	if ref != nil {
		return Lens{}, ref
	}
	if ref := checkKeys(obj, path, lensKeys); ref != nil {
		return Lens{}, ref
	}
	id, ref := lensIDAt(obj, path, seen)
	if ref != nil {
		return Lens{}, ref
	}
	model, ref := modelAt(obj, path)
	if ref != nil {
		return Lens{}, ref
	}
	maxFiles, maxFindings, ref := lensCapsAt(obj, path)
	if ref != nil {
		return Lens{}, ref
	}
	return Lens{ID: id, Model: model, MaxFiles: maxFiles, MaxFindings: maxFindings}, nil
}

// lensIDAt requires the row's id — a lowercase slug of 1..64 characters,
// unique within the entry — and records it as taken.
func lensIDAt(obj map[string]any, path string, seen map[string]bool) (string, *core.RefusalError) {
	id, ref := requiredString(obj, path, "id")
	if ref != nil {
		return "", ref
	}
	if !lensIDRE.MatchString(id) {
		return "", refuse(CodeBad, "%s.id: %q is not a lowercase slug", path, id)
	}
	if seen[id] {
		return "", refuse(CodeBad, "%s.id: duplicate lens id %q", path, id)
	}
	seen[id] = true
	return id, nil
}

// lensCapsAt reads the row's two optional dispatch caps.
func lensCapsAt(obj map[string]any, path string) (int, int, *core.RefusalError) {
	maxFiles, ref := capAt(obj, path, "max_files", minLensFiles, maxLensFiles)
	if ref != nil {
		return 0, 0, ref
	}
	maxFindings, ref := capAt(obj, path, "max_findings", minLensFindings, maxLensFindings)
	if ref != nil {
		return 0, 0, ref
	}
	return maxFiles, maxFindings, nil
}

// modelAt reads the optional model hint: at most 64 characters from the
// published grammar, so no hint can reshape the directive.
func modelAt(obj map[string]any, path string) (string, *core.RefusalError) {
	raw, ok := obj["model"]
	if !ok {
		return "", nil
	}
	model, ref := stringAt(raw, path+".model")
	if ref != nil {
		return "", ref
	}
	if !modelRE.MatchString(model) {
		return "", refuse(CodeBad, "%s.model: %q is not a model hint (an allowlisted spelling of at most %d characters)",
			path, model, maxModelLength)
	}
	return model, nil
}

// capAt reads an optional dispatch cap, refusing a spelled value out of bounds.
func capAt(obj map[string]any, path, key string, lo, hi int) (int, *core.RefusalError) {
	raw, ok := obj[key]
	if !ok {
		return 0, nil
	}
	return intAt(raw, path+"."+key, lo, hi)
}

// intAt requires a JSON integer in the inclusive range. An integer is written
// as an integer literal — no fraction, no exponent — the same rule the config
// and pack readers follow, so the published schema and this parser accept one
// document: 24.0 and 1e2 refuse on both sides.
func intAt(raw any, path string, lo, hi int) (int, *core.RefusalError) {
	num, ok := raw.(json.Number)
	if !ok {
		return 0, refuse(CodeBad, "%s: want a number, got %s", path, strictjson.Kind(raw))
	}
	value, err := strconv.Atoi(num.String())
	if err != nil {
		return 0, refuse(CodeBad, "%s: want an integer, got %s", path, num)
	}
	if value < lo || value > hi {
		return 0, refuse(CodeBad, "%s: want an integer %d..%d, got %s", path, lo, hi, num)
	}
	return value, nil
}

// floatBits is the width ParseFloat reads: a declared amount is a float64.
const floatBits = 64

// checkKeys refuses any key outside the closed set, naming the first in sorted
// order so one document always yields one diagnostic.
func checkKeys(obj map[string]any, path string, want []string) *core.RefusalError {
	for _, key := range slices.Sorted(maps.Keys(obj)) {
		if !slices.Contains(want, key) {
			return refuse(CodeBad, "%s: unknown key %q", path, key)
		}
	}
	return nil
}

// member returns one required object value.
func member(obj map[string]any, path, key string) (any, *core.RefusalError) {
	raw, ok := obj[key]
	if !ok {
		return nil, refuse(CodeBad, "%s: missing %q", path, key)
	}
	return raw, nil
}

// requiredString returns one required string value.
func requiredString(obj map[string]any, path, key string) (string, *core.RefusalError) {
	raw, ref := member(obj, path, key)
	if ref != nil {
		return "", ref
	}
	return stringAt(raw, path+"."+key)
}

// objectAt requires a JSON object.
func objectAt(raw any, path string) (map[string]any, *core.RefusalError) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, refuse(CodeBad, "%s: want an object, got %s", path, strictjson.Kind(raw))
	}
	return obj, nil
}

// stringAt requires a JSON string. Emptiness is the schema's business, not
// this helper's: a recipe description may be empty because the published
// schema publishes no minLength for it.
func stringAt(raw any, path string) (string, *core.RefusalError) {
	text, ok := raw.(string)
	if !ok {
		return "", refuse(CodeBad, "%s: want a string, got %s", path, strictjson.Kind(raw))
	}
	return text, nil
}

// refuse seals one recipe diagnostic. The code comes first so the archtest
// refusal-code scan reads the same emission shape every package uses.
func refuse(code, format string, args ...any) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: fmt.Sprintf(format, args...)}
}
