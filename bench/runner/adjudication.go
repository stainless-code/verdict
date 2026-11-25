package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	corpuscore "github.com/stainless-code/verdict/internal/core/corpus"
	"github.com/stainless-code/verdict/internal/jq"
)

// The three verdicts one unmatched finding can carry. real-unlisted and
// matches-ground-truth raise adjudicated precision; matches-ground-truth also
// raises adjudicated recall by counting its bug as found.
const (
	verdictRealUnlisted       = "real-unlisted"
	verdictMatchesGroundTruth = "matches-ground-truth"
	verdictFalsePositive      = "false-positive"
	// rationaleMin is the floor every verdict's rationale clears, the same 30
	// characters a triage note needs.
	rationaleMin = 30
	// quoteMin is the shortest backticked span that counts as quoting code.
	quoteMin                  = 3
	adjudicationSchemaVersion = "1"
)

// Adjudication is one record's effect on a run's figures, published beside the
// strict ones. Every count reclassifies a finding the run already reported, so
// the denominators are the published strict ones and nothing is recomputed.
type Adjudication struct { //nolint:govet // field order is the published property order
	KeywordMisses        []KeywordMiss `json:"keyword_misses"`
	Record               string        `json:"record"`
	SHA256               string        `json:"sha256"`
	RunID                string        `json:"run_id"`
	Entries              int           `json:"entries"`
	RealUnlisted         int           `json:"real_unlisted"`
	MatchesGroundTruth   int           `json:"matches_ground_truth"`
	FalsePositives       int           `json:"false_positives"`
	PrecisionAdjudicated *float64      `json:"precision_adjudicated"`
	RecallAdjudicated    *float64      `json:"recall_adjudicated"`
}

// KeywordMiss is one finding adjudicated as the ground-truth defect the case's
// keywords missed: the per-case signal to widen that case's vocabulary.
type KeywordMiss struct {
	Case    string `json:"case"`
	Sig     string `json:"sig"`
	Path    string `json:"path"`
	BugPath string `json:"bug_path"`
	Sample  int    `json:"sample"`
	Line    int    `json:"line"`
	Bug     int    `json:"bug"`
	BugLine int    `json:"bug_line"`
}

// adjudicationRecord is the versioned per-run file an adjudicator writes.
type adjudicationRecord struct {
	SchemaVersion string              `json:"schema_version"`
	RunID         string              `json:"run_id"`
	CreatedAt     string              `json:"created_at,omitempty"`
	Entries       []adjudicationEntry `json:"entries"`
}

// adjudicationEntry names one unmatched finding and the verdict on it.
type adjudicationEntry struct {
	Bug         *int   `json:"bug,omitempty"`
	Case        string `json:"case"`
	Sig         string `json:"sig"`
	Verdict     string `json:"verdict"`
	Adjudicator string `json:"adjudicator"`
	Rationale   string `json:"rationale"`
	Sample      int    `json:"sample"`
}

// gradedSample is what adjudication reads from one reported sample: its graded
// findings, the bugs the run matched, and the ground-truth windows.
type gradedSample struct {
	outcomes  map[string]gradedFinding
	bugs      []GroundTruthBug
	hits      []int
	tolerance int
}

// calibrationView is the part of one sample's calibrate block adjudication
// reads. Every field is the wire spelling the corpus verbs publish.
type calibrationView struct {
	Outcomes  []calibrateOutcome `json:"outcomes"`
	Hits      []int              `json:"sampling_hits"`
	Tolerance int                `json:"line_tolerance"`
}

// gradedFinding is one reported finding's strict grade: where it points and
// whether the keywords already named its bug.
type gradedFinding struct {
	path         string
	line         int
	truePositive bool
}

// calibrateOutcome is the calibrate block's per-finding grade.
type calibrateOutcome struct {
	Line   *int   `json:"line"`
	Sig    string `json:"sig"`
	Path   string `json:"path"`
	Result string `json:"result"`
}

// loadAdjudication reads one versioned record and reclassifies the run's
// unmatched findings against it. Strict figures are read, never rewritten.
func loadAdjudication(path string, rep *Report) (*Adjudication, error) {
	raw, err := jq.CappedOperatorFile(path, maxPublicationBytes)
	if err != nil {
		return nil, adjudicationRefusal("record %s: %s", path, err)
	}
	rec, err := decodeAdjudication(raw, path)
	if err != nil {
		return nil, err
	}
	if rec.RunID != rep.RunID {
		return nil, &core.RefusalError{Code: "run-mismatch", Message: fmt.Sprintf(
			"refusing adjudication: record %s is for run %q, the report is run %q", path, rec.RunID, rep.RunID)}
	}
	adj := &Adjudication{
		Record: path, SHA256: recordSHA256(raw), RunID: rec.RunID,
		Entries: len(rec.Entries), KeywordMisses: []KeywordMiss{},
	}
	index := indexSamples(rep)
	seen := map[string]bool{}
	seenBug := map[string]bool{}
	for i := range rec.Entries {
		if err := adjudicateEntry(adj, index, &rec.Entries[i], i, seen, seenBug); err != nil {
			return nil, err
		}
	}
	adj.setRates(rep)
	return adj, nil
}

// decodeAdjudication parses the record strictly: a mistyped key is a refusal,
// not a silently ignored verdict.
func decodeAdjudication(raw []byte, path string) (*adjudicationRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rec adjudicationRecord
	if err := dec.Decode(&rec); err != nil {
		return nil, adjudicationRefusal("record %s: %s", path, err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, adjudicationRefusal("record %s carries more than one JSON document", path)
	}
	if rec.SchemaVersion != adjudicationSchemaVersion {
		return nil, adjudicationRefusal("record %s schema_version %q: want %q", path, rec.SchemaVersion, adjudicationSchemaVersion)
	}
	return &rec, nil
}

// adjudicateEntry applies one entry, refusing an identity, rationale, or
// window the run does not support. The bookkeeping maps refuse a second
// verdict on the same finding and a second match on the same bug.
func adjudicateEntry(adj *Adjudication, index map[string]map[int]gradedSample, e *adjudicationEntry,
	n int, seen, seenBug map[string]bool,
) error {
	at := "entries[" + strconv.Itoa(n) + "]"
	if err := validateEntry(at, e); err != nil {
		return err
	}
	sample, ok := index[e.Case][e.Sample]
	if !ok {
		return adjudicationRefusal("%s: case %q sample %d is not a sample this run produced", at, e.Case, e.Sample)
	}
	finding, ok := sample.outcomes[e.Sig]
	if !ok {
		return &core.RefusalError{Code: "unknown-sig", Message: "refusing adjudication: " + fmt.Sprintf(
			"%s: sig %q is not a finding case %q sample %d produced", at, e.Sig, e.Case, e.Sample)}
	}
	if finding.truePositive {
		return adjudicationRefusal("%s: sig %q was graded a true positive; adjudication reclassifies unmatched findings only", at, e.Sig)
	}
	key := strings.Join([]string{e.Case, strconv.Itoa(e.Sample), e.Sig}, "\x00")
	if seen[key] {
		return adjudicationRefusal("%s: sig %q is adjudicated twice", at, e.Sig)
	}
	seen[key] = true
	return applyVerdict(adj, e, at, &sample, seenBug)
}

// applyVerdict counts one validated entry, turning a keyword-miss claim into
// the published per-case signal. validateEntry has already refused a verdict
// outside the three.
func applyVerdict(adj *Adjudication, e *adjudicationEntry, at string, sample *gradedSample, seenBug map[string]bool) error {
	switch e.Verdict {
	case verdictRealUnlisted:
		adj.RealUnlisted++
	case verdictFalsePositive:
		adj.FalsePositives++
	case verdictMatchesGroundTruth:
		miss, err := matchGroundTruth(at, e, sample, seenBug)
		if err != nil {
			return err
		}
		adj.MatchesGroundTruth++
		adj.KeywordMisses = append(adj.KeywordMisses, *miss)
	}
	return nil
}

// validateEntry checks the entry's own fields: its identity, verdict,
// adjudicator, and the rationale rule that verdict carries.
func validateEntry(at string, e *adjudicationEntry) error {
	switch {
	case strings.TrimSpace(e.Case) == "":
		return adjudicationRefusal("%s: case is required", at)
	case e.Sample < 1:
		return adjudicationRefusal("%s: sample must be 1 or greater, got %d", at, e.Sample)
	case strings.TrimSpace(e.Sig) == "":
		return adjudicationRefusal("%s: sig is required", at)
	case strings.TrimSpace(e.Adjudicator) == "":
		return adjudicationRefusal("%s: adjudicator is required: every verdict names who made it", at)
	}
	return validateVerdict(at, e)
}

// validateVerdict checks the verdict and the rationale it demands: all three
// clear the floor, and the two that raise a figure must quote the code.
func validateVerdict(at string, e *adjudicationEntry) error {
	switch e.Verdict {
	case verdictFalsePositive, verdictRealUnlisted:
		if e.Bug != nil {
			return adjudicationRefusal("%s: bug %d is only for verdict %q", at, *e.Bug, verdictMatchesGroundTruth)
		}
	case verdictMatchesGroundTruth:
		if e.Bug == nil {
			return adjudicationRefusal("%s: verdict %q names the ground-truth bug index", at, verdictMatchesGroundTruth)
		}
	default:
		return adjudicationRefusal("%s: verdict %q is not one of %s, %s, %s",
			at, e.Verdict, verdictRealUnlisted, verdictMatchesGroundTruth, verdictFalsePositive)
	}
	return validateRationale(at, e)
}

// validateRationale applies the rationale rule each verdict carries: all of
// them clear the floor, and the two that raise a figure quote the code.
func validateRationale(at string, e *adjudicationEntry) error {
	rationale := strings.TrimSpace(e.Rationale)
	if utf8.RuneCountInString(rationale) < rationaleMin {
		return &core.RefusalError{Code: "rationale-required", Message: "refusing adjudication: " + fmt.Sprintf(
			"%s: rationale must be at least %d characters, got %d", at, rationaleMin, utf8.RuneCountInString(rationale))}
	}
	if e.Verdict != verdictFalsePositive && !quotesCode(rationale) {
		return adjudicationRefusal("%s: verdict %q needs a code-citing rationale: quote the line in backticks", at, e.Verdict)
	}
	return nil
}

// matchGroundTruth refuses a keyword-miss claim the run contradicts: a bug the
// strict run already matched, a finding outside the bug's window, or a
// rationale that does not quote the ground-truth note.
func matchGroundTruth(at string, e *adjudicationEntry, sample *gradedSample, seenBug map[string]bool) (*KeywordMiss, error) {
	bug := *e.Bug
	if bug < 0 || bug >= len(sample.bugs) {
		return nil, adjudicationRefusal("%s: case %q has %d ground-truth bug(s) in this report; there is no bug %d",
			at, e.Case, len(sample.bugs), bug)
	}
	key := strings.Join([]string{e.Case, strconv.Itoa(e.Sample), strconv.Itoa(bug)}, "\x00")
	if seenBug[key] {
		return nil, adjudicationRefusal("%s: bug %d is adjudicated a keyword miss twice in one sample", at, bug)
	}
	if slices.Contains(sample.hits, bug) {
		return nil, adjudicationRefusal("%s: the strict run already matched bug %d; a second match counts one bug twice", at, bug)
	}
	finding := sample.outcomes[e.Sig]
	target := sample.bugs[bug]
	if !inWindow(finding, target, sample.tolerance) {
		return nil, adjudicationRefusal("%s: %s is outside bug %d's window (%s ±%d lines of %d)",
			at, locator(finding.path, finding.line), bug, target.Path, sample.tolerance, target.Line)
	}
	if target.Note != "" && !quotesNote(e.Rationale, target.Note) {
		return nil, adjudicationRefusal("%s: verdict %q must quote the ground-truth note: %q",
			at, verdictMatchesGroundTruth, target.Note)
	}
	seenBug[key] = true
	return &KeywordMiss{
		Case: e.Case, Sample: e.Sample, Sig: e.Sig,
		Path: finding.path, Line: finding.line,
		Bug: bug, BugPath: target.Path, BugLine: target.Line,
	}, nil
}

// setRates adds the reclassified findings to the strict run's own counts. The
// denominators are the published strict ones; nothing here re-reads a bundle.
func (adj *Adjudication) setRates(rep *Report) {
	var strict trendRates
	if json.Unmarshal(rep.TrendCorpus, &strict) != nil {
		strict = trendRates{}
	}
	if rate, ok := corpuscore.Rate(strict.Matched+adj.RealUnlisted+adj.MatchesGroundTruth, strict.Reported); ok {
		adj.PrecisionAdjudicated = &rate
	}
	if rate, ok := corpuscore.Rate(strict.Matched+adj.MatchesGroundTruth, strict.GroundTruth); ok {
		adj.RecallAdjudicated = &rate
	}
	slices.SortFunc(adj.KeywordMisses, func(a, b KeywordMiss) int {
		if n := strings.Compare(a.Case, b.Case); n != 0 {
			return n
		}
		if n := a.Sample - b.Sample; n != 0 {
			return n
		}
		return strings.Compare(a.Sig, b.Sig)
	})
}

// indexSamples reads every reported sample's graded outcomes, matched bug
// indices, tolerance, and ground-truth windows out of the publication.
func indexSamples(rep *Report) map[string]map[int]gradedSample {
	index := map[string]map[int]gradedSample{}
	for i := range rep.Bundles {
		b := &rep.Bundles[i]
		if index[b.Case] == nil {
			index[b.Case] = map[int]gradedSample{}
		}
		var cal calibrationView
		if json.Unmarshal(b.Calibrate, &cal) != nil {
			cal = calibrationView{}
		}
		index[b.Case][b.Run] = gradedSample{
			outcomes:  calibrateOutcomes(cal.Outcomes),
			bugs:      b.GroundTruthBugs,
			hits:      cal.Hits,
			tolerance: cal.Tolerance,
		}
	}
	return index
}

// calibrateOutcomes keys each reported finding by the sig the run published.
func calibrateOutcomes(list []calibrateOutcome) map[string]gradedFinding {
	out := make(map[string]gradedFinding, len(list))
	for _, o := range list {
		line := 0
		if o.Line != nil {
			line = *o.Line
		}
		out[o.Sig] = gradedFinding{path: o.Path, line: line, truePositive: o.Result == corpuscore.ResultTruePositive}
	}
	return out
}

// inWindow reports whether a finding sits on the bug's path inside its line
// window, counting every alternate anchor the case lists.
func inWindow(finding gradedFinding, bug GroundTruthBug, tolerance int) bool {
	if finding.line < 1 || finding.path != bug.Path {
		return false
	}
	for _, anchor := range slices.Concat([]int{bug.Line}, bug.AlsoLines) {
		if delta := finding.line - anchor; delta <= tolerance && -delta <= tolerance {
			return true
		}
	}
	return false
}

// quotesCode reports whether text carries a backticked span long enough to be
// a quoted line rather than decoration. Only visible runes count, so a span of
// zero-width spaces is no citation at all.
func quotesCode(text string) bool {
	spans := strings.Split(text, "`")
	for i := 1; i < len(spans); i += 2 {
		if utf8.RuneCountInString(strings.TrimSpace(visible(spans[i]))) >= quoteMin {
			return true
		}
	}
	return false
}

// quotesNote reports whether text quotes the ground-truth note, ignoring the
// invisibles, whitespace, and case a reflowed quotation changes.
func quotesNote(rationale, note string) bool {
	return strings.Contains(foldSpace(rationale), foldSpace(note))
}

// visible drops the runes that render as nothing: a format character (a
// zero-width space, joiner, or BOM) or anything unicode does not call graphic.
// Padding a quote with them must not reach a length floor, and splitting one
// with them must not slip past a containment check.
func visible(s string) string {
	var out strings.Builder
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) || !unicode.IsGraphic(r) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func foldSpace(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(visible(s)), " "))
}

func locator(path string, line int) string {
	if path == "" {
		return "?"
	}
	return path + ":" + strconv.Itoa(line)
}

func recordSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// adjudicationRefusal types every record problem as one code the operator can
// look up: the record is rewritten, not worked around.
func adjudicationRefusal(format string, args ...any) *core.RefusalError {
	return &core.RefusalError{Code: "bad-adjudication", Message: "refusing adjudication: " + fmt.Sprintf(format, args...)}
}
