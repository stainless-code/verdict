package kb

import (
	"strings"
	"testing"
)

// TestDisplayPathsAreSlashForm pins the operator-facing store path contract:
// warnings and goldens carry slash-form on every OS, so a Windows run never
// renders `.verdict\kb.jsonl`. It holds vacuously on Unix and guards Windows.
func TestDisplayPathsAreSlashForm(t *testing.T) {
	t.Parallel()
	c := &Ctx{Root: "repo"}
	for _, rel := range []string{File, Joins} {
		got := c.display(rel)
		if strings.Contains(got, `\`) {
			t.Errorf("display(%q) = %q; want slash-form", rel, got)
		}
		if want := "repo/" + rel; got != want {
			t.Errorf("display(%q) = %q; want %q", rel, got, want)
		}
	}
	if got := (&Ctx{}).display(File); got != File {
		t.Errorf("display with no root = %q; want %q", got, File)
	}
}
