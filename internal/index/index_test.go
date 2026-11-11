package index

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestParseCallersLimitRefuses pins the CLI's --limit usage refusal: one
// message and exit code for every out-of-range shape.
func TestParseCallersLimitRefuses(t *testing.T) {
	for _, value := range []string{"0", "201", "x", "-1"} {
		var diag strings.Builder
		r := &cli.Runner{Diag: &diag}
		if _, code, done := parse(r, []string{"callers", "a.Target", "--limit", value}); !done || code != cli.ExitUsage {
			t.Errorf("--limit %s = code %d, done %t; want usage refusal", value, code, done)
		}
		if want := "--limit wants an integer from 1 to 200\n"; diag.String() != want {
			t.Errorf("--limit %s stderr = %q; want %q", value, diag.String(), want)
		}
	}
}

// TestParseCallersLimitAcceptsBounds proves both published bounds parse.
func TestParseCallersLimitAcceptsBounds(t *testing.T) {
	for _, want := range []int{1, 200} {
		r := &cli.Runner{Diag: &strings.Builder{}}
		inv, code, done := parse(r, []string{"callers", "a.Target", "--limit", strconv.Itoa(want)})
		if done || code != 0 || inv.limit != want {
			t.Errorf("--limit %d = limit %d, code %d, done %t; want accepted", want, inv.limit, code, done)
		}
	}
}
