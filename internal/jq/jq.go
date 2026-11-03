// Package jq mirrors the jq behaviors the record path depends on, so
// the Go port emits byte-identical stores and reads agent JSON the same way.
//
// The rules below are bug-for-bug ports, verified against jq 1.7 in the
// parity harness (internal/record/parity_test.go):
//   - `//` yields the right side when the left is null OR false.
//   - `jq -r` renders null as "null", strings raw, numbers in jq's shortest
//     round-trip form, booleans as true/false, containers as compact JSON.
//   - `\(...)` interpolation renders null as "" (unlike -r) and otherwise
//     matches -r.
//   - `jq '.'` pretty-prints with 2-space indent and codepoint-sorted keys;
//     `jq -c` compacts with the same key order and escaping, and neither
//     escapes <, >, or &.
package jq

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Number is a raw JSON number literal, preserved verbatim from agent input
// (no float64 precision loss near sigs and SHAs).
type Number string

// String returns the literal text.
func (n Number) String() string { return string(n) }

// NewErr builds a locally owned error at a service boundary: the message is
// ours, so there is nothing to seal (GO-ERRORS-03) and nothing to unwrap.
func NewErr(msg string) error {
	return errors.New(msg) //nolint:wrapcheck // locally constructed message, not an external error
}

// NewErrf is [NewErr] with formatting.
func NewErrf(format string, args ...any) error {
	return fmt.Errorf(format, args...) //nolint:wrapcheck // locally constructed message, not an external error
}

// Sealed annotates err with op without exposing it to [errors.Is]/As, per
// GO-ERRORS-03: layer boundaries seal causes; only the command layer's
// refusal type carries an inspection contract.
func Sealed(op string, err error) error {
	//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
	return fmt.Errorf("%s: %v", op, err)
}

// Falsey reports whether v is falsey in jq: only null (nil) and false.
func Falsey(v any) bool {
	return v == nil || v == false
}

// Alt reads `a // b`: b when a is missing (nil here), null, or false.
func Alt(a, b any) any {
	if Falsey(a) {
		return b
	}
	return a
}

// Str renders v the way `jq -r` does for shell interpolation contexts
// (anchors, paths, body length checks): null renders as "null", strings pass
// raw, [json.Number] uses jq's canonical number form, booleans render as
// true/false, and containers render as compact JSON.
func Str(v any) string {
	switch t := v.(type) {
	case nil:
		return nullLit
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case Number:
		return CanonNumber(t.String())
	case json.Number:
		return CanonNumber(t.String())
	case float64:
		return CanonNumber(strconv.FormatFloat(t, 'g', -1, floatBits))
	default:
		return Compact(t)
	}
}

// R renders v exactly the way `jq -r` does: like Str, except containers
// render pretty-printed (jq -r pretty-prints non-string values instead of
// compacting them). Call sites that mirror a shell `jq -r` read over
// agent-controlled values use R; Str stays for compact-container contexts.
func R(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return nullLit
	}
	switch t := v.(type) {
	case Number:
		return CanonNumber(t.String())
	case json.Number:
		return CanonNumber(t.String())
	case float64:
		return CanonNumber(strconv.FormatFloat(t, 'g', -1, floatBits))
	}
	return valuePretty(v, "", "  ")
}

// Interp renders v for `\(...)` string interpolation: like Str except null
// renders as "" (jq stringifies null to empty inside interpolation).
func Interp(v any) string {
	if v == nil {
		return ""
	}
	return Str(v)
}

// CanonNumber normalizes a JSON number literal to jq's output form. Integer
// literals are already canonical (JSON forbids leading zeros); fractional or
// exponent literals that are integral and fit int64 print as integers, and
// anything else prints in the shortest float form. Anchor lines, confidence
// levels, and counts all live in the plain-integer domain where every form
// agrees; this only matters for exotic agent-written literals.
func CanonNumber(lit string) string {
	if isPlainInt(lit) {
		return lit
	}
	f, err := strconv.ParseFloat(lit, floatBits)
	if err != nil {
		return lit
	}
	if f == float64(int64(f)) && f < maxExactInt && f > -maxExactInt {
		return strconv.FormatInt(int64(f), decimalBase)
	}
	return strconv.FormatFloat(f, 'g', -1, floatBits)
}

// IntOf reads a decoded JSON number as an int. Decoded stores hold
// [json.Number] (the UseNumber decode form); constructed values hold
// [Number]. Float64 and int pass through truncated; anything else reports
// false. Shell arithmetic contexts only ever see validated digit strings,
// so non-plain-integer literals read false here.
func IntOf(v any) (int, bool) {
	switch t := v.(type) {
	case Number:
		return digitsInt(t.String())
	case json.Number:
		return digitsInt(t.String())
	case float64:
		return int(t), true
	case int:
		return t, true
	default:
		return 0, false
	}
}

// Digits reports whether s is a non-empty ASCII digit run (the
// `[[ =~ ^[0-9]+$ ]]` gate). Magnitude is not checked: callers that need an
// int parse with [strconv.Atoi] and refuse the overflow instead of wrapping.
func Digits(s string) bool {
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

// digitsInt parses one validated digit string, refusing values that do not
// fit in an int (a 20-digit anchor must not wrap into a nonsense span).
func digitsInt(s string) (int, bool) {
	if !Digits(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// PrefixInt is the string-to-number coercion used by hunks.tsv: the
// longest leading ASCII digit run ("12abc" reads 12, "-3" reads 0); a prefix
// with no digits reads 0. It reports false when the prefix overflows an int,
// so a hostile 20-digit span refuses rather than wrapping negative.
func PrefixInt(s string) (int, bool) {
	return prefixInt(s, false)
}

// SignedPrefixInt owns the KB counter path's one legacy difference from awk:
// an optional leading minus before the digit run ("-12abc" reads -12). It is
// used only for KB counter fields, which parse with an explicit
// sign case. Overflow still reports false.
func SignedPrefixInt(s string) (int, bool) {
	return prefixInt(s, true)
}

// prefixInt scans the longest leading digit run, optionally after one minus,
// and parses it with [strconv.Atoi] so overflow is an error, not a wrap.
func prefixInt(s string, signed bool) (int, bool) {
	i := 0
	if signed && strings.HasPrefix(s, "-") {
		i = 1
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == start {
		return 0, true
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, false
	}
	return n, true
}

// isPlainInt reports whether lit is already in jq's integer output form.
func isPlainInt(lit string) bool {
	if lit == "" {
		return false
	}
	i := 0
	if lit[0] == '-' {
		i = 1
	}
	if i >= len(lit) {
		return false
	}
	for ; i < len(lit); i++ {
		if lit[i] < '0' || lit[i] > '9' {
			return false
		}
	}
	return true
}

// Decode parses one JSON document with agent-input discipline: numbers stay
// [json.Number] (no float64 precision loss near sigs and SHAs), and the stream
// must hold exactly one value. Size caps belong to the caller (see [Capped]).
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, Sealed(opDecodeJSON, err)
	}
	if dec.More() {
		return nil, Sealed(opDecodeJSON, errTrailingData)
	}
	return v, nil
}

// DecodeObject parses one JSON object document into a string map, preserving
// every unknown key (store passthrough: never drop keys this package does
// not model).
func DecodeObject(data []byte) (map[string]any, error) {
	v, err := Decode(data)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, Sealed(opDecodeJSON, errWantObject)
	}
	return m, nil
}

// DecodeSlurp parses a JSON sequence (jq -s), e.g. kb.jsonl stores: every
// whitespace-separated value in order. A missing or empty file reads as no
// values, matching `jq -s` over an empty input.
func DecodeSlurp(r io.Reader) ([]any, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var out []any
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, Sealed("decode json sequence", err)
		}
		out = append(out, v)
	}
}

// Pretty renders v like `jq '.'`: 2-space indent, keys sorted by codepoint
// (encoding/json sorts map keys, which matches for UTF-8), no HTML escaping,
// trailing newline.
func Pretty(v any) string {
	return compactIndent(v, "", "  ") + "\n"
}

// PrettyOrdered renders v like `jq '.'` with stored key order for *Obj
// (2-space indent, no HTML escaping, trailing newline).
func PrettyOrdered(v any) string {
	return valuePretty(v, "", "  ") + "\n"
}

// Compact renders v like `jq -c`: sorted keys, `:`/`,` separators, no HTML
// escaping. [Number] renders as its raw literal — encoding/json would quote
// the defined string type, but numbers stay numbers.
func Compact(v any) string {
	if n, ok := v.(Number); ok {
		return n.String()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nullLit
	}
	return strings.TrimSuffix(buf.String(), newline)
}

// compactIndent renders v with the given prefix/indent and no HTML escaping.
// [Number] renders as its raw literal (see Compact). It is package-private:
// [Pretty] is the only renderer the CLI uses.
func compactIndent(v any, prefix, indent string) string {
	if n, ok := v.(Number); ok {
		return n.String()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, indent)
	if err := enc.Encode(v); err != nil {
		return nullLit
	}
	return strings.TrimSuffix(buf.String(), newline)
}

// StrOf returns the string value or "" for anything else.
func StrOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// Elements reads `. // [] | .[]` over an unknown-typed value: arrays yield
// elements in order, objects yield values in sorted-key order (jq iterates
// objects by key), anything else yields nothing.
func Elements(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case *Obj:
		out := make([]any, 0, len(t.Keys))
		for _, k := range t.Keys {
			out = append(out, t.Fields[k])
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sortStrings(keys)
		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, t[k])
		}
		return out
	default:
		return nil
	}
}

// sortStrings orders keys the way jq iterates objects: byte order, which
// equals codepoint order for UTF-8.
func sortStrings(s []string) {
	slices.Sort(s)
}

// AsMap returns v as a string map when it is one.
func AsMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// Number parsing and error-message constants.
const (
	// floatBits is the precision for threshold and literal parsing.
	floatBits = 64
	// decimalBase is the radix for integer rendering.
	decimalBase = 10
	// maxExactInt is the magnitude below which integral floats print as integers.
	maxExactInt = 1e21
	// newline terminates encoder output.
	newline = "\n"
	// opDecodeJSON names the JSON decode operation in sealed errors.
	opDecodeJSON = "decode json"
)

// nullLit is the JSON null rendering.
const nullLit = "null"
