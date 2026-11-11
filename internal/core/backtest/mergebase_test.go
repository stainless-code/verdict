package backtest

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// scriptedGit reports one canned result for every git command.
type scriptedGit struct {
	out    string
	stderr string
	exit   int
	err    error
}

// Run returns the canned result.
func (c scriptedGit) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{Stdout: c.out, Stderr: c.stderr, ExitCode: c.exit}, c.err
}

// TestMergeBaseDistinguishesNoAncestorFromFailure proves only exit 1 reads as
// "no common ancestor"; a git failure refuses backtest-failed with its stderr.
func TestMergeBaseDistinguishesNoAncestorFromFailure(t *testing.T) {
	tests := []struct {
		name    string
		cmdr    scriptedGit
		want    string
		code    string
		message string
	}{
		{"common ancestor", scriptedGit{out: "abc123\n"}, "abc123", "", ""},
		{"no common ancestor", scriptedGit{exit: 1}, "", "", ""},
		{
			"git failure", scriptedGit{exit: 2, stderr: "fatal: bad object\n"},
			"", "backtest-failed", "fatal: bad object",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &state{
				req:  &Request{Runtime: &core.Runtime{Commander: tc.cmdr}},
				root: t.TempDir(),
			}
			got, ref := st.mergeBase(t.Context(), "aaaaaaa", "bbbbbbb")
			if ref != nil {
				if tc.code == "" || ref.Code != tc.code {
					t.Fatalf("refusal = %s: %s; want none (%s)", ref.Code, ref.Message, tc.code)
				}
				if !strings.Contains(ref.Message, tc.message) {
					t.Errorf("message = %q; want it to carry %q", ref.Message, tc.message)
				}
				return
			}
			if tc.code != "" {
				t.Fatalf("refusal = nil; want %s", tc.code)
			}
			if got != tc.want {
				t.Errorf("base = %q; want %q", got, tc.want)
			}
		})
	}
}
