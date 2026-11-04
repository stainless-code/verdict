package verdict_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestPublicAPISurface pins the exported facade: `go doc -all` output must
// match api.txt byte-for-byte, and no core-internal path may appear in a
// public signature. Intentional API changes regenerate the golden with:
// go doc -all ./pkg/verdict > pkg/verdict/api.txt (from the repo root),
// reviewed as part of the change.
func TestPublicAPISurface(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "go", "doc", "-all", "github.com/stainless-code/verdict/pkg/verdict")
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go doc -all: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "internal/") {
		t.Errorf("public API mentions internal packages:\n%s", grepLines(string(out), "internal/"))
	}
	want, err := os.ReadFile("api.txt")
	if err != nil {
		t.Fatalf("read api.txt: %v", err)
	}
	if !bytes.Equal(out, want) {
		t.Errorf("public API changed: regenerate api.txt with `go doc -all ./pkg/verdict > pkg/verdict/api.txt` and review the diff")
	}
}

// grepLines returns the lines of s containing needle.
func grepLines(s, needle string) string {
	var kept []string
	for line := range strings.Lines(s) {
		if strings.Contains(line, needle) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "")
}
