package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

func loopGoodReview() string {
	return `{"schema_version":"1","body":"ok","approved":false,` +
		`"comments":[{"path":"pagination.go","line":42,"body":"off by one","rigor":"walked"}]}`
}

func dryInvalidMissingField() DryRunResult {
	return DryRunResult{
		Envelope: []byte(`{"error":{"code":"bad-review-input"}}`),
		Problems: []core.Problem{{
			Path: "a", Code: "missing-field",
			Level: "error", Message: "m", Fix: "f",
		}},
		Human: "error: bad input",
		Exit:  1,
	}
}

func dryValidEmpty() DryRunResult {
	norm := []byte(`{"schema_version":"1","body":"ok","approved":false,` +
		`"comments":[{"path":"pagination.go","line":42,"body":"off by one","rigor":"walked"}]}`)
	return DryRunResult{
		Envelope:   []byte(`{"data":{}}`),
		Normalized: norm,
	}
}

func dryWarningOnly() DryRunResult {
	norm := []byte(`{"schema_version":"1","body":"ok","approved":false,` +
		`"comments":[{"path":"pagination.go","line":42,"body":"off by one"}]}`)
	return DryRunResult{
		Envelope:   []byte(`{"data":{}}`),
		Normalized: norm,
		Warnings:   []string{"rigor is unset"},
	}
}

func loopTwoRoundConfig(t *testing.T, root, agent string) *Config {
	t.Helper()
	cfg := benchConfig(t, root, agent)
	cfg.Loop = 2
	cfg.Timeout = 2 * time.Minute
	return cfg
}

func checkRoundOneBytes(t *testing.T, cfg *Config, slug, want string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cfg.WorkDir, "runs", slug, "round-01", reviewJSONName))
	if err != nil {
		t.Fatalf("round-01 review: %v", err)
	}
	if string(raw) != want {
		t.Errorf("round-01 bytes changed: got %q", raw)
	}
}

// TestLoopBadThenFixedEndsValid drives two rounds.
func TestLoopBadThenFixedEndsValid(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	bad := `{"schema_version":"1","body":"ok","approved":false,"comments":[{"path":"x","line":1}]}`
	runBadThenFixed(t, bad, loopGoodReview())
}

func runBadThenFixed(t *testing.T, bad, fixed string) {
	t.Helper()
	t.Setenv("BENCH_TEST_BAD", bad)
	t.Setenv("BENCH_TEST_FIXED", fixed)
	agent := `if [ "${VERDICT_BENCH_ROUND:-1}" = "1" ]; then printf '%s' "$BENCH_TEST_BAD" > "$VERDICT_BENCH_OUT"; ` +
		`else printf '%s' "$BENCH_TEST_FIXED" > "$VERDICT_BENCH_OUT"; fi`
	cfg := loopTwoRoundConfig(t, fakeCorpus(t), agent)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	calls := 0
	fake.dry = func(string) (DryRunResult, error) {
		calls++
		if calls == 1 {
			return dryInvalidMissingField(), nil
		}
		return dryValidEmpty(), nil
	}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err != nil {
		t.Fatalf("runGraded: %v", err)
	}
	checkBadThenFixed(t, cfg, rep, bad)
}

func checkBadThenFixed(t *testing.T, cfg *Config, rep *Report, bad string) {
	t.Helper()
	if len(rep.Failures) != 0 || len(rep.Loops) != 1 {
		t.Fatalf("failures=%v loops=%d, want clean", rep.Failures, len(rep.Loops))
	}
	loop := rep.Loops[0]
	if !loop.FinalValid || loop.RoundsToValid != 2 || loop.ValidFirstTry {
		t.Errorf("loop = %+v, want valid on 2", loop)
	}
	checkRoundOneBytes(t, cfg, loop.Slug, bad)
	if rep.LoopCost == nil || rep.LoopCost.Attempts != 2 || len(rep.FixRates) == 0 {
		t.Errorf("cost=%+v rates=%d, want 2 and table", rep.LoopCost, len(rep.FixRates))
	}
}

// TestLoopSingleShotOmitsLoopBlocks keeps N=1 shape.
func TestLoopSingleShotOmitsLoopBlocks(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	t.Setenv("BENCH_TEST_REVIEW", reviewBody)
	cfg := benchConfig(t, fakeCorpus(t), agentCopiesReview)
	cfg.Loop = 1
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err != nil {
		t.Fatalf("runGraded: %v", err)
	}
	if len(rep.Loops) != 0 || len(rep.FixRates) != 0 || rep.LoopCost != nil {
		t.Errorf("N=1 carries loop blocks")
	}
	for _, c := range fake.calls {
		if strings.Contains(c, "dryRun") {
			t.Errorf("N=1 call %q must not run dry-run", c)
		}
	}
	round := filepath.Join(cfg.WorkDir, "runs",
		"corpus-off-by-one-fixture-good-run1", "round-01")
	if _, err := os.Stat(round); !os.IsNotExist(err) {
		t.Error("N=1 wrote round dirs")
	}
}

// TestFixRateRepairOnlyNotModelFix separates repairs.
func TestFixRateRepairOnlyNotModelFix(t *testing.T) {
	badBytes := []byte(`{"comments":[{"path":"a","line":1,"body":"one"}]}`)
	bad := makeRepairAttempt(badBytes, "aaa", false)
	good := makeRepairAttempt(badBytes, "aaa", true)
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{bad, good}}})
	for _, fr := range rates {
		if fr.Code != "key-alias" {
			continue
		}
		if fr.Shown != 1 || fr.Fixed != 0 || fr.RepairOnly != 1 {
			t.Errorf("rate = %+v, want repair_only", fr)
		}
		return
	}
	t.Fatal("no key-alias row")
}

func makeRepairAttempt(raw []byte, sha string, fixed bool) RoundAttempt {
	a := RoundAttempt{Round: 1, ReviewSHA: sha, ReviewBytes: raw}
	if fixed {
		a.Round = 2
		a.Dry = DryRunResult{Exit: 0, Normalized: raw}
		a.Dry.Repairs = []core.Repair{{Code: "key-alias", Path: "x"}}
		return a
	}
	a.Dry = DryRunResult{Exit: 1, Normalized: raw}
	a.Dry.Problems = []core.Problem{{
		Path: "x", Code: "key-alias", Level: "warning", Message: "a", Fix: "f",
	}}
	return a
}

// TestFixRateDropIsLossNotFix keeps deletion as loss.
func TestFixRateDropIsLossNotFix(t *testing.T) {
	before := []byte(`{"comments":[{"path":"a","line":1,"body":"one"}]}`)
	after := []byte(`{"comments":[]}`)
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: before}
	cur.Dry = DryRunResult{Exit: 1, Normalized: before}
	cur.Dry.Problems = []core.Problem{{
		Path: "c", Code: "missing-field", Level: "error", Message: "m", Fix: "f",
	}}
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: after}
	next.Dry = DryRunResult{Exit: 0, Normalized: after}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	for _, fr := range rates {
		if fr.Code != "missing-field" {
			continue
		}
		if fr.Fixed != 0 || fr.Dropped != 1 {
			t.Errorf("rate = %+v, want drop", fr)
		}
		checkDropDiff(t, &cur, &next)
		return
	}
	t.Fatal("no missing-field row")
}

func checkDropDiff(t *testing.T, cur, next *RoundAttempt) {
	t.Helper()
	added, dropped := diffLoopFindings(cur, next)
	if len(dropped) == 0 || len(added) != 0 {
		t.Errorf("added=%v dropped=%v, want only drops", added, dropped)
	}
}

// TestReplayLoopTwiceByteIdentical replays without agent.
func TestReplayLoopTwiceByteIdentical(t *testing.T) {
	bad := []byte(`{"comments":[{"path":"a","body":"x"}]}`)
	good := []byte(loopGoodReview())
	reviews := [][]byte{bad, good}
	dry := func(round int, _ []byte) (DryRunResult, error) {
		if round == 1 {
			return dryInvalidMissingField(), nil
		}
		return dryValidEmpty(), nil
	}
	first := replayLoop(reviews, dry)
	second := replayLoop(reviews, dry)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay differs:\n%+v\n%+v", first, second)
	}
	if !first.FinalValid || first.RoundsToValid != 2 {
		t.Errorf("outcome = %+v, want valid on 2", first)
	}
	if !bytes.Equal(first.Attempts[0].ReviewBytes, bad) {
		t.Error("replay changed round-1 bytes")
	}
}

// TestLoopGiveUpAfterMaxRounds stops on invalid.
func TestLoopGiveUpAfterMaxRounds(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	t.Setenv("BENCH_TEST_REVIEW", `{"body":"ok"}`)
	cfg := loopTwoRoundConfig(t, fakeCorpus(t), agentCopiesReview)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	fake.dry = func(string) (DryRunResult, error) { return dryInvalidMissingField(), nil }
	rep, err := runGraded(context.Background(), cfg, fake)
	if err == nil {
		t.Fatal("want give-up failure")
	}
	if rep == nil || len(rep.Failures) != 1 || len(rep.Loops) != 1 {
		t.Fatalf("rep = %+v, want one failure", rep)
	}
	if !rep.Loops[0].GiveUp || rep.Loops[0].FinalValid {
		t.Errorf("loop = %+v, want give-up", rep.Loops[0])
	}
}

// TestLoopWarningIsNotRefusal keeps warnings valid.
func TestLoopWarningIsNotRefusal(t *testing.T) {
	if dryWarningOnly().Exit != 0 {
		t.Fatal("warning-only must exit 0")
	}
	got := problemLevel(&core.Problem{Code: "rigor-unset"})
	if got != "warning" {
		t.Errorf("rigor-unset = %q, want warning", got)
	}
	attempt := RoundAttempt{Round: 1, ReviewSHA: "a"}
	attempt.ReviewBytes = []byte(`{"comments":[]}`)
	attempt.Dry = DryRunResult{Exit: 0, Warnings: []string{"r"}}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{attempt}}})
	if len(rates) != 0 {
		t.Errorf("valid round produced rows: %+v", rates)
	}
}

// TestLoopCostIncludesFailures sums spends.
func TestLoopCostIncludesFailures(t *testing.T) {
	half := 0.5
	quarter := 0.25
	out := LoopOutcome{Attempts: []RoundAttempt{
		{Round: 1, Cost: AttemptCost{Wall: 1, EstUSD: &half, HasEst: true}},
		{Round: 2, Cost: AttemptCost{Wall: 2, EstUSD: &quarter, HasEst: true}, AdapterError: "boom"},
	}}
	roll := loopCostRollup([]*LoopOutcome{&out})
	if roll.Attempts != 2 || roll.Usd != 0.75 || !roll.Measured {
		t.Errorf("roll = %+v, want 2/0.75", roll)
	}
	missing := LoopOutcome{Attempts: []RoundAttempt{{Round: 1, Cost: AttemptCost{Wall: 1}}}}
	roll = loopCostRollup([]*LoopOutcome{&missing})
	if roll.Measured || roll.Usd != 0 {
		t.Errorf("missing must stay unmeasured: %+v", roll)
	}
}

// TestLoopAdapterFailureEndsLoop classifies missing review.
func TestLoopAdapterFailureEndsLoop(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	cfg := benchConfig(t, fakeCorpus(t), `exit 1`)
	cfg.Loop = 3
	cfg.Timeout = 2 * time.Minute
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err == nil {
		t.Fatal("want adapter failure")
	}
	if len(rep.Loops) != 1 || rep.Loops[0].Attempts[0].AdapterError == "" {
		t.Fatalf("loops = %+v, want adapter error", rep.Loops)
	}
	if rep.Loops[0].FinalValid {
		t.Error("adapter failure must not be valid")
	}
}

// TestLoopConfigBounds pins caps.
func TestLoopConfigBounds(t *testing.T) {
	lc := loopConfig(15*time.Minute, 3)
	if lc.MaxRounds != 3 || lc.PerRound != 5*time.Minute {
		t.Errorf("15m/3 = %+v", lc)
	}
	lc = loopConfig(2*time.Minute, 5)
	if lc.PerRound != loopMinPerRound {
		t.Errorf("floor = %v", lc.PerRound)
	}
	if got := loopConfig(time.Minute, 99); got.MaxRounds != loopMaxRounds {
		t.Errorf("max = %d", got.MaxRounds)
	}
}

// TestLoopFeedbackFenceUsesRunNonce keeps output untrusted.
func TestLoopFeedbackFenceUsesRunNonce(t *testing.T) {
	dry := dryInvalidMissingField()
	fb := buildFeedback(1, 3, &dry)
	if !strings.Contains(fb, "round 1/3") || !strings.Contains(fb, "missing-field") {
		t.Errorf("feedback = %q", fb)
	}
	attempts := []RoundAttempt{{Dry: dry}}
	prompt := assembleLoopPrompt(fakePrefix, testNonce, fakePrefix,
		[]byte("{}"), []byte("diff"), 2, 3, attempts)
	if strings.Count(prompt, testNonce) < 2 {
		t.Errorf("prompt carries no fenced feedback")
	}
}
