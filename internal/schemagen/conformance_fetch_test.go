package schemagen

import (
	"reflect"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/schemacheck"
)

// TestRuntimeFetchOutputValidates proves the fetch service response (a)
// validates against the generated envelope schema, (b) its data block
// validates against the pinned fetchData profile, and (c) it round-trips
// through the modeled envelope field for field.
func TestRuntimeFetchOutputValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	head, base := "aaaa", "bbbb"
	data := &core.FetchData{
		Dir:       ".verdict/state/5101",
		HeadSHA:   head,
		BaseSHA:   base,
		Files:     2,
		Additions: 3,
		Deletions: 1,
		Hunks:     2,
		Omitted:   []any{},
		DiffBytes: 123,
		Panel:     core.FetchPanel{Members: []string{}},
		Artifacts: jq.NewObj(),
	}
	env := core.RenderEnvelope("fetch", "acme/app", "5101",
		core.NowUTC(now), "feed the bundle to any agent", []string{"w1"}, data.Wire(), nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))
	validateWire(t, doc, "fetch data", defOf(t, doc, "fetchData"), decodeWire(t, data.Wire()))

	var modeled Envelope
	unmarshalWire(t, env, &modeled)
	if modeled.Verb != "fetch" || modeled.Repo != "acme/app" {
		t.Errorf("envelope identity = %+v", modeled)
	}
	if modeled.Pr == nil || *modeled.Pr != 5101 {
		t.Errorf("envelope pr = %v", modeled.Pr)
	}
	if modeled.Data == nil || (*modeled.Data)["dir"] != ".verdict/state/5101" {
		t.Errorf("envelope data = %v", modeled.Data)
	}
	if modeled.Error != nil {
		t.Errorf("success envelope error = %+v", modeled.Error)
	}
}

// TestRuntimeFetchLocalOutputValidates proves the fetch-local data shape
// carries kind instead of incremental_base and still validates.
func TestRuntimeFetchLocalOutputValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	kind := "worktree"
	data := &core.FetchData{
		Local:     true,
		Dir:       ".verdict/state/local-mine",
		Kind:      kind,
		HeadSHA:   "",
		BaseSHA:   "cccc",
		Files:     1,
		Additions: 1,
		Deletions: 0,
		Hunks:     1,
		Omitted:   []any{},
		DiffBytes: 45,
		Panel:     core.FetchPanel{Members: []string{}},
		Artifacts: jq.NewObj(),
	}
	env := core.RenderEnvelope("fetch-local", "acme/app", "mine",
		"2026-11-02T12:00:00Z", "feed the bundle to any agent", nil, data.Wire(), nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))
	validateWire(t, doc, "fetch-local data", defOf(t, doc, "fetchLocalData"), decodeWire(t, data.Wire()))

	var modeled Envelope
	unmarshalWire(t, env, &modeled)
	if modeled.Pr != nil {
		t.Errorf("local slug pr = %v, want null", modeled.Pr)
	}
	if modeled.Data == nil || (*modeled.Data)["kind"] != kind {
		t.Errorf("envelope data = %v", modeled.Data)
	}
	if _, has := (*modeled.Data)["incremental_base"]; has {
		t.Errorf("local data carries incremental_base: %v", modeled.Data)
	}
}

// TestRuntimeAuthorValidates proves the author fingerprint writer's shape —
// a named model, then a null one — validates against its own published schema.
func TestRuntimeAuthorValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "author.schema.json")
	model := "Opus 4.5"
	named := &core.AuthorFile{
		SchemaVersion: "1", Agent: "claude", Model: &model, Confidence: "high",
		Evidence: []core.AuthorEvidence{{Field: "commits", Signal: "Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}},
	}
	validateWire(t, doc, "author.json", doc, decodeWire(t, core.Wire(named)))

	unnamed := &core.AuthorFile{
		SchemaVersion: "1", Agent: "codex", Confidence: "medium",
		Evidence: []core.AuthorEvidence{{Field: "head_ref", Signal: "codex/"}},
	}
	validateWire(t, doc, "author.json without a model", doc, decodeWire(t, core.Wire(unnamed)))
}

// TestGroupedFetchEstimateValidatesWithoutBudget proves the grouped estimate a
// run publishes with no ordinary budget validates against the generated
// envelope and fetchData profiles, with the unset guard and ceiling absent
// rather than zero, and decodes into the modeled field.
func TestGroupedFetchEstimateValidatesWithoutBudget(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	estimate := jq.NewObj("groups", []any{
		jq.NewObj("group", "group-a", "effort", "standard", "projected_usd", jq.Number("3"),
			"over_guard", false),
		jq.NewObj("group", "group-b", "effort", "standard", "over_guard", false),
	})
	estimate.Set("personal", jq.NewObj("basis", "known-lower-bound", "known_subtotal_usd", jq.Number("3")))
	data := &core.FetchData{
		Dir:       ".verdict/state/5102",
		HeadSHA:   "aaaa",
		BaseSHA:   "bbbb",
		Files:     2,
		Additions: 3,
		Deletions: 1,
		Hunks:     2,
		Omitted:   []any{},
		DiffBytes: 123,
		Panel:     core.FetchPanel{Members: []string{}},
		Artifacts: jq.NewObj(),
		Estimate:  estimate,
	}
	env := core.RenderEnvelope("fetch", "acme/app", "5102",
		core.NowUTC(time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)), "feed the bundle", nil,
		data.Wire(), nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))
	validateWire(t, doc, "grouped fetch data", defOf(t, doc, "fetchData"), decodeWire(t, data.Wire()))
	wire, isObj := decodeWire(t, data.Wire()).(map[string]any)
	if !isObj {
		t.Fatalf("fetch wire is not an object: %#v", wire)
	}
	if _, budget := wire["budget"]; budget {
		t.Fatal("a grouped run with no ordinary budget reported one")
	}
	var modeled Envelope
	unmarshalWire(t, env, &modeled)
	if modeled.Data == nil {
		t.Fatal("modeled envelope lost the fetch data")
	}
}

// TestRuntimeFetchPreviewOutputValidates proves the --preview payload the
// service renders (a) rides a valid envelope, (b) validates against the pinned
// previewData profile AND against both fetch data profiles — a preview data
// object carries no bundle key, so those profiles must accept the payload alone
// — and (c) round-trips through the modeled shape. The second withheld row
// carries no rename source, so the wire's omitted old_path is pinned against
// the profile too.
func TestRuntimeFetchPreviewOutputValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	data := &core.FetchData{Preview: &core.PreviewData{
		SchemaVersion: "1",
		Included:      []core.PreviewFile{{Path: "a.txt", Role: "docs", Bytes: 69}},
		Excluded: []core.ExcludedPath{
			{Path: ".env", OldPath: ".env", RuleID: "default:secret", Pattern: ".env", Reason: "secret-builtin", Bytes: 69},
			{Path: "vendor/lib.go", RuleID: "default:generated", Pattern: "vendor/**", Reason: "default", Bytes: 120},
		},
		IncludedBytes: 69,
		ExcludedBytes: 189,
		Files:         1,
		Budget:        core.PreviewBudget{Tier: "light", Reasons: []string{"docs-or-test-only"}, Finders: 1, CrossCheck: "off"},
	}}
	env := core.RenderEnvelope("fetch", "acme/app", "7",
		core.NowUTC(now), "preview only", nil, data.Wire(), nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))
	validateWire(t, doc, "preview data", defOf(t, doc, "previewData"), decodeWire(t, data.Preview.Wire()))
	validateWire(t, doc, "preview fetch data", defOf(t, doc, "fetchData"), decodeWire(t, data.Wire()))
	validateWire(t, doc, "preview fetch-local data", defOf(t, doc, "fetchLocalData"), decodeWire(t, data.Wire()))
	assertDataProfileRejectsIncomplete(t, doc)

	var modeled struct {
		Preview PreviewData `json:"preview"`
	}
	unmarshalWire(t, data.Wire(), &modeled)
	assertModeledPreview(t, &modeled.Preview)
}

// assertDataProfileRejectsIncomplete proves the fetch data profiles still
// refuse a payload that satisfies neither shape. Moving the required list into
// an anyOf must not turn the profiles into accept-anything: an object with no
// bundle key and no preview names no run this binary can produce, and a partial
// one would let a consumer read an absent field as a bundle's.
func assertDataProfileRejectsIncomplete(t *testing.T, doc map[string]any) {
	t.Helper()
	for _, def := range []string{"fetchData", "fetchLocalData"} {
		for _, payload := range []string{`{}`, `{"dir": ".verdict/state/1"}`} {
			var value map[string]any
			unmarshalJSON(t, []byte(payload), &value)
			if failures := schemacheck.Validate(doc, defOf(t, doc, def), value); len(failures) == 0 {
				t.Errorf("$defs/%s accepts %s; want both branches to refuse it", def, payload)
			}
		}
	}
}

// assertModeledPreview proves the payload round-trips field for field, dropped
// rename source included.
func assertModeledPreview(t *testing.T, p *PreviewData) {
	t.Helper()
	want := PreviewData{
		SchemaVersion: "1",
		Included:      []PreviewFile{{Path: "a.txt", Role: "docs", Bytes: 69}},
		Excluded: []ExcludedPath{
			{Path: ".env", OldPath: ".env", RuleID: "default:secret", Pattern: ".env", Reason: "secret-builtin", Bytes: 69},
			{Path: "vendor/lib.go", RuleID: "default:generated", Pattern: "vendor/**", Reason: "default", Bytes: 120},
		},
		IncludedBytes: 69,
		ExcludedBytes: 189,
		Files:         1,
		Budget:        PreviewBudget{Tier: "light", Reasons: []string{"docs-or-test-only"}, Finders: 1, CrossCheck: "off"},
	}
	if !reflect.DeepEqual(*p, want) {
		t.Fatalf("modeled preview = %+v; want %+v", *p, want)
	}
}
