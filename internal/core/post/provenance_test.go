package postcore

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const postSampleProvenance = `{
  "schema_version": "1",
  "run_id": "9999888877776666555544443333222211110000aaaabbbbccccddddeeeeffff",
  "prompts_hash": "abcdef1234567",
  "panel": ["sonnet"],
  "inputs": [
    {"kind": "prompt", "name": "SKILL.md", "status": "included", "sha256": "aaa", "bytes": 10},
    {"kind": "kb", "name": "kb.txt", "status": "included", "included": 1, "omitted": 0},
    {"kind": "learnings", "name": ".verdict/learnings.md", "status": "included"},
    {"kind": "config", "name": "defaults", "status": "included", "layer": "repo"}
  ],
  "truncated": []
}`

func TestPostProvenanceDryRunWireFooterAndDegrade(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)

	// Old bundle without provenance.json: no Inputs footer line.
	var warnings, human []string
	oldReq := postTestReq(postCommander(t), core.PostFlags{DryRun: true, Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	oldResp, ref := Run(context.Background(), oldReq)
	if ref != nil {
		t.Fatalf("old dry-run failed: %v", ref)
	}
	oldFooter, _ := oldResp.Data.Body.Footer.(string)
	if strings.Contains(oldFooter, "> Inputs:") {
		t.Fatalf("old bundle emitted Inputs footer:\n%s", oldFooter)
	}
	oldWire := jq.PrettyOrdered(oldResp.Data.Wire())
	if strings.Contains(oldWire, `"inputs"`) || strings.Contains(oldWire, `"99998888"`) {
		t.Fatalf("post without --provenance exposed receipt:\n%s", oldWire)
	}

	// Bundle with provenance.json and --provenance: one Inputs line in footer, receipt & truncated in wire, table printed.
	if err := os.WriteFile(".verdict/state/7/provenance.json", []byte(postSampleProvenance), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings, human = nil, nil
	req := postTestReq(postCommander(t), core.PostFlags{DryRun: true, Approve: "false", ReviewFile: ".verdict/state/7/review.json", Provenance: true}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("dry-run with provenance failed: %v", ref)
	}
	footerStr, _ := resp.Data.Body.Footer.(string)
	if got := strings.Count(footerStr, "> Inputs:"); got != 1 {
		t.Fatalf("footer Inputs line count = %d, want 1 in:\n%s", got, footerStr)
	}
	if got := strings.Count(footerStr, "<!-- verdict-footer -->"); got != 1 {
		t.Fatalf("expected single verdict-footer marker, got %d in:\n%s", got, footerStr)
	}
	wireJSON := jq.PrettyOrdered(resp.Data.Wire())
	var decoded struct {
		Receipt   core.ProvenanceReceipt        `json:"receipt"`
		Truncated []core.ProvenanceTruncatedRow `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(wireJSON), &decoded); err != nil {
		t.Fatalf("unmarshal post wire: %v", err)
	}
	if decoded.Receipt.RunID == "" || len(decoded.Receipt.Inputs) != 4 || decoded.Truncated == nil {
		t.Fatalf("post wire provenance incomplete: %+v", decoded)
	}

	// Human mode prints table when --provenance is set.
	warnings, human = nil, nil
	humanReq := postTestReq(postCommander(t), core.PostFlags{DryRun: true, Approve: "false", ReviewFile: ".verdict/state/7/review.json", Provenance: true}, &warnings, &human)
	humanReq.JSONMode = false
	if _, ref := Run(context.Background(), humanReq); ref != nil {
		t.Fatalf("human dry-run failed: %v", ref)
	}
	if !strings.Contains(strings.Join(human, "\n"), "kb.txt") {
		t.Fatalf("expected human provenance table, got: %v", human)
	}

	// Malformed provenance.json warns and still completes dry-run.
	if err := os.WriteFile(".verdict/state/7/provenance.json", []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings, human = nil, nil
	badReq := postTestReq(postCommander(t), core.PostFlags{DryRun: true, Approve: "false", ReviewFile: ".verdict/state/7/review.json", Provenance: true}, &warnings, &human)
	if _, ref := Run(context.Background(), badReq); ref != nil {
		t.Fatalf("malformed provenance failed post: %v", ref)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "provenance.json") {
		t.Fatalf("expected provenance.json warning, got: %v", warnings)
	}
}
