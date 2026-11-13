package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestContentDigestFields(t *testing.T) {
	t.Parallel()
	comment := func() *jq.Obj { return jq.NewObj("path", "a.go", "side", "RIGHT", "line", 10, "body", "broken") }
	baseline := contentDigest("summary", "head-a", []any{comment()})
	if baseline != "1f578e98ea09d48c92d1c5c80d1e695633e1f0f7" {
		t.Fatalf("digest = %s, want canonical content hash", baseline)
	}
	for _, field := range []string{"path", "side", "line", "body"} {
		t.Run(field, func(t *testing.T) {
			changed := comment()
			changed.Set(field, "changed")
			if contentDigest("summary", "head-a", []any{changed}) == baseline {
				t.Fatalf("digest ignored %s", field)
			}
		})
	}
}

func TestContentDigestHeadBodyAndPositions(t *testing.T) {
	t.Parallel()
	comment := func() *jq.Obj { return jq.NewObj("path", "a.go", "side", "RIGHT", "line", 10, "body", "broken") }
	const baseline = "1f578e98ea09d48c92d1c5c80d1e695633e1f0f7"
	for _, input := range []struct{ body, head string }{{"new summary", "head-a"}, {"summary", "head-b"}} {
		if contentDigest(input.body, input.head, []any{comment()}) == baseline {
			t.Fatal("digest ignored rendered body or reviewed head")
		}
	}
	shifted := comment()
	shifted.Set("position", 100)
	shifted.Set("original_position", 1)
	shifted.Set("start_line", 5)
	if got := contentDigest("summary", "head-a", []any{shifted}); got != baseline {
		t.Fatal("diff position or range metadata changed content digest")
	}
}

func TestContentDigestRenderedSources(t *testing.T) {
	t.Parallel()
	finding := jq.NewObj("path", "a.go", "side", "RIGHT", "line", 10, "body", "broken", "sources", []any{"docs/auth.md"})
	rendered := projectComment(finding, "")
	body, _ := rendered.Get("body")
	if body != "broken\n\nSources used: docs/auth.md" {
		t.Fatalf("posted body = %q", body)
	}
	digest := contentDigest("summary", "head-a", []any{rendered})
	rendered.Set("body", "broken")
	if contentDigest("summary", "head-a", []any{rendered}) == digest {
		t.Fatal("digest ignored rendered Sources used line")
	}
}
