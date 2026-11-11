package backtest

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// scriptedGit reports one canned result for every git command.
type scriptedGit struct {
	err    error
	out    string
	stderr string
	exit   int
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
		want    string
		code    string
		message string
		cmdr    scriptedGit
	}{
		{"common ancestor", "abc123", "", "", scriptedGit{out: "abc123\n"}},
		{"no common ancestor", "", "", "", scriptedGit{exit: 1}},
		{"git failure", "", "backtest-failed", "fatal: bad object", scriptedGit{exit: 2, stderr: "fatal: bad object\n"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rt := &core.Runtime{Commander: tc.cmdr}
			st := &state{req: &Request{Runtime: rt}, rt: rt, root: t.TempDir()}
			got, ref := st.mergeBase(t.Context(), "aaaaaaa", "bbbbbbb")
			checkMergeBase(t, got, ref, tc.want, tc.code, tc.message)
		})
	}
}

// checkMergeBase compares one answer: a code means a refusal carrying the
// message, an empty code means a base and no refusal.
func checkMergeBase(t *testing.T, got string, ref *core.RefusalError, want, code, message string) {
	t.Helper()
	if code == "" {
		checkNoRefusal(t, got, ref, want)
		return
	}
	checkRefusal(t, ref, code, message)
}

// checkNoRefusal expects a base and no refusal.
func checkNoRefusal(t *testing.T, got string, ref *core.RefusalError, want string) {
	t.Helper()
	if ref != nil {
		t.Fatalf("refusal = %s: %s; want none", ref.Code, ref.Message)
	}
	if got != want {
		t.Errorf("base = %q; want %q", got, want)
	}
}

// checkRefusal expects the named refusal carrying message.
func checkRefusal(t *testing.T, ref *core.RefusalError, code, message string) {
	t.Helper()
	if ref == nil {
		t.Fatalf("refusal = nil; want %s", code)
	}
	if ref.Code != code {
		t.Fatalf("refusal = %s: %s; want %s", ref.Code, ref.Message, code)
	}
	if !strings.Contains(ref.Message, message) {
		t.Errorf("message = %q; want it to carry %q", ref.Message, message)
	}
}
