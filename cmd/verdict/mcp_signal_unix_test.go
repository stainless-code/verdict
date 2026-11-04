//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
)

const envMCPChild = "VERDICT_TEST_MCP_CHILD"

const mcpSignalExitBound = 15 * time.Second

const mcpSignalToolCall = `{"jsonrpc":"2.0","id":2,"method":"tools/call",` +
	`"params":{"name":"verify","arguments":{"repo":"acme/app","pr":"5201","timeoutRaw":"300"}}}`

const mcpSignalInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize",` +
	`"params":{"protocolVersion":"2025-06-18"}}`

// TestMCPChild is the child half of TestMCPShutdownSignalKillsTheToolTree: it
// serves `verdict mcp` on the inherited stdin/stdout and exits with its code.
// It is inert in a normal suite run.
func TestMCPChild(t *testing.T) {
	if os.Getenv(envMCPChild) == "" {
		t.Skip("child half of TestMCPShutdownSignalKillsTheToolTree")
	}
	os.Exit(run([]string{"mcp"}, os.Stdout, os.Stderr, os.Getenv, os.ReadFile))
}

// TestMCPShutdownSignalKillsTheToolTree pins GO-CLI-05 for the transport: a
// SIGINT or SIGTERM to `verdict mcp` cancels the session, so the in-flight
// tool call sees the cancel (its `canceled` refusal is written), its process
// group — the verify grandchild — is dead, and the server exits within the
// bound instead of dying to the default signal action and orphaning the tree
// for up to the verify budget.
func TestMCPShutdownSignalKillsTheToolTree(t *testing.T) {
	t.Run("SIGINT", func(t *testing.T) { t.Parallel(); assertMCPSignalKillsTree(t, syscall.SIGINT) })
	t.Run("SIGTERM", func(t *testing.T) { t.Parallel(); assertMCPSignalKillsTree(t, syscall.SIGTERM) })
}

func assertMCPSignalKillsTree(t *testing.T, sig syscall.Signal) {
	t.Helper()
	got := runMCPThenSignal(t, sig)
	if got.code != cli.ExitBroke {
		t.Fatalf("exit = %d, want %d for a signaled server (a death by signal reports -1)\nstdout: %s",
			got.code, cli.ExitBroke, got.stdout)
	}
	if got.elapsed > mcpSignalExitBound {
		t.Errorf("server took %s after %v, want within %s", got.elapsed, sig, mcpSignalExitBound)
	}
	// Under -race the child is a race build, and the race detector only turns a
	// zero exit into 66: with exit 3 a report would pass unnoticed.
	if strings.Contains(got.stderr, "DATA RACE") {
		t.Errorf("the server reported a data race:\n%s", got.stderr)
	}
	assertCanceledCall(t, got.stdout)
	requireMCPChildGone(t, got.gcPID)
}

// mcpRun is one signaled `verdict mcp` session's observable outcome.
//
//nolint:govet // field order groups the process facts, then the streams
type mcpRun struct {
	gcPID   int
	code    int
	elapsed time.Duration
	stdout  string
	stderr  string
}

func runMCPThenSignal(t *testing.T, sig syscall.Signal) mcpRun {
	t.Helper()
	dir := t.TempDir()
	writeVerifyCmd(t, dir, `sleep 30 & echo $! > gc.pid; wait`)
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMCPChild$")
	cmd.Dir = dir
	cmd.Env = []string{
		envMCPChild + "=1",
		"PATH=/usr/bin:/bin",
		"HOME=" + dir,
		"XDG_CONFIG_HOME=" + dir,
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }() //nolint:errcheck // cleanup after a failed run leaves nothing behind
	if _, err := stdin.Write([]byte(mcpSignalInitialize + "\n" + mcpSignalToolCall + "\n")); err != nil {
		t.Fatal(err)
	}
	gcPID := waitForMCPPID(t, filepath.Join(dir, "gc.pid"))
	sent := time.Now()
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	waitForExit(t, cmd, mcpSignalExitBound+5*time.Second)
	elapsed := time.Since(sent)
	_ = stdin.Close() // the server is gone; the pipe end is cleanup only
	if cmd.ProcessState.ExitCode() != cli.ExitBroke {
		t.Logf("stderr: %s", stderr.String())
	}
	return mcpRun{
		gcPID: gcPID, code: cmd.ProcessState.ExitCode(), elapsed: elapsed,
		stdout: stdout.String(), stderr: stderr.String(),
	}
}

func waitForExit(t *testing.T, cmd *exec.Cmd, deadline time.Duration) {
	t.Helper()
	done := make(chan struct{}, 1)
	go func() {
		_ = cmd.Wait() //nolint:errcheck // the exit status is read from ProcessState below
		done <- struct{}{}
	}()
	select {
	case <-done:
	case <-time.After(deadline):
		_ = cmd.Process.Kill() //nolint:errcheck // the failure below is why we kill
		t.Fatalf("server still running %s after the signal, want an exit", deadline)
	}
}

func writeVerifyCmd(t *testing.T, dir, verifyCmd string) {
	t.Helper()
	bundle := filepath.Join(dir, ".review", "5201")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(map[string]string{"verifyCmd": verifyCmd})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "patch.json"), doc, 0o644); err != nil {
		t.Fatal(err)
	}
}

//
//nolint:govet // field order mirrors the decoded wire frame
type mcpCallFrame struct {
	ID     json.RawMessage `json:"id"`
	Result mcpCallResult   `json:"result"`
}

//
//nolint:govet // field order mirrors the decoded wire frame
type mcpCallResult struct {
	IsError           bool           `json:"isError"`
	StructuredContent mcpRefusalCode `json:"structuredContent"`
}

type mcpRefusalCode struct {
	Code string `json:"code"`
}

func assertCanceledCall(t *testing.T, stdout string) {
	t.Helper()
	for line := range strings.SplitSeq(stdout, "\n") {
		var frame mcpCallFrame
		if json.Unmarshal([]byte(line), &frame) != nil || string(frame.ID) != "2" {
			continue
		}
		if !frame.Result.IsError || frame.Result.StructuredContent.Code != "canceled" {
			t.Errorf("tool call result = %+v, want isError with code canceled\nstdout: %s", frame.Result, stdout)
		}
		return
	}
	t.Fatalf("no response frame for the in-flight call\nstdout: %s", stdout)
}

func waitForMCPPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if pid, ok := readCompletePID(path); ok {
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("verifyCmd never reported its grandchild pid: %s missing", path)
	return 0
}

func readCompletePID(path string) (int, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || !strings.HasSuffix(string(raw), "\n") {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func requireMCPChildGone(t *testing.T, pid int) {
	t.Helper()
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find pid %d: %v", pid, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return
		}
		if time.Now().After(deadline) {
			_ = proc.Kill() //nolint:errcheck // best-effort cleanup before the failure
			t.Fatalf("pid %d survived the MCP shutdown, want the verify tree killed", pid)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
