package reportcore_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

func TestReportRiskUnassessed(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/record.json",
		`{"head":"`+storeHeadSHA+`","coverage":{"gaps":0}}`)

	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Risk: true},
	})
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	if resp.Data.Risk == nil {
		t.Fatal("Data.Risk is nil")
	}
	if resp.Data.Risk.Claimed != "unknown" || resp.Data.Risk.Floor != "low" || resp.Data.Risk.Reported != "unknown" {
		t.Fatalf("Risk = %+v", resp.Data.Risk)
	}
	human := strings.Join(resp.Human, "\n")
	for _, want := range []string{
		"risk: unknown (claimed=unknown floor=low)",
		"risk floor inputs: open_critical=0 open_important=0 deferred=0 unanchored=0 unreviewed_files=0 stale_head=false missing_coverage=false",
		"risk receipt: missing-claim",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("human missing %q in:\n%s", want, human)
		}
	}
}

func TestReportRiskFloorOverride(t *testing.T) {
	req := fixture(t, `[`+openFinding+`]`)
	writeFile(t, ".verdict/state/5101/post.json",
		`{"head":"`+storeHeadSHA+`","risk":{"level":"low","rationale":"Looks straightforward at first glance.","assessed_head":"`+storeHeadSHA+`"}}`)

	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Risk: true},
	})
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	r := resp.Data.Risk
	if r == nil || r.Claimed != "low" || r.Floor != "critical" || r.Reported != "critical" {
		t.Fatalf("Risk = %+v", r)
	}
	if r.Inputs.OpenCritical != 1 || !r.Inputs.MissingCoverage {
		t.Fatalf("Inputs = %+v, want open_critical=1 missing_coverage=true", r.Inputs)
	}
	human := strings.Join(resp.Human, "\n")
	if !strings.Contains(human, "risk: critical (claimed=low floor=critical)") {
		t.Errorf("unexpected human output:\n%s", human)
	}
	if !strings.Contains(human, "risk receipt: missing-coverage") {
		t.Errorf("missing coverage reason absent:\n%s", human)
	}
}

func TestReportRiskMaxRiskPassOffline(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/post.json",
		`{"head":"`+storeHeadSHA+`","coverage":{"gaps":0},"risk":{"level":"medium","rationale":"Touches shared persistence layer cleanly.","assessed_head":"`+storeHeadSHA+`"}}`)

	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{MaxRisk: "medium"},
	})
	if ref != nil {
		t.Fatalf("pass Run refused: %+v", ref)
	}
	if resp.GateFailed || resp.Data.Gate == nil || !resp.Data.Gate.Passed {
		t.Fatalf("expected passing gate, got failed=%v gate=%+v", resp.GateFailed, resp.Data.Gate)
	}
	if len(resp.Data.Gate.Dimensions) != 1 || resp.Data.Gate.Dimensions[0].ID != "risk" {
		t.Fatalf("expected offline single risk dimension, got %+v", resp.Data.Gate.Dimensions)
	}
}

func TestReportRiskMaxRiskFailOffline(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/post.json",
		`{"head":"`+storeHeadSHA+`","coverage":{"gaps":0},"risk":{"level":"medium","rationale":"Touches shared persistence layer cleanly.","assessed_head":"`+storeHeadSHA+`"}}`)

	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{MaxRisk: "low"},
	})
	if ref != nil {
		t.Fatalf("fail Run refused: %+v", ref)
	}
	if !resp.GateFailed || resp.Data.Risk == nil || !resp.Data.Risk.Exceeded {
		t.Fatalf("expected failed risk gate, got failed=%v risk=%+v", resp.GateFailed, resp.Data.Risk)
	}
	if resp.Data.Risk.GateReason == nil || *resp.Data.Risk.GateReason != "risk-exceeded" {
		t.Fatalf("GateReason = %v, want risk-exceeded", resp.Data.Risk.GateReason)
	}
	if !strings.Contains(strings.Join(resp.CheckLines, "\n"), "risk-exceeded") {
		t.Fatalf("check lines = %v, want risk-exceeded", resp.CheckLines)
	}
}

func TestReportRiskInvalidMaxRiskRefuses(t *testing.T) {
	req := fixture(t, "[]")
	_, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{MaxRisk: "extreme"},
	})
	if ref == nil || ref.Code != "bad-flags" {
		t.Fatalf("ref = %+v, want bad-flags", ref)
	}
}

func TestReportRiskWithheldFindingStillRaisesFloor(t *testing.T) {
	raw, err := os.ReadFile("testdata/withheld.json")
	if err != nil {
		t.Fatal(err)
	}
	req := fixture(t, string(raw))
	writeFile(t, ".verdict/state/5101/record.json",
		`{"head":"`+storeHeadSHA+`","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Looks low.","assessed_head":"`+storeHeadSHA+`"}}`)

	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Risk: true, MaxRisk: "low"},
	})
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	r := resp.Data.Risk
	if r == nil || r.Floor != "critical" || r.Reported != "critical" || r.Inputs.OpenCritical != 1 {
		t.Fatalf("withheld open critical must raise floor to critical, got %+v", r)
	}
	if !resp.GateFailed || !r.Exceeded || r.GateReason == nil || *r.GateReason != "risk-exceeded" {
		t.Fatalf("expected --max-risk low to fail on withheld critical finding, got failed=%v risk=%+v", resp.GateFailed, r)
	}
}
