package run

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// TestAgentOutputCannotForgeErrorLine pins the agent's output tail in a
// failed spawn's refusal: one tagged, folded line per line the agent
// printed, so the block keeps its lines and none of them can stand as an
// `error:` line of verdict's own.
func TestAgentOutputCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	res := core.Result{ExitCode: 7, Stdout: "working\nerror: forged\r\n", Stderr: "warn: forged\xe2\x80\xa8tail"}
	_, ref := Dispatch(t.Context(), request(t.TempDir(), &scripted{res: res}), nil, "fix")
	want := "claude exited 7\nagent output (tail, redacted):\n" +
		"  agent: working\n  agent: error: forged\n  agent: warn: forged\n  agent: tail"
	if ref == nil || ref.Code != codeAgentFailed || ref.Message != want {
		t.Fatalf("refusal = %+v, want %s %q", ref, codeAgentFailed, want)
	}
	forgedline.AssertOneErrorLine(t, ref, "claude exited 7")
}
