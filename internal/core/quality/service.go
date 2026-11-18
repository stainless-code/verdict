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
	fixturesDir = "fixtures"
	reportName  = "report.json"
	nextText    = "inspect .verdict/backtest/fixtures/report.json; rates describe recorded reviews and fixture defects"
)

// Request is one `backtest fixtures` run. RepoRoot is the checkout that owns
// bench/quality; RecordingsRoot is an opt-in override for live recordings,
// resolved against RepoRoot when relative.
type Request struct {
	Verifier       QuoteVerifier
	Now            func() time.Time
	RepoRoot       string
	FixtureID      string
	RecordingsRoot string
}

// Response is the envelope data plus the frozen human block.
type Response struct {
	Data     *Report
	At       string
	Next     string
	Human    string
	Warnings []string
}

// fixtureInputs separates the runs being scored from the shipped calibration
// set. An override changes the former, never the latter.
type fixtureInputs struct {
	catalog     *Catalog
	root        string
	heads       map[string][]byte
	scoring     []recordingFile
	calibration []recordingFile
}

type recordingFile struct {
	path    string
	fixture string
}

// RunFixtures loads the catalog and recordings, scores them, and writes the
// report under .verdict/backtest/fixtures/report.json. It never launches an
// agent or calls a host.
func RunFixtures(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, canceled(err)
	}
	inputs, ref := loadInputs(req)
	if ref != nil {
		return nil, ref
	}
	byID := map[string]*Fixture{}
	for i := range inputs.catalog.Fixtures {
		byID[inputs.catalog.Fixtures[i].ID] = &inputs.catalog.Fixtures[i]
	}
	matcher := NewMatcher(inputs.catalog)
	runs, findings, ref := loadRuns(ctx, inputs.scoring, byID, matcher)
	if ref != nil {
		return nil, ref
	}
	calibration, _, ref := loadRuns(ctx, inputs.calibration, byID, matcher)
	if ref != nil {
		return nil, ref
	}
	report, ref := buildReport(ctx, req, inputs.catalog, inputs.heads, runs, calibration, findings)
	if ref != nil {
		return nil, ref
	}
	if ref := writeReport(inputs.root, report); ref != nil {
		return nil, ref
	}
	return response(req, report), nil
}

func loadInputs(req *Request) (*fixtureInputs, *core.RefusalError) {
	root := req.RepoRoot
	if root == "" {
		root = "."
	}
	catalog, ref := LoadCatalog(filepath.Join(root, "bench", "quality", catalogName))
	if ref != nil {
		return nil, ref
	}
	if ref := validateFixtureID(catalog, req.FixtureID); ref != nil {
		return nil, ref
	}
	scoring, ref := loadScoringFiles(root, req.RecordingsRoot, catalog)
	if ref != nil {
		return nil, ref
	}
	calibration, ref := loadCalibrationFiles(root, catalog)
	if ref != nil {
		return nil, ref
	}
	heads, ref := loadFixtureHeads(root, catalog)
	if ref != nil {
		return nil, ref
	}
	return &fixtureInputs{
		catalog: catalog, root: root, heads: heads, scoring: scoring,
		calibration: calibration,
	}, nil
}

func loadScoringFiles(root, override string, catalog *Catalog) ([]recordingFile, *core.RefusalError) {
	base := recordingsRoot(root, override)
	files, ref := fixtureFiles(base, catalog)
	if ref != nil {
		return nil, ref
	}
	if len(files) == 0 {
		return nil, invalidRecording(base, "no fixture recordings under the recordings root")
	}
	if ref := validateUniqueNames(files); ref != nil {
		return nil, ref
	}
	return files, nil
}

func loadCalibrationFiles(root string, catalog *Catalog) ([]recordingFile, *core.RefusalError) {
	shipped := filepath.Join(root, "bench", "quality")
	files, ref := fixtureFiles(shipped, catalog)
	if ref != nil {
		return nil, ref
	}
	if len(files) == 0 {
		return nil, invalidRecording(shipped, "no shipped calibration recordings")
	}
	if ref := validateUniqueNames(files); ref != nil {
		return nil, ref
	}
	return files, nil
}

func validateFixtureID(catalog *Catalog, id string) *core.RefusalError {
	if id == "" {
		return nil
	}
	ids := make([]string, 0, len(catalog.Fixtures))
	for i := range catalog.Fixtures {
		if catalog.Fixtures[i].ID == id {
			return nil
		}
		ids = append(ids, catalog.Fixtures[i].ID)
	}
	slices.Sort(ids)
	return &core.RefusalError{
		Code:    "fixtures-invalid",
		Message: "unknown fixture '" + id + "' (valid ids: " + strings.Join(ids, ", ") + ")",
	}
}

func fixtureFiles(base string, catalog *Catalog) ([]recordingFile, *core.RefusalError) {
	var files []recordingFile
	for i := range catalog.Fixtures {
		dir := filepath.Join(base, catalog.Fixtures[i].ID, "recordings")
		found, ref := RecordingFiles(dir)
		if ref != nil {
			return nil, ref
		}
		for _, path := range found {
			files = append(files, recordingFile{path: path, fixture: catalog.Fixtures[i].ID})
		}
	}
	return files, nil
}

func validateUniqueNames(files []recordingFile) *core.RefusalError {
	seen := map[string]bool{}
	for _, file := range files {
		name := recordingName(file.path)
		if seen[name] {
			return invalidRecording(file.path, "repeated recording name "+name)
		}
		seen[name] = true
	}
	return nil
}

func recordingName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), recordingExt)
}

func buildReport(ctx context.Context, req *Request, catalog *Catalog, heads map[string][]byte,
	runs []RunScore, calibration []RunScore, findings map[string][]Finding,
) (*Report, *core.RefusalError) {
	report := &Report{
		SchemaVersion: "1",
		Mode:          "fixtures",
		Recordings:    len(runs),
		Scored:        scoredRuns(runs),
		Calibration:   calibrationSummary(Calibrate(catalog, calibration)),
		Diagnostics:   diagnostics(runs),
	}
	for i := range catalog.Fixtures {
		f := &catalog.Fixtures[i]
		if req.FixtureID != "" && f.ID != req.FixtureID {
			continue
		}
		fixture, ref := fixtureReport(ctx, req, f, heads[f.ID], runs, findings)
		if ref != nil {
			return nil, ref
		}
		report.Fixtures = append(report.Fixtures, fixture)
	}
	return report, nil
}

func response(req *Request, report *Report) *Response {
	now := time.Now
	if req.Now != nil {
		now = req.Now
	}
	return &Response{Data: report, At: core.NowUTC(now()), Next: nextText, Human: Human(report)}
}

func canceled(err error) *core.RefusalError {
	return &core.RefusalError{Code: "canceled", Message: "backtest fixtures canceled: " + err.Error()}
}

func recordingsRoot(root, override string) string {
	if override == "" {
		return filepath.Join(root, "bench", "quality")
	}
	if filepath.IsAbs(override) {
		return override
	}
	return filepath.Join(root, override)
}

func loadRuns(ctx context.Context, files []recordingFile, byID map[string]*Fixture,
	matcher *Matcher,
) ([]RunScore, map[string][]Finding, *core.RefusalError) {
	runs := make([]RunScore, 0, len(files))
	findingsByRecording := map[string][]Finding{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, canceled(err)
		}
		run, findings, ref := scoreOne(file, byID, matcher)
		if ref != nil {
			return nil, nil, ref
		}
		findingsByRecording[run.Recording] = findings
		runs = append(runs, *run)
	}
	return runs, findingsByRecording, nil
}

func scoreOne(file recordingFile, byID map[string]*Fixture,
	matcher *Matcher,
) (*RunScore, []Finding, *core.RefusalError) {
	path := file.path
	name := recordingName(path)
	meta, ref := LoadRecordingMeta(MetaPath(path))
	if ref != nil {
		return nil, nil, ref
	}
	recording, ref := LoadFixtureRecording(path)
	if ref != nil {
		return nil, nil, ref
	}
	if meta.Fixture != file.fixture {
		return nil, nil, invalidRecording(path, "sidecar names fixture "+meta.Fixture+
			", expected "+file.fixture)
	}
	fixture := byID[meta.Fixture]
	if fixture == nil {
		return nil, nil, invalidRecording(path, "names unknown fixture "+meta.Fixture)
	}
	findings := make([]Finding, 0, len(recording.Findings))
	for i := range recording.Findings {
		findings = append(findings, recording.Findings[i].Canonical())
	}
	return Score(matcher, fixture, findings, meta, recording.GatePassed, name), findings, nil
}

func calibrationSummary(calibration *CalibrationResult) CalibrationReport {
	out := CalibrationReport{Rows: len(calibration.Rows), Reproduced: calibration.Reproduced, Mismatches: []string{}}
	for i := range calibration.Rows {
		if calibration.Rows[i].Status != statusPass {
			out.Mismatches = append(out.Mismatches, calibration.Rows[i].Recording)
		}
	}
	return out
}

func scoredRuns(runs []RunScore) int {
	n := 0
	for i := range runs {
		if runs[i].Scored {
			n++
		}
	}
	return n
}

func diagnostics(runs []RunScore) []Diagnostic {
	out := make([]Diagnostic, 0, len(runs))
	for i := range runs {
		run := &runs[i]
		hits := sortedHits(run.Hits)
		out = append(out, Diagnostic{
			Recording: run.Recording, Fixture: run.Fixture, Hits: hits,
			Praise: run.Praise, NullSeverity: run.NullSeverity, OtherFindings: run.OtherFindings,
			GatePassed: run.GatePassed, InfraError: run.InfraError, Scored: run.Scored,
		})
	}
	return out
}

func fixtureReport(ctx context.Context, req *Request, f *Fixture, head []byte, runs []RunScore,
	findings map[string][]Finding,
) (FixtureReport, *core.RefusalError) {
	out := FixtureReport{
		ID: f.ID, Label: f.Label, Known: len(f.Known), Bonus: len(f.Bonus),
		Recall:     RecallReport{PerRun: []float64{}, UnionHits: []string{}},
		BonusUnion: BonusReport{Hits: []string{}}, FalseGreen: FalseGreenReport{},
		Hallucination: HallucinationReport{Status: hallucStubbed},
	}
	union := map[string]bool{}
	bonus := map[string]bool{}
	for i := range runs {
		run := &runs[i]
		if run.Fixture != f.ID {
			continue
		}
		out.Runs++
		if run.RefusalCode == refusalInsufficient {
			out.ReviewInsufficient++
		}
		if !run.Scored {
			continue
		}
		out.Scored++
		tallyRun(&out, run, len(f.Known), union, bonus)
	}
	out.Recall.Mean = meanRate(out.Recall.PerRun, out.Scored)
	out.Recall.Union = ratePtr(countTrue(union), len(f.Known))
	out.Recall.UnionHits = sortedHits(union)
	out.BonusUnion = BonusReport{Count: countTrue(bonus), Hits: sortedHits(bonus)}
	out.FalseGreen.Rate = ratePtr(out.FalseGreen.Passes, out.FalseGreen.Measured)
	return completeFixtureReport(ctx, &out, req, f, head, runs, findings)
}

func completeFixtureReport(ctx context.Context, out *FixtureReport, req *Request, f *Fixture, head []byte,
	runs []RunScore, findings map[string][]Finding,
) (FixtureReport, *core.RefusalError) {
	hallucination, ref := measureHallucination(ctx, req.Verifier, f.ID, head, runs, findings)
	if ref != nil {
		return FixtureReport{}, ref
	}
	out.Hallucination = hallucination
	return *out, nil
}

func tallyRun(out *FixtureReport, run *RunScore, known int, union, bonus map[string]bool) {
	if run.GatePassed == nil {
		out.FalseGreen.Unknown++
	} else {
		out.FalseGreen.Measured++
		if *run.GatePassed {
			out.FalseGreen.Passes++
		}
	}
	if rate, ok := corpuscore.Rate(countTrue(run.Hits), known); ok {
		out.Recall.PerRun = append(out.Recall.PerRun, rate)
	}
	mergeHits(union, run.Hits)
	mergeHits(bonus, run.BonusHits)
}

func meanRate(perRun []float64, scored int) *float64 {
	if scored == 0 {
		return nil
	}
	sum := 0.0
	for _, rate := range perRun {
		sum += rate
	}
	mean := roundRate(sum / float64(scored))
	return &mean
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

func measureHallucination(ctx context.Context, verifier QuoteVerifier, fixtureID string, head []byte,
	runs []RunScore, findings map[string][]Finding,
) (HallucinationReport, *core.RefusalError) {
	out := HallucinationReport{Status: hallucStubbed}
	if verifier == nil {
		verifier = StubVerifier{}
	}
	var mismatches int
	var ref *core.RefusalError
	for i := range runs {
		run := &runs[i]
		if run.Fixture != fixtureID || !run.Scored {
			continue
		}
		mismatches, ref = addRunMismatches(ctx, mismatches, verifier, &out, head, findings[run.Recording])
		if ref != nil {
			return HallucinationReport{}, ref
		}
	}
	return finishHallucination(out, mismatches), nil
}

func addRunMismatches(ctx context.Context, mismatches int, verifier QuoteVerifier, out *HallucinationReport,
	head []byte, findings []Finding,
) (int, *core.RefusalError) {
	runMismatches, ref := verifyRun(ctx, verifier, out, head, findings)
	if ref != nil {
		return 0, ref
	}
	return mismatches + runMismatches, nil
}

func finishHallucination(out HallucinationReport, mismatches int) HallucinationReport {
	if out.Checked > 0 {
		out.Rate = ratePtr(mismatches, out.Checked)
	}
	return out
}

func verifyRun(ctx context.Context, verifier QuoteVerifier, out *HallucinationReport, head []byte,
	findings []Finding,
) (int, *core.RefusalError) {
	mismatches := 0
	for j := range findings {
		f := &findings[j]
		if !IsGatingFinding(f) {
			continue
		}
		out.GatingFindings++
		if err := ctx.Err(); err != nil {
			return mismatches, canceled(err)
		}
		mismatches += verifyOne(ctx, verifier, out, head, f)
		if err := ctx.Err(); err != nil {
			return mismatches, canceled(err)
		}
	}
	return mismatches, nil
}

func verifyOne(ctx context.Context, verifier QuoteVerifier, out *HallucinationReport, head []byte,
	f *Finding,
) int {
	verdict := verifier.Verify(ctx, f, head)
	if verdict.Status != quoteMatched && verdict.Status != quoteMismatch {
		out.Unverifiable++
		return 0
	}
	out.Checked++
	out.Status = hallucMeasured
	if verdict.Status == quoteMismatch {
		return 1
	}
	return 0
}

func writeReport(root string, report *Report) *core.RefusalError {
	path := filepath.Join(root, ".verdict", "backtest", fixturesDir, reportName)
	if err := os.MkdirAll(filepath.Dir(path), dirPermissions); err != nil {
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
