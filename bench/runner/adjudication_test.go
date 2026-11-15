package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The adjudication fixture is a run the real runner produced from the seeded
// off-by-one case and a review with one keyword miss, two real findings outside
// the ground truth, and two plain false positives.
const (
	adjudicationFixture = "testdata/adjudication-run.json"
	fixtureRunID        = "2026-11-15-fixture-adjudication"

	// The ground-truth notes of bench/corpus/off-by-one/case.json, quoted here
	// because a matches-ground-truth rationale must quote the note.
	noteOffByOne = "loop bound is <= len(xs), so the last read runs one past the slice"

	// quotedRows is the code quote every verdict that raises a figure needs.
	quotedRows = "the finding says `The loop iterates one index too far`"
)

func fixtureReport(t *testing.T) *Report {
	t.Helper()
	rep := readJSON[Report](t, adjudicationFixture)
	return &rep
}

// fixtureSig returns the sig the run published for the finding on line.
func fixtureSig(t *testing.T, rep *Report, line int) string {
	t.Helper()
	var cal struct {
		Outcomes []struct {
			Line *int   `json:"line"`
			Sig  string `json:"sig"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal(rep.Bundles[0].Calibrate, &cal); err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	for _, o := range cal.Outcomes {
		if o.Line != nil && *o.Line == line {
			return o.Sig
		}
	}
	t.Fatalf("no graded finding on line %d (outcomes %+v)", line, cal.Outcomes)
	return ""
}

// writeRecord writes one adjudication record and returns its path.
func writeRecord(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "adjudication.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}
	return path
}

// recordJSON composes a versioned record from literal entries.
func recordJSON(runID string, entries ...string) string {
	return fmt.Sprintf(`{"schema_version":"1","run_id":%q,"entries":[%s]}`, runID, strings.Join(entries, ","))
}

// entryJSON is one literal record entry.
func entryJSON(sig, verdict, rationale string, bug *int) string {
	bugField := ""
	if bug != nil {
		bugField = fmt.Sprintf(`,"bug":%d`, *bug)
	}
	return fmt.Sprintf(`{"case":"off-by-one","sample":1,"sig":%q,"verdict":%q,"adjudicator":"operator","rationale":%q%s}`,
		sig, verdict, rationale, bugField)
}

// adjudicationRecordBody is one record with a verdict of each kind applied to
// the off-by-one fixture: a keyword miss, a real finding outside the ground
// truth, and a confirmed false positive.
func adjudicationRecordBody(t *testing.T, miss, unlisted, confirmed string) string {
	t.Helper()
	return recordJSON(fixtureRunID,
		entryJSON(unlisted, verdictRealUnlisted, quotedRows+": the shadowing hides the outer slice at the call site", nil),
		entryJSON(miss, verdictMatchesGroundTruth,
			fmt.Sprintf("%s, and the ground-truth note reads %q", quotedRows, noteOffByOne), new(0)),
		entryJSON(confirmed, verdictFalsePositive, "the strict call stands: a caching idea, not a defect", nil),
	)
}

func TestAdjudicationReclassifiesBothFigures(t *testing.T) {
	rep := fixtureReport(t)
	miss := fixtureSig(t, rep, 42)
	path := writeRecord(t, t.TempDir(), adjudicationRecordBody(t, miss, fixtureSig(t, rep, 11), fixtureSig(t, rep, 28)))
	adj, err := loadAdjudication(path, rep)
	if err != nil {
		t.Fatalf("loadAdjudication: %v", err)
	}
	checkAdjudicatedCounts(t, adj)
	checkAdjudicatedRates(t, adj)
	checkStrictUntouched(t, rep)
	checkRecordIdentity(t, adj, path)
	want := KeywordMiss{Case: "off-by-one", Sample: 1, Sig: miss, Path: "pagination.go", Line: 42, Bug: 0, BugPath: "pagination.go", BugLine: 42}
	if len(adj.KeywordMisses) != 1 || adj.KeywordMisses[0] != want {
		t.Errorf("keyword misses = %+v, want exactly %+v", adj.KeywordMisses, want)
	}
	if rep.Adjudication != nil {
		t.Error("the report was mutated in place; the strict publication must stay untouched")
	}
}

// checkAdjudicatedCounts pins the tally each verdict contributes.
func checkAdjudicatedCounts(t *testing.T, adj *Adjudication) {
	t.Helper()
	if adj.Entries != 3 || adj.RealUnlisted != 1 || adj.MatchesGroundTruth != 1 || adj.FalsePositives != 1 {
		t.Errorf("counts = %d entries / %d real-unlisted / %d matches-ground-truth / %d false-positive, want 3/1/1/1",
			adj.Entries, adj.RealUnlisted, adj.MatchesGroundTruth, adj.FalsePositives)
	}
}

// checkAdjudicatedRates pins the arithmetic: one real finding over five
// reported, and the keyword miss as one more bug over two.
func checkAdjudicatedRates(t *testing.T, adj *Adjudication) {
	t.Helper()
	if adj.PrecisionAdjudicated == nil || *adj.PrecisionAdjudicated != 0.6 {
		t.Errorf("precision_adjudicated = %v, want 0.6 (strict 1/5 plus two real findings over 5 reported)", adj.PrecisionAdjudicated)
	}
	if adj.RecallAdjudicated == nil || *adj.RecallAdjudicated != 1 {
		t.Errorf("recall_adjudicated = %v, want 1.0 (strict 1/2 plus the keyword miss over 2 bugs)", adj.RecallAdjudicated)
	}
}

// checkStrictUntouched pins the figures adjudication may not move.
func checkStrictUntouched(t *testing.T, rep *Report) {
	t.Helper()
	var strict trendRates
	if err := json.Unmarshal(rep.TrendCorpus, &strict); err != nil {
		t.Fatalf("trend_corpus: %v", err)
	}
	if strict.Precision == nil || *strict.Precision != 0.2 || strict.Recall == nil || *strict.Recall != 0.5 {
		t.Errorf("strict precision/recall = %v/%v, want 0.2/0.5: adjudication never touches the strict figures", strict.Precision, strict.Recall)
	}
}

// checkRecordIdentity pins the record's published path, run, and digest.
func checkRecordIdentity(t *testing.T, adj *Adjudication, path string) {
	t.Helper()
	if adj.Record != path || adj.RunID != fixtureRunID {
		t.Errorf("record/run = %q/%q, want %q/%q", adj.Record, adj.RunID, path, fixtureRunID)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if want := hex.EncodeToString(sum[:]); adj.SHA256 != want {
		t.Errorf("sha256 = %q, want the record's own digest %q", adj.SHA256, want)
	}
}

// refusalCase is one record the run must refuse, with the code and message
// fragment the operator needs to fix it.
type refusalCase struct {
	name   string
	record string
	code   string
	want   string
}

func TestAdjudicationRefusals(t *testing.T) {
	rep := fixtureReport(t)
	for _, tc := range adjudicationRefusalCases(t, rep) {
		t.Run(tc.name, func(t *testing.T) {
			checkRefusal(t, tc, rep)
		})
	}
}

// checkRefusal pins one record's typed refusal and the fix it names.
func checkRefusal(t *testing.T, tc refusalCase, rep *Report) {
	t.Helper()
	path := writeRecord(t, t.TempDir(), tc.record)
	adj, err := loadAdjudication(path, rep)
	if err == nil {
		t.Fatalf("loadAdjudication accepted the record: %+v", adj)
	}
	if want := tc.code + ": "; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("refusal = %q, want code %q", err.Error(), tc.code)
	}
	if !strings.Contains(err.Error(), tc.want) {
		t.Errorf("refusal = %q, want it to name %q", err.Error(), tc.want)
	}
}

// adjudicationRefusalCases builds one record per refusal the contract names.
func adjudicationRefusalCases(t *testing.T, rep *Report) []refusalCase {
	t.Helper()
	miss := fixtureSig(t, rep, 42)
	outer := fixtureSig(t, rep, 43)
	unlisted := fixtureSig(t, rep, 11)
	matched := fixtureSig(t, rep, 51)
	missNote := fmt.Sprintf("%s, and the ground-truth note reads %q", quotedRows, noteOffByOne)
	long := "the strict call stands: a caching idea, not a defect"
	return []refusalCase{
		{"run mismatch", recordJSON("2025-01-01-elsewhere", entryJSON(unlisted, verdictRealUnlisted, quotedRows, nil)), "run-mismatch", "is for run"},
		{"schema version", strings.Replace(recordJSON(fixtureRunID, entryJSON(unlisted, verdictRealUnlisted, quotedRows, nil)), `"1"`, `"2"`, 1), "bad-adjudication", "schema_version"},
		{"not json", `{"schema_version":`, "bad-adjudication", "record"},
		{"unknown field", recordJSON(fixtureRunID, `{"case":"off-by-one","sample":1,"sig":"x","verdict":"false-positive","adjudicator":"operator","rationale":"`+long+`","note":"typo"}`), "bad-adjudication", "unknown field"},
		{"unknown finding", recordJSON(fixtureRunID, entryJSON("deadbeefdeadbeef", verdictRealUnlisted, quotedRows, nil)), "unknown-sig", "is not a finding"},
		{"unknown sample", strings.Replace(recordJSON(fixtureRunID, entryJSON(unlisted, verdictRealUnlisted, quotedRows, nil)), `"sample":1`, `"sample":2`, 1), "bad-adjudication", "is not a sample this run produced"},
		{"unknown verdict", recordJSON(fixtureRunID, entryJSON(unlisted, "real-unlisted-ish", quotedRows, nil)), "bad-adjudication", "is not one of"},
		{"no adjudicator", recordJSON(fixtureRunID, `{"case":"off-by-one","sample":1,"sig":"`+unlisted+`","verdict":"false-positive","rationale":"`+long+`"}`), "bad-adjudication", "adjudicator is required"},
		{"short rationale", recordJSON(fixtureRunID, entryJSON(unlisted, verdictRealUnlisted, "shadowing", nil)), "rationale-required", "at least 30 characters"},
		{"uncited rationale", recordJSON(fixtureRunID, entryJSON(unlisted, verdictRealUnlisted, long, nil)), "bad-adjudication", "code-citing rationale"},
		{"invisible citation", recordJSON(fixtureRunID, entryJSON(unlisted, verdictRealUnlisted, long+" `\u200b\u200b\u200b`", nil)), "bad-adjudication", "code-citing rationale"},
		{"padded citation", recordJSON(fixtureRunID, entryJSON(unlisted, verdictRealUnlisted, long+" `ab\u200b\u200b`", nil)), "bad-adjudication", "code-citing rationale"},
		{"true positive", recordJSON(fixtureRunID, entryJSON(matched, verdictRealUnlisted, quotedRows, nil)), "bad-adjudication", "graded a true positive"},
		{"bug on a false positive", recordJSON(fixtureRunID, entryJSON(unlisted, verdictFalsePositive, long, new(0))), "bad-adjudication", "only for verdict"},
		{"missing bug index", recordJSON(fixtureRunID, entryJSON(miss, verdictMatchesGroundTruth, missNote, nil)), "bad-adjudication", "names the ground-truth bug index"},
		{"bug out of range", recordJSON(fixtureRunID, entryJSON(miss, verdictMatchesGroundTruth, missNote, new(7))), "bad-adjudication", "there is no bug 7"},
		{"bug already matched", recordJSON(fixtureRunID, entryJSON(outer, verdictMatchesGroundTruth, missNote, new(1))), "bad-adjudication", "already matched bug 1"},
		{"outside the window", recordJSON(fixtureRunID, entryJSON(unlisted, verdictMatchesGroundTruth, missNote, new(0))), "bad-adjudication", "outside bug 0's window"},
		{"note not quoted", recordJSON(fixtureRunID, entryJSON(miss, verdictMatchesGroundTruth, quotedRows, new(0))), "bad-adjudication", "must quote the ground-truth note"},
		{"finding twice", recordJSON(fixtureRunID,
			entryJSON(unlisted, verdictFalsePositive, long, nil),
			entryJSON(unlisted, verdictRealUnlisted, quotedRows, nil)), "bad-adjudication", "adjudicated twice"},
		{"bug twice", recordJSON(fixtureRunID,
			entryJSON(miss, verdictMatchesGroundTruth, missNote, new(0)),
			entryJSON(outer, verdictMatchesGroundTruth, missNote, new(0))), "bad-adjudication", "twice in one sample"},
	}
}

// TestAdjudicationRendersThroughTheFlagPath drives the recompute the operator
// runs: -render with a record, and -out for the adjudicated publication.
func TestAdjudicationRendersThroughTheFlagPath(t *testing.T) {
	rep := fixtureReport(t)
	dir := t.TempDir()
	record := writeRecord(t, dir, adjudicationRecordBody(t, fixtureSig(t, rep, 42), fixtureSig(t, rep, 11), fixtureSig(t, rep, 28)))
	out := filepath.Join(dir, "adjudicated.json")
	cfg, err := parseFlags([]string{"-render", adjudicationFixture, "-adjudication", record, "-out", out})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	var md bytes.Buffer
	if err := renderPublication(&cfg, &md); err != nil {
		t.Fatalf("renderPublication: %v", err)
	}
	checkAdjudicatedMarkdown(t, md.String(), fixtureSig(t, rep, 42))
	checkAdjudicatedPublication(t, out, record)
	checkWrittenMarkdown(t, out)
}

// checkAdjudicatedMarkdown pins the figures the render prints side by side,
// the counts, and the keyword-miss row.
func checkAdjudicatedMarkdown(t *testing.T, md, miss string) {
	t.Helper()
	for _, want := range []string{
		"| precision | 0.2000 | 0.6000 |",
		"| recall | 0.5000 | 1.0000 |",
		"3 entries: 1 real-unlisted, 1 matches-ground-truth, 1 false-positive",
		"### Keyword misses",
		"| off-by-one | 1 | " + miss + " pagination.go:42 | 0 pagination.go:42 |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown omits %q\n%s", want, md)
		}
	}
}

// checkAdjudicatedPublication pins the written JSON: strict kept, adjudicated
// added, and the record named.
func checkAdjudicatedPublication(t *testing.T, out, record string) {
	t.Helper()
	written := readJSON[Report](t, out)
	var strict trendRates
	if err := json.Unmarshal(written.TrendCorpus, &strict); err != nil {
		t.Fatalf("trend_corpus: %v", err)
	}
	if strict.Precision == nil || *strict.Precision != 0.2 || strict.Recall == nil || *strict.Recall != 0.5 {
		t.Errorf("written strict precision/recall = %v/%v, want 0.2/0.5", strict.Precision, strict.Recall)
	}
	if written.Adjudication == nil || written.Adjudication.PrecisionAdjudicated == nil ||
		*written.Adjudication.PrecisionAdjudicated != 0.6 || written.Adjudication.RecallAdjudicated == nil ||
		*written.Adjudication.RecallAdjudicated != 1 {
		t.Fatalf("written adjudication = %+v, want precision 0.6 and recall 1.0", written.Adjudication)
	}
	if written.Adjudication.Record != record {
		t.Errorf("written record = %q, want the record path %q", written.Adjudication.Record, record)
	}
}

func checkWrittenMarkdown(t *testing.T, out string) {
	t.Helper()
	raw, err := os.ReadFile(strings.TrimSuffix(out, ".json") + ".md")
	if err != nil {
		t.Fatalf("read the written Markdown: %v", err)
	}
	for _, want := range []string{"| precision | 0.2000 | 0.6000 |", "| recall | 0.5000 | 1.0000 |"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("written Markdown omits %q", want)
		}
	}
}

// TestAdjudicationAcceptsPaddedCodeQuote proves the visible-rune rule refuses
// only invisible citations: real code carrying zero-width padding still cites.
func TestAdjudicationAcceptsPaddedCodeQuote(t *testing.T) {
	rep := fixtureReport(t)
	entry := entryJSON(fixtureSig(t, rep, 11), verdictRealUnlisted,
		"the finding says `\u200brows\u200b` at the slice bound", nil)
	adj, err := loadAdjudication(writeRecord(t, t.TempDir(), recordJSON(fixtureRunID, entry)), rep)
	if err != nil {
		t.Fatalf("loadAdjudication refused real code with zero-width padding: %v", err)
	}
	if adj.RealUnlisted != 1 {
		t.Errorf("real-unlisted count = %d, want the padded code quote accepted", adj.RealUnlisted)
	}
}

// TestAdjudicationAcceptsPaddedNoteQuote proves the note comparison drops the
// invisibles it folds away: a quotation carrying one still quotes the note.
func TestAdjudicationAcceptsPaddedNoteQuote(t *testing.T) {
	rep := fixtureReport(t)
	note := strings.Replace(noteOffByOne, "one past", "one \u200bpast", 1)
	entry := entryJSON(fixtureSig(t, rep, 42), verdictMatchesGroundTruth,
		"the ground-truth note reads `"+note+"`", new(0))
	adj, err := loadAdjudication(writeRecord(t, t.TempDir(), recordJSON(fixtureRunID, entry)), rep)
	if err != nil {
		t.Fatalf("loadAdjudication refused a note quote carrying an invisible: %v", err)
	}
	if adj.MatchesGroundTruth != 1 {
		t.Errorf("matches-ground-truth count = %d, want the padded note quote accepted", adj.MatchesGroundTruth)
	}
}

// TestAdjudicationAbsentWithoutRecord proves a report never borrows the strict
// figure for an adjudicated one: with no record the block and the section are
// simply not there.
func TestAdjudicationAbsentWithoutRecord(t *testing.T) {
	raw, err := os.ReadFile(adjudicationFixture)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if _, ok := keys["adjudication"]; ok {
		t.Error("the run wrote an adjudication block with no record: the figures must stay absent")
	}
	cfg, err := parseFlags([]string{"-render", adjudicationFixture})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	var md bytes.Buffer
	if err := renderPublication(&cfg, &md); err != nil {
		t.Fatalf("renderPublication: %v", err)
	}
	if strings.Contains(md.String(), "## Adjudication") {
		t.Error("Markdown rendered an adjudication section with no record")
	}
}

func TestAdjudicationFlagsNeedTheRenderPath(t *testing.T) {
	for _, args := range [][]string{
		{"-adjudication", "record.json"},
		{"-render", "report.json", "-out", "out.json"},
		{"-render", "report.json", "-adjudication", "record.json", "-out", "report.json"},
		{"-render", "report.json", "-adjudication", "report.json"},
	} {
		if _, err := parseFlags(args); err == nil {
			t.Errorf("parseFlags(%v) accepted flags that cannot do anything", args)
		}
	}
}
