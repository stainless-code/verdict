//go:build unix

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// envSignalChild marks the re-executed test binary that plays the verdict
// process in TestShutdownSignalsCancelTheVerb.
const envSignalChild = "VERDICT_TEST_SIGNAL_CHILD"

// TestSignalChild is the child half of TestShutdownSignalsCancelTheVerb: it
// runs the real front door and exits with its code. It is inert in a normal
// suite run.
func TestSignalChild(t *testing.T) {
	if os.Getenv(envSignalChild) == "" {
		t.Skip("child half of TestShutdownSignalsCancelTheVerb")
	}
	code := run([]string{"fetch", "o/r", "1", "--json"}, os.Stdout, os.Stderr, os.Getenv, os.ReadFile)
	//nolint:forbidigo // the re-executed child must exit with the verb's own code
	os.Exit(code)
}

// TestShutdownSignalsCancelTheVerb pins GO-CLI-05 end to end: SIGINT and
// SIGTERM mid-verb cancel the root context, so the verb exits 3 with the
// `canceled` envelope instead of dying to the default signal action (SIGTERM
// used to kill the process outright, leaving a verify tree orphaned and no
// envelope on stdout).
func TestShutdownSignalsCancelTheVerb(t *testing.T) {
	for _, tc := range []struct {
		name string
		sig  syscall.Signal
	}{
		{"SIGINT", syscall.SIGINT},
		{"SIGTERM", syscall.SIGTERM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			started := filepath.Join(dir, "gh.started")
			stub := "#!/bin/sh\n: > '" + started + "'\nexec sleep 30\n"
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
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
			if err := cmd.Process.Signal(tc.sig); err != nil {
				t.Fatal(err)
			}
			err := cmd.Wait()
			exitErr, ok := errors.AsType[*exec.ExitError](err)
			if !ok || exitErr.ExitCode() != 3 {
				t.Fatalf("Wait = %v (exit %v), want exit 3 for a cancelled verb\nstdout: %s", err, exitCode(exitErr), stdout.String())
			}
			if !bytes.Contains(stdout.Bytes(), []byte(`"code": "canceled"`)) {
				t.Errorf("stdout lacks the canceled envelope:\n%s", stdout.String())
			}
		})
	}
}

// waitForFile polls until the stub gh proves the verb is mid-flight.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stub gh never started: %s missing", path)
}

// exitCode renders an exit error's code for the failure message.
func exitCode(e *exec.ExitError) int {
	if e == nil {
		return 0
	}
	return e.ExitCode()
}
