package kb_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

func writeKB(t *testing.T, dir string, lines ...string) {
	t.Helper()
	var sb strings.Builder
	for _, ln := range lines {
		sb.WriteString(ln + "\n")
	}
	content := sb.String()
	if err := os.MkdirAll(filepath.Join(dir, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".verdict", "kb.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

// TestNormalizeExplicitVsAbsent checks absent/null/[]/mixed/broken cites.
func TestNormalizeExplicitVsAbsent(t *testing.T) {
	dir := t.TempDir()
	writeKB(t, dir, `{"id":"rec-rule","status":"active"}`)
	c := kb.New(func(string) {})
	c.Root = dir
	entry := func(v any, has bool) *jq.Obj {
		o := jq.NewObj()
		if has {
			o.Set("kb", v)
		}
		return o
	}
	checkAbsentNil(t, c, entry)
	checkExplicitEmpty(t, c, entry)
	checkMixedDrops(t, entry)
	checkBrokenEmpty(t, entry)
}

// checkAbsentNil checks absent and null kb read nil (keep prior cite).
func checkAbsentNil(t *testing.T, c *kb.Ctx, entry func(any, bool) *jq.Obj) {
	t.Helper()
	if got := c.NormalizeEntry(entry(nil, false), true); got != nil {
		t.Errorf("absent kb = %v; want nil (keep prior cite)", got)
	}
	if got := c.NormalizeEntry(entry(nil, true), true); got != nil {
		t.Errorf("null kb = %v; want nil (keep prior cite)", got)
	}
}

// checkExplicitEmpty checks explicit [] reads empty (clear).
func checkExplicitEmpty(t *testing.T, c *kb.Ctx, entry func(any, bool) *jq.Obj) {
	t.Helper()
	got := c.NormalizeEntry(entry([]any{}, true), true)
	arr, ok := got.([]any)
	if !ok || len(arr) != 0 {
		t.Errorf("explicit [] kb = %#v; want empty list (clear)", got)
	}
}

// checkMixedDrops checks present ids normalize with warn-and-drop.
func checkMixedDrops(t *testing.T, entry func(any, bool) *jq.Obj) {
	t.Helper()
	var warns []string
	c2 := kb.New(func(m string) { warns = append(warns, m) })
	got := c2.NormalizeEntry(entry([]any{"rec-rule", "nope-missing", 123}, true), true)
	arr, ok := got.([]any)
	if !ok || len(arr) != 1 || arr[0] != "rec-rule" {
		t.Errorf("mixed kb = %#v; want [rec-rule]", got)
	}
	if len(warns) != 2 {
		t.Errorf("warns = %v; want 2 drop warns", warns)
	}
}

// checkBrokenEmpty checks a broken store reads [] (cites ignored).
func checkBrokenEmpty(t *testing.T, entry func(any, bool) *jq.Obj) {
	t.Helper()
	c3 := kb.New(func(string) {})
	c3.Broken = true
	got := c3.NormalizeEntry(entry([]any{"rec-rule"}, true), true)
	if arr, ok := got.([]any); !ok || len(arr) != 0 {
		t.Errorf("broken-store kb = %#v; want []", got)
	}
}

// TestCreditSkipsBundledPackRuleQuietly proves a flip of a finding citing a
// bundled pack rule stages no credit and warns nothing (pack rules carry no
// counters), while a cite with no store row and no bundle listing still warns.
func TestCreditSkipsBundledPackRuleQuietly(t *testing.T) {
	dir := t.TempDir()
	writeKB(t, dir, `{"id":"rec-rule","status":"active"}`)
	var warns []string
	c := kb.New(func(m string) { warns = append(warns, m) })
	c.Root = dir
	c.Bundled = map[string]bool{"sec-injection": true}
	entry := jq.NewObj("sig", "s1", "kb", []any{"sec-injection", "nope-missing"})
	c.ApplyCredit([]any{entry}, "s1", "addressed", "human", "triage", "t", true)
	if len(c.Staged) != 0 {
		t.Errorf("staged = %v, want none", c.Staged)
	}
	want := []string{"kb credit skipped: rule 'nope-missing' not active in " + filepath.ToSlash(filepath.Join(dir, ".verdict/kb.jsonl"))}
	if strings.Join(warns, "\n") != strings.Join(want, "\n") {
		t.Errorf("warns = %q, want %q", warns, want)
	}
}

// TestBundledRulesReadsPackRowsOnly proves only manifest rows fetch rendered
// from a pack rule count as bundled: a store row must still pass the store.
func TestBundledRulesReadsPackRowsOnly(t *testing.T) {
	dir := t.TempDir()
	manifest := `[{"id":"store-rule","score":0.5,"hits":0,"judged":0},` +
		`{"id":"sec-injection","score":0.5,"hits":0,"judged":0,"severity":"critical","source":"pack:verdict/security@1.1.0"}]`
	if err := os.WriteFile(filepath.Join(dir, "kb-recall.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	got := kb.BundledRules(dir)
	if len(got) != 1 || !got["sec-injection"] {
		t.Errorf("BundledRules = %v, want only sec-injection", got)
	}
	if got := kb.BundledRules(t.TempDir()); len(got) != 0 {
		t.Errorf("BundledRules without a manifest = %v, want empty", got)
	}
}
