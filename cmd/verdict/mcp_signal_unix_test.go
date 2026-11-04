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

// envMCPChild marks the re-executed test binary that plays `verdict mcp` in
// TestMCPShutdownSignalKillsTheToolTree.
const envMCPChild = "VERDICT_TEST_MCP_CHILD"

// mcpSignalExitBound is the wall clock a signaled MCP server gets to cancel
// its handlers and exit. The kill path is the watchdog's group kill plus the
// exec wait delay, both well under a second.
const mcpSignalExitBound = 15 * time.Second

// mcpSignalToolCall is the one tool call the child serves: verify runs the
// recorded verifyCmd in its own process group, so the grandchild it spawns is
// exactly the process a host's SIGTERM used to orphan.
const mcpSignalToolCall = `{"jsonrpc":"2.0","id":2,"method":"tools/call",` +
	`"params":{"name":"verify","arguments":{"repo":"acme/app","pr":"5201","timeoutRaw":"300"}}}`

// mcpSignalInitialize is the handshake frame a host sends first.
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
// SIGTERM to `verdict mcp` cancels the session, so the in-flight tool call sees
// the cancel (its `canceled` refusal is written), its process group — the
// verify grandchild — is dead, and the server exits within the bound instead of
// dying to the default signal action and orphaning the tree for up to the
// verify budget.
func TestMCPShutdownSignalKillsTheToolTree(t *testing.T) {
	gcPID, stdout, elapsed, code := runMCPThenSignal(t, syscall.SIGTERM)
	if code != cli.ExitBroke {
		t.Fatalf("exit = %d, want %d for a signaled server (a death by signal reports -1)\nstdout: %s",
			code, cli.ExitBroke, stdout)
	}
	if elapsed > mcpSignalExitBound {
		t.Errorf("server took %s after SIGTERM, want within %s", elapsed, mcpSignalExitBound)
	}
	assertCanceledCall(t, stdout)
	requireMCPChildGone(t, gcPID)
}

// runMCPThenSignal serves `verdict mcp` with a verify call in flight, sends sig
// once the grandchild has reported its pid, and returns the exit code, stdout,
// the grandchild pid, and the time the server took after the signal.
func runMCPThenSignal(t *testing.T, sig syscall.Signal) (int, string, time.Duration, int) {
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
	// The host keeps its end of stdin open: the signal, not EOF, is what ends
	// this session.
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
	return gcPID, stdout.String(), elapsed, cmd.ProcessState.ExitCode()
}

// waitForExit fails the test if the server is still running at the deadline.
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

// writeVerifyCmd writes the bundle the verify tool call runs in.
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

// mcpCallFrame is one response frame: the request id plus the tool result.
// Field order mirrors the decoded wire frame (govet fieldalignment).
//
//nolint:govet // field order mirrors the decoded wire frame
type mcpCallFrame struct {
	ID     json.RawMessage `json:"id"`
	Result mcpCallResult   `json:"result"`
}

// mcpCallResult is one tool result's error flag and typed refusal.
//
//nolint:govet // field order mirrors the decoded wire frame
type mcpCallResult struct {
	IsError           bool           `json:"isError"`
	StructuredContent mcpRefusalCode `json:"structuredContent"`
}

// mcpRefusalCode is a refusal's machine-readable code.
type mcpRefusalCode struct {
	Code string `json:"code"`
}

// assertCanceledCall proves the in-flight tool call reported the cancel: the
// response frame for id 2 is an error whose structured content names the
// `canceled` code (not a recorded timeout, not a missing frame).
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

// waitForMCPPID polls until the verifyCmd's grandchild has reported its pid.
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

// readCompletePID reads a whole pid file, reporting ok only for a positive pid.
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

// requireMCPChildGone polls past the reparent/reap window until the pid is dead
// and fails if it outlives the tree kill.
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
