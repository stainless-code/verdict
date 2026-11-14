package risk_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/risk"
	"github.com/stainless-code/verdict/internal/jq"
)

func scoreBundle(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func impactDoc(symbols, callers int) string {
	return `{"sources":["index:tree"],"stale":false,` +
		`"symbols":{"count":` + strconv.Itoa(symbols) + `},"callers":{"count":` + strconv.Itoa(callers) + `}}`
}

func diffDoc(additions, deletions int) string {
	var b strings.Builder
	b.WriteString("diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,")
	b.WriteString(strconv.Itoa(deletions) + " +1," + strconv.Itoa(additions) + " @@\n")
	for range deletions {
		b.WriteString("-old line\n")
	}
	for range additions {
		b.WriteString("+new line\n")
	}
	return b.String()
}

func scoreFinding(status, category, rigor string) any {
	o := jq.NewObj("status", status, "severity", "informational", "kind", "inline", "anchored", true)
	if category != "" {
		o.Set("category", category)
	}
	if rigor != "" {
		o.Set("rigor", rigor)
	}
	return o
}

// TestScoreBucketEdges pins both bucket tables at every published edge: each
// row is the pair that straddles a boundary, so an off-by-one in either table
// fails here.
//
//nolint:revive // the two bucket matrices assert their edges inline
func TestScoreBucketEdges(t *testing.T) {
	reach := []struct {
		count, points int
	}{
		{0, 0}, {1, 25}, {3, 25}, {4, 50}, {10, 50}, {11, 75}, {25, 75}, {26, 100}, {40, 100},
	}
	for _, tc := range reach {
		dir := scoreBundle(t, map[string]string{"impact.json": impactDoc(tc.count, 0)})
		got := risk.Score(dir, nil)
		if got.Reach.Points != tc.points {
			t.Errorf("reach %d points = %d, want %d", tc.count, got.Reach.Points, tc.points)
		}
		if !got.Reach.Measured || got.Reach.Weight != 35 {
			t.Errorf("reach %d = %+v, want measured with weight 35", tc.count, got.Reach)
		}
	}

	churn := []struct {
		lines, points int
	}{
		{0, 0}, {20, 0}, {21, 25}, {100, 25}, {101, 50}, {300, 50}, {301, 75}, {800, 75}, {801, 100},
	}
	for _, tc := range churn {
		dir := scoreBundle(t, map[string]string{"full.diff": diffDoc(tc.lines, 0)})
		got := risk.Score(dir, nil)
		if got.Churn.Points != tc.points {
			t.Errorf("churn %d lines points = %d, want %d", tc.lines, got.Churn.Points, tc.points)
		}
		if !got.Churn.Measured || got.Churn.Weight != 25 {
			t.Errorf("churn %d = %+v, want measured with weight 25", tc.lines, got.Churn)
		}
	}
}

// TestScoreFormula pins the published integer formula and every component's
// contribution on one bundle built from all three artifacts: the truncating
// division is the point (6375/100 must read 63, never 64).
//
//nolint:cyclop,revive // one assertion block per component
func TestScoreFormula(t *testing.T) {
	dir := scoreBundle(t, map[string]string{
		"impact.json":   impactDoc(1, 3),
		"full.diff":     diffDoc(21, 0),
		"findings.json": `[]`,
	})
	entries := []any{scoreFinding("open", "security", "said")}

	got := risk.Score(dir, entries)
	if got.Value != 63 {
		t.Errorf("Value = %d, want 63", got.Value)
	}
	if got.Measured != 4 {
		t.Errorf("Measured = %d, want 4", got.Measured)
	}
	if got.Reach.Points != 50 || got.Reach.Symbols == nil || *got.Reach.Symbols != 1 ||
		got.Reach.Callers == nil || *got.Reach.Callers != 3 {
		t.Errorf("Reach = %+v, want symbols=1 callers=3 points=50", got.Reach)
	}
	if got.Churn.Points != 25 || got.Churn.Additions != 21 || got.Churn.Files != 1 || got.Churn.Hunks != 1 {
		t.Errorf("Churn = %+v, want 21 additions in 1 file and 1 hunk at 25 points", got.Churn)
	}
	if got.Category.Points != 100 || got.Category.Open != 1 || got.Category.Worst == nil || *got.Category.Worst != "security" {
		t.Errorf("Category = %+v, want worst=security open=1 points=100", got.Category)
	}
	if got.Rung.Points != 100 || got.Rung.Open != 1 || got.Rung.Weakest == nil || *got.Rung.Weakest != "said" {
		t.Errorf("Rung = %+v, want weakest=said open=1 points=100", got.Rung)
	}
}

// TestScoreUnmeasuredComponents proves an absent artifact reads as unmeasured
// with 0 points rather than vanishing or scoring 0 silently.
//
//nolint:cyclop,revive // the absent, partial, and findings-only trees read as one matrix
func TestScoreUnmeasuredComponents(t *testing.T) {
	empty := risk.Score(t.TempDir(), nil)
	if empty.Measured != 0 || empty.Value != 0 {
		t.Fatalf("empty bundle = %+v, want measured 0 value 0", empty)
	}
	for _, component := range []struct {
		name     string
		measured bool
	}{
		{"reach", empty.Reach.Measured},
		{"churn", empty.Churn.Measured},
		{"category", empty.Category.Measured},
		{"rung", empty.Rung.Measured},
	} {
		if component.measured {
			t.Errorf("%s measured on an empty bundle", component.name)
		}
	}

	findingsOnly := risk.Score(scoreBundle(t, map[string]string{"findings.json": `[]`}), nil)
	if findingsOnly.Measured != 2 || findingsOnly.Reach.Measured || findingsOnly.Churn.Measured {
		t.Fatalf("findings-only = %+v, want measured 2 (category and rung)", findingsOnly)
	}

	noImpact := risk.Score(scoreBundle(t, map[string]string{
		"full.diff":     diffDoc(4, 0),
		"findings.json": `[]`,
	}), nil)
	if noImpact.Measured != 3 || noImpact.Reach.Measured || noImpact.Reach.Points != 0 {
		t.Fatalf("no-impact = %+v, want measured 3 with an unmeasured reach", noImpact)
	}
	if !noImpact.Churn.Measured || noImpact.Churn.Points != 0 {
		t.Fatalf("no-impact churn = %+v, want measured at 0 points", noImpact.Churn)
	}
}

// TestScoreCategoryAndRung pins the worst-open-finding picks: only open
// findings count, the strongest category and the weakest rung win, and a
// measured zero (style, ran) still names what it measured.
//
//nolint:revive // the category and rung picks read as one matrix
func TestScoreCategoryAndRung(t *testing.T) {
	dir := scoreBundle(t, map[string]string{"findings.json": `[]`})
	cases := []struct {
		name    string
		worst   string
		weakest string
		entries []any
		cat     int
		rung    int
	}{
		{
			name:    "security said outranks a stronger rung",
			entries: []any{scoreFinding("open", "security", "reproduced"), scoreFinding("open", "logic", "said")},
			worst:   "security", cat: 100, weakest: "said", rung: 100,
		},
		{
			name:    "logic beats performance",
			entries: []any{scoreFinding("open", "performance", "walked"), scoreFinding("open", "logic", "pointed")},
			worst:   "logic", cat: 75, weakest: "pointed", rung: 75,
		},
		{
			name:    "untagged reads as other",
			entries: []any{scoreFinding("open", "", "")},
			worst:   "untagged", cat: 25, weakest: "said", rung: 100,
		},
		{
			name:    "style is a measured zero",
			entries: []any{scoreFinding("open", "style", "reproduced")},
			worst:   "style", cat: 0, weakest: "reproduced", rung: 0,
		},
		{
			name:    "addressed findings never score",
			entries: []any{scoreFinding("addressed", "security", "ran"), scoreFinding("deferred", "security", "ran")},
			worst:   "", cat: 0, weakest: "", rung: 0,
		},
	}
	for _, tc := range cases {
		got := risk.Score(dir, tc.entries)
		worst := ""
		if got.Category.Worst != nil {
			worst = *got.Category.Worst
		}
		weakest := ""
		if got.Rung.Weakest != nil {
			weakest = *got.Rung.Weakest
		}
		if worst != tc.worst || got.Category.Points != tc.cat {
			t.Errorf("%s: category = %q/%d, want %q/%d", tc.name, worst, got.Category.Points, tc.worst, tc.cat)
		}
		if weakest != tc.weakest || got.Rung.Points != tc.rung {
			t.Errorf("%s: rung = %q/%d, want %q/%d", tc.name, weakest, got.Rung.Points, tc.weakest, tc.rung)
		}
		if !got.Category.Measured || !got.Rung.Measured {
			t.Errorf("%s: a present findings store must measure category and rung: %+v", tc.name, got)
		}
	}
}

// TestScoreHostileImpact proves an impact.json that is unparsable, wrongly
// typed, negative, or out of range reads as an unmeasured reach at 0 points,
// never as a zero-byte count.
//
//nolint:revive // the hostile artifact matrix asserts its rows inline
func TestScoreHostileImpact(t *testing.T) {
	cases := []struct {
		name   string
		impact string
	}{
		{"float count", `{"symbols":{"count":2.5},"callers":{"count":1}}`},
		{"negative count", `{"symbols":{"count":-1},"callers":{"count":1}}`},
		{"string count", `{"symbols":{"count":"3"},"callers":{"count":1}}`},
		{"overflowing count", `{"symbols":{"count":1e30},"callers":{"count":1}}`},
		{"summed overflow", `{"symbols":{"count":9223372036854775807},"callers":{"count":1}}`},
		{"count above the tables", `{"symbols":{"count":1073741825},"callers":{"count":1}}`},
		{"missing count", `{"symbols":{},"callers":{"count":1}}`},
		{"unparsable", `{"symbols":`},
		{"not an object", `[1,2]`},
	}
	for _, tc := range cases {
		dir := scoreBundle(t, map[string]string{"impact.json": tc.impact})
		got := risk.Score(dir, nil)
		if got.Reach.Measured || got.Reach.Points != 0 || got.Value != 0 {
			t.Errorf("%s: reach = %+v score %d, want unmeasured at 0", tc.name, got.Reach, got.Value)
		}
	}

	kept := risk.Score(scoreBundle(t, map[string]string{
		"impact.json": `{"symbols":{},"callers":{"count":1}}`,
	}), nil)
	if kept.Reach.Callers == nil || *kept.Reach.Callers != 1 || kept.Reach.Symbols != nil {
		t.Errorf("partial counts = %+v, want callers=1 and symbols unread", kept.Reach)
	}

	stale := risk.Score(scoreBundle(t, map[string]string{
		"impact.json": `{"symbols":{"count":1},"callers":{"count":1},"stale":true}`,
	}), nil)
	if stale.Reach.Stale == nil || !*stale.Reach.Stale {
		t.Errorf("stale = %v, want true from impact.json", stale.Reach.Stale)
	}

	top := risk.Score(scoreBundle(t, map[string]string{
		"impact.json": `{"symbols":{"count":1073741824},"callers":{"count":1073741824}}`,
	}), nil)
	if !top.Reach.Measured || top.Reach.Points != 100 {
		t.Errorf("largest counts = %+v, want a measured reach in the top bucket", top.Reach)
	}
}

// TestScoreUnnamedRigorIsNotPublished proves a rigor token no rung names scores
// conservatively as said while the published weakest stays null: the store
// never carried that rung, so the row must not claim it did.
func TestScoreUnnamedRigorIsNotPublished(t *testing.T) {
	dir := scoreBundle(t, map[string]string{"findings.json": `[]`})
	cases := []struct {
		name    string
		entries []any
	}{
		{"unnamed alone", []any{scoreFinding("open", "logic", "invented")}},
		{"unnamed beside a named", []any{
			scoreFinding("open", "logic", "walked"),
			scoreFinding("open", "logic", "invented"),
		}},
	}
	for _, tc := range cases {
		got := risk.Score(dir, tc.entries)
		if !got.Rung.Measured || got.Rung.Points != 100 {
			t.Errorf("%s: rung = %+v, want a measured 100 points (said is the conservative floor)", tc.name, got.Rung)
		}
		if got.Rung.Weakest != nil {
			t.Errorf("%s: weakest = %q, want unset: the store names no such rung", tc.name, *got.Rung.Weakest)
		}
	}
}

// TestScoreOversizedImpactIsUnmeasured proves the score reads impact.json
// through the store cap: one byte over must not become an unbounded read.
func TestScoreOversizedImpactIsUnmeasured(t *testing.T) {
	pad := strings.Repeat("a", int(jq.MaxFindingsBytes))
	dir := scoreBundle(t, map[string]string{
		"impact.json": `{"symbols":{"count":1},"callers":{"count":1},"pad":"` + pad + `"}`,
	})
	got := risk.Score(dir, nil)
	if got.Reach.Measured || got.Reach.Points != 0 {
		t.Fatalf("oversized impact = %+v, want an unmeasured reach", got.Reach)
	}
}
