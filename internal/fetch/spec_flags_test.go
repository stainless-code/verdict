package fetch

import (
	"bytes"
	"slices"
	"strings"
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

// TestParseFetchIssueSpellings pins both spellings and the empty operand: the
// inline form is the one a caller writes without a shell argument.
func TestParseFetchIssueSpellings(t *testing.T) {
	t.Parallel()
	r := &cli.Runner{Out: &bytes.Buffer{}, Diag: &bytes.Buffer{}}
	f, code, done := parseFetchFlags(r,
		[]string{"--issue=acme/app#12", "--issue", "#7", "--issue=STA-466"})
	if done || code != 0 {
		t.Fatalf("parseFetchFlags = (code=%d, done=%v), want ok", code, done)
	}
	if want := []string{"acme/app#12", "#7", "STA-466"}; !slices.Equal(f.Issue, want) {
		t.Fatalf("FetchFlags.Issue = %q, want %q", f.Issue, want)
	}
}

// TestParseFetchIssueEmptyOperandRefusesUsage holds the empty inline value to
// the missing-operand refusal, not to a host read.
func TestParseFetchIssueEmptyOperandRefusesUsage(t *testing.T) {
	t.Parallel()
	var diag bytes.Buffer
	r := &cli.Runner{Out: &bytes.Buffer{}, Diag: &diag}
	_, code, done := parseFetchFlags(r, []string{"--issue="})
	if !done || code != cli.ExitUsage {
		t.Fatalf("fetch --issue= = (code=%d, done=%v), want exit %d", code, done, cli.ExitUsage)
	}
	if got := diag.String(); !strings.Contains(got, "--issue") {
		t.Fatalf("diagnostics = %q, want the flag named", got)
	}
}
