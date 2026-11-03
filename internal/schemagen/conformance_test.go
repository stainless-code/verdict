package schemagen

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
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
	raw, err := os.ReadFile(filepath.Join(root, "schemas", name))
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

// validateWire validates v against schema, resolving $ref within doc.
func validateWire(t *testing.T, doc map[string]any, path string, schema, v any) {
	t.Helper()
	if err := schemaError(doc, schema, v); err != nil {
		t.Errorf("%s does not validate against the generated schema: %v", path, err)
	}
}

// schemaError validates one value, supporting the keywords the generated
// schemas use.
//
//nolint:gocognit,revive,cyclop // the recursive schema walk reads best as one switch
func schemaError(doc map[string]any, schema, v any) error {
	s, ok := schema.(map[string]any)
	if !ok || len(s) == 0 {
		return nil
	}
	if ref, ok := s["$ref"].(string); ok {
		return schemaError(doc, resolveRef(doc, ref), v)
	}
	if branches, ok := s["anyOf"].([]any); ok {
		for _, branch := range branches {
			if schemaError(doc, branch, v) == nil {
				return nil
			}
		}
		return fmt.Errorf("no anyOf branch matches %v", v)
	}
	if want, ok := s["const"]; ok && !reflect.DeepEqual(want, v) {
		return fmt.Errorf("const %v, got %v", want, v)
	}
	if enum, ok := s["enum"].([]any); ok && !enumContains(enum, v) {
		return fmt.Errorf("%v not in enum %v", v, enum)
	}
	if typ, ok := s["type"]; ok && !typeMatches(typ, v) {
		return fmt.Errorf("type %v, got %T (%v)", typ, v, v)
	}
	if m, ok := v.(map[string]any); ok {
		if err := objectError(doc, s, m); err != nil {
			return err
		}
	}
	if list, ok := v.([]any); ok {
		if items, ok := s["items"]; ok {
			for i, elem := range list {
				if err := schemaError(doc, items, elem); err != nil {
					return fmt.Errorf("[%d]: %w", i, err)
				}
			}
		}
	}
	return nil
}

// objectError validates an object value's properties and required keys.
//
//nolint:gocognit,revive // one pass over properties/required/additionalProperties
func objectError(doc map[string]any, s map[string]any, m map[string]any) error {
	props, _ := s["properties"].(map[string]any) //nolint:errcheck // absent properties means no per-key validation
	if required, ok := s["required"].([]any); ok {
		for _, raw := range required {
			key, ok := raw.(string)
			if !ok {
				return fmt.Errorf("required entry %v is not a string", raw)
			}
			if _, has := m[key]; !has {
				return fmt.Errorf("missing required key %q", key)
			}
		}
	}
	if additional, ok := s["additionalProperties"].(bool); ok && !additional {
		for key := range m {
			if _, known := props[key]; !known {
				return fmt.Errorf("unknown key %q", key)
			}
		}
	}
	for key, sub := range props {
		value, ok := m[key]
		if !ok {
			continue
		}
		if err := schemaError(doc, sub, value); err != nil {
			return fmt.Errorf(".%s: %w", key, err)
		}
	}
	return nil
}

// resolveRef resolves one local #/$defs/... reference.
func resolveRef(doc map[string]any, ref string) any {
	const prefix = "#/$defs/"
	if !strings.HasPrefix(ref, prefix) {
		return map[string]any{}
	}
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return defs[strings.TrimPrefix(ref, prefix)]
}

// enumContains reports whether v equals one enum member.
func enumContains(enum []any, v any) bool {
	for _, member := range enum {
		if reflect.DeepEqual(member, v) {
			return true
		}
	}
	return false
}

// typeMatches checks a JSON Schema type or type array against v.
//
//nolint:revive,cyclop // the type table reads best as one switch
func typeMatches(typ any, v any) bool {
	if list, ok := typ.([]any); ok {
		for _, one := range list {
			if typeMatches(one, v) {
				return true
			}
		}
		return false
	}
	name, ok := typ.(string)
	if !ok {
		return false
	}
	switch name {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		return isNumber(v)
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		if strings.ContainsAny(string(n), ".eE") {
			return false
		}
		_, err := strconv.ParseInt(string(n), 10, 64)
		return err == nil
	default:
		return false
	}
}

// isNumber reports whether v is a JSON number.
func isNumber(v any) bool {
	_, ok := v.(json.Number)
	return ok
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
