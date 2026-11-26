package reportcore

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func newRequest(root string, now time.Time) *Request {
	rr := core.ReadRequest{Repo: "acme/app", Pr: "42", RepoRoot: root, Runtime: &core.Runtime{Now: func() time.Time { return now }}}
	return &Request{ReadRequest: rr}
}

func writeMTDFiles(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), []byte(`[{"est_usd":1.5,"effort":"standard"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(`[{"at":"2026-05-10T12:00:00Z","projected_usd":2}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func makeMTDState(root, dir string) *state {
	ceiling := 10.0
	rs := &config.RunState{
		Name: "42",
		Resolved: &config.Resolved{
			Spend:    config.SpendView{MonthlyBudgetUSD: &ceiling, MonthlyRepoCap: &ceiling},
			Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{Effort: "standard"}},
		},
	}
	return &state{
		dir:    dir,
		now:    time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		req:    newRequest(root, time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)),
		flags:  Flags{Cost: true},
		policy: rs,
	}
}

func assertMTD(t *testing.T, st *state) {
	t.Helper()
	assertMTDBasic(t, st)
	assertMTDCaps(t, st)
}

func assertMTDBasic(t *testing.T, st *state) {
	t.Helper()
	if st.mtd == nil {
		t.Fatalf("mtd nil when cap configured")
	}
	if st.mtd.Month != "2026-05" {
		t.Fatalf("month = %q, want 2026-05", st.mtd.Month)
	}
	if st.mtd.KnownUSD == nil || *st.mtd.KnownUSD != 2 {
		t.Fatalf("KnownUSD = %v, want 2", st.mtd.KnownUSD)
	}
}

func assertMTDCaps(t *testing.T, st *state) {
	t.Helper()
	if st.mtd.MonthlyCapUSD == nil || *st.mtd.MonthlyCapUSD != 10 {
		t.Fatalf("cap = %v, want 10", st.mtd.MonthlyCapUSD)
	}
	if st.mtd.RemainingUSD == nil || *st.mtd.RemainingUSD != 8 {
		t.Fatalf("remaining = %v, want 8", st.mtd.RemainingUSD)
	}
}

// TestMTDHumanFormats pins the three spend shapes the mtd: line renders:
// priced over-cap spend, an empty month, and unpriced-only receipts.
// Unknown spend never reads as $0.
func TestMTDHumanFormats(t *testing.T) {
	known := 20.0
	ceiling := 10.0
	remaining := 0.0
	full := 10.0
	cases := []struct {
		name string
		mtd  *core.ReportMTD
		want string
	}{
		{"priced", &core.ReportMTD{Month: "2026-05", KnownUSD: &known, MonthlyCapUSD: &ceiling, RemainingUSD: &remaining, KnownCount: 1}, "mtd: 2026-05 known $20 of $10 ceiling, remaining $0 (1 priced, 0 unknown)"},
		{"empty", &core.ReportMTD{Month: "2026-05", MonthlyCapUSD: &ceiling, RemainingUSD: &full}, "mtd: 2026-05 known $0 of $10 ceiling, remaining $10 (0 priced, 0 unknown)"},
		{"unknown", &core.ReportMTD{Month: "2026-05", MonthlyCapUSD: &ceiling, UnknownCount: 2}, "mtd: 2026-05 known unknown of $10 ceiling (0 priced, 2 unknown)"},
	}
	for _, tc := range cases {
		if got := mtdHuman(tc.mtd); got != tc.want {
			t.Errorf("%s: mtdHuman = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func assertMTDEnvelope(t *testing.T, st *state) {
	t.Helper()
	lines := costHuman(st)
	found := false
	for _, l := range lines {
		if len(l) > 3 && l[:3] == "mtd" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("human mtd line missing: %v", lines)
	}
	data := buildData(st)
	if data.Cost.Mtd == nil || data.Cost.Mtd.Month != "2026-05" {
		t.Fatalf("envelope mtd = %+v", data.Cost.Mtd)
	}
}

func TestMTDShownWhenCapConfigured(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "42")
	writeMTDFiles(t, dir)
	st := makeMTDState(root, dir)
	st.cost = jq.NewObj("runs", 1, "est_usd", 1.5)
	st.cost.Set("by_tier", jq.NewObj("cheap", jq.NewObj("runs", 0), "standard", jq.NewObj("runs", 1), "deep", jq.NewObj("runs", 0)))
	loadStateCost(st)
	attachMTD(st)
	assertMTD(t, st)
	assertMTDEnvelope(t, st)
}

// TestMTDShownWhenRunsZero proves the human --cost output keeps the
// mtd: line even when no usable cost record was recorded for the bundle.
func TestMTDShownWhenRunsZero(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCostless(t, dir, "budget.json", `[{"at":"2026-05-10T12:00:00Z","projected_usd":2}]`)
	writeCostless(t, dir, "findings.json", `[]`)
	st := makeMTDState(root, dir)
	loadStateCost(st)
	attachMTD(st)
	assertMTD(t, st)
	assertMTDEnvelope(t, st)
}

func writeCostless(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMTDOmittedWhenNoCap(t *testing.T) {
	dir := t.TempDir()
	st := &state{
		dir:    dir,
		now:    time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		req:    newRequest(dir, time.Now()),
		flags:  Flags{Cost: true},
		policy: &config.RunState{Resolved: &config.Resolved{Spend: config.SpendView{}}},
	}
	st.cost = jq.NewObj("runs", 0)
	loadStateCost(st)
	attachMTD(st)
	if st.mtd != nil {
		t.Fatalf("mtd = %+v, want nil when no cap", st.mtd)
	}
	if st.cost != nil {
		if _, ok := st.cost.Get("mtd"); ok {
			t.Fatalf("cost mtd present without cap")
		}
	}
}

// TestReportRunShowsMTDThroughRealLoadOrder proves the monthly ceiling
// reaches report --cost through the production load order: Run() verifies
// the captured run.json before attaching MTD, so a real fetch-local-shaped
// bundle with monthly_budget_usd: 10 and a $20 month-to-date receipt shows
// non-nil data.cost.mtd (known_usd "20", monthly_cap_usd "10") and the human
// mtd: line even when runs == 0 (no usable cost record for the bundle).
func TestReportRunShowsMTDThroughRealLoadOrder(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "42")
	writeMTDRunBundle(t, dir)
	writeMTDRunPolicy(t, root, dir)

	rr := core.ReadRequest{
		Repo: "acme/app", Pr: "42", RepoRoot: root,
		Runtime: &core.Runtime{Now: func() time.Time { return now }},
	}
	req := &Request{ReadRequest: rr, Flags: Flags{Cost: true}}
	// OverridesDir points at an absent directory so the run never reads
	// the checkout this package lives in.
	req.OverridesDir = filepath.Join(root, ".verdict", "overrides")
	resp, rref := Run(context.Background(), req)
	if rref != nil {
		t.Fatalf("Run refusal: %v", rref)
	}
	assertMTDRunResponse(t, resp)
}

// writeMTDRunBundle seeds a fetch-local-shaped bundle with no usable cost
// record (runs == 0) but a $20 month-to-date receipt in May 2026.
func writeMTDRunBundle(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCostless(t, dir, "PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeCostless(t, dir, "findings.json", `[]`)
	writeCostless(t, dir, "record.json",
		`{"schema_version":"1","status":"pr","head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"evidence":{"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","diff_sha256":"`+strings.Repeat("0", 64)+`",`+
			`"receipts":{"total":0,"covered":0,"exempt":0,"complete":true},`+
			`"spec":{"total":0,"covered":0,"min":80,"met":true},"insufficient":false}}`)
	writeCostless(t, dir, "budget.json", `[{"at":"2026-05-10T12:00:00Z","projected_usd":20}]`)
}

// writeMTDRunPolicy writes the captured run.json carrying
// monthly_budget_usd: 10, bound to the controls snapshot.
func writeMTDRunPolicy(t *testing.T, root, dir string) {
	t.Helper()
	ceiling := 10.0
	rs := &config.RunState{
		BaseSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted: true,
		Resolved: &config.Resolved{
			Spend:    config.SpendView{MonthlyBudgetUSD: &ceiling, MonthlyRepoCap: &ceiling},
			Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{Effort: "standard"}},
		},
	}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: dir, Root: root, BaseSHA: rs.BaseSHA, AllowHead: true,
	})
	if ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
	rs.Inventory = config.InventoryHashes(snap)
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
}

// assertMTDRunResponse checks both surfaces of the Run() result: the typed
// and wire JSON cost blocks carry known_usd "20" with monthly_cap_usd "10",
// and the human mtd: line survives runs == 0.
func assertMTDRunResponse(t *testing.T, resp *Response) {
	t.Helper()
	assertMTDTyped(t, resp)
	if wireCap := wireMTDCap(t, resp); wireCap != 10 {
		t.Fatalf("wire monthly_cap_usd = %v, want 10", wireCap)
	}
	assertMTDHumanLine(t, resp)
}

// assertMTDTyped checks the typed cost block: month, known spend, ceiling,
// and zero runs.
func assertMTDTyped(t *testing.T, resp *Response) {
	t.Helper()
	if resp.Data.Cost == nil || resp.Data.Cost.Mtd == nil {
		t.Fatalf("cost mtd nil through Run(): %+v", resp.Data.Cost)
	}
	mtd := resp.Data.Cost.Mtd
	if mtd.Month != "2026-05" || mtd.KnownUSD == nil || *mtd.KnownUSD != 20 {
		t.Fatalf("mtd = %+v, want month 2026-05 known 20", mtd)
	}
	if mtd.MonthlyCapUSD == nil || *mtd.MonthlyCapUSD != 10 {
		t.Fatalf("mtd cap = %v, want 10", mtd.MonthlyCapUSD)
	}
	if resp.Data.Cost.Runs != 0 {
		t.Fatalf("runs = %d, want 0 (no usable cost record)", resp.Data.Cost.Runs)
	}
}

// wireMTDCap reads monthly_cap_usd from the JSON wire cost object.
func wireMTDCap(t *testing.T, resp *Response) float64 {
	t.Helper()
	costObj, _ := resp.Data.Wire().Get("cost")
	cost, ok := costObj.(*jq.Obj)
	if !ok {
		t.Fatalf("wire cost = %T, want object", costObj)
	}
	mtdObj, _ := cost.Get("mtd")
	mtd, ok := mtdObj.(*jq.Obj)
	if !ok {
		t.Fatalf("wire mtd = %T, want object", mtdObj)
	}
	capVal, _ := mtd.Get("monthly_cap_usd")
	f, ok := capVal.(float64)
	if !ok || f != 10 {
		t.Fatalf("wire monthly_cap_usd = %v, want 10", capVal)
	}
	return f
}

// assertMTDHumanLine checks the mtd: line survives runs == 0 with the
// ceiling and remainder.
func assertMTDHumanLine(t *testing.T, resp *Response) {
	t.Helper()
	for _, line := range resp.Human {
		if !strings.Contains(line, "mtd:") {
			continue
		}
		if !strings.Contains(line, "known $20") || !strings.Contains(line, "of $10 ceiling") {
			t.Fatalf("human mtd line = %q, want known $20 of $10 ceiling", line)
		}
		return
	}
	t.Fatalf("human mtd line missing: %v", resp.Human)
}

func TestMTDUnknownCount(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(`[{"at":"2026-05-01T12:00:00Z","projected_usd":null},{"at":"2026-05-02T12:00:00Z","projected_usd":1}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	ceiling := 10.0
	st := &state{
		dir:    dir,
		now:    time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		req:    newRequest(root, time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)),
		flags:  Flags{Cost: true},
		policy: &config.RunState{Resolved: &config.Resolved{Spend: config.SpendView{MonthlyBudgetUSD: &ceiling}}},
	}
	st.cost = jq.NewObj("runs", 1)
	loadStateCost(st)
	attachMTD(st)
	if st.mtd == nil || st.mtd.UnknownCount != 1 || st.mtd.KnownUSD == nil || *st.mtd.KnownUSD != 1 {
		t.Fatalf("mtd unknown = %+v", st.mtd)
	}
}

// TestCostShowsMeasuredSpendNotConsent proves a two-finder run's settled
// spend receipt reads as measured spend, never as a consented over-budget
// run, and that its amount prints without the agent CLI's float noise.
func TestCostShowsMeasuredSpendNotConsent(t *testing.T) {
	dir := t.TempDir()
	writeCostless(t, dir, "run.json", `{"execution":{"id":"exec-A","finders":2}}`)
	writeCostless(t, dir, "budget.json", `[
  {"at":"2026-11-24T10:00:00Z","execution_id":"exec-A","scope":"invocation","invocation_id":"exec-A@1",
   "projected_usd":1.30604299999999994,"priced_by":"measured","budget_usd":null},
  {"at":"2026-11-24T11:00:00Z","projected_usd":3,"consent":""}
]`)
	st := &state{dir: dir, flags: Flags{Cost: true}, req: newRequest(dir, time.Now())}
	loadStateCost(st)
	lines := costHuman(st)
	want := "measured: $1.306 from 1 spend receipt(s) the agent CLIs priced"
	if !slices.Contains(lines, want) {
		t.Errorf("cost lines = %q, want %q", lines, want)
	}
	for _, line := range lines {
		if strings.Contains(line, "consented") || strings.Contains(line, "=null") {
			t.Errorf("cost line %q, want no consent claim and no null token count", line)
		}
	}
	data := buildData(st)
	if data.Cost.MeasuredUSD == nil || *data.Cost.MeasuredUSD != 1.30604299999999994 || data.Cost.MeasuredReceipts != 1 {
		t.Fatalf("envelope measured = %v over %d, want 1.30604299999999994 over 1", data.Cost.MeasuredUSD, data.Cost.MeasuredReceipts)
	}
}

// TestMTDHumanRoundsWithoutHiding pins the mtd: line's money: the agent
// CLI's float noise rounds away, while a breach of the ceiling and a tiny
// nonzero remainder stay visible instead of rounding to the ceiling or to 0.
func TestMTDHumanRoundsWithoutHiding(t *testing.T) {
	noisy, ceiling, roomy := 1.30604299999999994, 10.0, 8.69395700000000006
	breach, one, none := 1.00004, 1.0, 0.0
	tiny, rest := 0.99999, 0.00001
	cases := []struct {
		name string
		mtd  *core.ReportMTD
		want string
	}{
		{
			"noise", &core.ReportMTD{Month: "2026-11", KnownUSD: &noisy, MonthlyCapUSD: &ceiling, RemainingUSD: &roomy, KnownCount: 1},
			"mtd: 2026-11 known $1.306 of $10 ceiling, remaining $8.694 (1 priced, 0 unknown)",
		},
		{
			"breach", &core.ReportMTD{Month: "2026-11", KnownUSD: &breach, MonthlyCapUSD: &one, RemainingUSD: &none, KnownCount: 1},
			"mtd: 2026-11 known $1.0001 of $1 ceiling, remaining $0 (1 priced, 0 unknown)",
		},
		{
			"tiny remainder", &core.ReportMTD{Month: "2026-11", KnownUSD: &tiny, MonthlyCapUSD: &one, RemainingUSD: &rest, KnownCount: 1},
			"mtd: 2026-11 known $1 of $1 ceiling, remaining $0.0001 (1 priced, 0 unknown)",
		},
	}
	for _, tc := range cases {
		if got := mtdHuman(tc.mtd); got != tc.want {
			t.Errorf("%s: mtdHuman = %q, want %q", tc.name, got, tc.want)
		}
	}
}
