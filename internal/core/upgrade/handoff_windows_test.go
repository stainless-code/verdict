//go:build windows

package upgrade

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestWaitProcessReturnsForExitedChild(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := waitProcess(ctx, cmd.Process.Pid); err != nil {
		t.Fatalf("waitProcess: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait child: %v", err)
	}
}
