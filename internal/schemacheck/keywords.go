package schemacheck

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/jq"
)

// Schema keywords this checker reads.
const (
	keyRef      = "$ref"
	keyDefs     = "$defs"
	keyType     = "type"
	keyFormat   = "format"
	keyPattern  = "pattern"
	keyItems    = "items"
	keyMin      = "minimum"
	keyMax      = "maximum"
	keyMinLen   = "minLength"
	keyMaxLen   = "maxLength"
	keyAnyOf    = "anyOf"
	keyProps    = "properties"
	keyRequired = "required"
	keyAddl     = "additionalProperties"
	keyConst    = "const"
	keyEnum     = "enum"

	formatDateTime = "date-time"

	// Pointer and reference separators, named so the escapes stay readable.
	refSeparator = "/"
	refHash      = "#"
	escapedSlash = "~1"
	escapedTilde = "~0"
	tilde        = "~"
	typeObject   = "object"
	typeArray    = "array"
	typeString   = "string"
	typeInteger  = "integer"
	typeNumber   = "number"
	typeBoolean  = "boolean"
	typeNull     = "null"
)

// constraintKeys are the keywords this checker enforces or understands
// structurally.
var constraintKeys = names(keyRef, keyDefs, keyType, keyFormat, keyPattern,
	keyItems, keyMin, keyMax, keyMinLen, keyMaxLen, keyAnyOf, keyProps,
	keyRequired, keyAddl, keyConst, keyEnum)

// annotationKeys carry documentation, not constraints, so they are ignored.
var annotationKeys = names("$schema", "$id", "title", "description", "default",
	"examples", "$comment")

// names builds a keyword set from a list.
func names(keys ...string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		set[key] = true
	}
	return set
}

// applyKeywords runs every keyword check in a fixed order. Each check ignores
// keywords it does not own, so the list stays flat.
func (w *walk) applyKeywords(d document, s map[string]any, value any, path string) {
	w.checkType(d, s, value, path)
	w.checkConst(d, s, value, path)
	w.checkEnum(d, s, value, path)
	w.checkRequired(d, s, value, path)
	w.checkProperties(d, s, value, path)
	w.checkAdditional(d, s, value, path)
	w.checkItems(d, s, value, path)
	w.checkBounds(d, s, value, path)
	w.checkLength(d, s, value, path)
	w.checkPattern(d, s, value, path)
	w.checkFormat(d, s, value, path)
	w.checkAnyOf(d, s, value, path)
}

// checkKeywords fails loudly on a keyword outside the implemented subset, so a
// schema that outgrows this checker breaks the build instead of passing
// silently.
func (w *walk) checkKeywords(s map[string]any, path string) {
	var unknown []string
	for key := range s {
		if !constraintKeys[key] && !annotationKeys[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return
	}
	slices.Sort(unknown)
	w.fail(path, "unsupported keyword(s) for this checker: "+strings.Join(unknown, " "))
}

// checkType enforces "type" (one name or an array of names).
func (w *walk) checkType(_ document, s map[string]any, value any, path string) {
	spec, ok := s[keyType]
	if !ok || typeMatches(spec, value) {
		return
	}
	w.fail(path, fmt.Sprintf("want type %s, got %s", renderJSON(spec), jsonType(value)))
}

// checkConst enforces "const".
func (w *walk) checkConst(_ document, s map[string]any, value any, path string) {
	want, ok := s["const"]
	if !ok || equalJSON(want, value) {
		return
	}
	w.fail(path, fmt.Sprintf("%s is not the const %s", renderJSON(value), renderJSON(want)))
}

// checkEnum enforces "enum".
func (w *walk) checkEnum(_ document, s map[string]any, value any, path string) {
	members, ok := s["enum"].([]any)
	if !ok {
		if _, has := s["enum"]; has {
			w.fail(path, "enum must be an array")
		}
		return
	}
	if slicesContainsMember(members, value) {
		return
	}
	w.fail(path, fmt.Sprintf("%s is not one of %s", renderJSON(value), renderJSON(members)))
}

// checkRequired enforces "required" on an object value.
func (w *walk) checkRequired(_ document, s map[string]any, value any, path string) {
	raw, has := s[keyRequired]
	if !has {
		return
	}
	absent, err := missingKeys(raw, value)
	if err != nil {
		w.fail(path, err.Error())
		return
	}
	if len(absent) > 0 {
		w.fail(path, "missing required key(s): "+strings.Join(absent, " "))
	}
}

// missingKeys lists the required names the value does not carry; a schema
// whose required list is not an array of strings is an error, not an accept.
func missingKeys(raw, value any) ([]string, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, jq.NewErr("required keys but the value is not an object")
	}
	required, ok := raw.([]any)
	if !ok {
		return nil, jq.NewErr("required must be an array")
	}
	var missing []string
	for _, entry := range required {
		key, ok := entry.(string)
		if !ok {
			return nil, jq.NewErrf("required entry %s is not a string", renderJSON(entry))
		}
		if _, has := object[key]; !has {
			missing = append(missing, key)
		}
	}
	return missing, nil
}

// checkProperties validates each declared property that the value carries.
func (w *walk) checkProperties(d document, s map[string]any, value any, path string) {
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	properties, _ := s[keyProps].(map[string]any) //nolint:errcheck // absent properties means nothing to validate
	for _, key := range propertyNames(s) {
		sub, has := object[key]
		if !has {
			continue
		}
		w.check(d, properties[key], sub, path+"."+key)
	}
}

// checkAdditional enforces "additionalProperties": false rejects unknown keys,
// and a schema validates them. A value that is not an object is untouched.
func (w *walk) checkAdditional(d document, s map[string]any, value any, path string) {
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	spec, has := s[keyAddl]
	if !has {
		return
	}
	extra := unknownKeys(object, s)
	if len(extra) > 0 {
		w.checkExtra(d, spec, object, extra, path)
	}
}

// checkExtra applies "additionalProperties" to the keys no property covers: a
// false schema rejects them, and a schema validates each of them.
func (w *walk) checkExtra(d document, spec any, object map[string]any, extra []string, path string) {
	if allowed, ok := spec.(bool); ok {
		if !allowed {
			w.fail(path, "additional properties not allowed: "+strings.Join(extra, " "))
		}
		return
	}
	for _, key := range extra {
		w.check(d, spec, object[key], path+"."+key)
	}
}

// checkItems validates every array element against a single-schema "items".
func (w *walk) checkItems(d document, s map[string]any, value any, path string) {
	items, has := s[keyItems]
	if !has {
		return
	}
	list, ok := value.([]any)
	if !ok {
		return
	}
	if _, tupleForm := items.([]any); tupleForm {
		w.fail(path, "unsupported items form (want a single schema)")
		return
	}
	for index, element := range list {
		w.check(d, items, element, fmt.Sprintf("%s[%d]", path, index))
	}
}

// checkBounds enforces numeric "minimum"/"maximum".
func (w *walk) checkBounds(_ document, s map[string]any, value any, path string) {
	got, ok := numberOf(value)
	if !ok {
		return
	}
	if low, has := numberOf(s[keyMin]); has && got < low {
		w.fail(path, fmt.Sprintf("%s is below minimum %s", renderJSON(value), renderJSON(s[keyMin])))
	}
	if high, has := numberOf(s[keyMax]); has && got > high {
		w.fail(path, fmt.Sprintf("%s is above maximum %s", renderJSON(value), renderJSON(s[keyMax])))
	}
}

// checkLength enforces "minLength"/"maxLength" in characters (code points, as
// the specification counts them).
func (w *walk) checkLength(_ document, s map[string]any, value any, path string) {
	text, ok := value.(string)
	if !ok {
		return
	}
	length := utf8.RuneCountInString(text)
	if low, has := intBound(s[keyMinLen]); has && length < low {
		w.fail(path, fmt.Sprintf("shorter than minLength %d", low))
	}
	if high, has := intBound(s[keyMaxLen]); has && length > high {
		w.fail(path, fmt.Sprintf("longer than maxLength %d", high))
	}
}

// checkPattern enforces "pattern" as a regular expression match (Go's RE2
// syntax; the published schemas use plain character classes).
func (w *walk) checkPattern(_ document, s map[string]any, value any, path string) {
	raw, has := s[keyPattern]
	if !has {
		return
	}
	pattern, ok := raw.(string)
	if !ok {
		w.fail(path, "pattern must be a string")
		return
	}
	text, ok := value.(string)
	if !ok {
		return
	}
	matched, err := regexp.MatchString(pattern, text)
	if err != nil {
		w.fail(path, fmt.Sprintf("invalid pattern %q: %v", pattern, err))
		return
	}
	if !matched {
		w.fail(path, fmt.Sprintf("%s does not match pattern %q", renderJSON(value), pattern))
	}
}

// checkFormat enforces "format: date-time" (RFC 3339). Other formats are
// outside this checker and fail loudly, as the shell checker did.
func (w *walk) checkFormat(_ document, s map[string]any, value any, path string) {
	raw, has := s[keyFormat]
	if !has {
		return
	}
	format, ok := raw.(string)
	if !ok || format != formatDateTime {
		w.fail(path, fmt.Sprintf("unsupported format %q (this checker knows date-time only)", format))
		return
	}
	text, ok := value.(string)
	if !ok {
		return
	}
	if _, err := time.Parse(time.RFC3339, text); err != nil {
		w.fail(path, renderJSON(value)+" is not a date-time (want RFC 3339)")
	}
}

// checkAnyOf succeeds when one branch validates; a branch is tried in its own
// pass so its failures never leak into this one.
func (w *walk) checkAnyOf(d document, s map[string]any, value any, path string) {
	raw, has := s[keyAnyOf]
	if !has {
		return
	}
	branches, ok := raw.([]any)
	if !ok || len(branches) == 0 {
		w.fail(path, "anyOf must be a non-empty array")
		return
	}
	var nearest []string
	for index, branch := range branches {
		attempt := &walk{files: w.files}
		attempt.check(d, branch, value, path)
		if len(attempt.fails) == 0 {
			return
		}
		nearest = append(nearest, fmt.Sprintf("%d: %s", index, attempt.fails[0].String()))
	}
	w.fail(path, "matches no anyOf branch ("+strings.Join(nearest, "; ")+")")
}

// propertyNames lists an object schema's declared properties in a stable order.
func propertyNames(s map[string]any) []string {
	properties, _ := s[keyProps].(map[string]any) //nolint:errcheck // absent properties means no declared names
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// unknownKeys lists the value's keys that no declared property covers.
func unknownKeys(object, s map[string]any) []string {
	declared, _ := s[keyProps].(map[string]any) //nolint:errcheck // absent properties means every key is extra
	var extra []string
	for key := range object {
		if _, known := declared[key]; !known {
			extra = append(extra, key)
		}
	}
	slices.Sort(extra)
	return extra
}

// typeChecks maps each JSON Schema type name to its predicate.
var typeChecks = map[string]func(any) bool{
	typeObject:  func(v any) bool { _, ok := v.(map[string]any); return ok },
	typeArray:   func(v any) bool { _, ok := v.([]any); return ok },
	typeString:  func(v any) bool { _, ok := v.(string); return ok },
	typeBoolean: func(v any) bool { _, ok := v.(bool); return ok },
	typeNull:    func(v any) bool { return v == nil },
	typeNumber:  func(v any) bool { _, ok := numberOf(v); return ok },
	typeInteger: isInteger,
}

// typeMatches checks a type name or an array of names against value.
func typeMatches(spec, value any) bool {
	if choices, ok := spec.([]any); ok {
		return anyTypeMatches(choices, value)
	}
	name, ok := spec.(string)
	if !ok {
		return false
	}
	matches, known := typeChecks[name]
	return known && matches(value)
}

// anyTypeMatches reports whether one of the type names matches value.
func anyTypeMatches(choices []any, value any) bool {
	return slices.ContainsFunc(choices, func(choice any) bool { return typeMatches(choice, value) })
}

// jsonType names the decoded value's JSON type for a failure message.
func jsonType(value any) string {
	switch value.(type) {
	case nil:
		return typeNull
	case bool:
		return typeBoolean
	case string:
		return typeString
	case map[string]any:
		return typeObject
	case []any:
		return typeArray
	case json.Number, float64, int:
		return typeNumber
	default:
		return fmt.Sprintf("%T", value)
	}
}

// isInteger reports whether value is an integer: JSON text without a fraction
// or exponent, or a float with no fractional part.
func isInteger(value any) bool {
	switch number := value.(type) {
	case json.Number:
		if strings.ContainsAny(number.String(), ".eE") {
			return false
		}
		_, err := number.Int64()
		return err == nil
	case float64:
		return number == math.Trunc(number)
	case int:
		return true
	default:
		return false
	}
}

// numberOf converts a decoded number to float64, whether the document was
// decoded with number fidelity or by encoding/json's float64 default.
func numberOf(value any) (float64, bool) {
	switch number := value.(type) {
	case json.Number:
		converted, err := number.Float64()
		return converted, err == nil
	case float64:
		return number, true
	case int:
		return float64(number), true
	default:
		return 0, false
	}
}

// intBound reads an integer-valued keyword such as minLength.
func intBound(raw any) (int, bool) {
	got, ok := numberOf(raw)
	if !ok {
		return 0, false
	}
	return int(got), true
}

// slicesContainsMember reports whether value equals one enum member.
func slicesContainsMember(members []any, value any) bool {
	return slices.ContainsFunc(members, func(member any) bool { return equalJSON(member, value) })
}

// equalJSON reports whether two decoded values are the same JSON value:
// encoding/json renders object keys sorted and keeps number literals, so the
// compact renderings compare exactly.
func equalJSON(a, b any) bool { return renderJSON(a) == renderJSON(b) }

// renderJSON renders one decoded value as compact JSON for a message.
func renderJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(raw)
}
