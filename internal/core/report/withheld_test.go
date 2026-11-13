package reportcore_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

func TestReportWithheldVisibilityAndGate(t *testing.T) {
	raw, err := os.ReadFile("testdata/withheld.json")
	if err != nil {
		t.Fatal(err)
	}
	rr := fixture(t, string(raw))
	req := &core.ReportRequest{ReadRequest: rr, Flags: core.ReportFlags{Check: "critical", Sarif: "out.sarif", Live: "skip"}}
	resp := runQuietReport(t, req)
	if len(resp.Data.Items) != 0 || resp.GateFailed || resp.Data.Counts.Withheld != 1 {
		t.Fatalf("default report = %+v", resp.Data)
	}
	if strings.Contains(strings.Join(resp.Human, "\n"), "Quiet by policy") {
		t.Fatal("default report showed withheld")
	}
	checkSarifSilence(t, resp.Sarif.Content)
	req.Flags.IncludeWithheld = true
	resp = runQuietReport(t, req)
	if !strings.Contains(strings.Join(resp.Human, "\n"), "Quiet by policy (1)") || len(resp.Data.QuietByPolicy) != 1 {
		t.Fatalf("quiet report = %v", resp.Human)
	}
	if resp.GateFailed {
		t.Fatal("include-withheld changed the tier gate")
	}
}

func checkSarifSilence(t *testing.T, raw []byte) {
	t.Helper()
	var doc struct {
		Runs []struct {
			Properties struct {
				Withheld map[string]int `json:"withheld"`
			} `json:"properties"`
			Results []any `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Runs) != 1 || len(doc.Runs[0].Results) != 0 || doc.Runs[0].Properties.Withheld["agent"] != 1 {
		t.Fatalf("SARIF = %s", raw)
	}
}

func runQuietReport(t *testing.T, req *core.ReportRequest) *core.ReportResponse {
	t.Helper()
	resp, ref := reportcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	return resp
}
