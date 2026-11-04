package exec

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestExecuteOrphanSweepKillsTree proves the post-exit sweep on every OS: the
// parent exits 0 while a child outlives it (holding the output pipe past
// WaitDelay), so the recorded exit stays 0 with the timeout flag set through
// the decoy path. The child writes its pid, so the test proves the tree died
// instead of trusting the parent's exit code.
func TestExecuteOrphanSweepKillsTree(t *testing.T) {
	pidFile := filepath.ToSlash(filepath.Join(t.TempDir(), "orphan.pid"))
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	vcmd := `exec sleep 30 & ` + pidReport(pidFile)
	res, err := Watchdog{}.Run(tctx, core.Command{Argv: []string{"bash", "-c", vcmd}, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !res.TimedOut || !res.Decoy {
		t.Errorf("result = exit %d timedOut %v decoy %v; want exit 0, timedOut true, decoy true",
			res.ExitCode, res.TimedOut, res.Decoy)
	}
	assertPidDead(t, pidFile)
}

func assertPidDead(t *testing.T, pidFile string) {
	t.Helper()
	pid := readPid(t, pidFile)
	testenv.Until(t, func() bool { return !pidAlive(pid) })
}

func readPid(t *testing.T, pidFile string) int {
	t.Helper()
	var pid int
	testenv.Until(t, func() bool {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		var convErr error
		pid, convErr = strconv.Atoi(strings.TrimSpace(string(raw)))
		return convErr == nil
	})
	return pid
}
