package schemagen

import (
	"encoding/json"
	"fmt"
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

// recipeLensRows renders count distinct declared lens rows.
func recipeLensRows(count int) string {
	rows := make([]string, 0, count)
	for i := range count {
		rows = append(rows, fmt.Sprintf(`{"id":"lens-%d"}`, i))
	}
	return "[" + strings.Join(rows, ",") + "]"
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
		{"version at the 64-character cap", recipeDocument(`{"version":"1.0.0+` +
			strings.Repeat("b", 58) + `","synthesize":{}}`), true},
		{"65-character version", recipeDocument(`{"version":"1.0.0+` +
			strings.Repeat("b", 59) + `","synthesize":{}}`), false},
		{"lens id at the 64-character cap", recipeDocument(`{"version":"1.0.0","lenses":[{"id":"` +
			strings.Repeat("l", 64) + `"}],"synthesize":{}}`), true},
		{"65-character lens id", recipeDocument(`{"version":"1.0.0","lenses":[{"id":"` +
			strings.Repeat("l", 65) + `"}],"synthesize":{}}`), false},
		{"64 lenses", recipeDocument(`{"version":"1.0.0","lenses":` + recipeLensRows(64) +
			`,"synthesize":{}}`), true},
		{"65 lenses", recipeDocument(`{"version":"1.0.0","lenses":` + recipeLensRows(65) +
			`,"synthesize":{}}`), false},
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

// TestResolvedPlanMatchesThePublishedShape pins the other half of the recipe
// contract: what a run records. The resolved plan — marshaled from the type
// the envelope and launcher.json carry — validates against the published
// `$defs/plan`, and a plan outside that shape refuses. Without the `$defs` the
// shape would be pinned only by a Go test.
func TestResolvedPlanMatchesThePublishedShape(t *testing.T) {
	root := publishedSchema(t)
	published := publishedPlanDef(t, root)
	repo := parsedDocument(t, `{"schema_version":"1","recipes":{"quick":{"version":"1.2.3",`+
		`"budget_usd":0.5,"lenses":[{"id":"logic","model":"sonnet","max_files":8}],`+
		`"synthesize":{"model":"opus"}}}}`)
	plan, ref := recipe.Resolve(repo, "quick", []string{"logic"})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	recorded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal the resolved plan: %v", err)
	}
	if got := violationsOf(t, root, published, string(recorded)); len(got) != 0 {
		t.Fatalf("the resolved plan does not match the published shape: %v\n%s", got, recorded)
	}
	// The shape publishes the keys, not every runtime rule: which stage may
	// carry a model hint is the resolver's business (it never puts one on
	// dedupe or rank), exactly as the config schema leaves cross-field rules to
	// the parser. What it does publish is the closed key set and the enums.
	assertRefused(t, root, published, "a stage kind outside the four published ones",
		`{"name":"quick","version":"1.2.3","source":"repo","sha256":"x","stages":[{"kind":"prune"}]}`)
	assertRefused(t, root, published, "a source outside builtin and repo",
		`{"name":"quick","version":"1.2.3","source":"file","sha256":"x","stages":[{"kind":"dedupe"}]}`)
	assertRefused(t, root, published, "a plan with no stages",
		`{"name":"quick","version":"1.2.3","source":"repo"}`)
}

// publishedSchema returns the generated recipes schema as a decoded document.
func publishedSchema(t *testing.T) map[string]any {
	t.Helper()
	root, ok := recipeSchema(t).(map[string]any)
	if !ok {
		t.Fatal("generated recipes schema is not a JSON object")
	}
	return root
}

// publishedPlanDef returns the `$defs/plan` node the published shape lives at.
func publishedPlanDef(t *testing.T, root map[string]any) map[string]any {
	t.Helper()
	defs, ok := root["$defs"].(map[string]any)
	if !ok {
		t.Fatal("generated recipes schema carries no $defs")
	}
	published, ok := defs["plan"].(map[string]any)
	if !ok {
		t.Fatal("$defs misses plan: the resolved pipeline has no published shape")
	}
	return published
}

// assertRefused requires one candidate document to fail the published shape.
func assertRefused(t *testing.T, root, published map[string]any, what, give string) {
	t.Helper()
	if got := violationsOf(t, root, published, give); len(got) == 0 {
		t.Errorf("the schema accepts %s: %s", what, give)
	}
}

// parsedDocument decodes one recipes document through the parser.
func parsedDocument(t *testing.T, give string) *recipe.Document {
	t.Helper()
	doc, ref := recipe.Parse([]byte(give))
	if ref != nil {
		t.Fatalf("Parse(%s): %v", give, ref)
	}
	return doc
}

// violationsOf validates one JSON document against a schema node, resolving
// $ref against the whole published document.
func violationsOf(t *testing.T, root, schema any, give string) []schemacheck.Violation {
	t.Helper()
	value, err := schemacheck.Decode([]byte(give))
	if err != nil {
		t.Fatalf("candidate is not JSON: %v", err)
	}
	return schemacheck.Validate(root, schema, value)
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
