//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package readcommon_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/testenv"
)

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// probeTreeFixture installs a git shim that reports a top level only after
// its descendant is running: it writes the descendant's pid to ready, then
// blocks on it. The descendant and the shim are shell builtins only, so the
// stub PATH stays self-sufficient.
func probeTreeFixture(t *testing.T) (ready string) {
	t.Helper()
	dir := t.TempDir()
	ready = filepath.Join(dir, "descendant.pid")
	body := "#!/bin/sh\n(\nwhile :; do :; done\n) &\necho $! > '" + ready + "'\nwait\n"
	fakeGit(t, body)
	return ready
}

func readPID(t *testing.T, path string) (int, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, false
	}
	return pid, pidAlive(pid)
}

// TestRepoRootCancellationKillsTheProbeTree proves cancellation reaches the
// probe's process group: the probe blocks on a live descendant, cancellation
// is issued only after the descendant is running, and the descendant is gone
// afterwards.
func TestRepoRootCancellationKillsTheProbeTree(t *testing.T) {
	ready := probeTreeFixture(t)
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	rootErr := make(chan error, 1)
	go func() { _, err := readcommon.RepoRoot(ctx); rootErr <- err }()

	var pid int
	testenv.Until(t, func() bool {
		found, alive := readPID(t, ready)
		pid = found
		return alive
	})
	t.Cleanup(func() {
		if pidAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL) //nolint:errcheck // best-effort reap of a leaked fixture
		}
	})

	cancel()
	if err := <-rootErr; !errors.Is(err, readcommon.ErrRootUnresolved) {
		t.Fatalf("RepoRoot on cancel = %v, want ErrRootUnresolved", err)
	}
	testenv.Until(t, func() bool { return !pidAlive(pid) })
}

// fakeGit writes a git shim that runs body, so the probe's process behavior
// is observable end to end. The shim is a POSIX script, so it lives with the
// other unix-only fixtures here instead of failing generically on Windows.
func fakeGit(t *testing.T, body string) {
	t.Helper()
	bin := t.TempDir()
	path := filepath.Join(bin, "git")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

// TestRepoRootRefusesCappedProbeOutput proves a capped probe stream never
// answers with a truncated path: 4096+ bytes of "x" is not a checkout.
func TestRepoRootRefusesCappedProbeOutput(t *testing.T) {
	t.Chdir(t.TempDir())
	fakeGit(t, `i=0
while [ $i -lt 5000 ]; do printf 'x'; i=$((i+1)); done
printf '\n'`)
	if root, err := readcommon.RepoRoot(t.Context()); !errors.Is(err, readcommon.ErrRootUnresolved) {
		t.Fatalf("RepoRoot with capped stdout = (%q, %v), want ErrRootUnresolved", root, err)
	}
}
