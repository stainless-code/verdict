package archtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// promptReviewKeys are the review.json keys prompts/review.md must publish, and
// the schema branch that has to define each one. A pilot review wrote `rung`
// and `in_bounds` because the prompt described the evidence rung and the
// in-bounds flag without naming their keys; record repaired both instead of
// the review landing clean.
var promptReviewKeys = []struct {
	key  string
	text string
	path []string
}{
	{"rigor", "`rigor`", []string{"$defs", "comment", "properties", "rigor"}},
	{"in-bounds", "`in-bounds`", []string{"$defs", "comment", "properties", "in-bounds"}},
	{"file", `{"file": …, "ranges": …}`, []string{"$defs", "coverageClaim", "properties", "file"}},
	{"ranges", `{"file": …, "ranges": …}`, []string{"$defs", "coverageClaim", "properties", "ranges"}},
}

// TestPromptNamesReviewKeys holds the prompt to the published key names: every
// key it tells the model to emit exists in review.schema.json under that exact
// spelling, so the prompt can never describe a concept the schema names
// differently.
func TestPromptNamesReviewKeys(t *testing.T) {
	t.Parallel()
	prompt := string(readSkillFile(t, "prompts/review.md"))
	for _, row := range promptReviewKeys {
		if !strings.Contains(prompt, row.text) {
			t.Errorf("prompts/review.md never writes %s for key %q", row.text, row.key)
		}
		if !schemaHas(t, row.path...) {
			t.Errorf("review.schema.json has no property %q at %s", row.key, strings.Join(row.path, "."))
		}
	}
}

// TestPromptRigorValuesMatchSchema pins the rung values the prompt publishes to
// the schema enum: a prompt listing a value the schema refuses sends the model
// into a refusal, and one omitting a value hides an allowed rung.
func TestPromptRigorValuesMatchSchema(t *testing.T) {
	t.Parallel()
	want := []string{"said", "pointed", "walked", "ran", "reproduced"}
	published := "`" + strings.Join(want, "|") + "`"
	if prompt := string(readSkillFile(t, "prompts/review.md")); !strings.Contains(prompt, published) {
		t.Errorf("prompts/review.md does not publish the rigor values as %s", published)
	}
	enum, ok := schemaBranch(t, "$defs", "comment", "properties", "rigor")["enum"].([]any)
	if !ok {
		t.Fatal("review.schema.json rigor enum is not an array")
	}
	var got []string
	for _, v := range enum {
		if s, ok := v.(string); ok {
			got = append(got, s)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("review.schema.json rigor enum = %v; want %v", got, want)
	}
}

// readSkillFile reads one shipped skill file.
func readSkillFile(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleRoot(t), "skills", "verdict", filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read skills/verdict/%s: %v", name, err)
	}
	return raw
}

// schemaBranch returns the schema object at path, failing on a missing branch.
func schemaBranch(t *testing.T, path ...string) map[string]any {
	t.Helper()
	var doc any
	if err := json.Unmarshal(readSkillFile(t, "schemas/review.schema.json"), &doc); err != nil {
		t.Fatalf("decode review.schema.json: %v", err)
	}
	node, ok := doc.(map[string]any)
	for _, step := range path {
		if !ok {
			t.Fatalf("review.schema.json has no object at %s", strings.Join(path, "."))
		}
		node, ok = node[step].(map[string]any)
	}
	if !ok {
		t.Fatalf("review.schema.json has no object at %s", strings.Join(path, "."))
	}
	return node
}

// schemaHas reports whether review.schema.json defines path.
func schemaHas(t *testing.T, path ...string) bool {
	t.Helper()
	var doc any
	if err := json.Unmarshal(readSkillFile(t, "schemas/review.schema.json"), &doc); err != nil {
		t.Fatalf("decode review.schema.json: %v", err)
	}
	node := doc
	for _, step := range path {
		obj, ok := node.(map[string]any)
		if !ok {
			return false
		}
		if node, ok = obj[step]; !ok {
			return false
		}
	}
	return true
}
