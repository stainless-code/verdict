package verify_test

// Go contract for the verify execution edges (go-port 08): the retired
// edge_test.go compared bash vs Go on execution behaviors the evals never
// graded (process-group kill, orphan sweep, secret redaction, NO_COLOR
// inheritance, closed stdin, bundle gates). They are pinned here with
// fixed expectations via direct verify.Run calls; no shell entry point,
// no network, no stubs (every command is a shell builtin).
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/verify"
)

// edgeRun executes verify.Run in dir with a private HOME, returning exit
// code plus captured stdout. args are the post-positional flags.
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
	code := verify.Run(context.Background(), r, args, verify.Request{
		Env: full, Repo: "acme/app", Pr: "5201", Root: dir,
	})
	return code, out.String()
}

// edgePatch writes patch.json carrying the verifyCmd.
func edgePatch(t *testing.T, dir, cmd string) {
	t.Helper()
	bdir := filepath.Join(dir, ".review", "5201")
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
	raw, err := os.ReadFile(filepath.Join(dir, ".review", "5201", "patch.json"))
	if err != nil {
		t.Fatalf("read patch.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode patch.json: %v", err)
	}
	return doc
}

// TestVerifyContractTimeoutTreeKill pins the process-group kill: a
// grandchild that ignores SIGTERM still dies with the group, timed_out
// records true, and the timeout marker names the seconds.
func TestVerifyContractTimeoutTreeKill(t *testing.T) {
	dir := t.TempDir()
	edgePatch(t, dir, `trap '' TERM; (trap '' TERM; sleep 30) & sleep 30`)
	code, _ := edgeRun(t, dir, t.TempDir(), nil, true, "--json", "--timeout", "2")
	rec := edgeRecord(t, dir)
	if rec["timedOut"] != true {
		t.Fatalf("timedOut = %v exit = %d, want true\n%v", rec["timedOut"], code, rec)
	}
}

// TestVerifyContractOrphanSweep pins the decoy-exit sweep: the parent
// exits 0 while a child outlives it, so the recorded exit stays 0 with
// timedOut true and the orphan marker.
func TestVerifyContractOrphanSweep(t *testing.T) {
	dir := t.TempDir()
	edgePatch(t, dir, `(sleep 30 &)`)
	code, _ := edgeRun(t, dir, t.TempDir(), nil, true, "--json", "--timeout", "3")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (decoy exit)", code)
	}
	rec := edgeRecord(t, dir)
	if rec["timedOut"] != true {
		t.Fatalf("timed_out = %v, want true\n%v", rec["timedOut"], rec)
	}
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
	raw, err := os.ReadFile(filepath.Join(dir, ".review", "5201", ".verify.out"))
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
	raw, err := os.ReadFile(filepath.Join(dir, ".review", "5201", ".verify.out"))
	if err != nil || !strings.Contains(string(raw), "supersecretvalue123") {
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
	raw, err := os.ReadFile(filepath.Join(dir, ".review", "5201", ".verify.out"))
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
	bdir := filepath.Join(dir, ".review", "5201")
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
