package corpuscore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// LoadSample loads evidence; legacy bundles are independent k=1 runs.
func LoadSample(dir string, report *Report) (Sample, *core.RefusalError) {
	s := Sample{
		Report: report, CaseID: report.Case.ID, Bugs: len(report.Case.Bugs),
		Group: ModelOf(dir) + "\x00" + PanelLabel(PanelOf(dir)),
	}
	meta, ref := loadSampling(dir)
	if ref != nil {
		return s, ref
	}
	s.Sampling = meta
	s.LatencyS = meta.LatencyS
	for _, o := range report.Outcomes {
		if o.HasBugHit {
			s.Hits = append(s.Hits, o.BugIndex)
		}
	}
	s.CostUSD = recordedCost(dir)
	return s, nil
}

func recordedCost(dir string) *float64 {
	raw, err := jq.CappedFile(filepath.Join(dir, "cost.json"), maxManifestBytes)
	if err == nil {
		var cost struct {
			EstUSD     *float64 `json:"est_usd"`
			Incomplete bool     `json:"incomplete"`
		}
		if json.Unmarshal(raw, &cost) == nil && !cost.Incomplete && validRecorded(cost.EstUSD) {
			return cost.EstUSD
		}
	}
	return nil
}

func loadSampling(dir string) (Sampling, *core.RefusalError) {
	path := filepath.Join(dir, "corpus_run.json")
	raw, err := jq.CappedFile(path, maxManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return legacySampling(dir)
	}
	if err != nil {
		return Sampling{}, invalid(path, "unreadable sampling")
	}
	var m Sampling
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, invalid(path, "invalid sampling JSON")
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return m, invalid(path, "trailing sampling data")
	}
	return m, validateSampling(m, path)
}

func validateSampling(m Sampling, path string) *core.RefusalError {
	if strings.TrimSpace(m.RunID) == "" || m.K < 1 || m.Repeats < m.K || m.Sample < 1 || m.Sample > m.Repeats {
		return invalid(path, "sampling requires run_id and 1 <= k <= repeats, 1 <= sample <= repeats")
	}
	return validateMeasurementEvidence(m, path)
}

func validateMeasurementEvidence(m Sampling, path string) *core.RefusalError {
	if !validWeights(m.Weights) {
		return invalid(path, "sampling severity weights must be in 1..1000000")
	}
	if m.LatencyS != nil && !validRecorded(m.LatencyS) {
		return invalid(path, "latency_s must be finite and nonnegative")
	}
	return nil
}

func legacySampling(dir string) (Sampling, *core.RefusalError) {
	m := Sampling{RunID: filepath.Base(dir), K: 1, Repeats: 1, Sample: 1, Weights: config.SeverityWeights(nil)}
	path := filepath.Join(filepath.Dir(filepath.Dir(dir)), "config.json")
	raw, err := jq.CappedFile(path, config.MaxConfigBytes)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, invalid(path, "unreadable benchmark policy")
	}
	cfg, ref := config.Parse(raw)
	if ref != nil {
		return m, ref
	}
	m.Weights = config.SeverityWeights(cfg)
	return m, nil
}

const maxWeight = 1000000

func validWeights(w core.SeverityWeights) bool {
	return w.Critical >= 1 && w.Critical <= maxWeight &&
		w.Important >= 1 &&
		w.Important <= maxWeight &&
		w.Informational >= 1 &&
		w.Informational <= maxWeight
}

func validRecorded(v *float64) bool {
	return v != nil && *v >= 0 && !math.IsNaN(*v) && !math.IsInf(*v, 0)
}

// Cohort reads the selected case, run, model and panel for any-of-k recall.
func Cohort(dir string, sample *Sample) []Sample {
	out := []Sample{*sample}
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == filepath.Base(dir) || !strings.HasPrefix(e.Name(), "corpus-") {
			continue
		}
		sibling := filepath.Join(filepath.Dir(dir), e.Name())
		if s, ok := cohortSibling(sibling, sample); ok {
			out = append(out, s)
		}
	}
	return out
}

func cohortSibling(dir string, selected *Sample) (Sample, bool) {
	c, ref := Load(dir)
	if ref != nil {
		return Sample{}, false
	}
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		return Sample{}, false
	}
	s, ref := LoadSample(dir, Evaluate(c, entries))
	if ref != nil {
		return Sample{}, false
	}
	return s, s.Sampling.RunID == selected.Sampling.RunID && s.CaseID == selected.CaseID && s.Group == selected.Group
}
