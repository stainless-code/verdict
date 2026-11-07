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
