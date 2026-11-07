package reportcore_test

import (
	"context"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// reportPlan selects head-current and no-live-threads for the bundle's one
// file, so a display run has every reason to touch the check ledger.
func reportPlan() config.Plan {
	return config.Plan{Status: config.PendingStatus, Groups: []config.PlanGroup{{
		ID: "ledger-group", Label: "deep-review", Profile: "deep-review", Depth: "standard",
		Files:  []string{"notes.txt"},
		Checks: []string{config.CheckHeadCurrent, config.CheckNoLiveThreads},
	}}}
}

// writeReportRunState seeds the typed bundle the report verifies.
func writeReportRunState(t *testing.T) {
	t.Helper()
	dir := ".verdict/state/5101"
	rs := &config.RunState{
		BaseSHA:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:    true,
		CapturedAt: "2026-11-06T00:00:00Z",
		Evidence:   config.Evidence{SchemaVersion: "1", BaseSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Plan: reportPlan()},
		Resolved:   &config.Resolved{Plan: reportPlan()},
	}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: dir, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	})
	if ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	rs.Inventory = config.InventoryHashes(snap)
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
}

// TestReportNeverRecordsCheckOutcomes pins the display boundary: a report that
// passes the postpublication completeness dimension never appends check
// evidence, so a completed post can never stand in for a live head-current
// evaluation, and a display run cannot certify a requirement.
func TestReportNeverRecordsCheckOutcomes(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/post.json", `{"head":"`+storeHeadSHA+`"}`)
	writeReportRunState(t)
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Completeness == nil || !resp.Data.Completeness.Reviewed {
		t.Fatalf("completeness = %+v, want the completed-post dimension to pass", resp.Data.Completeness)
	}
	rs, found, serr := config.ReadRunState(".verdict/state/5101")
	if serr != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, serr)
	}
	if len(rs.Outcomes) != 0 {
		t.Fatalf("report appended %+v; display evidence never satisfies a required check", rs.Outcomes)
	}
}
