package schemagen

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
	"github.com/stainless-code/verdict/internal/jq"
)

// fixedNow is the conformance clock: the zero instant renders as the
// deterministic 0000-00-00T00:00:00Z envelope timestamp.
func fixedNow() time.Time { return time.Time{} }

// fixedRuntime builds the hermetic service runtime.
func fixedRuntime() *core.Runtime {
	return &core.Runtime{Now: fixedNow}
}

// TestRuntimeTriageEnvelopeValidates proves the triage response envelope
// validates against the generated envelope schema and carries the typed
// data profile byte-identically.
func TestRuntimeTriageEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/4251/findings.json", "["+fixtureFinding()+"]")
	req := &triagecore.Request{
		Sig:       "c111",
		Status:    "addressed",
		Rationale: "verified the note now carries the missing guard clause here",
	}
	req.ReadRequest = core.ReadRequest{Runtime: fixedRuntime(), Repo: "acme/app", Pr: "4251"}
	resp, ref := triagecore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("triage refusal: %v", ref)
	}
	data := resp.Data.Wire()
	assertEnvelope(t, doc, "triage", "4251", resp.At, resp.Next, resp.Warnings, data)
}

// TestRuntimeAdoptEnvelopeValidates proves the adopt response envelope
// validates against the generated envelope schema and reports the typed
// handoff counts.
func TestRuntimeAdoptEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/4311/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".review/4311/full.diff", fixtureDiff())
	writeFixture(t, ".review/local-adopt1/findings.json", "["+fixtureFinding()+"]")
	req := &adoptcore.Request{From: "adopt1"}
	req.ReadRequest = core.ReadRequest{Runtime: fixedRuntime(), Repo: "acme/app", Pr: "4311"}
	resp, ref := adoptcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("adopt refusal: %v", ref)
	}
	data := resp.Data.Wire()
	assertEnvelope(t, doc, "adopt", "4311", resp.At, resp.Next, resp.Warnings, data)
}

// assertEnvelope validates one rendered envelope against the generated
// schema and round-trips it through the modeled shape.
func assertEnvelope(t *testing.T, doc map[string]any, verb, pr, at, next string, warnings []string, data any) {
	t.Helper()
	env := core.RenderEnvelope(verb, "acme/app", pr, at, next, warnings, data, nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))

	var modeled Envelope
	unmarshalWire(t, env, &modeled)
	if modeled.Verb != verb || modeled.Repo != "acme/app" {
		t.Errorf("envelope identity = %+v", modeled)
	}
	if modeled.Error != nil {
		t.Errorf("success envelope error = %+v", modeled.Error)
	}
	if modeled.Data == nil || !reflect.DeepEqual(*modeled.Data, wireMap(t, data)) {
		t.Errorf("envelope data = %v, want %v", modeled.Data, wireMap(t, data))
	}
}

// wireMap decodes an ordered wire value into a plain map with number
// fidelity.
func wireMap(t *testing.T, wire any) map[string]any {
	t.Helper()
	var out map[string]any
	unmarshalWire(t, wire, &out)
	return out
}

// fixtureDiff is the patch both adopt fixtures share.
func fixtureDiff() string {
	return "diff --git a/notes.txt b/notes.txt\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/notes.txt\n" +
		"+++ b/notes.txt\n" +
		"@@ -1,3 +1,3 @@\n" +
		" top\n" +
		"-old line\n" +
		"+new line\n" +
		" bottom\n"
}

// TestCostRecordRoundTrips proves a bundle's cost.json round-trips through
// the core CostRecord shape field for field and validates against the
// generated cost schema.
//
//nolint:revive,cyclop // one assertion block per modeled field group
func TestCostRecordRoundTrips(t *testing.T) {
	doc := loadSchemaDocument(t, "cost.schema.json")
	raw := []byte(`{"schema_version":"1","effort":"standard","models":[` +
		`{"name":"m","input_tokens":100,"output_tokens":50,` +
		`"cache_read_input_tokens":10,"cache_creation_input_tokens":5}],` +
		`"est_usd":0.05,"wall_seconds":12.5,"diff_bytes":2048,"lenses":["correctness"]}`)
	ordered, err := jq.DecodeOrdered(raw)
	if err != nil {
		t.Fatalf("decode cost.json: %v", err)
	}
	validateWire(t, doc, "$defs.record", defOf(t, doc, "record"), decodeWire(t, ordered))

	var modeled CostRecord
	unmarshalWire(t, ordered, &modeled)
	if modeled.SchemaVersion != "1" || modeled.Effort != "standard" {
		t.Errorf("identity = %+v", modeled)
	}
	if len(modeled.Models) != 1 || modeled.Models[0].Name != "m" {
		t.Fatalf("models = %+v", modeled.Models)
	}
	if modeled.Models[0].InputTokens != 100 || modeled.Models[0].OutputTokens != 50 {
		t.Errorf("tokens = %+v", modeled.Models[0])
	}
	if got := modeled.Models[0].CacheReadInputTokens; got == nil || *got != 10 {
		t.Errorf("cache read = %v", got)
	}
	if modeled.EstUSD == nil || *modeled.EstUSD != 0.05 {
		t.Errorf("est_usd = %v", modeled.EstUSD)
	}
	if got, want := wireJSON(t, core.Wire(&modeled)), wireJSON(t, ordered); got != want {
		t.Errorf("cost round-trip differs:\n got %s\nwant %s", got, want)
	}
}
