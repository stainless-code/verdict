package recordcore_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// categoryComment renders one store-contract comment carrying raw category
// JSON (a quoted value, null, or absent).
func categoryComment(t *testing.T, cat string) string {
	t.Helper()
	extra := ""
	if cat != "absent" {
		extra = `,"category":` + cat
	}
	return `{"path":"notes.txt","line":2,"side":"RIGHT","body":"Cited finding here with enough substance."` + extra + `}`
}

func categoryReview(t *testing.T, cats ...string) string {
	t.Helper()
	comments := make([]string, 0, len(cats))
	for _, c := range cats {
		comments = append(comments, categoryComment(t, c))
	}
	return `{"schema_version":"1","body":"` + storeBody + `","approved":false,"comments":[` +
		strings.Join(comments, ",") + `]}`
}

// noteCategory records one stored entry's category label for the round-trip
// assertion: an explicit null reads as "null", a missing key fails the test.
func noteCategory(t *testing.T, seen map[string]bool, e map[string]any) {
	t.Helper()
	v, ok := e["category"]
	if !ok {
		t.Errorf("entry %v carries no category key", e["sig"])
		return
	}
	s, isStr := v.(string)
	if v == nil {
		seen["null"] = true
		return
	}
	if !isStr {
		t.Errorf("entry category = %#v, want a string or null", v)
		return
	}
	seen[s] = true
}

// TestCategoryRoundTrip proves all eight values plus explicit null survive
// review.json into findings.json, and that category wires immediately after
// severity on fresh entries.
func TestCategoryRoundTrip(t *testing.T) {
	cats := []string{`"logic"`, `"edge-case"`, `"security"`, `"performance"`, `"accidental-commit"`, `"style"`, `"docs"`, `"other"`, `null`}
	writeStoreBundle(t, "local-cat", categoryReview(t, cats...))
	c := &collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-cat")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	entries := storeEntries(t, "local-cat")
	if len(entries) != len(cats) {
		t.Fatalf("findings = %d, want %d", len(entries), len(cats))
	}
	seen := map[string]bool{}
	for _, e := range entries {
		noteCategory(t, seen, e)
	}
	for _, want := range []string{"logic", "edge-case", "security", "performance", "accidental-commit", "style", "docs", "other", "null"} {
		if !seen[want] {
			t.Errorf("category %q missing from the store; seen = %v", want, seen)
		}
	}
	checkWireOrder(t, ".review/local-cat/findings.json")
}

// checkWireOrder proves category wires immediately after severity on fresh
// entries, before the sig tail.
func checkWireOrder(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read findings.json: %v", err)
	}
	text := string(raw)
	sev, cat, sig := strings.Index(text, `"severity"`), strings.Index(text, `"category"`), strings.Index(text, `"sig"`)
	if sev < 0 || cat < 0 || sig < 0 || sev >= cat || cat >= sig {
		t.Errorf("wire order is not severity then category then sig: %d %d %d", sev, cat, sig)
	}
}

// TestCategoryNotInSig proves adding or changing a category never forks a
// finding's identity.
func TestCategoryNotInSig(t *testing.T) {
	writeStoreBundle(t, "local-sigcat", categoryReview(t, "absent"))
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, "local-sigcat")); ref != nil {
		t.Fatalf("plain Run refusal: %v", ref)
	}
	plain := storeEntries(t, "local-sigcat")
	writeFile(t, ".review/local-sigcat/review.json", categoryReview(t, `"security"`))
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, "local-sigcat")); ref != nil {
		t.Fatalf("categorized Run refusal: %v", ref)
	}
	categorized := storeEntries(t, "local-sigcat")
	if len(plain) != 1 || len(categorized) != 1 {
		t.Fatalf("findings = %d/%d, want one each", len(plain), len(categorized))
	}
	if plain[0]["sig"] != categorized[0]["sig"] {
		t.Errorf("sig moved with the category: %v vs %v", plain[0]["sig"], categorized[0]["sig"])
	}
}

// runBadCategory records one review carrying an unknown category and returns
// the response plus the warnings the run reported.
func runBadCategory(t *testing.T, slug string, dryRun bool) (*core.RecordResponse, []string) {
	t.Helper()
	writeStoreBundle(t, slug, categoryReview(t, `"sec"`))
	c := &collector{}
	req := recordRequest(t, c, slug)
	req.DryRun = dryRun
	resp, ref := recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("dryRun=%v Run refusal: %v", dryRun, ref)
	}
	return resp, c.warns
}

// countWarnings counts warnings containing want.
func countWarnings(warns []string, want string) int {
	n := 0
	for _, w := range warns {
		if strings.Contains(w, want) {
			n++
		}
	}
	return n
}

// TestUnknownCategoryWarnsNull proves a taxonomy typo warns once and stores
// null while the review still posts.
func TestUnknownCategoryWarnsNull(t *testing.T) {
	resp, warns := runBadCategory(t, "local-badcat", false)
	if n := countWarnings(warns, `unknown category "sec"`); n != 1 {
		t.Errorf("unknown-category warnings = %d, want exactly one: %v", n, warns)
	}
	entries := storeEntries(t, "local-badcat")
	if len(entries) != 1 {
		t.Fatalf("findings = %d, want 1", len(entries))
	}
	if v, ok := entries[0]["category"]; !ok || v != nil {
		t.Errorf("entry category = %#v (present=%v), want stored null", entries[0]["category"], ok)
	}
	if resp.Data.Items[0].Sig == "" {
		t.Error("recorded item lost its sig")
	}
}

// TestUnknownCategoryWarnsNullDryRun proves the same warn-once contract on
// the record --dry-run path, which writes no store.
func TestUnknownCategoryWarnsNullDryRun(t *testing.T) {
	resp, warns := runBadCategory(t, "local-badcat-dry", true)
	if n := countWarnings(warns, `unknown category "sec"`); n != 1 {
		t.Errorf("unknown-category warnings = %d, want exactly one: %v", n, warns)
	}
	if len(resp.Data.ItemsRaw) != 1 {
		t.Fatalf("dryRun items = %d, want 1", len(resp.Data.ItemsRaw))
	}
}

// TestNonStringCategory proves a non-string category warns and stores null
// instead of refusing the review.
func TestNonStringCategory(t *testing.T) {
	writeStoreBundle(t, "local-numcat", categoryReview(t, `3`))
	c := &collector{}
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-numcat")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	entries := storeEntries(t, "local-numcat")
	if len(entries) != 1 {
		t.Fatalf("findings = %d, want 1", len(entries))
	}
	if v, ok := entries[0]["category"]; !ok || v != nil {
		t.Errorf("entry category = %#v (present=%v), want stored null", entries[0]["category"], ok)
	}
	if !hasWarning(c.warns, "storing null") {
		t.Errorf("warnings = %v, want the store-null warning", c.warns)
	}
}

// TestCategoryPreservedAndClearedOnRepost proves the re-post merge rule: an
// absent category preserves the prior label, an explicit null clears it.
func TestCategoryPreservedAndClearedOnRepost(t *testing.T) {
	writeStoreBundle(t, "local-repost", categoryReview(t, `"security"`))
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, "local-repost")); ref != nil {
		t.Fatalf("first Run refusal: %v", ref)
	}
	writeFile(t, ".review/local-repost/review.json", categoryReview(t, "absent"))
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, "local-repost")); ref != nil {
		t.Fatalf("absent re-post refusal: %v", ref)
	}
	if got := storeEntries(t, "local-repost")[0]["category"]; got != "security" {
		t.Errorf("absent re-post category = %#v, want the preserved label", got)
	}
	writeFile(t, ".review/local-repost/review.json", categoryReview(t, `null`))
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, "local-repost")); ref != nil {
		t.Fatalf("explicit-null re-post refusal: %v", ref)
	}
	cleared := storeEntries(t, "local-repost")[0]
	if v, ok := cleared["category"]; !ok || v != nil {
		t.Errorf("explicit-null re-post category = %#v (present=%v), want cleared null", v, ok)
	}
}

// TestSectionCategoryNull proves section findings carry an explicit null
// category, never an inferred label.
func TestSectionCategoryNull(t *testing.T) {
	review := `{"schema_version":"1","body":"` + storeBody + `\n\n### Watch\n\nProse under the watch heading.",` +
		`"approved":false,"comments":[]}`
	writeStoreBundle(t, "local-seccat", review)
	if _, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, "local-seccat")); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	entries := storeEntries(t, "local-seccat")
	if len(entries) != 1 {
		t.Fatalf("findings = %d, want the one section", len(entries))
	}
	if v, ok := entries[0]["category"]; !ok || v != nil {
		t.Errorf("section category = %#v (present=%v), want explicit null", entries[0]["category"], ok)
	}
}

// TestCategoryCheckerWarnsNeverRefuses proves the shared checker reports an
// unknown or non-string category as a warning, so strict-off record proceeds
// and strict-on promotes it like every other warning.
func TestCategoryCheckerWarnsNeverRefuses(t *testing.T) {
	review := checkReview(`,"comments":[` + checkComment(`,"category":"sec"`) + `,` +
		checkComment(`,"category":3`) + `]`)
	result := recordcore.CheckReview([]byte(review), checkDiff)
	if result.Errors != 0 {
		t.Fatalf("errors = %d, want none: %+v", result.Errors, result.Problems)
	}
	unknown, wrongType := scanCategoryProblems(t, result.Problems)
	if !unknown || !wrongType {
		t.Errorf("category problems = %+v, want one bad-enum and one wrong-type warning", result.Problems)
	}
}

// scanCategoryProblems folds the checker's category problems: every one must
// warn, and the suite wants one bad-enum plus one wrong-type warning.
func scanCategoryProblems(t *testing.T, problems []core.Problem) (unknown, wrongType bool) {
	t.Helper()
	for _, p := range problems {
		if !strings.HasSuffix(p.Path, ".category") {
			continue
		}
		if p.Level != core.ProblemLevelWarning {
			t.Errorf("category problem level = %q, want warning: %+v", p.Level, p)
		}
		switch p.Code {
		case core.ProblemCodeBadEnum:
			unknown = true
		case core.ProblemCodeWrongType:
			wrongType = true
		}
	}
	return unknown, wrongType
}
