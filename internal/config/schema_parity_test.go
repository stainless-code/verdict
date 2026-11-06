package config

import (
	"testing"

	"github.com/stainless-code/verdict/internal/schemacheck"
	"github.com/stainless-code/verdict/internal/schemagen"
)

// paritySchema returns the generated config schema decoded for the
// in-memory checker: the schema under test is the generator output, so
// this test fails when types, schema, and runtime drift apart.
func paritySchema(t *testing.T) any {
	t.Helper()
	files, err := schemagen.Generate()
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
		if _, ref := Parse([]byte(doc)); ref != nil {
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
		{"defaults null", `{"schema_version":"1","defaults":null}`, CodeBadConfig, "defaults"},
		{"chunk null", `{"schema_version":"1","defaults":{"chunk":null}}`, CodeBadConfig, "chunk"},
		{"nested typo", `{"schema_version":"1","defaults":{"bogus":1}}`, CodeBadConfig, "bogus"},
		{"profile non-object", `{"schema_version":"1","profiles":{"x":42}}`, CodeBadConfig, "profiles"},
		{"profile extra key", `{"schema_version":"1","profiles":{"p":{"bogus":1}}}`, CodeBadConfig, "bogus"},
		{"clause extra key", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"critical","bogus":1}]}}`, CodeBadConfig, "bogus"},
		{"wrong check enum", `{"schema_version":"1","requirements":{"require_checks":["nope"]}}`, CodeBadRequirement, "require_checks"},
		{"wrong profile check", `{"schema_version":"1","profiles":{"p":{"checks":["nope"]}}}`, CodeBadConfig, "checks"},
		{"wrong category enum", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"critical","categories":["ux"]}]}}`, CodeBadRequirement, "categories"},
		{"empty block clauses", `{"schema_version":"1","requirements":{"block_clauses":[]}}`, CodeBadRequirement, "block_clauses"},
		{"empty include", `{"schema_version":"1","scopes":[{"name":"s","include":[],"requirements":{}}]}`, CodeBadScope, "include"},
		{"empty require checks", `{"schema_version":"1","requirements":{"require_checks":[]}}`, CodeBadRequirement, "require_checks"},
		{"empty panel", `{"schema_version":"1","defaults":{"panel":[]}}`, CodeBadConfig, "panel"},
		{"budget zero", `{"schema_version":"1","defaults":{"budget_usd":0}}`, CodeBadConfig, "budget_usd"},
		{"budget negative", `{"schema_version":"1","defaults":{"budget_usd":-1}}`, CodeBadConfig, "budget_usd"},
		{"budget string", `{"schema_version":"1","defaults":{"budget_usd":"5"}}`, CodeBadConfig, "budget_usd"},
		{"max bytes zero", `{"schema_version":"1","defaults":{"max_bytes":0}}`, CodeBadConfig, "max_bytes"},
		{"max lenses negative", `{"schema_version":"1","profiles":{"p":{"max_lenses":-1}}}`, CodeBadConfig, "max_lenses"},
		{"threshold wrong", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"blocker"}]}}`, CodeBadRequirement, "threshold"},
		{"effort wrong", `{"schema_version":"1","defaults":{"effort":"turbo"}}`, CodeBadConfig, "effort"},
		{"depth wrong", `{"schema_version":"1","profiles":{"p":{"depth":"turbo"}}}`, CodeBadConfig, "depth"},
		{"default profile non-string", `{"schema_version":"1","default_profile":42}`, CodeBadConfig, "default_profile"},
		{"scope profile non-string", `{"schema_version":"1","scopes":[{"name":"s","include":["a/**"],"profile":42}]}`, CodeBadScope, "profile"},
		{"kb rate high", `{"schema_version":"1","defaults":{"kb_policy":{"max_fp_rate":2}}}`, CodeBadConfig, "max_fp_rate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if violations := parityViolations(t, schema, tc.give); len(violations) == 0 {
				t.Errorf("schema accepts %s", tc.give)
			}
			checkParseRefusal(t, tc.give, tc.code, tc.fragment)
		})
	}
}

// TestSchemaSemanticOnly pins the rules that stay runtime-specific: the
// schema accepts these documents (JSON Schema cannot express them) while
// the parser refuses with the typed code. Duplicate keys belong here too:
// JSON objects collapse them before validation, so the parser's
// duplicate-key refusal has no schema counterpart by construction.
func TestSchemaSemanticOnly(t *testing.T) {
	schema := paritySchema(t)
	tests := []struct {
		name     string
		give     string
		code     string
		fragment string
	}{
		{"scope needs inputs", `{"schema_version":"1","scopes":[{"name":"s","include":["a/**"]}]}`, CodeBadScope, "one of"},
		{"profile plus defaults", `{"schema_version":"1","profiles":{"p":{}},"scopes":[{"name":"s","include":["a/**"],"profile":"p","defaults":{"chunk":true}}]}`, CodeBadScope, "exclusive"},
		{"dangling scope profile", `{"schema_version":"1","scopes":[{"name":"s","include":["a/**"],"profile":"ghost"}]}`, CodeBadScope, "ghost"},
		{"dangling default profile", `{"schema_version":"1","default_profile":"ghost"}`, CodeBadConfig, "ghost"},
		{"duplicate clause names", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"critical"},{"name":"c","threshold":"important"}]}}`, CodeBadRequirement, "c"},
		{"absolute glob", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"critical","paths":["/etc"]}]}}`, CodeBadRequirement, "paths"},
		{"glob escape", `{"schema_version":"1","scopes":[{"name":"s","include":["../evil"],"requirements":{}}]}`, CodeBadScope, "include"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if violations := parityViolations(t, schema, tc.give); len(violations) != 0 {
				t.Errorf("schema refuses semantic-only %s: %v", tc.give, violations)
			}
			checkParseRefusal(t, tc.give, tc.code, tc.fragment)
		})
	}
}
