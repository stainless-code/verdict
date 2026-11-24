package readcommon_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

func selectEntry(sig, status, severity, path, kind string) string {
	return `{"kind":"` + kind + `","path":"` + path + `","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":"pointed","severity":"` + severity + `","sig":"` + sig + `",` +
		`"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"` + status + `",` +
		`"rationale":"","history":[]}`
}

// selectLensEntry renders one finding carrying optional lens provenance: an
// empty lens spells no `lens` key at all, like a store written before lens
// provenance existed.
func selectLensEntry(sig, status, severity, path, lens string) string {
	key := ""
	if lens != "" {
		key = `"lens":"` + lens + `",`
	}
	return `{"kind":"inline","path":"` + path + `","side":"RIGHT","line":2,` + key +
		`"anchored":true,"body":"Body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":"pointed","severity":"` + severity + `","sig":"` + sig + `",` +
		`"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"` + status + `",` +
		`"rationale":"","history":[]}`
}

var selectFixture = "[" +
	selectEntry("crit", "open", "critical", "app.txt", "inline") + "," +
	selectEntry("imp", "open", "important", "src/a.go", "inline") + "," +
	selectEntry("info", "open", "informational", "src/b.go", "inline") + "," +
	selectEntry("untagged", "open", "", "src/c.go", "inline") + "," +
	selectEntry("section", "open", "critical", "src/d.go", "section") + "," +
	selectEntry("done", "addressed", "critical", "src/e.go", "inline") + "]"

var selectLensFixture = "[" +
	selectLensEntry("perf-imp", "open", "important", "src/perf.go", "perf") + "," +
	selectLensEntry("sec-crit", "open", "critical", "src/sec.go", "security") + "," +
	selectLensEntry("perf-info", "open", "informational", "src/misc.go", "perf") + "," +
	selectLensEntry("nolen", "open", "important", "src/plain.go", "") + "," +
	selectLensEntry("perf-done", "addressed", "important", "src/done.go", "perf") + "]"

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

func TestSelectLenses(t *testing.T) {
	t.Parallel()
	entries := mustEntries(t, selectLensFixture)
	tests := []struct {
		name string
		sel  readcommon.Selector
		want []string
	}{
		{
			name: "a lens set selects only the matching lenses in rank order",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen, Kind: readcommon.KindInline, Lenses: []string{"perf"},
			},
			want: []string{"perf-imp", "perf-info"},
		},
		{
			name: "several lenses select the union",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen, Kind: readcommon.KindInline, Lenses: []string{"perf", "security"},
			},
			want: []string{"sec-crit", "perf-imp", "perf-info"},
		},
		{
			name: "an unlisted lens selects nothing",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen, Kind: readcommon.KindInline, Lenses: []string{"naming"},
			},
			want: nil,
		},
		{
			name: "lenses and floor compose: the informational perf finding drops",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen, Kind: readcommon.KindInline,
				Lenses: []string{"perf"}, Floor: "important",
			},
			want: []string{"perf-imp"},
		},
		{
			name: "lenses and paths compose: the perf finding outside the glob drops",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen, Kind: readcommon.KindInline,
				Lenses: []string{"perf"}, Paths: []string{"src/perf.go"},
			},
			want: []string{"perf-imp"},
		},
		{
			name: "lenses and sigs compose: the sig outside the set drops",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen, Kind: readcommon.KindInline,
				Lenses: []string{"perf"}, Sigs: []string{"perf-info", "nolen"},
			},
			want: []string{"perf-info"},
		},
		{
			name: "an empty lens slice is no filter, not an empty scope",
			sel: readcommon.Selector{
				Status: readcommon.StatusOpen, Kind: readcommon.KindInline, Lenses: []string{},
			},
			want: []string{"sec-crit", "perf-imp", "nolen", "perf-info"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertSigs(t, readcommon.Select(entries, &tc.sel), tc.want)
		})
	}
}

// TestSelectLensesExcludesMissingLens spells the fail-closed contract: a
// finding with no `lens` key never matches a lens filter, however the filter
// spells its set.
func TestSelectLensesExcludesMissingLens(t *testing.T) {
	t.Parallel()
	entries := mustEntries(t, selectLensFixture)
	ranked := readcommon.Select(entries, &readcommon.Selector{
		Status: readcommon.StatusOpen, Kind: readcommon.KindInline,
		Lenses: []string{"perf", "security", "plain", "misc"},
	})
	for _, r := range ranked {
		if rankedSig(r) == "nolen" {
			t.Fatalf("Select(lens filter) = %v; the finding without lens provenance matched", rankSigs(ranked))
		}
	}
	if len(ranked) != 3 {
		t.Fatalf("Select(lens filter) = %v; want the three sourced findings", rankSigs(ranked))
	}
}

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

func rankNumbers(ranked []readcommon.Ranked) []int {
	out := make([]int, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, r.Rank)
	}
	return out
}
