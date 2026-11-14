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
	// Model hints are at most 64 characters: enough for provider/model.
	maxModelLength = 64
	// the dispatch caps a lens row may declare, shared with the pack manifest.
	minLensFiles    = 1
	maxLensFiles    = 24
	minLensFindings = 1
	maxLensFindings = 20

	namePattern   = `^[a-z0-9][a-z0-9-]{0,63}$`
	lensIDPattern = `^[a-z0-9][a-z0-9-]*$`
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
// strict, hash-pinned control document.
type Document struct { //nolint:govet // field order is the published property order
	SchemaVersion string           `const:"1"      json:"schema_version"`
	Recipes       map[string]Entry `json:"recipes"`
}

// Entry is one named pipeline declaration. BudgetUSD (absent reads as zero) and
// Description are recorded, never enforced; only Version, Synthesize, and the
// lens rows reach the directive.
type Entry struct { //nolint:govet // field order is the published property order
	Version     string     `json:"version"`
	Description string     `json:"description,omitempty" maxLength:"280"`
	BudgetUSD   float64    `exclusiveMin:"0"             json:"budget_usd,omitempty"`
	Lenses      []Lens     `json:"lenses,omitempty"      minItems:"1"`
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

// entryAt validates one named entry and records it.
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
	version, ref := requiredString(obj, path, "version")
	if ref != nil {
		return ref
	}
	if !semverRE.MatchString(version) {
		return refuse(CodeBad, "%s.version: %q is not semver", path, version)
	}
	entry := Entry{Version: version}
	if ref := entry.optionalAt(obj, path); ref != nil {
		return ref
	}
	d.Recipes[name] = entry
	return nil
}

// optionalAt validates the keys an entry may omit, then the one it may not.
//
//nolint:revive // one linear pass over the optional keys, in published order
func (e *Entry) optionalAt(obj map[string]any, path string) *core.RefusalError {
	if raw, ok := obj["description"]; ok {
		if ref := e.descriptionAt(raw, path); ref != nil {
			return ref
		}
	}
	if raw, ok := obj["budget_usd"]; ok {
		if ref := e.budgetAt(raw, path); ref != nil {
			return ref
		}
	}
	if raw, ok := obj["lenses"]; ok {
		if ref := e.lensesFieldAt(raw, path); ref != nil {
			return ref
		}
	}
	return e.synthesizeAt(obj, path)
}

// descriptionAt validates the human prose: at most 280 characters, and never
// rendered — only validated ids, enums, numbers, and hints reach a prompt.
func (e *Entry) descriptionAt(raw any, path string) *core.RefusalError {
	description, ref := stringAt(raw, path+".description")
	if ref != nil {
		return ref
	}
	if n := utf8.RuneCountInString(description); n > maxDescription {
		return refuse(CodeBad, "%s.description: %d characters, over the %d-character cap",
			path, n, maxDescription)
	}
	e.Description = description
	return nil
}

// budgetAt records the declared amount.
func (e *Entry) budgetAt(raw any, path string) *core.RefusalError {
	budget, ref := budgetAt(raw, path+".budget_usd")
	if ref != nil {
		return ref
	}
	e.BudgetUSD = budget
	return nil
}

// lensesFieldAt records the declared lens rows.
func (e *Entry) lensesFieldAt(raw any, path string) *core.RefusalError {
	lenses, ref := lensesAt(raw, path+".lenses")
	if ref != nil {
		return ref
	}
	e.Lenses = lenses
	return nil
}

// synthesizeAt validates the required synthesizer hint.
func (e *Entry) synthesizeAt(obj map[string]any, path string) *core.RefusalError {
	raw, ref := member(obj, path, "synthesize")
	if ref != nil {
		return ref
	}
	nodePath := path + ".synthesize"
	node, ref := objectAt(raw, nodePath)
	if ref != nil {
		return ref
	}
	if ref := checkKeys(node, nodePath, synthesizeKeys); ref != nil {
		return ref
	}
	model, ref := modelAt(node, nodePath)
	if ref != nil {
		return ref
	}
	e.Synthesize = Synthesize{Model: model}
	return nil
}

// lensesAt validates the declared lens rows: at least one, no repeated id, and
// every row's id, hint, and caps.
func lensesAt(raw any, path string) ([]Lens, *core.RefusalError) {
	items, ok := raw.([]any)
	if !ok {
		return nil, refuse(CodeBad, "%s: want an array, got %s", path, strictjson.Kind(raw))
	}
	if len(items) == 0 {
		return nil, refuse(CodeBad, "%s: want at least one lens", path)
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

// lensAt validates one lens row and records its id as taken.
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
	return lensHintsAt(obj, path, id)
}

// lensIDAt requires the row's id: a lowercase slug, unique within the entry.
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

// lensHintsAt reads one row's optional model hint and dispatch caps.
func lensHintsAt(obj map[string]any, path, id string) (Lens, *core.RefusalError) {
	lens := Lens{ID: id}
	var ref *core.RefusalError
	if lens.Model, ref = modelAt(obj, path); ref != nil {
		return Lens{}, ref
	}
	if lens.MaxFiles, ref = capAt(obj, path, "max_files", minLensFiles, maxLensFiles); ref != nil {
		return Lens{}, ref
	}
	if lens.MaxFindings, ref = capAt(obj, path, "max_findings", minLensFindings, maxLensFindings); ref != nil {
		return Lens{}, ref
	}
	return lens, nil
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

// intAt requires a JSON integer in the inclusive range.
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
		return 0, refuse(CodeBad, "%s: want an integer %d..%d, got %d", path, lo, hi, value)
	}
	return value, nil
}

// floatBits is the width ParseFloat reads: a declared amount is a float64.
const floatBits = 64

// budgetAt requires a positive USD amount: a projection run records and the
// driver may honor, so zero and negative values are not amounts at all.
func budgetAt(raw any, path string) (float64, *core.RefusalError) {
	num, ok := raw.(json.Number)
	if !ok {
		return 0, refuse(CodeBad, "%s: want a number, got %s", path, strictjson.Kind(raw))
	}
	budget, err := strconv.ParseFloat(num.String(), floatBits)
	if err != nil {
		return 0, refuse(CodeBad, "%s: want a number, got %s", path, num)
	}
	if budget <= 0 {
		return 0, refuse(CodeBad, "%s: want a number above 0, got %s", path, num)
	}
	return budget, nil
}

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

// stringAt requires a non-empty JSON string.
func stringAt(raw any, path string) (string, *core.RefusalError) {
	text, ok := raw.(string)
	if !ok {
		return "", refuse(CodeBad, "%s: want a string, got %s", path, strictjson.Kind(raw))
	}
	if text == "" {
		return "", refuse(CodeBad, "%s: want a non-empty string", path)
	}
	return text, nil
}

// refuse seals one recipe diagnostic. The code comes first so the archtest
// refusal-code scan reads the same emission shape every package uses.
func refuse(code, format string, args ...any) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: fmt.Sprintf(format, args...)}
}
