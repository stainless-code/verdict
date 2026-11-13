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

func TestMissingCommentsMultiset(t *testing.T) {
	t.Parallel()
	at := func(line int) *jq.Obj {
		return projectComment(jq.NewObj("path", "a.go", "side", "RIGHT", "line", line, "body", "same finding"), "")
	}
	remote := []commentIdentity{identifyComment("head", at(10))}
	for _, example := range []struct {
		lines []int
		want  int
	}{{[]int{90, 10}, 90}, {[]int{20, 90}, 90}} {
		lines := example.lines
		missing := missingComments("head", []any{at(lines[0]), at(lines[1])}, remote)
		if len(missing) != 1 {
			t.Fatalf("lines %v: missing %d, want 1", lines, len(missing))
		}
		obj, ok := missing[0].(*jq.Obj)
		if !ok {
			t.Fatal("missing comment shape")
		}
		got, _ := obj.Get("line")
		want := example.want
		if got != want {
			t.Fatalf("missing line = %v, want %d", got, want)
		}
	}
}
