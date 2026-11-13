package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestCRForgejoPositionPairsExactOccurrence(t *testing.T) {
	t.Parallel()
	for _, example := range []struct {
		side               string
		position, original int
	}{{"RIGHT", 10, 10}, {"LEFT", 0, 10}} {
		at := func(line int) *jq.Obj {
			return projectComment(jq.NewObj("path", "a.go", "side", example.side, "line", line, "body", "Same finding."), "")
		}
		posted := at(10)
		body, _ := posted.Get("body")
		head, comment := remoteComment(map[string]any{"commit_id": "head", "path": "a.go", "position": example.position, "original_position": example.original, "body": body})
		missing := missingComments("head", []any{at(90), at(10)}, []commentIdentity{identifyComment(head, comment)})
		line := crMissingLine(t, missing)
		if line != 90 {
			t.Fatalf("%s missing line %v, want 90", example.side, line)
		}
	}
}

func TestCRForgejoDraftCommentsExcluded(t *testing.T) {
	t.Parallel()
	st := crVerdictState(t, "COMMENT")
	finding := projectComment(jq.NewObj("path", "a.go", "line", 10, "body", "Draft finding."), "")
	body, _ := finding.Get("body")
	calls := 0
	read := func(url string) ([]map[string]any, error) {
		calls++
		if url != "reviews/2/comments?limit=50&page=1" {
			t.Fatalf("draft comments read: %s", url)
		}
		return []map[string]any{{"path": "a.go", "position": 10, "body": body}}, nil
	}
	remote, err := forgejoPublication("reviews", []map[string]any{
		{"id": 1, "state": "PENDING", "body": st.body, "commit_id": st.commitID},
		{"id": 2, "state": "COMMENT", "body": st.body, "commit_id": st.commitID},
	}, read)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(remote.comments) != 1 || len(remote.reviews) != 1 {
		t.Fatalf("publication = %+v, calls %d", remote, calls)
	}
}

func crMissingLine(t *testing.T, missing []any) any {
	t.Helper()
	if len(missing) != 1 {
		t.Fatalf("missing %d, want 1", len(missing))
	}
	obj, ok := missing[0].(*jq.Obj)
	if !ok {
		t.Fatal("missing comment shape")
	}
	line, _ := obj.Get("line")
	return line
}
