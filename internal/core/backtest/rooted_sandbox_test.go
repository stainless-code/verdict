package backtest

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testkit"
)

// TestReplayCommanderIsSandboxedLikeItsInner proves the replay's rooted
// wrapper forwards the sandbox decision: around the production runners a
// replayed review is OS-sandboxed (before, the wrapper made every backtest
// review bare), and around a test double it is not.
func TestReplayCommanderIsSandboxedLikeItsInner(t *testing.T) {
	for _, tc := range []struct {
		inner core.Commander
		name  string
		want  bool
	}{
		{name: "runner", inner: coreexec.Runner{}, want: true},
		{name: "watchdog", inner: coreexec.Watchdog{}, want: true},
		{name: "double", inner: &testkit.Script{}, want: false},
	} {
		if got := core.StartsHostProcesses(rootedCommander{inner: tc.inner}); got != tc.want {
			t.Errorf("%s: a replay through it starts host processes = %v, want %v", tc.name, got, tc.want)
		}
	}
}
