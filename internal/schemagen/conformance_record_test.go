package schemagen

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestRuntimeRecordOutputValidates proves the runtime record response (a)
// validates against the generated envelope schema, including the recordData
// definition, and (b) round-trips through the modeled shapes field for
// field.
func TestRuntimeRecordOutputValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	resp := runRecordFixture(t)
	data := resp.Data.Wire()

	validateWire(t, doc, "$defs.recordData", defOf(t, doc, "recordData"), decodeWire(t, data))

	var modeled RecordData
	unmarshalWire(t, data, &modeled)
	compareRecordData(t, resp.Data, &modeled)

	env := core.RenderEnvelope("record", "acme/app", "local-rr", resp.At, resp.Next,
		resp.Warnings, data, nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))

	var envelope Envelope
	unmarshalWire(t, env, &envelope)
	if envelope.Verb != "record" || envelope.Repo != "acme/app" {
		t.Errorf("envelope identity = %+v", envelope)
	}
	if envelope.Pr != nil {
		t.Errorf("envelope pr = %v, want null", envelope.Pr)
	}
	if envelope.Data == nil || (*envelope.Data)["dir"] != ".review/local-rr" {
		t.Errorf("envelope data = %v", envelope.Data)
	}
	if envelope.Error != nil {
		t.Errorf("success envelope error = %+v", envelope.Error)
	}
}

// TestRecordArtifactValidates proves the written record.json validates
// against the generated record schema and round-trips through the modeled
// shape.
func TestRecordArtifactValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "record.schema.json")
	runRecordFixture(t)
	raw, err := os.ReadFile(".review/local-rr/record.json")
	if err != nil {
		t.Fatalf("read record.json: %v", err)
	}
	var v any
	unmarshalJSON(t, raw, &v)
	validateWire(t, doc, "$", doc, v)

	var modeled Record
	unmarshalJSON(t, raw, &modeled)
	if modeled.SchemaVersion != "1" || modeled.Head != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("record identity = %+v", modeled)
	}
	if modeled.Findings != 1 || modeled.Open != 1 {
		t.Errorf("record tallies = %+v", modeled)
	}
	if modeled.Confidence == nil || *modeled.Confidence != 5 {
		t.Errorf("record confidence = %+v", modeled.Confidence)
	}
}

// TestFindingsUnknownKeysConform proves a findings entry with unknown keys
// round-trips through the typed shape with its bytes and key order intact,
// and validates against the generated findings schema.
func TestFindingsUnknownKeysConform(t *testing.T) {
	doc := loadSchemaDocument(t, "findings.schema.json")
	entry := `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Usable body.","suggestion":null,"kb":null,` +
		`"inBounds":true,"rigor":"walked","severity":"critical",` +
		`"sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[{"at":"2026-11-02T12:00:00Z","event":"reported"}],` +
		`"future_key":{"nested":true},"zzz":1}`
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/local-rr/findings.json", "["+entry+"]")
	raw, err := os.ReadFile(".review/local-rr/findings.json")
	if err != nil {
		t.Fatal(err)
	}
	var docs []any
	unmarshalJSON(t, raw, &docs)
	if len(docs) != 1 {
		t.Fatalf("entries = %d", len(docs))
	}
	validateWire(t, doc, "$", doc, docs)

	ordered, err := decodeOrdered(t, entry)
	if err != nil {
		t.Fatal(err)
	}
	wired := core.FindingEntryOf(ordered).Wire()
	if got, want := wireJSON(t, wired)+"\n", wirePretty(t, entry); got != want {
		t.Errorf("unknown-key round-trip differs:\n%s", got)
	}
	var modeled FindingEntry
	unmarshalWire(t, wired, &modeled)
	if modeled.Sig != "c111111111111111111111111111111111111111" || modeled.Status != "open" {
		t.Errorf("modeled entry = %+v", modeled)
	}
}

// runRecordFixture builds a hermetic local bundle and runs the record
// service.
func runRecordFixture(t *testing.T) *core.RecordResponse {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/local-rr/source.json",
		`{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`)
	writeFixture(t, ".review/local-rr/full.diff",
		"diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n"+
			"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n")
	writeFixture(t, ".review/local-rr/review.json",
		`{"schema_version":"1","body":"> ✅ Record conformance check with enough length.",`+
			`"approved":false,"confidence":5,"security_summary":"short summary",`+
			`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT",`+
			`"body":"Usable body with enough substance.","rigor":"walked","severity":"critical"}]}`)
	seedControls(t, ".review/local-rr")
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	runtime := &core.Runtime{Now: func() time.Time { return now }}
	read := core.ReadRequest{Runtime: runtime, Repo: "acme/app", Pr: "local-rr"}
	resp, ref := recordcore.Run(context.Background(), &core.RecordRequest{
		ReadRequest: read,
		Effective:   core.Defaults(),
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	return resp
}

// unmarshalJSON decodes raw JSON with number fidelity into out.
func unmarshalJSON(t *testing.T, raw []byte, out any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
}

// decodeOrdered decodes one JSON object with key order preserved.
func decodeOrdered(t *testing.T, entry string) (*jq.Obj, error) {
	t.Helper()
	doc, err := jq.DecodeOrdered([]byte(entry))
	if err != nil {
		return nil, err
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		t.Fatalf("decoded to %T", doc)
	}
	return obj, nil
}

// wirePretty renders one compact entry as jq-pretty JSON.
func wirePretty(t *testing.T, entry string) string {
	t.Helper()
	doc, err := jq.DecodeOrdered([]byte(entry))
	if err != nil {
		t.Fatal(err)
	}
	return jq.PrettyOrdered(doc)
}

// compareRecordData asserts every modeled record field round-trips.
//
//nolint:gocognit,revive,cyclop // one assertion block per modeled field group
func compareRecordData(t *testing.T, got *core.RecordData, modeled *RecordData) {
	t.Helper()
	if modeled.Dir != got.Dir || modeled.Findings != got.Findings {
		t.Errorf("identity = %+v, want %+v", modeled, got)
	}
	if modeled.Anchors != got.Anchors {
		t.Errorf("anchors = %+v, want %+v", modeled.Anchors, got.Anchors)
	}
	if (modeled.Confidence == nil) != (got.Confidence == nil) ||
		(modeled.Confidence != nil && *modeled.Confidence != *got.Confidence) {
		t.Errorf("confidence = %+v, want %+v", modeled.Confidence, got.Confidence)
	}
	if (modeled.SecuritySummary == nil) != (got.SecuritySummary == nil) ||
		(modeled.SecuritySummary != nil && *modeled.SecuritySummary != *got.SecuritySummary) {
		t.Errorf("security_summary = %+v, want %+v", modeled.SecuritySummary, got.SecuritySummary)
	}
	if len(modeled.Items) != len(got.Items) {
		t.Fatalf("items = %d, want %d", len(modeled.Items), len(got.Items))
	}
	for i := range got.Items {
		want, have := got.Items[i], modeled.Items[i]
		if have.Sig != want.Sig || have.Status != want.Status || have.Locator != want.Locator {
			t.Errorf("item %d = %+v, want %+v", i, have, want)
		}
		if !reflect.DeepEqual(have.Anchored, want.Anchored) {
			t.Errorf("item %d anchored = %v, want %v", i, have.Anchored, want.Anchored)
		}
	}
	if !strings.HasPrefix(modeled.Dir, ".review/") {
		t.Errorf("dir = %q", modeled.Dir)
	}
}
