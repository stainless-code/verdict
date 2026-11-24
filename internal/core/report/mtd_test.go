package reportcore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
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
	assertMTD(t, st)
	assertMTDEnvelope(t, st)
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
	if st.mtd != nil {
		t.Fatalf("mtd = %+v, want nil when no cap", st.mtd)
	}
	if st.cost != nil {
		if _, ok := st.cost.Get("mtd"); ok {
			t.Fatalf("cost mtd present without cap")
		}
	}
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
	if st.mtd == nil || st.mtd.UnknownCount != 1 || st.mtd.KnownUSD == nil || *st.mtd.KnownUSD != 1 {
		t.Fatalf("mtd unknown = %+v", st.mtd)
	}
}
