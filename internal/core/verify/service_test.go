package verifycore_test

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/testkit"
)

type scriptCommander struct {
	got []*core.Command
	err error
	// heads answers the checked-out-HEAD probes in order (the last repeats);
	// empty answers patchHead. Probes never land in got, which keeps the
	// spawned verify command at got[0].
	heads  []string
	res    core.Result
	probes int
}

// patchHead is the commit a scriptCommander reports as checked out.
const patchHead = "fedcba9876543210fedcba9876543210fedcba98"

// isHeadProbe reports whether cmd is verify's `git -C <root> rev-parse HEAD`.
func isHeadProbe(cmd *core.Command) bool {
	a := cmd.Argv
	return len(a) == 5 && a[0] == "git" && a[1] == "-C" && a[3] == "rev-parse" && a[4] == "HEAD"
}

func (s *scriptCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if isHeadProbe(cmd) {
		head := patchHead
		if len(s.heads) > 0 {
			head = s.heads[min(s.probes, len(s.heads)-1)]
		}
		s.probes++
		return core.Result{Stdout: head + "\n"}, nil
	}
	s.got = append(s.got, cmd)
	if s.err != nil {
		return core.Result{}, s.err
	}
	return s.res, nil
}

func bundle(t *testing.T, patch string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	testkit.WriteFile(t, ".verdict/state/5201/patch.json", patch)
}

func verifyRequest(c *testkit.Collector, cmdr *scriptCommander, timeout string, environ []string, jsonMode bool) *verifycore.Request {
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Now:       testkit.FixedNow,
			Progress:  c,
			Commander: cmdr,
		},
		Repo: "acme/app",
		Pr:   "5201",
	}
	return &verifycore.Request{
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
	c, cmdr := &testkit.Collector{}, &scriptCommander{res: core.Result{Stdout: "ok", ExitCode: 0}}
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

func checkVerifyResponse(t *testing.T, c *testkit.Collector, resp *verifycore.Response) {
	t.Helper()
	if resp.Exit != 0 || resp.TimedOut || resp.Tail != "ok" {
		t.Errorf("response = %+v", resp)
	}
	if resp.Data.Exit != 0 || resp.Data.TimedOut || resp.Data.OutputTail != "ok" || resp.Data.Patch != ".verdict/state/5201" ||
		resp.Data.Head != "fedcba9876543210fedcba9876543210fedcba98" {
		t.Errorf("data = %+v", resp.Data)
	}
	joined := strings.Join(c.HumanLines, "\n")
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
	wantOrder := []string{"verifyCmd", "verifyExit", "verifiedAt", "timedOut", "verifiedTail", "verifiedHead"}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("patch keys = %v, want %v", order, wantOrder)
	}
	checkVerifiedHead(t, raw)
	out, err := os.ReadFile(".verdict/state/5201/.verify.out")
	if err != nil || string(out) != "ok" {
		t.Errorf(".verify.out = %q, %v", out, err)
	}
}

// checkVerifiedHead proves patch.json binds the probed checked-out head.
func checkVerifiedHead(t *testing.T, raw []byte) {
	t.Helper()
	if !strings.Contains(string(raw), `"verifiedHead": "fedcba9876543210fedcba9876543210fedcba98"`) {
		t.Errorf("patch.json does not bind the checked-out head:\n%s", raw)
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
	c, cmdr := &testkit.Collector{}, &scriptCommander{}
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
	c, cmdr := &testkit.Collector{}, &scriptCommander{res: core.Result{Stdout: "leak:supersecretvalue123:done"}}
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

func TestVerifyOutputOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("NTFS carries no POSIX mode bits")
	}
	bundle(t, `{"verifyCmd":"printf tok"}`)
	cmdr := &scriptCommander{res: core.Result{Stdout: "tok=leakedvalue123"}}
	if _, ref := verifycore.Run(context.Background(),
		verifyRequest(&testkit.Collector{}, cmdr, "10", []string{"PATH=/bin"}, true)); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	info, err := os.Stat(".verdict/state/5201/.verify.out")
	if err != nil {
		t.Fatalf("stat .verify.out: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf(".verify.out mode = %#o, want 0o600", perm)
	}
}

// TestVerifyTimeout proves a timed-out run records timedOut with the
// seconds marker.
func TestVerifyTimeout(t *testing.T) {
	bundle(t, `{"verifyCmd":"sleep 30"}`)
	c, cmdr := &testkit.Collector{}, &scriptCommander{res: core.Result{Stdout: "partial", ExitCode: 137, TimedOut: true}}
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
	testkit.WriteFile(t, ".verdict/state/5201/.verify.out", "ok")
	testkit.WriteFile(t, ".verdict/state/5201/.verify.timeout", "timeout after 2s\n")
	before := readProof(t)
	ctx, cancel := context.WithCancel(context.Background())
	req := verifyRequest(&testkit.Collector{}, &scriptCommander{}, "10", []string{"PATH=/bin"}, true)
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

// TestVerifyHeadUnbound pins the runs that cannot bind a head: a HEAD that
// moves during the command, and a probe git refuses. Each still records the
// exit, drops the earlier run's verifiedHead instead of keeping it, and warns,
// because record/post flip a VERIFIED claim only on a receipt whose head
// equals the gated head.
func TestVerifyHeadUnbound(t *testing.T) {
	for _, tc := range []struct {
		name  string
		warn  string
		heads []string
	}{
		{
			"moved", "checked-out HEAD moved from fedcba9 to 0123456 during verify",
			[]string{patchHead, "0123456789012345678901234567890123456789"},
		},
		{"unreadable", "could not read the checked-out HEAD at . (git printed none)", []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkHeadUnbound(t, tc.heads, tc.warn)
		})
	}
}

func checkHeadUnbound(t *testing.T, heads []string, warn string) {
	t.Helper()
	bundle(t, `{"verifyCmd":"printf ok","verifiedHead":"1111111111111111111111111111111111111111"}`)
	c, cmdr := &testkit.Collector{}, &scriptCommander{res: core.Result{Stdout: "ok"}, heads: heads}
	resp, ref := verifycore.Run(context.Background(), verifyRequest(c, cmdr, "10", []string{"PATH=/bin"}, true))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if cmdr.probes != 2 {
		t.Errorf("head probes = %d, want one before and one after the command", cmdr.probes)
	}
	if resp.Data.Head != "" || resp.Exit != 0 {
		t.Errorf("data = %+v, want exit 0 and no head", resp.Data)
	}
	want := warn + " — patch.json records no verifiedHead, so this run cannot back a VERIFIED claim"
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %q, want %q", resp.Warnings, want)
	}
	raw := readProofFile(t, ".verdict/state/5201/patch.json")
	if strings.Contains(string(raw), "verifiedHead") || !strings.Contains(string(raw), `"verifyExit": 0`) {
		t.Errorf("patch.json = %s, want the exit recorded and no verifiedHead", raw)
	}
}

// TestVerifySuccessClearsStaleMarker pins that a successful re-verify empties
// the previous run's timeout marker.
func TestVerifySuccessClearsStaleMarker(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok"}`)
	testkit.WriteFile(t, ".verdict/state/5201/.verify.timeout", "timeout after 2s\n")
	c, cmdr := &testkit.Collector{}, &scriptCommander{res: core.Result{Stdout: "ok"}}
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
	c, cmdr := &testkit.Collector{}, &scriptCommander{res: core.Result{ExitCode: 0, TimedOut: true, Decoy: true}}
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
		testkit.WriteFile(t, ".verdict/state/5201/patch.json", patch)
	}
	c, cmdr := &testkit.Collector{}, &scriptCommander{}
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
