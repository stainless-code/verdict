package reportcore_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
)

const (
	reportBundleDir = ".verdict/state/5101"
	reportLearnings = "- Never skip closing errors\n"
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

// writeReportProvenance writes a receipt through the real producer so the
// fixture keeps the shape fetch emits.
func writeReportProvenance(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(reportBundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportBundleDir, "learnings.md"), []byte(reportLearnings), 0o644); err != nil {
		t.Fatal(err)
	}
	r := provenance.BuildReceipt(&provenance.ReceiptBuildInput{
		Dir:         reportBundleDir,
		PanelSource: "built-in",
		Panel:       []string{"opus"},
		Learnings: &provenance.ControlInput{
			Path: ".verdict/learnings.md", Status: "used", Source: "base",
			SHA256: "5ae1e3a4bf9c0b0b5b7e9f6a3c0d1e2f3a4b5c6d7e8f9012345678", Bytes: int64(len(reportLearnings)),
		},
		KB: &provenance.KBRecallInput{
			Candidates: []provenance.KBCandidateInput{
				{ID: "r-1", Status: "included"},
				{ID: "r-2", Status: "included"},
				{ID: "r-3", Status: "included"},
				{ID: "r-extra", Status: "omitted", Reason: "top_n"},
			},
			Reason: "top_n", Included: 3, Omitted: 1, CharsIncluded: 80, CharsTotal: 80,
		},
	})
	if err := provenance.WriteReceipt(reportBundleDir, r); err != nil {
		t.Fatal(err)
	}
}

func runReportChecked(t *testing.T, req core.ReadRequest, flags *core.ReportFlags) *core.ReportResponse {
	t.Helper()
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req, Flags: *flags})
	if ref != nil {
		t.Fatalf("report refused: %s: %s", ref.Code, ref.Message)
	}
	return resp
}

type reportProvenanceWire struct {
	Receipt   core.ProvenanceReceipt        `json:"receipt"`
	Truncated []core.ProvenanceTruncatedRow `json:"truncated"`
}

func decodeReportProvenance(t *testing.T, resp *core.ReportResponse) reportProvenanceWire {
	t.Helper()
	var decoded reportProvenanceWire
	if err := json.Unmarshal([]byte(jq.PrettyOrdered(resp.Data.Wire())), &decoded); err != nil {
		t.Fatalf("unmarshal report wire: %v", err)
	}
	return decoded
}

func TestReportProvenanceWireGatingHumanTableAndDegrade(t *testing.T) {
	req := fixture(t, "[]")
	writeReportProvenance(t)

	plainResp := runReportChecked(t, req, &core.ReportFlags{})
	plainWire := jq.PrettyOrdered(plainResp.Data.Wire())
	if strings.Contains(plainWire, `"inputs"`) || strings.Contains(plainWire, `"receipt"`) {
		t.Fatalf("report without --provenance exposed provenance keys:\n%s", plainWire)
	}

	assertReportProvenanceIncluded(t, req)

	writeFile(t, reportBundleDir+"/provenance.json", "{corrupt")
	badResp := runReportChecked(t, req, &core.ReportFlags{Provenance: true})
	if !strings.Contains(strings.Join(badResp.Warnings, "\n"), "provenance.json") {
		t.Fatalf("expected provenance.json warning on malformed file, got: %v", badResp.Warnings)
	}
}

func assertReportProvenanceIncluded(t *testing.T, req core.ReadRequest) {
	t.Helper()
	provResp := runReportChecked(t, req, &core.ReportFlags{Provenance: true})
	decoded := decodeReportProvenance(t, provResp)
	assertReportProvenanceRows(t, &decoded)
	humanText := strings.Join(provResp.Human, "\n")
	for _, want := range []string{"kb.txt", "r-extra", "learnings"} {
		if !strings.Contains(humanText, want) {
			t.Fatalf("human provenance output missing %q:\n%s", want, humanText)
		}
	}
}

func assertReportProvenanceRows(t *testing.T, decoded *reportProvenanceWire) {
	t.Helper()
	if decoded.Receipt.RunID == "" {
		t.Fatalf("report receipt lost its identity: %+v", decoded.Receipt)
	}
	kbRows, candidates := countReportRows(t, decoded.Receipt.Inputs)
	if kbRows != 1 || candidates != 4 {
		t.Fatalf("report receipt rows = %d kb, %d candidates; want 1 and 4", kbRows, candidates)
	}
	if row := findTruncationRow(decoded.Truncated, "bundle"); row != nil {
		t.Fatalf("complete bundle reported a bundle truncation row: %+v", *row)
	}
}

func countReportRows(t *testing.T, inputs []core.ProvenanceInput) (kbRows, candidates int) {
	t.Helper()
	for i := range inputs {
		in := &inputs[i]
		switch in.Kind {
		case "kb":
			kbRows++
			if *in.Included != 3 || *in.Omitted != 1 {
				t.Fatalf("kb row = included %d omitted %d, want 3 and 1", *in.Included, *in.Omitted)
			}
		case "kb_candidate":
			candidates++
		}
	}
	return kbRows, candidates
}

func findTruncationRow(rows []core.ProvenanceTruncatedRow, input string) *core.ProvenanceTruncatedRow {
	for i := range rows {
		if rows[i].Input == input {
			return &rows[i]
		}
	}
	return nil
}

func TestReportProvenanceCopiesBundleTruncation(t *testing.T) {
	req := fixture(t, "[]")
	writeReportProvenance(t)
	writeFile(t, reportBundleDir+"/truncation.json", bundleTruncation)

	resp := runReportChecked(t, req, &core.ReportFlags{Provenance: true})
	if resp.Data.Receipt == nil || resp.Data.Receipt.Returned != 0 {
		t.Fatalf("report's own receipt = %+v, want returned 0 for an empty item list", resp.Data.Receipt)
	}
	decoded := decodeReportProvenance(t, resp)
	row := findTruncationRow(decoded.Truncated, "bundle")
	if row == nil {
		t.Fatalf("no bundle truncation row in %+v", decoded.Truncated)
	}
	if row.Returned != 7 || row.Total == nil || *row.Total != 9 {
		t.Fatalf("bundle truncation row = returned %d total %v, want 7 and 9", row.Returned, row.Total)
	}
}
