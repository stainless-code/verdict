package quality

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/evidence"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	quoteUnsupported    = "unsupported"
	quoteMatched        = "matched"
	quoteMismatch       = "mismatch"
	quoteNoQuote        = "no-quote"
	hallucStubbed       = "stubbed"
	hallucMeasured      = "measured"
	refusalInsufficient = "review-insufficient"
	rateBits            = 64
	notApplicable       = "n/a"
)

// IsGatingFinding is the one predicate the quote verifier sees: a gating
// finding is critical or important, anchored, and not marked unverified. No
// other call site decides which findings a verifier checks.
func IsGatingFinding(f *Finding) bool {
	switch strings.ToLower(f.Severity) {
	case "critical", "important":
	default:
		return false
	}
	if f.Anchored == nil || !*f.Anchored {
		return false
	}
	return f.Refusal != "unverified"
}

// QuoteResult is one verifier answer. The anchored quote check answers through
// this interface so the published report shape never moves with the verifier.
type QuoteResult struct {
	Status string
	Reason string
}

// QuoteVerifier checks one finding's quote against the pinned fixture head
// material. Only explicit "matched" and "mismatch" results are verified;
// "no-quote", "unsupported" and unknown results are unverifiable.
type QuoteVerifier interface {
	Verify(ctx context.Context, f *Finding, head []byte) QuoteResult
}

// PinnedQuoteVerifier is the default verifier: it checks each finding's quote
// against the fixture's pinned full.diff bytes with evidence.MatchQuote. It
// answers for every fixture the catalog pins, so its status is measured even
// when a fixture carries no quote to check.
type PinnedQuoteVerifier struct{}

// Verify implements QuoteVerifier. The index is built per call because the
// interface hands the verifier bytes, not a pre-parsed diff.
func (PinnedQuoteVerifier) Verify(_ context.Context, f *Finding, diff []byte) QuoteResult {
	return verifyQuote(f, evidence.IndexDiff(diff))
}

func verifyQuote(f *Finding, idx *evidence.DiffIndex) QuoteResult {
	if strings.TrimSpace(f.Quote) == "" {
		return QuoteResult{Status: quoteNoQuote}
	}
	res := evidence.MatchQuote(idx, evidence.QuoteRequest{
		Path:      f.Path,
		Side:      f.Side,
		Line:      f.Line,
		StartLine: f.StartLine,
		Quote:     f.Quote,
	})
	if res.Verified() {
		return QuoteResult{Status: quoteMatched}
	}
	switch res.Reason {
	case evidence.ReasonTextDiffers, evidence.ReasonDeletedLine, evidence.ReasonRangeCrossesHunks:
		return QuoteResult{Status: quoteMismatch, Reason: res.Reason}
	default:
		return QuoteResult{Status: quoteUnsupported, Reason: res.Reason}
	}
}

// resolveVerifier returns the verifier for one run and the status that
// verifier justifies. Only the pinned verifier measures — by value or by
// pointer, because both answer from the fixture's pinned bytes — while an
// injected double is reported stubbed so the report never claims a stand-in
// checked a quote.
func resolveVerifier(verifier QuoteVerifier) (QuoteVerifier, string) {
	switch verifier.(type) {
	case nil, PinnedQuoteVerifier, *PinnedQuoteVerifier:
		return PinnedQuoteVerifier{}, hallucMeasured
	default:
		return verifier, hallucStubbed
	}
}

// RecallReport is the fixture's recall over scored runs. A nil rate means the
// denominator was zero, not that recall was zero.
type RecallReport struct {
	PerRun    []float64 `json:"per_run"`
	Mean      *float64  `json:"mean"`
	Union     *float64  `json:"union"`
	UnionHits []string  `json:"union_hits"`
}

// BonusReport reports bonus defects separately; they never enter recall.
type BonusReport struct {
	Hits  []string `json:"hits"`
	Count int      `json:"count"`
}

// FalseGreenReport is the measured gate-pass rate. Unknown gate outcomes are
// listed but excluded from the denominator.
type FalseGreenReport struct {
	Rate     *float64 `json:"rate"`
	Passes   int      `json:"passes"`
	Measured int      `json:"measured"`
	Unknown  int      `json:"unknown"`
}

// HallucinationReport counts explicitly verified quotes only. A finding whose
// quote cannot be checked is unverifiable, never a clean check. Status is
// measured when the pinned verifier ran and stubbed when a test double stood
// in for it; rate is null until a quote is checked either way.
type HallucinationReport struct {
	Rate           *float64 `json:"rate"`
	Status         string   `json:"status"`
	GatingFindings int      `json:"gating_findings"`
	Checked        int      `json:"checked"`
	Unverifiable   int      `json:"unverifiable"`
}

// CalibrationReport summarizes the hand-score rows.
type CalibrationReport struct {
	Mismatches []string `json:"mismatches"`
	Rows       int      `json:"rows"`
	Reproduced bool     `json:"reproduced"`
}

// FixtureReport is one fixture's published metrics.
type FixtureReport struct {
	FalseGreen         FalseGreenReport    `json:"false_green"`
	ID                 string              `json:"id"`
	Label              string              `json:"label"`
	Recall             RecallReport        `json:"recall"`
	BonusUnion         BonusReport         `json:"bonus_union"`
	Hallucination      HallucinationReport `json:"hallucination"`
	Runs               int                 `json:"runs"`
	Scored             int                 `json:"scored"`
	Known              int                 `json:"known"`
	Bonus              int                 `json:"bonus"`
	ReviewInsufficient int                 `json:"review_insufficient"`
}

// Diagnostic is one run's matcher output, kept beside the rates so a reader
// can audit a surprising hit without opening the recording.
type Diagnostic struct {
	GatePassed    *bool    `json:"gate_passed"`
	Recording     string   `json:"recording"`
	Fixture       string   `json:"fixture"`
	InfraError    string   `json:"infra_error"`
	Hits          []string `json:"hits"`
	Praise        int      `json:"praise"`
	NullSeverity  int      `json:"null_severity"`
	OtherFindings int      `json:"other_findings"`
	Scored        bool     `json:"scored"`
}

// Report is the `backtest fixtures` data object, also written to
// .verdict/backtest/fixtures/report.json.
type Report struct {
	SchemaVersion string            `json:"schema_version"`
	Mode          string            `json:"mode"`
	Fixtures      []FixtureReport   `json:"fixtures"`
	Diagnostics   []Diagnostic      `json:"diagnostics"`
	Calibration   CalibrationReport `json:"calibration"`
	Recordings    int               `json:"recordings"`
	Scored        int               `json:"scored"`
}

// Wire renders the report in the published key order.
func (r *Report) Wire() *jq.Obj {
	fixtures := make([]any, 0, len(r.Fixtures))
	for i := range r.Fixtures {
		fixtures = append(fixtures, r.Fixtures[i].Wire())
	}
	diagnostics := make([]any, 0, len(r.Diagnostics))
	for i := range r.Diagnostics {
		diagnostics = append(diagnostics, r.Diagnostics[i].Wire())
	}
	return jq.NewObj(
		"schema_version", r.SchemaVersion,
		"mode", r.Mode,
		"recordings", r.Recordings,
		"scored", r.Scored,
		"fixtures", fixtures,
		"calibration", r.Calibration.Wire(),
		"diagnostics", diagnostics,
	)
}

// Wire renders one fixture's metrics in the published key order.
func (r *FixtureReport) Wire() *jq.Obj {
	perRun := make([]any, 0, len(r.Recall.PerRun))
	for _, rate := range r.Recall.PerRun {
		perRun = append(perRun, rate)
	}
	return jq.NewObj(
		"id", r.ID,
		"label", r.Label,
		"runs", r.Runs,
		"scored", r.Scored,
		"known", r.Known,
		"bonus", r.Bonus,
		"recall", jq.NewObj(
			"per_run", perRun,
			"mean", optionalFloatPtr(r.Recall.Mean),
			"union", optionalFloatPtr(r.Recall.Union),
			"union_hits", stringsValue(r.Recall.UnionHits),
		),
		"bonus_union", jq.NewObj("count", r.BonusUnion.Count, "hits", stringsValue(r.BonusUnion.Hits)),
		"false_green", jq.NewObj(
			"passes", r.FalseGreen.Passes,
			"measured", r.FalseGreen.Measured,
			"unknown", r.FalseGreen.Unknown,
			"rate", optionalFloatPtr(r.FalseGreen.Rate),
		),
		"hallucination", jq.NewObj(
			"rate", optionalFloatPtr(r.Hallucination.Rate),
			"status", r.Hallucination.Status,
			"gating_findings", r.Hallucination.GatingFindings,
			"checked", r.Hallucination.Checked,
			"unverifiable", r.Hallucination.Unverifiable,
		),
		"review_insufficient", r.ReviewInsufficient,
	)
}

// Wire renders one diagnostic in the published key order.
func (r *Diagnostic) Wire() *jq.Obj {
	return jq.NewObj(
		"recording", r.Recording,
		"fixture", r.Fixture,
		"hits", stringsValue(r.Hits),
		"praise", r.Praise,
		"null_severity", r.NullSeverity,
		"other_findings", r.OtherFindings,
		"gate_passed", optionalBool(r.GatePassed),
		"infra_error", r.InfraError,
		"scored", r.Scored,
	)
}

// Wire renders the calibration summary in the published key order.
func (r *CalibrationReport) Wire() *jq.Obj {
	return jq.NewObj(
		"rows", r.Rows,
		"reproduced", r.Reproduced,
		"mismatches", stringsValue(r.Mismatches),
	)
}

func stringsValue(values []string) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, v)
	}
	return out
}

func optionalFloatPtr(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func optionalBool(v *bool) any {
	if v == nil {
		return nil
	}
	return *v
}

func ratePtr(n, d int) *float64 {
	if d == 0 {
		return nil
	}
	rate, ok := corpuscore.Rate(n, d)
	if !ok {
		return nil
	}
	return &rate
}

func roundRate(v float64) float64 {
	scale := math.Pow10(corpuscore.RateDecimals)
	return math.Round(v*scale) / scale
}

func rateText(v *float64) string {
	if v == nil {
		return notApplicable
	}
	return strconv.FormatFloat(*v, 'f', corpuscore.RateDecimals, rateBits)
}

func countNoun(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func unknownText(n int) string {
	if n == 1 {
		return "1 unknown gate outcome"
	}
	return strconv.Itoa(n) + " unknown gate outcomes"
}

// Human renders the frozen human block from the report data.
func Human(r *Report) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "backtest fixtures  (%d fixtures, %d recorded reviews, %d scored)\n",
		len(r.Fixtures), r.Recordings, r.Scored)
	for i := range r.Fixtures {
		f := &r.Fixtures[i]
		fmt.Fprintf(&sb, "%s  %s  (%s, %d scored)\n", f.ID, f.Label,
			countNoun(f.Runs, "recording"), f.Scored)
		perRun := make([]string, 0, len(f.Recall.PerRun))
		for _, rate := range f.Recall.PerRun {
			perRun = append(perRun, rateText(&rate))
		}
		fmt.Fprintf(&sb, "  recall per run: %s  mean: %s  union: %s (N=%d, %d known defects)\n",
			strings.Join(perRun, " "), rateText(f.Recall.Mean), rateText(f.Recall.Union), len(perRun), f.Known)
		fmt.Fprintf(&sb, "  bonus union: %d/%d  false green: %d/%d (%s)\n",
			f.BonusUnion.Count, f.Bonus, f.FalseGreen.Passes, f.FalseGreen.Measured, unknownText(f.FalseGreen.Unknown))
		fmt.Fprintf(&sb, "  hallucination: %s (%s; %d gating findings, %d checked, %d unverifiable)  review-insufficient: %d\n",
			rateText(f.Hallucination.Rate), f.Hallucination.Status, f.Hallucination.GatingFindings,
			f.Hallucination.Checked, f.Hallucination.Unverifiable, f.ReviewInsufficient)
	}
	fmt.Fprintf(&sb, "calibration: %d/%d rows reproduced\n", r.Calibration.Rows-len(r.Calibration.Mismatches), r.Calibration.Rows)
	sb.WriteString("known defects from fixture manifests; rates describe recorded reviews, not a model claim\n")
	return sb.String()
}
