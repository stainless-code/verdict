package reportcore_test

import (
	"context"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

func floorFinding(severity, sig string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Body.","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":"` + severity + `",` +
		`"sig":"` + sig + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[]}`
}

func writeFloorPolicy(t *testing.T, floor string) {
	t.Helper()
	severity := floor
	rs := &config.RunState{
		BaseSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted: true,
		Resolved: &config.Resolved{
			Defaults: config.ResolvedDefaults{
				Values: config.EffectiveDefaults{DisplayMinSeverity: &severity},
			},
		},
	}
	if serr := config.WriteRunState(".", "5101", rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
}

// TestDisplayFloorHidesBelowFloorButGatesStayFull pins the view/gate
// split with independently specified expectations: the informational
// finding leaves the items and human lines under an important floor,
// while the informational check gate still fails on the full set.
func TestDisplayFloorHidesBelowFloorButGatesStayFull(t *testing.T) {
	req := fixture(t, "["+floorFinding("critical", "c111111111111111111111111111111111111111")+","+
		floorFinding("informational", "d222222222222222222222222222222222222222")+"]")
	writeFloorPolicy(t, "important")
	r := req
	rq := &core.ReportRequest{ReadRequest: r, Flags: core.ReportFlags{Check: "informational"}}
	resp, ref := reportcore.Run(context.Background(), rq)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("items = %d, want only the critical finding", len(resp.Data.Items))
	}
	if got := resp.Human[0]; got != "report: .review/5101 (1 shown) open=1 head=aaaaaaa" {
		t.Fatalf("human summary = %q, want the filtered view", got)
	}
	if !resp.GateFailed {
		t.Fatal("informational check gate passed: gates must read the full set, not the view")
	}
	found := false
	for _, line := range resp.CheckLines {
		if len(line) > 0 && contains(line, "at-or-above informational") {
			found = true
		}
	}
	if !found {
		t.Fatalf("check lines miss the full-set gate verdict: %v", resp.CheckLines)
	}
}

// TestDisplayFloorAbsentShowsAll pins the legacy view: without a run
// bundle every finding renders.
func TestDisplayFloorAbsentShowsAll(t *testing.T) {
	req := fixture(t, "["+floorFinding("critical", "c111111111111111111111111111111111111111")+","+
		floorFinding("informational", "d222222222222222222222222222222222222222")+"]")
	rq := &core.ReportRequest{ReadRequest: req}
	resp, ref := reportcore.Run(context.Background(), rq)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if len(resp.Data.Items) != 2 {
		t.Fatalf("items = %d, want both findings without a floor", len(resp.Data.Items))
	}
}

// TestHeadCurrentObservationAppended pins the execution evidence: a
// passing completeness gate appends one head-current observation, and
// a repeated identical report appends nothing more.
func TestHeadCurrentObservationAppended(t *testing.T) {
	req := fixture(t, "["+floorFinding("critical", "c111111111111111111111111111111111111111")+"]")
	writeFloorPolicy(t, "informational")
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	writeFile(t, ".review/5101/post.json", `{"schema_version":"1","head":"`+head+`"}`)
	rq := &core.ReportRequest{ReadRequest: req, Flags: core.ReportFlags{Completeness: true}}
	if _, ref := reportcore.Run(context.Background(), rq); ref != nil {
		t.Fatalf("first Run refusal: %v", ref)
	}
	loaded, found, serr := config.ReadRunState(".", "5101")
	if serr != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, serr)
	}
	if len(loaded.Outcomes) != 1 {
		t.Fatalf("outcomes = %+v, want one head-current observation", loaded.Outcomes)
	}
	obs := loaded.Outcomes[0]
	if obs.Check != config.CheckHeadCurrent || !obs.Passed {
		t.Fatalf("observation = %+v, want the passing head check", obs)
	}
	if _, ref := reportcore.Run(context.Background(), rq); ref != nil {
		t.Fatalf("second Run refusal: %v", ref)
	}
	reloaded, found, rerr := config.ReadRunState(".", "5101")
	if rerr != nil || !found {
		t.Fatalf("ReadRunState: found=%v err=%v", found, rerr)
	}
	if len(reloaded.Outcomes) != 1 {
		t.Fatalf("outcomes after repeat = %+v, want no duplicate", reloaded.Outcomes)
	}
}

// TestOutcomeChanged pins the ledger guard: only a changed verdict for
// the same check and group appends.
func TestOutcomeChanged(t *testing.T) {
	rs := &config.RunState{Outcomes: []config.CheckObservation{
		{Check: config.CheckHeadCurrent, Passed: true},
	}}
	if config.OutcomeChanged(rs, &config.CheckObservation{Check: config.CheckHeadCurrent, Passed: true}) {
		t.Fatal("identical verdict reports changed")
	}
	if !config.OutcomeChanged(rs, &config.CheckObservation{Check: config.CheckHeadCurrent, Passed: false}) {
		t.Fatal("flipped verdict reports unchanged")
	}
	if !config.OutcomeChanged(rs, &config.CheckObservation{Check: config.CheckNoLiveThreads, Passed: true}) {
		t.Fatal("new check reports unchanged")
	}
	if !config.OutcomeChanged(nil, &config.CheckObservation{Check: config.CheckHeadCurrent}) {
		t.Fatal("nil state reports unchanged")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
