package reportcore_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
)

const sampleProvenanceJSON = `{
  "schema_version": "1",
  "run_id": "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
  "prompts_hash": "1234567abcdef",
  "panel": ["opus"],
  "inputs": [
    {"kind": "prompt", "name": "SKILL.md", "status": "included", "sha256": "deadbeef", "bytes": 100},
    {"kind": "kb", "name": "kb.txt", "status": "truncated", "reason": "cap", "included": 3, "omitted": 1, "chars_included": 80, "chars_total": 150, "cap": 100},
    {"kind": "kb_candidate", "name": "r-extra", "id": "r-extra", "status": "omitted", "reason": "top_n"},
    {"kind": "learnings", "name": ".verdict/learnings.md", "status": "included"}
  ],
  "truncated": [
    {"input": "kb", "status": "truncated", "reason": "cap", "truncated": true, "truncation_reason": "response_cap", "returned": 80, "total": 150, "next_offset": null, "chars_included": 80, "chars_total": 150, "cap": 100},
    {"input": "kb_candidate", "id": "r-extra", "status": "omitted", "reason": "top_n", "truncated": true, "truncation_reason": "response_cap", "returned": 3, "total": 4, "next_offset": null, "included": 3, "omitted": 1}
  ]
}`

func runReportChecked(t *testing.T, req core.ReadRequest, flags *core.ReportFlags) *core.ReportResponse {
	t.Helper()
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req, Flags: *flags})
	if ref != nil {
		t.Fatalf("report refused: %s: %s", ref.Code, ref.Message)
	}
	return resp
}

func TestReportProvenanceWireGatingHumanTableAndDegrade(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/provenance.json", sampleProvenanceJSON)

	plainResp := runReportChecked(t, req, &core.ReportFlags{})
	plainWire := jq.PrettyOrdered(plainResp.Data.Wire())
	if strings.Contains(plainWire, `"inputs"`) || strings.Contains(plainWire, `"abcd1234"`) {
		t.Fatalf("report without --provenance exposed provenance keys:\n%s", plainWire)
	}

	assertReportProvenanceIncluded(t, req)

	writeFile(t, ".verdict/state/5101/provenance.json", "{corrupt")
	badResp := runReportChecked(t, req, &core.ReportFlags{Provenance: true})
	if !strings.Contains(strings.Join(badResp.Warnings, "\n"), "provenance.json") {
		t.Fatalf("expected provenance.json warning on malformed file, got: %v", badResp.Warnings)
	}
}

func assertReportProvenanceIncluded(t *testing.T, req core.ReadRequest) {
	t.Helper()
	provResp := runReportChecked(t, req, &core.ReportFlags{Provenance: true})
	var decoded struct {
		Receipt   core.ProvenanceReceipt        `json:"receipt"`
		Truncated []core.ProvenanceTruncatedRow `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(jq.PrettyOrdered(provResp.Data.Wire())), &decoded); err != nil {
		t.Fatalf("unmarshal report wire: %v", err)
	}
	if len(decoded.Receipt.Inputs) != 4 || len(decoded.Truncated) != 2 {
		t.Fatalf("wire provenance mismatch: inputs=%d truncated=%d", len(decoded.Receipt.Inputs), len(decoded.Truncated))
	}
	humanText := strings.Join(provResp.Human, "\n")
	for _, want := range []string{"kb.txt", "r-extra", "learnings"} {
		if !strings.Contains(humanText, want) {
			t.Fatalf("human provenance output missing %q:\n%s", want, humanText)
		}
	}
}
