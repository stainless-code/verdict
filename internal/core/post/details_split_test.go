package postcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/jq"
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

// TestSplitKeepsPromptBlockAtomic proves the copy-me prompt block is one split
// unit at the real 65,536-byte hosting limit: the filler leaves part one with
// less room than the block needs, so a splitter that did not treat the region
// as atomic would land its opening tag in the first comment and the rest in the
// second. The block also references no `[ev-N]` definition, so a split part
// gains none from it.
func TestSplitKeepsPromptBlockAtomic(t *testing.T) {
	t.Parallel()
	const budget = hostingBodyLimit
	block := promptBlockText(t, renderPromptBody(t))
	if strings.Contains(block, "[ev-") {
		t.Fatalf("prompt block carries an [ev-N] reference:\n%s", block)
	}
	filler := strings.Repeat("f", budget-len(block)/2)
	input := filler + paragraphBreak + block + paragraphBreak + strings.Repeat("t", budget)
	parts := splitBody(input, budget)
	if len(parts) < 2 {
		t.Fatalf("splitBody produced %d part(s), want the block to force a split", len(parts))
	}
	if got := strings.Join(parts, paragraphBreak); got != input {
		t.Fatalf("split lost bytes: %d in, %d out", len(input), len(got))
	}
	assertPromptInOnePart(t, parts, block)
}

// assertPromptInOnePart requires exactly one part to carry the summary line and
// the whole block, so a boundary inside the block fails here.
func assertPromptInOnePart(t *testing.T, parts []string, block string) {
	t.Helper()
	carriers := 0
	for i, part := range parts {
		if !strings.Contains(part, "<summary>Prompt for AI agent</summary>") {
			continue
		}
		carriers++
		if !strings.Contains(part, block) {
			t.Errorf("part %d carries a partial prompt block:\n%s", i+1, part)
		}
	}
	if carriers != 1 {
		t.Errorf("prompt block landed in %d parts, want exactly 1", carriers)
	}
}

// renderPromptBody renders one review body carrying the prompt block through
// the production renderer, so the bytes under test are the ones a post sends.
func renderPromptBody(t *testing.T) string {
	t.Helper()
	f := jq.NewObj(
		"sig", "sig-split", "status", "open", "severity", "critical", "rigor", "ran",
		"anchored", true, "inBounds", true, "path", "src/auth.go", "start_line", 42, "line", 44,
		"claim", "Token check skips `exp` validation when `leeway` is zero.",
		"verification", jq.NewObj("quote", "verified"),
		"brief", jq.NewObj(
			"sites", []any{jq.NewObj("path", "src/middleware.go", "line", 19)},
			"outcome", "Reject expired tokens regardless of whether leeway is zero.",
			"approach", "Check `now.After(claims.ExpiresAt.Add(leeway))` unconditionally.",
			"questions", []any{"Should clock skew default to 30s when config omits leeway?"},
		),
	)
	return reviewdoc.Render(&reviewdoc.RenderInput{
		Review:    jq.NewObj("findings", []any{f}),
		Approval:  &core.ApprovalReceipt{Approved: false, Reason: "open findings", Tier: "important"},
		Kept:      []any{f},
		BaseURL:   "https://github.com/acme/app",
		HeadFull:  "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
		FixPrompt: true,
	})
}

func promptBlockText(t *testing.T, body string) string {
	t.Helper()
	const opening = "<details><summary>Prompt for AI agent</summary>"
	start := strings.Index(body, opening)
	if start < 0 {
		t.Fatalf("rendered body carries no prompt block:\n%s", body)
	}
	rest := body[start:]
	end := strings.Index(rest, "</details>")
	if end < 0 {
		t.Fatalf("prompt block never closes:\n%s", rest)
	}
	return rest[:end+len("</details>")]
}
