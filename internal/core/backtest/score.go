package backtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// modeScore names the scoring pass in the artifact and the envelope.
const modeScore = "score"

// msgScoreCanceled is the frozen score cancel text.
const msgScoreCanceled = "canceled before backtest score completed"

// ScoreRequest is one `backtest score` invocation: the injected runtime and
// the run whose replay bundles are graded. It reads only the run directory and
// the bundles that directory names; it never launches an agent, builds a
// bundle, or writes into the corpus store.
type ScoreRequest struct {
	Runtime  *core.Runtime
	RepoRoot string
	Run      string
	Warnings []string
}

// ScoreResponse carries the envelope data and the accumulated warnings.
type ScoreResponse struct { //nolint:govet // field grouping documents the response surface
	Data     *ScoreReport
	Warnings []string
	Next     string
	At       string
}

// ScoreReport is report.json, and the score envelope's data object: the two
// are the same shape, so one schema describes both and a consumer never has to
// know which one it holds.
type ScoreReport struct { //nolint:govet // field order is the published property order
	SchemaVersion    string        `const:"1"                  json:"schema_version"`
	Run              string        `json:"run"`
	Mode             string        `json:"mode"`
	PRs              int           `json:"prs"                 min:"0"`
	NotReplayed      int           `json:"not_replayed"        min:"0"`
	Bugs             int           `json:"bugs"                min:"0"`
	Reported         int           `json:"reported"            min:"0"`
	Matched          int           `json:"matched"             min:"0"`
	Unclassified     int           `json:"unclassified"        min:"0"`
	CatchRate        *float64      `json:"catch_rate"          min:"0"`
	OffTargetPerPR   *float64      `json:"off_target_per_pr"   min:"0"`
	CostPerReviewUSD *float64      `json:"cost_per_review_usd" min:"0"`
	Spend            ScoreSpend    `json:"spend"`
	Signals          []ScoreSignal `json:"signals"`
}

// ScoreSpend is the priced spend over the scored reviews. USD is the sum of
// the prices the reviews reported, null when none was priced, and a lower
// bound whenever UnpricedReviews is non-zero.
type ScoreSpend struct {
	USD             *float64 `json:"usd"              min:"0"`
	PricedReviews   int      `json:"priced_reviews"   min:"0"`
	UnpricedReviews int      `json:"unpriced_reviews" min:"0"`
}

// ScoreSignal is one row of the seed-signal table: the bugs a fix or a revert
// produced, how many a review named, and the rate between them.
type ScoreSignal struct { //nolint:govet // field order is the published property order
	Signal    string   `enum:"fix|revert" json:"signal"`
	Bugs      int      `json:"bugs"       min:"0"`
	Matched   int      `json:"matched"    min:"0"`
	CatchRate *float64 `json:"catch_rate" min:"0"`
}

// Wire renders the data object in published key order.
func (d *ScoreReport) Wire() *jq.Obj {
	signals := make([]any, 0, len(d.Signals))
	for i := range d.Signals {
		signals = append(signals, d.Signals[i].wire())
	}
	return jq.NewObj(
		"schema_version", d.SchemaVersion,
		"run", d.Run,
		"mode", d.Mode,
		"prs", d.PRs,
		"not_replayed", d.NotReplayed,
		"bugs", d.Bugs,
		"reported", d.Reported,
		"matched", d.Matched,
		"unclassified", d.Unclassified,
		"catch_rate", optionalFloat(d.CatchRate),
		"off_target_per_pr", optionalFloat(d.OffTargetPerPR),
		"cost_per_review_usd", optionalFloat(d.CostPerReviewUSD),
		"spend", d.Spend.wire(),
		"signals", signals,
	)
}

// wire renders one signal row.
func (s *ScoreSignal) wire() *jq.Obj {
	return jq.NewObj(
		"signal", s.Signal,
		"bugs", s.Bugs,
		"matched", s.Matched,
		"catch_rate", optionalFloat(s.CatchRate),
	)
}

// wire renders the spend block.
func (s *ScoreSpend) wire() *jq.Obj {
	return jq.NewObj(
		"usd", optionalFloat(s.USD),
		"priced_reviews", s.PricedReviews,
		"unpriced_reviews", s.UnpricedReviews,
	)
}

// RunScore executes `backtest score`: grade every replayed PR's findings
// against the bugs its own candidate records, write report.json, and print
// the rates. The replay bundles are the input; the corpus store is untouched.
func RunScore(ctx context.Context, req *ScoreRequest) (resp *ScoreResponse, ref *core.RefusalError) {
	st := &state{rt: req.Runtime, cancelMsg: msgScoreCanceled, warnings: append([]string{}, req.Warnings...)}
	defer st.carryScore(&resp, &ref)
	if ref := st.canceled(ctx); ref != nil {
		return nil, ref
	}
	run, ref := scoreRunID(req.Run)
	if ref != nil {
		return nil, ref
	}
	st.root = req.RepoRoot
	candidates, ref := st.scoreCandidates(run)
	if ref != nil {
		return nil, ref
	}
	rows, ref := st.scoreRows(run, len(candidates))
	if ref != nil {
		return nil, ref
	}
	data, ref := st.score(ctx, run, candidates, rows)
	if ref != nil {
		return nil, ref
	}
	if ref := st.canceled(ctx); ref != nil {
		return nil, ref
	}
	if ref := st.writeReport(run, data); ref != nil {
		return nil, ref
	}
	st.humanScore(data)
	return &ScoreResponse{
		Data:     data,
		Warnings: st.warnings,
		Next:     "inspect .verdict/backtest/" + run + "/report.json; a score is a match rate over blame-derived candidates, not a citable measurement",
		At:       st.timeUTC(),
	}, nil
}

// carryScore replays the warnings onto the response or the refusal.
func (st *state) carryScore(resp **ScoreResponse, ref **core.RefusalError) {
	if *ref != nil {
		(*ref).Warnings = st.warnings
	}
	if *resp != nil {
		(*resp).Warnings = st.warnings
	}
}

// scoreRunID validates --run: scoring needs the run id its replay wrote.
func scoreRunID(run string) (string, *core.RefusalError) {
	if run == "" {
		return "", &core.RefusalError{Code: codeBadFlags, Message: "backtest score needs --run <id> (the run 'backtest replay' wrote)"}
	}
	if ref := runIDSlug(run); ref != nil {
		return "", ref
	}
	return run, nil
}

// scoreCandidates reads the mined candidates the replay walked.
func (st *state) scoreCandidates(run string) ([]Candidate, *core.RefusalError) {
	path := filepath.Join(st.root, artifactDir, run, "mine.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, st.refuse(codeMissingBundle, fmt.Sprintf(
			"backtest run '%s' has no mine.json — mine it first (verdict backtest mine --since <span> --run %s)",
			run, run))
	}
	if err != nil {
		return nil, st.refuse(codeBacktestFail, "cannot read "+display(st.root, path)+labelSep+err.Error())
	}
	var doc MineDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, st.refuse(codeBacktestRunStale, "unreadable "+display(st.root, path)+"; re-mine the run in place")
	}
	return doc.Candidates, nil
}

// scoreRows reads every candidate's resume record. A run with no prs
// directory was never replayed, which is the one thing scoring cannot recover
// from — unless mining found no candidates at all: there is nothing to record,
// and the zero report is the honest result.
func (st *state) scoreRows(run string, mined int) ([]ReplayPRDoc, *core.RefusalError) {
	dir := filepath.Join(st.root, artifactDir, run, replayPRsDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if mined == 0 {
			return nil, nil
		}
		return nil, st.refuse(codeMissingBundle, fmt.Sprintf(
			"backtest run '%s' has no replayed reviews — replay it first (verdict backtest replay --run %s --agent <name>)",
			run, run))
	}
	if err != nil {
		return nil, st.refuse(codeBacktestFail, "cannot read "+display(st.root, dir)+labelSep+err.Error())
	}
	rows := make([]ReplayPRDoc, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		doc, ref := st.scoreRow(run, filepath.Join(dir, entry.Name()))
		if ref != nil {
			return nil, ref
		}
		rows = append(rows, *doc)
	}
	return rows, nil
}

// scoreRow reads one candidate's resume record.
func (st *state) scoreRow(run, dir string) (*ReplayPRDoc, *core.RefusalError) {
	path := filepath.Join(dir, replayFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, st.refuse(codeBacktestRowStale, "unreadable "+display(st.root, path)+"; delete its directory to replay this PR again")
	}
	var doc ReplayPRDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, st.refuse(codeBacktestRowStale, "unreadable "+display(st.root, path)+"; delete its directory to replay this PR again")
	}
	if doc.Run != run {
		return nil, st.refuse(codeBacktestRowStale, fmt.Sprintf(
			"%s names run '%s', not '%s'; delete its directory to replay it", st.displayPR(run, doc.N), doc.Run, run))
	}
	return &doc, nil
}

// score grades every review that recorded one. A row the replay marked as ok
// but whose candidate the mine no longer carries refuses backtest-run-stale:
// the review would be graded against ground truth that is not this run's. The
// context is checked between reviews so a cancel stops before the next read.
func (st *state) score(
	ctx context.Context, run string, candidates []Candidate, rows []ReplayPRDoc,
) (*ScoreReport, *core.RefusalError) {
	totals := &scoreTotals{scored: map[*Candidate]bool{}}
	for i := range rows {
		if ref := st.canceled(ctx); ref != nil {
			return nil, ref
		}
		if ref := st.scoreOne(totals, run, &rows[i], candidates); ref != nil {
			return nil, ref
		}
	}
	return totals.report(run, len(candidates)), nil
}

// scoreTotals accumulates one pass's counts and priced spend.
type scoreTotals struct {
	scored   map[*Candidate]bool
	data     ScoreReport
	tally    signalTally
	prices   float64
	priced   int
	unpriced int
}

// scoreOne folds one resume record into the totals; a row that recorded no
// review contributes nothing but its not-replayed count.
func (st *state) scoreOne(t *scoreTotals, run string, row *ReplayPRDoc, candidates []Candidate) *core.RefusalError {
	if row.Status != statusOK {
		return nil
	}
	c, ref := st.rowCandidate(run, row, candidates)
	if ref != nil {
		return ref
	}
	if t.scored[c] {
		return st.refuse(codeBacktestRowStale, fmt.Sprintf(
			"run '%s' PR %d repeats a candidate another resume record already scored; delete one of the record directories", run, row.N))
	}
	t.scored[c] = true
	report, ref := st.gradeReview(c, row.Bundle)
	if ref != nil {
		return ref
	}
	t.data.PRs++
	t.data.Bugs += len(c.Bugs)
	t.data.Reported += report.Reported
	t.data.Matched += report.Matched
	if ref := st.gradeSignals(&t.tally, run, c, report); ref != nil {
		return ref
	}
	t.addSpend(row.CostUSD)
	return nil
}

// addSpend folds one review's price: an unpriced review contributes no
// sample, so an unknown price never divides into the measured mean.
func (t *scoreTotals) addSpend(cost *float64) {
	if cost == nil {
		t.unpriced++
		return
	}
	t.prices += *cost
	t.priced++
}

// report renders the final object.
func (t *scoreTotals) report(run string, candidates int) *ScoreReport {
	t.data.SchemaVersion = schemaVersion
	t.data.Run = run
	t.data.Mode = modeScore
	t.data.NotReplayed = candidates - t.data.PRs
	t.data.Unclassified = t.tally.unclassified
	t.data.Signals = t.tally.rows()
	t.data.CatchRate = ratePtr(t.data.Matched, t.data.Bugs)
	t.data.OffTargetPerPR = ratePtr(t.data.Reported-t.data.Matched, t.data.PRs)
	t.data.Spend = ScoreSpend{PricedReviews: t.priced, UnpricedReviews: t.unpriced}
	if t.priced > 0 {
		usd := readcommon.Round4(t.prices)
		mean := readcommon.Round4(t.prices / float64(t.priced))
		t.data.Spend.USD = &usd
		t.data.CostPerReviewUSD = &mean
	}
	return &t.data
}

// rowCandidate is the mined candidate a recorded review belongs to.
func (st *state) rowCandidate(run string, row *ReplayPRDoc, candidates []Candidate) (*Candidate, *core.RefusalError) {
	for i := range candidates {
		if candidates[i].Head == row.Head && candidates[i].Base == row.Base {
			return &candidates[i], nil
		}
	}
	return nil, st.refuse(codeBacktestRunStale, fmt.Sprintf(
		"run '%s' PR %d was replayed at %s..%s, which mine.json no longer carries; re-mine and replay the run",
		run, row.N, short(row.Base), short(row.Head)))
}

// gradeReview grades one PR's recorded findings against its bugs through the
// corpus matcher, on a case built in memory: a backtest case is never written
// as a corpus manifest and never enters the corpus store.
func (st *state) gradeReview(c *Candidate, bundle string) (*corpuscore.Report, *core.RefusalError) {
	if bundle == "" {
		return nil, st.refuse(codeCorruptStore, "a recorded review names no bundle; replay it again")
	}
	dir := filepath.FromSlash(readcommon.ResolveBundleDir(st.root, bundle))
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		return nil, st.refuse(codeBacktestRowStale,
			"recorded review "+display(st.root, dir)+" is missing or unreadable; delete its prs/<NNN>/ directory and replay the run")
	}
	return corpuscore.Evaluate(scoreCase(c), entries), nil
}

// signalTally accumulates the two published seed-signal rows.
type signalTally struct {
	unclassified int
	fix          signalCounts
	revert       signalCounts
}

// signalCounts is one signal row's bug and matched totals.
type signalCounts struct {
	bugs    int
	matched int
}

// gradeSignals adds one PR's bugs to the signal table. The matched bugs are
// the true-positive outcomes' own anchors: one bug is one (path, line), so the
// key identifies it without re-running the matcher.
func (st *state) gradeSignals(tally *signalTally, run string, c *Candidate, report *corpuscore.Report) *core.RefusalError {
	matched := matchedAnchors(report)
	for i := range c.Bugs {
		b := &c.Bugs[i]
		row, ok := tally.row(b.Signal)
		if !ok {
			return st.refuse(codeBacktestRunStale, fmt.Sprintf(
				"run '%s' was mined before bugs recorded a seed signal; re-mine it", run))
		}
		row.bugs++
		if matched[b.Path+":"+strconv.Itoa(b.Line)] {
			row.matched++
		}
		if b.Kind == kindUnclassified {
			tally.unclassified++
		}
	}
	return nil
}

// row resolves one signal to its accumulator.
func (t *signalTally) row(signal string) (*signalCounts, bool) {
	switch signal {
	case signalFix:
		return &t.fix, true
	case signalRevert:
		return &t.revert, true
	default:
		return nil, false
	}
}

// rows renders the table: fix then revert, always both, so a signal with no
// bugs reads as an unmeasured zero rather than disappearing.
func (t *signalTally) rows() []ScoreSignal {
	return []ScoreSignal{
		{Signal: signalFix, Bugs: t.fix.bugs, Matched: t.fix.matched, CatchRate: ratePtr(t.fix.matched, t.fix.bugs)},
		{Signal: signalRevert, Bugs: t.revert.bugs, Matched: t.revert.matched, CatchRate: ratePtr(t.revert.matched, t.revert.bugs)},
	}
}

// matchedAnchors is the set of ground-truth anchors a true-positive outcome
// named.
func matchedAnchors(report *corpuscore.Report) map[string]bool {
	matched := map[string]bool{}
	for i := range report.Outcomes {
		o := &report.Outcomes[i]
		if o.Result == corpuscore.ResultTruePositive && o.HasPath && o.HasBugHit {
			matched[o.Path+":"+strconv.Itoa(o.BugLine)] = true
		}
	}
	return matched
}

// writeReport lands report.json atomically in the run directory.
func (st *state) writeReport(run string, data *ScoreReport) *core.RefusalError {
	path := filepath.Join(st.root, artifactDir, run, "report.json")
	if err := atomic.WriteFile(path, []byte(jq.Pretty(data))); err != nil {
		return st.refuse(codeBacktestFail, "cannot write "+display(st.root, path)+labelSep+err.Error())
	}
	return nil
}

// severityInert is the severity a backtest case's bugs carry. A backtest bug
// records no severity, so the value only fills the corpus type, and the
// matcher never reads it.
const severityInert = "important"

// scoreCase builds the in-memory case a PR is graded against. An
// `unclassified` bug stays in the denominator but never reaches the matcher:
// its kind text is a sentinel, so a finding that says "unclassified" would
// otherwise grade as a true positive. The bugs carry no severity claim: the
// value only fills the type, and Evaluate ignores it.
func scoreCase(c *Candidate) *core.CorpusCase {
	bugs := make([]core.CorpusBug, 0, len(c.Bugs))
	for _, b := range c.Bugs {
		if b.Kind == kindUnclassified {
			continue
		}
		bugs = append(bugs, core.CorpusBug{Path: b.Path, Line: b.Line, Kind: b.Kind, Severity: severityInert})
	}
	return &core.CorpusCase{
		SchemaVersion: schemaVersion,
		ID:            "backtest",
		Kind:          "real",
		Provenance:    core.CorpusProvenance{Kind: "real"},
		Bugs:          bugs,
	}
}

// ratePtr publishes one rate, null when the denominator is zero.
func ratePtr(n, d int) *float64 {
	rate, ok := corpuscore.Rate(n, d)
	if !ok {
		return nil
	}
	return &rate
}

// humanScore renders the rates, the signal table, and the not-citable line.
func (st *state) humanScore(d *ScoreReport) {
	st.humanLine(fmt.Sprintf("backtest score %s  (%s)", d.Run, scoreScope(d)))
	st.humanLine(fmt.Sprintf("  catch rate: %s (%d/%d bugs)", scoreRateText(d.CatchRate), d.Matched, d.Bugs))
	st.humanLine(fmt.Sprintf("  off-target: %s per PR (%d reported, %d matched, %s)",
		scoreRateText(d.OffTargetPerPR), d.Reported, d.Matched, scorePRs(d.PRs)))
	st.humanLine(scoreCost(d))
	st.humanLine(fmt.Sprintf("  unclassified: %d of %d bugs (kind matched no rule)", d.Unclassified, d.Bugs))
	for i := range d.Signals {
		st.humanLine(scoreSignalLine(&d.Signals[i]))
	}
	st.humanLine("not citable — SZZ-derived: a score grades a blame-derived guess, not a verified defect")
}

// scoreScope renders the header's counted scope.
func scoreScope(d *ScoreReport) string {
	scope := scorePRs(d.PRs) + ", " + scoreBugs(d.Bugs)
	if d.NotReplayed > 0 {
		scope += ", " + strconv.Itoa(d.NotReplayed) + " not replayed"
	}
	return scope
}

// scoreCost renders the per-review cost line: a priced mean, a lower-bound
// spend when some reviews reported nothing, or `n/a` when none was priced.
func scoreCost(d *ScoreReport) string {
	s := d.Spend
	if s.PricedReviews == 0 {
		return fmt.Sprintf("  cost: n/a per review (0 priced reviews, %d unpriced)", s.UnpricedReviews)
	}
	mean := fmt.Sprintf(priceFormat, *d.CostPerReviewUSD)
	total := fmt.Sprintf(priceFormat, *s.USD)
	if s.UnpricedReviews > 0 {
		return fmt.Sprintf("  cost: %s per review (≥ %s over %s, %d unpriced)",
			mean, total, scorePriced(s.PricedReviews), s.UnpricedReviews)
	}
	return fmt.Sprintf("  cost: %s per review (%s over %s)", mean, total, scorePriced(s.PricedReviews))
}

// scoreSignalLine renders one seed-signal row.
func scoreSignalLine(s *ScoreSignal) string {
	return fmt.Sprintf("  signal %s: %s (%d/%d)", s.Signal, scoreRateText(s.CatchRate), s.Matched, s.Bugs)
}

// scoreRateText renders one published rate as text, n/a when unmeasured.
func scoreRateText(rate *float64) string {
	if rate == nil {
		return "n/a"
	}
	return corpuscore.RateText(*rate)
}

// scorePRs renders a PR count with its noun.
func scorePRs(n int) string { return scoreCount(n, "PR", "PRs") }

// scoreBugs renders a bug count with its noun.
func scoreBugs(n int) string { return scoreCount(n, "bug", "bugs") }

// scorePriced renders a priced-review count with its noun.
func scorePriced(n int) string { return scoreCount(n, "priced review", "priced reviews") }

// scoreCount renders one count with the singular or plural noun.
func scoreCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
