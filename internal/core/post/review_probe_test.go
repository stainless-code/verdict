package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestReviewDistinctLines(t *testing.T) {
	remote := []commentIdentity{identifyComment("head-a", projectComment(jq.NewObj("path", "a.go", "side", "RIGHT", "line", 10, "body", "Missing nil check."), ""))}
	comments := []any{projectComment(jq.NewObj("path", "a.go", "side", "RIGHT", "line", 10, "body", "Missing nil check."), ""), projectComment(jq.NewObj("path", "a.go", "side", "RIGHT", "line", 90, "body", "Missing nil check."), "")}
	if got := len(missingComments("head-a", comments, remote)); got != 1 {
		t.Fatalf("distinct same-body findings: missing %d, want 1", got)
	}
}
