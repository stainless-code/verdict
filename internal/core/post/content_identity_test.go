package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestMissingCommentsIdentity(t *testing.T) {
	t.Parallel()
	finding := jq.NewObj("path", "a.go", "side", "RIGHT", "line", 10, "body", "broken")
	remote := []commentIdentity{identifyComment("head-a", projectComment(finding, ""))}
	moved := jq.NewObj("path", "a.go", "line", 90, "position", 200, "body", "broken")
	if got := len(missingComments("head-a", []any{moved}, remote)); got != 0 {
		t.Fatalf("moved finding: missing %d, want 0", got)
	}
	if got := len(missingComments("head-b", []any{moved}, remote)); got != 1 {
		t.Fatalf("new head: missing %d, want 1", got)
	}
	changed := jq.NewObj("path", "a.go", "side", "RIGHT", "line", 10, "body", "another bug")
	if got := len(missingComments("head-a", []any{changed}, remote)); got != 1 {
		t.Fatalf("new finding: missing %d, want 1", got)
	}
}
