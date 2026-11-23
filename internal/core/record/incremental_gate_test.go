package recordcore_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/testkit"
)

// incrementalFullDiff changes notes.txt (the prior range) and delta.txt
// (the delta), each 1-3, so the full snapshot holds six changed lines.
const incrementalFullDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n" +
	"diff --git a/delta.txt b/delta.txt\nindex 3333333..4444444 100644\n" +
	"--- a/delta.txt\n+++ b/delta.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// incrementalDeltaDiff touches only delta.txt: the marker fetch writes for
// a --since/--base-sha bundle. The delta lines live in the full snapshot.
const incrementalDeltaDiff = "diff --git a/delta.txt b/delta.txt\n" +
	"@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// incrementalPrior records the prior round: the whole notes.txt file.
const incrementalPrior = `[{"file":"notes.txt","ranges":"1-3"}]`

// incrementalReview is one finding on the delta plus one receipt per
// changed hunk; extra carries coverage[] (delta-only, empty, or absent).
func incrementalReview(extra string) string {
	return `{"schema_version":"1","body":"> [!IMPORTANT]\n> Incremental gate fixture body with enough length.","approved":false,` +
		extra + `"receipts":[{"hunk":"notes.txt\tNEW\t1\t3","quote":"new line"},` +
		`{"hunk":"delta.txt\tNEW\t1\t3","quote":"new line"}],` +
		`"comments":[{"path":"delta.txt","line":2,"side":"RIGHT",` +
		`"body":"Cited finding here with enough substance.","rigor":"walked","severity":"important"}]}`
}

// incrementalBundle writes the two-file fixture with the delta marker and,
// when prior is non-empty, the recorded prior. It mirrors coverageBundle
// so the merge and record helpers read the same files they always do.
func incrementalBundle(t *testing.T, review, prior string) {
	t.Helper()
	t.Chdir(t.TempDir())
	dir := ".verdict/state/" + coverageSlug
	testkit.WriteFile(t, dir+"/source.json", `{"head_sha":"`+coverageHead+
		`","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`)
	testkit.WriteFile(t, dir+"/full.diff", incrementalFullDiff)
	testkit.WriteFile(t, dir+"/hunks.tsv", hunks.Format(hunks.Parse(incrementalFullDiff)))
	testkit.WriteFile(t, dir+"/incremental.diff", incrementalDeltaDiff)
	testkit.WriteFile(t, dir+"/review.json", review)
	if prior != "" {
		testkit.WriteFile(t, dir+"/prior-coverage.json", prior)
	}
	seedControls(t, dir)
}

// overlapFullDiff is the single-file overlap snapshot: overlap.txt 1-10,
// ten changed lines under the full snapshot.
const overlapFullDiff = "diff --git a/overlap.txt b/overlap.txt\nindex 1111111..2222222 100644\n" +
	"--- a/overlap.txt\n+++ b/overlap.txt\n@@ -1,10 +1,10 @@\n top\n-old line\n+new line\n bottom\n"

// overlapDeltaDiff re-modifies only overlap.txt 5-7. Its OLD side is 3-7,
// so crediting 7 carried lines proves only the NEW side invalidates.
const overlapDeltaDiff = "diff --git a/overlap.txt b/overlap.txt\n" +
	"@@ -3,5 +5,3 @@\n top\n-old line\n+new line\n bottom\n"

// overlapReview is one finding on the re-modified range plus the single
// receipt for the 1-10 hunk; extra carries coverage[].
func overlapReview(extra string) string {
	return `{"schema_version":"1","body":"> [!IMPORTANT]\n> Overlap gate fixture body with enough length.","approved":false,` +
		extra + `"receipts":[{"hunk":"overlap.txt\tNEW\t1\t10","quote":"new line"}],` +
		`"comments":[{"path":"overlap.txt","line":6,"side":"RIGHT",` +
		`"body":"Cited finding here with enough substance.","rigor":"walked","severity":"important"}]}`
}

// overlapBundle writes the single-file overlap fixture with the given delta
// marker and recorded prior (possibly empty).
func overlapBundle(t *testing.T, review, delta, prior string) {
	t.Helper()
	t.Chdir(t.TempDir())
	dir := ".verdict/state/" + coverageSlug
	testkit.WriteFile(t, dir+"/source.json", `{"head_sha":"`+coverageHead+
		`","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`)
	testkit.WriteFile(t, dir+"/full.diff", overlapFullDiff)
	testkit.WriteFile(t, dir+"/hunks.tsv", hunks.Format(hunks.Parse(overlapFullDiff)))
	testkit.WriteFile(t, dir+"/incremental.diff", delta)
	testkit.WriteFile(t, dir+"/review.json", review)
	if prior != "" {
		testkit.WriteFile(t, dir+"/prior-coverage.json", prior)
	}
	seedControls(t, dir)
}

// TestMergeIncrementalGatePassesWithPrior proves the STA-373 happy path at
// the merge gate: the agent attests only the delta, the recorded prior
// covers notes.txt, and --require-coverage passes naming the credit.
func TestMergeIncrementalGatePassesWithPrior(t *testing.T) {
	incrementalBundle(t, incrementalReview(`"coverage":[{"file":"delta.txt","ranges":"all"}],`), incrementalPrior)
	_, warns, err := mergeCoverage(t, true)
	if err != nil {
		t.Fatalf("merge refused a delta-only attestation with recorded prior: %v", err)
	}
	got := coverageWarnings(warns)
	if len(got) != 1 || !strings.Contains(got[0], "carried 3 line(s)") {
		t.Errorf("coverage warnings = %v, want one naming the 3 carried prior lines", got)
	}
}

// TestMergeIncrementalGateRefusesUnattestedDelta proves leg (a) at the
// merge gate: with the prior covering only notes.txt, an empty attestation
// leaves the delta gap and the gate refuses naming it.
func TestMergeIncrementalGateRefusesUnattestedDelta(t *testing.T) {
	incrementalBundle(t, incrementalReview(`"coverage":[],`), incrementalPrior)
	_, _, err := mergeCoverage(t, true)
	ref := coverageRefusal(t, err)
	if !strings.Contains(ref.Message, "delta.txt:1-3") {
		t.Errorf("message = %q, want it to name the unattested delta", ref.Message)
	}
	if !strings.Contains(ref.Message, "50") {
		t.Errorf("message = %q, want 50%% (3 credited of 6)", ref.Message)
	}
}

// TestMergeIncrementalGateFailsClosedWithoutPrior proves an unrecorded
// prior gets no credit at the merge gate: the delta-only attestation that
// passes above now refuses, naming the uncredited notes.txt gap.
func TestMergeIncrementalGateFailsClosedWithoutPrior(t *testing.T) {
	incrementalBundle(t, incrementalReview(`"coverage":[{"file":"delta.txt","ranges":"all"}],`), "")
	_, _, err := mergeCoverage(t, true)
	ref := coverageRefusal(t, err)
	if !strings.Contains(ref.Message, "notes.txt:1-3") {
		t.Errorf("message = %q, want it to name the uncredited prior range", ref.Message)
	}
}

// TestRecordIncrementalPersistsPrior proves the record seam: a successful
// incremental record stores a 100% receipt and persists the attested union
// (prior notes plus current delta) as the next round's recorded credit,
// which a following delta-only merge then reads back without refusal.
func TestRecordIncrementalPersistsPrior(t *testing.T) {
	incrementalBundle(t, incrementalReview(`"coverage":[{"file":"delta.txt","ranges":"all"}],`), incrementalPrior)
	if ref := runCoverage(t, true, false); ref != nil {
		t.Fatalf("record refused: %v", ref)
	}
	record := decodeObject(t, ".verdict/state/"+coverageSlug+"/record.json")
	assertStoredCoverage(t, record, true,
		map[string]any{"percent": 100.0, "attested": 6.0, "total": 6.0, "gaps": 0.0})
	byFile := decodePrior(t)
	if len(byFile) != 2 {
		t.Fatalf("prior entries = %d (%v), want 2 (notes + delta union)", len(byFile), byFile)
	}
	if byFile["notes.txt"] != "1-3" || byFile["delta.txt"] != "1-3" {
		t.Errorf("prior = %v, want notes.txt:1-3 and delta.txt:1-3", byFile)
	}
	// The next round needs no new prior fixture: the persisted union is
	// the recorded credit it grades against.
	if _, _, err := mergeCoverage(t, true); err != nil {
		t.Fatalf("second merge refused on persisted prior: %v", err)
	}
}

// decodePrior reads the persisted recorded prior into a file-to-ranges map.
func decodePrior(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(".verdict/state/" + coverageSlug + "/prior-coverage.json")
	if err != nil {
		t.Fatalf("read prior-coverage.json: %v", err)
	}
	var entries []struct {
		File   string `json:"file"`
		Ranges string `json:"ranges"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode prior-coverage.json: %v\n%s", err, raw)
	}
	byFile := make(map[string]string, len(entries))
	for _, e := range entries {
		byFile[e.File] = e.Ranges
	}
	return byFile
}

// TestRecordIncrementalRefusalKeepsPrior proves a refused incremental run
// leaves the recorded prior untouched: the gate fires before any store
// write, so the credit earlier rounds recorded survives the refusal.
func TestRecordIncrementalRefusalKeepsPrior(t *testing.T) {
	incrementalBundle(t, incrementalReview(`"coverage":[],`), incrementalPrior)
	if ref := runCoverage(t, true, false); ref == nil || ref.Code != codeCoverageIncomplete {
		t.Fatalf("refusal = %v, want coverage-incomplete", ref)
	}
	after, err := os.ReadFile(".verdict/state/" + coverageSlug + "/prior-coverage.json")
	if err != nil {
		t.Fatalf("read prior after refusal: %v", err)
	}
	// Literal bytes of the fixture, not a re-encode: the refusal must not
	// have rewritten the file the next round will credit. Either exact
	// form passes — the fixture write may not append a newline.
	if string(after) != incrementalPrior && string(after) != incrementalPrior+"\n" {
		t.Errorf("prior after refusal = %q, want the fixture untouched", after)
	}
	if _, err := os.Stat(".verdict/state/" + coverageSlug + "/record.json"); !os.IsNotExist(err) {
		t.Error("a refused run wrote record.json")
	}
}

// TestMergeIncrementalFullOverlapRefuses proves the C1 fix at the merge
// gate: the prior attested exactly the re-modified 5-7, the delta
// re-modifies 5-7, and an empty attestation cannot claim that stale credit —
// --require-coverage refuses coverage-incomplete naming 0% over the whole
// file. Without the subtraction this would read 30% from the prior alone.
func TestMergeIncrementalFullOverlapRefuses(t *testing.T) {
	overlapBundle(t, overlapReview(`"coverage":[],`), overlapDeltaDiff, `[{"file":"overlap.txt","ranges":"5-7"}]`)
	_, warns, err := mergeCoverage(t, true)
	ref := coverageRefusal(t, err)
	if !strings.Contains(ref.Message, "0%") {
		t.Errorf("message = %q, want 0%% (nothing validly credited)", ref.Message)
	}
	if !strings.Contains(ref.Message, "overlap.txt:1-10") {
		t.Errorf("message = %q, want the whole-file gap", ref.Message)
	}
	if got := coverageWarnings(warns); len(got) != 0 {
		t.Errorf("coverage warnings = %v, want none (nothing carried over)", got)
	}
}

// TestMergeIncrementalPartialOverlapCarriesUnchanged proves the C1 fix's
// precision at the merge gate: prior 1-10 with the delta re-modifying only
// 5-7 carries 1-4,8-10 (7 lines) while 5-7 stays the gap under an empty
// attestation, and re-attesting 5-7 then passes --require-coverage naming
// the 7 carried lines.
func TestMergeIncrementalPartialOverlapCarriesUnchanged(t *testing.T) {
	overlapBundle(t, overlapReview(`"coverage":[],`), overlapDeltaDiff, `[{"file":"overlap.txt","ranges":"1-10"}]`)
	_, warns, err := mergeCoverage(t, true)
	ref := coverageRefusal(t, err)
	if !strings.Contains(ref.Message, "70%") {
		t.Errorf("message = %q, want 70%% (7 of 4,8-10 credited)", ref.Message)
	}
	if !strings.Contains(ref.Message, "overlap.txt:5-7") {
		t.Errorf("message = %q, want the gap to be exactly 5-7", ref.Message)
	}
	if got := coverageWarnings(warns); len(got) != 1 || !strings.Contains(got[0], "carried 7 line(s)") {
		t.Errorf("coverage warnings = %v, want one naming the 7 carried prior lines", got)
	}
	overlapBundle(t, overlapReview(`"coverage":[{"file":"overlap.txt","ranges":"5-7"}],`), overlapDeltaDiff, `[{"file":"overlap.txt","ranges":"1-10"}]`)
	_, warns, err = mergeCoverage(t, true)
	if err != nil {
		t.Fatalf("merge refused a re-attested delta with carried prior: %v", err)
	}
	if got := coverageWarnings(warns); len(got) != 1 || !strings.Contains(got[0], "carried 7 line(s)") {
		t.Errorf("coverage warnings = %v, want one naming the 7 carried prior lines", got)
	}
}

// TestRecordPersistPriorFailureWarns proves the A5 contract: record.json is
// already durable when the prior write fails, so the run succeeds with a
// warning naming the failure instead of refusing as record-failed. The
// prior path is a directory so the atomic write cannot land; the full
// attestation needs no prior credit, so the coverage gate itself passes.
func TestRecordPersistPriorFailureWarns(t *testing.T) {
	incrementalBundle(t,
		incrementalReview(`"coverage":[{"file":"notes.txt","ranges":"all"},{"file":"delta.txt","ranges":"all"}],`), "")
	if err := os.Mkdir(".verdict/state/"+coverageSlug+"/prior-coverage.json", 0o750); err != nil {
		t.Fatalf("mkdir prior path: %v", err)
	}
	c := &testkit.Collector{}
	req := recordRequest(t, c, coverageSlug)
	req.Flags.RequireCoverage = true
	resp, ref := recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v, want success with a prior-write warning", ref)
	}
	want := ".verdict/state/" + coverageSlug + "/prior-coverage.json (record already written)"
	found := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one naming the failed prior write", resp.Warnings)
	}
	if _, err := os.Stat(".verdict/state/" + coverageSlug + "/record.json"); err != nil {
		t.Errorf("record.json missing after a committed record: %v", err)
	}
}

// TestMergeIncrementalSuffixOnWarnAndBodyLine pins the A1 human suffix on
// the record surface: an incomplete delta round that carried prior credit
// names `(incremental)` on both its summary warning and its body line.
// Full-diff absence is pinned exactly by
// TestMergeCoveragePartialWarnsOnceAndAddsOneLine.
func TestMergeIncrementalSuffixOnWarnAndBodyLine(t *testing.T) {
	// Prior credits 3 of 6, the delta stays unattested: 50% with a suffix.
	incrementalBundle(t, incrementalReview(`"coverage":[],`), incrementalPrior)
	body, warns, err := mergeCoverage(t, false)
	if err != nil {
		t.Fatalf("default posture refused a carried partial: %v", err)
	}
	got := coverageWarnings(warns)
	wantCarried := "coverage: incremental — prior attestation carried 3 line(s) over from the recorded prior round"
	wantWarn := "coverage: 50% of full hunk spans, both sides and context attested (3/6; 1 file(s) with gaps) (incremental)"
	if len(got) != 2 || got[0] != wantCarried || got[1] != wantWarn {
		t.Errorf("coverage warnings = %v, want [%q %q]", got, wantCarried, wantWarn)
	}
	lines := coverageBodyLines(body)
	wantLine := "> ⚠️ coverage: 50% of changed lines attested (unattested: delta.txt:1-3) (incremental)"
	if len(lines) != 1 || lines[0] != wantLine {
		t.Errorf("coverage body lines = %v, want exactly [%q]", lines, wantLine)
	}
}

// TestMergeIncrementalNoSuffixWithoutPriorCredit pins the suffix guard on
// the record surface: the same delta marker with no recorded prior carries
// nothing, so the identical posture warns without `(incremental)` even
// though the bundle is incremental — the guard is the credit, not the
// marker.
func TestMergeIncrementalNoSuffixWithoutPriorCredit(t *testing.T) {
	incrementalBundle(t, incrementalReview(`"coverage":[],`), "")
	body, warns, err := mergeCoverage(t, false)
	if err != nil {
		t.Fatalf("default posture refused an uncredited delta: %v", err)
	}
	got := coverageWarnings(warns)
	wantNoPrior := "coverage: incremental bundle has no recorded prior coverage — grading against full hunks"
	wantWarn := "coverage: 0% of full hunk spans, both sides and context attested (0/6; 2 file(s) with gaps)"
	if len(got) != 2 || got[0] != wantNoPrior || got[1] != wantWarn {
		t.Errorf("coverage warnings = %v, want [%q %q]", got, wantNoPrior, wantWarn)
	}
	lines := coverageBodyLines(body)
	wantLine := "> ⚠️ coverage: 0% of changed lines attested (unattested: delta.txt:1-3, notes.txt:1-3)"
	if len(lines) != 1 || lines[0] != wantLine {
		t.Errorf("coverage body lines = %v, want exactly [%q]", lines, wantLine)
	}
}
