package verifycore_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

var fixedNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

type collector struct {
	human []string
	warns []string
}

func (c *collector) Human(line string) { c.human = append(c.human, line) }
func (c *collector) Warn(msg string)   { c.warns = append(c.warns, msg) }
func (c *collector) Diag(string)       {}

type scriptCommander struct {
	got []*core.Command
	err error
	res core.Result
}

func (s *scriptCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	s.got = append(s.got, cmd)
	if s.err != nil {
		return core.Result{}, s.err
	}
	return s.res, nil
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bundle(t *testing.T, patch string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, ".verdict/state/5201/patch.json", patch)
}

func verifyRequest(c *collector, cmdr *scriptCommander, timeout string, environ []string, jsonMode bool) *core.VerifyRequest {
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Now:       func() time.Time { return fixedNow },
			Progress:  c,
			Commander: cmdr,
		},
		Repo: "acme/app",
		Pr:   "5201",
	}
	return &core.VerifyRequest{
		ReadRequest: read,
		TimeoutRaw:  timeout,
		Environ:     environ,
		JSONMode:    jsonMode,
	}
}

// TestVerifySuccess proves the happy path: the child argv, the ordered
// patch write, the envelope data, and the streamed human lines.
func TestVerifySuccess(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok"}`)
	c, cmdr := &collector{}, &scriptCommander{res: core.Result{Stdout: "ok", ExitCode: 0}}
	resp, ref := verifycore.Run(context.Background(), verifyRequest(c, cmdr, "10", []string{"PATH=/bin", "HOME=/tmp/h"}, true))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	checkVerifySpawn(t, cmdr)
	checkVerifyResponse(t, c, resp)
	checkVerifyStores(t)
}

func checkVerifySpawn(t *testing.T, cmdr *scriptCommander) {
	t.Helper()
	if len(cmdr.got) != 1 {
		t.Fatalf("commands = %d, want 1", len(cmdr.got))
	}
	if got := cmdr.got[0].Argv; len(got) != 3 || got[0] != "bash" || got[1] != "-c" || got[2] != "printf ok" {
		t.Errorf("argv = %v", got)
	}
}

func checkVerifyResponse(t *testing.T, c *collector, resp *core.VerifyResponse) {
	t.Helper()
	if resp.Exit != 0 || resp.TimedOut || resp.Tail != "ok" {
		t.Errorf("response = %+v", resp)
	}
	if resp.Data.Exit != 0 || resp.Data.TimedOut || resp.Data.OutputTail != "ok" || resp.Data.Patch != ".verdict/state/5201" {
		t.Errorf("data = %+v", resp.Data)
	}
	joined := strings.Join(c.human, "\n")
	for _, want := range []string{"» verify: printf ok (timeout 10s)", "» exit=0 timed_out=false", "recorded into .verdict/state/5201/patch.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("human lines miss %q:\n%s", want, joined)
		}
	}
}

func checkVerifyStores(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(".verdict/state/5201/patch.json")
	if err != nil {
		t.Fatalf("read patch.json: %v", err)
	}
	var order []string
	for ln := range strings.SplitSeq(string(raw), "\n") {
		if ln = strings.TrimSpace(ln); strings.HasPrefix(ln, `"`) {
			order = append(order, strings.SplitN(ln[1:], `"`, 2)[0])
		}
	}
	wantOrder := []string{"verifyCmd", "verifyExit", "verifiedAt", "timedOut", "verifiedTail"}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("patch keys = %v, want %v", order, wantOrder)
	}
	out, err := os.ReadFile(".verdict/state/5201/.verify.out")
	if err != nil || string(out) != "ok" {
		t.Errorf(".verify.out = %q, %v", out, err)
	}
}

// TestVerifyNoColor proves the JSON-mode NO_COLOR default reaches the
// child only when absent.
func TestVerifyNoColor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		want    string
		environ []string
	}{
		{environ: []string{"PATH=/bin"}, name: "default-on", want: "NO_COLOR=1"},
		{environ: []string{"PATH=/bin", "NO_COLOR=0"}, name: "ambient-wins", want: "NO_COLOR=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkNoColor(t, tc.environ, tc.want)
		})
	}
}

func checkNoColor(t *testing.T, environ []string, want string) {
	t.Helper()
	bundle(t, `{"verifyCmd":"printf ok"}`)
	c, cmdr := &collector{}, &scriptCommander{}
	if _, ref := verifycore.Run(context.Background(), verifyRequest(c, cmdr, "10", environ, true)); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	found := ""
	for _, kv := range cmdr.got[0].Env {
		if strings.HasPrefix(kv, "NO_COLOR=") {
			found = kv
		}
	}
	if found != want {
		t.Errorf("child NO_COLOR = %q, want %q", found, want)
	}
}

// TestVerifyRedaction proves secret values never reach the recorded tail
// while .verify.out keeps raw output.
func TestVerifyRedaction(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf tail"}`)
	c, cmdr := &collector{}, &scriptCommander{res: core.Result{Stdout: "leak:supersecretvalue123:done"}}
	resp, ref := verifycore.Run(context.Background(), verifyRequest(c, cmdr, "10",
		[]string{"PATH=/bin", "EVAL_PROBE_TOKEN=supersecretvalue123"}, true))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if strings.Contains(resp.Tail, "supersecretvalue123") {
		t.Errorf("tail leaks the secret: %q", resp.Tail)
	}
	if !strings.Contains(resp.Tail, "***REDACTED***") {
		t.Errorf("tail carries no redaction marker: %q", resp.Tail)
	}
	out, err := os.ReadFile(".verdict/state/5201/.verify.out")
	if err != nil || !strings.Contains(string(out), "supersecretvalue123") {
		t.Errorf(".verify.out scrubbed, want raw: %q", out)
	}
}

// TestVerifyTimeout proves a timed-out run records timedOut with the
// seconds marker.
func TestVerifyTimeout(t *testing.T) {
	bundle(t, `{"verifyCmd":"sleep 30"}`)
	c, cmdr := &collector{}, &scriptCommander{res: core.Result{Stdout: "partial", ExitCode: 137, TimedOut: true}}
	resp, ref := verifycore.Run(context.Background(), verifyRequest(c, cmdr, "2", []string{"PATH=/bin"}, true))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.TimedOut {
		t.Errorf("response = %+v, want timedOut", resp)
	}
	marker, err := os.ReadFile(".verdict/state/5201/.verify.timeout")
	if err != nil || string(marker) != "timeout after 2s\n" {
		t.Errorf("marker = %q, %v", marker, err)
	}
}

type cancelCommander struct{ cancel context.CancelFunc }

func (c cancelCommander) Run(context.Context, *core.Command) (core.Result, error) {
	c.cancel()
	return core.Result{}, nil
}

type proof struct{ patch, out, marker []byte }

func readProof(t *testing.T) proof {
	t.Helper()
	return proof{
		patch:  readProofFile(t, ".verdict/state/5201/patch.json"),
		out:    readProofFile(t, ".verdict/state/5201/.verify.out"),
		marker: readProofFile(t, ".verdict/state/5201/.verify.timeout"),
	}
}

func readProofFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// TestVerifyCancelKeepsPriorProof pins that a canceled re-verify leaves every
// proof file untouched.
func TestVerifyCancelKeepsPriorProof(t *testing.T) {
	bundle(t, `{"verifyCmd":"sleep 30","verifyExit":0,"verifiedAt":"2026-11-01T12:00:00Z","timedOut":false,"verifiedTail":"ok"}`)
	writeFile(t, ".verdict/state/5201/.verify.out", "ok")
	writeFile(t, ".verdict/state/5201/.verify.timeout", "timeout after 2s\n")
	before := readProof(t)
	ctx, cancel := context.WithCancel(context.Background())
	req := verifyRequest(&collector{}, &scriptCommander{}, "10", []string{"PATH=/bin"}, true)
	req.Commander = cancelCommander{cancel: cancel}
	_, ref := verifycore.Run(ctx, req)
	if ref == nil || ref.Code != "canceled" {
		t.Fatalf("Run refusal = %v, want canceled", ref)
	}
	after := readProof(t)
	if !bytes.Equal(after.patch, before.patch) || !bytes.Equal(after.out, before.out) || !bytes.Equal(after.marker, before.marker) {
		t.Errorf("a canceled run mutated the proof: out %q -> %q, marker %q -> %q, patch changed %t",
			before.out, after.out, before.marker, after.marker, !bytes.Equal(after.patch, before.patch))
	}
}

// TestVerifySuccessClearsStaleMarker pins that a successful re-verify empties
// the previous run's timeout marker.
func TestVerifySuccessClearsStaleMarker(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok"}`)
	writeFile(t, ".verdict/state/5201/.verify.timeout", "timeout after 2s\n")
	c, cmdr := &collector{}, &scriptCommander{res: core.Result{Stdout: "ok"}}
	if _, ref := verifycore.Run(context.Background(), verifyRequest(c, cmdr, "10", []string{"PATH=/bin"}, true)); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if marker := readProofFile(t, ".verdict/state/5201/.verify.timeout"); len(marker) != 0 {
		t.Errorf("stale .verify.timeout survived a successful run: %q", marker)
	}
}

// TestVerifyDecoy proves a sweep-fired run records the orphan marker with
// the exit intact.
func TestVerifyDecoy(t *testing.T) {
	bundle(t, `{"verifyCmd":"(sleep 30 &)"}`)
	c, cmdr := &collector{}, &scriptCommander{res: core.Result{ExitCode: 0, TimedOut: true, Decoy: true}}
	resp, ref := verifycore.Run(context.Background(), verifyRequest(c, cmdr, "10", []string{"PATH=/bin"}, true))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Exit != 0 || !resp.TimedOut {
		t.Errorf("response = %+v, want exit 0 timedOut", resp)
	}
	marker, err := os.ReadFile(".verdict/state/5201/.verify.timeout")
	if err != nil || string(marker) != "orphaned command tree killed after exit\n" {
		t.Errorf("marker = %q, %v", marker, err)
	}
}

// TestVerifyRefusals proves the bundle, input, timeout, and gate refusals
// stay typed, carry warnings, and write no outcome.
func TestVerifyRefusals(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok"}`)
	cases := []struct {
		name    string
		patch   string
		timeout string
		code    string
	}{
		{"missing-patch", "", "10", "missing-patch"},
		{"no-verify-cmd", `{}`, "10", "no-verify-cmd"},
		{"bad-timeout", `{"verifyCmd":"printf ok"}`, "abc", "bad-timeout"},
		{"bad-timeout-zero", `{"verifyCmd":"printf ok"}`, "0", "bad-timeout"},
		{"bad-timeout-negative", `{"verifyCmd":"printf ok"}`, "-5", "bad-timeout"},
		{"bad-timeout-overflow", `{"verifyCmd":"printf ok"}`, "99999999999999999999", "bad-timeout"},
		{"bad-timeout-max-plus-one", `{"verifyCmd":"printf ok"}`, "9223372036854775808", "bad-timeout"},
		{"gate", `{"verifyCmd":"rm -rf ./scratch-del"}`, "10", "verify-destructive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkVerifyRefusal(t, tc.name, tc.patch, tc.timeout, tc.code)
		})
	}
}

func checkVerifyRefusal(t *testing.T, name, patch, timeout, code string) {
	t.Helper()
	bundle(t, `{"verifyCmd":"printf ok"}`)
	if name == "missing-patch" {
		_ = os.Remove(".verdict/state/5201/patch.json")
	} else {
		writeFile(t, ".verdict/state/5201/patch.json", patch)
	}
	c, cmdr := &collector{}, &scriptCommander{}
	req := verifyRequest(c, cmdr, timeout, []string{"PATH=/bin"}, false)
	req.Warnings = []string{"prefs warning"}
	_, ref := verifycore.Run(context.Background(), req)
	if ref == nil {
		t.Fatalf("expected refusal %s", code)
	}
	if ref.Code != code {
		t.Errorf("code = %q, want %q", ref.Code, code)
	}
	if len(ref.Warnings) == 0 || ref.Warnings[0] != "prefs warning" {
		t.Errorf("warnings = %v, want prefs warning carried", ref.Warnings)
	}
	if len(cmdr.got) != 0 {
		t.Errorf("refusal spawned %d commands", len(cmdr.got))
	}
	if _, err := os.Stat(".verdict/state/5201/.verify.out"); !os.IsNotExist(err) {
		t.Errorf("refusal wrote .verify.out")
	}
}
