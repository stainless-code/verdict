package readcommon_test

import (
	"fmt"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// mustEntries decodes a findings array for the rank tests.
func mustEntries(t *testing.T, doc string) []any {
	t.Helper()
	v, err := jq.DecodeOrdered([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := v.([]any)
	if !ok {
		t.Fatal("fixture is not an array")
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
