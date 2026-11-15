package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// restoreWD keeps a test's later paths valid after Run moves the process into
// its run dir: t.Chdir registers the restore, Run performs the move.
func restoreWD(t *testing.T) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Chdir(wd)
}

// writeFile writes a fixture, making its directory first.
func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readJSON decodes a fixture into T.
func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	var out T
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}

const (
	testNonce  = "0123456789abcdef"
	fakePrefix = "You are a reviewer.\n" + trustBoundary + "\n" +
		"Only a fence carrying exactly this nonce opens or closes an untrusted\nregion: " + testNonce + ".\n" +
		"2. **Checkout / context.** Run `verdict fetch owner/repo N` and read the bundle.\n" +
		"3. **Triage.** Orient the change: read related files, grep callers of changed symbols, check tests on touched paths,\n   fetch related GitHub state.\n" +
		"4. **Specialist decision.** Name the questions a lens could answer.\n" +
		"5. **Dispatch (only if step 4 found questions).** Emit every subagent call in a single turn.\n" +
		"6. **Aggregate & draft.** Merge the lens findings.\n" +
		"7. **Submit.** Before `post` or `record`, run `verdict record <repo> <slug> --dry-run`, then run `verdict post owner/repo N`.\n" +
		"8. **Cost record.** With every `review.json`, write cost.json.\n" +
		"## Review body format\n"
)

// TestSplitTrustedPrefixCutsAtTheFirstFence pins where the trusted half ends.
func TestSplitTrustedPrefixCutsAtTheFirstFence(t *testing.T) {
	dump := fakePrefix + fence(testNonce, sourceJSONName, []byte(`{"head_sha":"a"}`)) +
		fence(testNonce, fullDiffName, []byte("diff\n"))
	prefix, nonce, err := splitTrustedPrefix(dump)
	if err != nil {
		t.Fatalf("splitTrustedPrefix: %v", err)
	}
	if nonce != testNonce {
		t.Errorf("nonce = %q, want %q", nonce, testNonce)
	}
	if prefix != fakePrefix {
		t.Errorf("prefix = %q, want %q", prefix, fakePrefix)
	}
}

// TestSplitTrustedPrefixIgnoresTheProseFenceShape keeps prose that resembles a
// fence marker in the trusted half.
func TestSplitTrustedPrefixIgnoresTheProseFenceShape(t *testing.T) {
	dump := fakePrefix + "The shape is <<<UNTRUSTED nonce=<hex> source=<file>>> on its own.\n" +
		fence(testNonce, fullDiffName, []byte("diff\n"))
	prefix, nonce, err := splitTrustedPrefix(dump)
	if err != nil {
		t.Fatalf("splitTrustedPrefix: %v", err)
	}
	if nonce != testNonce {
		t.Errorf("nonce = %q, want %q", nonce, testNonce)
	}
	if !strings.HasSuffix(prefix, "on its own.\n") {
		t.Errorf("prefix did not keep the prose before the first real fence: %q", prefix)
	}
}

// TestSplitTrustedPrefixRefusesADumpWithoutWhatItNeeds refuses a dump the
// runner cannot reproduce.
func TestSplitTrustedPrefixRefusesADumpWithoutWhatItNeeds(t *testing.T) {
	for name, dump := range map[string]string{
		"no fence":      "no fence here\n",
		"no boundary":   "plain\n" + fence(testNonce, fullDiffName, nil),
		"short nonce":   fakePrefix + "<<<UNTRUSTED nonce=abc source=full.diff>>>\n",
		"uppercase hex": fakePrefix + "<<<UNTRUSTED nonce=0123456789ABCDEF source=full.diff>>>\n",
	} {
		if _, _, err := splitTrustedPrefix(dump); err == nil {
			t.Errorf("%s: splitTrustedPrefix accepted an unusable dump", name)
		}
	}
}

// TestAssemblePromptFencesTheMetaAndDiffInOrder pins the untrusted layout.
func TestAssemblePromptFencesTheMetaAndDiffInOrder(t *testing.T) {
	source := []byte(`{"head_sha":"a","base_sha":"b"}`)
	prompt := assemblePrompt(fakePrefix, testNonce, source, []byte("diff --git a/x b/x\n+added"))
	if !strings.HasPrefix(prompt, fakePrefix) {
		t.Fatal("prompt does not open with the trusted prefix")
	}
	meta := strings.Index(prompt, "<<<UNTRUSTED nonce="+testNonce+" source="+sourceJSONName+">>>")
	diff := strings.Index(prompt, "<<<UNTRUSTED nonce="+testNonce+" source="+fullDiffName+">>>")
	if meta < 0 || diff < 0 || meta > diff {
		t.Fatalf("fenced sections out of order: meta=%d diff=%d", meta, diff)
	}
	if !strings.HasSuffix(prompt, "+added\n<<<END UNTRUSTED nonce="+testNonce+">>>\n") {
		t.Errorf("a diff without a trailing newline did not gain one before the close marker: %q", prompt[len(prompt)-80:])
	}
}

// TestCaseSourceIsDeterministicAndSynthetic pins the synthesized provenance.
func TestCaseSourceIsDeterministicAndSynthetic(t *testing.T) {
	first := caseSource(&corpusCase{id: "off-by-one", diff: []byte("diff\n")})
	again := caseSource(&corpusCase{id: "off-by-one", diff: []byte("diff\n")})
	other := caseSource(&corpusCase{id: "other", diff: []byte("diff\n")})
	if !bytes.Equal(first, again) {
		t.Errorf("caseSource is not deterministic: %q then %q", first, again)
	}
	if bytes.Equal(first, other) {
		t.Error("two cases share one provenance stamp")
	}
	var meta struct {
		HeadSHA string `json:"head_sha"`
		BaseSHA string `json:"base_sha"`
	}
	if err := json.Unmarshal(first, &meta); err != nil {
		t.Fatalf("caseSource: %v", err)
	}
	if len(meta.HeadSHA) != 40 || len(meta.BaseSHA) != 40 {
		t.Errorf("shas are not 40 hex chars: head=%q base=%q", meta.HeadSHA, meta.BaseSHA)
	}
}

// TestCaseSourceStampsTheReviewedPair pins a real case's stamp: the commits the
// manifest names, not hashes of the case bytes, so a bundle's base_sha lands on
// the repository the diff came from.
func TestCaseSourceStampsTheReviewedPair(t *testing.T) {
	got := string(caseSource(&corpusCase{
		id:      "urfave-cli-2319-required-flags-help-name",
		diff:    []byte("diff --git a/command.go b/command.go\n"),
		baseSHA: "b79d76894ba609be305dbb458a95f1bad6ef7f2f",
		headSHA: "5af95006c1cb31cdeb4d9c90ef3303af10950eb6",
	}))
	want := "{\"head_sha\":\"5af95006c1cb31cdeb4d9c90ef3303af10950eb6\"," +
		"\"base_sha\":\"b79d76894ba609be305dbb458a95f1bad6ef7f2f\"}\n"
	if got != want {
		t.Errorf("caseSource = %q, want %q", got, want)
	}
}

// TestReadCaseRefusesARealCaseWithoutCommits refuses a real manifest the
// runner cannot stamp: its bundle would carry an empty base_sha.
func TestReadCaseRefusesARealCaseWithoutCommits(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "case-a", caseManifestName),
		[]byte(`{"schema_version":"1","id":"case-a","kind":"real","provenance":{"kind":"real","repo":"o/r","pr":1},"bugs":[{"path":"x","line":1}]}`))
	writeFile(t, filepath.Join(dir, "case-a", fullDiffName), []byte("diff\n"))
	if _, _, err := loadCases(dir, nil); err == nil || !strings.Contains(err.Error(), `provenance.base_sha "" is not a 40-character lowercase hex commit id`) {
		t.Fatalf("loadCases error = %v, want a missing-commit refusal", err)
	}
}

// TestReadCaseRefusesADiffSpelledKeyword pins the guard on the run path: a
// real case whose keyword the diff itself spells never reaches an agent.
func TestReadCaseRefusesADiffSpelledKeyword(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "case-a", caseManifestName), []byte(
		`{"schema_version":"1","id":"case-a","kind":"real","provenance":{"kind":"real","repo":"o/r","pr":1,`+
			`"base_sha":"`+strings.Repeat("a", 40)+`","head_sha":"`+strings.Repeat("b", 40)+`",`+
			`"fix_commit":"`+strings.Repeat("c", 40)+`"},`+
			`"bugs":[{"path":"command.go","line":3,"kind":"validation-bypass","severity":"important",`+
			`"keywords":["boundary check","stoponntharg"]}]}`))
	writeFile(t, filepath.Join(dir, "case-a", fullDiffName), []byte("+\tif StopOnNthArg > 0 {\n"))
	if _, _, err := loadCases(dir, nil); err == nil || !strings.Contains(err.Error(), `"stoponntharg"`) {
		t.Fatalf("loadCases error = %v, want a refusal naming the keyword", err)
	}
}

// TestCostRollupCountsUnpricedRefusals pins the counter split: a refused
// sample the agent priced contributes to refused_usd, one it did not still
// counts in refused_samples, and both leave the rollup unmeasured.
func TestCostRollupCountsUnpricedRefusals(t *testing.T) {
	r := &runner{cfg: &Config{}, spend: []sampleSpend{
		{usd: 0.03, refused: true, has: true},
		{refused: true},
		{usd: 0.01, has: true},
	}}
	cost := r.cost()
	if cost.RefusedSamples != 2 || cost.RefusedUsd != 0.03 {
		t.Errorf("refused_samples=%d refused_usd=%v, want 2 and 0.03", cost.RefusedSamples, cost.RefusedUsd)
	}
	if cost.Samples != 3 || cost.SampleEstimates != 2 || cost.Usd != 0.04 {
		t.Errorf("cost = %+v, want 3 samples, 2 priced, 0.04 USD", cost)
	}
	if cost.Measured {
		t.Error("measured = true, want false: one sample carries no est_usd")
	}
}

// TestSampleSpendFlagsAnUnpricedRound pins what a loop sample knows about its
// spend: a round that cost.json never priced leaves the total a lower bound,
// so the sample must read incomplete even though another round was priced. The
// old `continue` dropped the round silently and the sample read complete.
func TestSampleSpendFlagsAnUnpricedRound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "round-01", costJSONName), []byte(`{"est_usd":0.03}`))
	writeFile(t, filepath.Join(dir, "round-02", costJSONName), []byte(`{"incomplete":false}`))
	r := &runner{cfg: &Config{Loop: 2}}
	spend := r.sampleSpend(&caseRun{dir: dir}, false)
	if !spend.has || spend.usd != 0.03 {
		t.Fatalf("spend = %+v, want the priced round's 0.03 kept", spend)
	}
	if !spend.incomplete {
		t.Fatal("incomplete = false, want true: round-02 carries no est_usd")
	}
	r.spend = []sampleSpend{spend}
	cost := r.cost()
	if cost.SampleIncomplete != 1 || cost.Measured {
		t.Errorf("cost = %+v, want one incomplete sample and measured false", cost)
	}
	if cost.Usd != 0.03 || cost.RefusedUsd != 0 {
		t.Errorf("measured_usd=%v refused_usd=%v, want 0.03 and 0", cost.Usd, cost.RefusedUsd)
	}
}

// TestBudgetRefusalNeedsNoMeasuredRun pins the cap's spend base when only part
// of the run is priced: known spend is a lower bound, so a run at the cap with
// one unpriced sample must still be refused. Requiring cost.measured let it be
// published with the budget already spent.
func TestBudgetRefusalNeedsNoMeasuredRun(t *testing.T) {
	budget := 0.05
	rep := &Report{
		Cost:      CostRollup{Usd: 0.06, Samples: 2, SampleEstimates: 1, Measured: false},
		BudgetUSD: &budget,
	}
	if !atBudget(rep) {
		t.Fatal("atBudget = false, want true: 0.06 known spend is at the 0.05 cap")
	}
	if got := strings.Join(budgetReasons(rep), "; "); !strings.Contains(got, "reached the 0.050000 USD budget") {
		t.Errorf("budgetReasons = %q, want the spend refusal", got)
	}
	rep.Cost.Usd = 0.04
	if atBudget(rep) {
		t.Error("atBudget = true for 0.04 under a 0.05 cap")
	}
	rep.BudgetUSD = nil
	rep.Cost.Usd = 1
	if atBudget(rep) {
		t.Error("atBudget = true with no budget set")
	}
}

// TestMaxUSDRefusesNonFiniteCaps pins the flag's domain: NaN passes every
// comparison, so the old "< 0" test let it through and capReached then never
// fired; +Inf ran the whole corpus and made the report unserializable, because
// encoding/json refuses Inf.
func TestMaxUSDRefusesNonFiniteCaps(t *testing.T) {
	for _, tc := range []struct {
		name   string
		refuse string
		limit  float64
	}{
		{name: "NaN", limit: math.NaN(), refuse: "finite number"},
		{name: "+Inf", limit: math.Inf(1), refuse: "finite number"},
		{name: "-Inf", limit: math.Inf(-1), refuse: "finite number"},
		{name: "negative", limit: -1, refuse: "zero (no cap) or positive"},
		{name: "zero", limit: 0},
		{name: "positive", limit: 0.05},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkMaxUSD(t, tc.limit, tc.refuse)
		})
	}
}

// checkMaxUSD pins one -max-usd value: accepted, or refused in words naming the
// rule it broke.
func checkMaxUSD(t *testing.T, limit float64, refuse string) {
	t.Helper()
	err := (&Config{Runs: 1, Loop: 1, MaxUSD: limit}).validateNumbers()
	if refuse == "" {
		if err != nil {
			t.Fatalf("validateNumbers(%v) = %v, want accepted", limit, err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), refuse) {
		t.Fatalf("validateNumbers(%v) = %v, want a refusal naming %q", limit, err, refuse)
	}
}

// TestMaxUSDCountsRefusedSpend pins the cap's spend base: a refused sample
// costs what its agent reported, so three refusals at 0.03 against a 0.05 cap
// stop after two. A rollup that ignored refused spend would let all three run.
func TestMaxUSDCountsRefusedSpend(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	cfg := benchConfig(t, fakeCorpus(t), agentWritesSpend)
	cfg.Runs = 3
	cfg.MaxUSD = 0.05
	fake := &fakeGrader{
		model:   cfg.Model,
		panel:   cfg.Panel,
		prefix:  fakePrefix,
		refusal: jq.NewErr("record: bad-review-input: coverage[0] wants file and ranges"),
	}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err == nil {
		t.Fatal("runGraded reported success after three refused samples")
	}
	if rep.Samples != 2 || len(rep.Skipped) != 1 {
		t.Fatalf("samples=%d skipped=%v, want two run and one skipped", rep.Samples, rep.Skipped)
	}
	if got, want := rep.Cost.Usd, 0.06; got != want || rep.Cost.RefusedUsd != want {
		t.Errorf("measured=%v refused=%v, want %v (refused spend funds the cap)", got, rep.Cost.RefusedUsd, want)
	}
	if !rep.CapHit {
		t.Error("cap_hit = false, want the cap to stop the third refused sample")
	}
}

// TestCommandsRecordTheBudget pins the reproduction command: a capped run must
// name its cap, and an uncapped one must not grow a -max-usd 0 the operator
// never typed.
func TestCommandsRecordTheBudget(t *testing.T) {
	capped := cappedMultiCaseReport(t)
	command := strings.Join(capped.Commands[0], " ")
	if !strings.Contains(command, "-max-usd 0.05") {
		t.Errorf("command = %q, want the cap it ran under", command)
	}
	plain := oneSampleReport(t, 0)
	if plain.BudgetUSD != nil {
		t.Fatalf("budget = %v, want none for an uncapped run", plain.BudgetUSD)
	}
	if command := strings.Join(plain.Commands[0], " "); strings.Contains(command, "-max-usd") {
		t.Errorf("command = %q, want no budget flag on an uncapped run", command)
	}
}

// TestBudgetReachedWithoutASkipIsNotCitable pins the other half of the budget
// rule: when the last scheduled sample pushes spend to the cap, nothing is
// skipped, so cap_hit stays false — and the run must still not be citable.
func TestBudgetReachedWithoutASkipIsNotCitable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limit  float64
		reason bool
	}{
		{name: "past the cap", limit: 0.01, reason: true},
		{name: "exactly at the cap", limit: 0.03, reason: true},
		{name: "under the cap", limit: 0.04, reason: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkBudgetRefusal(t, oneSampleReport(t, tc.limit), tc.reason)
		})
	}
}

// checkBudgetRefusal asserts a full run at a cap either carries the budget
// refusal and is uncitable, or carries neither.
func checkBudgetRefusal(t *testing.T, rep *Report, want bool) {
	t.Helper()
	if rep.CapHit || len(rep.Skipped) != 0 {
		t.Fatalf("cap_hit=%v skipped=%v, want no skipped sample", rep.CapHit, rep.Skipped)
	}
	got := strings.Contains(rep.CitableReason, "reached the 0.0")
	if got != want {
		t.Errorf("citable=%v reason=%q, want the budget reason present=%v", rep.Citable, rep.CitableReason, want)
	}
	if want && rep.Citable {
		t.Errorf("citable=%v, want a run that spent its budget refused", rep.Citable)
	}
}

// oneSampleReport runs one priced 0.03 USD sample against a cap.
func oneSampleReport(t *testing.T, limit float64) *Report {
	t.Helper()
	needPOSIX(t)
	restoreWD(t)
	cfg := benchConfig(t, fakeCorpus(t), agentWritesSpend)
	cfg.MaxUSD = limit
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err != nil {
		t.Fatalf("runGraded: %v", err)
	}
	return rep
}

// TestMalformedRealControlNeverReachesAnAgent pins the pre-schedule manifest
// validation: the runner's own check asked only that a real case's commit pair
// be non-empty, so a control with base_sha "x" passed it, spent a sample, and
// failed later at scoring. The corpus validator now runs first.
func TestMalformedRealControlNeverReachesAnAgent(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	t.Setenv("BENCH_TEST_REVIEW", reviewBody)
	marker := filepath.Join(t.TempDir(), "agent-ran")
	root := fakeCorpusOf(t, "case-a")
	writeFile(t, filepath.Join(root, "corpus", "case-a", caseManifestName), []byte(
		`{"schema_version":"1","id":"case-a","kind":"control","provenance":{"kind":"real","repo":"o/r","pr":1,`+
			`"base_sha":"x","head_sha":"`+strings.Repeat("b", 40)+`"},"bugs":[]}`))
	cfg := benchConfig(t, root, `touch `+marker+`
`+agentCopiesReview)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	if _, err := runGraded(context.Background(), cfg, fake); err == nil ||
		!strings.Contains(err.Error(), `provenance.base_sha "x" is not a 40-character lowercase hex commit id`) {
		t.Fatalf("run = %v, want a base_sha refusal", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("agent marker exists: a malformed case reached the agent")
	}
}

// TestReadCaseRefusesAContextAnchor pins the anchor guard on the run path: a
// real case whose ground truth is a context line never reaches an agent.
func TestReadCaseRefusesAContextAnchor(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "case-a", caseManifestName), []byte(
		`{"schema_version":"1","id":"case-a","kind":"real","provenance":{"kind":"real","repo":"o/r","pr":1,`+
			`"base_sha":"`+strings.Repeat("a", 40)+`","head_sha":"`+strings.Repeat("b", 40)+`",`+
			`"fix_commit":"`+strings.Repeat("c", 40)+`"},`+
			`"bugs":[{"path":"command.go","line":4,"kind":"validation-bypass","severity":"important",`+
			`"keywords":["boundary check"]}]}`))
	writeFile(t, filepath.Join(dir, "case-a", fullDiffName), []byte(
		"diff --git a/command.go b/command.go\n--- a/command.go\n+++ b/command.go\n"+
			"@@ -1,3 +1,4 @@\n \tfirst()\n \tsecond()\n+\tthird()\n \tfourth()\n"))
	if _, _, err := loadCases(dir, nil); err == nil || !strings.Contains(err.Error(), "command.go:4") {
		t.Fatalf("loadCases error = %v, want a context-anchor refusal naming the line", err)
	}
}

// TestSlugKeepsBundleAlphabet pins the bundle-name alphabet.
func TestSlugKeepsBundleAlphabet(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"deepseek-flash", "deepseek-flash"},
		{"Vendor/Model:1", "vendor-model-1"},
		{"  spaced  ", "spaced"},
	} {
		if got := slug(tc.in); got != tc.want {
			t.Errorf("slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNormalizeCostSynthesizesARecord pins the record a silent agent gets.
func TestNormalizeCostSynthesizesARecord(t *testing.T) {
	doc, err := normalizeCost(nil, "fixture-good", 1.2345)
	if err != nil {
		t.Fatalf("normalizeCost: %v", err)
	}
	rec := readJSON[struct {
		SchemaVersion string `json:"schema_version"`
		Effort        string `json:"effort"`
		Models        []struct {
			Name string `json:"name"`
		} `json:"models"`
		Wall float64 `json:"wall_seconds"`
	}](t, writeTemp(t, doc))
	if rec.SchemaVersion != "2" || rec.Effort != "standard" || rec.Wall != 1.235 {
		t.Errorf("record = %+v, want v2/standard/wall 1.235", rec)
	}
	if len(rec.Models) != 1 || rec.Models[0].Name != "fixture-good" {
		t.Errorf("models = %+v, want exactly fixture-good", rec.Models)
	}
}

// TestNormalizeCostPrependsTheModel keeps the agent's own spend row.
func TestNormalizeCostPrependsTheModel(t *testing.T) {
	doc, err := normalizeCost([]byte(`{"models":[{"name":"other","input_tokens":10}],"est_usd":0.5}`), "fixture-good", 2)
	if err != nil {
		t.Fatalf("normalizeCost: %v", err)
	}
	rec := readJSON[struct {
		Models []struct {
			Name  string  `json:"name"`
			Input float64 `json:"input_tokens"`
		} `json:"models"`
		Usd float64 `json:"est_usd"`
	}](t, writeTemp(t, doc))
	if rec.Usd != 0.5 {
		t.Errorf("est_usd = %v, want 0.5 kept", rec.Usd)
	}
	if len(rec.Models) != 2 || rec.Models[0].Name != "fixture-good" || rec.Models[1].Input != 10 {
		t.Errorf("models = %+v, want fixture-good prepended and the agent's row kept", rec.Models)
	}
}

// TestNormalizeCostRefusesAnArray stops a spend array from becoming a record.
func TestNormalizeCostRefusesAnArray(t *testing.T) {
	if _, err := normalizeCost([]byte(`[{"models":[]}]`), "fixture-good", 1); err == nil {
		t.Fatal("normalizeCost accepted an array of spend records")
	}
}

// writeTemp writes one decoded document where readJSON can read it back.
func writeTemp(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.json")
	writeFile(t, path, body)
	return path
}

// TestStatSummarizesRunsAndKeepsNullsEmpty pins the spread helper.
func TestStatSummarizesRunsAndKeepsNullsEmpty(t *testing.T) {
	if got := stat(nil); got.Median != nil || got.Min != nil || got.Max != nil {
		t.Errorf("stat(nil) = %+v, want nulls", got)
	}
	one, half := 1.0, 0.5
	got := stat([]*float64{&one, nil, &half})
	if got.Min == nil || *got.Min != 0.5 || got.Max == nil || *got.Max != 1 {
		t.Errorf("stat = %+v, want min 0.5 max 1", got)
	}
	if got.Median == nil || *got.Median != 0.75 {
		t.Errorf("median = %v, want 0.75 across two samples", got.Median)
	}
}

// TestLoadCasesRefusesAManifestThatDoesNotMatchItsDirectory refuses a corpus
// whose ids and directories disagree.
func TestLoadCasesRefusesAManifestThatDoesNotMatchItsDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "case-a", caseManifestName), []byte(
		`{"schema_version":"1","id":"case-b","kind":"seeded","provenance":{"kind":"seeded","injected":"x"},`+
			`"bugs":[{"path":"x","line":1,"kind":"nil-deref","severity":"critical"}]}`))
	writeFile(t, filepath.Join(dir, "case-a", fullDiffName), []byte("diff\n"))
	if _, _, err := loadCases(dir, nil); err == nil || !strings.Contains(err.Error(), "does not match its directory") {
		t.Fatalf("loadCases error = %v, want an id-mismatch refusal", err)
	}
}

// TestLoadCasesRefusesABuglessManifest refuses a case that cannot be scored.
func TestLoadCasesRefusesABuglessManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "case-a", caseManifestName), []byte(
		`{"schema_version":"1","id":"case-a","kind":"seeded","provenance":{"kind":"seeded","injected":"x"},"bugs":[]}`))
	writeFile(t, filepath.Join(dir, "case-a", fullDiffName), []byte("diff\n"))
	if _, _, err := loadCases(dir, nil); err == nil || !strings.Contains(err.Error(), "needs at least one ground-truth bug") {
		t.Fatalf("loadCases error = %v, want a bugless refusal", err)
	}
}

// fakeGrader stands in for the shipped binary: it writes the record store the
// runner reads back and hands over canned calibrate data, so the loop can be
// tested without a build.
type fakeGrader struct {
	dry     func(slug string) (DryRunResult, error)
	refusal error
	refuse  string
	model   string
	prefix  string
	panel   []string
	calls   []string
}

func (f *fakeGrader) promptPrefix(context.Context) (string, string, error) {
	f.calls = append(f.calls, "promptPrefix")
	return f.prefix, testNonce, nil
}

func (f *fakeGrader) record(_ context.Context, slug string) ([]string, error) {
	f.calls = append(f.calls, "record "+slug)
	if f.refusal != nil && (f.refuse == "" || strings.Contains(slug, f.refuse)) {
		return nil, f.refusal
	}
	doc, err := json.Marshal(map[string]any{"model": f.model, "prompts_hash": "f00d"})
	if err != nil {
		return nil, err
	}
	return []string{"rebuilt the hunk snapshot"}, os.WriteFile(filepath.Join(reviewRoot, slug, recordJSONName), doc, 0o600)
}

func (f *fakeGrader) dryRun(_ context.Context, slug string) (DryRunResult, error) {
	f.calls = append(f.calls, "dryRun "+slug)
	if f.dry != nil {
		return f.dry(slug)
	}
	return DryRunResult{Exit: 0, Envelope: []byte(`{"data":{}}`), Normalized: []byte(`{"schema_version":"1","body":"ok","approved":false,"comments":[]}`)}, nil
}

func (f *fakeGrader) calibrate(_ context.Context, slug string) (json.RawMessage, error) {
	f.calls = append(f.calls, "calibrate "+slug)
	return json.Marshal(map[string]any{
		"bundle": slug, "case": "off-by-one", "kind": "seeded", "model": f.model, "panel": f.panel,
		"line_tolerance": 3, "ground_truth": 2, "reported": 3, "matched": 2,
		"false_positives": 1, "missed": 0, "precision": 0.6667, "recall": 1, "false_discovery_rate": 0.3333,
	})
}

func (f *fakeGrader) trend(context.Context) (json.RawMessage, error) {
	f.calls = append(f.calls, "trend")
	return json.RawMessage(`{"cases":1,"bundles":1,"ground_truth":2,"reported":3,"matched":2,"false_positives":1,"missed":0,"precision":0.6667,"recall":1,"false_discovery_rate":0.3333,"by_model":[{"model":"fixture-good"}]}`), nil
}

const reviewBody = `{"schema_version":"1","body":"ok","approved":false,"comments":[{"path":"pagination.go","line":42,"body":"off by one"}]}`

// fakeCorpus writes a one-case corpus plus the shipped prompt set the runner
// copies into every run dir.
func fakeCorpus(t *testing.T) string {
	t.Helper()
	return fakeCorpusOf(t, "off-by-one")
}

// fakeCorpusOf writes one seeded case per id, in the order given, so a test can
// drive a corpus the cap must stop part-way through.
func fakeCorpusOf(t *testing.T, ids ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range promptFiles {
		writeFile(t, filepath.Join(root, prefixPrompts, filepath.FromSlash(rel)), []byte("fixture prompt "+rel+"\n"))
	}
	dir := filepath.Join(root, "corpus")
	for _, id := range ids {
		writeFile(t, filepath.Join(dir, id, caseManifestName),
			[]byte(`{"schema_version":"1","id":"`+id+`","kind":"seeded","line_tolerance":3,`+
				`"provenance":{"kind":"seeded","injected":"the bound skips the last element"},`+
				`"bugs":[{"path":"pagination.go","line":42,"kind":"off-by-one","severity":"critical"}]}`))
		writeFile(t, filepath.Join(dir, id, fullDiffName), []byte(fakeDiff))
	}
	return root
}

// fakeDiff is one reviewed file with one hunk, so the bundle has a snapshot
// to write.
const fakeDiff = `diff --git a/pagination.go b/pagination.go
--- a/pagination.go
+++ b/pagination.go
@@ -1,3 +1,3 @@
+for i := range items {
`

// agentCopiesReview is the smallest conforming agent: it writes the review the
// test supplies through the environment.
const agentCopiesReview = `printf '%s' "$BENCH_TEST_REVIEW" > "$VERDICT_BENCH_OUT"`

// agentFails is the smallest failing agent: a non-zero exit and no review.
const agentFails = `printf 'agent exploded'; exit 3`

// agentWritesSpend is a conforming agent that also reports a priced sample:
// the refused-sample rollup can only be tested with spend on the other side.
const agentWritesSpend = `printf '%s' "$BENCH_TEST_REVIEW" > "$VERDICT_BENCH_OUT"
printf '{"est_usd":0.03}' > "$VERDICT_BENCH_COST"`

// runRefusal is Run's error for an unconfigured run: the loop must refuse
// before it touches the corpus or the working directory.
func runRefusal(cfg *Config) error {
	_, err := Run(context.Background(), cfg)
	return err
}

// benchConfig is a one-case fixture run.
func benchConfig(t *testing.T, root, agent string) *Config {
	t.Helper()
	return &Config{
		K:       1,
		Root:    root,
		Corpus:  filepath.Join(root, "corpus"),
		Verdict: os.Args[0],
		WorkDir: t.TempDir(),
		Out:     filepath.Join(t.TempDir(), "run.json"),
		Agent:   agent,
		Model:   "fixture-good",
		Panel:   []string{"panel-alpha"},
		Runs:    1,
		Timeout: 30 * time.Second,
		Now:     time.Date(2026, 11, 5, 1, 2, 3, 0, time.UTC),
		Summary: io.Discard,
	}
}

// needPOSIX skips a test that drives Run, which refuses Windows because it
// runs agents through sh(1).
func needPOSIX(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the bench runner drives agents through sh(1)")
	}
}

// runFixture drives one sample of the fake corpus through a canned scorer.
func runFixture(t *testing.T) (*Report, *Config, *fakeGrader) {
	t.Helper()
	needPOSIX(t)
	restoreWD(t)
	t.Setenv("BENCH_TEST_REVIEW", reviewBody)
	cfg := benchConfig(t, fakeCorpus(t), agentCopiesReview)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err != nil {
		t.Fatalf("runGraded: %v", err)
	}
	if rep.Samples != 1 || len(rep.Failures) != 0 {
		t.Fatalf("samples=%d failures=%v, want one clean sample", rep.Samples, rep.Failures)
	}
	return rep, cfg, fake
}

// TestRunScoresOneSampleEndToEnd drives the loop with a canned scorer.
func TestRunScoresOneSampleEndToEnd(t *testing.T) {
	rep, cfg, fake := runFixture(t)
	if rep.Citable || !strings.Contains(rep.CitableReason, "1 sample") {
		t.Errorf("citable=%v reason=%q, want a one-sample refusal", rep.Citable, rep.CitableReason)
	}
	if len(rep.Cases) != 1 || rep.Cases[0].Precision.Min == nil || *rep.Cases[0].Precision.Min != 0.6667 {
		t.Errorf("cases = %+v, want the fake's precision", rep.Cases)
	}
	if rep.PromptsHash != "f00d" {
		t.Errorf("prompts_hash = %q, want the record stamp", rep.PromptsHash)
	}
	if !slices.Equal(fake.calls, []string{"promptPrefix", "record corpus-off-by-one-fixture-good-run1", "calibrate corpus-off-by-one-fixture-good-run1", "trend"}) {
		t.Errorf("grader calls = %v, want prefix, record, calibrate, trend in that order", fake.calls)
	}
	checkBundleAndPublication(t, cfg, rep)
}

// checkBundleAndPublication pins what a clean sample leaves behind.
func checkBundleAndPublication(t *testing.T, cfg *Config, rep *Report) {
	t.Helper()
	bundle := filepath.Join(reviewRoot, "corpus-off-by-one-fixture-good-run1")
	checkBundleFiles(t, bundle)
	checkBundleSpend(t, filepath.Join(bundle, costJSONName), cfg.Model)
	checkBundleHunks(t, bundle)
	checkPromptProvenance(t, cfg, rep)
	if got := readJSON[map[string]any](t, cfg.Out)["schema_version"]; got != "1" {
		t.Errorf("publication schema_version = %v, want 1", got)
	}
}

// checkBundleFiles pins the files record needs to score the bundle later.
func checkBundleFiles(t *testing.T, bundle string) {
	t.Helper()
	for _, name := range []string{
		fullDiffName, hunksTSVName, corpusJSONName, sourceJSONName, panelName,
		reviewJSONName, costJSONName, recordJSONName, "controls.json",
	} {
		if _, err := os.Stat(filepath.Join(bundle, name)); err != nil {
			t.Errorf("bundle is missing %s: %v", name, err)
		}
	}
	if policy := readJSON[map[string]any](t, filepath.Join(bundle, "controls.json"))["policy"]; policy != "head" {
		t.Errorf("controls policy = %v, want head (a seeded corpus has no base commit)", policy)
	}
}

// checkBundleHunks pins the snapshot record keeps: the bundle's own hunks.tsv,
// indexed from the diff the agent reviewed, so a later post or re-score cannot
// move an anchor by rebuilding it from a diff that has since changed.
func checkBundleHunks(t *testing.T, bundle string) {
	t.Helper()
	diff, err := os.ReadFile(filepath.Join(bundle, fullDiffName))
	if err != nil {
		t.Fatalf("read bundle diff: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(bundle, hunksTSVName))
	if err != nil {
		t.Fatalf("read bundle hunks: %v", err)
	}
	if want := hunks.Format(hunks.Parse(string(diff))); string(got) != want {
		t.Errorf("hunks.tsv = %q, want the bundle diff's index %q", got, want)
	}
}

// checkBundleSpend keeps the agent's own spend row rather than synthesizing.
func checkBundleSpend(t *testing.T, path, model string) {
	t.Helper()
	spend := readJSON[struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}](t, path)
	if len(spend.Models) != 1 || spend.Models[0].Name != model {
		t.Errorf("cost.json models = %+v, want the run's model", spend.Models)
	}
}

// checkPromptProvenance ties the published sha to the bytes the agent read
// plus the shipped bytes they derive from: either side moving changes the pin.
func checkPromptProvenance(t *testing.T, cfg *Config, rep *Report) {
	t.Helper()
	promptPath := filepath.Join(cfg.WorkDir, "runs", "corpus-off-by-one-fixture-good-run1", promptFile)
	raw, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("read %s: %v", promptPath, err)
	}
	sum := sha256.Sum256([]byte(fakePrefix + "\x00" + string(raw)))
	if got, want := rep.PromptSHA["off-by-one"], hex.EncodeToString(sum[:]); got != want {
		t.Errorf("prompt sha256 = %q, want the shipped plus derived bytes (%q)", got, want)
	}
	if _, err := os.Stat(filepath.Join(cfg.WorkDir, prefixPrompts, "SKILL.md")); err != nil {
		t.Errorf("the run dir has no prompt set: %v", err)
	}
}

// TestRunKeepsEvidenceWhenTheAgentFails keeps a broken sample's log.
func TestRunKeepsEvidenceWhenTheAgentFails(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	cfg := benchConfig(t, fakeCorpus(t), agentFails)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err == nil {
		t.Fatal("runGraded reported success after a failing agent")
	}
	if rep == nil {
		t.Fatalf("runGraded dropped the report on a sample failure: %v", err)
	}
	if len(rep.Failures) != 1 || !strings.Contains(rep.Failures[0].Error, "agent exit 3") {
		t.Fatalf("failures = %+v, want one agent-exit refusal", rep.Failures)
	}
	if _, statErr := os.Stat(filepath.Join(reviewRoot, "corpus-off-by-one-fixture-good-run1")); !os.IsNotExist(statErr) {
		t.Error("a failed sample left a bundle behind: trend would warn on it every run")
	}
	checkEvidenceLog(t, rep)
	if !slices.Contains(fake.calls, "promptPrefix") {
		t.Errorf("grader calls = %v, want the prefix rendered", fake.calls)
	}
}

// checkEvidenceLog keeps a failed sample's agent output readable after the run.
func checkEvidenceLog(t *testing.T, rep *Report) {
	t.Helper()
	log, err := os.ReadFile(filepath.Join(rep.Failures[0].Evidence, agentLogName))
	if err != nil {
		t.Fatalf("evidence log: %v", err)
	}
	if !strings.Contains(string(log), "agent exploded") {
		t.Errorf("evidence log = %q, want the agent's output", log)
	}
}

// TestCostRollupCarriesRefusedSampleSpend pins the rollup the pilot got wrong:
// two samples each spent 0.03 USD, the second was refused by record, and the
// report published 0.03 for the pair. Spend belongs to every sample, and the
// refused share is named rather than folded in silently.
func TestCostRollupCarriesRefusedSampleSpend(t *testing.T) {
	rep := refusedSampleReport(t)
	checkRefusedSampleCost(t, &rep.Cost)
}

// refusedSampleReport runs two samples and has record refuse the second.
func refusedSampleReport(t *testing.T) *Report {
	t.Helper()
	needPOSIX(t)
	restoreWD(t)
	cfg := benchConfig(t, fakeCorpus(t), agentWritesSpend)
	cfg.Runs = 2
	fake := &fakeGrader{
		model:   cfg.Model,
		panel:   cfg.Panel,
		prefix:  fakePrefix,
		refusal: jq.NewErr("record: bad-review-input: coverage[0] wants a path"),
		refuse:  "run2",
	}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err == nil || rep == nil {
		t.Fatalf("runGraded = %v, %v; want a report plus a refused-sample error", rep, err)
	}
	return rep
}

// checkRefusedSampleCost says what the rollup must report for two 0.03 USD
// samples, one refused.
func checkRefusedSampleCost(t *testing.T, cost *CostRollup) {
	t.Helper()
	if cost.Samples != 2 || cost.SampleEstimates != 2 || !cost.Measured {
		t.Errorf("cost = %+v, want two priced samples, measured", cost)
	}
	if got, want := cost.Usd, 0.06; got != want {
		t.Errorf("measured_usd = %v, want %v (both samples spent 0.03)", got, want)
	}
	if got, want := cost.RefusedUsd, 0.03; got != want {
		t.Errorf("refused_usd = %v, want %v", got, want)
	}
	if cost.RefusedSamples != 1 || cost.Bundles != 1 {
		t.Errorf("cost = %+v, want 1 refused sample and 1 bundle", cost)
	}
}

// TestMaxUSDStopsSchedulingAtTheCap pins the budget stop: three samples at
// 0.03 USD each against a 0.05 USD cap run two, and the third is reported as
// not run rather than as a failure. The run is never citable.
func TestMaxUSDStopsSchedulingAtTheCap(t *testing.T) {
	rep := cappedReport(t)
	if rep.Samples != 2 || len(rep.Skipped) != 1 {
		t.Fatalf("samples=%d skipped=%v, want two run and one skipped", rep.Samples, rep.Skipped)
	}
	if got := rep.Skipped[0]; got.Case != "off-by-one" || got.Run != 3 {
		t.Errorf("skipped = %+v, want off-by-one run 3", got)
	}
	checkCapRecorded(t, rep)
	if rep.Citable || !strings.Contains(rep.CitableReason, "budget cap hit") {
		t.Errorf("citable=%v reason=%q, want a budget refusal", rep.Citable, rep.CitableReason)
	}
	if got := countRunDirs(t); got != 2 {
		t.Errorf("%d sample dirs, want 2 (the cap stops scheduling)", got)
	}
}

// cappedReport runs three priced samples against a 0.05 USD cap.
func cappedReport(t *testing.T) *Report {
	t.Helper()
	needPOSIX(t)
	restoreWD(t)
	cfg := benchConfig(t, fakeCorpus(t), agentWritesSpend)
	cfg.Runs = 3
	cfg.MaxUSD = 0.05
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err != nil {
		t.Fatalf("runGraded: %v (a cap stop is not a failure)", err)
	}
	return rep
}

// checkCapRecorded pins the cap the report publishes, not just the stop.
func checkCapRecorded(t *testing.T, rep *Report) {
	t.Helper()
	if !rep.CapHit || rep.BudgetUSD == nil || *rep.BudgetUSD != 0.05 {
		t.Errorf("cap_hit=%v budget=%v, want the 0.05 cap recorded", rep.CapHit, rep.BudgetUSD)
	}
	if got, want := rep.SpentAtCap, 0.06; got != want {
		t.Errorf("spent_at_cap = %v, want %v", got, want)
	}
}

// TestMaxUSDRecordsEachSkippedSampleOnce pins the skip list across cases: the
// cap is hit while scheduling the third case, and the two cases it never
// reached appear once each — not once from skipFrom and once from schedule.
func TestMaxUSDRecordsEachSkippedSampleOnce(t *testing.T) {
	rep := cappedMultiCaseReport(t)
	want := []SkippedSample{{Case: "case-c", Run: 1}, {Case: "case-d", Run: 1}}
	if !slices.Equal(rep.Skipped, want) {
		t.Fatalf("skipped = %v, want %v (each unscheduled sample once)", rep.Skipped, want)
	}
	if got, want := skipLine(t, rep), "budget: cap 0.050000 USD hit after 0.060000 USD; 2 sample(s) not run: case-c run 1, case-d run 1"; got != want {
		t.Errorf("human budget line = %q, want %q", got, want)
	}
	if rep.Samples != 2 || rep.Cost.Usd != 0.06 {
		t.Errorf("samples=%d cost=%v, want two samples at 0.06", rep.Samples, rep.Cost.Usd)
	}
}

// cappedMultiCaseReport runs four priced cases at one sample each against a
// 0.05 USD cap, so the cap is reached while scheduling the third.
func cappedMultiCaseReport(t *testing.T) *Report {
	t.Helper()
	needPOSIX(t)
	restoreWD(t)
	cfg := benchConfig(t, fakeCorpusOf(t, "case-a", "case-b", "case-c", "case-d"), agentWritesSpend)
	cfg.Runs = 1
	cfg.MaxUSD = 0.05
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err != nil {
		t.Fatalf("runGraded: %v (a cap stop is not a failure)", err)
	}
	return rep
}

// skipLine renders the report's budget line through the real renderer, so the
// human count is pinned against the same string the operator reads.
func skipLine(t *testing.T, rep *Report) string {
	t.Helper()
	var buf bytes.Buffer
	r := &runner{cfg: &Config{}}
	r.render(&buf, rep)
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.HasPrefix(line, "budget: ") {
			return line
		}
	}
	t.Fatalf("no budget line in %q", buf.String())
	return ""
}

// countRunDirs counts the sample dirs a run created; Run moved the process
// into the run dir, so the path is relative.
func countRunDirs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("runs")
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	return len(entries)
}

// TestRunRefusesAMissingModel refuses a run no bundle could be attributed to.
func TestRunRefusesAMissingModel(t *testing.T) {
	cfg := &Config{K: 1, Agent: "true", Runs: 1}
	err := runRefusal(cfg)
	if err == nil || !strings.Contains(err.Error(), "-model is required") {
		t.Fatalf("Run error = %v, want a model refusal", err)
	}
}

func (f *fakeGrader) version(context.Context) (string, error) { return "fixture-tool", nil }
