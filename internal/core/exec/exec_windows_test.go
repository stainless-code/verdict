//go:build windows

package exec

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// TestExecuteTimeoutKillsTree proves the Windows watchdog: a command that
// ignores TERM and leaves a sleeping child behind is killed as a tree,
// recording the shell's SIGKILL code with the timeout flag set (no decoy).
func TestExecuteTimeoutKillsTree(t *testing.T) {
	const vcmd = `trap '' TERM; (trap '' TERM; sleep 30) & sleep 30`
	tctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	res, err := OSCommander{}.Run(tctx, core.Command{Argv: []string{"bash", "-c", vcmd}, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != sigkillExit || !res.TimedOut || res.Decoy {
		t.Errorf("result = exit %d timedOut %v decoy %v; want exit %d, timedOut true, decoy false",
			res.ExitCode, res.TimedOut, res.Decoy, sigkillExit)
	}
}

// TestExecuteOrphanSweepKillsTree proves the post-exit sweep: the parent
// exits 0 while a child outlives it, so the recorded exit stays 0 with the
// timeout flag set through the decoy path.
func TestExecuteOrphanSweepKillsTree(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	res, err := OSCommander{}.Run(tctx, core.Command{Argv: []string{"bash", "-c", `(sleep 30 &)`}, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !res.TimedOut || !res.Decoy {
		t.Errorf("result = exit %d timedOut %v decoy %v; want exit 0, timedOut true, decoy true",
			res.ExitCode, res.TimedOut, res.Decoy)
	}
}
