package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestCRForgejoPositionPairsExactOccurrence(t *testing.T) {
	t.Parallel()
	for _, example := range []struct {
		position, original int
		side               string
	}{{10, 10, "RIGHT"}, {0, 10, "LEFT"}} {
		at := func(line int) *jq.Obj {
			return projectComment(jq.NewObj("path", "a.go", "side", example.side, "line", line, "body", "Same finding."), "")
		}
		posted := at(10)
		body, _ := posted.Get("body")
		head, comment := remoteComment(map[string]any{"commit_id": "head", "path": "a.go", "position": example.position, "original_position": example.original, "body": body})
		missing := missingComments("head", []any{at(90), at(10)}, []commentIdentity{identifyComment(head, comment)})
		if len(missing) != 1 {
			t.Fatalf("%s missing %d, want 1", example.side, len(missing))
		}
		obj, ok := missing[0].(*jq.Obj)
		if !ok {
			t.Fatal("missing comment shape")
		}
		line, _ := obj.Get("line")
		if line != 90 {
			t.Fatalf("%s missing line %v, want 90", example.side, line)
		}
	}
}
