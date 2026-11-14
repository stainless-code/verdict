package postcore

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
)

const (
	postBundleDir = ".verdict/state/7"
	postLearnings = "- Never skip closing errors\n"
)

// bundleTruncation is the bundle's own capped-source receipt: the fetch
// returned 7 of 9 rows. Both report and post must copy these counts.
const bundleTruncation = `{
  "truncated": true,
  "truncation_reason": "response_cap",
  "returned": 7,
  "total": 9,
  "next_offset": null
}
`

// writePostProvenance writes a receipt through the real producer so the
// fixture keeps the shape fetch emits.
func writePostProvenance(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(postBundleDir+"/learnings.md", []byte(postLearnings), 0o644); err != nil {
		t.Fatal(err)
	}
	r := provenance.BuildReceipt(&provenance.ReceiptBuildInput{
		Dir:         postBundleDir,
		PanelSource: "built-in",
		Panel:       []string{"sonnet"},
		Learnings: &provenance.ControlInput{
			Path: ".verdict/learnings.md", Status: "used", Source: "base",
			SHA256: "5ae1e3a4bf9c0b0b5b7e9f6a3c0d1e2f3a4b5c6d7e8f9012345678", Bytes: int64(len(postLearnings)),
		},
		KB: &provenance.KBRecallInput{
			Candidates: []provenance.KBCandidateInput{
				{ID: "r-1", Status: "included"},
				{ID: "r-2", Status: "omitted", Reason: "top_n"},
			},
			Reason: "top_n", Included: 1, Omitted: 1, CharsIncluded: 40, CharsTotal: 40,
		},
	})
	if err := provenance.WriteReceipt(postBundleDir, r); err != nil {
		t.Fatal(err)
	}
}

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
	assertPostAbsentReceiptWarnsWithFlag(t)
	assertPostProvenanceEnabled(t)
	assertPostMalformedReceiptWarnsOnlyWithFlag(t)
}

func assertPostLegacyBundleUnchanged(t *testing.T) {
	t.Helper()
	oldResp, _, _ := runPostDryRun(t, false, true)
	if strings.Contains(requireFooterStr(t, oldResp.Data.Body.Footer), "> Inputs:") {
		t.Fatal("old bundle emitted Inputs footer")
	}
	oldWire := jq.PrettyOrdered(oldResp.Data.Wire())
	if strings.Contains(oldWire, `"inputs"`) {
		t.Fatalf("post without --provenance exposed receipt:\n%s", oldWire)
	}
}

func assertPostProvenanceEnabled(t *testing.T) {
	t.Helper()
	writePostProvenance(t)
	resp, _, _ := runPostDryRun(t, true, true)
	footerStr := requireFooterStr(t, resp.Data.Body.Footer)
	if got := strings.Count(footerStr, "\n> Inputs: "); got != 1 {
		t.Fatalf("expected 1 Inputs line, got %d in:\n%s", got, footerStr)
	}
	if got := strings.Count(footerStr, core.FooterDivider); got != 1 {
		t.Fatalf("expected 1 footer marker, got %d in:\n%s", got, footerStr)
	}
	decoded := decodePostProvenance(t, resp)
	assertPostProvenanceRows(t, &decoded.Receipt)
	_, _, human := runPostDryRun(t, true, false)
	if !strings.Contains(strings.Join(human, "\n"), "kb.txt") {
		t.Fatalf("expected human provenance table, got: %v", human)
	}
}

type postProvenanceWire struct {
	Receipt   core.ProvenanceReceipt        `json:"receipt"`
	Truncated []core.ProvenanceTruncatedRow `json:"truncated"`
}

func decodePostProvenance(t *testing.T, resp *core.PostResponse) postProvenanceWire {
	t.Helper()
	var decoded postProvenanceWire
	if err := json.Unmarshal([]byte(jq.PrettyOrdered(resp.Data.Wire())), &decoded); err != nil {
		t.Fatalf("unmarshal post wire: %v", err)
	}
	return decoded
}

func assertPostProvenanceRows(t *testing.T, receipt *core.ProvenanceReceipt) {
	t.Helper()
	if receipt.RunID == "" || receipt.PromptsHash == "" {
		t.Fatalf("post receipt lost its identity: %+v", receipt)
	}
	seen := map[string]bool{}
	for i := range receipt.Inputs {
		in := &receipt.Inputs[i]
		seen[in.Kind] = true
		assertPostProvenanceRow(t, in)
	}
	for _, want := range []string{"prompt", "panel", "bundle", "kb", "kb_candidate", "learnings", "config", "exclusions"} {
		if !seen[want] {
			t.Fatalf("post receipt missing kind %q", want)
		}
	}
}

func assertPostProvenanceRow(t *testing.T, in *core.ProvenanceInput) {
	t.Helper()
	if in.Kind == "kb" && (in.Status != "included" || *in.Included != 1 || *in.Omitted != 1) {
		t.Fatalf("kb row = %s included=%v omitted=%v", in.Status, in.Included, in.Omitted)
	}
	if in.Kind == "panel" && len(in.Members) != 1 {
		t.Fatalf("panel row members = %v", in.Members)
	}
}

func assertPostAbsentReceiptWarnsWithFlag(t *testing.T) {
	t.Helper()
	_, warnings, _ := runPostDryRun(t, true, true)
	if !strings.Contains(strings.Join(warnings, "\n"), "provenance receipt absent") {
		t.Fatalf("expected an absent-receipt warning under --provenance, got: %v", warnings)
	}
}

func assertPostMalformedReceiptWarnsOnlyWithFlag(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(postBundleDir+"/provenance.json", []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, plainWarnings, _ := runPostDryRun(t, false, true)
	if joined := strings.Join(plainWarnings, "\n"); strings.Contains(joined, "provenance") {
		t.Fatalf("post without --provenance warned about an unused receipt: %v", plainWarnings)
	}
	_, warnings, _ := runPostDryRun(t, true, true)
	if !strings.Contains(strings.Join(warnings, "\n"), "provenance.json") {
		t.Fatalf("expected provenance.json warning, got: %v", warnings)
	}
}

func TestPostProvenanceCopiesBundleTruncation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)
	writePostProvenance(t)
	if err := os.WriteFile(postBundleDir+"/truncation.json", []byte(bundleTruncation), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, _, _ := runPostDryRun(t, true, true)
	decoded := decodePostProvenance(t, resp)
	row := findBundleTruncationRow(t, decoded.Truncated)
	if row.Returned != 7 || row.Total == nil || *row.Total != 9 {
		t.Fatalf("bundle truncation row = returned %d total %v, want 7 and 9", row.Returned, row.Total)
	}
}

func findBundleTruncationRow(t *testing.T, rows []core.ProvenanceTruncatedRow) *core.ProvenanceTruncatedRow {
	t.Helper()
	for i := range rows {
		if rows[i].Input == "bundle" {
			return &rows[i]
		}
	}
	t.Fatalf("no bundle truncation row in %+v", rows)
	return nil
}
