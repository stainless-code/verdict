package corpuscore

import (
	"encoding/json"
	"strings"
	"testing"
)

// guardDiff is the shape of a real case's reviewed diff: identifiers, a
// selector, a comment lifted from the patch, prose the author wrote, and
// context lines a ground truth may not anchor on.
const guardDiff = `diff --git a/help.go b/help.go
--- a/help.go
+++ b/help.go
@@ -90,6 +90,10 @@ func helpCommandAction(ctx context.Context, cmd *Command) error {
 	args := cmd.Args()
+	if StopOnNthArg > 0 {
+		return args
+	}
+	// skip empty source values for flags that cannot parse them
+	cmd.Name = helpName
 	return nil
`

// TestCheckDiffRefusesDiffVocabulary pins the vocabulary guard from both
// sides: a keyword the diff spells is refused, and a keyword that merely
// shares letters with an identifier is kept.
func TestCheckDiffRefusesDiffVocabulary(t *testing.T) {
	for _, tc := range keywordGuardRows {
		t.Run(tc.name, func(t *testing.T) { checkGuardRow(t, tc) })
	}
}

// TestCheckDiffRefusesAContextAnchor pins the anchor guard: a ground truth on
// a line the diff only carries as context is refused, on an added line kept.
func TestCheckDiffRefusesAContextAnchor(t *testing.T) {
	for _, tc := range anchorGuardRows {
		t.Run(tc.name, func(t *testing.T) { checkGuardRow(t, tc) })
	}
}

// checkGuardRow grades one row: a refused case must be named by the refusal,
// and a kept one must survive untouched.
func checkGuardRow(t *testing.T, tc *guardRow) {
	t.Helper()
	ref := CheckDiff(guardManifest(t, tc), []byte(guardDiff), "case.json")
	if tc.refuse == "" {
		if ref != nil {
			t.Fatalf("refused %v, want the case kept", ref)
		}
		return
	}
	if ref == nil {
		t.Fatalf("accepted %+v, want a refusal naming %q", tc, tc.refuse)
	}
	if !strings.Contains(ref.Message, tc.refuse) {
		t.Errorf("message %q does not name %q", ref.Message, tc.refuse)
	}
}

// guardRow is one case manifest plus the text its refusal must name.
type guardRow struct {
	name     string
	kind     string
	refuse   string
	keywords []string
	line     int
}

var keywordGuardRows = []*guardRow{
	{name: "identifier, any case", refuse: "stoponntharg", keywords: []string{"early return", "stoponntharg"}, line: 93},
	{name: "selector", refuse: "cmd.name", keywords: []string{"cmd.name", "on the boundary"}, line: 93},
	{name: "a word the diff comments", refuse: "empty source", keywords: []string{"empty source", "thrown away"}, line: 93},
	{name: "letters inside a longer identifier", keywords: []string{"help", "ntharg"}, line: 93},
	{name: "prose the diff never spells", keywords: []string{"the check runs too late", "boundary check", "swallowed"}, line: 93},
	{name: "seeded case, its own diff", kind: "seeded", keywords: []string{"stoponntharg"}, line: 93},
	{name: "control, no bugs", kind: "control", keywords: []string{"stoponntharg"}, line: 93},
}

var anchorGuardRows = []*guardRow{
	{name: "added line", keywords: []string{"the check runs too late"}, line: 93},
	{name: "first added line", keywords: []string{"the check runs too late"}, line: 91},
	{name: "context line", refuse: "help.go:96", keywords: []string{"the check runs too late"}, line: 96},
	{name: "context line before the hunk", refuse: "help.go:12", keywords: []string{"the check runs too late"}, line: 12},
	{name: "seeded case anchors freely", kind: "seeded", keywords: []string{"the check runs too late"}, line: 96},
}

// TestCheckDiffRefusesAnUnreadableManifest keeps the guard's failure the same
// corpus-invalid refusal every other manifest failure is.
func TestCheckDiffRefusesAnUnreadableManifest(t *testing.T) {
	ref := CheckDiff([]byte("{"), []byte(guardDiff), "case.json")
	if ref == nil || ref.Code != codeCorpusInvalid {
		t.Fatalf("ref = %v, want a corpus-invalid refusal", ref)
	}
}

// guardManifest builds one case.json body: real unless a test says otherwise,
// so the guards' kind rule is the only variable.
func guardManifest(t *testing.T, row *guardRow) []byte {
	t.Helper()
	kind := row.kind
	if kind == "" {
		kind = kindReal
	}
	line := row.line
	if line == 0 {
		line = 93
	}
	bugs := []any{map[string]any{
		"path": "help.go", "line": line, "kind": "validation-bypass",
		"severity": "important", "keywords": row.keywords,
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
