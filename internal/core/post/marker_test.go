package postcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestBuildPostCommentsMarksInline proves every posted inline comment carries
// verdict's hidden finding marker as its final line, and that a comment with
// no sig (an unmatchable entry) posts unmarked rather than mislinked.
func TestBuildPostCommentsMarksInline(t *testing.T) {
	t.Parallel()
	sig := strings.Repeat("a", 40)
	kept := []any{
		projectCommentFixture(),
		jq.NewObj(fieldPath, "b.txt", fieldLine, 4, "side", "LEFT", fieldBody, "plain"),
	}
	got := buildPostComments(kept, []string{sig, ""})
	if len(got) != 2 {
		t.Fatalf("comments = %d", len(got))
	}
	first, ok := got[0].(*jq.Obj)
	if !ok {
		t.Fatal("projected comment is not an object")
	}
	body := jq.R(mustGet(first))
	want := "seen\n\n```suggestion\nfix();\n```\n\n" + threadscore.FindingMarker(sig)
	if body != want {
		t.Errorf("marked body = %q, want %q", body, want)
	}
	if _, linked := threadscore.FindingSig(body); !linked {
		t.Error("posted marker does not parse back")
	}
	second, ok := got[1].(*jq.Obj)
	if !ok {
		t.Fatal("second projected comment is not an object")
	}
	if jq.R(mustGet(second)) != "plain" {
		t.Errorf("sig-less body was marked: %q", jq.R(mustGet(second)))
	}
}
