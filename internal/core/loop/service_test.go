package loopcore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewflow"
	"github.com/stainless-code/verdict/internal/spend"
	"github.com/stainless-code/verdict/internal/testkit"
)

// t0 is the scripted clock's start; every phase call advances it by step.
var t0 = time.Date(2026, 11, 25, 10, 15, 0, 0, time.UTC)

// fakeClock is the injected clock: each phase call moves it by step, so the
// wall budget is spent deterministically.
type fakeClock struct {
	now  time.Time
	step time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

// script answers each phase per round (1-based) and logs the calls in order.
type script struct {
	admitRef   map[string]*core.RefusalError
	sigs       map[int][]string
	hang       map[string]bool
	verifyRef  *Refusal
	admitted   []string
	fetchRef   map[int]*core.RefusalError
	reviewRef  map[int]*core.RefusalError
	recordRef  map[int]*core.RefusalError
	clock      *fakeClock
	fixRef     map[int]*core.RefusalError
	open       map[int]int
	gate       map[int]bool
	cost       map[int]string
	calls      []string
	directives []string
	verifyExit int
	declared   bool
}

// roundOf reads the round number off a bundle name or directory.
func roundOf(t *testing.T, name string) int {
	t.Helper()
	n, err := strconv.Atoi(name[strings.LastIndex(name, "-")+1:])
	if err != nil {
		t.Fatalf("bundle %q carries no round number", name)
	}
	return n
}

type fake struct {
	t *testing.T
	s *script
}

func (f *fake) log(call string) {
	f.s.calls = append(f.s.calls, call)
	f.s.clock.now = f.s.clock.now.Add(f.s.clock.step)
}

// hung blocks a phase the script marks hanging until its context ends, the
// way a stuck child does, and answers with the shared cancel.
func (f *fake) hung(ctx context.Context, phase string) *core.RefusalError {
	if !f.s.hang[phase] {
		return nil
	}
	<-ctx.Done()
	return &core.RefusalError{Code: codeCanceled, Message: "canceled before " + phase + " completed"}
}

func (f *fake) fetch(ctx context.Context, name string) ([]string, *core.RefusalError) {
	n := roundOf(f.t, name)
	f.log("fetch " + strconv.Itoa(n))
	if ref := f.hung(ctx, "fetch"); ref != nil {
		return nil, ref
	}
	return nil, f.s.fetchRef[n]
}

// admit records one admission in the ordered call list without moving the
// clock, and answers the scripted refusal for that spawn.
func (f *fake) admit(launchID string) *core.RefusalError {
	f.s.admitted = append(f.s.admitted, launchID)
	label := spawnLabel.FindString(launchID)
	if label == "" {
		f.t.Fatalf("launch id %q names no round spawn", launchID)
	}
	label = label[1:]
	f.s.calls = append(f.s.calls, "admit "+label)
	return f.s.admitRef[label]
}

// spawnLabel is the round-and-spawn tail of a launch id: "1-review-1", "1-fix".
var spawnLabel = regexp.MustCompile(`-\d+-(review-\d+|fix)$`)

func (f *fake) authorize(
	_ context.Context, _ *reviewflow.Bundle, _ reviewflow.Launch, _, launchID string,
) ([]string, *core.RefusalError) {
	return nil, f.admit(launchID)
}

func (f *fake) review(
	_ context.Context, b *reviewflow.Bundle, launch reviewflow.Launch, _, prefix string,
) ([]string, *core.RefusalError) {
	n := roundOf(f.t, b.Pr)
	if ref := f.admit(prefix + "-1"); ref != nil {
		return nil, ref
	}
	f.log("review " + strconv.Itoa(n) + " timeout " + launch.TimeoutRaw + " rounds " + launch.RoundsRaw)
	return nil, f.s.reviewRef[n]
}

func (f *fake) record(_ context.Context, b *reviewflow.Bundle) (*core.RecordData, []string, *core.RefusalError) {
	n := roundOf(f.t, b.Pr)
	f.log("record " + strconv.Itoa(n))
	if ref := f.s.recordRef[n]; ref != nil {
		return nil, nil, ref
	}
	sigs, ok := f.s.sigs[n]
	if !ok {
		for i := range f.s.open[n] {
			sigs = append(sigs, "sig-"+strconv.Itoa(i))
		}
	}
	data := &core.RecordData{Findings: core.RecordFindings{Open: len(sigs)}}
	for _, sig := range sigs {
		data.Items = append(data.Items, core.FindingItem{Sig: sig, Status: statusOpen})
	}
	data.Items = append(data.Items, core.FindingItem{Sig: "fixed", Status: "addressed"})
	return data, nil, nil
}

func (f *fake) gate(ctx context.Context, b *reviewflow.Bundle) (bool, []string, *core.RefusalError) {
	n := roundOf(f.t, b.Pr)
	f.log("gate " + strconv.Itoa(n))
	if ref := f.hung(ctx, "gate"); ref != nil {
		return false, nil, ref
	}
	return f.s.gate[n], nil, nil
}

func (f *fake) fix(
	_ context.Context, b *reviewflow.Bundle, _ reviewflow.Launch, directive, _, _ string,
) *core.RefusalError {
	n := roundOf(f.t, b.Pr)
	f.log("fix " + strconv.Itoa(n))
	f.s.directives = append(f.s.directives, directive)
	return f.s.fixRef[n]
}

func (f *fake) declared(string) bool { return f.s.declared }

func (f *fake) verify(_ context.Context, b *reviewflow.Bundle, timeout string) (*Verify, []string) {
	f.log("verify " + strconv.Itoa(roundOf(f.t, b.Pr)) + " timeout " + timeout)
	if f.s.verifyRef != nil {
		return &Verify{Refusal: f.s.verifyRef}, nil
	}
	exit := f.s.verifyExit
	return &Verify{Exit: &exit}, nil
}

func (f *fake) cost(dir string) spend.MTD {
	known := f.s.cost[roundOf(f.t, dir)]
	if known == "" {
		return spend.MTD{UnknownCount: 1}
	}
	return spend.MTD{KnownUSD: known, KnownCount: 1}
}

// harness builds a request whose clock, receipts directory, and phases are
// test-owned.
type harness struct {
	req     *Request
	script  *script
	collect *testkit.Collector
	output  string
}

func newHarness(t *testing.T, rounds int) *harness {
	t.Helper()
	clock := &fakeClock{now: t0, step: time.Second}
	collect := &testkit.Collector{}
	root := t.TempDir()
	h := &harness{
		collect: collect,
		output:  filepath.Join(t.TempDir(), "receipts"),
		script: &script{
			clock: clock, open: map[int]int{}, gate: map[int]bool{}, cost: map[int]string{},
		},
	}
	h.req = &Request{
		Flow: &reviewflow.Flow{
			Runtime: &core.Runtime{Env: map[string]string{}, Now: clock.Now, Progress: collect},
		},
		RepoRoot:  root,
		Repo:      "acme/app",
		Agent:     "claude",
		MaxRounds: rounds,
		Budget:    10 * time.Minute,
		Output:    h.output,
	}
	return h
}

func (h *harness) run(t *testing.T) *Response {
	t.Helper()
	resp, ref := run(t.Context(), h.req, &fake{t: t, s: h.script})
	if ref != nil {
		t.Fatalf("loop refused: %v", ref)
	}
	return resp
}

// stopOf reads the stop reason.
func stopOf(t *testing.T, d *Data) string {
	t.Helper()
	return d.Stop
}

// readReceipt decodes one receipt file.
func readReceipt(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("receipt %s is not JSON: %v", path, err)
	}
	return out
}

func TestLoopPassesOnTheFirstCleanGate(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.script.gate[1] = true
	resp := h.run(t)
	if !resp.Data.Passed || stopOf(t, resp.Data) != StopPass {
		t.Fatalf("passed=%v stop=%v, want a pass", resp.Data.Passed, resp.Data.Stop)
	}
	want := []string{"fetch 1", "admit 1-review-1", "review 1 timeout 300 rounds 2", "record 1", "gate 1"}
	if !slices.Equal(h.script.calls, want) {
		t.Errorf("calls = %q, want %q", h.script.calls, want)
	}
	summary := readReceipt(t, filepath.Join(h.output, "summary.json"))
	if summary["passed"] != true || summary["stop"] != "pass" {
		t.Errorf("summary = %v, want passed pass", summary)
	}
	round := readReceipt(t, filepath.Join(h.output, "round-1.json"))
	if round["gate"] != true || round["fix"] != nil || round["open"] != float64(0) {
		t.Errorf("round-1.json = %v, want gate true, no fix, open 0", round)
	}
}

func TestLoopFixesThenPassesOnTheReReview(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.script.open[1] = 2
	h.script.gate[2] = true
	h.script.declared = true
	resp := h.run(t)
	if resp.Data.Stop != StopPass || len(resp.Data.Rounds) != 2 {
		t.Fatalf("stop=%s rounds=%d, want pass after 2", resp.Data.Stop, len(resp.Data.Rounds))
	}
	assertCalls(t, h.script.calls,
		"fetch 1", "admit 1-review-1", "review 1 timeout 300 rounds 2", "record 1", "gate 1", "admit 1-fix", "fix 1", "verify 1 timeout 300",
		"fetch 2", "admit 2-review-1", "review 2 timeout 297 rounds 2", "record 2", "gate 2")
	first := resp.Data.Rounds[0]
	if !slices.Equal(first.OpenSigs, []string{"sig-0", "sig-1"}) {
		t.Errorf("round 1 open sigs = %q, want the two open sigs only", first.OpenSigs)
	}
	assertVerifyExit(t, first.Fix, 0)
	dir := filepath.Join(h.req.RepoRoot, ".verdict", "state", first.Bundle)
	assertDirective(t, h.script.directives,
		defaultFixPrompt,
		"Round 1 of at most 3.",
		"Findings file: "+filepath.Join(dir, "findings.json"),
		"patch.json: "+filepath.Join(dir, "patch.json"))
}

// assertCalls compares the phase log against literals.
func assertCalls(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

// assertVerifyExit proves a fix receipt carries verify's exit.
func assertVerifyExit(t *testing.T, fix *Fix, want int) {
	t.Helper()
	if fix == nil || fix.Verify == nil || fix.Verify.Exit == nil || *fix.Verify.Exit != want {
		t.Errorf("fix = %+v, want a verify receipt with exit %d", fix, want)
	}
}

// assertDirective proves the one fix directive carries every line.
func assertDirective(t *testing.T, directives []string, lines ...string) {
	t.Helper()
	if len(directives) != 1 {
		t.Fatalf("fix directives = %d, want 1", len(directives))
	}
	for _, line := range lines {
		if !strings.Contains(directives[0], line) {
			t.Errorf("fix directive lacks %q:\n%s", line, directives[0])
		}
	}
}

func TestLoopFixPromptReplacesTheBuiltInInstructions(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2)
	h.script.open[1] = 1
	h.script.gate[2] = true
	h.req.FixPrompt = "Fix only the security findings.\n"
	h.run(t)
	got := h.script.directives[0]
	if !strings.HasPrefix(got, "Fix only the security findings.\n\nRound 1 of at most 2.") ||
		strings.Contains(got, defaultFixPrompt) {
		t.Errorf("directive = %q, want the operator's text then the round context", got)
	}
}

func TestLoopStopsAtMaxRoundsWithoutAnUnreviewedFix(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2)
	h.script.open[1] = 2
	h.script.open[2] = 1
	resp := h.run(t)
	if stopOf(t, resp.Data) != StopMaxRounds || resp.Data.Passed {
		t.Fatalf("stop = %s, want max-rounds", stopOf(t, resp.Data))
	}
	if !slices.Contains(h.script.calls, "fix 1") || slices.Contains(h.script.calls, "fix 2") {
		got := strings.Join(h.script.calls, ",")
		t.Errorf("calls = %s, want one fix after round 1 and none after the last round", got)
	}
	if resp.Data.Rounds[1].Fix != nil {
		t.Errorf("last round fix = %+v, want null", resp.Data.Rounds[1].Fix)
	}
}

func TestLoopStopsWhenOpenFindingsDoNotShrink(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 5)
	h.script.open[1] = 2
	h.script.open[2] = 2
	resp := h.run(t)
	if stopOf(t, resp.Data) != StopNoProgress || len(resp.Data.Rounds) != 2 {
		t.Fatalf("stop=%s rounds=%d, want no-progress after 2", stopOf(t, resp.Data), len(resp.Data.Rounds))
	}
	if resp.Data.Rounds[1].Fix != nil {
		t.Errorf("plateau round fixed anyway: %+v", resp.Data.Rounds[1].Fix)
	}
}

// TestLoopProgressIsAResolvedSig proves the plateau rule reads sigs: a round
// with the same count but a different set resolved a finding and continues.
func TestLoopProgressIsAResolvedSig(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.script.sigs = map[int][]string{1: {"a", "b"}, 2: {"b", "c"}}
	h.script.gate[3] = true
	resp := h.run(t)
	if resp.Data.Stop != StopPass || len(resp.Data.Rounds) != 3 {
		t.Fatalf("stop=%s rounds=%d, want the same-count different-set round to continue to a pass",
			resp.Data.Stop, len(resp.Data.Rounds))
	}
}

// TestLoopPlateauIsASupersetOfTheOpenSigs proves a round that resolved nothing
// stops even when it found more.
func TestLoopPlateauIsASupersetOfTheOpenSigs(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 5)
	h.script.sigs = map[int][]string{1: {"a"}, 2: {"a", "b"}}
	resp := h.run(t)
	if resp.Data.Stop != StopNoProgress || len(resp.Data.Rounds) != 2 {
		t.Fatalf("stop=%s rounds=%d, want no-progress after round 2", resp.Data.Stop, len(resp.Data.Rounds))
	}
}

// TestLoopVerifyCancelIsTheRoundStop runs the cancel through a live context:
// the operator's ^C cancels the loop while verify runs.
func TestLoopVerifyCancelIsTheRoundStop(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.script.open[1] = 1
	h.script.declared = true
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resp, ref := run(ctx, h.req, &canceling{fake: &fake{t: t, s: h.script}, cancel: cancel})
	if ref != nil {
		t.Fatal(ref)
	}
	want := Refusal{Code: "canceled", Message: "canceled before verify completed"}
	if resp.Data.Stop != StopRefused || resp.Data.Refusal == nil || *resp.Data.Refusal != want {
		t.Fatalf("stop=%s refusal=%+v, want refused carrying %+v", resp.Data.Stop, resp.Data.Refusal, want)
	}
	if len(resp.Data.Rounds) != 1 || resp.Data.Rounds[0].Refusal == nil || *resp.Data.Rounds[0].Refusal != want {
		t.Errorf("rounds = %+v, want round 1 alone carrying the cancel", resp.Data.Rounds)
	}
	if slices.Contains(h.script.calls, "fetch 2") {
		t.Errorf("calls = %q, want no round after the cancel", h.script.calls)
	}
}

// canceling cancels the loop's context from inside verify, then answers the
// way verify does once its context is gone.
type canceling struct {
	*fake
	cancel context.CancelFunc
}

func (c *canceling) verify(ctx context.Context, b *reviewflow.Bundle, timeout string) (*Verify, []string) {
	c.log("verify " + strconv.Itoa(roundOf(c.t, b.Pr)) + " timeout " + timeout)
	c.cancel()
	<-ctx.Done()
	return &Verify{Refusal: &Refusal{Code: codeCanceled, Message: "canceled before verify completed"}}, nil
}

// TestLoopDeadlineBoundsTheInProcessPhases proves fetch, record, and gate run
// under the wall deadline: a gate that hangs stops the loop on the budget,
// not as an operator cancel.
func TestLoopDeadlineBoundsTheInProcessPhases(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.req.Budget = 50 * time.Millisecond
	h.script.clock.step = 0
	h.script.hang = map[string]bool{"gate": true}
	resp := h.run(t)
	if resp.Data.Stop != StopWallBudget || resp.Data.Refusal == nil || resp.Data.Refusal.Code != codeCanceled {
		t.Fatalf("stop=%s refusal=%+v, want wall-budget carrying the phase's own cancel", resp.Data.Stop, resp.Data.Refusal)
	}
}

// TestLoopSplitsTheReviewBudgetAcrossAttempts proves one review cannot outlast
// the deadline: run makes up to two attempts, so each watchdog gets half the
// remaining budget, rounded up.
func TestLoopSplitsTheReviewBudgetAcrossAttempts(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1)
	h.req.Budget = 7 * time.Second
	h.script.gate[1] = true
	h.run(t)
	// 7s budget, 1s spent fetching: 6s left over two attempts.
	assertCalls(t, h.script.calls, "fetch 1", "admit 1-review-1", "review 1 timeout 3 rounds 2", "record 1", "gate 1")
}

func TestLoopStopsOnTheWallBudget(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 5)
	h.req.Budget = 6 * time.Second
	h.script.open[1] = 3
	h.script.open[2] = 2
	resp := h.run(t)
	if stopOf(t, resp.Data) != StopWallBudget {
		t.Fatalf("stop = %s, want wall-budget", stopOf(t, resp.Data))
	}
	// Six one-second phases fill round 1 (fetch, review, record, gate, fix);
	// round 2 starts with one second left and stops before its fix.
	if got := h.script.calls[len(h.script.calls)-1]; got != "fetch 2" {
		t.Errorf("last call = %q (calls %q), want round 2's fetch", got, h.script.calls)
	}
}

func TestLoopMapsTheWatchdogToTheWallBudget(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.script.reviewRef = map[int]*core.RefusalError{1: {
		Code: codeAgentTimeout, Message: "claude ran past the 600s watchdog and was killed",
	}}
	resp := h.run(t)
	if stopOf(t, resp.Data) != StopWallBudget || resp.Data.Refusal == nil ||
		resp.Data.Refusal.Code != codeAgentTimeout {
		t.Fatalf("stop=%s refusal=%+v, want wall-budget carrying agent-timeout",
			stopOf(t, resp.Data), resp.Data.Refusal)
	}
}

func TestLoopStopsOnTheCostBudget(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 5)
	h.req.MaxCostUSD = "1"
	h.script.open[1] = 3
	h.script.open[2] = 2
	h.script.cost[1] = "0.6"
	h.script.cost[2] = "0.5"
	resp := h.run(t)
	if stopOf(t, resp.Data) != StopCostBudget || len(resp.Data.Rounds) != 2 {
		t.Fatalf("stop=%s rounds=%d, want cost-budget in round 2", stopOf(t, resp.Data), len(resp.Data.Rounds))
	}
	if resp.Data.Rounds[1].Fix != nil {
		t.Errorf("round 2 fixed past the ceiling: %+v", resp.Data.Rounds[1].Fix)
	}
	if resp.Data.CostUSD == nil || *resp.Data.CostUSD != 1.1 {
		t.Errorf("cost_usd = %v, want the summed 1.1", resp.Data.CostUSD)
	}
}

// TestLoopUnmeasuredSpendStopsACostCeilingedLoop proves the fail-closed rule:
// with --max-cost-usd set, a round whose spend has no known amount stops the
// loop before the next spawn instead of spending blind.
func TestLoopUnmeasuredSpendStopsACostCeilingedLoop(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.req.MaxCostUSD = "5"
	h.script.open[1] = 1
	resp := h.run(t)
	if resp.Data.Stop != StopCostBudget || resp.Data.Refusal == nil || resp.Data.Refusal.Code != "budget-unknown" {
		t.Fatalf("stop=%s refusal=%+v, want cost-budget carrying budget-unknown", resp.Data.Stop, resp.Data.Refusal)
	}
	if !strings.Contains(resp.Data.Refusal.Message, "round 1's spend could not be measured") {
		t.Errorf("refusal message = %q, want it to say the spend could not be measured", resp.Data.Refusal.Message)
	}
	assertCalls(t, h.script.calls, "fetch 1", "admit 1-review-1", "review 1 timeout 300 rounds 2", "record 1", "gate 1")
}

// TestLoopUnmeasuredSpendStopsBeforeTheNextReview proves a fix spawn that
// reported nothing stops a cost-ceilinged loop at the next round's start.
func TestLoopUnmeasuredSpendStopsBeforeTheNextReview(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.req.MaxCostUSD = "5"
	h.script.open[1] = 1
	h.script.cost[1] = "0.2"
	cost := h.script.cost
	f := &fake{t: t, s: h.script}
	// Known after the review; the fix spawn then leaves the round unknown.
	resp, ref := run(t.Context(), h.req, &unknownAfterFix{fake: f, known: cost})
	if ref != nil {
		t.Fatal(ref)
	}
	if resp.Data.Stop != StopCostBudget || resp.Data.Refusal == nil || resp.Data.Refusal.Code != "budget-unknown" {
		t.Fatalf("stop=%s refusal=%+v, want budget-unknown before round 2", resp.Data.Stop, resp.Data.Refusal)
	}
	if slices.Contains(h.script.calls, "fetch 2") {
		t.Errorf("calls = %q, want no round 2 after unmeasured spend", h.script.calls)
	}
}

// unknownAfterFix prices a round only until its fix spawn runs.
type unknownAfterFix struct {
	*fake
	known map[int]string
	fixed map[int]bool
}

func (u *unknownAfterFix) fix(
	ctx context.Context, b *reviewflow.Bundle, l reviewflow.Launch, directive, dir, id string,
) *core.RefusalError {
	if u.fixed == nil {
		u.fixed = map[int]bool{}
	}
	u.fixed[roundOf(u.t, b.Pr)] = true
	return u.fake.fix(ctx, b, l, directive, dir, id)
}

func (u *unknownAfterFix) cost(dir string) spend.MTD {
	mtd := u.fake.cost(dir)
	if u.fixed[roundOf(u.t, dir)] {
		mtd.UnknownCount++
	}
	return mtd
}

// TestLoopUnmeasuredSpendWarnsWithoutACeiling proves the no-ceiling case keeps
// going and says the spend counts as unknown toward the monthly ceiling.
func TestLoopUnmeasuredSpendWarnsWithoutACeiling(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1)
	resp := h.run(t)
	if resp.Data.CostUSD != nil || resp.Data.Rounds[0].CostUSD != nil {
		t.Errorf("cost = %v / %v, want null when no receipt priced the round",
			resp.Data.CostUSD, resp.Data.Rounds[0].CostUSD)
	}
	want := "round 1's spend could not be fully measured (an agent spawn reported no cost) — " +
		"it counts as unknown toward the monthly spend ceiling"
	if !slices.Contains(resp.Warnings, want) || !slices.Contains(h.collect.Warnings, want) {
		t.Errorf("warnings = %q / streamed %q, want %q", resp.Warnings, h.collect.Warnings, want)
	}
}

// TestLoopAdmitsEverySpawn proves both spawns of a round go through the
// admission that writes their ledger receipts, under distinct ids.
func TestLoopAdmitsEverySpawn(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2)
	h.script.open[1] = 1
	h.script.gate[2] = true
	h.run(t)
	want := []string{"-1-review-1", "-1-fix", "-2-review-1"}
	if len(h.script.admitted) != len(want) {
		t.Fatalf("admitted = %q, want one admission per spawn %q", h.script.admitted, want)
	}
	for i, id := range h.script.admitted {
		if !strings.HasPrefix(id, "loop-20261125t101500z-") || !strings.HasSuffix(id, want[i]) {
			t.Errorf("admitted[%d] = %q, want the round bundle name plus %q", i, id, want[i])
		}
	}
}

func TestLoopMonthlyCeilingIsTheCostBudget(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.script.admitRef = map[string]*core.RefusalError{"1-review-1": {Code: "spend-capped", Message: "monthly ceiling reached"}}
	resp := h.run(t)
	if stopOf(t, resp.Data) != StopCostBudget || resp.Data.Refusal == nil || resp.Data.Refusal.Code != "spend-capped" {
		t.Fatalf("stop=%s refusal=%+v, want cost-budget carrying spend-capped", stopOf(t, resp.Data), resp.Data.Refusal)
	}
	if slices.ContainsFunc(h.script.calls, func(c string) bool { return strings.HasPrefix(c, "review") }) {
		t.Errorf("calls = %q, want no spawn past the ceiling", h.script.calls)
	}
}

func TestLoopSurfacesAgentFailedAsIs(t *testing.T) {
	t.Parallel()
	failed := &core.RefusalError{Code: codeAgentFailed, Message: "claude exited 7"}
	t.Run("review", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, 3)
		h.script.reviewRef = map[int]*core.RefusalError{1: failed}
		assertAgentFailed(t, h.run(t).Data)
	})
	t.Run("fix", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, 3)
		h.script.open[1] = 1
		h.script.fixRef = map[int]*core.RefusalError{1: failed}
		assertAgentFailed(t, h.run(t).Data)
	})
}

// assertAgentFailed proves the stop and both receipts carry run's refusal.
func assertAgentFailed(t *testing.T, d *Data) {
	t.Helper()
	want := Refusal{Code: "agent-failed", Message: "claude exited 7"}
	if d.Stop != StopAgentFailed || d.Refusal == nil || *d.Refusal != want {
		t.Fatalf("stop=%s refusal=%+v, want agent-failed carrying the run refusal", d.Stop, d.Refusal)
	}
	if d.Rounds[0].Refusal == nil || *d.Rounds[0].Refusal != want {
		t.Errorf("round-1 refusal = %+v, want %+v", d.Rounds[0].Refusal, want)
	}
}

func TestLoopStopsOnAComposedRefusal(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.script.recordRef = map[int]*core.RefusalError{1: {Code: "coverage-incomplete", Message: "no coverage"}}
	resp := h.run(t)
	if stopOf(t, resp.Data) != StopRefused || resp.Data.Refusal.Code != "coverage-incomplete" {
		t.Fatalf("stop=%s refusal=%+v, want refused coverage-incomplete", stopOf(t, resp.Data), resp.Data.Refusal)
	}
	round := readReceipt(t, filepath.Join(h.output, "round-1.json"))
	if round["gate"] != nil || round["open"] != nil {
		t.Errorf("round-1.json = %v, want gate and open null when record refused", round)
	}
}

func TestLoopPrintPlansWithoutRunning(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2)
	h.req.Print = true
	// A fixed output path pins the run id's hash half; print never creates it.
	h.req.Output = "/plan/receipts"
	resp := h.run(t)
	if resp.Data.Stop != StopPlanned || resp.Data.Passed || len(resp.Data.Rounds) != 0 || resp.Data.Output != nil {
		t.Fatalf("print data = %+v, want a plan only", resp.Data)
	}
	assertCalls(t, h.script.calls)
	assertPlanPhases(t, resp.Data.Plan,
		"1 fetch always", "1 review always", "1 record always", "1 gate always",
		"1 fix gate-failed", "1 verify verify-cmd-declared",
		"2 fetch always", "2 review always", "2 record always", "2 gate always")
	const local = "local-loop-20261125t101500z-496a8b-1"
	assertPlanArgv(t, resp.Data.Plan, map[int][]string{
		0: {"verdict", "fetch-local", "acme/app", "loop-20261125t101500z-496a8b-1"},
		1: {"verdict", "run", "acme/app", local, "--agent", "claude", "--finders", "1", "--rounds", "2", "--timeout", "300"},
		2: {"verdict", "record", "acme/app", local, "--require-coverage"},
		3: {"verdict", "gate", "acme/app", local, "--max-severity", "informational", "--coverage", "require", "--live", "skip"},
		5: {"verdict", "verify", "acme/app", local, "--timeout", "300"},
	})
	fix := resp.Data.Plan[4].Argv
	if len(fix) != 3 || fix[0] != "claude" || fix[1] != "-p" ||
		!strings.HasPrefix(fix[2], defaultFixPrompt+"\n\nRound 1 of at most 2.") {
		t.Errorf("fix argv = %q, want the claude row with the fix directive last", fix)
	}
	if _, err := os.Stat(filepath.Join(h.req.RepoRoot, ".verdict")); err == nil {
		t.Error("--print wrote under the checkout")
	}
}

// assertPlanPhases compares the plan's round/phase/condition triples.
func assertPlanPhases(t *testing.T, plan []PlanStep, want ...string) {
	t.Helper()
	got := make([]string, 0, len(plan))
	for _, step := range plan {
		got = append(got, strconv.Itoa(step.Round)+" "+step.Phase+" "+step.When)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plan phases = %q, want %q", got, want)
	}
}

// assertPlanArgv compares the listed plan steps' argv against literals.
func assertPlanArgv(t *testing.T, plan []PlanStep, want map[int][]string) {
	t.Helper()
	for i, argv := range want {
		if got := plan[i].Argv; !slices.Equal(got, argv) {
			t.Errorf("plan[%d] = %q, want %q", i, got, argv)
		}
	}
}

func TestLoopPrintRefusesAnUnknownAgent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 2)
	h.req.Agent = "no-such-agent"
	_, ref := run(t.Context(), h.req, &fake{t: t, s: h.script})
	if ref == nil || ref.Code != "unknown-agent" {
		t.Fatalf("ref = %v, want unknown-agent before any work", ref)
	}
	if _, err := os.Stat(h.output); err == nil {
		t.Error("an unknown agent still created the receipts directory")
	}
}

func TestLoopRefusesAnExistingOutput(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1)
	if err := os.MkdirAll(h.output, 0o755); err != nil {
		t.Fatal(err)
	}
	_, ref := run(t.Context(), h.req, &fake{t: t, s: h.script})
	if ref == nil || ref.Code != codeBadFlags || !strings.Contains(ref.Message, "already exists") {
		t.Fatalf("ref = %v, want bad-flags naming the existing directory", ref)
	}
	if len(h.script.calls) != 0 {
		t.Errorf("calls = %q, want none", h.script.calls)
	}
}

func TestLoopValidatesLimits(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*Request){
		"two sandboxes": func(r *Request) { r.VerifySandbox, r.VerifySandboxNetworkOnly = true, true },
		"no agent":      func(r *Request) { r.Agent = "" },
		"zero rounds":   func(r *Request) { r.MaxRounds = 0 },
		"too many":      func(r *Request) { r.MaxRounds = 21 },
		"zero budget":   func(r *Request) { r.Budget = 0 },
		"negative cost": func(r *Request) { r.MaxCostUSD = "-1" },
		"zero cost":     func(r *Request) { r.MaxCostUSD = "0" },
		"word cost":     func(r *Request) { r.MaxCostUSD = "lots" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertRefusedLimit(t, mutate)
		})
	}
}

// assertRefusedLimit proves one invalid limit refuses bad-flags with nothing
// written.
func assertRefusedLimit(t *testing.T, mutate func(*Request)) {
	t.Helper()
	h := newHarness(t, 1)
	mutate(h.req)
	_, ref := run(t.Context(), h.req, &fake{t: t, s: h.script})
	if ref == nil || ref.Code != codeBadFlags {
		t.Fatalf("ref = %v, want bad-flags", ref)
	}
	if _, err := os.Stat(h.output); err == nil {
		t.Error("a refused loop created its receipts directory")
	}
}

// TestLoopPlanPassesTheVerifySandbox proves --sandbox reaches the verify
// step's rendered command, and the network-only form likewise.
func TestLoopPlanPassesTheVerifySandbox(t *testing.T) {
	t.Parallel()
	for flag, set := range map[string]func(*Request){
		"--sandbox":              func(r *Request) { r.VerifySandbox = true },
		"--sandbox-network-only": func(r *Request) { r.VerifySandboxNetworkOnly = true },
	} {
		h := newHarness(t, 2)
		h.req.Print = true
		set(h.req)
		resp := h.run(t)
		verify := resp.Data.Plan[5].Argv
		if resp.Data.Plan[5].Phase != PhaseVerify || verify[len(verify)-1] != flag {
			t.Errorf("verify step = %q, want it to end with %s", verify, flag)
		}
	}
}

// TestLoopRefusedFixAdmissionNeverFixes proves the fix spawn is admitted
// before it launches: a ceiling that refuses the fix stops the loop on the
// cost budget and no fix runs.
func TestLoopRefusedFixAdmissionNeverFixes(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.script.open[1] = 1
	h.script.admitRef = map[string]*core.RefusalError{"1-fix": {Code: "spend-capped", Message: "monthly ceiling reached"}}
	resp := h.run(t)
	if resp.Data.Stop != StopCostBudget || resp.Data.Refusal == nil || resp.Data.Refusal.Code != "spend-capped" {
		t.Fatalf("stop=%s refusal=%+v, want cost-budget carrying spend-capped", resp.Data.Stop, resp.Data.Refusal)
	}
	assertCalls(t, h.script.calls,
		"fetch 1", "admit 1-review-1", "review 1 timeout 300 rounds 2", "record 1", "gate 1", "admit 1-fix")
}

// TestLoopAdmissionErrorIsNotTheCostBudget proves only a spend ceiling's
// refusal is the cost budget: a malformed ceiling keeps its own refusal.
func TestLoopAdmissionErrorIsNotTheCostBudget(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.script.open[1] = 1
	h.script.admitRef = map[string]*core.RefusalError{"1-fix": {Code: codeBadFlags, Message: "monthly spend ceiling must be a finite nonnegative USD amount"}}
	resp := h.run(t)
	if resp.Data.Stop != StopRefused || resp.Data.Refusal == nil || resp.Data.Refusal.Code != codeBadFlags {
		t.Fatalf("stop=%s refusal=%+v, want refused carrying bad-flags", resp.Data.Stop, resp.Data.Refusal)
	}
}

// TestLoopCancelAfterAPassingGateIsACancel proves a ^C that lands after the
// gate passes is still reported as the cancel, never exit-0 success.
func TestLoopCancelAfterAPassingGateIsACancel(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 3)
	h.script.gate[1] = true
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resp, ref := run(ctx, h.req, &cancelAtGate{fake: &fake{t: t, s: h.script}, cancel: cancel})
	if ref != nil {
		t.Fatal(ref)
	}
	if resp.Data.Passed || resp.Data.Stop != StopRefused || resp.Data.Refusal == nil || resp.Data.Refusal.Code != codeCanceled {
		t.Fatalf("passed=%v stop=%s refusal=%+v, want the canceled refusal", resp.Data.Passed, resp.Data.Stop, resp.Data.Refusal)
	}
}

// cancelAtGate cancels the loop just as the gate answers pass.
type cancelAtGate struct {
	*fake
	cancel context.CancelFunc
}

func (c *cancelAtGate) gate(ctx context.Context, b *reviewflow.Bundle) (bool, []string, *core.RefusalError) {
	passed, warnings, ref := c.fake.gate(ctx, b)
	c.cancel()
	return passed, warnings, ref
}

// TestLoopOneRoundStillResolvesTheAgent proves a one-round loop, which
// plans no fix, refuses an unknown agent before any work.
func TestLoopOneRoundStillResolvesTheAgent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 1)
	h.req.Agent = "no-such-agent"
	_, ref := run(t.Context(), h.req, &fake{t: t, s: h.script})
	if ref == nil || ref.Code != "unknown-agent" {
		t.Fatalf("ref = %v, want unknown-agent", ref)
	}
	if len(h.script.calls) != 0 {
		t.Errorf("calls = %q, want none", h.script.calls)
	}
}
