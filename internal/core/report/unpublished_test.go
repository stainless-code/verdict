package reportcore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// unpublishedSigs are the four store signatures the order leg ranks.
const (
	sigTraced   = "a111111111111111111111111111111111111111"
	sigWalked   = "b222222222222222222222222222222222222222"
	sigNullRung = "c333333333333333333333333333333333333333"
	sigUntagged = "d444444444444444444444444444444444444444"
)

// liveSkipLine is the neutral live dimension every leg here asks for.
const liveSkipLine = "check: live recheck skipped (--live skip) — gating on local dimensions only"

// unpublishedFinding renders one open anchored inline logic finding carrying
// raw severity and rigor JSON, so a null or unpublished value survives into
// the store exactly as recorded.
func unpublishedFinding(sig, severity, rigor, line string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":` + line + `,` +
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":` + rigor + `,"severity":` + severity + `,"category":"logic",` +
		`"sig":"` + sig + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[]}`
}

// unpublishedRun runs one report over a single-finding store, live probe
// skipped so the leg stays offline, and returns the response.
func unpublishedRun(t *testing.T, store string, flags *core.ReportFlags) *core.ReportResponse {
	t.Helper()
	req := fixture(t, store)
	flags.Live = "skip"
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req, Flags: *flags})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	return resp
}

// assertCheckLines proves the exact check lines and the gate verdict.
func assertCheckLines(t *testing.T, resp *core.ReportResponse, wantFail bool, want []string) {
	t.Helper()
	if got := strings.Join(resp.CheckLines, "\n"); got != strings.Join(want, "\n") {
		t.Errorf("CheckLines = %q, want %q", resp.CheckLines, want)
	}
	if resp.GateFailed != wantFail {
		t.Errorf("GateFailed = %v, want %v", resp.GateFailed, wantFail)
	}
}

// TestReportUnpublishedTierNeverGates proves an open anchored inline finding
// whose severity is null is untagged, not critical: the severity gate counts
// nothing at the critical threshold, while the finding still shows up.
func TestReportUnpublishedTierNeverGates(t *testing.T) {
	store := "[" + unpublishedFinding(sigUntagged, "null", `"walked"`, "2") + "]"
	resp := unpublishedRun(t, store, &core.ReportFlags{Check: "critical"})
	want := []string{"check: no open findings at-or-above critical — gate passes", liveSkipLine}
	assertCheckLines(t, resp, false, want)
	if len(resp.Data.Items) != 1 || resp.Data.Items[0].Sig != sigUntagged {
		t.Errorf("items = %+v, want the untagged finding still ranked", resp.Data.Items)
	}
}

// TestReportUnpublishedRungNeverGates proves a rigor no rung names is not
// evidence: the category gate needs a recorded rung, so an unknown one leaves
// the gate passing, and a null rigor counts as ineligible the same way while
// the baseline gate keeps its severity-only semantics.
func TestReportUnpublishedRungNeverGates(t *testing.T) {
	tests := []struct {
		name     string
		rigor    string
		category string
		wantLine string
		wantFail bool
	}{
		{
			name: "unknown-rung-in-category", rigor: `"traced"`, category: "logic",
			wantLine: "check: no open findings at-or-above critical in logic — gate passes",
		},
		{
			name: "null-rigor-in-category", rigor: "null", category: "logic",
			wantLine: "check: no open findings at-or-above critical in logic — gate passes",
		},
		{
			name: "null-rigor-baseline-still-gates", rigor: "null", wantFail: true,
			wantLine: "check: 1 open finding(s) at-or-above critical — gate fails",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := "[" + unpublishedFinding(sigTraced, `"critical"`, tc.rigor, "2") + "]"
			resp := unpublishedRun(t, store, &core.ReportFlags{Check: "critical", Category: tc.category})
			assertCheckLines(t, resp, tc.wantFail, []string{tc.wantLine, liveSkipLine})
		})
	}
}

// TestReportRanksUnpublishedValues proves the published sort contract for the
// raw values: an unknown rung (rank -1) sorts ahead of every recorded rung, a
// null rigor sorts as said, and a null severity sorts past informational.
func TestReportRanksUnpublishedValues(t *testing.T) {
	store := "[" + strings.Join([]string{
		unpublishedFinding(sigNullRung, `"critical"`, "null", "4"),
		unpublishedFinding(sigUntagged, "null", `"walked"`, "5"),
		unpublishedFinding(sigWalked, `"critical"`, `"walked"`, "3"),
		unpublishedFinding(sigTraced, `"critical"`, `"traced"`, "2"),
	}, ",") + "]"
	resp := unpublishedRun(t, store, &core.ReportFlags{})
	want := []struct {
		sig  string
		rank int
	}{
		{sigTraced, 1},
		{sigWalked, 2},
		{sigNullRung, 3},
		{sigUntagged, 4},
	}
	if len(resp.Data.Items) != len(want) {
		t.Fatalf("items = %d, want %d", len(resp.Data.Items), len(want))
	}
	for i, w := range want {
		if got := resp.Data.Items[i].Sig; got != w.sig {
			t.Errorf("item %d sig = %s, want %s", i, got, w.sig)
		}
		if got := resp.Data.Items[i].Rank; got != w.rank {
			t.Errorf("item %d rank = %d, want %d", i, got, w.rank)
		}
	}
}

// TestReportBasisDefaultsNullRigor proves the rendered provenance line keeps
// the pointed default for a null rigor instead of reading it as unrecorded.
func TestReportBasisDefaultsNullRigor(t *testing.T) {
	store := "[" + unpublishedFinding(sigNullRung, `"critical"`, "null", "2") + "]"
	resp := unpublishedRun(t, store, &core.ReportFlags{Output: "out.md"})
	if resp.Output == nil {
		t.Fatal("no output artifact returned")
	}
	want := "> Basis: 0 reproduced · 0 ran · 0 walked · 1 pointed · 0 said · 0 rules cited · 0 verified fixes"
	for line := range strings.SplitSeq(string(resp.Output.Content), "\n") {
		if line == want {
			return
		}
	}
	t.Errorf("output has no %q line:\n%s", want, resp.Output.Content)
}
