package fetch

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

func TestParseFetchSpecFlagRepeatable(t *testing.T) {
	t.Parallel()
	r := &cli.Runner{Out: &bytes.Buffer{}, Diag: &bytes.Buffer{}}
	f, code, done := parseFetchFlags(r, []string{"--spec", "a.md", "--context", "c.md", "--spec", "b.md"})
	if done || code != 0 {
		t.Fatalf("parseFetchFlags = (code=%d, done=%v), want ok", code, done)
	}
	if !slices.Equal(f.Spec, []string{"a.md", "b.md"}) {
		t.Fatalf("FetchFlags.Spec = %q, want [a.md b.md]", f.Spec)
	}
	if !slices.Equal(f.Context, []string{"c.md"}) {
		t.Fatalf("FetchFlags.Context = %q, want [c.md]", f.Context)
	}
}

func TestParseLocalSpecFlagRepeatable(t *testing.T) {
	t.Parallel()
	r := &cli.Runner{Out: &bytes.Buffer{}, Diag: &bytes.Buffer{}}
	f, code, done := parseLocalFlags(r, []string{"--staged", "--spec", "a.md", "--spec", "b.md"})
	if done || code != 0 {
		t.Fatalf("parseLocalFlags = (code=%d, done=%v), want ok", code, done)
	}
	if !slices.Equal(f.Spec, []string{"a.md", "b.md"}) {
		t.Fatalf("FetchLocalFlags.Spec = %q, want [a.md b.md]", f.Spec)
	}
}

func TestParseSpecFlagMissingOperandRefusesUsage(t *testing.T) {
	t.Parallel()
	r := &cli.Runner{Out: &bytes.Buffer{}, Diag: &bytes.Buffer{}}
	_, code, done := parseFetchFlags(r, []string{"--spec"})
	if !done || code != cli.ExitUsage {
		t.Fatalf("fetch missing --spec = (code=%d, done=%v), want exit %d", code, done, cli.ExitUsage)
	}
	_, localCode, localDone := parseLocalFlags(r, []string{"--spec"})
	if !localDone || localCode != cli.ExitUsage {
		t.Fatalf("fetch-local missing --spec = (code=%d, done=%v), want exit %d", localCode, localDone, cli.ExitUsage)
	}
}
