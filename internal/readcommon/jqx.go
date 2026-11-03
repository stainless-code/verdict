package readcommon

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// Truthy ports jq truthiness: everything except null and false.
func Truthy(v any) bool {
	return v != nil && v != false
}

// Tostring ports jq's `tostring`: strings pass raw, null renders "null",
// numbers render canonically, containers render compact.
func Tostring(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case json.Number:
		return jq.CanonNumber(t.String())
	case jq.Number:
		return jq.CanonNumber(t.String())
	case float64:
		return CanonFloat(t)
	default:
		return jq.Compact(v)
	}
}

// CanonFloat renders a float the way jq prints ordinary magnitudes.
func CanonFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, floatBits)
}

// floatBits is the float precision for rendering and parsing.
const floatBits = 64

// NumVal extracts a numeric value: JSON numbers (either decoder spelling)
// and Go floats count; everything else (including numeric strings, per the
// `numbers` filter) does not.
func NumVal(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		return parseJSONFloat(t.String())
	case jq.Number:
		return parseJSONFloat(t.String())
	default:
		return 0, false
	}
}

// parseJSONFloat parses one JSON number literal.
func parseJSONFloat(lit string) (float64, bool) {
	f, err := strconv.ParseFloat(lit, floatBits)
	if err != nil {
		return 0, false
	}
	return f, true
}

// Type ranks for jq's total value order: null < false < true < numbers <
// strings < arrays < objects.
const (
	rankNull   = 0
	rankFalse  = 1
	rankTrue   = 2
	rankNumber = 3
	rankString = 4
	rankArray  = 5
	rankObject = 6
)

// TypeRank ports jq's total value order for sort_by keys. Exotic containers
// tie-break on their canonical bytes; finding stores only ever sort plain
// scalars.
func TypeRank(v any) int {
	switch v.(type) {
	case nil:
		return rankNull
	case bool:
		if v == false {
			return rankFalse
		}
		return rankTrue
	case float64, json.Number, jq.Number:
		return rankNumber
	case string:
		return rankString
	case []any:
		return rankArray
	default:
		return rankObject
	}
}

// Cmp compares two jq values in jq's total order: type rank first, then
// numeric order for numbers, byte order for strings, length-then-canonical
// bytes for containers. Booleans order false before true.
func Cmp(a, b any) int {
	ra, rb := TypeRank(a), TypeRank(b)
	if ra != rb {
		return cmpInt(ra, rb)
	}
	switch ra {
	case rankFalse, rankTrue:
		return cmpBool(a == true, b == true)
	case rankNumber:
		fa, _ := NumVal(a)
		fb, _ := NumVal(b)
		return cmpFloat(fa, fb)
	case rankString:
		return strings.Compare(asString(a), asString(b))
	default:
		return strings.Compare(jq.Compact(a), jq.Compact(b))
	}
}

// asString renders a string-ranked value (plain strings only reach here;
// exotic String() implementations fall back to their literal).
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return jq.Str(v)
}

// cmpInt orders integers.
func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// cmpBool orders booleans (false before true).
func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	default:
		return 1
	}
}

// cmpFloat orders floats.
func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// EffortTier ports the cost-effort normalization: null/false/missing
// reads standard, strings downcase through the allowlist, anything else
// reads standard.
func EffortTier(eff any) string {
	if eff == nil || eff == false {
		return tierStandard
	}
	switch ASCIILower(Tostring(eff)) {
	case tierCheap, tierStandard, tierDeep:
		return ASCIILower(Tostring(eff))
	default:
		return tierStandard
	}
}

// JSONRaw reads one JSON field with `jq -r ... // empty` semantics:
// missing or null reads "", scalars render jq-style, containers render
// pretty. Unreadable files read "".
func JSONRaw(path, field string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	v, ok := m[field]
	if !ok || v == nil {
		return ""
	}
	return jq.R(v)
}

// ASCIILower lowercases ASCII letters only, mirroring tr -d under C locale.
func ASCIILower(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// IsDigits reports whether s is a non-empty ASCII digit run, mirroring the
// shell's `”|*[!0-9]*` guard inverted. It is the shared spelling of the
// [jq.Digits] gate.
func IsDigits(s string) bool {
	return jq.Digits(s)
}
