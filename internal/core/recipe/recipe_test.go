package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// entryValid is the least a recipe entry may declare.
const entryValid = `{"version":"1.0.0","synthesize":{}}`

// doc wraps one entry body in the document envelope.
func doc(body string) string {
	return `{"schema_version":"1","recipes":{"quick":` + body + `}}`
}

// lensRows renders count distinct declared lens rows.
func lensRows(count int) string {
	rows := make([]string, 0, count)
	for i := range count {
		rows = append(rows, `{"id":"lens-`+strconv.Itoa(i)+`"}`)
	}
	return "[" + strings.Join(rows, ",") + "]"
}

// checkRefusal proves one input refuses bad-recipe with the named fragment.
func checkRefusal(t *testing.T, give, fragment string) {
	t.Helper()
	_, ref := Parse([]byte(give))
	if ref == nil {
		t.Fatalf("Parse(%s) accepted the document", give)
	}
	if ref.Code != CodeBad {
		t.Fatalf("Parse(%s) code = %q, want %q (%s)", give, ref.Code, CodeBad, ref.Message)
	}
	if !strings.Contains(ref.Message, fragment) {
		t.Errorf("Parse(%s) = %q, want the fragment %q", give, ref.Message, fragment)
	}
}

// TestParseRefusals pins every structural fault: one diagnostic naming the
// JSON path and the offending key.
func TestParseRefusals(t *testing.T) {
	for _, tc := range []struct{ name, give, fragment string }{
		{"unknown top key", `{"schema_version":"1","recipes":{},"bogus":1}`, `$: unknown key "bogus"`},
		{"missing recipes", `{"schema_version":"1"}`, `$: missing "recipes"`},
		{"recipes null", `{"schema_version":"1","recipes":null}`, `$.recipes: want an object, got null`},
		{"duplicate recipe key", `{"schema_version":"1","recipes":{"quick":` + entryValid + `,"quick":` + entryValid + `}}`, `$.recipes: duplicate key "quick"`},
		{"duplicate entry key", doc(`{"version":"1.0.0","version":"1.0.0","synthesize":{}}`), `$.recipes.quick: duplicate key "version"`},
		{"trailing data", `{"schema_version":"1","recipes":{}} {}`, `$: trailing data after the top object at offset`},
		{"malformed", `{"schema_version":`, `$.schema_version: malformed JSON at offset`},
		{"nesting past the decoder cap", strings.Repeat("[", 65) + strings.Repeat("]", 65), `$: nesting deeper than 64 levels at offset 65`},
		{"top array", `[]`, `$: want an object, got array`},
		{"schema_version type", `{"schema_version":1,"recipes":{}}`, `schema_version: want a string, got number`},
		{"schema_version value", `{"schema_version":"2","recipes":{}}`, `schema_version: want the string "1", got "2"`},
		{"recipe key not a slug", `{"schema_version":"1","recipes":{"Quick":` + entryValid + `}}`, `$.recipes: recipe key "Quick" is not a lowercase slug`},
		{"entry not an object", `{"schema_version":"1","recipes":{"quick":"x"}}`, `$.recipes.quick: want an object, got string`},
		{"unknown entry key", doc(`{"version":"1.0.0","synthesize":{},"bogus":1}`), `$.recipes.quick: unknown key "bogus"`},
		{"missing version", doc(`{"synthesize":{}}`), `$.recipes.quick: missing "version"`},
		{"version not semver", doc(`{"version":"1.0","synthesize":{}}`), `$.recipes.quick.version: "1.0" is not semver`},
		{"version too long", doc(`{"version":"1.0.0+` + strings.Repeat("b", 59) + `","synthesize":{}}`), `$.recipes.quick.version: 65 characters, over the 64-character cap`},
		{"description too long", doc(`{"version":"1.0.0","description":"` + strings.Repeat("x", 281) + `","synthesize":{}}`), `$.recipes.quick.description: 281 characters, over the 280-character cap`},
		{"budget zero", doc(`{"version":"1.0.0","budget_usd":0,"synthesize":{}}`), `$.recipes.quick.budget_usd: want a number above 0, got 0`},
		{"budget negative", doc(`{"version":"1.0.0","budget_usd":-1,"synthesize":{}}`), `$.recipes.quick.budget_usd: want a number above 0, got -1`},
		{"budget string", doc(`{"version":"1.0.0","budget_usd":"1.2","synthesize":{}}`), `$.recipes.quick.budget_usd: want a number, got string`},
		{"lenses empty", doc(`{"version":"1.0.0","lenses":[],"synthesize":{}}`), `$.recipes.quick.lenses: want at least one lens`},
		{"lenses null", doc(`{"version":"1.0.0","lenses":null,"synthesize":{}}`), `$.recipes.quick.lenses: want an array, got null`},
		{"lens not an object", doc(`{"version":"1.0.0","lenses":["logic"],"synthesize":{}}`), `$.recipes.quick.lenses[0]: want an object, got string`},
		{"unknown lens key", doc(`{"version":"1.0.0","lenses":[{"id":"logic","globs":[]}],"synthesize":{}}`), `$.recipes.quick.lenses[0]: unknown key "globs"`},
		{"lens id missing", doc(`{"version":"1.0.0","lenses":[{"model":"opus"}],"synthesize":{}}`), `$.recipes.quick.lenses[0]: missing "id"`},
		{"lens id not a slug", doc(`{"version":"1.0.0","lenses":[{"id":"Logic"}],"synthesize":{}}`), `$.recipes.quick.lenses[0].id: "Logic" is not a lowercase slug`},
		{"lens id too long", doc(`{"version":"1.0.0","lenses":[{"id":"` + strings.Repeat("l", 65) + `"}],"synthesize":{}}`), `$.recipes.quick.lenses[0].id: "` + strings.Repeat("l", 65) + `" is not a lowercase slug`},
		{"too many lenses", doc(`{"version":"1.0.0","lenses":` + lensRows(65) + `,"synthesize":{}}`), `$.recipes.quick.lenses: 65 lenses, over the 64-lens cap`},
		{"duplicate lens id", doc(`{"version":"1.0.0","lenses":[{"id":"logic"},{"id":"logic"}],"synthesize":{}}`), `$.recipes.quick.lenses[1].id: duplicate lens id "logic"`},
		{"max_files zero", doc(`{"version":"1.0.0","lenses":[{"id":"logic","max_files":0}],"synthesize":{}}`), `$.recipes.quick.lenses[0].max_files: want an integer 1..24, got 0`},
		{"max_files over", doc(`{"version":"1.0.0","lenses":[{"id":"logic","max_files":25}],"synthesize":{}}`), `$.recipes.quick.lenses[0].max_files: want an integer 1..24, got 25`},
		{"max_findings under", doc(`{"version":"1.0.0","lenses":[{"id":"logic","max_findings":0}],"synthesize":{}}`), `$.recipes.quick.lenses[0].max_findings: want an integer 1..20, got 0`},
		{"max_findings fraction", doc(`{"version":"1.0.0","lenses":[{"id":"logic","max_findings":1.5}],"synthesize":{}}`), `$.recipes.quick.lenses[0].max_findings: want an integer, got 1.5`},
		{"max_files spelled over", doc(`{"version":"1.0.0","lenses":[{"id":"logic","max_files":25.0}],"synthesize":{}}`), `$.recipes.quick.lenses[0].max_files: want an integer, got 25.0`},
		{"max_files zero fraction", doc(`{"version":"1.0.0","lenses":[{"id":"logic","max_files":24.0}],"synthesize":{}}`), `$.recipes.quick.lenses[0].max_files: want an integer, got 24.0`},
		{"max_files exponent", doc(`{"version":"1.0.0","lenses":[{"id":"logic","max_files":1e1}],"synthesize":{}}`), `$.recipes.quick.lenses[0].max_files: want an integer, got 1e1`},
		{"recipe key too long", `{"schema_version":"1","recipes":{"` + strings.Repeat("r", 65) + `":` + entryValid + `}}`, `$.recipes: recipe key "` + strings.Repeat("r", 65) + `" is not a lowercase slug`},
		{"lens model whitespace", doc(`{"version":"1.0.0","lenses":[{"id":"logic","model":"opus 4"}],"synthesize":{}}`), `$.recipes.quick.lenses[0].model: "opus 4" is not a model hint`},
		{"lens model quote", doc(`{"version":"1.0.0","lenses":[{"id":"logic","model":"o\"pus"}],"synthesize":{}}`), `$.recipes.quick.lenses[0].model: "o\"pus" is not a model hint`},
		{"lens model too long", doc(`{"version":"1.0.0","lenses":[{"id":"logic","model":"` + strings.Repeat("m", 65) + `"}],"synthesize":{}}`), `$.recipes.quick.lenses[0].model: ` + `"` + strings.Repeat("m", 65) + `" is not a model hint`},
		{"missing synthesize", doc(`{"version":"1.0.0"}`), `$.recipes.quick: missing "synthesize"`},
		{"synthesize null", doc(`{"version":"1.0.0","synthesize":null}`), `$.recipes.quick.synthesize: want an object, got null`},
		{"unknown synthesize key", doc(`{"version":"1.0.0","synthesize":{"model":"opus","temp":1}}`), `$.recipes.quick.synthesize: unknown key "temp"`},
		{"synthesize model whitespace", doc(`{"version":"1.0.0","synthesize":{"model":"op us"}}`), `$.recipes.quick.synthesize.model: "op us" is not a model hint`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkRefusal(t, tc.give, tc.fragment)
		})
	}
}

// TestParseAcceptsValidEntry pins the accepted shape, including an entry that
// omits every optional key.
func TestParseAcceptsMinimalEntry(t *testing.T) {
	doc := parsed(t, `{"schema_version":"1","recipes":{"quick":`+entryValid+`}}`)
	if doc.SchemaVersion != "1" {
		t.Errorf("schema_version = %q, want \"1\"", doc.SchemaVersion)
	}
	if quick := doc.Recipes["quick"]; quick.Version != "1.0.0" || quick.BudgetUSD != 0 || len(quick.Lenses) != 0 {
		t.Errorf("quick = %+v, want the minimal entry", quick)
	}
}

// TestParseAcceptsFullEntry proves every optional key round-trips into the
// entry: description, budget, one lens row with its hint and caps, and the
// synthesizer hint.
func TestParseAcceptsFullEntry(t *testing.T) {
	doc := parsed(t, `{"schema_version":"1","recipes":{"full":{"version":"1.2.3","description":"Cheap pass","budget_usd":1.2,"lenses":[{"id":"logic","model":"sonnet","max_files":8,"max_findings":6}],"synthesize":{"model":"opus"}}}}`)
	full := doc.Recipes["full"]
	if full.Version != "1.2.3" || full.Description != "Cheap pass" || full.BudgetUSD != 1.2 {
		t.Errorf("full = %+v", full)
	}
	if len(full.Lenses) != 1 || full.Lenses[0].ID != "logic" || full.Lenses[0].Model != "sonnet" ||
		full.Lenses[0].MaxFiles != 8 || full.Lenses[0].MaxFindings != 6 {
		t.Errorf("full lenses = %+v", full.Lenses)
	}
	if full.Synthesize.Model != "opus" {
		t.Errorf("full synthesize = %+v", full.Synthesize)
	}
}

// TestParseAcceptsWhatTheSchemaPublishes pins the two rules the published
// schema states, so the parser cannot refuse a document a third-party validator
// accepts: an empty description is a description (the schema publishes no
// minLength), and an integer is an integer literal (24, not 24.0 — the config
// and pack readers refuse the fraction spelling, so the schema does too).
func TestParseAcceptsWhatTheSchemaPublishes(t *testing.T) {
	parsedDoc := parsed(t, `{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","description":"",`+
		`"lenses":[{"id":"logic","max_files":24,"max_findings":10}],"synthesize":{}}}}`)
	quick := parsedDoc.Recipes["quick"]
	if quick.Description != "" {
		t.Errorf("description = %q, want the empty string", quick.Description)
	}
	if len(quick.Lenses) != 1 {
		t.Fatalf("lenses = %+v, want one row", quick.Lenses)
	}
	if got := quick.Lenses[0]; got.MaxFiles != 24 || got.MaxFindings != 10 {
		t.Errorf("caps = %d/%d, want 24/10", got.MaxFiles, got.MaxFindings)
	}
	for _, spelled := range []string{"24.0", "1e1"} {
		checkRefusal(t, doc(`{"version":"1.0.0","lenses":[{"id":"logic","max_files":`+spelled+`}],"synthesize":{}}`),
			"want an integer, got "+spelled)
	}
}

// TestDeclaredLensesKeepOrder proves a declared list stands in declaration
// order, with its hints and caps, whatever the bundle loads.
func TestDeclaredLensesKeepOrder(t *testing.T) {
	const give = `{"schema_version":"1","recipes":{"declared":{"version":"1.0.0","lenses":[{"id":"security","model":"opus","max_files":4},{"id":"perf"}],"synthesize":{"model":"sonnet"}}}}`
	lenses := planOf(t, give, "declared", []string{"perf", "logic", "test"}).Stages[0]
	if lenses.Kind != StageLenses || lenses.LensSource != LensSourceDeclared {
		t.Fatalf("declared lenses stage = %+v", lenses)
	}
	if got := lensIDs(lenses.Lenses); !slices.Equal(got, []string{"security", "perf"}) {
		t.Errorf("declared lens ids = %v, want declaration order", got)
	}
	if lenses.Lenses[0].Model != "opus" || lenses.Lenses[0].MaxFiles != 4 || lenses.Lenses[1].Model != "" {
		t.Errorf("declared lens hints = %+v", lenses.Lenses)
	}
}

// TestOmittedLensesInheritBundleOrder proves a recipe that declares no lenses
// inherits the bundle's loaded ids in file order.
func TestOmittedLensesInheritBundleOrder(t *testing.T) {
	const give = `{"schema_version":"1","recipes":{"bundled":{"version":"1.0.0","synthesize":{}}}}`
	installed := []string{"perf", "logic", "test"}
	lenses := planOf(t, give, "bundled", installed).Stages[0]
	if got := lenses.LensSource; got != LensSourceBundle {
		t.Errorf("bundled lens_source = %q, want %q", got, LensSourceBundle)
	}
	if got := lensIDs(lenses.Lenses); !slices.Equal(got, installed) {
		t.Errorf("bundled lens ids = %v, want the loaded ids %v", got, installed)
	}
}

// TestResolvedPlanIsFourStages proves the pipeline order and the two hints a
// stage carries: the lenses source and the synthesizer model.
func TestResolvedPlanIsFourStages(t *testing.T) {
	repo, ref := Parse([]byte(`{"schema_version":"1","recipes":{"quick":` + entryValid + `}}`))
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	plan, ref := Resolve(repo, "quick", nil)
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	var kinds []string
	for _, stage := range plan.Stages {
		kinds = append(kinds, stage.Kind)
	}
	if want := []string{"lenses", "synthesize", "dedupe", "rank"}; !slices.Equal(kinds, want) {
		t.Fatalf("stage kinds = %v, want %v", kinds, want)
	}
	if plan.Stages[1].Model != "" || plan.Stages[2].Model != "" || plan.Stages[3].Model != "" {
		t.Errorf("undeclared models leaked: %+v", plan.Stages)
	}
	if plan.BudgetUSD != 0 {
		t.Errorf("budget_usd = %v on an entry that declares none", plan.BudgetUSD)
	}
}

// TestPlanWireShape pins the published data.recipe shape: every key, its
// order, and the keys an undeclared value omits.
func TestPlanWireShape(t *testing.T) {
	repo, ref := Parse([]byte(`{"schema_version":"1","recipes":{"quick":{"version":"1.2.3","budget_usd":1.2,"lenses":[{"id":"logic","model":"sonnet","max_files":8,"max_findings":6},{"id":"perf"}],"synthesize":{"model":"opus"}}}}`))
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	plan, ref := Resolve(repo, "quick", []string{"logic", "perf"})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	plan.SHA256 = strings.Repeat("a", 64)
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	want := `{"name":"quick","version":"1.2.3","source":"repo","sha256":"` + strings.Repeat("a", 64) + `","budget_usd":1.2,` +
		`"stages":[{"kind":"lenses","lens_source":"declared","lenses":[{"id":"logic","model":"sonnet","max_files":8,"max_findings":6},{"id":"perf"}]},` +
		`{"kind":"synthesize","model":"opus"},{"kind":"dedupe"},{"kind":"rank"}]}`
	if string(raw) != want {
		t.Errorf("plan wire shape =\n%s\nwant\n%s", raw, want)
	}
}

// TestHashIsCanonicalAndMoves proves the recorded sha256: the same document
// re-parsed yields the same digest, and one field's change moves it.
func TestHashIsCanonicalAndMoves(t *testing.T) {
	give := `{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"logic","max_files":8}],"synthesize":{"model":"opus"}}}}`
	one := planOf(t, give, "quick", nil)
	two := planOf(t, give, "quick", nil)
	if one.SHA256 != two.SHA256 {
		t.Fatalf("sha256 moved across a re-parse: %s then %s", one.SHA256, two.SHA256)
	}
	if len(one.SHA256) != 64 || strings.ToLower(one.SHA256) != one.SHA256 {
		t.Errorf("sha256 = %q, want 64 lower-case hex characters", one.SHA256)
	}
	moved := planOf(t, strings.Replace(give, `"max_files":8`, `"max_files":9`, 1), "quick", nil)
	if moved.SHA256 == one.SHA256 {
		t.Error("one declared cap changed and the sha256 did not move")
	}
}

// TestWarnings proves the one lens warning: a declared lens the bundle does not
// load. A recipe that declares no lenses is silent whatever the bundle loaded,
// because inheriting an empty set is the ordinary lens-less bundle, not news.
func TestWarnings(t *testing.T) {
	const give = `{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"security"}],"synthesize":{}},"bare":` + entryValid + `}}`
	if got := planOf(t, give, "quick", []string{"perf"}).Warnings; len(got) != 1 ||
		!strings.Contains(got[0], `"security"`) {
		t.Errorf("warnings = %v, want one naming the absent lens", got)
	}
	if got := planOf(t, give, "quick", []string{"security"}).Warnings; len(got) != 0 {
		t.Errorf("warnings = %v, want none when the bundle loads the lens", got)
	}
	if got := planOf(t, give, "bare", nil).Warnings; len(got) != 0 {
		t.Errorf("warnings = %v, want none for a recipe that declares no lenses", got)
	}
	if got := planOf(t, give, "bare", []string{"perf"}).Warnings; len(got) != 0 {
		t.Errorf("warnings = %v, want none when the bundle supplies the lenses", got)
	}
}

// TestPlanHashIsOverTheEntry proves the digest is the entry's canonical JSON,
// computed here from the published bytes rather than from the loader.
func TestPlanHashIsOverTheEntry(t *testing.T) {
	doc, ref := Parse([]byte(`{"schema_version":"1","recipes":{"default":` + entryValid + `}}`))
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	plan, ref := Resolve(doc, "default", nil)
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	want := sha256Hex(`{"version":"1.0.0","synthesize":{}}`)
	if plan.SHA256 != want {
		t.Errorf("sha256 = %s, want the canonical entry digest %s", plan.SHA256, want)
	}
}

// sha256Hex hashes one literal with the canonical digest the plan publishes.
func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// lensIDs lists the ids one lenses stage resolved.
func lensIDs(lenses []LensUse) []string {
	ids := make([]string, 0, len(lenses))
	for _, lens := range lenses {
		ids = append(ids, lens.ID)
	}
	return ids
}
