package panelcore

import (
	"errors"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestPlanDispatch(t *testing.T) {
	review := jq.NewObj("comments", []any{jq.NewObj("path", "src/db.go", "line", 42, "body", "claim\n## hostile")}, "body", "## section only")
	plan, dispatch, warnings, err := Plan("bundle", "head", "now", "synth\na\nb", "trusted", review)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 || strings.Count(dispatch, "\n## ") != 2 {
		t.Fatalf("unexpected dispatch: %s; warnings %v", dispatch, warnings)
	}
	if !strings.Contains(dispatch, "> claim\n> ## hostile") || strings.Contains(dispatch, "(synth)") || !strings.Contains(dispatch, "verifier 2/2 (b)") {
		t.Fatalf("dispatch did not preserve roster and quote finding: %s", dispatch)
	}
	if got := jq.PrettyOrdered(plan); !strings.Contains(got, `"dispatches": 2`) || !strings.Contains(got, `"dispatch": "bundle/verification/dispatch.md"`) {
		t.Fatalf("plan = %s", got)
	}
}

func TestPlanBounds(t *testing.T) {
	tests := []struct {
		name, head, roster, code string
		n                        int
	}{
		{"no roster", "head", "", "panel-empty", 0}, {"synth only", "head", "synth", "panel-empty", 0}, {"no head", "", "synth a", "panel-empty", 0},
		{"findings", "head", "synth a", "panel-too-large", 101}, {"verifiers", "head", "synth a b c d e", "panel-too-large", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := make([]any, tt.n)
			for i := range cs {
				cs[i] = jq.NewObj("path", "x", "body", "finding")
			}
			_, _, _, err := Plan("bundle", tt.head, "now", tt.roster, "prompt", jq.NewObj("comments", cs))
			var ref *core.RefusalError
			if !errors.As(err, &ref) || ref.Code != tt.code {
				t.Fatalf("refusal = %v, want %s", err, tt.code)
			}
		})
	}
}

func TestPlanEmptyAndOneVerifier(t *testing.T) {
	plan, dispatch, warnings, err := Plan("bundle", "head", "now", "synth a", "prompt", jq.NewObj("comments", []any{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(dispatch, "Nothing to verify") || !strings.Contains(jq.PrettyOrdered(plan), `"dispatches": 0`) {
		t.Fatalf("plan=%v dispatch=%s warnings=%v", plan, dispatch, warnings)
	}
}
