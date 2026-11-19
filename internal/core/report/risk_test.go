package reportcore_test

import (
	"context"
	"os"
	"slices"
	"strconv"
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
	// Every gate mode carries the always-on dimensions, so a --max-risk run
	// reports risk beside the spec verdict and the receipt that backs the head.
	dims := resp.Data.Gate.Dimensions
	if len(dims) != 3 || dims[0].ID != "risk" || dims[1].ID != "spec" || dims[2].ID != "evidence" {
		t.Fatalf("expected risk, spec, and evidence dimensions, got %+v", dims)
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

func TestReportRiskPrefersFreshRecordClaimOverStalePost(t *testing.T) {
	req := fixture(t, "[]")
	const oldHead = "1111111111111111111111111111111111111111"
	writeFile(t, ".verdict/state/5101/post.json",
		`{"head":"`+oldHead+`","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Old post.","assessed_head":"`+oldHead+`"}}`)
	writeFile(t, ".verdict/state/5101/record.json",
		`{"head":"`+storeHeadSHA+`","coverage":{"gaps":0},"risk":{"level":"high","rationale":"Fresh record.","assessed_head":"`+storeHeadSHA+`"}}`)

	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Risk: true},
	})
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	r := resp.Data.Risk
	if r == nil || r.Claimed != "high" || r.Reported != "high" || r.Inputs.StaleHead {
		t.Fatalf("stale post.json shadowed fresh record.json: %+v (stale_head=%v)", r, r.Inputs.StaleHead)
	}
}

func reportRiskDiff(additions int) string {
	var b strings.Builder
	b.WriteString("diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,0 +1,")
	b.WriteString(strconv.Itoa(additions) + " @@\n")
	for range additions {
		b.WriteString("+new line\n")
	}
	return b.String()
}

// TestReportRiskScore pins the score report --risk publishes: every component
// echoes its points and weight, the value is the published integer formula,
// and the human line follows the floor inputs it belongs to.
//
//nolint:cyclop,revive // one assertion block per component
func TestReportRiskScore(t *testing.T) {
	req := fixture(t, "["+openFinding+"]")
	writeFile(t, ".verdict/state/5101/impact.json", `{"symbols":{"count":1},"callers":{"count":3},"stale":false}`)
	writeFile(t, ".verdict/state/5101/full.diff", reportRiskDiff(21))

	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Risk: true},
	})
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	s := resp.Data.Risk.Score
	if s == nil {
		t.Fatal("Data.Risk.Score is nil under --risk")
	}
	if s.Value != 33 || s.Measured != 4 {
		t.Errorf("Score = value %d measured %d, want 33/4", s.Value, s.Measured)
	}
	if s.Reach.Points != 50 || s.Reach.Weight != 35 || !s.Reach.Measured {
		t.Errorf("Reach = %+v, want 50w35 measured", s.Reach)
	}
	if s.Churn.Points != 25 || s.Churn.Weight != 25 || s.Churn.Additions != 21 {
		t.Errorf("Churn = %+v, want 25w25 with 21 additions", s.Churn)
	}
	if s.Category.Points != 25 || s.Category.Weight != 25 || s.Category.Worst == nil || *s.Category.Worst != "untagged" {
		t.Errorf("Category = %+v, want 25w25 untagged", s.Category)
	}
	if s.Rung.Points != 25 || s.Rung.Weight != 15 || s.Rung.Weakest == nil || *s.Rung.Weakest != "walked" {
		t.Errorf("Rung = %+v, want 25w15 walked", s.Rung)
	}

	const want = "risk score: 33/100 (reach 50w35 churn 25w25 category 25w25 rung 25w15, measured 4/4)"
	at := slices.Index(resp.Human, want)
	if at < 0 {
		t.Fatalf("human lines %v lack %q", resp.Human, want)
	}
	if at == 0 || !strings.HasPrefix(resp.Human[at-1], "risk floor inputs: ") {
		t.Errorf("score line follows %q, want the risk floor inputs line", resp.Human[max(0, at-1)])
	}
}

// TestReportRiskScoreOnlyUnderRiskFlag proves --max-risk alone (and a plain
// report) stays byte-identical: the gate reads reported, never the score.
func TestReportRiskScoreOnlyUnderRiskFlag(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/impact.json", `{"symbols":{"count":1},"callers":{"count":3}}`)
	writeFile(t, ".verdict/state/5101/record.json",
		`{"head":"`+storeHeadSHA+`","coverage":{"gaps":0},"risk":{"level":"medium","rationale":"Touches shared persistence layer cleanly.","assessed_head":"`+storeHeadSHA+`"}}`)

	gated, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{MaxRisk: "medium"},
	})
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	if gated.Data.Risk == nil || gated.Data.Risk.Score != nil {
		t.Fatalf("--max-risk alone must publish the band without a score: %+v", gated.Data.Risk)
	}
	if strings.Contains(strings.Join(gated.Human, "\n"), "risk score: ") {
		t.Errorf("--max-risk human output carries a score line:\n%s", strings.Join(gated.Human, "\n"))
	}

	plain, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req})
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	if plain.Data.Risk != nil {
		t.Errorf("plain report publishes a risk block: %+v", plain.Data.Risk)
	}
}
