package reportcore_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// violatedItem is the spec item the violating fixtures mark violated, spelled
// the way the dimension's detail and check line have to publish it.
const violatedItem = "the manual names context.txt"

// metItem is the spec item a passing fixture marks met.
const metItem = "the directive names context.txt"

// specCheck builds one stored spec check with the quoted evidence the record
// stores beside it.
func specCheck(item, status string) core.SpecCheck {
	return core.SpecCheck{
		Item:     item,
		Status:   status,
		Evidence: core.SpecEvidence{Path: "notes.txt", Quote: "the quoted line", Line: 2},
	}
}

// writeRecordWithSpecChecks stores a record receipt whose evidence pass
// receipted every changed hunk and covered every spec item, so the spec
// dimension is the only thing that can withhold approval: nothing in the
// receipt is insufficient and no finding gates.
func writeRecordWithSpecChecks(t *testing.T, checks []core.SpecCheck) {
	t.Helper()
	rec := core.Record{
		SchemaVersion: "1",
		Status:        "pr",
		Head:          storeHeadSHA,
		Evidence: &core.EvidenceReceipt{
			Head:       storeHeadSHA,
			DiffSHA256: strings.Repeat("d", 64),
			Receipts:   core.EvidenceReceipts{Total: 2, Covered: 2, Complete: true},
			Spec:       core.EvidenceSpec{Total: len(checks), Covered: len(checks), Min: 80, Met: true},
		},
		SpecChecks: checks,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	writeFile(t, ".verdict/state/5101/record.json", string(raw))
}

// runSpecGate runs one report gate over a store whose record carries these
// spec checks.
func runSpecGate(t *testing.T, flags *reportcore.Flags, checks []core.SpecCheck) *reportcore.Response {
	t.Helper()
	req := fixture(t, "[]")
	writeRecordWithSpecChecks(t, checks)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{ReadRequest: req, Flags: *flags})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	return resp
}

// specDim returns the published spec dimension, failing the test when the
// gate does not carry it.
func specDim(t *testing.T, resp *reportcore.Response) core.GateDimension {
	t.Helper()
	if resp.Data.Gate == nil {
		t.Fatal("data.gate is nil on a gate run")
	}
	for _, dim := range resp.Data.Gate.Dimensions {
		if dim.ID == "spec" {
			return dim
		}
	}
	t.Fatalf("spec dimension missing from %+v", resp.Data.Gate.Dimensions)
	return core.GateDimension{}
}

// assertSpecDim proves one spec dimension's verdict, tally, and detail.
func assertSpecDim(t *testing.T, resp *reportcore.Response, status string, count int, detail string) {
	t.Helper()
	dim := specDim(t, resp)
	if dim.Status != status {
		t.Errorf("spec status = %q, want %q", dim.Status, status)
	}
	if dim.Count == nil || *dim.Count != count {
		t.Errorf("spec count = %v, want %d", dim.Count, count)
	}
	if dim.Detail != detail {
		t.Errorf("spec detail = %q, want %q", dim.Detail, detail)
	}
}

// assertCheckLine proves one exact line on the human tail.
func assertCheckLine(t *testing.T, resp *reportcore.Response, want string) {
	t.Helper()
	if !containsLine(resp.CheckLines, want) {
		t.Errorf("CheckLines = %q, want it to contain %q", resp.CheckLines, want)
	}
}

// assertApprovalWithheld proves the derived decision refused this review for
// its spec check alone.
func assertApprovalWithheld(t *testing.T, resp *reportcore.Response) {
	t.Helper()
	if resp.Data.Approval == nil || resp.Data.Approval.Approved {
		t.Fatalf("approval = %+v, want withheld", resp.Data.Approval)
	}
	if want := "violated spec check"; resp.Data.Approval.Reason != want {
		t.Errorf("approval reason = %q, want %q", resp.Data.Approval.Reason, want)
	}
}

// assertSpecPasses proves the clean legs: the gate passes, the decision
// approves, and the dimension reports a zero tally.
func assertSpecPasses(t *testing.T, resp *reportcore.Response) {
	t.Helper()
	if resp.GateFailed {
		t.Errorf("GateFailed = true, want false (lines %q)", resp.CheckLines)
	}
	if resp.Data.Approval == nil || !resp.Data.Approval.Approved {
		t.Errorf("approval = %+v, want approved", resp.Data.Approval)
	}
	assertSpecDim(t, resp, "pass", 0, "no violated spec check")
	assertCheckLine(t, resp, "check: no violated spec check — gate passes")
}

// TestRunReportCheckSpecViolationFailsGate proves the false green this
// dimension closes: a review whose only defect is a violated spec check
// withholds approval, and the gate used to pass over it because no dimension
// read the spec checks at all.
func TestRunReportCheckSpecViolationFailsGate(t *testing.T) {
	resp := runSpecGate(t, &reportcore.Flags{Check: "important"}, []core.SpecCheck{
		specCheck(metItem, "met"),
		specCheck(violatedItem, "violated"),
	})
	assertApprovalWithheld(t, resp)
	if !resp.GateFailed {
		t.Errorf("GateFailed = false; a gate must not pass while approval withholds (lines %q)", resp.CheckLines)
	}
	assertSpecDim(t, resp, "fail", 1, "violated spec check: "+violatedItem)
	assertCheckLine(t, resp, "check: violated spec check: "+violatedItem+" — gate fails")
}

// TestRunReportCheckSpecCheckCount pins the tally and the first item: two
// violated spec checks fail with count 2 and name the first, so a dimension
// that counted met items, or dropped the count, cannot pass.
func TestRunReportCheckSpecCheckCount(t *testing.T) {
	resp := runSpecGate(t, &reportcore.Flags{Check: "important"}, []core.SpecCheck{
		specCheck(violatedItem, "violated"),
		specCheck(metItem, "met"),
		specCheck("the toc names context", "violated"),
	})
	if !resp.GateFailed {
		t.Error("GateFailed = false, want true")
	}
	assertSpecDim(t, resp, "fail", 2, "2 violated spec checks: "+violatedItem)
}

// TestRunReportCheckSpecCheckPasses proves the dimension stays quiet on the
// clean legs: met, n/a, a mix, and an absent array all pass with a zero tally,
// and approval is granted, so the dimension cannot fail a healthy gate.
func TestRunReportCheckSpecCheckPasses(t *testing.T) {
	tests := []struct {
		name   string
		checks []core.SpecCheck
	}{
		{name: "met", checks: []core.SpecCheck{specCheck(metItem, "met")}},
		{name: "na", checks: []core.SpecCheck{specCheck(metItem, "n/a")}},
		{
			name:   "mixed-met-and-na",
			checks: []core.SpecCheck{specCheck(metItem, "met"), specCheck("the toc names context", "n/a")},
		},
		{name: "absent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertSpecPasses(t, runSpecGate(t, &reportcore.Flags{Check: "important"}, tc.checks))
		})
	}
}

// TestRunReportSpecDimensionOnEveryGateMode proves the dimension is not tied
// to --check: the head-only, coverage, and risk gate modes all publish it and
// all fail on the same violation, so no gate surface can pass while the
// derived decision withholds.
func TestRunReportSpecDimensionOnEveryGateMode(t *testing.T) {
	tests := []struct {
		name  string
		flags reportcore.Flags
	}{
		{name: "completeness-only", flags: reportcore.Flags{Completeness: true, Live: "skip"}},
		{name: "coverage", flags: reportcore.Flags{Coverage: "warn", Live: "skip"}},
		{name: "max-risk", flags: reportcore.Flags{MaxRisk: "critical"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertSpecFails(t, runSpecGate(t, &tc.flags, []core.SpecCheck{specCheck(violatedItem, "violated")}))
		})
	}
}

// assertSpecFails proves the spec dimension failed and took the gate with it.
func assertSpecFails(t *testing.T, resp *reportcore.Response) {
	t.Helper()
	if dim := specDim(t, resp); dim.Status != "fail" {
		t.Errorf("spec status = %q, want fail", dim.Status)
	}
	if !resp.GateFailed {
		t.Errorf("GateFailed = false, want true (lines %q)", resp.CheckLines)
	}
}
