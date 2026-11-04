//go:build windows

package upgrade

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/stainless-code/verdict/internal/jq"
)

// errInvalidParameter is ERROR_INVALID_PARAMETER: OpenProcess on a PID
// that has already exited.
const errInvalidParameter syscall.Errno = 87

func writableDir(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// removePathMarker removes installDir from the user PATH via PowerShell,
// mirroring install.ps1's uninstall (the user environment has no text
// profile to edit).
func removePathMarker(ctx context.Context, _ map[string]string, installDir string) (string, bool, error) {
	script := "$d=$args[0]; $p=[Environment]::GetEnvironmentVariable('Path','User');" +
		"if ($null -eq $p) { exit 0 };" +
		"$parts=@($p -split ';' | Where-Object { $_ -ne '' -and $_ -ne $d });" +
		"if ($parts.Count -eq @($p -split ';').Count) { exit 3 };" +
		"[Environment]::SetEnvironmentVariable('Path', ($parts -join ';'), 'User')"
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script, installDir)
	cmd.WaitDelay = commandWaitDelay
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 3 {
			return "user PATH", false, nil
		}
		return "", false, jq.Sealed("update user PATH", err)
	}
	return "user PATH", true, nil
}

func waitProcess(ctx context.Context, pid int) error {
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, errInvalidParameter) {
			return nil
		}
		return jq.Sealed("open parent process", err)
	}
	defer func() { _ = syscall.CloseHandle(handle) }() //nolint:errcheck // handle cleanup; the wait outcome rules
	for {
		event, err := syscall.WaitForSingleObject(handle, 100)
		if err != nil {
			return jq.Sealed("wait for parent", err)
		}
		if event == syscall.WAIT_OBJECT_0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}
