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

// selectorDiff is a patch whose only mention of the selector is the dotted
// form, so a keyword that is one of its parts is spelled only as a part.
const selectorDiff = `diff --git a/command.go b/command.go
--- a/command.go
+++ b/command.go
@@ -410,1 +410,4 @@ func run() error {
+	if cmd.Name == helpName {
+		return nil
+	}
 	return cmd.Run()
`

// nilCheckDiff carries a category phrase in a comment, the shape that let a
// nil-deref case grade the diff's own words.
const nilCheckDiff = `diff --git a/session.go b/session.go
--- a/session.go
+++ b/session.go
@@ -50,2 +50,3 @@ func load(id string) *Session {
+	// add a nil check before the lookup
+	s := lookup(id)
 	return s
`

// kindPhraseDiff carries a bug's kind in space form, which names the defect
// without any of the case's keywords.
const kindPhraseDiff = `diff --git a/parse.go b/parse.go
--- a/parse.go
+++ b/parse.go
@@ -10,1 +10,3 @@ func parse() {
+	// validation bypass: the required flag is never checked
+	run(name)
 	return
`

// TestCheckDiffRefusesDiffVocabulary pins the vocabulary guard from both
// sides: a phrase the diff spells as a word, a selector part, or a longer
// identifier's prefix is refused, and a phrase that only shares letters inside
// an identifier is kept.
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
	ref := CheckDiff(guardManifest(t, tc), []byte(tc.diffText()), "case.json")
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
	bugKind  string
	diff     string
	path     string
	refuse   string
	keywords []string
	also     []int
	line     int
}

func (r *guardRow) diffText() string {
	if r.diff == "" {
		return guardDiff
	}
	return r.diff
}

var keywordGuardRows = []*guardRow{
	{name: "identifier, any case", refuse: "stoponntharg", keywords: []string{"early return", "stoponntharg"}, line: 93},
	{name: "selector", refuse: "cmd.name", keywords: []string{"cmd.name", "on the boundary"}, line: 93},
	{name: "split selector", refuse: "\"cmd\"", diff: selectorDiff, path: "command.go", keywords: []string{"cmd", "name"}, line: 410},
	{name: "split selector field", refuse: "\"name\"", diff: selectorDiff, path: "command.go", keywords: []string{"name"}, line: 410},
	{name: "spaced selector", refuse: "\"cmd .Name\"", keywords: []string{"cmd .Name"}, line: 93},
	{name: "prefix of a longer identifier", refuse: "\"help\"", diff: selectorDiff, path: "command.go", keywords: []string{"help"}, line: 410},
	{name: "inside a longer identifier", keywords: []string{"ntharg"}, line: 93},
	{name: "a word the diff comments", refuse: "empty source", keywords: []string{"empty source", "thrown away"}, line: 93},
	{name: "prose the diff never spells", keywords: []string{"the check runs too late", "boundary check", "swallowed"}, line: 93},
	{name: "category phrase the diff comments", refuse: "\"nil check\"", bugKind: "nil-deref", diff: nilCheckDiff, path: "session.go", keywords: []string{"dereference before the lookup"}, line: 50},
	{name: "kind phrase the diff comments", refuse: "\"validation bypass\"", diff: kindPhraseDiff, path: "parse.go", keywords: []string{"the exemption ignores the required flag"}, line: 10},
	{name: "seeded case, its own diff", kind: "seeded", keywords: []string{"stoponntharg"}, line: 93},
	{name: "control, no bugs", kind: "control", keywords: []string{"stoponntharg"}, line: 93},
}

var anchorGuardRows = []*guardRow{
	{name: "added line", keywords: []string{"the check runs too late"}, line: 93},
	{name: "first added line", keywords: []string{"the check runs too late"}, line: 91},
	{name: "context line", refuse: "help.go:96", keywords: []string{"the check runs too late"}, line: 96},
	{name: "context line before the hunk", refuse: "help.go:12", keywords: []string{"the check runs too late"}, line: 12},
	{name: "also_lines on an added line", keywords: []string{"the check runs too late"}, line: 93, also: []int{94, 95}},
	{name: "also_line on a context line", refuse: "help.go:96", keywords: []string{"the check runs too late"}, line: 93, also: []int{96}},
	{name: "also_line outside the hunk", refuse: "help.go:200", keywords: []string{"the check runs too late"}, line: 93, also: []int{200}},
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
	bug := map[string]any{
		"path": "help.go", "line": line, "kind": "validation-bypass",
		"severity": "important", "keywords": row.keywords,
	}
	if row.path != "" {
		bug["path"] = row.path
	}
	if row.bugKind != "" {
		bug["kind"] = row.bugKind
	}
	if len(row.also) > 0 {
		bug["also_lines"] = row.also
	}
	bugs := []any{bug}
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
