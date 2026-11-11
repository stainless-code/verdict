//go:build windows

package upgrade

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestWaitProcessReturnsForExitedChild proves WaitForSingleObject on an
// already-exited parent signals immediately instead of polling to the budget.
func TestWaitProcessReturnsForExitedChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", "exit", "0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := waitProcess(ctx, cmd.Process.Pid); err != nil {
		t.Fatalf("waitProcess: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("waitProcess took %v for an exited child; the signaled handle returns on the first 100ms poll, not after a wait toward the 10s budget", elapsed)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait child: %v", err)
	}
}
