package schemagen

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/recipe"
	"github.com/stainless-code/verdict/internal/schemacheck"
)

// recipeSchema returns the generated recipes schema decoded for the in-memory
// checker: the schema under test is the generator output, so this test fails
// when the published contract and the parser drift apart.
func recipeSchema(t *testing.T) any {
	t.Helper()
	files, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, file := range files {
		if file.Name != "recipes.schema.json" {
			continue
		}
		schema, err := schemacheck.Decode(file.Data)
		if err != nil {
			t.Fatalf("decode generated recipes schema: %v", err)
		}
		return schema
	}
	t.Fatal("recipes.schema.json not generated")
	return nil
}

// recipeDocument wraps one entry body in the document envelope.
func recipeDocument(body string) string {
	return `{"schema_version":"1","recipes":{"quick":` + body + `}}`
}

// TestSchemaAndParserAgree is the published contract: one table of documents,
// and every row is accepted by the published schema and by recipe.Parse, or by
// neither. Each row also pins the expected side, so a rule both sides drop
// together still fails here.
//
// JSON-syntax faults stay parser-only by construction: a decoded document has
// already collapsed a duplicate key, cut trailing data, and rejected malformed
// bytes, so TestParseRefusals is where those live.
func TestSchemaAndParserAgree(t *testing.T) {
	schema := recipeSchema(t)
	tests := []struct {
		name  string
		give  string
		valid bool
	}{
		{"minimal entry", recipeDocument(`{"version":"1.0.0","synthesize":{}}`), true},
		{"full entry", recipeDocument(`{"version":"1.2.3","description":"Cheap pass","budget_usd":1.2,` +
			`"lenses":[{"id":"logic","model":"sonnet","max_files":8,"max_findings":6}],` +
			`"synthesize":{"model":"opus"}}`), true},
		{"empty recipe map", `{"schema_version":"1","recipes":{}}`, true},
		{"empty description", recipeDocument(`{"version":"1.0.0","description":"","synthesize":{}}`), true},
		{"280-character description", recipeDocument(`{"version":"1.0.0","description":"` +
			strings.Repeat("x", 280) + `","synthesize":{}}`), true},
		{"281-character description", recipeDocument(`{"version":"1.0.0","description":"` +
			strings.Repeat("x", 281) + `","synthesize":{}}`), false},
		{"integral cap with a zero fraction", recipeDocument(
			`{"version":"1.0.0","lenses":[{"id":"logic","max_files":24.0}],"synthesize":{}}`), true},
		{"integral cap as an exponent", recipeDocument(
			`{"version":"1.0.0","lenses":[{"id":"logic","max_files":1e1}],"synthesize":{}}`), true},
		{"fractional cap", recipeDocument(
			`{"version":"1.0.0","lenses":[{"id":"logic","max_files":1.5}],"synthesize":{}}`), false},
		{"cap above the range", recipeDocument(
			`{"version":"1.0.0","lenses":[{"id":"logic","max_files":25}],"synthesize":{}}`), false},
		{"minimum-length recipe key", `{"schema_version":"1","recipes":{"q":` +
			`{"version":"1.0.0","synthesize":{}}}}`, true},
		{"64-character recipe key", `{"schema_version":"1","recipes":{"` + strings.Repeat("r", 64) +
			`":{"version":"1.0.0","synthesize":{}}}}`, true},
		{"65-character recipe key", `{"schema_version":"1","recipes":{"` + strings.Repeat("r", 65) +
			`":{"version":"1.0.0","synthesize":{}}}}`, false},
		{"uppercase recipe key", `{"schema_version":"1","recipes":{"Quick":` +
			`{"version":"1.0.0","synthesize":{}}}}`, false},
		{"recipe key with a dot", `{"schema_version":"1","recipes":{"a.b":` +
			`{"version":"1.0.0","synthesize":{}}}}`, false},
		{"unknown top key", `{"schema_version":"1","recipes":{},"bogus":1}`, false},
		{"missing recipes", `{"schema_version":"1"}`, false},
		{"wrong schema_version", `{"schema_version":"2","recipes":{}}`, false},
		{"missing synthesize", recipeDocument(`{"version":"1.0.0"}`), false},
		{"unknown entry key", recipeDocument(`{"version":"1.0.0","synthesize":{},"nope":1}`), false},
		{"version not semver", recipeDocument(`{"version":"1.0","synthesize":{}}`), false},
		{"budget zero", recipeDocument(`{"version":"1.0.0","budget_usd":0,"synthesize":{}}`), false},
		{"empty lenses", recipeDocument(`{"version":"1.0.0","lenses":[],"synthesize":{}}`), false},
		{"lens id not a slug", recipeDocument(
			`{"version":"1.0.0","lenses":[{"id":"Logic"}],"synthesize":{}}`), false},
		{"model hint with whitespace", recipeDocument(
			`{"version":"1.0.0","synthesize":{"model":"op us"}}`), false},
		{"65-character model hint", recipeDocument(`{"version":"1.0.0","synthesize":{"model":"` +
			strings.Repeat("m", 65) + `"}}`), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			value, err := schemacheck.Decode([]byte(tc.give))
			if err != nil {
				t.Fatalf("candidate is not JSON: %v", err)
			}
			assertSide(t, "schema", len(schemacheck.Validate(schema, schema, value)) == 0, tc.valid, tc.give)
			_, ref := recipe.Parse([]byte(tc.give))
			assertSide(t, "parser", ref == nil, tc.valid, tc.give)
		})
	}
}

// assertSide fails one side of the contract when its verdict is not the pinned
// one, naming the document so a drift reads as a contract change.
func assertSide(t *testing.T, side string, got, want bool, doc string) {
	t.Helper()
	if got == want {
		return
	}
	t.Errorf("%s verdict on %s = %t, want %t", side, doc, got, want)
}
