package corpuscore

import (
	"encoding/json"
	"strings"
	"testing"
)

// guardDiff is the shape of a real case's reviewed diff: identifiers, a
// selector, a comment lifted from the patch, and prose the author wrote.
const guardDiff = `@@ -90,6 +90,10 @@
+	if StopOnNthArg > 0 {
+		return args
+	}
+	// skip empty source values for flags that cannot parse them
+	cmd.Name = helpName
`

// TestCheckKeywordsRefusesDiffVocabulary pins the structural guard from both
// sides: a keyword the diff spells is refused, and a keyword that merely
// shares letters with an identifier is kept.
func TestCheckKeywordsRefusesDiffVocabulary(t *testing.T) {
	for _, tc := range guardRows {
		t.Run(tc.name, func(t *testing.T) { checkGuardRow(t, tc) })
	}
}

// checkGuardRow grades one row: a refused keyword must be named by the
// refusal, and a kept one must survive untouched.
func checkGuardRow(t *testing.T, tc guardRow) {
	t.Helper()
	ref := CheckKeywords(guardManifest(t, tc.kind, tc.keywords), []byte(guardDiff), "case.json")
	if tc.refuse == "" {
		if ref != nil {
			t.Fatalf("refused %v, want the keyword kept", ref)
		}
		return
	}
	if ref == nil {
		t.Fatalf("accepted keywords %v, want %q refused", tc.keywords, tc.refuse)
	}
	if !strings.Contains(ref.Message, `"`+tc.refuse+`"`) {
		t.Errorf("message %q does not name the keyword %q", ref.Message, tc.refuse)
	}
}

// guardRow is one keyword list and the keyword, if any, the guard must refuse.
type guardRow struct {
	name     string
	kind     string
	refuse   string
	keywords []string
}

var guardRows = []guardRow{
	{name: "identifier, any case", refuse: "stoponntharg", keywords: []string{"early return", "stoponntharg"}},
	{name: "selector", refuse: "cmd.name", keywords: []string{"cmd.name", "on the boundary"}},
	{name: "a word the diff comments", refuse: "empty source", keywords: []string{"empty source", "thrown away"}},
	{name: "letters inside a longer identifier", keywords: []string{"help", "ntharg"}},
	{name: "prose the diff never spells", keywords: []string{"the check runs too late", "boundary check", "swallowed"}},
	{name: "seeded case, its own diff", kind: "seeded", keywords: []string{"stoponntharg"}},
	{name: "control, no bugs", kind: "control", keywords: []string{"stoponntharg"}},
}

// TestCheckKeywordsRefusesAnUnreadableManifest keeps the guard's failure the
// same corpus-invalid refusal every other manifest failure is.
func TestCheckKeywordsRefusesAnUnreadableManifest(t *testing.T) {
	ref := CheckKeywords([]byte("{"), []byte(guardDiff), "case.json")
	if ref == nil || ref.Code != codeCorpusInvalid {
		t.Fatalf("ref = %v, want a corpus-invalid refusal", ref)
	}
}

// guardManifest builds one case.json body: real unless a test says otherwise,
// so the guard's kind rule is the only variable.
func guardManifest(t *testing.T, kind string, keywords []string) []byte {
	t.Helper()
	if kind == "" {
		kind = kindReal
	}
	bugs := []any{map[string]any{
		"path": "command.go", "line": 414, "kind": "validation-bypass",
		"severity": "important", "keywords": keywords,
	}}
	if kind == kindControl {
		bugs = []any{}
	}
	doc := map[string]any{
		"schema_version": "1",
		"id":             "guard-fixture",
		"kind":           kind,
		"provenance": map[string]any{
			"kind": kind, "repo": "urfave/cli", "pr": 2319,
			"base_sha": strings.Repeat("a", 40), "head_sha": strings.Repeat("b", 40),
		},
		"bugs": bugs,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	return raw
}
