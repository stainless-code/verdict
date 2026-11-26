package corpuscore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// LoadSample loads one bundle's sampling evidence; a bundle without
// corpus_run.json was written by an older bench runner and refuses.
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
		return Sampling{}, &core.RefusalError{Code: config.CodeBundleOutdated, Message: core.Msgf(
			"corpus bundle %s has no corpus_run.json: an older bench runner wrote it "+
				"(re-run the benchmark with the current bench/runner to rebuild it)", filepath.ToSlash(dir))}
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
func Cohort(dir string, sample *Sample) ([]Sample, []string) {
	out := []Sample{*sample}
	var warnings []string
	selected := readableIdentity(dir)
	for _, sibling := range siblingDirs(dir) {
		s, ok, reason := cohortSibling(sibling, sample)
		if ok {
			out = append(out, s)
			continue
		}
		if reason != "" && readableIdentity(sibling).joins(selected) {
			warnings = append(warnings, fmt.Sprintf("corpus bundle %q skipped from the cohort (%s)", sibling, reason))
		}
	}
	return out, warnings
}

type cohortIdentity struct {
	caseID string
	runID  string
	model  string
	panel  string
}

func readableIdentity(dir string) cohortIdentity {
	return cohortIdentity{
		caseID: manifestID(dir),
		runID:  samplingRunID(dir),
		model:  provenance.CostModel(dir),
		panel:  PanelLabel(PanelOf(dir)),
	}
}

func manifestID(dir string) string {
	var manifest struct {
		ID string `json:"id"`
	}
	if !readIdentity(filepath.Join(dir, "corpus.json"), &manifest) {
		return ""
	}
	return strings.TrimSpace(manifest.ID)
}

func samplingRunID(dir string) string {
	var sampling struct {
		RunID string `json:"run_id"`
	}
	if !readIdentity(filepath.Join(dir, "corpus_run.json"), &sampling) {
		return ""
	}
	return strings.TrimSpace(sampling.RunID)
}

func readIdentity(path string, target any) bool {
	raw, err := jq.CappedFile(path, maxManifestBytes)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, target) == nil
}

func (id cohortIdentity) joins(selected cohortIdentity) bool {
	return samePart(id.caseID, selected.caseID) && samePart(id.runID, selected.runID) &&
		samePart(id.model, selected.model) && samePart(id.panel, selected.panel)
}

func samePart(sibling, selected string) bool {
	return unknownPart(sibling) || unknownPart(selected) || sibling == selected
}

func unknownPart(part string) bool {
	return part == "" || part == unrecorded || part == unpaneled
}

// siblingDirs lists the other corpus bundles beside dir. A listing failure is
// an empty cohort, not a skipped one.
func siblingDirs(dir string) []string {
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != filepath.Base(dir) && strings.HasPrefix(e.Name(), "corpus-") {
			out = append(out, filepath.Join(filepath.Dir(dir), e.Name()))
		}
	}
	return out
}

// cohortSibling grades one sibling. A non-empty reason is a skip worth
// surfacing; an empty one means it simply belongs to another cohort.
func cohortSibling(dir string, selected *Sample) (Sample, bool, string) {
	c, ref := Load(dir)
	if ref != nil {
		return Sample{}, false, ref.Message
	}
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		return Sample{}, false, err.Error()
	}
	report, ref := EvaluateDir(dir, c, entries)
	if ref != nil {
		return Sample{}, false, ref.Message
	}
	s, ref := LoadSample(dir, report)
	if ref != nil {
		return Sample{}, false, ref.Message
	}
	if s.Sampling.RunID != selected.Sampling.RunID || s.CaseID != selected.CaseID || s.Group != selected.Group {
		return Sample{}, false, ""
	}
	return s, true, ""
}
