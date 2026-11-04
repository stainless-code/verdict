// Package jq mirrors the jq behaviors the record path depends on, so
// the Go port emits byte-identical stores and reads agent JSON the same way.
//
// The rules below are bug-for-bug ports, verified against jq 1.7 in the
// record golden suite:
//   - `//` yields the right side when the left is null OR false.
//   - `jq -r` renders null as "null", strings raw, numbers in jq's literal
//     form (see [CanonNumber]), booleans as true/false, containers as compact
//     JSON.
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
	"math"
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
		return Float(t)
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
		return Float(t)
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

// CanonNumber normalizes a JSON number literal to the bytes jq prints for
// that same literal. Integer literals are already canonical (JSON forbids
// leading zeros); every other literal is rebuilt from its own digits and
// exponent, exactly as jq's decNumber path does. Nothing here converts to
// float64 or int64, so the result cannot differ by architecture or lose a
// digit above 2^53: 9.223372036854775808e18 keeps all nineteen digits instead
// of saturating to MaxInt64 on arm64 and going negative on amd64. Anchor
// lines, confidence levels, and counts all live in the plain-integer domain;
// this only matters for exotic agent-written literals.
func CanonNumber(lit string) string {
	if isPlainInt(lit) {
		return lit
	}
	if canon, ok := canonLiteral(lit); ok {
		return canon
	}
	return lit
}

// Float renders a Go float64 the way jq prints a computed double: the
// shortest round-trip digits, plain notation unless the decimal point sits
// more than fifteen places past the digits or four or more places before
// them, and otherwise exponential with a signed two-digit-minimum exponent
// (1e-05, 1e+16). It is the counterpart of [CanonNumber], which only knows
// number literals: a double has no literal to preserve. NaN and the
// infinities pass through as strconv spells them. No float64 is converted to
// an integer, so the result cannot differ by architecture.
func Float(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return strconv.FormatFloat(f, 'g', -1, floatBits)
	}
	if f == 0 {
		return zeroLit // jq 1.7 prints a computed -0 as -0 and 1.8 as 0; the sign was never kept here
	}
	body := floatBody(math.Abs(f))
	if math.Signbit(f) {
		return minusSign + body
	}
	return body
}

// floatBody renders a positive finite double from its shortest digits.
func floatBody(f float64) string {
	mant, exp, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, floatBits), "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, err := strconv.Atoi(exp)
	if err != nil { // FormatFloat 'e' always writes a valid exponent
		return strconv.FormatFloat(f, 'g', -1, floatBits)
	}
	decpt := x + 1
	switch {
	case decpt <= -floatPlainBelow || decpt > len(digits)+floatPlainAbove:
		return exponential(digits, x)
	case decpt > len(digits):
		return digits + strings.Repeat(zeroLit, decpt-len(digits))
	default:
		return plainDecimal(digits, decpt-len(digits))
	}
}

// exponential renders digits in the double notation jq uses: one leading
// digit, the rest after a point, then e, an explicit sign and at least two
// exponent digits.
func exponential(digits string, exp int) string {
	var b strings.Builder
	b.WriteByte(digits[0])
	if len(digits) > 1 {
		b.WriteByte('.')
		b.WriteString(digits[1:])
	}
	b.WriteByte('e')
	if exp < 0 {
		b.WriteString(minusSign)
		exp = -exp
	} else {
		b.WriteByte('+')
	}
	if exp < minExponentDigits {
		b.WriteString(zeroLit)
	}
	b.WriteString(strconv.Itoa(exp))
	return b.String()
}

// canonLiteral re-renders one JSON number literal the way jq does: plain
// notation while the exponent is non-positive and the adjusted exponent is
// at least [minPlainAdjusted], exponential notation otherwise. jq keeps the
// literal's trailing fraction zeros ("2.0", "1.500"), its exponent spelling
// ("1E+7"), and the sign of zero ("-0.0"). ok is false for anything that is
// not a number with a representable exponent; the caller then passes the
// literal through untouched.
func canonLiteral(lit string) (string, bool) {
	mant, exponent, neg, ok := cutExponent(lit)
	if !ok {
		return "", false
	}
	digits, fracExp, ok := literalDigits(mant)
	if !ok {
		return "", false
	}
	body := jqDecimal(digits, exponent+fracExp)
	if neg {
		return "-" + body, true
	}
	return body, true
}

// jqDecimal renders digits×10^exponent in the notation jq picks for an
// adjusted exponent of this size.
func jqDecimal(digits string, exponent int) string {
	if adjusted := exponent + len(digits) - 1; exponent > 0 || adjusted < minPlainAdjusted {
		return scientific(digits, adjusted)
	}
	return plainDecimal(digits, exponent)
}

// literalDigits folds a mantissa into its significant digits (leading zeros
// dropped, trailing zeros kept) and the power of ten its fraction carries.
func literalDigits(mant string) (string, int, bool) {
	intPart, fracPart, found := strings.Cut(mant, ".")
	if !isPlainInt(intPart) || (found && !isPlainInt(fracPart)) {
		return "", 0, false
	}
	digits := strings.TrimLeft(intPart+fracPart, "0")
	if digits == "" {
		digits = "0"
	}
	return digits, -len(fracPart), true
}

// cutExponent splits a JSON number literal into its mantissa, its exponent
// (zero when absent) and its sign. ok is false without a mantissa or when the
// exponent does not fit an int (jq falls back to a double there; verdict
// keeps the literal instead of guessing).
func cutExponent(lit string) (mant string, exponent int, neg bool, ok bool) {
	s := lit
	if strings.HasPrefix(s, "-") {
		neg, s = true, s[1:]
	}
	mant = s
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e, err := strconv.Atoi(s[i+1:])
		if err != nil {
			return "", 0, false, false
		}
		mant, exponent = s[:i], e
	}
	if mant == "" {
		return "", 0, false, false
	}
	return mant, exponent, neg, true
}

// scientific renders digits in jq's exponential notation: one leading digit,
// the rest after a point when there are more, then the adjusted exponent.
func scientific(digits string, adjusted int) string {
	var b strings.Builder
	b.WriteByte(digits[0])
	if len(digits) > 1 {
		b.WriteByte('.')
		b.WriteString(digits[1:])
	}
	b.WriteByte('E')
	if adjusted < 0 {
		b.WriteByte('-')
		adjusted = -adjusted
	} else {
		b.WriteByte('+')
	}
	b.WriteString(strconv.Itoa(adjusted))
	return b.String()
}

// plainDecimal renders digits in plain notation at the given power of ten,
// padding with at most [minPlainAdjusted] leading fraction zeros (the
// caller sends everything smaller to [scientific]).
func plainDecimal(digits string, exponent int) string {
	point := len(digits) + exponent
	switch {
	case point >= len(digits):
		return digits
	case point > 0:
		return digits[:point] + "." + digits[point:]
	default:
		return "0." + strings.Repeat("0", -point) + digits
	}
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
	if err := sealTail(dec, opDecodeJSON); err != nil {
		return nil, err
	}
	return v, nil
}

// sealTail reports anything but a clean end of stream after a document, sealed
// for op. [json.Decoder.More] also reports false in front of a stray closer
// (`}` or `]`), so a tail check built on it accepts `{"a":1}}` — the strict
// decode entries share this one check instead.
func sealTail(dec *json.Decoder, op string) error {
	_, err := dec.Token()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return Sealed(op, errTrailingData)
	}
	return Sealed(op, err)
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
	// minPlainAdjusted is the smallest adjusted exponent jq writes in plain
	// notation; below it the literal renders as exponential.
	minPlainAdjusted = -6
	// floatPlainBelow is how many places before the digits jq's double
	// printing still writes plain: a decimal point at or past -4 turns
	// exponential (0.0001 stays plain, 0.00001 prints 1e-05).
	floatPlainBelow = 4
	// minExponentDigits is the exponent magnitude below which jq's double
	// printing pads a leading zero (1e-05, not 1e-5).
	minExponentDigits = 10
	// zeroLit and minusSign are the digit and sign jq's number printing pads
	// and prefixes with.
	zeroLit   = "0"
	minusSign = "-"
	// floatPlainAbove is how many zeros jq's double printing appends to the
	// digits before it turns exponential (1e15 plain, 1e16 prints 1e+16).
	floatPlainAbove = 15
	// newline terminates encoder output.
	newline = "\n"
	// opDecodeJSON names the JSON decode operation in sealed errors.
	opDecodeJSON = "decode json"
)

// nullLit is the JSON null rendering.
const nullLit = "null"
