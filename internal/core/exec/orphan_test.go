package exec

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// TestExecuteOrphanSweepKillsTree proves the post-exit sweep on every OS: the
// parent exits 0 while a child outlives it (holding the output pipe past
// WaitDelay), so the recorded exit stays 0 with the timeout flag set through
// the decoy path.
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
