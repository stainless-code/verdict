package corpuscore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func TestSamplePinsWeightsAndRecordedZero(t *testing.T) {
	dir := t.TempDir()
	raw := `{"run_id":"run","k":3,"repeats":3,"sample":1,"severity_weights":{"critical":40,"important":7,"informational":2},"latency_s":0}`
	if err := os.WriteFile(filepath.Join(dir, "corpus_run.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), []byte(`{"est_usd":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, ref := LoadSample(dir, evaluateAll(&core.CorpusCase{ID: "clean", Kind: "control"}, nil))
	if ref != nil {
		t.Fatal(ref)
	}
	if s.Sampling.Weights.Critical != 40 || s.Sampling.K != 3 || s.CostUSD == nil || *s.CostUSD != 0 || s.LatencyS == nil || *s.LatencyS != 0 {
		t.Fatalf("sample = %+v; want pinned critical=40 k=3 measured zero cost/latency", s)
	}
}

// TestSamplingRefusesBundleWithoutRecord pins the removed k=1 fallback: a
// corpus bundle without corpus_run.json was written by an older bench runner,
// so it refuses bundle-outdated naming the re-run instead of scoring as an
// independent single sample with weights read from the repository config.
func TestSamplingRefusesBundleWithoutRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "corpus-old")
	_, ref := LoadSample(dir, evaluateAll(&core.CorpusCase{ID: "clean", Kind: "control"}, nil))
	want := "corpus bundle " + filepath.ToSlash(dir) + " has no corpus_run.json: an older bench runner wrote it " +
		"(re-run the benchmark with the current bench/runner to rebuild it)"
	if ref == nil || ref.Code != "bundle-outdated" || ref.Message != want {
		t.Fatalf("LoadSample without corpus_run.json = %v, want bundle-outdated %q", ref, want)
	}
}

func TestSamplingRefusesInvalidEvidence(t *testing.T) {
	for _, raw := range []string{
		`{"run_id":"r","k":0,"repeats":3,"sample":1,"severity_weights":{"critical":25,"important":5,"informational":1}}`,
		`{"run_id":"r","k":3,"repeats":2,"sample":1,"severity_weights":{"critical":25,"important":5,"informational":1}}`,
		`{"run_id":"r","k":1,"repeats":1,"sample":2,"severity_weights":{"critical":25,"important":5,"informational":1}}`,
		`{"run_id":"r","k":1,"repeats":1,"sample":1,"severity_weights":{"critical":0,"important":5,"informational":1}}`,
		`{"run_id":"r","k":1,"repeats":1,"sample":1,"severity_weights":{"critical":25,"important":5,"informational":1},"latency_s":-1}`,
		`{"unknown":true}`, `{} {}`, `null`,
	} {
		t.Run(raw, func(t *testing.T) { checkInvalidSampling(t, raw) })
	}
}

func checkInvalidSampling(t *testing.T, raw string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "corpus_run.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ref := loadSampling(dir); ref == nil {
		t.Fatalf("accepted invalid sampling %s", raw)
	}
}
