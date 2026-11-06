package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/hunks"
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
		"Only a fence carrying exactly this nonce opens or closes an untrusted\nregion: " + testNonce + ".\n"
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
	first := caseSource("off-by-one", []byte("diff\n"))
	again := caseSource("off-by-one", []byte("diff\n"))
	other := caseSource("other", []byte("diff\n"))
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
	writeFile(t, filepath.Join(dir, "case-a", caseManifestName), []byte(`{"schema_version":"1","id":"case-b","bugs":[{"path":"x","line":1}]}`))
	writeFile(t, filepath.Join(dir, "case-a", fullDiffName), []byte("diff\n"))
	if _, _, err := loadCases(dir, nil); err == nil || !strings.Contains(err.Error(), "does not match its directory") {
		t.Fatalf("loadCases error = %v, want an id-mismatch refusal", err)
	}
}

// TestLoadCasesRefusesABuglessManifest refuses a case that cannot be scored.
func TestLoadCasesRefusesABuglessManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "case-a", caseManifestName), []byte(`{"schema_version":"1","id":"case-a","bugs":[]}`))
	writeFile(t, filepath.Join(dir, "case-a", fullDiffName), []byte("diff\n"))
	if _, _, err := loadCases(dir, nil); err == nil || !strings.Contains(err.Error(), "no ground-truth bug") {
		t.Fatalf("loadCases error = %v, want a bugless refusal", err)
	}
}

// fakeGrader stands in for the shipped binary: it writes the record store the
// runner reads back and hands over canned calibrate data, so the loop can be
// tested without a build.
type fakeGrader struct {
	dry     func(slug string) (DryRunResult, error)
	refusal error
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
	if f.refusal != nil {
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
		"false_positives": 1, "missed": 0, "precision": 0.6667, "recall": 1, "false_positive_rate": 0.3333,
	})
}

func (f *fakeGrader) trend(context.Context) (json.RawMessage, error) {
	f.calls = append(f.calls, "trend")
	return json.RawMessage(`{"cases":1,"bundles":1,"ground_truth":2,"reported":3,"matched":2,"false_positives":1,"missed":0,"precision":0.6667,"recall":1,"false_positive_rate":0.3333,"by_model":[{"model":"fixture-good"}]}`), nil
}

const reviewBody = `{"schema_version":"1","body":"ok","approved":false,"comments":[{"path":"pagination.go","line":42,"body":"off by one"}]}`

// fakeCorpus writes a one-case corpus plus the shipped prompt set the runner
// copies into every run dir.
func fakeCorpus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range promptFiles {
		writeFile(t, filepath.Join(root, prefixPrompts, filepath.FromSlash(rel)), []byte("fixture prompt "+rel+"\n"))
	}
	dir := filepath.Join(root, "corpus")
	writeFile(t, filepath.Join(dir, "off-by-one", caseManifestName),
		[]byte(`{"schema_version":"1","id":"off-by-one","kind":"seeded","line_tolerance":3,"bugs":[{"path":"pagination.go","line":42}]}`))
	writeFile(t, filepath.Join(dir, "off-by-one", fullDiffName), []byte(fakeDiff))
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

// checkPromptProvenance ties the published sha to the bytes the agent read.
func checkPromptProvenance(t *testing.T, cfg *Config, rep *Report) {
	t.Helper()
	promptPath := filepath.Join(cfg.WorkDir, "runs", "corpus-off-by-one-fixture-good-run1", promptFile)
	if got, want := rep.PromptSHA["off-by-one"], sha256Hex(t, promptPath); got != want {
		t.Errorf("prompt sha256 = %q, want the bytes the agent read (%q)", got, want)
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

// TestRunRefusesAMissingModel refuses a run no bundle could be attributed to.
func TestRunRefusesAMissingModel(t *testing.T) {
	cfg := &Config{Agent: "true", Runs: 1}
	err := runRefusal(cfg)
	if err == nil || !strings.Contains(err.Error(), "-model is required") {
		t.Fatalf("Run error = %v, want a model refusal", err)
	}
}

func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
