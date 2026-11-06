package schemacheck_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/schemacheck"
)

// decode decodes one JSON literal with number fidelity.
func decode(t *testing.T, raw string) any {
	t.Helper()
	value, err := schemacheck.Decode([]byte(raw))
	if err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return value
}

// wantFailure asserts the failures name one substring; an empty want requires
// a valid document.
func wantFailure(t *testing.T, failures []schemacheck.Violation, want string) {
	t.Helper()
	if want == "" {
		if len(failures) != 0 {
			t.Errorf("failures = %v; want none", failures)
		}
		return
	}
	if len(failures) == 0 {
		t.Fatalf("no failures; want one naming %q", want)
	}
	rendered := make([]string, 0, len(failures))
	for _, failure := range failures {
		rendered = append(rendered, failure.String())
	}
	if !strings.Contains(strings.Join(rendered, "\n"), want) {
		t.Errorf("failures = %v; want one naming %q", rendered, want)
	}
}

// TestKeywordsEnforcesSubset pins every keyword this checker owns, so a
// document that breaks the contract cannot pass the conformance path and the
// shipped script at the same time.
func TestKeywordsEnforcesSubset(t *testing.T) {
	doc := decode(t, `{"$defs": {"scalar": {"type": "string"}}}`)
	cases := []struct {
		name   string
		schema string
		value  string
		want   string
	}{
		{name: "minimum violated", schema: `{"type":"number","minimum":0}`, value: `-1`, want: "below minimum"},
		{name: "minimum respected", schema: `{"type":"number","minimum":0}`, value: `0`},
		{name: "maximum violated", schema: `{"type":"integer","maximum":5}`, value: `6`, want: "above maximum"},
		{name: "exclusiveMinimum violated at bound", schema: `{"type":"number","exclusiveMinimum":0}`, value: `0`, want: "not above exclusiveMinimum"},
		{name: "exclusiveMinimum violated below", schema: `{"type":"number","exclusiveMinimum":0}`, value: `-0.5`, want: "not above exclusiveMinimum"},
		{name: "exclusiveMinimum respected", schema: `{"type":"number","exclusiveMinimum":0}`, value: `0.5`},
		{name: "minItems violated", schema: `{"type":"array","minItems":1}`, value: `[]`, want: "below minItems"},
		{name: "minItems respected", schema: `{"type":"array","minItems":1}`, value: `["a"]`},
		{name: "date-time violated", schema: `{"type":"string","format":"date-time"}`, value: `"not-a-date"`, want: "not a date-time"},
		{name: "date-time respected", schema: `{"type":"string","format":"date-time"}`, value: `"2026-11-02T12:00:00Z"`},
		{name: "date-time offset accepted", schema: `{"type":"string","format":"date-time"}`, value: `"2026-11-02T12:00:00+02:00"`},
		{name: "unknown format fails loudly", schema: `{"type":"string","format":"email"}`, value: `"a@b"`, want: "unsupported format"},
		{name: "minLength violated", schema: `{"type":"string","minLength":3}`, value: `"ab"`, want: "shorter than minLength"},
		{name: "minLength counts code points", schema: `{"type":"string","minLength":2}`, value: `"✅✅"`},
		{name: "maxLength violated", schema: `{"type":"string","maxLength":1}`, value: `"ab"`, want: "longer than maxLength"},
		{name: "pattern violated", schema: `{"type":"string","pattern":"^[a-f]+$"}`, value: `"gg"`, want: "does not match pattern"},
		{name: "pattern respected", schema: `{"type":"string","pattern":"^[a-f]+$"}`, value: `"feed"`},
		{name: "pattern must compile", schema: `{"type":"string","pattern":"("}`, value: `"x"`, want: "invalid pattern"},
		{name: "type violated", schema: `{"type":"integer"}`, value: `"1"`, want: `want type "integer"`},
		{name: "integer rejects a fraction", schema: `{"type":"integer"}`, value: `1.5`, want: "want type"},
		{name: "integer accepted", schema: `{"type":"integer"}`, value: `1`},
		{name: "type array accepted", schema: `{"type":["integer","null"]}`, value: `null`},
		{name: "const violated", schema: `{"const":"1"}`, value: `"2"`, want: "is not the const"},
		{name: "enum violated", schema: `{"enum":["open","closed"]}`, value: `"maybe"`, want: "not one of"},
		{name: "enum respected", schema: `{"enum":["open","closed"]}`, value: `"open"`},
		{name: "required missing", schema: `{"type":"object","required":["body","head"]}`, value: `{}`, want: "missing required key(s): body head"},
		{name: "required needs an object", schema: `{"required":["body"]}`, value: `"text"`, want: "not an object"},
		{
			name:   "additionalProperties false",
			schema: `{"type":"object","properties":{"a":{}},"additionalProperties":false}`,
			value:  `{"a":1,"ninth_key":2}`,
			want:   "additional properties not allowed: ninth_key",
		},
		{
			name:   "additionalProperties schema",
			schema: `{"type":"object","properties":{"a":{}},"additionalProperties":{"type":"string"}}`,
			value:  `{"b":2}`,
			want:   "$.b: want type",
		},
		{
			name:   "additionalProperties schema respected",
			schema: `{"type":"object","properties":{"a":{}},"additionalProperties":{"type":"string"}}`,
			value:  `{"b":"text"}`,
		},
		{
			name:   "nested property path",
			schema: `{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"string"}}}}}`,
			value:  `{"a":{"b":1}}`,
			want:   "$.a.b: want type",
		},
		{
			name:   "items violated",
			schema: `{"type":"array","items":{"type":"string"}}`,
			value:  `["a",2]`,
			want:   "$[1]: want type",
		},
		{name: "items tuple form fails loudly", schema: `{"items":[{"type":"string"}]}`, value: `["a"]`, want: "unsupported items form"},
		{name: "anyOf matched", schema: `{"anyOf":[{"type":"null"},{"type":"string"}]}`, value: `"x"`},
		{name: "anyOf unmatched", schema: `{"anyOf":[{"type":"null"},{"type":"string"}]}`, value: `1`, want: "matches no anyOf branch"},
		{name: "ref resolved", schema: `{"$ref":"#/$defs/scalar"}`, value: `"anything"`},
		{name: "ref violated", schema: `{"$ref":"#/$defs/scalar"}`, value: `1`, want: "want type \"string\""},
		{name: "dangling ref", schema: `{"$ref":"#/$defs/missing"}`, value: `"x"`, want: "unresolvable $ref"},
		{name: "foreign ref", schema: `{"$ref":"other.json#/$defs/x"}`, value: `"x"`, want: "unresolvable $ref"},
		{name: "unsupported keyword", schema: `{"multipleOf":2}`, value: `1`, want: `unsupported keyword(s) for this checker: multipleOf`},
		{name: "annotation keywords ignored", schema: `{"title":"t","$comment":"c","examples":[1]}`, value: `1`},
		{name: "ref siblings stay in force", schema: `{"$ref":"#/$defs/scalar","minLength":3}`, value: `"ab"`, want: "shorter than minLength"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failures := schemacheck.Validate(doc, decode(t, tc.schema), decode(t, tc.value))
			wantFailure(t, failures, tc.want)
		})
	}
}

// TestFalseSchemaRejects proves a boolean false schema is a rejection, not an
// accept.
func TestFalseSchemaRejects(t *testing.T) {
	failures := schemacheck.Validate(nil, false, decode(t, `1`))
	wantFailure(t, failures, "false schema rejects every value")
}

// TestLoadFragmentSelectsRoot proves the #/pointer fragment selects a named
// subschema, the shape the eval uses for each verb's data profile.
func TestLoadFragmentSelectsRoot(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "envelope.json", `{
		"type": "object",
		"properties": {"data": {"$ref": "#/$defs/data"}},
		"$defs": {"data": {"type": "object", "required": ["verb"]}}
	}`)
	checker, err := schemacheck.Load(filepath.Join(dir, "envelope.json"), "#/$defs/data")
	if err != nil {
		t.Fatalf("load fragment: %v", err)
	}
	wantFailure(t, checker.Check(decode(t, `{"verb":"report"}`)), "")
	wantFailure(t, checker.Check(decode(t, `{"data":{}}`)), "missing required key(s): verb")

	if _, err := schemacheck.Load(filepath.Join(dir, "envelope.json"), "#/$defs/missing"); err == nil {
		t.Error("a dangling fragment loads; want an error")
	}
}

// TestCrossFileRef proves a sibling-file $ref resolves against the file that
// carries it, and that a nested ref inside that file stays relative to it.
func TestCrossFileRef(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "common/defs.json", `{
		"$defs": {
			"scalar": {"type": "string"},
			"nested": {"$ref": "#/$defs/scalar"}
		}
	}`)
	writeSchema(t, dir, "root.json", `{
		"type": "object",
		"properties": {"a": {"$ref": "common/defs.json#/$defs/scalar"}},
		"additionalProperties": {"$ref": "common/defs.json#/$defs/nested"}
	}`)
	checker, err := schemacheck.Load(filepath.Join(dir, "root.json"), "")
	if err != nil {
		t.Fatalf("load root: %v", err)
	}
	wantFailure(t, checker.Check(decode(t, `{"a":"x","b":"y"}`)), "")
	wantFailure(t, checker.Check(decode(t, `{"a":1}`)), "want type")
	wantFailure(t, checker.Check(decode(t, `{"b":2}`)), "want type")
}

// TestPointerEscapes proves the ~0/~1 pointer escapes resolve, so a schema
// definition whose name contains a slash is reachable.
func TestPointerEscapes(t *testing.T) {
	doc := decode(t, `{"$defs": {"a/b": {"type": "string"}}}`)
	failures := schemacheck.Validate(doc, decode(t, `{"$ref":"#/$defs/a~1b"}`), decode(t, `1`))
	wantFailure(t, failures, `want type "string"`)
}

// TestDecodeKeepsNumbers proves the checker sees integers as integers and
// keeps big values exact.
func TestDecodeKeepsNumbers(t *testing.T) {
	value, err := schemacheck.Decode([]byte(`{"n": 9007199254740993}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("decode = %T; want an object", value)
	}
	number, ok := object["n"].(json.Number)
	if !ok || number.String() != "9007199254740993" {
		t.Errorf("n = %v (%T); want the exact json.Number", object["n"], object["n"])
	}
	failures := schemacheck.Validate(nil, decode(t, `{"type":"integer"}`), object["n"])
	wantFailure(t, failures, "")
	if _, err := schemacheck.Decode([]byte(`{"a":1} {"b":2}`)); err == nil {
		t.Error("trailing data decodes; want an error")
	}
}

// TestUnsupportedKeywordFailsLoudly proves the subset is a contract: a schema
// that outgrows it breaks instead of passing silently.
func TestUnsupportedKeywordFailsLoudly(t *testing.T) {
	failures := schemacheck.Validate(nil, decode(t, `{"allOf":[{"type":"string"}]}`), decode(t, `"x"`))
	wantFailure(t, failures, "unsupported keyword(s) for this checker: allOf")
}

// writeSchema writes one schema file under dir.
func writeSchema(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
