package coverage

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

func mustReview(t *testing.T, body string) *jq.Obj {
	t.Helper()
	doc, err := jq.DecodeOrdered([]byte(body))
	if err != nil {
		t.Fatalf("decode review: %v", err)
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		t.Fatalf("review decoded to %T, want *jq.Obj", doc)
	}
	return obj
}

func TestParseRanges(t *testing.T) {
	good := map[string]string{
		"42":          "42",
		"42-48":       "42-48",
		"1-2, 4-5":    "1-2,4-5",
		" 10 - 12 ":   "10-12",
		"5-10,6-8":    "5-10",
		"7-9,10-12":   "7-12",
		"3,1-2":       "1-3",
		"100-100":     "100",
		"1-1,2-2,4-4": "1-2,4",
	}
	for in, want := range good {
		got, err := ParseRanges(in)
		if err != nil {
			t.Errorf("ParseRanges(%q) error: %v", in, err)
			continue
		}
		if Format(got) != want {
			t.Errorf("ParseRanges(%q) = %q, want %q", in, Format(got), want)
		}
	}
	bad := []string{"", " ", "0", "0-5", "9-3", "a-b", "1-", "-5", "1,,2", "1-2-3", "1-2,"}
	for _, in := range bad {
		if _, err := ParseRanges(in); err == nil {
			t.Errorf("ParseRanges(%q) accepted a malformed range", in)
		}
	}
}

// fixtureRows is the spec's worked example plus a generated file and a file the
// agent never mentions.
func fixtureRows() []hunks.Hunk {
	return []hunks.Hunk{
		{File: "src/db.ts", Side: "NEW", Start: 10, End: 20},
		{File: "src/db.ts", Side: "OLD", Start: 40, End: 50},
		{File: "src/app.ts", Side: "NEW", Start: 90, End: 100},
		{File: "dist/app.min.js", Side: "NEW", Start: 1, End: 3},
	}
}

func notGenerated(string) bool { return false }

var generatedMin = func(p string) bool { return strings.Contains(p, ".min.") }

func TestWholeFile(t *testing.T) {
	for _, body := range []string{
		`{"coverage":[{"file":"src/db.ts","ranges":"all"}]}`,
		`{"coverage":[{"file":"src/db.ts"}]}`,
	} {
		review := mustReview(t, body)
		got, warns, ref := Evaluate(writeBundle(t, fixtureRows()), review, generatedMin)
		if ref != nil {
			t.Fatalf("Evaluate refused: %v", ref)
		}
		if len(warns) != 0 {
			t.Fatalf("warnings = %v, want none", warns)
		}
		// db.ts is whole; app.ts is untouched, so 22 of 33 lines.
		if got.Attested != 22 || got.Total != 33 {
			t.Fatalf("attested/total = %d/%d, want 22/33", got.Attested, got.Total)
		}
		if pct(got) != "66" {
			t.Fatalf("percent = %v, want 66", pct(got))
		}
	}
}

func TestPartialOldAndNew(t *testing.T) {
	review := mustReview(t, `{"coverage":[{"file":"src/db.ts","ranges":"10-15,40-45"}]}`)
	got, _, ref := Evaluate(writeBundle(t, fixtureRows()), review, generatedMin)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	// db.ts is 22 lines, 12 attested; app.ts contributes 11 unattested lines.
	if got.Attested != 12 || got.Total != 33 {
		t.Fatalf("attested/total = %d/%d, want 12/33", got.Attested, got.Total)
	}
	if pct(got) != "36" {
		t.Fatalf("percent = %v, want 36", pct(got))
	}
	gaps := gapMap(got)
	if gaps["src/db.ts"] != "16-20,46-50" {
		t.Fatalf("db gaps = %q, want 16-20,46-50", gaps["src/db.ts"])
	}
	if gaps["src/app.ts"] != "90-100" {
		t.Fatalf("app gaps = %q, want 90-100", gaps["src/app.ts"])
	}
	if got.Gaps != 2 {
		t.Fatalf("gaps = %d, want 2", got.Gaps)
	}
}

func TestGeneratedExempt(t *testing.T) {
	review := mustReview(t, `{"coverage":[{"file":"src/db.ts","ranges":"all"},{"file":"src/app.ts","ranges":"all"},{"file":"dist/app.min.js"}]}`)
	got, warns, ref := Evaluate(writeBundle(t, fixtureRows()), review, generatedMin)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if len(warns) != 0 {
		t.Fatalf("warnings = %v, want none (a generated file is silently exempt)", warns)
	}
	if got.Exempt != 1 || got.Files != 2 {
		t.Fatalf("exempt/files = %d/%d, want 1/2", got.Exempt, got.Files)
	}
	if !got.Complete() {
		t.Fatalf("percent = %v, want 100", got.Percent)
	}
}

func TestUnknownFileWarns(t *testing.T) {
	review := mustReview(t, `{"coverage":[{"file":"nope.txt","ranges":"all"}]}`)
	got, warns, ref := Evaluate(writeBundle(t, fixtureRows()), review, notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "nope.txt is not a changed file") {
		t.Fatalf("warnings = %v, want the unknown-file warning", warns)
	}
	if got.Attested != 0 {
		t.Fatalf("attested = %d, want 0", got.Attested)
	}
}

func TestMalformedRangesCountAsUnattested(t *testing.T) {
	review := mustReview(t, `{"coverage":[{"file":"src/db.ts","ranges":"9-3"}]}`)
	got, warns, ref := Evaluate(writeBundle(t, fixtureRows()), review, generatedMin)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if len(warns) == 0 || !strings.Contains(warns[0], "malformed ranges") {
		t.Fatalf("warnings = %v, want the malformed-range warning", warns)
	}
	if got.Attested != 0 || got.Gaps != 2 {
		t.Fatalf("attested/gaps = %d/%d, want 0/2", got.Attested, got.Gaps)
	}
}

func TestDuplicateFileUnions(t *testing.T) {
	review := mustReview(t, `{"coverage":[{"file":"src/db.ts","ranges":"10-15"},{"file":"src/db.ts","ranges":"16-20"}]}`)
	got, _, ref := Evaluate(writeBundle(t, fixtureRows()), review, notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if got.Attested != 11 {
		t.Fatalf("attested = %d, want 11 (no double count)", got.Attested)
	}
}

func TestAbsentVersusEmpty(t *testing.T) {
	dir := writeBundle(t, fixtureRows())

	absent, warns, ref := Evaluate(dir, mustReview(t, `{"body":"x"}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if absent.Present {
		t.Fatal("absent coverage read as present")
	}
	if len(warns) != 0 {
		t.Fatalf("warnings = %v, want none for a legacy review", warns)
	}
	if len(absent.Unattested) == 0 {
		t.Fatal("absent coverage produced no gaps; next --coverage needs them")
	}

	empty, _, ref := Evaluate(dir, mustReview(t, `{"coverage":[]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if !empty.Present {
		t.Fatal("coverage: [] read as absent")
	}
	if pct(empty) != "0" {
		t.Fatalf("percent = %v, want 0", pct(empty))
	}
}

func TestNoHunksMeansUnmeasured(t *testing.T) {
	dir := writeBundle(t, nil)
	got, _, ref := Evaluate(dir, mustReview(t, `{"coverage":[]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if got.Percent != nil {
		t.Fatalf("percent = %v, want nil when the denominator is zero", got.Percent)
	}
	if !got.Present {
		t.Fatal("an explicit empty attestation must stay present")
	}
}

func TestChunkedSkips(t *testing.T) {
	dir := writeBundle(t, fixtureRows())
	if err := mkdirAll(dir + "/chunks"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir+"/chunks/manifest.json", `{"chunks":[]}`)
	writeFile(t, dir+"/chunks/01.diff", "diff")
	got, warns, ref := Evaluate(dir, mustReview(t, `{"coverage":[{"file":"src/db.ts","ranges":"all"}]}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if !got.Chunked || got.Chunks != 1 {
		t.Fatalf("chunked/chunks = %v/%d, want true/1", got.Chunked, got.Chunks)
	}
	if len(warns) != 0 {
		t.Fatalf("warnings = %v, want none: the chunked notice belongs to the caller, so a legacy chunked bundle stays quiet", warns)
	}
	if !Present(mustReview(t, `{"coverage":[]}`)) {
		t.Fatal("Present must report a coverage key")
	}
	if got.Attested != 0 || got.Total != 0 {
		t.Fatalf("chunked bundle was evaluated: %+v", got)
	}
}

func TestNonArrayCoverage(t *testing.T) {
	_, warns, ref := Evaluate(writeBundle(t, fixtureRows()), mustReview(t, `{"coverage":"all"}`), notGenerated)
	if ref != nil {
		t.Fatalf("Evaluate refused: %v", ref)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "not an array") {
		t.Fatalf("warnings = %v, want the not-an-array warning", warns)
	}
}

func gapMap(s Summary) map[string]string {
	out := make(map[string]string, len(s.Unattested))
	for _, g := range s.Unattested {
		out[g.File] = g.Ranges
	}
	return out
}

// pct renders a nullable percent for a failure message: a pointer printed with
// %v is an address, which hides the value that actually failed.
func pct(s Summary) string {
	if s.Percent == nil {
		return "null"
	}
	return strconv.Itoa(*s.Percent)
}
