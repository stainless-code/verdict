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

func TestReportProvenanceWireGatingHumanTableAndDegrade(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/provenance.json", sampleProvenanceJSON)

	// Without --provenance: wire must omit provenance receipt & truncated array.
	plainResp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req})
	if ref != nil {
		t.Fatal(ref)
	}
	plainWire := jq.PrettyOrdered(plainResp.Data.Wire())
	if strings.Contains(plainWire, `"inputs"`) || strings.Contains(plainWire, `"abcd1234"`) {
		t.Fatalf("report without --provenance exposed provenance keys:\n%s", plainWire)
	}

	// With --provenance: wire exposes data.receipt.inputs[] and data.truncated[], human prints table.
	provResp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Provenance: true},
	})
	if ref != nil {
		t.Fatal(ref)
	}
	wireJSON := jq.PrettyOrdered(provResp.Data.Wire())
	var decoded struct {
		Receipt   core.ProvenanceReceipt        `json:"receipt"`
		Truncated []core.ProvenanceTruncatedRow `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(wireJSON), &decoded); err != nil {
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

	// Malformed provenance.json degrades cleanly with a warning.
	writeFile(t, ".verdict/state/5101/provenance.json", "{corrupt")
	badResp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Provenance: true},
	})
	if ref != nil {
		t.Fatalf("malformed provenance.json refused report: %v", ref)
	}
	if !strings.Contains(strings.Join(badResp.Warnings, "\n"), "provenance.json") {
		t.Fatalf("expected provenance.json warning on malformed file, got: %v", badResp.Warnings)
	}
}
