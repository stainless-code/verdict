package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/agentusage"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/spend"
)

// claudeResult is the `claude -p --output-format json` result object carrying
// text as its final message and usd as its total cost.
func claudeResult(t *testing.T, text, usd string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "num_turns": 3,
		"result": text, "session_id": "s", "total_cost_usd": json.Number(usd),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// measured is one measurement.
func measured(usd, by string) agentusage.Measurement {
	return agentusage.Measurement{USD: usd, PricedBy: by}
}

// spent records the spend a launch reported per attempt.
type spent struct{ rows []string }

func (s *spent) on(attempt int, usd, pricedBy string) {
	s.rows = append(s.rows, strings.Join([]string{string(rune('0' + attempt)), usd, pricedBy}, " "))
}

// TestMeasuredLaunchReadsTheCLIsOwnCost proves a measuring launch requests
// the row's structured output, reports the CLI's own cost for the attempt,
// and still salvages the review the agent printed inside that structure.
func TestMeasuredLaunchReadsTheCLIsOwnCost(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	cmdr := &scripted{res: core.Result{Stdout: claudeResult(t, fenced(salvagedFinding), "0.25")}}
	req := request(root, cmdr)
	req.MeasureSpend = true
	got := &spent{}
	req.OnSpend = got.on
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	if !slices.Equal(cmdr.cmd.Argv[:4], []string{"claude", "-p", "--output-format", "json"}) {
		t.Errorf("argv = %q, want the json output requested after -p", cmdr.cmd.Argv)
	}
	if !slices.Equal(got.rows, []string{"1 0.25 measured"}) {
		t.Errorf("spend = %q, want attempt 1 measured at 0.25", got.rows)
	}
	assertArtifact(t, bundleDir(root), salvagedFinding)
}

// TestPlainLaunchKeepsItsArgv proves a launch that measures nothing leaves
// the agent's argv and output mode untouched.
func TestPlainLaunchKeepsItsArgv(t *testing.T) {
	t.Parallel()
	cmdr := &scripted{res: core.Result{Stdout: fenced(salvagedFinding)}}
	if _, ref := Run(t.Context(), request(fixtureRoot(t), cmdr)); ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	if slices.Contains(cmdr.cmd.Argv, "--output-format") {
		t.Errorf("argv = %q, want no structured-output request", cmdr.cmd.Argv)
	}
}

// TestOperatorOutputModeWins proves an operator's own --output-format after
// `--` keeps the launch on their mode: nothing is requested or measured.
func TestOperatorOutputModeWins(t *testing.T) {
	t.Parallel()
	cmdr := &scripted{res: core.Result{Stdout: fenced(salvagedFinding)}}
	req := request(fixtureRoot(t), cmdr)
	req.MeasureSpend = true
	req.PassThrough = []string{"--output-format=text"}
	got := &spent{}
	req.OnSpend = got.on
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	if strings.Count(strings.Join(cmdr.cmd.Argv, " "), "--output-format") != 1 || len(got.rows) != 0 {
		t.Errorf("argv = %q spend = %q, want only the operator's mode and no measurement", cmdr.cmd.Argv, got.rows)
	}
}

// TestDispatchMeasuresTheSpawn proves the fix launch returns its measured
// spend when the request measures.
func TestDispatchMeasuresTheSpawn(t *testing.T) {
	t.Parallel()
	req := request(t.TempDir(), &scripted{res: core.Result{Stdout: claudeResult(t, "done", "0.3")}})
	req.MeasureSpend = true
	out, ref := Dispatch(t.Context(), req, nil, "fix")
	if ref != nil || out.USD != "0.3" || out.PricedBy != spend.PricedMeasured {
		t.Fatalf("dispatch = %+v, %v; want 0.3 measured", out, ref)
	}
}

// TestSettledExecutionCountsTowardTheMonthlyCeiling proves a two-finder
// execution whose every spawn was measured prices its spend receipt with the
// measured total, which the monthly ceiling then counts; one unmeasured spawn
// leaves the projection in place.
func TestSettledExecutionCountsTowardTheMonthlyCeiling(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := spawnBundle(t, root)
	ledger := `[{"at":"2026-05-15T11:00:00Z","execution_id":"exec-1","scope":"invocation",` +
		`"invocation_id":"exec-1@a","projected_usd":null}]`
	if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(ledger), 0o600); err != nil {
		t.Fatal(err)
	}
	req := request(root, &scripted{})
	req.Now = mayNow
	st := &state{req: req, meter: &meter{}, receiptID: "exec-1@a"}
	st.meter.add(measured("0.3", spend.PricedMeasured))
	st.meter.miss()
	st.settleExecution(t.Context(), dir)
	if mtd := spend.AggregateMTD(filepath.Join(root, ".verdict", "state"), mayNow()); mtd.UnknownCount != 1 {
		t.Fatalf("partly measured MTD = %+v, want the projection left unknown", mtd)
	}
	st.meter = &meter{}
	st.meter.add(measured("0.3", spend.PricedMeasured))
	st.meter.add(measured("0.2", spend.PricedTokens))
	st.settleExecution(t.Context(), dir)
	mtd := spend.AggregateMTD(filepath.Join(root, ".verdict", "state"), mayNow())
	if mtd.KnownUSD != "0.5" || mtd.UnknownCount != 0 {
		t.Fatalf("settled MTD = %+v, want the measured 0.5", mtd)
	}
	capped := request(root, &scripted{})
	capped.Now = mayNow
	capped.SpendPolicy = &spend.Policy{MonthlyCap: "0.4"}
	if _, ref := AuthorizeSpawn(t.Context(), capped, spawnBundle(t, root), "loop-x-1-review-1", ""); ref == nil ||
		ref.Code != "spend-capped" {
		t.Fatalf("next spawn = %v, want spend-capped on the measured spend", ref)
	}
}

// flagRejecter is an agent CLI too old for the structured-output flag: it
// refuses any argv carrying it and otherwise prints the review.
type flagRejecter struct {
	out   string
	argvs [][]string
}

func (f *flagRejecter) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	f.argvs = append(f.argvs, slices.Clone(cmd.Argv))
	if slices.Contains(cmd.Argv, "--output-format") {
		return core.Result{ExitCode: 1, Stdout: "error: unknown option '--output-format'\n"}, nil
	}
	return core.Result{Stdout: f.out}, nil
}

// TestRejectedUsageFlagRetriesWithoutIt proves an agent CLI that rejects the
// structured-output flag never fails the run: the spawn is retried once
// without it, the caller is told, and the run measures nothing further.
func TestRejectedUsageFlagRetriesWithoutIt(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	agent := &flagRejecter{out: fenced(salvagedFinding)}
	req := request(root, agent)
	req.MeasureSpend = true
	rejected := 0
	req.OnUsageRejected = func() { rejected++ }
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	if len(agent.argvs) != 2 || slices.Contains(agent.argvs[1], "--output-format") || rejected != 1 {
		t.Fatalf("argvs = %q rejected = %d, want one retry without the flag and one notice", agent.argvs, rejected)
	}
	if !slices.ContainsFunc(resp.Warnings, func(w string) bool { return strings.Contains(w, "rejected --output-format") }) {
		t.Errorf("warnings = %q, want the rejection named", resp.Warnings)
	}
	assertArtifact(t, bundleDir(root), salvagedFinding)
}

// TestOnlyAStartFailureIsFree proves a fix launch is "never started" — zero
// spend — only on the commander's typed start failure; any other error comes
// from a process that ran and spent, so it measures nothing.
func TestOnlyAStartFailureIsFree(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err error
		usd string
		by  string
		ran bool
	}{
		{err: &core.StartError{Err: errors.New("no such file")}, ran: false, usd: "0", by: spend.PricedNotStarted},
		{err: &core.OutputCappedError{}, ran: true},
	} {
		req := request(t.TempDir(), &scripted{err: tc.err})
		req.MeasureSpend = true
		out, ref := Dispatch(t.Context(), req, nil, "fix")
		if ref == nil {
			t.Errorf("err %T: dispatch refused nothing", tc.err)
		}
		if out.Ran != tc.ran || out.USD != tc.usd || out.PricedBy != tc.by {
			t.Errorf("err %T: dispatch = %+v, want ran=%v usd=%q by=%q", tc.err, out, tc.ran, tc.usd, tc.by)
		}
	}
}

// TestExecutionThatNeverStartedSettlesToZero proves a two-finder execution
// whose launches all failed to start settles its receipt at zero instead of
// keeping the projection of spend that never happened.
func TestExecutionThatNeverStartedSettlesToZero(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := spawnBundle(t, root)
	ledger := `[{"at":"2026-05-15T11:00:00Z","execution_id":"exec-1","scope":"invocation",` +
		`"invocation_id":"exec-1@a","projected_usd":2}]`
	if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(ledger), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &state{req: request(root, &scripted{}), meter: &meter{}, receiptID: "exec-1@a"}
	st.meter.never()
	st.meter.never()
	st.settleExecution(t.Context(), dir)
	if mtd := spend.AggregateMTD(filepath.Join(root, ".verdict", "state"), mayNow()); mtd.KnownUSD != "0" {
		t.Fatalf("MTD = %+v, want the receipt settled at zero", mtd)
	}
}

// TestSpawnModelReadsCodexConfig proves a codex launch that names no model
// is priced for the model its own config.toml selects.
func TestSpawnModelReadsCodexConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = \"gpt-6.2-sol\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := request(t.TempDir(), &scripted{})
	req.Agent = "codex"
	req.Env = map[string]string{"CODEX_HOME": home}
	if got := SpawnModel(req, nil); got != "gpt-6.2-sol" {
		t.Errorf("SpawnModel = %q, want the config's gpt-6.2-sol", got)
	}
	req.PassThrough = []string{"-m", "gpt-5"}
	if got := SpawnModel(req, nil); got != "gpt-5" {
		t.Errorf("SpawnModel = %q, want the explicit gpt-5", got)
	}
}

// TestTruncatedCaptureIsNeverMeasured proves a commander that cannot tap
// stdout and hands back a tail-capped capture measures nothing: a partial
// stream is unknown spend, never a partial total reported as measured.
func TestTruncatedCaptureIsNeverMeasured(t *testing.T) {
	t.Parallel()
	res := core.Result{Stdout: claudeResult(t, fenced(salvagedFinding), "0.25"), StdoutTruncated: true}
	req := request(fixtureRoot(t), &scripted{res: res})
	req.MeasureSpend = true
	got := &spent{}
	req.OnSpend = got.on
	// The review printed inside the cut stream is lost with it, so the run
	// refuses; the point is what it measured.
	if _, ref := Run(t.Context(), req); ref == nil {
		t.Error("a review lost in a cut stream did not refuse")
	}
	if len(got.rows) != 0 {
		t.Errorf("spend = %q, want nothing measured from a truncated capture", got.rows)
	}
}

// TestPostStartErrorStillMeasuresItsStdout proves a spawn that started and
// then failed is priced from the complete stdout a non-tapping commander
// handed back with the error, never dropped to a lower projection; a cut
// capture stays unpriced.
func TestPostStartErrorStillMeasuresItsStdout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		usd, by   string
		truncated bool
	}{
		{name: "complete", usd: "0.3", by: spend.PricedMeasured},
		{name: "truncated", truncated: true},
	} {
		res := core.Result{Stdout: claudeResult(t, "done", "0.3"), StdoutTruncated: tc.truncated, ExitCode: -1}
		req := request(t.TempDir(), &scripted{res: res, err: errors.New("wait: process group lost")})
		req.MeasureSpend = true
		out, ref := Dispatch(t.Context(), req, nil, "fix")
		if ref == nil || !out.Ran {
			t.Errorf("%s: dispatch = %+v refused=%v, want a refused spawn that ran", tc.name, out, ref)
		}
		if out.USD != tc.usd || out.PricedBy != tc.by {
			t.Errorf("%s: priced %q %q, want %q %q", tc.name, out.USD, out.PricedBy, tc.usd, tc.by)
		}
	}
}
