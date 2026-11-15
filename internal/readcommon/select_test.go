package readcommon_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// selectEntry renders one finding whose path, severity, status, and kind are
// the axis a selector row varies.
func selectEntry(sig, status, severity, path, kind string) string {
	return `{"kind":"` + kind + `","path":"` + path + `","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":"pointed","severity":"` + severity + `","sig":"` + sig + `",` +
		`"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"` + status + `",` +
		`"rationale":"","history":[]}`
}

// selectFixture is the shared store: one finding per selection axis.
var selectFixture = "[" +
	selectEntry("crit", "open", "critical", "app.txt", "inline") + "," +
	selectEntry("imp", "open", "important", "src/a.go", "inline") + "," +
	selectEntry("info", "open", "informational", "src/b.go", "inline") + "," +
	selectEntry("untagged", "open", "", "src/c.go", "inline") + "," +
	selectEntry("section", "open", "critical", "src/d.go", "section") + "," +
	selectEntry("done", "addressed", "critical", "src/e.go", "inline") + "]"

// TestSelect proves the one selection rule every fix mode shares: the
// status/kind filters, the severity floor, the path globs, and the sig list.
func TestSelect(t *testing.T) {
	t.Parallel()
	entries := mustEntries(t, selectFixture)
	tests := []struct {
		name string
		sel  readcommon.Selector
		want []string
	}{
		{
			name: "zero selector reads the whole store in rank order",
			want: []string{"crit", "section", "imp", "info", "untagged", "done"},
		},
		{
			name: "open inline keeps crit imp info untagged in severity order",
			sel:  readcommon.Selector{Status: readcommon.StatusOpen, Kind: readcommon.KindInline},
			want: []string{"crit", "imp", "info", "untagged"},
		},
		{
			name: "important floor keeps crit and imp and never withholds untagged",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen,
				Kind:   readcommon.KindInline, Floor: "important",
			},
			want: []string{"crit", "imp", "untagged"},
		},
		{
			name: "informational floor keeps every published severity",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen,
				Kind:   readcommon.KindInline, Floor: "informational",
			},
			want: []string{"crit", "imp", "info", "untagged"},
		},
		{
			name: "critical floor keeps crit and the untagged finding",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen,
				Kind:   readcommon.KindInline, Floor: "critical",
			},
			want: []string{"crit", "untagged"},
		},
		{
			name: "path globs select the matching paths",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen,
				Kind:   readcommon.KindInline, Paths: []string{"src/*.go"},
			},
			want: []string{"imp", "info", "untagged"},
		},
		{
			name: "a directory glob spans every file under it",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen,
				Kind:   readcommon.KindInline, Paths: []string{"src/**"},
			},
			want: []string{"imp", "info", "untagged"},
		},
		{
			name: "globs and floor compose",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen,
				Kind:   readcommon.KindInline, Floor: "important", Paths: []string{"src/**"},
			},
			want: []string{"imp", "untagged"},
		},
		{
			name: "an explicit sig list selects only those sigs",
			sel:  readcommon.Selector{Sigs: []string{"info", "done"}},
			want: []string{"info", "done"},
		},
		{
			name: "a sig outside the store selects nothing",
			sel:  readcommon.Selector{Sigs: []string{"absent"}},
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertSigs(t, readcommon.Select(entries, &tc.sel), tc.want)
		})
	}
}

// assertSigs compares one selection's sigs against the literal expectation.
func assertSigs(t *testing.T, ranked []readcommon.Ranked, want []string) {
	t.Helper()
	got := rankSigs(ranked)
	if len(got) != len(want) {
		t.Fatalf("Select = %v; want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Select = %v; want %v", got, want)
		}
	}
}

// TestSelectFloorLeavesUntaggedProvesFloorIsNotASecondRank pins the untagged
// rule against the published floor semantics: a severity the vocabulary
// cannot place is never withheld.
func TestSelectFloorLeavesUntagged(t *testing.T) {
	t.Parallel()
	entries := mustEntries(t, "["+
		selectEntry("untagged", "open", "", "src/c.go", "inline")+"]")
	ranked := readcommon.Select(entries, &readcommon.Selector{
		Status: readcommon.StatusOpen, Kind: readcommon.KindInline, Floor: "critical",
	})
	if len(ranked) != 1 || rankedSig(ranked[0]) != "untagged" {
		t.Fatalf("Select(critical floor) = %v; want the untagged finding kept",
			rankSigs(ranked))
	}
}

// TestSelectKeepsStoreRanks proves a selection reports the store-wide rank
// numbers, not a renumbered list: a selected finding's rank keeps saying how
// many fixable findings sit above it.
func TestSelectKeepsStoreRanks(t *testing.T) {
	t.Parallel()
	entries := mustEntries(t, selectFixture)
	ranked := readcommon.Select(entries, &readcommon.Selector{
		Status: readcommon.StatusOpen, Kind: readcommon.KindInline, Floor: "important",
	})
	want := []int{1, 2, 4}
	if len(ranked) != len(want) {
		t.Fatalf("Select(important floor) = %v; want %d findings", rankSigs(ranked), len(want))
	}
	for i := range want {
		if ranked[i].Rank != want[i] {
			t.Fatalf("ranks = %v; want %v", rankNumbers(ranked), want)
		}
	}
}

// rankNumbers renders one ranking's rank column.
func rankNumbers(ranked []readcommon.Ranked) []int {
	out := make([]int, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, r.Rank)
	}
	return out
}
