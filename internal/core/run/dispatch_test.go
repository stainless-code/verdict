package run

import (
	"slices"
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
	argv, ref := Dispatch(t.Context(), request(root, cmdr), nil, "fix the open findings")
	if ref != nil {
		t.Fatalf("dispatch refused: %v", ref)
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
			_, ref := Dispatch(t.Context(), request(t.TempDir(), &scripted{res: tc.res}), nil, "fix")
			if ref == nil || ref.Code != tc.code || ref.Message != tc.msg {
				t.Fatalf("ref = %v, want %s %q", ref, tc.code, tc.msg)
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
	argv, ref := Dispatch(t.Context(), req, agents, "fix")
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
	if _, ref := Dispatch(t.Context(), req, agents, "fix"); ref == nil || ref.Code != codeUnknownAgent {
		t.Fatalf("unknown agent = %v, want %s", ref, codeUnknownAgent)
	}
}

// TestMonthlyCeilingBindsAnUnpricedLaunch proves the loop's pre-spawn check:
// the bundle's captured ceiling refuses once month-to-date known spend is over
// it, and a bundle without run.json falls back to the personal policy.
func TestMonthlyCeilingBindsAnUnpricedLaunch(t *testing.T) {
	t.Parallel()
	root, dir, _ := setupMonthlyBundle(t, "3", `[{"at":"2026-05-01T12:00:00Z","projected_usd":3.5,"execution_id":"mtd-1"}]`)
	req := request(root, &scripted{})
	req.Now = func() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) }
	if _, ref := MonthlyCeiling(t.Context(), req, dir); ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("captured ceiling = %v, want spend-capped", ref)
	}

	fresh := t.TempDir()
	personal := request(root, &scripted{})
	personal.Now = req.Now
	personal.SpendPolicy = &spend.Policy{MonthlyCap: "3"}
	if _, ref := MonthlyCeiling(t.Context(), personal, fresh); ref == nil || ref.Code != "spend-capped" {
		t.Fatalf("personal ceiling = %v, want spend-capped", ref)
	}
	personal.SpendPolicy = &spend.Policy{MonthlyCap: "10"}
	if _, ref := MonthlyCeiling(t.Context(), personal, fresh); ref != nil {
		t.Fatalf("under the ceiling = %v, want nil", ref)
	}
}
