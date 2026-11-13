package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/jq"
)

// Report is the publication JSON: the fields the runbook requires, plus the
// per-sample blocks they rest on.
type Report struct {
	LoopCost          *LoopCost              `json:"loop_cost,omitempty"`
	PromptSHA         map[string]string      `json:"prompt_sha256"`
	GeneratedAt       string                 `json:"generated_at"`
	CitableReason     string                 `json:"citable_reason,omitempty"`
	RunID             string                 `json:"run_id"`
	PromptsHash       string                 `json:"prompts_hash,omitempty"`
	Date              string                 `json:"date"`
	ToolVersion       string                 `json:"tool_version"`
	SchemaVersion     string                 `json:"schema_version"`
	FixRates          []FixRate              `json:"fix_rates,omitempty"`
	Commands          [][]string             `json:"commands"`
	Loops             []LoopSummary          `json:"loops,omitempty"`
	TrendCorpus       json.RawMessage        `json:"trend_corpus"`
	Bundles           []BundleRun            `json:"bundles"`
	Failures          []Failure              `json:"failures"`
	Panel             []string               `json:"panel"`
	Cases             []CaseSpread           `json:"cases"`
	AnyOfKRecall      corpuscore.Measurement `json:"any_of_k_recall"`
	Agent             AgentRef               `json:"agent"`
	Corpus            CorpusRef              `json:"corpus"`
	Cost              CostRollup             `json:"cost"`
	SeverityWeights   core.SeverityWeights   `json:"severity_weights"`
	K                 int                    `json:"k"`
	Repeats           int                    `json:"repeats"`
	IncompleteCohorts int                    `json:"incomplete_cohorts"`
	Samples           int                    `json:"samples"`
	ElapsedSeconds    float64                `json:"elapsed_seconds"`
	TolerancePoints   int                    `json:"tolerance_points"`
	Seeded            bool                   `json:"seeded"`
	Citable           bool                   `json:"citable"`
	Internal          bool                   `json:"internal"`
}

// CorpusRef pins what was measured: the checkout the cases came from.
type CorpusRef struct {
	Commit  string   `json:"commit,omitempty"`
	CaseIDs []string `json:"case_ids"`
	Cases   int      `json:"cases"`
	Dirty   bool     `json:"dirty"`
}

// AgentRef pins who measured it and how long one sample may take.
type AgentRef struct {
	Command        string `json:"command"`
	Model          string `json:"model"`
	Runs           int    `json:"runs"`
	Loop           int    `json:"loop,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// BundleRun is one scored sample: the calibrate data verbatim plus the wall
// clock it took.
type BundleRun struct {
	Slug        string          `json:"slug"`
	Case        string          `json:"case"`
	Warnings    []string        `json:"warnings,omitempty"`
	Calibrate   json.RawMessage `json:"calibrate"`
	Run         int             `json:"run"`
	WallSeconds float64         `json:"wall_seconds"`
}

// CaseSpread is one case over its samples: the runbook publishes median, min,
// and max so a reader can see the noise floor.
type CaseSpread struct {
	Metrics           map[string]Stat `json:"metrics"`
	Precision         Stat            `json:"precision"`
	Recall            Stat            `json:"recall"`
	FalsePositiveRate Stat            `json:"false_positive_rate"`
	Case              string          `json:"case"`
	Runs              int             `json:"runs"`
}

// Stat is one rate across samples; null when no sample had a denominator.
type Stat struct {
	Median *float64 `json:"median"`
	Min    *float64 `json:"min"`
	Max    *float64 `json:"max"`
	N      int      `json:"n"`
}

// CostRollup is the spend the bundles report, measured or absent.
type CostRollup struct {
	Bundles      int     `json:"bundles"`
	WithEstimate int     `json:"bundles_with_estimate"`
	Incomplete   int     `json:"bundles_incomplete,omitempty"`
	Usd          float64 `json:"measured_usd"`
	Measured     bool    `json:"measured"`
}

// Failure is one sample that produced no score. Its evidence stays in the run
// dir for the operator.
type Failure struct {
	Case     string `json:"case"`
	Error    string `json:"error"`
	Evidence string `json:"evidence,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Run      int    `json:"run"`
}

// calibration is the calibrate data block's numbers; the block itself is what
// the report publishes.
type calibration struct {
	Recall            *float64            `json:"recall"`
	FalsePositiveRate *float64            `json:"false_positive_rate"`
	Precision         *float64            `json:"precision"`
	Metrics           *corpuscore.Metrics `json:"metrics"`
	Model             string              `json:"model"`
	Case              string              `json:"case"`
	Panel             []string            `json:"panel"`
	Hits              []int               `json:"sampling_hits"`
	GroundTruth       int                 `json:"ground_truth"`
	Reported          int                 `json:"reported"`
	Matched           int                 `json:"matched"`
	FalsePositives    int                 `json:"false_positives"`
	Missed            int                 `json:"missed"`
}

// score is one sample's measurement, in memory only: the published form is the
// calibrate block plus the spread.
type score struct {
	slug     string
	caseID   string
	warnings []string
	data     json.RawMessage
	cal      calibration
	run      int
	wall     float64
}

// trendRates is the aggregate the tool computed over every corpus bundle.
type trendRates struct {
	Precision         *float64 `json:"precision"`
	Recall            *float64 `json:"recall"`
	FalsePositiveRate *float64 `json:"false_positive_rate"`
	Cases             int      `json:"cases"`
	Bundles           int      `json:"bundles"`
	GroundTruth       int      `json:"ground_truth"`
	Reported          int      `json:"reported"`
	Matched           int      `json:"matched"`
	FalsePositives    int      `json:"false_positives"`
	Missed            int      `json:"missed"`
}

const (
	minRuns = 3
	// tolerancePoints is the runbook's same-measurement band: a shift within
	// it is the same result.
	tolerancePoints = 5
	round4          = 1e4
	round6          = 1e6
)

func (r *runner) newReport() *Report {
	ids := make([]string, 0, len(r.cases))
	for _, c := range r.cases {
		ids = append(ids, c.id)
	}
	agent := AgentRef{Command: r.cfg.Agent, Model: r.cfg.Model, Runs: r.cfg.Runs, TimeoutSeconds: int(r.cfg.Timeout.Seconds())}
	if r.cfg.Loop > 1 {
		agent.Loop = r.cfg.Loop
	}
	return &Report{
		SchemaVersion:   "1",
		Date:            r.cfg.Now.Format("2006-01-02"),
		SeverityWeights: r.cfg.Weights,
		Seeded:          seededCorpus(r.cases),
		K:               r.cfg.K, Repeats: r.cfg.Runs,
		RunID:       runID(r.cfg),
		GeneratedAt: r.cfg.Now.Format(time.RFC3339),
		Internal:    r.cfg.Internal,
		Corpus:      CorpusRef{Commit: r.commit, Dirty: r.dirty, Cases: len(r.cases), CaseIDs: ids},
		Agent:       agent,
		Panel:       r.cfg.Panel,
		PromptSHA:   map[string]string{},
		Failures:    []Failure{},

		TolerancePoints: tolerancePoints,
	}
}

// finish turns the samples into the publication and writes it. The tool's
// aggregate is fetched last, so it sees exactly the bundles that recorded.
func (r *runner) finish(ctx context.Context, rep *Report) error {
	corpus, err := r.grader.trend(ctx)
	if err != nil {
		return err
	}
	rep.TrendCorpus = corpus
	version, err := r.grader.version(ctx)
	if err != nil {
		return err
	}
	rep.ToolVersion = version
	rep.Commands = r.commands()
	rep.PromptsHash = r.prompts
	samples := make([]corpuscore.Sample, 0, len(r.scores))
	for _, s := range r.scores {
		samples = append(samples,
			corpuscore.Sample{
				CaseID: s.caseID,
				Bugs:   s.cal.GroundTruth,
				Hits:   s.cal.Hits,
				Sampling: corpuscore.Sampling{
					RunID:   rep.RunID,
					K:       rep.K,
					Repeats: rep.Repeats,
					Sample:  s.run,
				},
			},
		)
	}
	rep.AnyOfKRecall, rep.IncompleteCohorts = corpuscore.AnyOfKRecall(samples)
	rep.Cases = r.spread()
	rep.Bundles = r.bundles()
	rep.Cost = r.cost()
	if len(r.loops) > 0 {
		rep.Loops = r.loopSummaries()
		rep.FixRates = r.fixRates()
		if lc := r.loopCost(); lc.Attempts > 0 {
			cp := lc
			rep.LoopCost = &cp
		}
	}
	rep.ElapsedSeconds = roundMillis(time.Since(r.started).Seconds())
	rep.Citable, rep.CitableReason = r.citable(rep)
	return rep.write(r.cfg.Out)
}

func (r *runner) bundles() []BundleRun {
	out := make([]BundleRun, 0, len(r.scores))
	for _, s := range r.scores {
		out = append(out, BundleRun{
			Slug:        s.slug,
			Case:        s.caseID,
			Run:         s.run,
			WallSeconds: s.wall,
			Warnings:    s.warnings,
			Calibrate:   s.data,
		})
	}
	return out
}

func (r *runner) spread() []CaseSpread {
	out := make([]CaseSpread, 0, len(r.cases))
	for _, c := range r.cases {
		out = append(out, r.caseSpread(c.id))
	}
	return out
}

func (r *runner) caseSpread(id string) CaseSpread {
	var precision, recall, fpr []*float64
	metrics := map[string][]*float64{}
	runs := 0
	for _, s := range r.scores {
		if s.caseID != id {
			continue
		}
		runs++
		for key, value := range sampleMetricValues(s.cal.Metrics) {
			metrics[key] = append(metrics[key], value)
		}
		precision = append(precision, s.cal.Precision)
		recall = append(recall, s.cal.Recall)
		fpr = append(fpr, s.cal.FalsePositiveRate)
	}
	stats := map[string]Stat{}
	for key, values := range metrics {
		stats[key] = stat(values)
	}
	return CaseSpread{Case: id, Runs: runs, Precision: stat(precision), Recall: stat(recall), FalsePositiveRate: stat(fpr), Metrics: stats}
}

// cost sums the est_usd each bundle reports. Nothing here prices a token: a
// rate the operator did not measure stays unmeasured.
func (r *runner) cost() CostRollup {
	roll := CostRollup{Bundles: len(r.scores)}
	for _, s := range r.scores {
		est, ok, incomplete := costEstimate(filepath.Join(reviewRoot, s.slug, costJSONName))
		if !ok {
			continue
		}
		roll.Usd += est
		roll.WithEstimate++
		if incomplete {
			roll.Incomplete++
		}
	}
	roll.Usd = math.Round(roll.Usd*round6) / round6
	roll.Measured = roll.Bundles > 0 && roll.WithEstimate == roll.Bundles && roll.Incomplete == 0
	return roll
}

func costEstimate(path string) (float64, bool, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false, false
	}
	var rec struct {
		EstUSD     *float64 `json:"est_usd"`
		Incomplete bool     `json:"incomplete"`
	}
	if json.Unmarshal(raw, &rec) != nil || rec.EstUSD == nil {
		return 0, false, false
	}
	return *rec.EstUSD, true, rec.Incomplete
}

// citable applies the runbook's publication bar: three samples per case, the
// whole corpus, no failures, and not an internal pilot.
func (r *runner) citable(rep *Report) (bool, string) {
	var reasons []string
	if rep.Seeded {
		reasons = append(reasons, "not a model-quality claim")
	}
	if rep.Internal {
		reasons = append(reasons, "internal pilot")
	}
	if rep.Agent.Runs < minRuns {
		reasons = append(reasons, fmt.Sprintf("%d sample(s) per case; the runbook publishes %d", rep.Agent.Runs, minRuns))
	}
	if rep.Corpus.Cases != r.total {
		reasons = append(reasons, fmt.Sprintf("%d of %d cases: a subset is a new publication", rep.Corpus.Cases, r.total))
	}
	if len(rep.Failures) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d failed sample(s)", len(rep.Failures)))
	}
	if r.cfg.Loop > 1 {
		reasons = append(reasons, "loop mode")
	}
	return len(reasons) == 0, strings.Join(reasons, "; ")
}

// loopSummaries converts stored outcomes.
func (r *runner) loopSummaries() []LoopSummary {
	out := make([]LoopSummary, 0, len(r.loops))
	for i := range r.loops {
		rec := &r.loops[i]
		cr := &caseRun{slug: rec.slug, corpus: corpusCase{id: rec.caseID}, index: rec.run}
		out = append(out, summarizeLoop(cr, &rec.out))
	}
	return out
}

// fixRates aggregates per-code resolution.
func (r *runner) fixRates() []FixRate {
	outs := make([]*LoopOutcome, 0, len(r.loops))
	for i := range r.loops {
		outs = append(outs, &r.loops[i].out)
	}
	return fixRatesFromOutcomes(outs)
}

// loopCost sums every attempt spend.
func (r *runner) loopCost() LoopCost {
	outs := make([]*LoopOutcome, 0, len(r.loops))
	for i := range r.loops {
		outs = append(outs, &r.loops[i].out)
	}
	return loopCostRollup(outs)
}

func (rep *Report) write(out string) error {
	doc, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return jq.NewErrf("report: %s", err)
	}
	if err := os.MkdirAll(filepath.Dir(out), dirPerm); err != nil {
		return jq.NewErrf("report dir: %s", err)
	}
	if err := atomic.WriteFile(out, append(doc, '\n')); err != nil {
		return jq.NewErrf("report %s: %s", out, err)
	}
	if err := atomic.WriteFile(strings.TrimSuffix(out, filepath.Ext(out))+".md", []byte(renderMarkdown(rep))); err != nil {
		return jq.NewErrf("Markdown report: %s", err)
	}
	return nil
}

// stat summarizes one rate across samples.
func stat(vals []*float64) Stat {
	kept := make([]float64, 0, len(vals))
	for _, v := range vals {
		if v != nil {
			kept = append(kept, math.Round(*v*round4)/round4)
		}
	}
	if len(kept) == 0 {
		return Stat{}
	}
	slices.Sort(kept)
	mid := len(kept) / 2
	median := kept[mid]
	if len(kept)%2 == 0 {
		median = (kept[mid-1] + kept[mid]) / 2
	}
	return Stat{N: len(kept), Median: &median, Min: &kept[0], Max: &kept[len(kept)-1]}
}

// render prints the shape of the run an operator needs before opening the
// JSON: per-case rates, the tool's aggregate, the spend, and the failures.
func (r *runner) render(w io.Writer, rep *Report) {
	fmt.Fprintf(w, "bench: %s, %d case(s) x %d run(s), panel %s\n",
		rep.Agent.Model, rep.Corpus.Cases, rep.Agent.Runs, strings.Join(rep.Panel, "+"))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "case\truns\tprecision\trecall\tfpr\twall(med)")
	for i := range rep.Cases {
		c := &rep.Cases[i]
		wall := r.caseWall(c.Case)
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%.1fs\n",
			c.Case, c.Runs, sampleRate(c.Precision), sampleRate(c.Recall),
			sampleRate(c.FalsePositiveRate), wall)
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintf(w, "table: %v\n", err)
	}
	r.renderTotals(w, rep)
	for _, f := range rep.Failures {
		fmt.Fprintf(w, "failed: %s run %d: %s (%s)\n", f.Case, f.Run, f.Error, f.Evidence)
	}
	if rep.Citable {
		fmt.Fprintln(w, "citable: yes")
	} else {
		fmt.Fprintf(w, "citable: no (%s)\n", rep.CitableReason)
	}
	fmt.Fprintf(w, "run dir: %s\nwrote: %s\n", r.work, r.cfg.Out)
}

func (r *runner) renderTotals(w io.Writer, rep *Report) {
	var totals trendRates
	if len(rep.TrendCorpus) == 0 || json.Unmarshal(rep.TrendCorpus, &totals) != nil || totals.Bundles == 0 {
		fmt.Fprintln(w, "corpus: no recorded bundle")
	} else {
		fmt.Fprintf(w,
			"corpus: %d bundle(s), %d bug-runs, %d reported, %d matched, %d false positives, %d missed — "+
				"precision %s (n=%d), recall %s (n=%d), fpr %s (n=%d)\n",
			totals.Bundles, totals.GroundTruth, totals.Reported, totals.Matched, totals.FalsePositives, totals.Missed,
			rate(totals.Precision), totals.Reported, rate(totals.Recall), totals.GroundTruth,
			rate(totals.FalsePositiveRate), totals.Reported)
	}
	if rep.Cost.Measured {
		fmt.Fprintf(w, "cost: measured %.6f USD over %d bundle(s)\n", rep.Cost.Usd, rep.Cost.Bundles)
	} else {
		fmt.Fprintf(w, "cost: unmeasured (%d of %d bundles carry est_usd)\n", rep.Cost.WithEstimate, rep.Cost.Bundles)
	}
	if rep.PromptsHash != "" {
		fmt.Fprintf(w, "prompts_hash: %s, %.1fs elapsed\n", rep.PromptsHash, rep.ElapsedSeconds)
	}
}

func (r *runner) caseWall(id string) float64 {
	var walls []float64
	for _, s := range r.scores {
		if s.caseID == id {
			walls = append(walls, s.wall)
		}
	}
	slices.Sort(walls)
	if len(walls) == 0 {
		return 0
	}
	return walls[len(walls)/2]
}

func rate(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.4f", *v)
}

func sampleMetricValues(m *corpuscore.Metrics) map[string]*float64 {
	if m == nil {
		return nil
	}
	number := func(v any) *float64 {
		f, ok := v.(float64)
		if !ok {
			return nil
		}
		return &f
	}
	return map[string]*float64{
		"precision":            number(m.Precision.Value),
		"recall":               number(m.Recall.Value),
		"control_fp_rate":      number(m.ControlFPRate.Value),
		"severity_weighted_f1": number(m.SeverityWeightedF1.Value),
		"signal_to_noise":      number(m.SignalToNoise.Value),
		"cost_usd":             m.CostUSD,
		"latency_s":            m.LatencyS,
	}
}

func seededCorpus(cases []corpusCase) bool {
	for _, c := range cases {
		var manifest struct {
			Kind       string `json:"kind"`
			Provenance struct {
				Kind string `json:"kind"`
			} `json:"provenance"`
		}
		if json.Unmarshal(c.manifest, &manifest) != nil || manifest.Kind == "seeded" || manifest.Provenance.Kind == "seeded" {
			return true
		}
	}
	return false
}

func (r *runner) commands() [][]string {
	c := r.cfg
	command := []string{
		"go", "run", "./bench/runner", "-model", c.Model, "-agent", c.Agent,
		"-repeats", strconv.Itoa(c.Runs), "-k", strconv.Itoa(c.K), "-panel", strings.Join(c.Panel, ","),
		"-root", c.Root, "-corpus", c.Corpus, "-verdict", c.Verdict, "-out", c.Out, "-timeout", c.Timeout.String(),
		"-loop", strconv.Itoa(c.Loop),
	}
	if len(c.Cases) > 0 {
		command = append(command, "-cases", strings.Join(c.Cases, ","))
	}
	if c.Internal {
		command = append(command, "-internal")
	}
	commands := [][]string{command}
	for _, s := range r.scores {
		commands = append(commands, []string{c.Verdict, "record", c.Repo, s.slug, jsonFlag},
			[]string{c.Verdict, "calibrate", c.Repo, s.slug, jsonFlag})
	}
	return append(commands, []string{c.Verdict, "trend", c.Repo, jsonFlag})
}

func sampleRate(s Stat) string { return fmt.Sprintf("%s (n=%d)", rate(s.Median), s.N) }
