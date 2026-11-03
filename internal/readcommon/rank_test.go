package readcommon_test

import (
	"fmt"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// mustEntries decodes a findings array for the rank tests.
func mustEntries(tb testing.TB, doc string) []any {
	tb.Helper()
	v, err := jq.DecodeOrdered([]byte(doc))
	if err != nil {
		tb.Fatal(err)
	}
	entries, ok := v.([]any)
	if !ok {
		tb.Fatal("fixture is not an array")
	}
	return entries
}

// rankEntry renders one ranked-test finding.
func rankEntry(sig, status, severity, rigor, kind string, line int) string {
	return fmt.Sprintf(`{"kind":%q,"path":"notes.txt","side":"RIGHT","line":%d,`+
		`"anchored":true,"body":"Body here with enough substance.","suggestion":null,`+
		`"inBounds":true,"rigor":%q,"severity":%q,"sig":%q,"sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":%q,"rationale":"","history":[]}`,
		kind, line, rigor, severity, sig, status)
}

// rankSigs returns the sigs (or null markers) of a ranking in order.
func rankSigs(ranked []readcommon.Ranked) []string {
	out := make([]string, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, rankedSig(r))
	}
	return out
}

// rankedSig renders one ranked sig; null markers read "null".
func rankedSig(r readcommon.Ranked) string {
	m, ok := r.Entry.(*jq.Obj)
	if !ok || m == nil {
		return "null"
	}
	v, has := m.Get("sig")
	if !has {
		return "null"
	}
	s, isStr := v.(string)
	if !isStr {
		return "null"
	}
	return s
}

// TestRankOrdering checks the ranked sort: open before deferred before
// the rest, severity tiers, rigor rungs, then the anchor/path/line/sig
// tiebreaks, with stable 1-based ranks.
func TestRankOrdering(t *testing.T) {
	t.Parallel()
	entries := mustEntries(t, "["+rankEntry("s-open-crit", "open", "critical", "walked", "inline", 2)+","+
		rankEntry("s-open-info", "open", "informational", "said", "inline", 3)+","+
		rankEntry("s-def", "deferred", "critical", "reproduced", "inline", 1)+","+
		rankEntry("s-addr", "addressed", "critical", "walked", "inline", 1)+","+
		rankEntry("s-open-imp", "open", "important", "pointed", "inline", 2)+"]")
	ranked := readcommon.Rank(entries, "all", "all")
	want := []string{"s-open-crit", "s-open-imp", "s-open-info", "s-def", "s-addr"}
	got := rankSigs(ranked)
	if len(got) != len(want) {
		t.Fatalf("Rank() = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Rank()[%d] = %v; want %v", i, got, want)
		}
		if ranked[i].Rank != i+1 {
			t.Errorf("Rank()[%d].Rank = %d; want %d", i, ranked[i].Rank, i+1)
		}
	}
}

// TestRankFilters checks the status and kind display filters.
func TestRankFilters(t *testing.T) {
	t.Parallel()
	entries := mustEntries(t, "["+rankEntry("s-open", "open", "critical", "walked", "inline", 2)+","+
		rankEntry("s-sect", "open", "informational", "pointed", "section", 2)+","+
		rankEntry("s-addr", "addressed", "important", "walked", "inline", 2)+"]")
	if got := len(readcommon.Rank(entries, "open", "all")); got != 2 {
		t.Errorf("Rank(open, all) = %d entries; want 2", got)
	}
	if got := len(readcommon.Rank(entries, "all", "section")); got != 1 {
		t.Errorf("Rank(all, section) = %d entries; want 1", got)
	}
	if got := readcommon.Rank(entries, "addressed", "section"); len(got) != 0 {
		t.Errorf("Rank(addressed, section) = %d entries; want 0", len(got))
	}
}

// TestRankNullEntry checks null entries flow through the ranking like the
// shell's (filters read null, keys sort first) instead of crashing it.
func TestRankNullEntry(t *testing.T) {
	t.Parallel()
	entries := mustEntries(t, `[null,`+
		rankEntry("s-open", "open", "critical", "walked", "inline", 2)+`]`)
	ranked := readcommon.Rank(entries, "all", "all")
	if len(ranked) != 2 {
		t.Fatalf("Rank() = %d entries; want 2", len(ranked))
	}
	if got := readcommon.CountOpen(entries); got != 1 {
		t.Errorf("CountOpen() = %d; want 1", got)
	}
	if got := readcommon.CountStatus(ranked, "open"); got != 1 {
		t.Errorf("CountStatus(open) = %d; want 1", got)
	}
}

// TestFieldOfNullEntries checks the shared entry-field read on entries that
// are not objects: a null, a non-object, and a typed nil pointer all read
// nil, which is what the calibrate and report callers rely on (decoded agent
// arrays may hold nulls).
func TestFieldOfNullEntries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		entry any
		name  string
	}{
		{nil, "null entry"},
		{"text", "non-object entry"},
		{(*jq.Obj)(nil), "typed nil pointer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := readcommon.FieldOf(tc.entry, "sig"); got != nil {
				t.Errorf("FieldOf(%v, sig) = %v; want nil", tc.entry, got)
			}
		})
	}
}

// TestFieldOfFields checks the shared entry-field read on a decoded entry:
// an absent key reads nil, and a present key hands back the raw value
// whatever its JSON type.
func TestFieldOfFields(t *testing.T) {
	t.Parallel()
	entry := mustEntries(t, `[{"sig":"abc","anchored":true,"body":null}]`)[0]
	cases := []struct {
		key  string
		want string
	}{
		{"missing", "null"},
		{"sig", `"abc"`},
		{"anchored", "true"},
		{"body", "null"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			if got := jq.Compact(readcommon.FieldOf(entry, tc.key)); got != tc.want {
				t.Errorf("FieldOf(entry, %q) = %s; want %s", tc.key, got, tc.want)
			}
		})
	}
}

// TestLocator checks the ranked locator: path[:line] when the path is
// truthy, else the heading fallback. Zero is truthy in jq, so only a
// missing line drops the suffix.
func TestLocator(t *testing.T) {
	t.Parallel()
	entries := mustEntries(t, "["+rankEntry("s1", "open", "critical", "walked", "inline", 2)+","+
		`{"kind":"section","path":null,"heading":"Scope note","line":null,`+
		`"body":"Body here with enough substance.","sig":"s2"}`+"]")
	if got := readcommon.Locator(entries[0]); got != "notes.txt:2" {
		t.Errorf("Locator() = %q; want notes.txt:2", got)
	}
	// A null path falls back to the heading.
	if got := readcommon.Locator(entries[1]); got != "Scope note" {
		t.Errorf("Locator() = %q; want the heading fallback", got)
	}
	if got := readcommon.ResolveSig(entries, "s"); got != "MANY" {
		t.Errorf("ResolveSig() = %q; want MANY", got)
	}
	if got := readcommon.ResolveSig(entries, "s1"); got != "s1" {
		t.Errorf("ResolveSig(s1) = %q; want s1", got)
	}
	if got := readcommon.ResolveSig(entries, "zzz"); got != "NONE" {
		t.Errorf("ResolveSig(zzz) = %q; want NONE", got)
	}
	if got := readcommon.Prefix("c11111111"); got != "c1111111" {
		t.Errorf("Prefix() = %q; want the 8-codepoint head", got)
	}
}

// rankFixture is one finding's ranked fields, varied one at a time.
type rankFixture struct {
	sig      string
	status   string
	severity string
	rigor    string
	path     string
	line     int
	anchored bool
	inBounds bool
}

// json renders the fixture as a findings-store entry.
func (f *rankFixture) json() string {
	return fmt.Sprintf(`{"kind":"inline","path":%q,"side":"RIGHT","line":%d,`+
		`"anchored":%t,"body":"Body here with enough substance.","suggestion":null,`+
		`"inBounds":%t,"rigor":%q,"severity":%q,"sig":%q,"sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":%q,"rationale":"","history":[]}`,
		f.path, f.line, f.anchored, f.inBounds, f.rigor, f.severity, f.sig, f.status)
}

// rankVariant renders one fixture from the common base, mutating one key.
func rankVariant(sig string, mutate func(*rankFixture)) string {
	f := rankFixture{
		sig:      sig,
		status:   "open",
		severity: "important",
		rigor:    "walked",
		path:     "notes.txt",
		line:     2,
		anchored: true,
		inBounds: true,
	}
	mutate(&f)
	return f.json()
}

// assertRankFirst proves a wins over b in both store orders.
func assertRankFirst(t *testing.T, name, a, b string) {
	t.Helper()
	for _, doc := range []string{"[" + a + "," + b + "]", "[" + b + "," + a + "]"} {
		ranked := readcommon.Rank(mustEntries(t, doc), readcommon.StatusAll, readcommon.KindAll)
		if got := rankSigs(ranked); len(got) != 2 || got[0] != "a" {
			t.Errorf("Rank(%s)[0] = %v; want a", name, got)
		}
	}
}

// TestRankKeyOrder pins each of the eight ranked sort keys in precedence
// order, in both store orders, so a comparator rewrite cannot silently
// reorder report, next, or fix.
func TestRankKeyOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a    string
		b    string
	}{
		{
			"status", rankVariant("a", func(f *rankFixture) { f.status = "open" }),
			rankVariant("b", func(f *rankFixture) { f.status = "deferred" }),
		},
		{
			"severity", rankVariant("a", func(f *rankFixture) { f.severity = "critical" }),
			rankVariant("b", func(f *rankFixture) { f.severity = "informational" }),
		},
		{
			"rigor", rankVariant("a", func(f *rankFixture) { f.rigor = "reproduced" }),
			rankVariant("b", func(f *rankFixture) { f.rigor = "said" }),
		},
		{
			"anchored", rankVariant("a", func(f *rankFixture) { f.anchored = true }),
			rankVariant("b", func(f *rankFixture) { f.anchored = false }),
		},
		{
			"inBounds", rankVariant("a", func(f *rankFixture) { f.inBounds = true }),
			rankVariant("b", func(f *rankFixture) { f.inBounds = false }),
		},
		{
			"path", rankVariant("a", func(f *rankFixture) { f.path = "a.txt" }),
			rankVariant("b", func(f *rankFixture) { f.path = "b.txt" }),
		},
		{
			"line", rankVariant("a", func(f *rankFixture) { f.line = 2 }),
			rankVariant("b", func(f *rankFixture) { f.line = 10 }),
		},
		{
			"sig", rankVariant("a", func(*rankFixture) {}),
			rankVariant("b", func(*rankFixture) {}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertRankFirst(t, tc.name, tc.a, tc.b)
		})
	}
}

// TestTopOpenInlineMatchesRank proves fix's default pick is the first
// open-inline item of the shared ranking, not a second comparator.
func TestTopOpenInlineMatchesRank(t *testing.T) {
	t.Parallel()
	entries := mustEntries(t, "["+
		rankVariant("deferred", func(f *rankFixture) { f.status = "deferred" })+","+
		rankVariant("section", func(f *rankFixture) { f.status = "open" })+","+
		`{"kind":"section","path":"notes.txt","line":2,"severity":"critical",`+
		`"rigor":"reproduced","anchored":true,"inBounds":true,"sig":"section",`+
		`"status":"open"}`+","+
		rankVariant("found", func(f *rankFixture) { f.rigor = "reproduced" })+"]")
	sig, picked := readcommon.TopOpenInline(entries)
	if sig != "found" {
		t.Errorf("TopOpenInline() sig = %q; want found", sig)
	}
	ranked := readcommon.Rank(entries, readcommon.StatusOpen, readcommon.KindInline)
	if len(ranked) == 0 || ranked[0].Entry != picked || rankSigs(ranked)[0] != sig {
		t.Errorf("TopOpenInline() = %q, %v; want the first open-inline Rank entry", sig, picked)
	}
}

// rankEntriesN builds n valid findings for the allocation and benchmark
// checks (setup runs outside the measured region).
func rankEntriesN(tb testing.TB, n int) []any {
	tb.Helper()
	out := make([]any, 0, n)
	for i := range n {
		entry := rankEntry(fmt.Sprintf("%040d", i), "open", "important", "walked", "inline", i+1)
		out = append(out, mustEntries(tb, "["+entry+"]")[0])
	}
	return out
}

// rankAllocEntries is the acceptance benchmark size.
const rankAllocEntries = 500

// maxRankAllocs bounds per-run allocations: the decorate-sort-undecorate
// shape allocates O(n) slices once, never one key slice per comparison.
const maxRankAllocs = 8

// TestRankAllocations pins the allocation shape: for 500 entries Rank stays
// at a handful of allocations instead of the old per-comparison key slices.
func TestRankAllocations(t *testing.T) {
	entries := rankEntriesN(t, rankAllocEntries)
	allocs := testing.AllocsPerRun(5, func() {
		_ = readcommon.Rank(entries, readcommon.StatusAll, readcommon.KindAll)
	})
	if allocs > maxRankAllocs {
		t.Errorf("Rank(%d entries) = %.0f allocs/op; want <= %d",
			rankAllocEntries, allocs, maxRankAllocs)
	}
}

// BenchmarkRank500 reports the ranked-sort cost at the acceptance size.
func BenchmarkRank500(b *testing.B) {
	entries := rankEntriesN(b, rankAllocEntries)
	b.ReportAllocs()
	for b.Loop() {
		_ = readcommon.Rank(entries, readcommon.StatusAll, readcommon.KindAll)
	}
}
