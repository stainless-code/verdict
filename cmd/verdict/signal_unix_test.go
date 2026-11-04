//go:build unix

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

const envSignalChild = "VERDICT_TEST_SIGNAL_CHILD"

// TestSignalChild is the child half of TestShutdownSignalsCancelTheVerb: it
// runs the real front door and exits with its code. It is inert in a normal
// suite run.
func TestSignalChild(t *testing.T) {
	if os.Getenv(envSignalChild) == "" {
		t.Skip("child half of TestShutdownSignalsCancelTheVerb")
	}
	code := run([]string{"fetch", "o/r", "1", "--json"}, os.Stdout, os.Stderr, os.Getenv, os.ReadFile)
	os.Exit(code)
}

// TestShutdownSignalsCancelTheVerb pins GO-CLI-05 end to end: SIGINT and
// SIGTERM mid-verb cancel the root context, so the verb exits 3 with the
// `canceled` envelope instead of dying to the default signal action (SIGTERM
// used to kill the process outright, leaving a verify tree orphaned and no
// envelope on stdout).
func TestShutdownSignalsCancelTheVerb(t *testing.T) {
	t.Run("SIGINT", func(t *testing.T) { t.Parallel(); assertSignalCancels(t, syscall.SIGINT) })
	t.Run("SIGTERM", func(t *testing.T) { t.Parallel(); assertSignalCancels(t, syscall.SIGTERM) })
}

func assertSignalCancels(t *testing.T, sig syscall.Signal) {
	t.Helper()
	code, stdout := signalMidFetch(t, sig)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 for a canceled verb\nstdout: %s", code, stdout)
	}
	if !strings.Contains(stdout, `"code": "canceled"`) {
		t.Errorf("stdout lacks the canceled envelope:\n%s", stdout)
	}
}

func signalMidFetch(t *testing.T, sig syscall.Signal) (int, string) {
	t.Helper()
	dir := t.TempDir()
	started := filepath.Join(dir, "gh.started")
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\n: > '" + started + "'\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSignalChild$")
	cmd.Dir = dir
	cmd.Env = []string{
		envSignalChild + "=1",
		"PATH=" + bin + ":/usr/bin:/bin",
		"HOME=" + dir,
		"XDG_CONFIG_HOME=" + dir,
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, started)
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait() //nolint:errcheck // the exit code below carries the outcome; a non-zero exit is expected
	return cmd.ProcessState.ExitCode(), stdout.String()
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	testenv.Until(t, func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
}
