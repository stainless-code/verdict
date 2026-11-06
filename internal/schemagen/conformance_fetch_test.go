package schemagen

import (
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
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
