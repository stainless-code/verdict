package postcore

import (
	"strings"
	"testing"
)

// TestSplitKeepsDetailsAtomic proves a <details> region is one split unit: the
// hosting splitter never lands a part boundary between a block's opening tag
// and its closing one, so a posted topology block always renders. The filler
// is sized so the block straddles the budget — without the region awareness the
// first part keeps the fence and loses its </details>.
func TestSplitKeepsDetailsAtomic(t *testing.T) {
	t.Parallel()
	const budget = 4000
	block := "<details>\n<summary>Change topology (computed from the diff)</summary>\n\n" +
		"```mermaid\nflowchart TD\n" +
		"    PR[\"PR #4610 +12/-0\"]\n" +
		"    PR --> F1[\"F1 app.ts\"]\n" +
		"    PR --> F2[\"F2 theme.ts\"]\n" +
		"    F1 -->|\"uses theme.ts\"| F2\n" +
		"```\n\n</details>"
	body := strings.Repeat("f", budget-200) + "\n\n" + block + "\n\n" + strings.Repeat("t", budget)
	parts := splitBody(body, budget)
	if len(parts) < 2 {
		t.Fatalf("splitBody produced %d part(s), want the block to force a split", len(parts))
	}
	if got := strings.Join(parts, paragraphBreak); got != body {
		t.Fatalf("split lost bytes: %d in, %d out", len(body), len(got))
	}
	assertFenceParts(t, parts)
}

// TestBodyUnitsToleratesStrayClose proves an unbalanced closing tag never
// starts a region: the paragraphs around it stay separate units, so a stray
// tag cannot glue unrelated text into one split candidate.
func TestBodyUnitsToleratesStrayClose(t *testing.T) {
	t.Parallel()
	units := bodyUnits("a" + paragraphBreak + "</details>" + paragraphBreak + "b")
	if len(units) != 3 {
		t.Fatalf("bodyUnits produced %d units, want 3: %q", len(units), units)
	}
	for i, want := range []string{"a", "</details>", "b"} {
		if units[i] != want {
			t.Errorf("unit %d = %q, want %q", i, units[i], want)
		}
	}
}

// assertFenceParts proves the fence landed whole in one part, tags and all.
func assertFenceParts(t *testing.T, parts []string) {
	t.Helper()
	fences := 0
	for i, part := range parts {
		if !strings.Contains(part, "flowchart TD") {
			continue
		}
		fences++
		if !strings.Contains(part, "<details>") || !strings.Contains(part, "</details>") {
			t.Errorf("part %d carries the mermaid fence without its block tags:\n%s", i+1, part)
		}
	}
	if fences != 1 {
		t.Errorf("mermaid fence landed in %d parts, want exactly 1", fences)
	}
}
