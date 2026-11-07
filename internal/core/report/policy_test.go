package reportcore_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
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
	dir := filepath.Join(".", ".verdict", "state", "5101")
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: dir, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
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
	if got := resp.Human[0]; got != "report: .verdict/state/5101 (1 shown) open=1 head=aaaaaaa" {
		t.Fatalf("human summary = %q, want the filtered view", got)
	}
	if !resp.GateFailed {
		t.Fatal("informational check gate passed: gates must read the full set, not the view")
	}
	assertGateLine(t, resp.CheckLines, "at-or-above informational")
}

// TestDisplayFloorEmptyViewDoesNotPromiseThreads pins the footer/view
// agreement: with every open finding below the floor the header reads
// "(0 shown) open=0", so the recovery line must not send the operator to
// threads the report does not show.
func TestDisplayFloorEmptyViewDoesNotPromiseThreads(t *testing.T) {
	req := fixture(t, "["+floorFinding("informational", "d222222222222222222222222222222222222222")+"]")
	writeFloorPolicy(t, "important")
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if len(resp.Data.Items) != 0 {
		t.Fatalf("items = %d, want the floored view", len(resp.Data.Items))
	}
	if resp.Next != "none open — nothing to address" {
		t.Errorf("next = %q, want no open-thread action with an empty view", resp.Next)
	}
	last := resp.Human[len(resp.Human)-1]
	if strings.Contains(last, "address open threads") {
		t.Errorf("human footer = %q, want no open-thread action with an empty view", last)
	}
}

// assertGateLine checks one gate verdict line names the full-set check.
func assertGateLine(t *testing.T, lines []string, verdict string) {
	t.Helper()
	for _, line := range lines {
		if len(line) > 0 && contains(line, verdict) {
			return
		}
	}
	t.Fatalf("check lines miss the full-set gate verdict %q: %v", verdict, lines)
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

// TestOutcomeChanged pins the ledger guard: only a changed verdict for the
// same check, group, and head appends, so another head is a new execution.
func TestOutcomeChanged(t *testing.T) {
	rs := &config.RunState{Outcomes: []config.CheckObservation{
		{Check: config.CheckHeadCurrent, Group: "g", Head: "h1", Passed: true},
	}}
	if !config.OutcomeChanged(rs, &config.CheckObservation{Check: config.CheckHeadCurrent, Group: "g", Head: "h2", Passed: true}) {
		t.Fatal("a verdict recorded for another head reports unchanged")
	}
	if !config.OutcomeChanged(rs, &config.CheckObservation{Check: config.CheckHeadCurrent, Group: "other", Head: "h1", Passed: true}) {
		t.Fatal("a verdict recorded for another group reports unchanged")
	}
	if config.OutcomeChanged(rs, &config.CheckObservation{Check: config.CheckHeadCurrent, Group: "g", Head: "h1", Passed: true}) {
		t.Fatal("identical verdict reports changed")
	}
	if !config.OutcomeChanged(rs, &config.CheckObservation{Check: config.CheckHeadCurrent, Group: "g", Head: "h1", Passed: false}) {
		t.Fatal("flipped verdict reports unchanged")
	}
	if !config.OutcomeChanged(rs, &config.CheckObservation{Check: config.CheckNoLiveThreads, Group: "g", Head: "h1", Passed: true}) {
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

// scopedFinding is floorFinding placed on a review path, so a scoped capture
// can separate the two.
func scopedFinding(path, severity, sig string) string {
	return `{"kind":"inline","path":"` + path + `","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Body.","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":"` + severity + `",` +
		`"sig":"` + sig + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[]}`
}

// writeScopedFloorPolicy persists a capture produced by the real resolver: the
// vendored scope carries critical, the repository default is important.
func writeScopedFloorPolicy(t *testing.T) {
	t.Helper()
	important, critical := "important", "critical"
	repo := &core.Config{
		SchemaVersion: "1",
		Defaults:      &core.ConfigDefaults{DisplayMinSeverity: &important},
		Scopes: []core.ConfigScope{{
			Name:     "vendored",
			Include:  []string{"vendor/**"},
			Defaults: &core.ConfigDefaults{DisplayMinSeverity: &critical},
		}},
	}
	resolved, ref := config.Resolve(&config.Inputs{
		Repo:  repo,
		Files: []config.FileInput{{Path: "vendor/lib.go"}, {Path: "app/main.go"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if got := *resolved.Files[0].Defaults.Values.DisplayMinSeverity; got != critical {
		t.Fatalf("resolver gave the vendored path %q; the fixture expects critical", got)
	}
	rs := &config.RunState{
		BaseSHA:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:  true,
		Resolved: resolved,
	}
	dir := filepath.Join(".", ".verdict", "state", "5101")
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: dir, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
}

// TestScopedDisplayFloorWithholdsOnlyItsOwnPath drives the report service over
// a real resolved capture: the vendored scope's critical floor hides its own
// important finding while the ordinary path's important one renders, and the
// informational check still fails on the full stored set.
func TestScopedDisplayFloorWithholdsOnlyItsOwnPath(t *testing.T) {
	list := "[" + scopedFinding("vendor/lib.go", "critical", "c111111111111111111111111111111111111111") +
		"," + scopedFinding("vendor/lib.go", "important", "e333333333333333333333333333333333333333") +
		"," + scopedFinding("app/main.go", "important", "f444444444444444444444444444444444444444") +
		"," + scopedFinding("app/main.go", "informational", "d222222222222222222222222222222222222222") + "]"
	req := fixture(t, list)
	writeScopedFloorPolicy(t)
	resp, ref := reportcore.Run(context.Background(),
		&core.ReportRequest{ReadRequest: req, Flags: core.ReportFlags{Check: "informational"}})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if len(resp.Data.Items) != 2 {
		t.Fatalf("items = %d; want the vendored critical and the ordinary important", len(resp.Data.Items))
	}
	paths := make([]string, 0, len(resp.Data.Items))
	for i := range resp.Data.Items {
		raw, _ := resp.Data.Items[i].Raw.Get("path")
		severity, _ := resp.Data.Items[i].Raw.Get("severity")
		paths = append(paths, jq.StrOf(raw)+":"+jq.StrOf(severity))
	}
	if paths[0] != "vendor/lib.go:critical" || paths[1] != "app/main.go:important" {
		t.Fatalf("view = %v; want the vendored critical and the ordinary important", paths)
	}
	if !resp.GateFailed {
		t.Fatal("informational gate passed: gates must read the full set, not the scoped view")
	}
}
