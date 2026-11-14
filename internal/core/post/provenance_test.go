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

func runPostDryRun(t *testing.T, prov, jsonMode bool) (*core.PostResponse, []string, []string) {
	t.Helper()
	var warnings, human []string
	flags := core.PostFlags{DryRun: true, Approve: "false", ReviewFile: ".verdict/state/7/review.json", Provenance: prov}
	req := postTestReq(postCommander(t), flags, &warnings, &human)
	req.JSONMode = jsonMode
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("dry-run failed: %s: %s", ref.Code, ref.Message)
	}
	return resp, warnings, human
}

func requireFooterStr(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("expected string footer, got %T", v)
	}
	return s
}

func TestPostProvenanceDryRunWireFooterAndDegrade(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)

	assertPostLegacyBundleUnchanged(t)
	assertPostProvenanceEnabled(t)
	assertPostMalformedReceiptWarns(t)
}

func assertPostLegacyBundleUnchanged(t *testing.T) {
	t.Helper()
	oldResp, _, _ := runPostDryRun(t, false, true)
	if strings.Contains(requireFooterStr(t, oldResp.Data.Body.Footer), "> Inputs:") {
		t.Fatal("old bundle emitted Inputs footer")
	}
	oldWire := jq.PrettyOrdered(oldResp.Data.Wire())
	if strings.Contains(oldWire, `"inputs"`) || strings.Contains(oldWire, `"99998888"`) {
		t.Fatalf("post without --provenance exposed receipt:\n%s", oldWire)
	}
}

func assertPostProvenanceEnabled(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(".verdict/state/7/provenance.json", []byte(postSampleProvenance), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, _, _ := runPostDryRun(t, true, true)
	footerStr := requireFooterStr(t, resp.Data.Body.Footer)
	if strings.Count(footerStr, "> Inputs:") != 1 || strings.Count(footerStr, "<!-- verdict-footer -->") != 1 {
		t.Fatalf("unexpected footer:\n%s", footerStr)
	}
	var decoded struct {
		Receipt   core.ProvenanceReceipt        `json:"receipt"`
		Truncated []core.ProvenanceTruncatedRow `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(jq.PrettyOrdered(resp.Data.Wire())), &decoded); err != nil {
		t.Fatalf("unmarshal post wire: %v", err)
	}
	if decoded.Receipt.RunID == "" || len(decoded.Receipt.Inputs) != 4 || decoded.Truncated == nil {
		t.Fatalf("post wire provenance incomplete: %+v", decoded)
	}
	_, _, human := runPostDryRun(t, true, false)
	if !strings.Contains(strings.Join(human, "\n"), "kb.txt") {
		t.Fatalf("expected human provenance table, got: %v", human)
	}
}

func assertPostMalformedReceiptWarns(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(".verdict/state/7/provenance.json", []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, warnings, _ := runPostDryRun(t, true, true)
	if !strings.Contains(strings.Join(warnings, "\n"), "provenance.json") {
		t.Fatalf("expected provenance.json warning, got: %v", warnings)
	}
}
