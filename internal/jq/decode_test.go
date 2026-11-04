package jq_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestDecodeNumbersStayExact pins the decoder's stated discipline: numbers
// arrive as [json.Number], so a wide literal survives verbatim instead of
// rounding through float64. Independent oracle, jq 1.8.2:
//
//	echo '{"n":99999999999999999999,"f":1.5}' | jq -c .
//	{"n":99999999999999999999,"f":1.5}
//
// Break killed: dropping UseNumber (or the [json.Number] case) makes n a
// float64 and both assertions fail.
func TestDecodeNumbersStayExact(t *testing.T) {
	t.Parallel()
	doc, err := jq.Decode([]byte(`{"n":99999999999999999999,"f":1.5}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	m, ok := doc.(map[string]any)
	if !ok {
		t.Fatalf("Decode root = %T; want map[string]any", doc)
	}
	n, ok := m["n"].(json.Number)
	if !ok {
		t.Fatalf("n = %T; want json.Number (float64 loses the digits)", m["n"])
	}
	if n.String() != "99999999999999999999" {
		t.Errorf("n = %s; want the literal 99999999999999999999", n)
	}
	if got := jq.Str(m["f"]); got != "1.5" {
		t.Errorf("Str(f) = %q; want 1.5", got)
	}
}

// TestDecodeRefusesTrailingData pins the documented one-document contract
// ("the stream must hold exactly one value") for both trailing shapes: a
// second value and a stray closing bracket. [json.Decoder.More] reports false
// in front of a closer, so `{"a":1}}` used to read as a clean document with
// the extra byte silently dropped; the refusal must name the byte.
//
// Break killed: restoring the More()-based tail check (`if dec.More()`) in
// Decode makes the three stray-closer rows return a value and no error.
func TestDecodeRefusesTrailingData(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"second document", `{"a":1} {"b":2}`, "decode json: unexpected trailing data"},
		{"trailing number", `{"a":1} 2`, "decode json: unexpected trailing data"},
		{"stray object closer", `{"a":1}}`, "decode json: invalid character '}'"},
		{"stray array closer", `[1]]`, "decode json: invalid character ']'"},
		{"stray closer on its own line", "{\"a\":1}\n]", "decode json: invalid character ']'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkTrailingRefusal(t, tc.in, tc.want)
		})
	}
}

// checkTrailingRefusal asserts one input is refused, returns no partial value,
// and names want in the message.
func checkTrailingRefusal(t *testing.T, in, want string) {
	t.Helper()
	got, err := jq.Decode([]byte(in))
	if err == nil {
		t.Fatalf("Decode(%q) = %#v, want a refusal", in, got)
	}
	if got != nil {
		t.Errorf("Decode(%q) returned a partial value %#v with the refusal", in, got)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("Decode(%q) err = %q; want it to contain %q", in, err, want)
	}
}

// TestDecodeAcceptsOneDocument pins the other direction, so a decoder that
// refuses everything cannot pass the refusal rows: one document with
// surrounding whitespace decodes, and a JSON scalar is a document too.
//
// Break killed: making sealTail refuse every tail (returning errTrailingData
// unconditionally) turns every row here red.
func TestDecodeAcceptsOneDocument(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"{\"a\":1}", "  {\"a\":1}\n\t", "null", "2"} {
		if _, err := jq.Decode([]byte(in)); err != nil {
			t.Errorf("Decode(%q) refused a single document: %v", in, err)
		}
	}
}

// TestDecodeObjectShape pins the object entry the settings and store reads
// use: unknown keys pass through untouched and a non-object document refuses
// with the typed object message rather than a decode error.
//
// Break killed: negating the `if err != nil` in DecodeObject makes a malformed
// document report "want a JSON object" instead of the decode error (row 2),
// and dropping the type assertion's ok arm lets an array through (row 3).
func TestDecodeObjectShape(t *testing.T) {
	t.Parallel()
	m, err := jq.DecodeObject([]byte(`{"schema_version":"1","future_key":{"deep":[1,2]},"z":3}`))
	if err != nil {
		t.Fatalf("DecodeObject: %v", err)
	}
	if len(m) != 3 {
		t.Errorf("keys = %d (%v); want every key kept", len(m), m)
	}
	if _, ok := m["future_key"]; !ok {
		t.Error("future_key was dropped; store passthrough must keep unknown keys")
	}
	for _, tc := range []struct{ name, in, want string }{
		{"non-object", `[1,2]`, "decode json: want a JSON object"},
		{"malformed", `{"a":`, "decode json: unexpected EOF"},
		{"trailing data", `{"a":1} {"b":2}`, "decode json: unexpected trailing data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkObjectRefusal(t, tc.in, tc.want)
		})
	}
}

// checkObjectRefusal asserts DecodeObject refuses in with a message naming
// want, and returns no partial map.
func checkObjectRefusal(t *testing.T, in, want string) {
	t.Helper()
	got, err := jq.DecodeObject([]byte(in))
	if err == nil {
		t.Fatalf("DecodeObject(%q) = %v, want a refusal", in, got)
	}
	if got != nil {
		t.Errorf("DecodeObject(%q) returned a partial map %v", in, got)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("DecodeObject(%q) err = %q; want it to contain %q", in, err, want)
	}
}

// TestDecodeSlurpSequence pins the JSONL entry (kb.jsonl stores): values come
// back in stream order, an empty stream is no values rather than an error,
// and a malformed stream refuses instead of returning a partial slice.
//
// Break killed: inverting the [io.EOF] arm turns the empty stream into a
// refusal; inverting `err != nil` makes a truncated stream decode as if it had
// ended cleanly.
func TestDecodeSlurpSequence(t *testing.T) {
	t.Parallel()
	vals, err := jq.DecodeSlurp(strings.NewReader("{\"n\":1}\n{\"n\":2}\n"))
	if err != nil {
		t.Fatalf("DecodeSlurp: %v", err)
	}
	if len(vals) != 2 {
		t.Fatalf("DecodeSlurp = %d values; want 2 (%v)", len(vals), vals)
	}
	if got := jq.Str(jq.Elements(vals[0])[0]); got != "1" {
		t.Errorf("first value n = %s; want 1 (stream order)", got)
	}
	if got := jq.Str(jq.Elements(vals[1])[0]); got != "2" {
		t.Errorf("second value n = %s; want 2 (stream order)", got)
	}
	empty, err := jq.DecodeSlurp(strings.NewReader(""))
	if err != nil || len(empty) != 0 {
		t.Errorf("DecodeSlurp(\"\") = %v, %v; want no values and no error", empty, err)
	}
}

// TestDecodeSlurpRefusesPartialOutput pins the failure arm: a malformed tail
// refuses instead of returning the values read so far.
//
// Break killed: inverting the `err != nil` check in DecodeSlurp's loop hands
// back the one decoded value with no error.
func TestDecodeSlurpRefusesPartialOutput(t *testing.T) {
	t.Parallel()
	got, err := jq.DecodeSlurp(strings.NewReader("{\"n\":1} x"))
	if err == nil {
		t.Fatalf("DecodeSlurp(truncated) = %v, want a refusal", got)
	}
	if got != nil {
		t.Errorf("DecodeSlurp(truncated) returned a partial slice %v", got)
	}
}

// TestDecodeOrderedDuplicateKeys pins the duplicate-key rule both decoders
// inherit from jq: the last value wins and keeps the first position. Oracle,
// jq 1.8.2:
//
//	echo '{"b":1,"a":2,"b":3}' | jq -c .
//	{"b":3,"a":2}
//
// Break killed: negating the `_, ok := o.Fields[key]` guard in Obj.Set appends
// the repeat to Keys, so the rendering grows a second "b" and both the order
// assertion and the compact comparison fail.
func TestDecodeOrderedDuplicateKeys(t *testing.T) {
	t.Parallel()
	doc, err := jq.DecodeOrdered([]byte(`{"b":1,"a":2,"b":3}`))
	if err != nil {
		t.Fatalf("DecodeOrdered: %v", err)
	}
	o, ok := doc.(*jq.Obj)
	if !ok {
		t.Fatalf("DecodeOrdered root = %T; want *Obj", doc)
	}
	if len(o.Keys) != 2 || o.Keys[0] != "b" || o.Keys[1] != "a" {
		t.Errorf("Keys = %v; want [b a] (first position, last value)", o.Keys)
	}
	if got := o.MarshalCompact(); got != `{"b":3,"a":2}` {
		t.Errorf("MarshalCompact() = %s; want {\"b\":3,\"a\":2} (jq's last-wins)", got)
	}
}
