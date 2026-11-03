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
	vcmd := `exec sleep 30 & echo $! > '` + pidFile + `'`
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

// assertPidDead polls the pid the child reported and fails unless it reads
// dead: "killed the tree" is the claim under test, so the parent's exit code
// and flags alone cannot pass it.
func assertPidDead(t *testing.T, pidFile string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	pid := readPid(t, pidFile, deadline)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("child pid %d still alive after the tree kill", pid)
}

// readPid polls until the child has written its pid.
func readPid(t *testing.T, pidFile string, deadline time.Time) int {
	t.Helper()
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("child never wrote its pid to %s", pidFile)
	return 0
}
