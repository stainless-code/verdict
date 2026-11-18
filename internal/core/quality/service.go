package quality

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	fixturesDir   = "fixtures"
	reportName    = "report.json"
	nextText      = "inspect .verdict/backtest/fixtures/report.json; rates describe recorded reviews and fixture defects"
	reportTimeFmt = "2006-01-02T15:04:05Z"
)

// Request is one `backtest fixtures` run. RepoRoot is the checkout that owns
// bench/quality; RecordingsRoot is an opt-in override for live recordings,
// resolved against RepoRoot when relative.
type Request struct {
	RepoRoot       string
	FixtureID      string
	RecordingsRoot string
	Verifier       QuoteVerifier
	Now            func() time.Time
}

// Response is the envelope data plus the frozen human block.
type Response struct {
	Data     *Report
	At       string
	Next     string
	Warnings []string
	Human    string
}

// RunFixtures loads the catalog and recordings, scores them, and writes the
// report under .verdict/backtest/fixtures/report.json. It never launches an
// agent or calls a host.
func RunFixtures(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: "canceled", Message: "backtest fixtures canceled: " + err.Error()}
	}
	root := req.RepoRoot
	if root == "" {
		root = "."
	}
	catalog, ref := LoadCatalog(filepath.Join(root, "bench", "quality", catalogName))
	if ref != nil {
		return nil, ref
	}
	fixturesDirPath := filepath.Join(root, "bench", "quality", "recordings")
	if req.RecordingsRoot != "" {
		fixturesDirPath = req.RecordingsRoot
		if !filepath.IsAbs(fixturesDirPath) {
			fixturesDirPath = filepath.Join(root, fixturesDirPath)
		}
	}
	dirs, ref := RecordingDirs(fixturesDirPath)
	if ref != nil {
		return nil, ref
	}
	if len(dirs) == 0 {
		return nil, &core.RefusalError{Code: "fixtures-recording-invalid", Message: "no recordings under " + fixturesDirPath}
	}
	matcher := NewMatcher(catalog)
	byID := map[string]*Fixture{}
	for i := range catalog.Fixtures {
		byID[catalog.Fixtures[i].ID] = &catalog.Fixtures[i]
	}
	runs := make([]RunScore, 0, len(dirs))
	findingsByRecording := map[string][]Finding{}
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, &core.RefusalError{Code: "canceled", Message: "backtest fixtures canceled: " + err.Error()}
		}
		name := filepath.Base(dir)
		meta, ref := LoadRecordingMeta(dir)
		if ref != nil {
			return nil, ref
		}
		fixture := byID[meta.Fixture]
		if fixture == nil {
			return nil, &core.RefusalError{Code: "fixtures-recording-invalid", Message: "recording " + name + " names unknown fixture " + meta.Fixture}
		}
		findings, ref := loadFindings(dir, meta.Source)
		if ref != nil {
			return nil, ref
		}
		findingsByRecording[name] = findings
		runs = append(runs, *Score(matcher, fixture, findings, *meta, name))
	}
	calibration := Calibrate(catalog, runs)
	calibrationReport := CalibrationReport{Rows: len(calibration.Rows), Reproduced: calibration.Reproduced, Mismatches: []string{}}
	for _, row := range calibration.Rows {
		if row.Status != "PASS" {
			calibrationReport.Mismatches = append(calibrationReport.Mismatches, row.Recording)
		}
	}
	report := &Report{
		SchemaVersion: "1",
		Mode:          "fixtures",
		Recordings:    len(runs),
		Scored:        scoredRuns(runs),
		Calibration:   calibrationReport,
		Diagnostics:   diagnostics(runs),
	}
	for i := range catalog.Fixtures {
		f := &catalog.Fixtures[i]
		if req.FixtureID != "" && f.ID != req.FixtureID {
			continue
		}
		report.Fixtures = append(report.Fixtures, fixtureReport(ctx, req, f, runs, findingsByRecording))
	}
	if ref := writeReport(root, report); ref != nil {
		return nil, ref
	}
	now := time.Now
	if req.Now != nil {
		now = req.Now
	}
	return &Response{Data: report, At: core.NowUTC(now()), Next: nextText, Human: Human(report)}, nil
}

func loadFindings(dir, source string) ([]Finding, *core.RefusalError) {
	switch source {
	case reviewName:
		return LoadArchivedReview(filepath.Join(dir, reviewName))
	case findingsName:
		return LoadRecordedFindings(dir)
	default:
		return nil, &core.RefusalError{Code: "fixtures-recording-invalid", Message: "unsupported source " + source}
	}
}

func scoredRuns(runs []RunScore) int {
	n := 0
	for _, run := range runs {
		if run.Scored {
			n++
		}
	}
	return n
}

func diagnostics(runs []RunScore) []Diagnostic {
	out := make([]Diagnostic, 0, len(runs))
	for _, run := range runs {
		hits := make([]string, 0, len(run.Hits))
		for id, hit := range run.Hits {
			if hit {
				hits = append(hits, id)
			}
		}
		slices.Sort(hits)
		out = append(out, Diagnostic{
			Recording: run.Recording, Fixture: run.Fixture, Hits: hits,
			Praise: run.Praise, NullSeverity: run.NullSeverity, OtherFindings: run.OtherFindings,
			GatePassed: run.GatePassed, InfraError: run.InfraError, Scored: run.Scored,
		})
	}
	return out
}

func fixtureReport(ctx context.Context, req *Request, f *Fixture, runs []RunScore, findings map[string][]Finding) FixtureReport {
	out := FixtureReport{ID: f.ID, Label: f.Label, Known: len(f.Known), Bonus: len(f.Bonus),
		Recall:     RecallReport{PerRun: []float64{}, UnionHits: []string{}},
		BonusUnion: BonusReport{Hits: []string{}}, FalseGreen: FalseGreenReport{},
		Hallucination: HallucinationReport{Status: hallucStubbed}}
	union := map[string]bool{}
	bonus := map[string]bool{}
	sum := 0.0
	for _, run := range runs {
		if run.Fixture != f.ID {
			continue
		}
		out.Runs++
		if run.RefusalCode == "review-insufficient" {
			out.ReviewInsufficient++
		}
		if !run.Scored {
			continue
		}
		if run.GatePassed == nil {
			out.FalseGreen.Unknown++
		} else {
			out.FalseGreen.Measured++
			if *run.GatePassed {
				out.FalseGreen.Passes++
			}
		}
		out.Scored++
		rate, ok := corpuscore.Rate(countTrue(run.Hits), len(f.Known))
		if ok {
			out.Recall.PerRun = append(out.Recall.PerRun, rate)
			sum += rate
		}
		mergeHits(union, run.Hits)
		mergeHits(bonus, run.BonusHits)
	}
	if out.Scored > 0 {
		mean := roundRate(sum / float64(out.Scored))
		out.Recall.Mean = &mean
	}
	out.Recall.Union = ratePtr(countTrue(union), len(f.Known))
	out.Recall.UnionHits = sortedHits(union)
	out.BonusUnion = BonusReport{Count: countTrue(bonus), Hits: sortedHits(bonus)}
	out.FalseGreen.Rate = ratePtr(out.FalseGreen.Passes, out.FalseGreen.Measured)
	out.Hallucination = measureHallucination(ctx, req.Verifier, f.ID, runs, findings)
	return out
}

func mergeHits(dst map[string]bool, src map[string]bool) {
	for id, hit := range src {
		if hit {
			dst[id] = true
		}
	}
}

func sortedHits(values map[string]bool) []string {
	ids := make([]string, 0, len(values))
	for id, hit := range values {
		if hit {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func measureHallucination(ctx context.Context, verifier QuoteVerifier, fixtureID string, runs []RunScore, findings map[string][]Finding) HallucinationReport {
	out := HallucinationReport{Status: hallucStubbed}
	if verifier == nil {
		verifier = StubVerifier{}
	}
	var mismatches int
	for _, run := range runs {
		if run.Fixture != fixtureID || !run.Scored {
			continue
		}
		for _, f := range findings[run.Recording] {
			if !IsGatingFinding(f) {
				continue
			}
			out.GatingFindings++
			if err := ctx.Err(); err != nil {
				return out
			}
			result := verifier.Verify(ctx, f, nil)
			if result.Status == quoteUnsupported {
				continue
			}
			out.Checked++
			out.Status = hallucMeasured
			if result.Status == quoteMismatch {
				mismatches++
			}
		}
	}
	if out.Checked > 0 {
		out.Rate = ratePtr(mismatches, out.Checked)
	}
	return out
}

func writeReport(root string, report *Report) *core.RefusalError {
	path := filepath.Join(root, ".verdict", "backtest", fixturesDir, reportName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return &core.RefusalError{Code: "fixtures-failed", Message: "cannot create fixtures report directory: " + err.Error()}
	}
	if err := atomic.WriteFile(path, []byte(jq.PrettyOrdered(report.Wire()))); err != nil {
		return &core.RefusalError{Code: "fixtures-failed", Message: "cannot write fixtures report: " + err.Error()}
	}
	return nil
}

// HumanReport renders the human block for an already-computed report.
func HumanReport(report *Report) string {
	return strings.TrimSuffix(Human(report), "\n")
}
