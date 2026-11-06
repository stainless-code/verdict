package schemagen

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/schemacheck"
)

// paritySchema returns the generated config schema decoded for the
// in-memory checker: the schema under test is the generator output, so
// this test fails when types, schema, and runtime drift apart.
func paritySchema(t *testing.T) any {
	t.Helper()
	files, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, file := range files {
		if file.Name != "config.schema.json" {
			continue
		}
		schema, err := schemacheck.Decode(file.Data)
		if err != nil {
			t.Fatalf("decode generated schema: %v", err)
		}
		return schema
	}
	t.Fatal("config.schema.json not generated")
	return nil
}

// parityViolations validates one candidate document against the generated
// schema, failing the test when the document is not JSON.
func parityViolations(t *testing.T, schema any, doc string) []schemacheck.Violation {
	t.Helper()
	value, err := schemacheck.Decode([]byte(doc))
	if err != nil {
		t.Fatalf("candidate is not JSON: %v", err)
	}
	return schemacheck.Validate(schema, schema, value)
}

// TestSchemaParityValid pins documents both sides accept, including
// omitted optionals: absent keys take defaults and never refuse.
func TestSchemaParityValid(t *testing.T) {
	schema := paritySchema(t)
	docs := []string{
		`{"schema_version":"1"}`,
		`{"$schema":"https://example.invalid/c.json","schema_version":"1"}`,
		`{"schema_version":"1","defaults":{"budget_usd":5.0}}`,
		`{"schema_version":"1","defaults":{},"requirements":{},"scopes":[],"profiles":{}}`,
		`{"schema_version":"1","profiles":{"deep-review":{"depth":"deep","checks":["no-open-above-block"],"max_lenses":4}},"default_profile":"deep-review"}`,
		`{"schema_version":"1","scopes":[{"name":"req-only","include":["payments/**"],"requirements":{"require_bind":true}}]}`,
	}
	for _, doc := range docs {
		if violations := parityViolations(t, schema, doc); len(violations) != 0 {
			t.Errorf("schema rejects %s: %v", doc, violations)
		}
		if _, ref := config.Parse([]byte(doc)); ref != nil {
			t.Errorf("Parse rejects %s: %v", doc, ref)
		}
	}
}

// TestSchemaParityStructural pins the structural contract both sides
// enforce: null-widened optionals, unknown fields (including inside
// closed $defs and typed profile values), wrong enums, empty required
// arrays, and out-of-range scalars refuse on both sides.
func TestSchemaParityStructural(t *testing.T) {
	schema := paritySchema(t)
	tests := []struct {
		name     string
		give     string
		code     string
		fragment string
	}{
		{"defaults null", `{"schema_version":"1","defaults":null}`, config.CodeBadConfig, "defaults"},
		{"chunk null", `{"schema_version":"1","defaults":{"chunk":null}}`, config.CodeBadConfig, "chunk"},
		{"nested typo", `{"schema_version":"1","defaults":{"bogus":1}}`, config.CodeBadConfig, "bogus"},
		{"profile non-object", `{"schema_version":"1","profiles":{"x":42}}`, config.CodeBadConfig, "profiles"},
		{"profile extra key", `{"schema_version":"1","profiles":{"p":{"bogus":1}}}`, config.CodeBadConfig, "bogus"},
		{"clause extra key", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"critical","bogus":1}]}}`, config.CodeBadConfig, "bogus"},
		{"wrong check enum", `{"schema_version":"1","requirements":{"require_checks":["nope"]}}`, config.CodeBadRequirement, "require_checks"},
		{"wrong profile check", `{"schema_version":"1","profiles":{"p":{"checks":["nope"]}}}`, config.CodeBadConfig, "checks"},
		{"wrong category enum", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"critical","categories":["ux"]}]}}`, config.CodeBadRequirement, "categories"},
		{"empty block clauses", `{"schema_version":"1","requirements":{"block_clauses":[]}}`, config.CodeBadRequirement, "block_clauses"},
		{"empty include", `{"schema_version":"1","scopes":[{"name":"s","include":[],"requirements":{}}]}`, config.CodeBadScope, "include"},
		{"empty require checks", `{"schema_version":"1","requirements":{"require_checks":[]}}`, config.CodeBadRequirement, "require_checks"},
		{"empty panel", `{"schema_version":"1","defaults":{"panel":[]}}`, config.CodeBadConfig, "panel"},
		{"budget zero", `{"schema_version":"1","defaults":{"budget_usd":0}}`, config.CodeBadConfig, "budget_usd"},
		{"budget negative", `{"schema_version":"1","defaults":{"budget_usd":-1}}`, config.CodeBadConfig, "budget_usd"},
		{"budget string", `{"schema_version":"1","defaults":{"budget_usd":"5"}}`, config.CodeBadConfig, "budget_usd"},
		{"max bytes zero", `{"schema_version":"1","defaults":{"max_bytes":0}}`, config.CodeBadConfig, "max_bytes"},
		{"max lenses negative", `{"schema_version":"1","profiles":{"p":{"max_lenses":-1}}}`, config.CodeBadConfig, "max_lenses"},
		{"threshold wrong", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"blocker"}]}}`, config.CodeBadRequirement, "threshold"},
		{"effort wrong", `{"schema_version":"1","defaults":{"effort":"turbo"}}`, config.CodeBadConfig, "effort"},
		{"depth wrong", `{"schema_version":"1","profiles":{"p":{"depth":"turbo"}}}`, config.CodeBadConfig, "depth"},
		{"default profile non-string", `{"schema_version":"1","default_profile":42}`, config.CodeBadConfig, "default_profile"},
		{"scope profile non-string", `{"schema_version":"1","scopes":[{"name":"s","include":["a/**"],"profile":42}]}`, config.CodeBadScope, "profile"},
		{"kb rate high", `{"schema_version":"1","defaults":{"kb_policy":{"max_fp_rate":2}}}`, config.CodeBadConfig, "max_fp_rate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if violations := parityViolations(t, schema, tc.give); len(violations) == 0 {
				t.Errorf("schema accepts %s", tc.give)
			}
			checkConfigRefusal(t, tc.give, tc.code, tc.fragment)
		})
	}
}

// TestSchemaStructuralExclusivity pins the scope presence contract on both
// sides: a scope needs at least one of profile, defaults, or requirements,
// and never combines profile with inline defaults. The generator emits the
// anyOf/not pair and the parser enforces the same rule, so these fixtures
// must fail schema validation and parser acceptance alike.
func TestSchemaStructuralExclusivity(t *testing.T) {
	schema := paritySchema(t)
	tests := []struct {
		name     string
		give     string
		code     string
		fragment string
	}{
		{"scope needs inputs", `{"schema_version":"1","scopes":[{"name":"s","include":["a/**"]}]}`, config.CodeBadScope, "one of"},
		{"profile plus defaults", `{"schema_version":"1","profiles":{"p":{}},"scopes":[{"name":"s","include":["a/**"],"profile":"p","defaults":{"chunk":true}}]}`, config.CodeBadScope, "exclusive"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if violations := parityViolations(t, schema, tc.give); len(violations) == 0 {
				t.Errorf("schema accepts %s; want the scope exclusivity refusal", tc.give)
			}
			checkConfigRefusal(t, tc.give, tc.code, tc.fragment)
		})
	}
}

// TestSchemaSemanticOnly pins the rules that stay runtime-specific: dynamic
// profile references (the vocabulary is user-defined, so no schema can
// enumerate it), duplicate keys (JSON objects collapse them before
// validation, so the parser's duplicate-key refusal has no schema
// counterpart by construction), and value portability (globs, thresholds)
// the parser reports naming the path and the rule.
func TestSchemaSemanticOnly(t *testing.T) {
	schema := paritySchema(t)
	tests := []struct {
		name     string
		give     string
		code     string
		fragment string
	}{
		{"dangling scope profile", `{"schema_version":"1","scopes":[{"name":"s","include":["a/**"],"profile":"ghost"}]}`, config.CodeBadScope, "ghost"},
		{"dangling default profile", `{"schema_version":"1","default_profile":"ghost"}`, config.CodeBadConfig, "ghost"},
		{"duplicate clause names", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"critical"},{"name":"c","threshold":"important"}]}}`, config.CodeBadRequirement, "c"},
		{"absolute glob", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"critical","paths":["/etc"]}]}}`, config.CodeBadRequirement, "paths"},
		{"glob escape", `{"schema_version":"1","scopes":[{"name":"s","include":["../evil"],"requirements":{}}]}`, config.CodeBadScope, "include"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if violations := parityViolations(t, schema, tc.give); len(violations) != 0 {
				t.Errorf("schema refuses semantic-only %s: %v", tc.give, violations)
			}
			checkConfigRefusal(t, tc.give, tc.code, tc.fragment)
		})
	}
}

// checkConfigRefusal asserts Parse refuses doc with the typed code naming
// the fragment.
func checkConfigRefusal(t *testing.T, doc, code, fragment string) {
	t.Helper()
	_, ref := config.Parse([]byte(doc))
	if ref == nil {
		t.Fatalf("Parse accepts %s; want %s", doc, code)
	}
	if ref.Code != code {
		t.Fatalf("Parse(%s) code = %q, want %q", doc, ref.Code, code)
	}
	if !strings.Contains(ref.Message, fragment) {
		t.Fatalf("Parse(%s) message = %q, want it to name %q", doc, ref.Message, fragment)
	}
}

// TestSchemaFixedStructure pins the structural obligations the generator
// must hold for the semantic contract: every fixed-shape $def is closed
// and profile values validate through the typed reference.
func TestSchemaFixedStructure(t *testing.T) {
	doc, ok := paritySchema(t).(map[string]any)
	if !ok {
		t.Fatal("generated schema is not a JSON object")
	}
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal("$defs missing from the generated config schema")
	}
	for _, name := range []string{"defaults", "kbPolicy", "requirements", "blockClause", "scope", "profile"} {
		assertClosedDef(t, defs, name)
	}
	root, ok := doc["properties"].(map[string]any)
	if !ok {
		t.Fatal("generated schema carries no root properties")
	}
	profiles, ok := root["profiles"].(map[string]any)
	if !ok {
		t.Fatal("root misses profiles")
	}
	additional, ok := profiles["additionalProperties"].(map[string]any)
	if !ok || additional["$ref"] != "#/$defs/profile" {
		t.Fatalf("profiles values do not validate through the typed profile: %v", profiles["additionalProperties"])
	}
}

// assertClosedDef reports when one $def is missing or admits undeclared
// properties.
func assertClosedDef(t *testing.T, defs map[string]any, name string) {
	t.Helper()
	def, ok := defs[name].(map[string]any)
	if !ok {
		t.Fatalf("$defs misses %q", name)
	}
	closed, ok := def["additionalProperties"].(bool)
	if !ok || closed != false {
		t.Fatalf("$defs %q is not closed: additionalProperties = %v", name, def["additionalProperties"])
	}
}

// TestSchemaNullFree pins the no-widening rule: optional defaults carry no
// null branch, so a present null refuses on both sides.
func TestSchemaNullFree(t *testing.T) {
	doc, ok := paritySchema(t).(map[string]any)
	if !ok {
		t.Fatal("generated schema is not a JSON object")
	}
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal("$defs missing from the generated config schema")
	}
	defaults, ok := defs["defaults"].(map[string]any)
	if !ok {
		t.Fatal("generated schema misses the defaults $def")
	}
	props, ok := defaults["properties"].(map[string]any)
	if !ok {
		t.Fatal("defaults $def carries no properties")
	}
	optional := []string{"panel", "budget_usd", "max_bytes", "chunk", "kb_policy", "effort", "display_min_severity", "post_min_severity"}
	for _, key := range optional {
		prop, ok := props[key].(map[string]any)
		if !ok {
			t.Fatalf("defaults misses %q", key)
		}
		if _, widened := prop["anyOf"]; widened {
			t.Fatalf("defaults %q is null-widened: a present null must refuse", key)
		}
	}
}
