package schemagen

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/schemacheck"
)

// TestRuntimeReportOutputValidates proves the runtime report response (a)
// validates against the generated envelope schema, including the reportData
// definition, and (b) round-trips through the modeled shapes field for
// field.
func TestRuntimeReportOutputValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	resp := runReportFixture(t)
	data := resp.Data.Wire()

	validateWire(t, doc, "$defs.reportData", defOf(t, doc, "reportData"), decodeWire(t, data))

	var modeled ReportData
	unmarshalWire(t, data, &modeled)
	compareReportData(t, resp.Data, &modeled)

	env := core.RenderEnvelope("report", "acme/app", "5101", resp.At, resp.Next,
		resp.Warnings, data, nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))

	var envelope Envelope
	unmarshalWire(t, env, &envelope)
	if envelope.Verb != "report" || envelope.Repo != "acme/app" {
		t.Errorf("envelope identity = %+v", envelope)
	}
	if envelope.Pr == nil || *envelope.Pr != 5101 {
		t.Errorf("envelope pr = %v", envelope.Pr)
	}
	if envelope.Data == nil || (*envelope.Data)["dir"] != ".review/5101" {
		t.Errorf("envelope data = %v", envelope.Data)
	}
	if envelope.Error != nil {
		t.Errorf("success envelope error = %+v", envelope.Error)
	}
}

// TestRuntimeRefusalOutputValidates proves a refusal envelope validates too.
func TestRuntimeRefusalOutputValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	env := core.RenderEnvelope("report", "acme/app", "5101", "2026-11-02T12:00:00Z", "",
		nil, nil, &core.SchemaError{Code: "corrupt-store", Message: "bad store"})
	validateWire(t, doc, "$", doc, decodeWire(t, env))
	var modeled Envelope
	unmarshalWire(t, env, &modeled)
	if modeled.Error == nil || modeled.Error.Code != "corrupt-store" {
		t.Errorf("error = %+v", modeled.Error)
	}
}

// runReportFixture builds a hermetic bundle and runs the report service.
func runReportFixture(t *testing.T) *core.ReportResponse {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixture(t, ".review/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFixture(t, ".review/5101/findings.json", "["+fixtureFinding()+"]")
	writeFixture(t, ".review/5101/post.json",
		`{"schema_version":"1","head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	writeFixture(t, ".review/5101/patch.json", `{"sig":"patch1","note":"hello"}`)
	writeFixture(t, ".review/5101/cost.json",
		`{"schema_version":"1","effort":"standard","models":[{"name":"m",`+
			`"input_tokens":100,"output_tokens":50}],"est_usd":0.05}`)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	runtime := &core.Runtime{Now: func() time.Time { return now }}
	read := core.ReadRequest{Runtime: runtime, Repo: "acme/app", Pr: "5101"}
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: read,
		Flags:       core.ReportFlags{Cost: true, Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	return resp
}

// fixtureFinding renders one valid store entry.
func fixtureFinding() string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Usable body.","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":"critical",` +
		`"sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[]}`
}

// writeFixture writes one fixture file, creating parents.
func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedControls snapshots the sandbox's controls into a bundle through the
// head opt-out, standing in for fetch's base resolution.
func seedControls(t *testing.T, bundleDir string) {
	t.Helper()
	req := controls.Request{BundleDir: bundleDir, BaseSHA: strings.Repeat("b", 40), AllowHead: true}
	if _, ref := controls.Resolve(t.Context(), nil, req); ref != nil {
		t.Fatal(ref)
	}
}

// loadSchemaDocument reads one committed schema as a generic document.
func loadSchemaDocument(t *testing.T, name string) map[string]any {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "skills", "verdict", "schemas", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return doc
}

// defOf returns one $defs entry.
func defOf(t *testing.T, doc map[string]any, name string) any {
	t.Helper()
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no $defs")
	}
	def, ok := defs[name]
	if !ok {
		t.Fatalf("schema has no $defs.%s", name)
	}
	return def
}

// unmarshalWire renders an ordered wire value to JSON and decodes it into
// out with number fidelity.
func unmarshalWire(t *testing.T, wire any, out any) {
	t.Helper()
	raw := []byte(wireJSON(t, wire))
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		t.Fatalf("unmarshal wire: %v\n%s", err, raw)
	}
}

// decodeWire renders and re-decodes an ordered wire value.
func decodeWire(t *testing.T, wire any) any {
	t.Helper()
	var v any
	unmarshalWire(t, wire, &v)
	return v
}

// wireJSON renders one ordered wire value as jq-pretty JSON.
func wireJSON(t *testing.T, wire any) string {
	t.Helper()
	if obj, ok := wire.(interface{ MarshalPretty() string }); ok {
		return obj.MarshalPretty()
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal wire: %v", err)
	}
	return string(raw)
}

// validateWire validates v against schema through the production checker
// (internal/schemacheck), the same code the shipped `schemas/validate.sh`
// runs, so a fixture cannot pass one side and fail the other.
func validateWire(t *testing.T, doc map[string]any, path string, schema, v any) {
	t.Helper()
	for _, failure := range schemacheck.Validate(doc, schema, v) {
		t.Errorf("%s does not validate against the generated schema: %v", path, failure)
	}
}

// validationError renders the first failure of one schema check as an error,
// so a caller that wants a single error keeps the pre-promotion signature.
func validationError(doc, schema, value any) error {
	failures := schemacheck.Validate(doc, schema, value)
	if len(failures) == 0 {
		return nil
	}
	return errors.New(failures[0].String())
}

// TestEnvelopeRejectsBadTimestamp pins the acceptance case the test-only
// validator used to accept: an envelope whose `at` is not RFC 3339 fails the
// conformance path, exactly as the shipped checker refused it.
func TestEnvelopeRejectsBadTimestamp(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	env := core.RenderEnvelope("report", "acme/app", "5101", "2026-11-02T12:00:00Z", "",
		nil, nil, nil)
	valid := decodeWire(t, env)
	if err := validationError(doc, doc, valid); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
	object, ok := valid.(map[string]any)
	if !ok {
		t.Fatalf("decoded envelope = %T; want an object", valid)
	}
	object["at"] = "not-a-date"
	if err := validationError(doc, doc, object); err == nil {
		t.Fatal("an envelope with at=not-a-date validates; want a date-time rejection")
	}
}

// TestUsageRejectsNegativeUsd pins the other acceptance case: a usage record
// with a negative measured price is rejected, which the test-only validator
// accepted because it ignored `minimum`.
func TestUsageRejectsNegativeUsd(t *testing.T) {
	doc := loadSchemaDocument(t, "usage.schema.json")
	record := `{"schema_version":"1","source":"claude-code",` +
		`"started_at":"2026-11-02T12:00:00Z","ended_at":"2026-11-02T13:00:00Z"`
	good := decodeJSONText(t, record+`,"usd":0.25}`)
	if err := validationError(doc, doc, good); err != nil {
		t.Fatalf("a priced record is rejected: %v", err)
	}
	bad := decodeJSONText(t, record+`,"usd":-1}`)
	err := validationError(doc, doc, bad)
	if err == nil || !strings.Contains(err.Error(), "below minimum") {
		t.Fatalf("usd=-1 = %v; want a minimum rejection", err)
	}
}

// decodeJSONText decodes one JSON literal with number fidelity.
func decodeJSONText(t *testing.T, text string) any {
	t.Helper()
	value, err := schemacheck.Decode([]byte(text))
	if err != nil {
		t.Fatalf("decode %s: %v", text, err)
	}
	return value
}

// compareReportData asserts every modeled report field round-trips.
//
//nolint:gocognit,revive,cyclop // one assertion block per modeled field group
func compareReportData(t *testing.T, got *core.ReportData, modeled *ReportData) {
	t.Helper()
	if modeled.GeneratedAt != got.GeneratedAt || modeled.Dir != got.Dir {
		t.Errorf("identity round-trip = %+v", modeled)
	}
	if !reflect.DeepEqual(modeled.Head, got.Head) {
		t.Errorf("head = %+v, want %+v", modeled.Head, got.Head)
	}
	if modeled.Verdict != got.Verdict || modeled.Counts != got.Counts {
		t.Errorf("tallies = %+v %+v", modeled.Verdict, modeled.Counts)
	}
	if !reflect.DeepEqual(modeled.Panel, got.Panel) {
		t.Errorf("panel = %+v, want %+v", modeled.Panel, got.Panel)
	}
	if !reflect.DeepEqual(modeled.Filters, got.Filters) {
		t.Errorf("filters = %+v", modeled.Filters)
	}
	if modeled.Output == nil != (got.Output == nil) {
		t.Errorf("output = %+v, want %+v", modeled.Output, got.Output)
	}
	if len(modeled.Items) != len(got.Items) {
		t.Fatalf("items = %d, want %d", len(modeled.Items), len(got.Items))
	}
	for i := range got.Items {
		want, have := got.Items[i], modeled.Items[i]
		if have.Sig != want.Sig || have.Status != want.Status || have.Locator != want.Locator {
			t.Errorf("item %d = %+v, want %+v", i, have, want)
		}
	}
	if got.Cost == nil {
		t.Fatal("fixture lost its cost rollup")
	}
	if modeled.Cost.Runs != got.Cost.Runs ||
		modeled.Cost.InputTokens != got.Cost.InputTokens ||
		modeled.Cost.TotalTokens != got.Cost.TotalTokens ||
		!reflect.DeepEqual(modeled.Cost.ByTier, got.Cost.ByTier) {
		t.Errorf("cost = %+v, want %+v", modeled.Cost, got.Cost)
	}
	if got.Completeness == nil || !reflect.DeepEqual(modeled.Completeness, *got.Completeness) {
		t.Errorf("completeness = %+v, want %+v", modeled.Completeness, got.Completeness)
	}
}

// TestRuntimeThreadsOutputValidates proves a normalized thread feed (the
// shape fetch writes) validates against the generated threads schema.
func TestRuntimeThreadsOutputValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "threads.schema.json")
	line, side := 3, "RIGHT"
	sig := strings.Repeat("a", 40)
	turl := "https://example.invalid/comment/10"
	sample := ThreadsFile{
		SchemaVersion: "1",
		Provider:      "github",
		PR:            4701,
		HeadSHA:       strings.Repeat("b", 40),
		FetchedAt:     "2026-11-03T12:00:00Z",
		Threads: []Thread{{
			ID: "PRRT_10", Path: "a.go", Line: &line, Side: &side,
			URL: &turl, Author: "ana", AuthorKind: "human",
			FindingSig: &sig, Kind: "review-comment",
			Comments: []ThreadComment{{
				ID: "10", Author: "ana", AuthorKind: "human",
				Body: "nit", Truncated: false,
				CreatedAt: "2026-11-03T12:00:00Z",
			}},
		}},
	}
	validateWire(t, doc, "$", doc, decodeWire(t, sample))
}
