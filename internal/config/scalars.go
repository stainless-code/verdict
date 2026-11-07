package config

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	// decimalBase parses the base-10 integer literals configuration uses.
	decimalBase = 10
	// floatBits parses USD amounts and rates as 64-bit floats.
	floatBits = 64
)

// itoa renders a small non-negative index without formatting machinery.
func itoa(i int) string {
	return strconv.Itoa(i)
}

// asObject requires a JSON object, naming misplaced nulls and wrong types.
func asObject(raw any, path string) (map[string]any, *core.RefusalError) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, refuse(CodeBadConfig, "%s: want an object, got %s", path, jsonKind(raw))
	}
	return obj, nil
}

// asArray requires a JSON array, naming misplaced nulls and wrong types.
func asArray(raw any, path string) ([]any, *core.RefusalError) {
	items, ok := raw.([]any)
	if !ok {
		return nil, refuse(CodeBadConfig, "%s: want an array, got %s", path, jsonKind(raw))
	}
	return items, nil
}

// asBool requires a JSON boolean.
func asBool(raw any, path string) (bool, *core.RefusalError) {
	val, ok := raw.(bool)
	if !ok {
		return false, refuse(CodeBadConfig, "%s: want a boolean, got %s", path, jsonKind(raw))
	}
	return val, nil
}

// nonEmptyString requires a non-blank string.
func nonEmptyString(raw any, path string) (string, *core.RefusalError) {
	str, ok := raw.(string)
	if !ok {
		return "", refuse(CodeBadConfig, "%s: want a string, got %s", path, jsonKind(raw))
	}
	if len(str) == 0 {
		return "", refuse(CodeBadConfig, "%s: want a non-empty string", path)
	}
	return str, nil
}

// asEnum requires a string from the closed vocabulary.
func asEnum(raw any, path string, vocab []string) (string, *core.RefusalError) {
	str, ok := raw.(string)
	if !ok {
		return "", refuse(CodeBadConfig, "%s: want a string, got %s", path, jsonKind(raw))
	}
	if slices.Contains(vocab, str) {
		return str, nil
	}
	return "", refuse(CodeBadConfig, "%s: want one of %s, got %q", path, joinQuoted(vocab), str)
}

// nonEmptyStringArray requires an array of non-empty strings with at
// least minLen entries.
func nonEmptyStringArray(raw any, path string, minLen int) ([]string, *core.RefusalError) {
	items, ref := asArray(raw, path)
	if ref != nil {
		return nil, ref
	}
	if len(items) < minLen {
		return nil, refuse(CodeBadConfig, "%s: want %d or more entries", path, minLen)
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		str, ref := nonEmptyString(item, arrayPath(path, i))
		if ref != nil {
			return nil, ref
		}
		out = append(out, str)
	}
	return out, nil
}

// enumArray requires an array of vocabulary strings with at least minLen
// entries.
func enumArray(raw any, path string, vocab []string, minLen int) ([]string, *core.RefusalError) {
	items, ref := asArray(raw, path)
	if ref != nil {
		return nil, ref
	}
	if len(items) < minLen {
		return nil, refuse(CodeBadConfig, "%s: want %d or more entries", path, minLen)
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		str, ref := asEnum(item, arrayPath(path, i), vocab)
		if ref != nil {
			return nil, ref
		}
		out = append(out, str)
	}
	return out, nil
}

// checkIDArray requires a non-empty array of prepublication check ids.
func checkIDArray(raw any, path string) ([]string, *core.RefusalError) {
	return enumArray(raw, path, checkEnum, 1)
}

// globArray requires an array of portable globs with at least minLen
// entries.
func globArray(raw any, path string, minLen int) ([]string, *core.RefusalError) {
	items, ref := asArray(raw, path)
	if ref != nil {
		return nil, ref
	}
	if len(items) < minLen {
		return nil, refuse(CodeBadConfig, "%s: want %d or more entries", path, minLen)
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		str, ok := item.(string)
		if !ok {
			return nil, refuse(CodeBadConfig, "%s: want a string, got %s", arrayPath(path, i), jsonKind(item))
		}
		if ref := checkGlob(str, arrayPath(path, i)); ref != nil {
			return nil, ref
		}
		out = append(out, str)
	}
	return out, nil
}

// jsonNumber is a decoded JSON number with its integer reading resolved:
// integer holds only when the literal parses as a base-10 int64.
type jsonNumber struct {
	literal string
	value   float64
	intVal  int64
	integer bool
}

// toNumber converts a decoded value into a JSON number, refusing
// misplaced nulls and wrong types with the caller's expectation named.
func toNumber(raw any, path, want string) (jsonNumber, *core.RefusalError) {
	var zero jsonNumber
	lit, ok := raw.(json.Number)
	if !ok {
		return zero, refuse(CodeBadConfig, "%s: want %s, got %s", path, want, jsonKind(raw))
	}
	val, err := lit.Float64()
	if err != nil {
		return zero, refuse(CodeBadConfig, "%s: want %s, got %s", path, want, jsonKind(raw))
	}
	out := jsonNumber{literal: lit.String(), value: val}
	if intVal, err := strconv.ParseInt(lit.String(), decimalBase, floatBits); err == nil {
		out.intVal, out.integer = intVal, true
	}
	return out, nil
}

// positiveFloat requires a finite number above zero.
func positiveFloat(raw any, path string) (float64, *core.RefusalError) {
	num, ref := toNumber(raw, path, "a number above 0")
	if ref != nil {
		return 0, ref
	}
	if math.IsNaN(num.value) || math.IsInf(num.value, 0) || num.value <= 0 {
		return 0, refuse(CodeBadConfig, "%s: want a number above 0", path)
	}
	return num.value, nil
}

// boundedFloat requires a finite number inside the closed interval.
func boundedFloat(raw any, path string, lo, hi float64) (float64, *core.RefusalError) {
	num, ref := toNumber(raw, path, "a number")
	if ref != nil {
		return 0, ref
	}
	if math.IsNaN(num.value) || math.IsInf(num.value, 0) || num.value < lo || num.value > hi {
		return 0, refuse(CodeBadConfig, "%s: want a number from %v to %v", path, lo, hi)
	}
	return num.value, nil
}

// positiveInt requires an integer number above zero.
func positiveInt(raw any, path string) (int64, *core.RefusalError) {
	return boundedInt(raw, path, 1)
}

// boundedInt requires an integer number at or above the floor.
func boundedInt(raw any, path string, floor int64) (int64, *core.RefusalError) {
	num, ref := toNumber(raw, path, "an integer")
	if ref != nil {
		return 0, ref
	}
	if !num.integer {
		return 0, refuse(CodeBadConfig, "%s: want an integer, got %s", path, num.literal)
	}
	if num.intVal < floor {
		return 0, refuse(CodeBadConfig, "%s: want an integer at or above %d", path, floor)
	}
	return num.intVal, nil
}

// boundedCount requires a small non-negative integer count at or above
// the floor. Counts ride Go ints; the parser already bounds the literal
// to int64, and every shipped platform is 64-bit.
func boundedCount(raw any, path string, floor int64) (int, *core.RefusalError) {
	val, ref := boundedInt(raw, path, floor)
	if ref != nil {
		return 0, ref
	}
	return int(val), nil
}

// arrayPath renders the canonical indexed child path.
func arrayPath(path string, i int) string {
	return path + "[" + itoa(i) + "]"
}

// joinQuoted renders a vocabulary as `"a", "b", "c"` for diagnostics.
func joinQuoted(vocab []string) string {
	quoted := make([]string, 0, len(vocab))
	for _, v := range vocab {
		quoted = append(quoted, `"`+v+`"`)
	}
	return strings.Join(quoted, ", ")
}
