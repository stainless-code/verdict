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
	before := []byte(`{"comments":[{"path":"a","line":1}]}`)
	after := []byte(`{"comments":[]}`)
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: before}
	cur.Dry = DryRunResult{Exit: 1, Normalized: before}
	cur.Dry.Problems = []core.Problem{{
		Path: "$.comments[0].body", Code: "missing-field", Level: "error", Message: "m", Fix: "f",
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

// dropPair builds the two attempts a deletion spans: an invalid round with
// problems and a valid round with fewer findings.
func dropPair(curRaw, nextRaw []byte, problems []core.Problem) (RoundAttempt, RoundAttempt) {
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: curRaw}
	cur.Dry = DryRunResult{Exit: 1}
	cur.Dry.Problems = problems
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: nextRaw}
	next.Dry = DryRunResult{Exit: 0, Normalized: nextRaw}
	return cur, next
}

// TestFixRateDeletesMultipleInvalidFindingsWhileUnrelatedSurvives mirrors
// the missing-drop repro at the unit seam: two missing-body findings are
// deleted while one valid finding survives, so both problems are drops.
func TestFixRateDeletesMultipleInvalidFindingsWhileUnrelatedSurvives(t *testing.T) {
	kept := `{"path":"pagination.go","line":11,"body":"looks good"}`
	before := []byte(`{"comments":[` + kept +
		`,{"path":"pagination.go","line":42},{"path":"pagination.go","line":51}]}`)
	after := []byte(`{"comments":[` + kept + `]}`)
	problems := []core.Problem{
		{Path: "$.comments[1].body", Code: "missing-field", Level: "error", Message: "m", Fix: "f"},
		{Path: "$.comments[2].body", Code: "missing-field", Level: "error", Message: "m", Fix: "f"},
	}
	cur, next := dropPair(before, after, problems)
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	checkMissingDropRate(t, rates)
	added, dropped := diffLoopFindings(&cur, &next)
	if len(added) != 0 || len(dropped) != 2 {
		t.Errorf("added=%d dropped=%d, want 0 and 2", len(added), len(dropped))
	}
}

// checkMissingDropRate asserts the missing-drop shape: shown and lost.
func checkMissingDropRate(t *testing.T, rates []FixRate) {
	t.Helper()
	for _, fr := range rates {
		if fr.Code != "missing-field" {
			continue
		}
		if !dropRateOK(&fr) {
			t.Errorf("rate = %+v, want shown 2 dropped 2 rate 0", fr)
		}
		return
	}
	t.Fatal("no missing-field row")
}

func dropRateOK(fr *FixRate) bool {
	return fr.Shown == 2 && fr.Fixed == 0 && fr.Dropped == 2 && fr.Rate != nil && *fr.Rate == 0
}

// TestFixRateBodyEditCountsAsFix keeps an edited finding from reading as a
// deletion: the anchor survives with new text, so the problem is fixed.
func TestFixRateBodyEditCountsAsFix(t *testing.T) {
	before := []byte(`{"comments":[{"path":"a","line":1}]}`)
	after := []byte(`{"comments":[{"path":"a","line":1,"body":"found it"}]}`)
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: before}
	cur.Dry = DryRunResult{Exit: 1}
	cur.Dry.Problems = []core.Problem{{
		Path: "$.comments[0].body", Code: "missing-field", Level: "error", Message: "m", Fix: "f",
	}}
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: after}
	next.Dry = DryRunResult{Exit: 0, Normalized: after}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	for _, fr := range rates {
		if fr.Code != "missing-field" {
			continue
		}
		if fr.Shown != 1 || fr.Fixed != 1 || fr.Dropped != 0 {
			t.Errorf("rate = %+v, want one fix", fr)
		}
		return
	}
	t.Fatal("no missing-field row")
}

// TestFixRateSplitsLevelsPerCode keeps one code's error and warning rows
// apart.
func TestFixRateSplitsLevelsPerCode(t *testing.T) {
	before := []byte(`{"comments":[{"path":"a","line":1},{"path":"b","line":2}]}`)
	after := []byte(`{"comments":[{"path":"a","line":1,"body":"x"},{"path":"b","line":2,"body":"y"}]}`)
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: before}
	cur.Dry = DryRunResult{Exit: 1}
	cur.Dry.Problems = []core.Problem{
		{Path: "$.comments[0].body", Code: "missing-field", Level: "error", Message: "m", Fix: "f"},
		{Path: "$.comments[1].body", Code: "missing-field", Level: "warning", Message: "m", Fix: "f"},
	}
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: after}
	next.Dry = DryRunResult{Exit: 0, Normalized: after}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	seen := ratesByLevel(rates, "missing-field")
	for _, level := range []string{"error", "warning"} {
		fr, ok := seen[level]
		if !ok {
			t.Fatalf("no missing-field/%s row in %+v", level, rates)
		}
		if !levelRateOK(&fr, 1, 1, 0) {
			t.Errorf("rate = %+v, want one fix", fr)
		}
	}
}

// ratesByLevel indexes one code's rows by level.
func ratesByLevel(rates []FixRate, code string) map[string]FixRate {
	out := map[string]FixRate{}
	for _, fr := range rates {
		if fr.Code == code {
			out[fr.Level] = fr
		}
	}
	return out
}

func levelRateOK(fr *FixRate, shown, fixed, dropped int) bool {
	return fr.Shown == shown && fr.Fixed == fixed && fr.Dropped == dropped
}

// TestFixRateRepairNeedsSamePath keeps a repair on another path from
// excusing a model fix.
func TestFixRateRepairNeedsSamePath(t *testing.T) {
	raw := []byte(`{"comments":[{"path":"a","line":1,"body":"one"}]}`)
	problem := []core.Problem{{
		Path: "$.comments[0].rigor", Code: "key-alias", Level: "warning", Message: "m", Fix: "f",
	}}
	for _, tc := range []struct {
		name       string
		repairPath string
		wantRepair int
		wantFixed  int
	}{
		{name: "same path", repairPath: "$.comments[0].rigor", wantRepair: 1, wantFixed: 0},
		{name: "other path", repairPath: "$.comments[0].path", wantRepair: 0, wantFixed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkRepairPath(t, raw, problem, tc.repairPath, tc.wantRepair, tc.wantFixed)
		})
	}
}

func checkRepairPath(t *testing.T, raw []byte, problems []core.Problem, repairPath string, wantRepair, wantFixed int) {
	t.Helper()
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: raw}
	cur.Dry = DryRunResult{Exit: 1, Normalized: raw}
	cur.Dry.Problems = problems
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: raw}
	next.Dry = DryRunResult{Exit: 0, Normalized: raw}
	next.Dry.Repairs = []core.Repair{{Code: "key-alias", Path: repairPath}}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	for _, fr := range rates {
		if fr.Code != "key-alias" {
			continue
		}
		if fr.RepairOnly != wantRepair || fr.Fixed != wantFixed {
			t.Errorf("rate = %+v, want repair %d fixed %d", fr, wantRepair, wantFixed)
		}
		return
	}
	t.Fatal("no key-alias row")
}

func checkEmptySlot(t *testing.T, got []rawSlot) {
	t.Helper()
	if got[0].pathKey != "" || got[0].id == "" {
		t.Errorf("empty slot = %+v, want a distinct preserved id", got[0])
	}
}

func checkAliasSlots(t *testing.T, got []rawSlot) {
	t.Helper()
	if got[1].path != "a" || got[2].path != "a" {
		t.Errorf("alias slot = %+v vs %+v, want file resolved to path", got[1], got[2])
	}
	if got[1].id != got[2].id {
		t.Errorf("alias ids differ: %q vs %q", got[1].id, got[2].id)
	}
}

func checkWrongTypeSlot(t *testing.T, got []rawSlot) {
	t.Helper()
	if got[3].bodyText || got[3].bodyKey == "" || got[3].id == got[1].id {
		t.Errorf("wrong-type slot = %+v, want a distinct preserved id", got[3])
	}
}

// TestLoopRawSlotsPreserveEveryIndex pins the raw-slot read: every comment
// index survives with its alias-resolved path, even with empty or wrong-typed
// fields, so later matching sees the true owner.
func TestLoopRawSlotsPreserveEveryIndex(t *testing.T) {
	raw := []byte(`{"comments":[{},{"path":"a","line":1,"body":"raw"},{"file":"a","line":1,"body":"raw"},{"path":"a","line":1,"body":42}]}`)
	got := loopCommentList(raw)
	if len(got) != 4 {
		t.Fatalf("slots = %+v, want four preserved indexes", got)
	}
	checkEmptySlot(t, got)
	checkAliasSlots(t, got)
	checkWrongTypeSlot(t, got)
	if set := loopFindingSet(raw); len(set) != 3 {
		t.Fatalf("set = %v, want empty plus raw plus wrong-type", set)
	}
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

// TestAssemblePromptCarriesPriorResponse pins the history an adapter that
// only reads the prompt sees: the exact prior review plus its feedback.
func TestAssemblePromptCarriesPriorResponse(t *testing.T) {
	prev := []byte(`{"comments":[{"path":"a","line":1,"body":"PRIOR_MARKER_8c3f02"}]}`)
	dry := dryInvalidMissingField()
	attempts := []RoundAttempt{{Dry: dry, ReviewBytes: prev}}
	prompt := assembleLoopPrompt(fakePrefix, testNonce, fakePrefix,
		[]byte("{}"), []byte("diff"), 2, 3, attempts)
	if !strings.HasPrefix(prompt, fakePrefix) {
		t.Fatal("round-2 prompt does not extend the round-1 bytes")
	}
	if !strings.Contains(prompt, "PRIOR_MARKER_8c3f02") {
		t.Error("round-2 prompt omits the exact prior response")
	}
	if !strings.Contains(prompt, "<<<UNTRUSTED nonce="+testNonce+" source="+prevReviewFile+">>>") {
		t.Errorf("round-2 prompt carries no fenced %s", prevReviewFile)
	}
	if !strings.Contains(prompt, "<<<UNTRUSTED nonce="+testNonce+" source="+feedFile+">>>") {
		t.Errorf("round-2 prompt carries no fenced %s", feedFile)
	}
	first := assembleLoopPrompt(fakePrefix, testNonce, "", []byte("{}"), []byte("diff"), 1, 3, nil)
	if want := assemblePrompt(fakePrefix, testNonce, []byte("{}"), []byte("diff")); first != want {
		t.Error("round 1 prompt is not byte-identical to single-shot")
	}
}

// TestLoopDropThroughShellAgentCountsLoss replays a deletion through sh:
// two invalid findings deleted while one valid survives counts as loss.
func TestLoopDropThroughShellAgentCountsLoss(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	kept := `{"path":"pagination.go","line":11,"body":"looks good"}`
	t.Setenv("BENCH_TEST_BAD", `{"comments":[`+kept+
		`,{"path":"pagination.go","line":42},{"path":"pagination.go","line":51}]}`)
	t.Setenv("BENCH_TEST_FIXED", `{"comments":[`+kept+`]}`)
	agent := `if [ "${VERDICT_BENCH_ROUND:-1}" = "1" ]; then printf '%s' "$BENCH_TEST_BAD" > "$VERDICT_BENCH_OUT"; ` +
		`else printf '%s' "$BENCH_TEST_FIXED" > "$VERDICT_BENCH_OUT"; fi`
	cfg := loopTwoRoundConfig(t, fakeCorpus(t), agent)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	calls := 0
	fake.dry = func(string) (DryRunResult, error) {
		calls++
		if calls == 1 {
			out := DryRunResult{Exit: 1, Envelope: []byte(`{"error":{"code":"bad-review-input"}}`)}
			out.Problems = []core.Problem{
				{Path: "$.comments[1].body", Code: "missing-field", Level: "error", Message: "m", Fix: "f"},
				{Path: "$.comments[2].body", Code: "missing-field", Level: "error", Message: "m", Fix: "f"},
			}
			return out, nil
		}
		return DryRunResult{Exit: 0, Envelope: []byte(`{"data":{}}`), Normalized: []byte(os.Getenv("BENCH_TEST_FIXED"))}, nil
	}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err != nil {
		t.Fatalf("runGraded: %v", err)
	}
	checkDropFixRates(t, rep)
}

// checkDropFixRates asserts the shell-driven deletion reads as loss.
func checkDropFixRates(t *testing.T, rep *Report) {
	t.Helper()
	for _, fr := range rep.FixRates {
		if fr.Code != "missing-field" {
			continue
		}
		if !dropRateOK(&fr) {
			t.Errorf("rate = %+v, want shown 2 dropped 2 rate 0", fr)
		}
		return
	}
	t.Fatalf("no missing-field row in %+v", rep.FixRates)
}

// TestLoopPromptCarriesHistoryThroughShellAgent mirrors the pi-history stub
// through sh: round 2 refuses unless the prompt file carries round 1 bytes.
func TestLoopPromptCarriesHistoryThroughShellAgent(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	t.Setenv("BENCH_TEST_MARKER", "HISTORYMARKER42")
	t.Setenv("BENCH_TEST_BAD", `{"body":"HISTORYMARKER42 needs more than twenty chars of verdict text","comments":[{"path":"x","line":1}]}`)
	t.Setenv("BENCH_TEST_FIXED", loopGoodReview())
	agent := `if [ "${VERDICT_BENCH_ROUND:-1}" = "1" ]; then printf '%s' "$BENCH_TEST_BAD" > "$VERDICT_BENCH_OUT"; ` +
		`else grep -q "$BENCH_TEST_MARKER" "$VERDICT_BENCH_PROMPT" || { echo "history absent" >&2; exit 7; }; ` +
		`printf '%s' "$BENCH_TEST_FIXED" > "$VERDICT_BENCH_OUT"; fi`
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
	if len(rep.Loops) != 1 || !rep.Loops[0].FinalValid {
		t.Fatalf("loops = %+v, want one valid loop", rep.Loops)
	}
}

// TestLoopFailedShellAgentKeepsSpend keeps a failed attempt's reported spend
// in the lower bound, while a spend-less failure stays unmeasured.
func TestLoopFailedShellAgentKeepsSpend(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	cfg := loopTwoRoundConfig(t, fakeCorpus(t),
		`printf '{"est_usd":0.42,"models":[{"name":"fixture-good"}]}' > "$VERDICT_BENCH_COST"; printf 'agent exploded'; exit 3`)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err == nil {
		t.Fatal("want adapter failure")
	}
	if rep == nil || rep.LoopCost == nil {
		t.Fatalf("rep = %+v, want a loop cost", rep)
	}
	got := rep.LoopCost
	if got.Attempts != 1 || got.WithEstimate != 1 || got.Usd != 0.42 || !got.Measured {
		t.Errorf("loop cost = %+v, want 1 attempt measured at 0.42", got)
	}
	if est := rep.Loops[0].Attempts[0].EstUSD; est == nil || *est != 0.42 {
		t.Errorf("attempt spend = %v, want 0.42", est)
	}
}

// TestLoopFailedShellAgentWithoutSpendStaysUnmeasured keeps a missing spend
// report a lower bound, never free money.
func TestLoopFailedShellAgentWithoutSpendStaysUnmeasured(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	cfg := loopTwoRoundConfig(t, fakeCorpus(t), agentFails)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err == nil {
		t.Fatal("want adapter failure")
	}
	if rep == nil || rep.LoopCost == nil {
		t.Fatalf("rep = %+v, want a loop cost", rep)
	}
	got := rep.LoopCost
	if got.Measured || got.Usd != 0 || got.WithEstimate != 0 {
		t.Errorf("loop cost = %+v, want unmeasured", got)
	}
}

func missingAt(path string) []core.Problem {
	return []core.Problem{{Path: path, Code: "missing-field", Level: "error", Message: "m", Fix: "f"}}
}

func wrongAt(path string) []core.Problem {
	return []core.Problem{{Path: path, Code: "wrong-type", Level: "error", Message: "m", Fix: "f"}}
}

func findRate(rates []FixRate, code, level string) *FixRate {
	for i := range rates {
		if rates[i].Code == code && rates[i].Level == level {
			return &rates[i]
		}
	}
	return nil
}

func checkRate(t *testing.T, rates []FixRate, code, level string, shown, fixed, dropped int) {
	t.Helper()
	fr := findRate(rates, code, level)
	if fr == nil {
		t.Fatalf("no %s/%s row in %+v", code, level, rates)
	}
	if fr.Shown != shown || fr.Fixed != fixed || fr.Dropped != dropped {
		t.Errorf("rate = %+v, want shown %d fixed %d dropped %d", fr, shown, fixed, dropped)
	}
	if fr.Unattributed != 0 {
		t.Errorf("rate = %+v, want no unattributed", fr)
	}
}

// TestFixRateSameAnchorDeletionIsDrop keeps a deletion from borrowing.
func TestFixRateSameAnchorDeletionIsDrop(t *testing.T) {
	before := []byte(`{"comments":[{"path":"a","line":11,"body":"keep"},{"path":"a","line":11}]}`)
	after := []byte(`{"comments":[{"path":"a","line":11,"body":"keep"}]}`)
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: before}
	cur.Dry = DryRunResult{Exit: 1}
	cur.Dry.Problems = missingAt("$.comments[1].body")
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: after}
	next.Dry = DryRunResult{Exit: 0, Normalized: after}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	checkRate(t, rates, "missing-field", "error", 1, 0, 1)
	added, dropped := diffLoopFindings(&cur, &next)
	if len(added) != 0 || len(dropped) != 1 {
		t.Errorf("added=%d dropped=%d, want 0 and 1", len(added), len(dropped))
	}
}

// TestFixRateWrongTypeKeepsSiblings keeps siblings on wrong type.
func TestFixRateWrongTypeKeepsSiblings(t *testing.T) {
	before := []byte(`{"comments":[{"path":"a","line":11,"body":"keep"},{"path":"a","line":42,"body":42},{"path":"a","line":51,"body":"keep2"}]}`)
	after := []byte(`{"comments":[{"path":"a","line":11,"body":"keep"},{"path":"a","line":51,"body":"keep2"}]}`)
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: before}
	cur.Dry = DryRunResult{Exit: 1}
	cur.Dry.Problems = wrongAt("$.comments[1].body")
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: after}
	next.Dry = DryRunResult{Exit: 0, Normalized: after}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	checkRate(t, rates, "wrong-type", "error", 1, 0, 1)
	added, dropped := diffLoopFindings(&cur, &next)
	if len(added) != 0 || len(dropped) != 1 {
		t.Errorf("added=%d dropped=%d, want 0 and 1", len(added), len(dropped))
	}
}

// TestFixRateEmptyEntryKeepsIndex keeps an empty entry indexed.
func TestFixRateEmptyEntryKeepsIndex(t *testing.T) {
	before := []byte(`{"comments":[{},{"path":"a","line":11,"body":"keep"}]}`)
	after := []byte(`{"comments":[{"path":"a","line":11,"body":"keep"}]}`)
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: before}
	cur.Dry = DryRunResult{Exit: 1}
	cur.Dry.Problems = append(missingAt("$.comments[0].body"),
		core.Problem{Path: "$.comments[0].rigor", Code: "rigor-unset", Level: "warning", Message: "m", Fix: "f"})
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: after}
	next.Dry = DryRunResult{Exit: 0, Normalized: after}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	checkRate(t, rates, "missing-field", "error", 1, 0, 1)
	checkRate(t, rates, "rigor-unset", "warning", 1, 0, 1)
	added, dropped := diffLoopFindings(&cur, &next)
	if len(added) != 0 || len(dropped) != 1 {
		t.Errorf("added=%d dropped=%d, want 0 and 1", len(added), len(dropped))
	}
}

// TestFixRateAliasDeleteIsDrop treats a deleted alias as a loss.
func TestFixRateAliasDeleteIsDrop(t *testing.T) {
	before := []byte(`{"comments":[{"path":"a","line":11,"body":"keep"},{"file":"a","line":42},{"path":"a","line":51,"body":"keep2"}]}`)
	after := []byte(`{"comments":[{"path":"a","line":11,"body":"keep"},{"path":"a","line":51,"body":"keep2"}]}`)
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: before}
	cur.Dry = DryRunResult{Exit: 1}
	cur.Dry.Problems = missingAt("$.comments[1].body")
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: after}
	next.Dry = DryRunResult{Exit: 0, Normalized: after}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	checkRate(t, rates, "missing-field", "error", 1, 0, 1)
	added, dropped := diffLoopFindings(&cur, &next)
	if len(added) != 0 || len(dropped) != 1 {
		t.Errorf("added=%d dropped=%d, want 0 and 1", len(added), len(dropped))
	}
}

// TestFixRateBodyRepairCountsFix keeps a repaired body as a fix.
func TestFixRateBodyRepairCountsFix(t *testing.T) {
	before := []byte(`{"comments":[{"path":"a","line":11}]}`)
	after := []byte(`{"comments":[{"path":"a","line":11,"body":"found"}]}`)
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: before}
	cur.Dry = DryRunResult{Exit: 1}
	cur.Dry.Problems = missingAt("$.comments[0].body")
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: after}
	next.Dry = DryRunResult{Exit: 0, Normalized: after}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	checkRate(t, rates, "missing-field", "error", 1, 1, 0)
}

// TestFixRateTwoDeletionsAreDrops counts two losses.
func TestFixRateTwoDeletionsAreDrops(t *testing.T) {
	before := []byte(`{"comments":[{"path":"a","line":11,"body":"keep"},{"path":"a","line":42},{"path":"a","line":51}]}`)
	after := []byte(`{"comments":[{"path":"a","line":11,"body":"keep"}]}`)
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: before}
	cur.Dry = DryRunResult{Exit: 1}
	cur.Dry.Problems = []core.Problem{
		{Path: "$.comments[1].body", Code: "missing-field", Level: "error", Message: "m", Fix: "f"},
		{Path: "$.comments[2].body", Code: "missing-field", Level: "error", Message: "m", Fix: "f"},
	}
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: after}
	next.Dry = DryRunResult{Exit: 0, Normalized: after}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	checkRate(t, rates, "missing-field", "error", 2, 0, 2)
}

// TestFixRateUnattributedWhenOwnerMissing marks a missing owner unattributed.
func TestFixRateUnattributedWhenOwnerMissing(t *testing.T) {
	before := []byte(`{"comments":[]}`)
	after := []byte(`{"comments":[{"path":"a","line":1,"body":"x"}]}`)
	cur := RoundAttempt{Round: 1, ReviewSHA: "aaa", ReviewBytes: before}
	cur.Dry = DryRunResult{Exit: 1}
	cur.Dry.Problems = missingAt("$.comments[0].body")
	next := RoundAttempt{Round: 2, ReviewSHA: "bbb", ReviewBytes: after}
	next.Dry = DryRunResult{Exit: 0, Normalized: after}
	rates := fixRatesFromOutcomes([]*LoopOutcome{{Attempts: []RoundAttempt{cur, next}}})
	for _, fr := range rates {
		if fr.Code != "missing-field" {
			continue
		}
		if fr.Shown != 1 || fr.Fixed != 0 || fr.Dropped != 0 || fr.Unattributed != 1 {
			t.Errorf("rate = %+v, want unattributed", fr)
		}
		return
	}
	t.Fatal("no missing-field row")
}

// TestLoopCostIncompleteIsLowerBound keeps a truncated spend as a lower bound.
func TestLoopCostIncompleteIsLowerBound(t *testing.T) {
	half := 0.42
	out := LoopOutcome{Attempts: []RoundAttempt{
		{Round: 1, Cost: AttemptCost{Wall: 1, EstUSD: &half, HasEst: true, Incomplete: true}},
	}}
	roll := loopCostRollup([]*LoopOutcome{&out})
	if roll.Attempts != 1 || roll.WithEstimate != 1 || roll.Usd != 0.42 {
		t.Errorf("roll = %+v, want 1/1/0.42", roll)
	}
	if roll.Incomplete != 1 || roll.Measured {
		t.Errorf("roll = %+v, want incomplete 1 and unmeasured", roll)
	}
}

func piStub(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stub.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("stub: %v", err)
	}
	return path
}

func runPiStubLoop(t *testing.T, stub string) (*Report, *Config) {
	t.Helper()
	needPOSIX(t)
	restoreWD(t)
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("moduleRoot: %v", err)
	}
	pi := filepath.Join(root, "bench", "agents", "pi.sh")
	t.Setenv("BENCH_PI_MODEL", "fixture-good")
	t.Setenv("BENCH_PI_BIN", stub)
	cfg := loopTwoRoundConfig(t, fakeCorpus(t), "sh "+pi)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err == nil {
		t.Fatal("want adapter failure")
	}
	if rep == nil || rep.LoopCost == nil || len(rep.Loops) != 1 {
		t.Fatalf("rep = %+v, want one failed loop", rep)
	}
	return rep, cfg
}

func checkAgentLogContains(t *testing.T, cfg *Config, slug, want string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(cfg.WorkDir, "runs", slug, "round-01", agentLogName))
	if err != nil {
		t.Fatalf("agent log: %v", err)
	}
	if !strings.Contains(string(raw), want) {
		t.Errorf("agent log = %q, want %q", string(raw)[:500], want)
	}
}

// TestLoopPiBadContentSavesCost saves spend when content extraction fails.
func TestLoopPiBadContentSavesCost(t *testing.T) {
	stub := piStub(t, "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[42],\"usage\":{\"cost\":{\"total\":0.42}}}}'\n")
	rep, cfg := runPiStubLoop(t, stub)
	got := rep.LoopCost
	if got.Attempts != 1 || got.WithEstimate != 1 || got.Usd != 0.42 || !got.Measured {
		t.Errorf("loop cost = %+v, want 1 measured at 0.42", got)
	}
	if got.Incomplete != 0 {
		t.Errorf("loop cost = %+v, want complete", got)
	}
	checkAgentLogContains(t, cfg, rep.Loops[0].Slug, "0.42")
	if est := rep.Loops[0].Attempts[0].EstUSD; est == nil || *est != 0.42 {
		t.Errorf("attempt spend = %v, want 0.42", est)
	}
}

// TestLoopPiTruncatedKeepsSalvagedCost keeps salvaged spend on truncation.
func TestLoopPiTruncatedKeepsSalvagedCost(t *testing.T) {
	stub := piStub(t, "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"not-json\"}],\"usage\":{\"input\":20,\"output\":5,\"cost\":{\"total\":0.42}}}}'\nprintf '{broken-event\\n'\n")
	rep, cfg := runPiStubLoop(t, stub)
	got := rep.LoopCost
	if got.Attempts != 1 || got.WithEstimate != 1 || got.Usd != 0.42 {
		t.Errorf("loop cost = %+v, want 1 salvaged at 0.42", got)
	}
	if got.Incomplete != 1 || got.Measured {
		t.Errorf("loop cost = %+v, want incomplete 1 and unmeasured lower bound", got)
	}
	checkAgentLogContains(t, cfg, rep.Loops[0].Slug, "0.42")
}

// TestLoopPiMissingUsageStaysUnmeasured keeps missing spend unmeasured.
func TestLoopPiMissingUsageStaysUnmeasured(t *testing.T) {
	stub := piStub(t, "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"not-json\"}],\"usage\":{\"input\":20,\"output\":5,\"cost\":{\"total\":0}}}}'\n")
	rep, cfg := runPiStubLoop(t, stub)
	got := rep.LoopCost
	if got.Measured || got.Usd != 0 || got.WithEstimate != 0 {
		t.Errorf("loop cost = %+v, want unmeasured", got)
	}
	if got.Incomplete != 0 {
		t.Errorf("loop cost = %+v, want no incomplete", got)
	}
	checkAgentLogContains(t, cfg, rep.Loops[0].Slug, "not-json")
}
