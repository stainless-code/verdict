package recordcore_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

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
