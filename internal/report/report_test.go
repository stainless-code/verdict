package report

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

func TestUnknownFlagListsAllReportFlags(t *testing.T) {
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag}
	code := Run(context.Background(), r, []string{"--bogus"}, Request{Repo: "acme/app", Pr: "1"})
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d; want %d", code, cli.ExitUsage)
	}
	got := diag.String()
	for _, flag := range []string{
		"--status", "--kind", "--category", "--output", "--sarif",
		"--include-withheld", "--cost", "--check", "--completeness", "--live", "--json",
	} {
		if !strings.Contains(got, flag) {
			t.Errorf("unknown-flag message %q missing %s", got, flag)
		}
	}
}
