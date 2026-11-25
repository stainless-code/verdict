package run

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/spend"
)

// TestDispatchSpawnsTheDirectiveWithoutTheReviewRestriction proves the fix
// launch: the claude row's flags, then the directive, with no read-only
// restriction (the fix agent must edit) and the checkout as the working
// directory.
func TestDispatchSpawnsTheDirectiveWithoutTheReviewRestriction(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cmdr := &scripted{}
	argv, ran, ref := Dispatch(t.Context(), request(root, cmdr), nil, "fix the open findings")
	if ref != nil || !ran {
		t.Fatalf("dispatch ran=%v refused=%v, want a started process", ran, ref)
	}
	want := []string{"claude", "-p", "fix the open findings"}
	if !slices.Equal(argv, want) || cmdr.cmd == nil || !slices.Equal(cmdr.cmd.Argv, want) {
		t.Fatalf("argv = %q, spawned %v, want %q", argv, cmdr.cmd, want)
	}
	if cmdr.cmd.Dir != root {
		t.Errorf("dir = %q, want the checkout %q", cmdr.cmd.Dir, root)
	}
}

// TestDispatchReportsRunsOwnRefusals proves a failed fix spawn carries the
// run verb's codes and wording.
func TestDispatchReportsRunsOwnRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code string
		msg  string
		res  core.Result
	}{
		{res: core.Result{ExitCode: 7}, code: codeAgentFailed, msg: "claude exited 7"},
		{
			res:  core.Result{TimedOut: true, ExitCode: -1},
			code: codeAgentTimeout,
			msg:  "claude ran past the 300s watchdog and was killed (raise --timeout, or pass its fast flags after '--')",
		},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()
			_, ran, ref := Dispatch(t.Context(), request(t.TempDir(), &scripted{res: tc.res}), nil, "fix")
			if ref == nil || ref.Code != tc.code || ref.Message != tc.msg || !ran {
				t.Fatalf("ref = %v ran=%v, want %s %q from a process that started", ref, ran, tc.code, tc.msg)
			}
		})
	}
}

// TestDispatchPrintResolvesAgentsWithoutSpawning proves --print resolves an
// operator-defined row from prefs and spawns nothing, and an unknown name
// refuses before any spawn.
func TestDispatchPrintResolvesAgentsWithoutSpawning(t *testing.T) {
	t.Parallel()
	cmdr := &scripted{}
	req := request(t.TempDir(), cmdr)
	req.Print = true
	req.Agent = "mine"
	agents := []config.PersonalAgent{{Name: "mine", Binary: "my-agent", Args: []string{"--yes"}}}
	argv, _, ref := Dispatch(t.Context(), req, agents, "fix")
	if ref != nil {
		t.Fatalf("print refused: %v", ref)
	}
	if want := []string{"my-agent", "--yes", "fix"}; !slices.Equal(argv, want) {
		t.Errorf("argv = %q, want %q", argv, want)
	}
	if cmdr.calls != 0 {
		t.Errorf("print spawned %d processes", cmdr.calls)
	}
	req.Agent = "nobody"
	if _, _, ref := Dispatch(t.Context(), req, agents, "fix"); ref == nil || ref.Code != codeUnknownAgent {
		t.Fatalf("unknown agent = %v, want %s", ref, codeUnknownAgent)
	}
}

// TestAuthorizeSpawnBindsTheMonthlyCeiling proves the loop's pre-spawn check:
// the bundle's captured ceiling refuses once month-to-date known spend is over
// it, and a bundle without run.json falls back to the personal policy.
func TestAuthorizeSpawnBindsTheMonthlyCeiling(t *testing.T) {
	t.Parallel()
	root, dir, _ := setupMonthlyBundle(t, "3", `[{"at":"2026-05-01T12:00:00Z","projected_usd":3.5,"execution_id":"mtd-1"}]`)
	req := request(root, &scripted{})
	req.Now = mayNow
	if _, ref := AuthorizeSpawn(t.Context(), req, dir, "loop-a-review"); ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("captured ceiling = %v, want spend-capped", ref)
	}

	fresh := spawnBundle(t, root)
	personal := request(root, &scripted{})
	personal.Now = mayNow
	personal.SpendPolicy = &spend.Policy{MonthlyCap: "3"}
	if _, ref := AuthorizeSpawn(t.Context(), personal, fresh, "loop-b-review"); ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("personal ceiling = %v, want spend-capped", ref)
	}
	personal.SpendPolicy = &spend.Policy{MonthlyCap: "10"}
	if _, ref := AuthorizeSpawn(t.Context(), personal, fresh, "loop-b-fix"); ref != nil {
		t.Fatalf("under the ceiling = %v, want nil", ref)
	}
}

// mayNow is the fixed clock inside the seeded month.
func mayNow() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) }

// spawnBundle makes a fresh loop bundle under the repository's state
// directory, where the month-to-date scan reads it.
func spawnBundle(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "state", "local-loop-x-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestAuthorizeSpawnCountsTheSpawnAsUnknownUntilPriced proves a spawn the
// loop starts reaches the month-to-date scan: unknown spend at admission, the
// reported amount once priced, never free.
func TestAuthorizeSpawnCountsTheSpawnAsUnknownUntilPriced(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := spawnBundle(t, root)
	req := request(root, &scripted{})
	req.Now = mayNow
	if _, ref := AuthorizeSpawn(t.Context(), req, dir, "loop-x-1-fix"); ref != nil {
		t.Fatalf("admit = %v", ref)
	}
	state := filepath.Join(root, ".verdict", "state")
	if mtd := spend.AggregateMTD(state, mayNow()); mtd.UnknownCount != 1 || mtd.KnownUSD != "" {
		t.Fatalf("after admission MTD = %+v, want one unknown spawn", mtd)
	}
	if ref := PriceSpawn(t.Context(), req, dir, "loop-x-1-fix", "0.4", "agent"); ref != nil {
		t.Fatalf("price = %v", ref)
	}
	if mtd := spend.AggregateMTD(state, mayNow()); mtd.UnknownCount != 0 || mtd.KnownUSD != "0.4" {
		t.Fatalf("after pricing MTD = %+v, want 0.4 known", mtd)
	}
}

// TestAuthorizeSpawnRowSurvivesTheRecordedCost proves a spawn after the
// review stays visible without moving its timestamp: the review's
// unidentified cost.json covers projection rows at or before its second, but
// never a spawn row, which only a record naming its own launch can cover.
func TestAuthorizeSpawnRowSurvivesTheRecordedCost(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := spawnBundle(t, root)
	if err := os.WriteFile(filepath.Join(dir, "cost.json"),
		[]byte(`{"at":"2026-05-15T12:00:00Z","est_usd":0.5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	req := request(root, &scripted{})
	req.Now = mayNow
	if _, ref := AuthorizeSpawn(t.Context(), req, dir, "loop-x-1-fix"); ref != nil {
		t.Fatalf("admit = %v", ref)
	}
	mtd := spend.AggregateMTD(filepath.Join(root, ".verdict", "state"), mayNow())
	if mtd.KnownUSD != "0.5" || mtd.UnknownCount != 1 {
		t.Fatalf("MTD = %+v, want the review's 0.5 plus the unknown fix spawn", mtd)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "budget.json"))
	if err != nil || !strings.Contains(string(raw), `"at": "2026-05-15T12:00:00Z"`) {
		t.Errorf("budget.json = %s (err %v), want the row dated at its admission", raw, err)
	}
}

// TestAuthorizeSpawnRequiresAKnownEstimateWithoutACeiling proves
// require_known_estimate refuses an unpriced spawn even when no monthly
// ceiling is set, and writes no receipt.
func TestAuthorizeSpawnRequiresAKnownEstimateWithoutACeiling(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := spawnBundle(t, root)
	req := request(root, &scripted{})
	req.Now = mayNow
	req.SpendPolicy = &spend.Policy{RequireKnown: true}
	if _, ref := AuthorizeSpawn(t.Context(), req, dir, "loop-x-1-review-1"); ref == nil || ref.Code != "budget-unknown" {
		t.Fatalf("admit = %v, want budget-unknown", ref)
	}
	if _, err := os.Stat(filepath.Join(dir, "budget.json")); err == nil {
		t.Error("a refused spawn left a receipt")
	}
}

// TestAuthorizeSpawnCancelIsCanceled proves a cancel that lands while
// admission waits is the canceled refusal, never a record failure.
func TestAuthorizeSpawnCancelIsCanceled(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := spawnBundle(t, root)
	req := request(root, &scripted{})
	req.Now = mayNow
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, ref := AuthorizeSpawn(ctx, req, dir, "loop-x-1-review-1"); ref == nil || ref.Code != codeCanceled {
		t.Fatalf("admit = %v, want canceled", ref)
	}
}

// TestMonthlyCeilingSeesLoopSpawns proves spawns the loop priced count toward
// the monthly ceiling: two $0.6 spawns put the month over a $1 cap, so the
// third is refused before it starts.
func TestMonthlyCeilingSeesLoopSpawns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := spawnBundle(t, root)
	req := request(root, &scripted{})
	req.Now = mayNow
	req.SpendPolicy = &spend.Policy{MonthlyCap: "1"}
	for _, id := range []string{"loop-x-1-review", "loop-x-1-fix"} {
		if _, ref := AuthorizeSpawn(t.Context(), req, dir, id); ref != nil {
			t.Fatalf("admit %s = %v", id, ref)
		}
		if ref := PriceSpawn(t.Context(), req, dir, id, "0.6", "agent"); ref != nil {
			t.Fatalf("price %s = %v", id, ref)
		}
	}
	if _, ref := AuthorizeSpawn(t.Context(), req, dir, "loop-x-2-review"); ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("third spawn = %v, want spend-capped", ref)
	}
}
