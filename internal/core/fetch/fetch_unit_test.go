package fetchcore

import (
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// diffFixture builds a two-file diff with known per-file byte sizes.
func diffFixture() string {
	var sb strings.Builder
	sb.WriteString("diff --git a/one.txt b/one.txt\n")
	sb.WriteString("--- a/one.txt\n+++ b/one.txt\n@@ -1 +1 @@\n-a\n+b\n")
	sb.WriteString("diff --git a/two.txt b/two.txt\n")
	sb.WriteString("--- a/two.txt\n+++ b/two.txt\n@@ -1 +1 @@\n-c\n+d\n")
	return sb.String()
}

// TestChunkFileTableSizes exercises the fetch helper.
func TestChunkFileTableSizes(t *testing.T) {
	t.Parallel()
	fixture := diffFixture()
	table := chunkFileTable(writeTemp(t, "full.diff", fixture))
	if len(table) != 2 {
		t.Fatalf("table has %d slices, want 2", len(table))
	}
	if table[0].path != "one.txt" || table[1].path != "two.txt" {
		t.Fatalf("paths = %q %q, want one.txt two.txt", table[0].path, table[1].path)
	}
	if table[0].size != table[1].size {
		t.Fatalf("sizes differ: %d vs %d", table[0].size, table[1].size)
	}
	if table[0].size+table[1].size != int64(len(fixture)) {
		t.Fatalf("sizes sum to %d, want file size %d", table[0].size+table[1].size, len(fixture))
	}
}

// TestChunkPlanOK exercises the fetch helper.
func TestChunkPlanOK(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, "full.diff", diffFixture())
	fi, serr := os.Stat(path)
	if serr != nil {
		t.Fatal(serr)
	}
	half := fi.Size() / 2
	if !chunkPlanOK(path, fi.Size()) {
		t.Fatal("plan reads ok at the total size")
	}
	if chunkPlanOK(path, half-1) {
		t.Fatal("plan reads refusing below a file slice")
	}
	if chunkPlanOK(writeTemp(t, "empty.diff", ""), 512000) {
		t.Fatal("empty diff has no plan")
	}
}

// TestPackChunksNextFit exercises the fetch helper.
func TestPackChunksNextFit(t *testing.T) {
	t.Parallel()
	table := []fileSlice{{size: 60, path: "a"}, {size: 60, path: "b"}, {size: 60, path: "c"}}
	plan := packChunks(table, 100)
	if len(plan) != 3 {
		t.Fatalf("plan has %d chunks, want 3 (60+60 never fits 100)", len(plan))
	}
	plan = packChunks(table, 120)
	if len(plan) != 2 {
		t.Fatalf("plan has %d chunks, want 2", len(plan))
	}
	if len(plan[0].members) != 2 || plan[0].members[0].path != "a" {
		t.Fatalf("first chunk = %+v, want [a b]", plan[0].members)
	}
	// Deterministic: same bytes always yield the same map.
	again := packChunks(table, 120)
	if len(again) != len(plan) || again[0].estSize != plan[0].estSize {
		t.Fatal("repack differs")
	}
}

// TestSuggestSplits exercises the fetch helper.
func TestSuggestSplits(t *testing.T) {
	t.Parallel()
	fixture := diffFixture()
	cands := suggestSplits(writeTemp(t, "full.diff", fixture), int64(len(fixture)))
	if len(cands) != 3 {
		t.Fatalf("candidates = %d, want 3", len(cands))
	}
	first, ok := cands[0].(*jq.Obj)
	if !ok {
		t.Fatalf("first is %T", cands[0])
	}
	kind, _ := first.Get("kind")
	est, _ := first.Get("estimated_bytes")
	if kind != "paths" || est != int64(len(fixture)/2) {
		t.Fatalf("first = %s", first.MarshalCompact())
	}
	last, ok := cands[2].(*jq.Obj)
	if !ok {
		t.Fatalf("last is %T", cands[2])
	}
	lkind, _ := last.Get("kind")
	if lkind != "range" {
		t.Fatalf("last = %s", last.MarshalCompact())
	}
	if got := suggestSplits(writeTemp(t, "empty.diff", ""), 0); len(got) != 0 {
		t.Fatalf("empty diff candidates = %v", got)
	}
}

// TestRound4 exercises the fetch helper.
func TestRound4(t *testing.T) {
	t.Parallel()
	if got := readcommon.Round4(1.0 / 3.0); got != 0.3333 {
		t.Fatalf("readcommon.Round4(1/3) = %v", got)
	}
	if got := readcommon.Round4(2.5); got != 2.5 {
		t.Fatalf("readcommon.Round4(2.5) = %v", got)
	}
}

// TestPickEstimateTier exercises the fetch helper.
func TestPickEstimateTier(t *testing.T) {
	t.Parallel()
	recs := []costRec{
		{tier: "cheap", usd: "1.00", has: true},
		{tier: "cheap", usd: "3.00", has: true},
		{tier: "standard", usd: "0.50", has: true},
	}
	tier, runs, stat, ok := pickEstimateTier(recs)
	if !ok || tier != "cheap" || runs != 2 || stat.mean != "2" {
		t.Fatalf("pick = %s/%d/%s/%v, want cheap/2/2/true", tier, runs, stat.mean, ok)
	}
	// Ties break toward the deeper tier.
	tied := []costRec{{tier: "cheap", usd: "1", has: true}, {tier: "deep", usd: "9", has: true}}
	tier, _, _, _ = pickEstimateTier(tied)
	if tier != "deep" {
		t.Fatalf("tie pick = %s, want deep", tier)
	}
	// Unpriced tier reads unknown.
	unpriced := []costRec{{tier: "standard"}}
	_, _, stat, ok = pickEstimateTier(unpriced)
	if !ok || stat.mean != "" {
		t.Fatalf("unpriced = %q/%v, want empty/true", stat.mean, ok)
	}
	if _, _, _, ok := pickEstimateTier(nil); ok {
		t.Fatal("empty records read ok")
	}
}

// TestPickEstimateTierMeasured proves a handed usage record prices
// the run and names its free-form source, while the self-reported value stays
// the fallback for runs with no measured twin.
func TestPickEstimateTierMeasured(t *testing.T) {
	t.Parallel()
	measured := 0.75
	recs := []costRec{
		{tier: "standard", usd: "5.00", has: true, measured: &measured, usageSource: "bench-harness"},
		{tier: "standard", usd: "0.25", has: true},
	}
	tier, runs, stat, ok := pickEstimateTier(recs)
	if !ok || tier != "standard" || runs != 2 {
		t.Fatalf("pick = %s/%d/%v", tier, runs, ok)
	}
	if stat.mean != "0.5" || stat.priced != 2 || stat.measured != 1 {
		t.Fatalf("stat = %+v, want mean 0.5 with 1 measured of 2 priced", stat)
	}
	if len(stat.harness) != 1 || stat.harness[0] != "bench-harness" {
		t.Fatalf("harness = %v", stat.harness)
	}
}

// TestEstimateObjectBasis proves the measured basis keys are additive-only:
// a self-reported estimate keeps the legacy object, a measured one names its
// source, and a partial split reads mixed.
func TestEstimateObjectBasis(t *testing.T) {
	t.Parallel()
	st := &budgetState{estUSD: "1.5", estRuns: 2}
	obj := estimateObject(st, "standard")
	for _, key := range []string{"basis", "harness", "measured_runs"} {
		if obj.Has(key) {
			t.Fatalf("self-reported estimate carries %q", key)
		}
	}
	st.recordBasis(tierStat{measured: 2, priced: 2, harness: []string{"claude-code"}})
	obj = estimateObject(st, "standard")
	if got, _ := obj.Get("basis"); got != "measured" {
		t.Fatalf("basis = %v, want measured", got)
	}
	if !obj.Has("harness") || !obj.Has("measured_runs") {
		t.Fatalf("measured estimate misses harness/measured_runs: %s", obj.MarshalCompact())
	}
	if !strings.Contains(st.basisNote(), "measured from 2 harness session(s)") {
		t.Fatalf("basisNote = %q", st.basisNote())
	}
}

// TestRecordBasisMixed proves a partly measured tier reads mixed, never
// measured.
func TestRecordBasisMixed(t *testing.T) {
	t.Parallel()
	st := &budgetState{}
	st.recordBasis(tierStat{measured: 1, priced: 2, harness: []string{"opencode"}})
	if st.estBasis != "mixed" {
		t.Fatalf("partial basis = %q, want mixed", st.estBasis)
	}
	if !strings.Contains(st.basisNote(), "partly measured") {
		t.Fatalf("mixed basisNote = %q", st.basisNote())
	}
}

// TestIsBudgetNumber exercises the fetch helper.
func TestIsBudgetNumber(t *testing.T) {
	t.Parallel()
	for _, good := range []string{"0", "0.50", "10", "10.25", "007", "99999999999999999999"} {
		if !isBudgetNumber(good) {
			t.Fatalf("%q reads invalid", good)
		}
	}
	for _, bad := range []string{"", "abc", "1.", ".5", "-1", "1.2.3", " 1", "$1"} {
		if isBudgetNumber(bad) {
			t.Fatalf("%q reads valid", bad)
		}
	}
}

// TestPromptNonceShape exercises the fetch helper.
func TestPromptNonceShape(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 10 {
		n := promptNonce()
		if len(n) != 16 {
			t.Fatalf("nonce %q has length %d", n, len(n))
		}
		if !isLowerHex(n) {
			t.Fatalf("nonce %q is not lowercase hex", n)
		}
		seen[n] = true
	}
	if len(seen) < 2 {
		t.Fatal("nonces never vary")
	}
}

// isLowerHex reports whether s is all [0-9a-f].
func isLowerHex(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// TestFenceSectionNewlineGuard exercises the fetch helper.
func TestFenceSectionNewlineGuard(t *testing.T) {
	t.Parallel()
	without := fenceSection("abc123", "full.diff", writeTemp(t, "a.diff", "line\n"))
	if !strings.HasSuffix(without, "<<<END UNTRUSTED nonce=abc123>>>\n") {
		t.Fatalf("fence = %q", without)
	}
	if strings.Count(without, "\n") != 3 {
		t.Fatalf("fence has %d lines, want 3", strings.Count(without, "\n"))
	}
	// A file without a trailing newline gains one before the close marker.
	unterminated := fenceSection("abc123", "full.diff", writeTemp(t, "b.diff", "line"))
	if !strings.Contains(unterminated, "line\n<<<END UNTRUSTED nonce=abc123>>>\n") {
		t.Fatalf("unterminated fence = %q", unterminated)
	}
}

// TestScopeMatch exercises the fetch helper.
func TestScopeMatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"*", "anything/at/all", true},
		{"src/*.go", "src/a.go", true},
		{"src/*.go", "src/sub/a.go", true},
		{"*.go", "a.go", true},
		{"*.go", "a.txt", false},
		{"docs/**", "docs/a/b.md", true},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"*.[ch]", "x.c", true},
		{"*.[ch]", "x.go", false},
		{"*[!0-9]*", "a1", true},
		{"*[!0-9]*", "1", false},
		{`a\*b`, "a*b", true},
		{`a\*b`, "axb", false},
		{"", "", true},
		{"", "x", false},
		{"exact", "exact", true},
	}
	for _, c := range cases {
		if got := scopeMatch(c.pattern, c.s); got != c.want {
			t.Errorf("scopeMatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

// TestSensPathReasons exercises the fetch helper.
func TestSensPathReasons(t *testing.T) {
	t.Parallel()
	if got := sensPathReasons("config/.env"); len(got) != 1 || got[0] != "filename" {
		t.Fatalf(".env = %v", got)
	}
	if got := sensPathReasons(".env.example"); len(got) != 0 {
		t.Fatalf(".env.example = %v", got)
	}
	if got := sensPathReasons("certs/key.pem"); len(got) != 1 || got[0] != "extension" {
		t.Fatalf("key.pem = %v", got)
	}
	if got := sensPathReasons("src/a.go"); len(got) != 0 {
		t.Fatalf("a.go = %v", got)
	}
	if got := sensPathReasons(""); len(got) != 0 {
		t.Fatalf("empty = %v", got)
	}
}

// TestSensAllHitsQuotedHeader proves the sensitive section walk reads
// headers through the shared quote/space-safe parser: a C-quoted file with
// spaces still flags under its decoded path.
func TestSensAllHitsQuotedHeader(t *testing.T) {
	t.Parallel()
	raw := "diff --git \"a/my key.pem\" \"b/my key.pem\"\n@@ -1 +1 @@\n+x\n"
	hits := sensAllHits(raw, "")
	if len(hits) != 2 || hits[0].path != "my key.pem" || hits[1].path != "my key.pem" {
		t.Fatalf("sensAllHits = %v; want both sides of my key.pem", hits)
	}
}

// TestDedupeHits exercises the fetch helper.
func TestDedupeHits(t *testing.T) {
	t.Parallel()
	got := dedupeHits([]sensHit{
		{path: "b", reason: "extension"},
		{path: "a", reason: "filename"},
		{path: "a", reason: "content"},
		{path: "a", reason: "filename"},
		{path: "/dev/null", reason: "filename"},
	})
	if len(got) != 2 || got[0] != "a (filename,content)" || got[1] != "b (extension)" {
		t.Fatalf("hits = %v", got)
	}
}

// TestRolesForPaths exercises the fetch helper.
func TestRolesForPaths(t *testing.T) {
	t.Parallel()
	got := rolesForPaths([]string{"src/a.go", "README.md"})
	want := "## docs\n- README.md\n\n## source\n- src/a.go\n"
	if got != want {
		t.Fatalf("roles = %q, want %q", got, want)
	}
	// The degenerate single-empty-line input.
	if got := rolesForPathsText(""); got != "## source\n- \n" {
		t.Fatalf("empty roles = %q", got)
	}
	if got := rolesTextEmpty(nil); got != "" {
		t.Fatalf("empty jq roles = %q", got)
	}
}

// TestLocalSlug exercises the fetch helper.
func TestLocalSlug(t *testing.T) {
	t.Parallel()
	if got := localSlug("My Fix_2"); got != "my-fix-2" {
		t.Fatalf("slug = %q", got)
	}
	if got := localSlug("---"); got != "" {
		t.Fatalf("dashes slug = %q", got)
	}
	if got := localSlug(strings.Repeat("a", 60)); len(got) != 48 {
		t.Fatalf("long slug length = %d", len(got))
	}
	if !readcommon.IsDigits("4351") || readcommon.IsDigits("43a1") || readcommon.IsDigits("") {
		t.Fatal("IsDigits wrong")
	}
}

// TestNumOrZero exercises the fetch helper.
func TestNumOrZero(t *testing.T) {
	t.Parallel()
	if numOrZero("12") != 12 || numOrZero("-") != 0 || numOrZero("") != 0 || numOrZero("12x") != 0 {
		t.Fatal("numOrZero wrong")
	}
	if numOrZero("007") != 7 || numOrZero("99999999999999999999") != 0 || numOrZero("-3") != 0 {
		t.Fatal("numOrZero overflow/sign wrong")
	}
}

// TestIntOrZero exercises the local numstat helper: leading zeros parse and
// an overflow reads zero instead of a wrapped count.
func TestIntOrZero(t *testing.T) {
	t.Parallel()
	if intOrZero("12") != 12 || intOrZero("007") != 7 || intOrZero("") != 0 || intOrZero("12x") != 0 {
		t.Fatal("intOrZero wrong")
	}
	if intOrZero("99999999999999999999") != 0 || intOrZero("9223372036854775808") != 0 {
		t.Fatal("intOrZero overflow wrong")
	}
}

// TestCafeItems exercises the fetch helper.
func TestCafeItems(t *testing.T) {
	t.Parallel()
	items := cafeItems(`{"data":{"items":[{"path":"a"}]}}`)
	if len(items) != 1 {
		t.Fatalf("envelope items = %v", items)
	}
	if items := cafeItems(`not json`); len(items) != 0 {
		t.Fatalf("garbage items = %v", items)
	}
}

// TestMergeAdd exercises the fetch helper.
func TestMergeAdd(t *testing.T) {
	t.Parallel()
	merged, ok := mergeAdd([]any{[]any{1.0}, []any{2.0}})
	marr, mok := merged.([]any)
	if !ok || !mok || len(marr) != 2 {
		t.Fatalf("merge = %v %v", merged, ok)
	}
	if _, ok := mergeAdd([]any{[]any{1.0}, "x"}); ok {
		t.Fatal("mixed merge reads ok")
	}
	merged, ok = mergeAdd(nil)
	marr, mok = merged.([]any)
	if !ok || !mok || len(marr) != 0 {
		t.Fatal("empty merge wrong")
	}
}

// TestUpstreamRefusalSignal exercises the fetch helper.
func TestUpstreamRefusalSignal(t *testing.T) {
	t.Parallel()
	if upstreamDiffRefusal("api error: diff too large to be rendered") == nil {
		t.Fatal("large-diff signal missed")
	}
	if upstreamDiffRefusal("HTTP 403: rate limit exceeded") != nil {
		t.Fatal("rate-limit misfires as upstream")
	}
	if upstreamDiffRefusal("connection refused") != nil {
		t.Fatal("plain failure misfires as upstream")
	}
}
