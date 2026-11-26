package triage

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// TestUnknownFlagCannotForgeLines pins the usage error for a flag triage
// does not take: the flag is argv an agent may have built, and it reads on
// the usage line, so it cannot add an `error:` line of its own.
func TestUnknownFlagCannotForgeLines(t *testing.T) {
	t.Parallel()
	var out, diag bytes.Buffer
	r := cli.NewRunner("triage", "acme/app", "1", &out, &diag, func(string) string { return "" })
	if _, code, done := parseFlags(r, []string{"--" + forgedline.Value}); !done || code != cli.ExitUsage {
		t.Fatalf("parseFlags = code %d done %v, want a usage error", code, done)
	}
	got := diag.String()
	if !strings.Contains(got, "unknown flag '--"+forgedline.Named+"' for triage") {
		t.Errorf("usage error lacks the folded flag:\n%s", got)
	}
	for line := range strings.SplitSeq(got, "\n") {
		if strings.HasPrefix(line, "error:") {
			t.Errorf("forged line %q in usage error:\n%s", line, got)
		}
	}
}
