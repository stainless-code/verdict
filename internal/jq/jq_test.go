package jq_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestAltFalsey checks Alt treats null and false as absent.
func TestAltFalsey(t *testing.T) {
	if got := jq.Alt(nil, "b"); got != "b" {
		t.Errorf("Alt(nil) = %v; want b", got)
	}
	if got := jq.Alt(false, "b"); got != "b" {
		t.Errorf("Alt(false) = %v; want b (jq // is falsey for null AND false)", got)
	}
	if got := jq.Alt(0, "b"); got != 0 {
		t.Errorf("Alt(0) = %v; want 0 (zero is truthy in jq)", got)
	}
	if got := jq.Alt("", "b"); got != "" {
		t.Errorf("Alt(\"\") = %v; want empty (empty string is truthy in jq)", got)
	}
}

// TestStr checks the jq string rendering across JSON types.
func TestStr(t *testing.T) {
	tests := []struct {
		name string
		give any
		want string
	}{
		{"null", nil, "null"},
		{"string", "x", "x"},
		{"true", true, "true"},
		{"false", false, "false"},
		{"int literal", jq.Number("2"), "2"},
		{"integral float literal", jq.Number("2.0"), "2"},
		{"object", map[string]any{"a": 1}, `{"a":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jq.Str(tt.give); got != tt.want {
				t.Errorf("Str(%v) = %q; want %q", tt.give, got, tt.want)
			}
		})
	}
}

// TestInterpNull checks interpolation renders null as empty.
func TestInterpNull(t *testing.T) {
	if got := jq.Interp(nil); got != "" {
		t.Errorf("Interp(nil) = %q; want empty (interpolation stringifies null to nothing)", got)
	}
	if got := jq.Interp("x"); got != "x" {
		t.Errorf("Interp(x) = %q; want x", got)
	}
}

// TestCanonNumber checks number canonicalization matches jq.
func TestCanonNumber(t *testing.T) {
	tests := []struct{ give, want string }{
		{"2", "2"},
		{"2.0", "2"},
		{"-0", "-0"},
		{"abc", "abc"},
	}
	for _, tt := range tests {
		if got := jq.CanonNumber(tt.give); got != tt.want {
			t.Errorf("CanonNumber(%q) = %q; want %q", tt.give, got, tt.want)
		}
	}
}

// TestNumberRaw checks jq.Number encodes as a raw JSON number everywhere.
func TestNumberRaw(t *testing.T) {
	o := jq.NewObj("pr", jq.Number("47"), "nested", jq.NewObj("n", jq.Number("3")))
	if got := o.MarshalCompact(); got != `{"pr":47,"nested":{"n":3}}` {
		t.Errorf("MarshalCompact() = %s; want raw numbers", got)
	}
	if got := o.MarshalPretty(); got != "{\n  \"pr\": 47,\n  \"nested\": {\n    \"n\": 3\n  }\n}" {
		t.Errorf("MarshalPretty() =\n%s\nwant raw numbers", got)
	}
	if got := jq.Compact(jq.Number("47")); got != "47" {
		t.Errorf("Compact(Number) = %q; want 47", got)
	}
}

// TestR checks the jq -r rendering: raw strings, pretty containers.
func TestR(t *testing.T) {
	if got := jq.R(map[string]any{"a": 1}); got != "{\n  \"a\": 1\n}" {
		t.Errorf("R(object) = %q; want pretty", got)
	}
	if got := jq.R("x"); got != "x" {
		t.Errorf("R(x) = %q; want x", got)
	}
	if got := jq.R(nil); got != "null" {
		t.Errorf("R(nil) = %q; want null", got)
	}
	if got := jq.R(jq.Number("2.0")); got != "2" {
		t.Errorf("R(2.0) = %q; want 2", got)
	}
}

// TestElementsObjectOrder checks Elements preserves object order.
func TestElementsObjectOrder(t *testing.T) {
	o := jq.NewObj("b", 1, "a", 2)
	got := jq.Elements(o)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("Elements(obj) = %v; want [1 2] in insertion order", got)
	}
	if got := jq.Elements(5); got != nil {
		t.Errorf("Elements(5) = %v; want nil", got)
	}
}

// TestOrderedRoundTrip checks ordered decode/encode round-trips.
func TestOrderedRoundTrip(t *testing.T) {
	doc, err := jq.DecodeOrdered([]byte(`{"b":1,"a":[1,{"z":null}],"c":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	o, ok := doc.(*jq.Obj)
	if !ok {
		t.Fatalf("DecodeOrdered root = %T; want *Obj", doc)
	}
	if o.Keys[0] != "b" || o.Keys[1] != "a" || o.Keys[2] != "c" {
		t.Errorf("key order = %v; want [b a c]", o.Keys)
	}
	want := "{\n  \"b\": 1,\n  \"a\": [\n    1,\n    {\n      \"z\": null\n    }\n  ],\n  \"c\": \"x\"\n}"
	if got := o.MarshalPretty(); got != want {
		t.Errorf("MarshalPretty() =\n%s\nwant\n%s", got, want)
	}
}

// intParsingCase is one strict digit-parsing leg.
type intParsingCase struct {
	name string
	lit  string
	want int
	ok   bool
}

// intParsingCases pins the strict digit contract shared by every anchor
// parser: leading zeros parse, signs/fractions/exponents/trailing junk
// refuse, and a value past the int range refuses instead of wrapping.
func intParsingCases() []intParsingCase {
	maxInt := int(^uint(0) >> 1)
	return []intParsingCase{
		{"empty", "", 0, false},
		{"zero", "0", 0, true},
		{"leading zeros", "007", 7, true},
		{"negative", "-3", 0, false},
		{"plus sign", "+3", 0, false},
		{"fraction", "1.5", 0, false},
		{"exponent", "1e3", 0, false},
		{"trailing junk", "12abc", 0, false},
		{"max int", strconv.Itoa(maxInt), maxInt, true},
		{"max int64 plus one", "9223372036854775808", 0, false},
		{"20 digits", "99999999999999999999", 0, false},
	}
}

// TestDigits pins the shared non-empty digit-run gate.
func TestDigits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"zero", "0", true},
		{"leading zeros", "007", true},
		{"negative", "-3", false},
		{"plus sign", "+3", false},
		{"fraction", "1.5", false},
		{"exponent", "1e3", false},
		{"trailing junk", "12abc", false},
		{"20 digits", "99999999999999999999", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := jq.Digits(tc.in); got != tc.want {
				t.Errorf("Digits(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIntOf pins the JSON number accessor: it accepts the digit form only and
// refuses an overflowing literal rather than returning a wrapped int.
func TestIntOf(t *testing.T) {
	t.Parallel()
	for _, tc := range intParsingCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkIntOf(t, tc)
		})
	}
}

// checkIntOf asserts one literal through both decoder spellings.
func checkIntOf(t *testing.T, tc intParsingCase) {
	t.Helper()
	for _, v := range []any{jq.Number(tc.lit), json.Number(tc.lit)} {
		got, ok := jq.IntOf(v)
		if got != tc.want || ok != tc.ok {
			t.Errorf("IntOf(%T %q) = %d, %v; want %d, %v", v, tc.lit, got, ok, tc.want, tc.ok)
		}
	}
}

// TestPrefixInt pins awk's no-sign prefix coercion: leading digits parse,
// everything else follows awk, and an overflow refuses.
func TestPrefixInt(t *testing.T) {
	t.Parallel()
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		name string
		in   string
		want int
		ok   bool
	}{
		{"empty", "", 0, true},
		{"zero", "0", 0, true},
		{"leading zeros", "007", 7, true},
		{"negative", "-3", 0, true},
		{"plus sign", "+3", 0, true},
		{"fraction", "1.5", 1, true},
		{"exponent", "1e3", 1, true},
		{"trailing junk", "12abc", 12, true},
		{"max int", strconv.Itoa(maxInt), maxInt, true},
		{"max int64 plus one", "9223372036854775808", 0, false},
		{"20 digits", "99999999999999999999", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := jq.PrefixInt(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Errorf("PrefixInt(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestSignedPrefixInt pins the KB counter's optional-minus variant, including
// the minimum int (which fits) and the refusing overflow.
func TestSignedPrefixInt(t *testing.T) {
	t.Parallel()
	minInt := -int(^uint(0)>>1) - 1
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		name string
		in   string
		want int
		ok   bool
	}{
		{"empty", "", 0, true},
		{"lone minus", "-", 0, true},
		{"zero", "0", 0, true},
		{"leading zeros", "007", 7, true},
		{"negative", "-3", -3, true},
		{"negative leading zeros", "-007", -7, true},
		{"plus sign", "+3", 0, true},
		{"fraction", "1.5", 1, true},
		{"exponent", "1e3", 1, true},
		{"trailing junk", "12abc", 12, true},
		{"negative trailing junk", "-12abc", -12, true},
		{"max int", strconv.Itoa(maxInt), maxInt, true},
		{"min int", strconv.Itoa(minInt), minInt, true},
		{"max int64 plus one", "9223372036854775808", 0, false},
		{"20 digits", "99999999999999999999", 0, false},
		{"negative 20 digits", "-99999999999999999999", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := jq.SignedPrefixInt(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Errorf("SignedPrefixInt(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}
