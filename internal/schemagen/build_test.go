package schemagen

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// tagModel exercises every mapping convention the generator supports.
type tagModel struct { //nolint:govet // field order is the expected property order
	Required         string            `json:"required"`
	Optional         string            `json:"optional,omitempty"`
	Nullable         *string           `json:"nullable"`
	OptionalNullable *string           `json:"optional_nullable,omitempty"`
	Enum             string            `enum:"a|b|null"                    json:"enum"`
	Const            string            `const:"1"                          json:"const"`
	Bounded          int               `json:"bounded"                     max:"9"                    min:"0"`
	When             string            `format:"date-time"                 json:"when"`
	List             []string          `json:"list"`
	OpenList         []any             `json:"open_list"`
	Ref              tagRef            `json:"ref"`
	Inline           tagInline         `inline:"true"                      json:"inline"`
	OpenMap          map[string]any    `json:"open_map"`
	StringMap        map[string]string `json:"string_map"`
	Anything         any               `json:"anything"`
	Union            any               `json:"union"                       type:"integer|string|null"`
	Nested           []map[string]any  `json:"nested"`
}

// tagRef is a registered $defs target.
type tagRef struct {
	A string `json:"a"`
}

// tagInline is a named struct forced to inline.
type tagInline struct {
	B []string `json:"b"`
}

// buildTagModel renders tagModel as a parsed JSON object.
func buildTagModel(t *testing.T) map[string]any {
	t.Helper()
	b := newBuilder([]definition{{typ: reflect.TypeFor[tagRef](), name: "tagged"}})
	root, err := b.structNode(reflect.TypeFor[tagModel](), true)
	if err != nil {
		t.Fatalf("structNode: %v", err)
	}
	parsed := map[string]any{}
	if err := json.Unmarshal(printDocument(root), &parsed); err != nil {
		t.Fatalf("generated JSON does not parse: %v", err)
	}
	return parsed
}

// objectAt walks a parsed object by key and fails when absent.
func objectAt(t *testing.T, obj map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := obj[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want object", key, obj[key])
	}
	return value
}

// TestTypeMapping pins the reflection conventions through parsed JSON.
func TestTypeMapping(t *testing.T) {
	root := buildTagModel(t)
	props := objectAt(t, root, "properties")
	t.Run("required", func(t *testing.T) { assertRequired(t, root) })
	t.Run("nullability", func(t *testing.T) { assertNullability(t, props) })
	t.Run("enums and consts", func(t *testing.T) { assertEnums(t, props) })
	t.Run("bounds and format", func(t *testing.T) { assertBounds(t, props) })
	t.Run("arrays", func(t *testing.T) { assertArrays(t, props) })
	t.Run("refs and inline", func(t *testing.T) { assertRefs(t, props) })
	t.Run("open values", func(t *testing.T) { assertOpen(t, props) })
	t.Run("open properties", func(t *testing.T) {
		if root["additionalProperties"] != true {
			t.Errorf("additionalProperties = %v, want true", root["additionalProperties"])
		}
	})
}

// assertRequired checks omitempty drives requiredness, in field order.
func assertRequired(t *testing.T, root map[string]any) {
	t.Helper()
	required, ok := root["required"].([]any)
	if !ok {
		t.Fatalf("required is %T, want array", root["required"])
	}
	want := []string{"required", "nullable", "enum", "const", "bounded", "when", "list", "open_list", "ref", "inline", "open_map", "string_map", "anything", "union", "nested"}
	if len(required) != len(want) {
		t.Fatalf("required = %v, want %v", required, want)
	}
	for i, name := range want {
		if required[i] != name {
			t.Fatalf("required[%d] = %v, want %s", i, required[i], name)
		}
	}
}

// assertNullability checks pointer fields widen the type with null.
func assertNullability(t *testing.T, props map[string]any) {
	t.Helper()
	if got := objectAt(t, props, "nullable")["type"]; !reflect.DeepEqual(got, []any{"string", "null"}) {
		t.Errorf("nullable.type = %v", got)
	}
	if got := objectAt(t, props, "optional")["type"]; got != "string" {
		t.Errorf("optional.type = %v, want string", got)
	}
	if got := objectAt(t, props, "optional_nullable")["type"]; !reflect.DeepEqual(got, []any{"string", "null"}) {
		t.Errorf("optional_nullable.type = %v", got)
	}
}

// assertEnums checks enum/const tags emit no type keyword.
func assertEnums(t *testing.T, props map[string]any) {
	t.Helper()
	enum := objectAt(t, props, "enum")
	if got := enum["enum"]; !reflect.DeepEqual(got, []any{"a", "b", nil}) {
		t.Errorf("enum.enum = %v", got)
	}
	if _, ok := enum["type"]; ok {
		t.Errorf("enum carries a type; published enums do not")
	}
	constant := objectAt(t, props, "const")
	if got := constant["const"]; got != "1" {
		t.Errorf("const.const = %v", got)
	}
	if _, ok := constant["type"]; ok {
		t.Errorf("const carries a type; published consts do not")
	}
}

// assertBounds checks integer bounds and date-time formats.
func assertBounds(t *testing.T, props map[string]any) {
	t.Helper()
	bounded := objectAt(t, props, "bounded")
	if bounded["type"] != "integer" || bounded["minimum"] != float64(0) || bounded["maximum"] != float64(9) {
		t.Errorf("bounded = %v", bounded)
	}
	when := objectAt(t, props, "when")
	if when["type"] != "string" || when["format"] != "date-time" {
		t.Errorf("when = %v", when)
	}
}

// assertArrays checks slice items and the []any no-items form.
func assertArrays(t *testing.T, props map[string]any) {
	t.Helper()
	list := objectAt(t, props, "list")
	if list["type"] != "array" || objectAt(t, list, "items")["type"] != "string" {
		t.Errorf("list = %v", list)
	}
	openList := objectAt(t, props, "open_list")
	if openList["type"] != "array" {
		t.Errorf("open_list = %v", openList)
	}
	if _, ok := openList["items"]; ok {
		t.Errorf("[]any gained an items schema")
	}
	nested := objectAt(t, props, "nested")
	if nested["type"] != "array" || objectAt(t, nested, "items")["type"] != "object" {
		t.Errorf("nested = %v", nested)
	}
}

// assertRefs checks $refs and forced-inline structs.
func assertRefs(t *testing.T, props map[string]any) {
	t.Helper()
	ref := objectAt(t, props, "ref")
	if ref["$ref"] != "#/$defs/tagged" {
		t.Errorf("ref = %v", ref)
	}
	inline := objectAt(t, props, "inline")
	if inline["type"] != "object" {
		t.Errorf("inline = %v", inline)
	}
	if _, ok := inline["$ref"]; ok {
		t.Errorf("inline struct became a $ref")
	}
	if got := objectAt(t, inline, "properties")["b"]; got == nil {
		t.Errorf("inline properties lost b")
	}
}

// assertOpen checks open maps, empty schemas and explicit type unions.
func assertOpen(t *testing.T, props map[string]any) {
	t.Helper()
	openMap := objectAt(t, props, "open_map")
	if openMap["type"] != "object" || openMap["additionalProperties"] != true {
		t.Errorf("open_map = %v", openMap)
	}
	stringMap := objectAt(t, props, "string_map")
	if stringMap["type"] != "object" || !reflect.DeepEqual(stringMap["additionalProperties"], map[string]any{"type": "string"}) {
		t.Errorf("string_map = %v", stringMap)
	}
	anything := objectAt(t, props, "anything")
	if len(anything) != 0 {
		t.Errorf("anything = %v, want {}", anything)
	}
	union := objectAt(t, props, "union")
	if got := union["type"]; !reflect.DeepEqual(got, []any{"integer", "string", "null"}) {
		t.Errorf("union.type = %v", got)
	}
}

// TestGenerateDeterministic checks byte stability across runs.
func TestGenerateDeterministic(t *testing.T) {
	first, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	second, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("generated %d files, then %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Name != second[i].Name || !bytes.Equal(first[i].Data, second[i].Data) {
			t.Fatalf("%s is not deterministic", first[i].Name)
		}
	}
}

// TestDocumentsCarryStableIdentity pins the header fields and every name.
func TestDocumentsCarryStableIdentity(t *testing.T) {
	files, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(files) != 12 {
		t.Fatalf("generated %d schemas, want 12", len(files))
	}
	seen := map[string]bool{}
	for _, file := range files {
		seen[file.Name] = true
		assertIdentity(t, file)
	}
	for _, name := range []string{"config.schema.json", "corpus.schema.json", "cost.schema.json", "envelope.schema.json", "findings.schema.json", "identity.schema.json", "post.schema.json", "record.schema.json", "review.schema.json", "threads.schema.json", "usage.schema.json"} {
		if !seen[name] {
			t.Errorf("missing generated schema %s", name)
		}
	}
}

// assertIdentity checks one generated document's stable header fields.
func assertIdentity(t *testing.T, file File) {
	t.Helper()
	parsed := map[string]any{}
	if err := json.Unmarshal(file.Data, &parsed); err != nil {
		t.Fatalf("%s does not parse: %v", file.Name, err)
	}
	if got := parsed["$schema"]; got != draft2020 {
		t.Errorf("%s $schema = %v", file.Name, got)
	}
	wantID := idBase + file.Name
	if got := parsed["$id"]; got != wantID {
		t.Errorf("%s $id = %v, want %s", file.Name, got, wantID)
	}
	if _, ok := parsed["title"].(string); !ok {
		t.Errorf("%s has no title", file.Name)
	}
}

// TestDiffFiles pins the drift-test failure message shape.
func TestDiffFiles(t *testing.T) {
	diff := DiffFiles("x.schema.json", []byte("{\n  \"a\": 1\n}\n"), []byte("{\n  \"a\": 2\n}\n"))
	for _, want := range []string{"--- schemas/x.schema.json (committed)", "+++ schemas/x.schema.json (generated)", "-  \"a\": 1", "+  \"a\": 2"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff misses %q:\n%s", want, diff)
		}
	}
}
