package readcommon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// rankBase is the shared finding body: every sort key except the ones a case
// varies, all of them tied between the case's entries.
var rankBase = []string{
	`"kind":"inline"`, `"anchored":true`, `"inBounds":true`, `"line":1`,
}

// rankOpen is the open status every finding carries unless a case varies it.
const rankOpen = `"status":"open"`

// rankFinding renders one open finding whose differing keys are handed in as
// raw JSON pairs (so a case can spell a null or a false path).
func rankFinding(pairs ...string) string {
	return rankFindingWith(rankOpen, pairs...)
}

// rankFindingWith renders one finding with an explicit status.
func rankFindingWith(status string, pairs ...string) string {
	fields := append(append([]string{}, rankBase...), status)
	fields = append(fields, pairs...)
	return "{" + strings.Join(fields, ",") + "}"
}

// rankDoc renders a findings array from entries.
func rankDoc(entries ...string) string { return "[" + strings.Join(entries, ",") + "]" }

// assertRankOrder ranks one document and compares the sig order to want.
// Each fixture is built so the keys under test cannot be substituted by a
// lower tiebreak: the entries differ only on the key under test, and their
// sigs/paths are ordered the other way round, so a neutralized key reorders
// them.
func assertRankOrder(t *testing.T, doc string, want []string) {
	t.Helper()
	ranked := readcommon.Rank(mustEntries(t, doc), readcommon.StatusAll, readcommon.KindAll)
	if got := rankSigs(ranked); !slices.Equal(got, want) {
		t.Fatalf("rank order = %v; want %v", got, want)
	}
}

// TestRankSeverityTiebreak pins the severity leg: critical ranks before
// informational even when the informational finding sorts first on path and
// sig. A severity leg that reads every tier as untagged leaves the order to
// those lower keys, so the sev gate's order silently becomes a path sort.
func TestRankSeverityTiebreak(t *testing.T) {
	t.Parallel()
	assertRankOrder(t, rankDoc(
		rankFinding(`"sig":"b"`, `"path":"aaa.txt"`, `"severity":"informational"`, `"rigor":"walked"`),
		rankFinding(`"sig":"a"`, `"path":"zzz.txt"`, `"severity":"critical"`, `"rigor":"walked"`),
	), []string{"a", "b"})
}

// TestRankRigorTiebreak pins the rigor leg's two ends: an unknown rung sorts
// before reproduced (the -1 sentinel the rung table documents), so the
// unknown-rung finding keeps rank 0 even when its path and sig sort later. A
// rung table that drops the sentinel (or reads reproduced as untagged) turns
// that into a path sort.
func TestRankRigorTiebreak(t *testing.T) {
	t.Parallel()
	assertRankOrder(t, rankDoc(
		rankFinding(`"sig":"a"`, `"path":"aaa.txt"`, `"severity":"critical"`, `"rigor":"reproduced"`),
		rankFinding(`"sig":"b"`, `"path":"zzz.txt"`, `"severity":"critical"`, `"rigor":"bogus"`),
	), []string{"b", "a"})
}

// TestRankRigorMissingReadsSaid pins the null/false posture: a rigor that is
// null or false reads "said" (the bottom rung), not untagged. Untagged sorts
// first, so confusing the two lifts every unrecorded finding above the ones
// the reviewer actually ranked.
func TestRankRigorMissingReadsSaid(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, missing string }{
		{"null rigor", `"rigor":null`},
		{"false rigor", `"rigor":false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertRankOrder(t, rankDoc(
				rankFinding(`"sig":"a"`, `"path":"zzz.txt"`, `"severity":"critical"`, tc.missing),
				rankFinding(`"sig":"b"`, `"path":"aaa.txt"`, `"severity":"critical"`, `"rigor":"said"`),
			), []string{"b", "a"})
		})
	}
}

// TestRankPathNullAndFalseReadEmpty pins the path normalization: a null or
// false path reads "" before the comparison, so three findings whose only
// path spelling differs stay tied on path and fall through to sig. Leaving
// null or false in place ranks them below the empty string and reorders the
// tied group.
func TestRankPathNullAndFalseReadEmpty(t *testing.T) {
	t.Parallel()
	assertRankOrder(t, rankDoc(
		rankFinding(`"sig":"z"`, `"path":null`, `"severity":"critical"`, `"rigor":"walked"`),
		rankFinding(`"sig":"a"`, `"path":""`, `"severity":"critical"`, `"rigor":"walked"`),
		rankFinding(`"sig":"y"`, `"path":false`, `"severity":"critical"`, `"rigor":"walked"`),
	), []string{"a", "y", "z"})
}

// TestRankCountsPinBothDirections pins the two count helpers on a store that
// is not one-of-each: two open findings and one deferred, so an inverted
// comparison cannot land on the same total by counting the complement.
func TestRankCountsPinBothDirections(t *testing.T) {
	t.Parallel()
	entries := mustEntries(t, rankDoc(
		rankFinding(`"sig":"a"`, `"path":"a.txt"`, `"severity":"critical"`, `"rigor":"walked"`),
		rankFinding(`"sig":"b"`, `"path":"b.txt"`, `"severity":"critical"`, `"rigor":"walked"`),
		rankFindingWith(`"status":"deferred"`,
			`"sig":"c"`, `"path":"c.txt"`, `"severity":"critical"`, `"rigor":"walked"`),
	))
	if got := readcommon.CountOpen(entries); got != 2 {
		t.Errorf("CountOpen = %d; want 2", got)
	}
	ranked := readcommon.Rank(entries, readcommon.StatusAll, readcommon.KindAll)
	if got := readcommon.CountStatus(ranked, readcommon.StatusOpen); got != 2 {
		t.Errorf("CountStatus(open) = %d; want 2", got)
	}
	if got := readcommon.CountStatus(ranked, "deferred"); got != 1 {
		t.Errorf("CountStatus(deferred) = %d; want 1", got)
	}
}

// TestRankLowerKeysBeatSigAndPath pins the anchored, inBounds and line legs
// where each wins against the keys below it: the winner carries the larger
// path and sig, so a leg that stops comparing (or a chain that skips it)
// hands the order to path/sig and reorders the pair.
func TestRankLowerKeysBeatSigAndPath(t *testing.T) {
	t.Parallel()
	pair := func(winner, loser string) string {
		return rankDoc(loser, winner)
	}
	tests := []struct {
		name string
		doc  string
	}{
		{"anchored", pair(
			`{"kind":"inline","status":"open","severity":"critical","rigor":"walked","anchored":true,"inBounds":true,"line":1,"path":"zzz.txt","sig":"z"}`,
			`{"kind":"inline","status":"open","severity":"critical","rigor":"walked","anchored":false,"inBounds":true,"line":1,"path":"aaa.txt","sig":"a"}`,
		)},
		{"inBounds", pair(
			`{"kind":"inline","status":"open","severity":"critical","rigor":"walked","anchored":true,"inBounds":true,"line":1,"path":"zzz.txt","sig":"z"}`,
			`{"kind":"inline","status":"open","severity":"critical","rigor":"walked","anchored":true,"inBounds":false,"line":1,"path":"aaa.txt","sig":"a"}`,
		)},
		{"line", pair(
			`{"kind":"inline","status":"open","severity":"critical","rigor":"walked","anchored":true,"inBounds":true,"line":2,"path":"same.txt","sig":"z"}`,
			`{"kind":"inline","status":"open","severity":"critical","rigor":"walked","anchored":true,"inBounds":true,"line":10,"path":"same.txt","sig":"a"}`,
		)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertRankOrder(t, tc.doc, []string{"z", "a"})
		})
	}
}
