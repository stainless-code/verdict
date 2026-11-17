package fetchcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestNormalizedPRIncludesLabels(t *testing.T) {
	pr, err := normalizeGitLab(`{"iid":7,"title":"fix","labels":["security","deps"]}`, `[]`, nil)
	if err != nil {
		t.Fatalf("normalizeGitLab: %v", err)
	}
	labels, ok := pr.Get("labels")
	if !ok {
		t.Fatal("normalized PR has no labels key")
	}
	var got []string
	for _, value := range jq.Elements(labels) {
		got = append(got, jq.StrOf(value))
	}
	want := []string{"security", "deps"}
	if len(got) != len(want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("labels[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestForgejoPRIncludesObjectLabels(t *testing.T) {
	pr, err := normalizeForgejo(`{"number":7,"title":"fix","labels":[{"name":"security"}]}`, `[]`)
	if err != nil {
		t.Fatalf("normalizeForgejo: %v", err)
	}
	labels, _ := pr.Get("labels")
	var got []string
	for _, value := range jq.Elements(labels) {
		got = append(got, jq.StrOf(value))
	}
	if len(got) != 1 || got[0] != "security" {
		t.Errorf("labels = %v, want [security]", got)
	}
}
