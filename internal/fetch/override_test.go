package fetch

import (
	"io"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

func TestExclusionOverrideFlags(t *testing.T) {
	r := &cli.Runner{Out: io.Discard, Diag: io.Discard, Human: io.Discard}
	args := []string{"--ignore", "**", "--ignore", "!keep.go", "--no-default-excludes"}
	f, code, done := parseFetchFlags(r, args)
	if done || code != 0 || !f.NoDefaultExcludes || len(f.Ignore) != 2 || f.Ignore[1] != "!keep.go" {
		t.Fatalf("fetch flags=%+v code=%d done=%v", f, code, done)
	}
	l, code, done := parseLocalFlags(r, args)
	if done || code != 0 || !l.NoDefaultExcludes || len(l.Ignore) != 2 || l.Ignore[1] != "!keep.go" {
		t.Fatalf("local flags=%+v code=%d done=%v", l, code, done)
	}
}
