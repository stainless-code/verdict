//go:build !windows

package upgrade

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// helperPoll bounds the parent-PID poll interval.
const helperPoll = 50 * time.Millisecond

// writableDir reports whether the effective user may write to dir.
func writableDir(dir string) bool {
	// W_OK is 2; syscall.Access asks the kernel, so ACLs are honored.
	return syscall.Access(dir, 2) == nil
}

// removePathMarker removes every profile line carrying the ticket-10
// marker, mirroring install.sh --uninstall --remove-path. The install dir
// is unused here: the marker owns the line identity on POSIX.
func removePathMarker(_ context.Context, env map[string]string, _ string) (string, bool, error) {
	profile := profilePath(env)
	if profile == "" {
		return "", false, nil
	}
	raw, err := os.ReadFile(profile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return profile, false, nil
		}
		return "", false, sealed("read "+profile, err)
	}
	body, changed := stripMarker(string(raw))
	if !changed {
		return profile, false, nil
	}
	if err := os.WriteFile(profile, []byte(body), filePermResult); err != nil { //nolint:gosec // match the installer's profile mode
		return "", false, sealed("rewrite "+profile, err)
	}
	return profile, true, nil
}

// stripMarker drops every profile line carrying the installer marker.
func stripMarker(raw string) (string, bool) {
	lines := strings.Split(raw, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.Contains(line, pathMarker) {
			kept = append(kept, line)
		}
	}
	if len(kept) == len(lines) {
		return raw, false
	}
	return strings.Join(kept, "\n"), true
}

// profilePath picks the shell profile the installer would have edited.
func profilePath(env map[string]string) string {
	home := homeDir(env)
	if home == "" {
		return ""
	}
	switch filepath.Base(env["SHELL"]) {
	case "zsh":
		if dir := env["ZDOTDIR"]; dir != "" {
			return filepath.Join(dir, ".zshrc")
		}
		return filepath.Join(home, ".zshrc")
	case "bash":
		return bashProfile(home)
	default:
		return filepath.Join(home, ".profile")
	}
}

// bashProfile prefers the file bash actually reads on this machine.
func bashProfile(home string) string {
	for _, name := range []string{".bashrc", ".bash_profile"} {
		if info, err := os.Stat(filepath.Join(home, name)); err == nil && !info.IsDir() {
			return filepath.Join(home, name)
		}
	}
	return filepath.Join(home, ".bashrc")
}

// waitProcess polls until pid exits (kill 0 reads ESRCH).
func waitProcess(ctx context.Context, pid int) error {
	ticker := time.NewTicker(helperPoll)
	defer ticker.Stop()
	for {
		gone, err := processGone(pid)
		if err != nil {
			return err
		}
		if gone {
			return nil
		}
		select {
		case <-ctx.Done():
			return sealed("wait for parent", ctx.Err())
		case <-ticker.C:
		}
	}
}

// processGone reports ESRCH (the parent exited).
func processGone(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return true, nil
	}
	return false, sealed("probe parent pid", err)
}
