package verify_test

// Go contract for the verify execution edges: the retired cross-implementation
// suite compared bash vs Go on execution behaviors the evals never
// graded (process-group kill, orphan sweep, secret redaction, NO_COLOR
// inheritance, closed stdin, bundle gates). They are pinned here with
// fixed expectations via direct verify.Run calls; no shell entry point,
// no network, no stubs (every command is a shell builtin).
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
	"github.com/stainless-code/verdict/internal/verify"
)

// edgeRun executes verify.Run in dir with a private HOME, returning exit
// code plus captured stdout. args are the post-positional flags; --json is
// the global toggle (jsonMode) and never reaches the verb parser.
func edgeRun(t *testing.T, dir, home string, env map[string]string, jsonMode bool, args ...string) (int, string) {
	t.Helper()
	t.Chdir(dir)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	full := map[string]string{"HOME": home}
	for k, v := range env {
		full[k] = v
		t.Setenv(k, v)
	}
	var out, derr strings.Builder
	r := &cli.Runner{
		Cmd: "verify", Repo: "acme/app", Pr: "5201",
		Out: &out, Diag: &derr, Human: &out,
		Now: time.Now, Effective: cli.Defaults(),
		JSONMode: jsonMode, EnvNoColor: true,
	}
	if jsonMode {
		r.Human = &derr
	}
	code := verify.Run(context.Background(), r, withoutJSON(args), verify.Request{
		Env: full, Repo: "acme/app", Pr: "5201", Root: dir,
	})
	return code, out.String()
}

// withoutJSON drops the global --json toggle from verb-local args.
func withoutJSON(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != "--json" {
			out = append(out, a)
		}
	}
	return out
}

// edgePatch writes patch.json carrying the verifyCmd.
func edgePatch(t *testing.T, dir, cmd string) {
	t.Helper()
	bdir := filepath.Join(dir, ".verdict", "state", "5201")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(map[string]string{"verifyCmd": cmd})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "patch.json"), doc, 0o644); err != nil {
		t.Fatal(err)
	}
}

// edgeRecord reads the patch record back.
func edgeRecord(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict", "state", "5201", "patch.json"))
	if err != nil {
		t.Fatalf("read patch.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode patch.json: %v", err)
	}
	return doc
}

// readPID reads one pid file the verify child wrote.
func readPID(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file %s: %v", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("pid file %s = %q: %v", path, raw, err)
	}
	return pid
}

// requireProcessGone proves the pid is dead, polling past the reparent/reap
// window. Windows needs a different liveness API; its tree kill is covered
// by core/exec's Windows tests.
func requireProcessGone(t *testing.T, pid int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("find pid %d: %v", pid, err)
	}
	t.Cleanup(func() {
		_ = proc.Kill() //nolint:errcheck // best-effort cleanup of a survivor after the failure
	})
	testenv.Until(t, func() bool {
		return proc.Signal(syscall.Signal(0)) != nil
	})
}

// verifyMarker reads the recorded .verify.timeout marker.
func verifyMarker(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict", "state", "5201", ".verify.timeout"))
	if err != nil {
		t.Fatalf("read .verify.timeout: %v", err)
	}
	return string(raw)
}

// TestVerifyContractTimeoutTreeKill pins the process-group kill: a
// grandchild that ignores SIGTERM writes its pid, and the timeout must kill
// it with the group, record timed_out, and name the seconds.
func TestVerifyContractTimeoutTreeKill(t *testing.T) {
	dir := t.TempDir()
	edgePatch(t, dir, `trap '' TERM; (trap '' TERM; sleep 30 & echo $! > gc.pid; wait) & sleep 30`)
	code, _ := edgeRun(t, dir, t.TempDir(), nil, true, "--json", "--timeout", "2")
	rec := edgeRecord(t, dir)
	if rec["timedOut"] != true {
		t.Fatalf("timedOut = %v exit = %d, want true\n%v", rec["timedOut"], code, rec)
	}
	if got := verifyMarker(t, dir); got != "timeout after 2s\n" {
		t.Errorf(".verify.timeout = %q, want the 2s timeout line", got)
	}
	requireProcessGone(t, readPID(t, filepath.Join(dir, "gc.pid")))
}

// TestVerifyContractOrphanSweep pins the decoy-exit sweep: the parent
// exits 0 while a grandchild outlives it, so the recorded exit stays 0 with
// timedOut true, the orphan marker, and the grandchild dead.
func TestVerifyContractOrphanSweep(t *testing.T) {
	dir := t.TempDir()
	edgePatch(t, dir, `(sleep 30 & echo $! > orphan.pid)`)
	code, _ := edgeRun(t, dir, t.TempDir(), nil, true, "--json", "--timeout", "3")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (decoy exit)", code)
	}
	rec := edgeRecord(t, dir)
	if rec["timedOut"] != true {
		t.Fatalf("timed_out = %v, want true\n%v", rec["timedOut"], rec)
	}
	if got := verifyMarker(t, dir); got != "orphaned command tree killed after exit\n" {
		t.Errorf(".verify.timeout = %q, want the orphan marker", got)
	}
	requireProcessGone(t, readPID(t, filepath.Join(dir, "orphan.pid")))
}

// TestVerifyContractTruncatedOutput pins the capture cap: a noisy but
// successful command records a bounded .verify.out carrying the truncation
// marker (and the redacted proof tail names it too), instead of growing the
// process with the child's output.
func TestVerifyContractTruncatedOutput(t *testing.T) {
	dir := t.TempDir()
	edgePatch(t, dir, `yes | head -c 2097152`)
	code, _ := edgeRun(t, dir, t.TempDir(), nil, true, "--json", "--timeout", "10")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict", "state", "5201", ".verify.out"))
	if err != nil {
		t.Fatalf("read .verify.out: %v", err)
	}
	if len(raw) > coreexec.VerifyOutputCap+256 {
		t.Errorf(".verify.out = %d bytes, want <= the %d-byte cap plus the marker", len(raw), coreexec.VerifyOutputCap)
	}
	if !strings.Contains(string(raw), "output truncated") {
		t.Errorf(".verify.out carries no truncation marker")
	}
	rec := edgeRecord(t, dir)
	tail, ok := rec["verifiedTail"].(string)
	if !ok || !strings.Contains(tail, "truncated") {
		t.Errorf("verifiedTail = %q, want the truncation marker", tail)
	}
}

// TestVerifyContractRedaction pins secret redaction: the recorded tail
// never carries the known secret value, while .verify.out keeps raw
// output for debugging.
func TestVerifyContractRedaction(t *testing.T) {
	dir := t.TempDir()
	edgePatch(t, dir, `printf '%s' "$EVAL_PROBE_TOKEN"`)
	env := map[string]string{"EVAL_PROBE_TOKEN": "supersecretvalue123"}
	code, _ := edgeRun(t, dir, t.TempDir(), env, true, "--json", "--timeout", "10")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	rec := edgeRecord(t, dir)
	tail, ok := rec["verifiedTail"].(string)
	if !ok || tail == "" {
		t.Fatalf("recorded tail empty, want redacted output\n%v", rec)
	}
	if strings.Contains(tail, "supersecretvalue123") {
		t.Fatalf("recorded tail leaks the secret:\n%v", rec)
	}
	if !strings.Contains(tail, "***REDACTED***") {
		t.Fatalf("recorded tail carries no redaction marker:\n%v", rec)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict", "state", "5201", ".verify.out"))
	if err != nil || !strings.Contains(string(raw), "supersecretvalue123") {
		t.Fatalf(".verify.out = %q, want raw output", raw)
	}
}

// TestVerifyContractShapeRedaction pins the shape half of the redaction
// contract end to end: a command that echoes a leaked token, an
// Authorization header, URL userinfo, and ANSI colors records a plain,
// redacted tail — the value list alone cannot see any of them — while
// .verify.out keeps the raw bytes for debugging.
func TestVerifyContractShapeRedaction(t *testing.T) {
	dir := t.TempDir()
	edgePatch(t, dir, `printf '\033[31m%s\033[0m\r\n' "Authorization: Bearer ghp_0123456789abcdefghij"`+
		`; printf 'clone https://alice:s3cretpw@example.invalid/r.git\n'`)
	if code, _ := edgeRun(t, dir, t.TempDir(), nil, true, "--json", "--timeout", "10"); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	rec := edgeRecord(t, dir)
	tail, ok := rec["verifiedTail"].(string)
	if !ok || tail == "" {
		t.Fatalf("recorded tail empty, want redacted output\n%v", rec)
	}
	checkTailRedacted(t, tail)
	checkRawOutputKept(t, dir)
}

// checkTailRedacted proves the recorded tail carries no credential, no ANSI
// escape, and no carriage return, while naming what it did redact.
func checkTailRedacted(t *testing.T, tail string) {
	t.Helper()
	for _, leak := range []string{"ghp_0123456789abcdefghij", "s3cretpw", "\x1b", "\r"} {
		if strings.Contains(tail, leak) {
			t.Fatalf("recorded tail leaks %q:\n%q", leak, tail)
		}
	}
	for _, want := range []string{
		"Authorization: ***REDACTED***",
		"clone https://***REDACTED***@example.invalid/r.git",
	} {
		if !strings.Contains(tail, want) {
			t.Fatalf("recorded tail misses %q:\n%q", want, tail)
		}
	}
}

// checkRawOutputKept proves .verify.out still holds the raw bytes: debugging a
// failed proof needs what the command actually printed.
func checkRawOutputKept(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict", "state", "5201", ".verify.out"))
	if err != nil {
		t.Fatalf("read .verify.out: %v", err)
	}
	if !strings.Contains(string(raw), "ghp_0123456789abcdefghij") || !strings.Contains(string(raw), "\x1b") {
		t.Fatalf(".verify.out = %q, want raw output", raw)
	}
}

// TestVerifyContractNoColor pins NO_COLOR inheritance: under --json with
// no ambient NO_COLOR, the verify child sees NO_COLOR=1.
func TestVerifyContractNoColor(t *testing.T) {
	dir := t.TempDir()
	edgePatch(t, dir, `printf '%s' "$NO_COLOR"`)
	code, _ := edgeRun(t, dir, t.TempDir(), nil, true, "--json", "--timeout", "10")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict", "state", "5201", ".verify.out"))
	if err != nil || string(raw) != "1" {
		t.Fatalf(".verify.out = %q, want NO_COLOR=1", raw)
	}
}

// TestVerifyContractStdinClosed pins closed-stdin completion: a
// stdin-consuming verifyCmd finishes promptly with timedOut false.
func TestVerifyContractStdinClosed(t *testing.T) {
	dir := t.TempDir()
	edgePatch(t, dir, `read -r line || true`)
	code, _ := edgeRun(t, dir, t.TempDir(), nil, true, "--json", "--timeout", "10")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	rec := edgeRecord(t, dir)
	if timed, ok := rec["timedOut"].(bool); !ok || timed {
		t.Fatalf("timedOut = %v, want false\n%v", rec["timedOut"], rec)
	}
}

// TestVerifyContractBundleGates pins the bundle-gate refusals and their
// no-write shape, plus the timeout flag and unknown-flag usage errors.
func TestVerifyContractBundleGates(t *testing.T) {
	dir := t.TempDir()
	assertGateRefusal(t, dir, "missing-patch")
	writeBarePatch(t, dir)
	assertGateRefusal(t, dir, "no-verify-cmd")
	edgePatch(t, dir, "")
	assertGateRefusal(t, dir, "no-verify-cmd")
	edgePatch(t, dir, `printf ok`)
	assertBadTimeouts(t, dir)
	code, _ := edgeRun(t, dir, t.TempDir(), nil, false, "--bogus")
	if code != 2 {
		t.Fatalf("unknown flag exit = %d, want 2", code)
	}
}

// assertGateRefusal pins one bundle-gate refusal code.
func assertGateRefusal(t *testing.T, dir, want string) {
	t.Helper()
	code, stdout := edgeRun(t, dir, t.TempDir(), nil, true, "--json", "--timeout", "10")
	if code != 1 || edgeCode(stdout) != want {
		t.Fatalf("exit = %d code = %q, want 1/%s\n%s", code, edgeCode(stdout), want, stdout)
	}
}

// writeBarePatch writes an empty patch.json, exercising the missing-cmd gate.
func writeBarePatch(t *testing.T, dir string) {
	t.Helper()
	bdir := filepath.Join(dir, ".verdict", "state", "5201")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "patch.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertBadTimeouts pins the timeout-flag refusals.
func assertBadTimeouts(t *testing.T, dir string) {
	t.Helper()
	for _, timeout := range []string{"0", "abc", "-5", "99999999999999999999"} {
		code, stdout := edgeRun(t, dir, t.TempDir(), nil, true, "--json", "--timeout", timeout)
		if code != 1 || edgeCode(stdout) != "bad-timeout" {
			t.Fatalf("timeout %q exit = %d code = %q, want 1/bad-timeout\n%s", timeout, code, edgeCode(stdout), stdout)
		}
	}
}

// edgeCode reads the envelope error code.
func edgeCode(stdout string) string {
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		return "undecodable: " + stdout
	}
	e, ok := doc["error"].(map[string]any)
	if !ok {
		return ""
	}
	c, ok := e["code"].(string)
	if !ok {
		return ""
	}
	return c
}

// TestVerifyContractCancelIsNotATimeout pins GO-CLI-05 for verify: a caller
// cancel (Ctrl-C, SIGTERM, a host cancel) kills the whole tree, exits 3 with
// the `canceled` refusal, and records no proof. It used to be recorded as a
// failed run — exit 137, timedOut true, "timeout after 300s" — and exit 0.
func TestVerifyContractCancelIsNotATimeout(t *testing.T) {
	dir := t.TempDir()
	edgePatch(t, dir, `sleep 30 & echo $! > gc.pid; wait`)
	code, stdout := runVerifyCanceledOnceUp(t, dir)
	if code != cli.ExitBroke {
		t.Fatalf("exit = %d, want %d (canceled)\nstdout: %s", code, cli.ExitBroke, stdout)
	}
	if !strings.Contains(stdout, `"code": "canceled"`) {
		t.Errorf("stdout lacks the canceled refusal:\n%s", stdout)
	}
	bundle := filepath.Join(dir, ".verdict", "state", "5201")
	if marker, _ := os.ReadFile(filepath.Join(bundle, ".verify.timeout")); len(marker) > 0 { //nolint:errcheck // an absent marker is the passing case
		t.Errorf(".verify.timeout = %q, want no timeout recorded for a cancel", marker)
	}
	patch := mustRead(t, filepath.Join(bundle, "patch.json"))
	if strings.Contains(patch, "timedOut") || strings.Contains(patch, `"exit"`) {
		t.Errorf("patch.json recorded a verify outcome for a canceled run: %s", patch)
	}
	requireProcessGone(t, readPID(t, filepath.Join(dir, "gc.pid")))
}

// runVerifyCanceledOnceUp runs verify in dir and cancels its context once the
// command has reported its grandchild pid, so a live tree is what dies.
func runVerifyCanceledOnceUp(t *testing.T, dir string) (int, string) {
	t.Helper()
	home := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", home)
	var out, derr strings.Builder
	r := &cli.Runner{
		Cmd: "verify", Repo: "acme/app", Pr: "5201",
		Out: &out, Diag: &derr, Human: &derr,
		Now: time.Now, Effective: cli.Defaults(),
		JSONMode: true, EnvNoColor: true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for range 400 {
			if raw, err := os.ReadFile(filepath.Join(dir, "gc.pid")); err == nil && strings.HasSuffix(string(raw), "\n") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	code := verify.Run(ctx, r, nil, verify.Request{
		Env: map[string]string{"HOME": home}, Repo: "acme/app", Pr: "5201", Root: dir,
	})
	return code, out.String()
}

// mustRead returns a file's text, failing the test on any error.
func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
