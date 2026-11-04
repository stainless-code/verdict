//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package exec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// pidReport is the shell fragment that writes the last background job's pid
// to file.
func pidReport(file string) string {
	return `echo $! > '` + file + `'`
}

// pidAlive reports whether pid is still running: kill -0 succeeds for a
// running (or zombie) process, and EPERM still means the pid exists.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// TestWatchdogRun proves the watchdog contract: argv runs, combined streams
// come back in Stdout, the wait code maps, and a clean run is not a timeout.
func TestWatchdogRun(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	res, err := Watchdog{}.Run(tctx, core.Command{
		Argv: []string{"bash", "-c", `printf out; printf err >&2`},
		Env:  []string{"PATH=/bin:/usr/bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "outerr" || res.Stderr != "" {
		t.Errorf("result = %+v, want combined outerr", res)
	}
	if res.ExitCode != 0 || res.TimedOut || res.Decoy {
		t.Errorf("result = %+v, want clean exit 0", res)
	}
}

// TestWatchdogExitCode proves nonzero exits come back as results, not
// errors.
func TestWatchdogExitCode(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	res, err := Watchdog{}.Run(tctx, core.Command{Argv: []string{"bash", "-c", `exit 3`}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.TimedOut {
		t.Errorf("result = %+v, want exit 3", res)
	}
}

// TestWatchdogCapsCombinedOutput proves a child that writes past the
// combined cap is truncated to the documented tail, with the marker.
func TestWatchdogCapsCombinedOutput(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const limit = 4096
	res, err := Watchdog{}.Run(tctx, core.Command{
		Argv:      []string{"bash", "-c", "yes | head -c 4194304"},
		OutputCap: limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.StdoutTruncated {
		t.Fatal("StdoutTruncated = false, want true")
	}
	if len(res.Stdout) > limit+len(truncationNotice(captureTail, limit)) {
		t.Errorf("Stdout = %d bytes, want <= cap plus the marker", len(res.Stdout))
	}
	if !strings.Contains(res.Stdout, "truncated") {
		t.Errorf("Stdout carries no truncation marker: %q", res.Stdout)
	}
}

// TestWatchdogNoisyChildStaysBounded pins the OOM vector: `yes` as a
// verifyCmd runs until the watchdog kills it, and the capture holds at most
// the cap instead of growing with the child.
func TestWatchdogNoisyChildStaysBounded(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()
	const limit = 4096
	res, err := Watchdog{}.Run(tctx, core.Command{Argv: []string{"yes"}, OutputCap: limit})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Error("TimedOut = false, want true (the watchdog budget fired)")
	}
	if !res.StdoutTruncated {
		t.Fatal("StdoutTruncated = false, want true")
	}
	if len(res.Stdout) > limit+len(truncationNotice(captureTail, limit)) {
		t.Errorf("Stdout = %d bytes, want <= cap plus the marker", len(res.Stdout))
	}
}

// TestRunnerExit3IsData pins the GO-EXEC-05 rule: a completed process with a
// non-zero exit comes back as a result, never as a Go error.
func TestRunnerExit3IsData(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	res, err := Runner{}.Run(tctx, core.Command{Argv: []string{"bash", "-c", `exit 3`}})
	if err != nil {
		t.Fatalf("err = %v, want a completed result", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
}

// TestRunnerStreamsStaySeparate proves the runner keeps stdout and stderr
// apart: fetch parses stdout as a document while probe stderr stages beside
// the bundle.
func TestRunnerStreamsStaySeparate(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	res, err := Runner{}.Run(tctx, core.Command{Argv: []string{"bash", "-c", `printf out; printf err >&2`}})
	if err != nil {
		t.Fatalf("err = %v, want a completed result", err)
	}
	if res.Stdout != "out" || res.Stderr != "err" {
		t.Errorf("result = %+v, want stdout out and stderr err", res)
	}
}

// TestRunnerStdin documents the one stdin quirk: a command that carries
// a reader uses it (post's payload), and a nil command reader falls back to
// the runner's Stdin, which itself defaults to inheriting this process's.
func TestRunnerStdin(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	t.Run("command stdin", func(t *testing.T) {
		assertRunnerStdout(tctx, t, Runner{}, core.Command{
			Argv:  []string{"bash", "-c", `cat`},
			Stdin: strings.NewReader("from the command"),
		}, "from the command")
	})
	t.Run("runner stdin default", func(t *testing.T) {
		assertRunnerStdout(tctx, t, Runner{Stdin: strings.NewReader("from the runner")},
			core.Command{Argv: []string{"bash", "-c", `cat`}}, "from the runner")
	})
}

// TestRunnerMissingTool proves the MissingTool pre-check is opt-in: with it
// the frozen core.MissingToolError drives the `missing: <tool>` line, and
// without it the spawn failure surfaces as a plain error.
func TestRunnerMissingTool(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	t.Run("pre-check on", func(t *testing.T) {
		_, err := Runner{MissingTool: true}.Run(tctx,
			core.Command{Argv: []string{"verdict-no-such-tool"}})
		missing, ok := errors.AsType[*core.MissingToolError](err)
		if !ok || missing.Tool != "verdict-no-such-tool" {
			t.Fatalf("err = %v, want MissingToolError for the tool", err)
		}
	})
	t.Run("pre-check off", func(t *testing.T) {
		_, err := Runner{}.Run(tctx, core.Command{Argv: []string{"verdict-no-such-tool"}})
		if err == nil {
			t.Fatal("err = nil, want a spawn error")
		}
		if _, ok := errors.AsType[*core.MissingToolError](err); ok {
			t.Fatalf("err = %v, want a spawn error, not MissingToolError", err)
		}
	})
}

// assertRunnerStdout runs one command and pins its stdout.
func assertRunnerStdout(ctx context.Context, t *testing.T, runner Runner, cmd core.Command, want string) {
	t.Helper()
	res, err := runner.Run(ctx, cmd)
	if err != nil {
		t.Fatalf("err = %v, want a completed result", err)
	}
	if res.Stdout != want {
		t.Errorf("Stdout = %q, want %q", res.Stdout, want)
	}
}

// TestRunnerCapsStdout proves an over-cap read refuses instead of returning
// a truncated document as success, and that the runner's cap is the default
// for commands that carry none.
func TestRunnerCapsStdout(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const limit = 32
	t.Run("command cap", func(t *testing.T) {
		res, err := Runner{}.Run(tctx, core.Command{
			Argv:      []string{"bash", "-c", "head -c 4096 /dev/zero"},
			OutputCap: limit,
		})
		assertOutputCapped(t, res, err, limit)
	})
	t.Run("runner cap", func(t *testing.T) {
		res, err := Runner{OutputCap: limit}.Run(tctx,
			core.Command{Argv: []string{"bash", "-c", "head -c 4096 /dev/zero"}})
		assertOutputCapped(t, res, err, limit)
	})
}

// assertOutputCapped pins the over-cap refusal: a typed stdout cap error, the
// truncation flag, and a bounded capture.
func assertOutputCapped(t *testing.T, res core.Result, err error, limit int) {
	t.Helper()
	capErr, ok := errors.AsType[*core.OutputCappedError](err)
	if !ok || capErr.Stream != "stdout" {
		t.Fatalf("err = %v, want OutputCappedError for stdout", err)
	}
	if capErr.Limit != limit {
		t.Errorf("cap = %d, want %d", capErr.Limit, limit)
	}
	if !res.StdoutTruncated {
		t.Error("StdoutTruncated = false, want true")
	}
	if len(res.Stdout) > limit+len(truncationNotice(captureHead, limit)) {
		t.Errorf("Stdout = %d bytes, want <= cap plus the marker", len(res.Stdout))
	}
}

// TestRunnerKeepsTwentyMiBDiff proves the fetch cap leaves the documented
// 20 MiB diff intact, so the existing cap/chunk decision still sees the
// whole payload.
func TestRunnerKeepsTwentyMiBDiff(t *testing.T) {
	const diffBytes = 20 << 20
	tctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	res, err := Runner{OutputCap: FetchOutputCap, MissingTool: true}.Run(tctx, core.Command{
		Argv: []string{"bash", "-c", "head -c 20971520 /dev/zero"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.StdoutTruncated {
		t.Fatal("StdoutTruncated = true, want false at 20 MiB")
	}
	if len(res.Stdout) != diffBytes {
		t.Errorf("Stdout = %d bytes, want %d", len(res.Stdout), diffBytes)
	}
}

// holdSeconds is how long the pipe-holding grandchildren below live. It is
// longer than the bound the runner must return within, so a runner that waits
// for the pipe to close fails the bound instead of passing slowly.
const holdSeconds = "9"

// returnBound is the longest a cancelled or exited runner may take: the
// 2s WaitDelay plus scheduling slack, well under holdSeconds.
const returnBound = 6 * time.Second

// TestRunnerCancelDoesNotWaitForPipeHolders pins the Ctrl-C hang: the
// context fires, the child is killed, but a grandchild keeps the stdout pipe
// open. Run must give up on the pipe after WaitDelay instead of blocking
// until the grandchild exits (found by running `verdict fetch` against a gh
// that spawned a long-lived helper: SIGINT took 18s to exit 3).
func TestRunnerCancelDoesNotWaitForPipeHolders(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	pidFile := filepath.ToSlash(filepath.Join(t.TempDir(), "holder.pid"))
	start := time.Now()
	res, err := Runner{}.Run(tctx, core.Command{
		Argv: []string{"sh", "-c", `sleep ` + holdSeconds + ` & ` + pidReport(pidFile) + `; wait`},
	})
	elapsed := time.Since(start)
	t.Cleanup(func() { killReportedPid(pidFile) })
	if elapsed > returnBound {
		t.Fatalf("Run took %v after a 300ms deadline; want it to return within %v of the kill", elapsed, returnBound)
	}
	if err != nil {
		t.Errorf("err = %v, want a completed result for the killed child", err)
	}
	if res.ExitCode != 137 {
		t.Errorf("ExitCode = %d, want 137 (SIGKILL by the context)", res.ExitCode)
	}
}

// TestRunnerExitZeroWithLingeringPipeHolder pins the other WaitDelay arm: a
// child that exits 0 while a background helper still holds stdout is a
// completed process, not a spawn failure. The captured output is returned
// and the exit code stays 0.
func TestRunnerExitZeroWithLingeringPipeHolder(t *testing.T) {
	tctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	pidFile := filepath.ToSlash(filepath.Join(t.TempDir(), "holder.pid"))
	start := time.Now()
	res, err := Runner{}.Run(tctx, core.Command{
		Argv: []string{"sh", "-c", `printf done; sleep ` + holdSeconds + ` & ` + pidReport(pidFile)},
	})
	elapsed := time.Since(start)
	t.Cleanup(func() { killReportedPid(pidFile) })
	if err != nil {
		t.Fatalf("err = %v, want a completed result (the child exited 0)", err)
	}
	if res.ExitCode != 0 || res.Stdout != "done" {
		t.Errorf("result = exit %d stdout %q, want exit 0 stdout done", res.ExitCode, res.Stdout)
	}
	if elapsed > returnBound {
		t.Errorf("Run took %v; want it to stop waiting for the pipe holder within %v", elapsed, returnBound)
	}
}

// killReportedPid reaps the helper a test left behind, if it reported one.
func killReportedPid(pidFile string) {
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
