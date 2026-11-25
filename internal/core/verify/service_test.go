package verifycore_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
	// onRun acts for the verify command while it "runs": a swapped bundle, an
	// edited patch.json.
	onRun func()
	// tree is the content tree the snapshot reads answer; empty answers
	// patchTree. git calls never land in got, which keeps the spawned verify
	// command at got[0].
	tree string
	res  core.Result
	gits int
}

const (
	// patchHead is the commit a fake reports as checked out.
	patchHead = "fedcba9876543210fedcba9876543210fedcba98"
	// patchTree is the content tree a fake's snapshot reads name.
	patchTree = "7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7"
	// snapshotCommit is the commit-tree id a fake answers.
	snapshotCommit = "5aa95aa95aa95aa95aa95aa95aa95aa95aa95aa9"
)

// gitAnswer plays git for verify's own reads: the checked-out head, the
// snapshot's tree and commit, and a `worktree add` that leaves a checkout
// with a .git (the sandbox inspects it), so a test scripts only the command.
func gitAnswer(cmd *core.Command, tree string) (core.Result, bool) {
	a := cmd.Argv
	if len(a) < 4 || a[0] != "git" {
		return core.Result{}, false
	}
	if tree == "" {
		tree = patchTree
	}
	switch {
	case slices.Equal(a[3:], []string{"rev-parse", "HEAD"}):
		return core.Result{Stdout: patchHead + "\n"}, true
	case a[3] == "rev-parse" || a[3] == "write-tree":
		return core.Result{Stdout: tree + "\n"}, true
	case a[3] == "commit-tree":
		return core.Result{Stdout: snapshotCommit + "\n"}, true
	case a[3] == "worktree" && len(a) > 4 && a[4] == "add":
		fakeCheckout(a[len(a)-2])
	}
	return core.Result{}, true
}

// fakeCheckout leaves the .git a `worktree add` would, which the sandbox
// inspects.
func fakeCheckout(dest string) {
	if err := os.MkdirAll(filepath.Join(dest, ".git"), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(dest, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		panic(err)
	}
}

func (s *scriptCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if res, ok := gitAnswer(cmd, s.tree); ok {
		s.gits++
		return res, nil
	}
	s.got = append(s.got, cmd)
	if s.onRun != nil {
		s.onRun()
	}
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
		resp.Data.Tree != "7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7" {
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
	wantOrder := []string{"verifyCmd", "verifyExit", "verifiedAt", "timedOut", "verifiedTail"}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("patch keys = %v, want %v", order, wantOrder)
	}
	checkReceiptFile(t, "")
	out, err := os.ReadFile(".verdict/state/5201/.verify.out")
	if err != nil || string(out) != "ok" {
		t.Errorf(".verify.out = %q, %v", out, err)
	}
}

// receiptAt is the receipt a run of `printf ok` on the patchTree content
// leaves, naming sig when patch.json named one.
func receiptAt(sig string) string {
	sigLine := ""
	if sig != "" {
		sigLine = "\n  \"sig\": \"" + sig + "\","
	}
	return "{\n  \"schema_version\": \"1\",\n  \"command\": \"printf ok\"," + sigLine +
		"\n  \"tree\": \"7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7ee7\",\n  \"at\": \"2026-11-02T12:00:00Z\"," +
		"\n  \"exit\": 0,\n  \"timed_out\": false\n}\n"
}

// checkReceiptFile proves verify wrote its tool-only receipt beside
// patch.json: the command it ran, the sig patch.json named, and the content
// tree it ran on.
func checkReceiptFile(t *testing.T, sig string) {
	t.Helper()
	if got, want := string(readProofFile(t, ".verdict/state/5201/verify-receipt.json")), receiptAt(sig); got != want {
		t.Errorf("verify-receipt.json =\n%s\nwant\n%s", got, want)
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

// TestVerifyWithoutSnapshot pins a checkout git cannot snapshot (not a
// repository): the command still runs, in place, and the run records its
// exit, but the receipt names no content tree — so it cannot back a VERIFIED
// claim — and the run says so. An earlier receipt is replaced, never kept.
func TestVerifyWithoutSnapshot(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok"}`)
	testkit.WriteFile(t, ".verdict/state/5201/verify-receipt.json", receiptAt(""))
	cmdr := &notRepoCommander{res: core.Result{Stdout: "ok"}}
	req := verifyRequest(&testkit.Collector{}, &scriptCommander{}, "10", []string{"PATH=/bin"}, true)
	req.Commander = cmdr
	resp, ref := verifycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Tree != "" || resp.Exit != 0 || len(cmdr.got) != 1 || cmdr.got[0].Dir != "." {
		t.Fatalf("data = %+v, command dir %q; want exit 0, no tree, the command in place", resp.Data, firstCmdDir(cmdr.got))
	}
	want := "the command runs in place and verify-receipt.json records no tree, so this run cannot back a VERIFIED claim"
	if !strings.Contains(strings.Join(resp.Warnings, "\n"), want) {
		t.Errorf("warnings = %q, want one saying %q", resp.Warnings, want)
	}
	raw := string(readProofFile(t, ".verdict/state/5201/verify-receipt.json"))
	if strings.Contains(raw, `"tree"`) || !strings.Contains(raw, `"exit": 0`) {
		t.Errorf("verify-receipt.json = %s, want the exit recorded and no tree", raw)
	}
}

// notRepoCommander answers every git read the way git does outside a
// repository and scripts the command.
type notRepoCommander struct {
	got []*core.Command
	res core.Result
}

func (c *notRepoCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if cmd.Argv[0] == "git" {
		return core.Result{ExitCode: 128, Stderr: "fatal: not a git repository"}, nil
	}
	c.got = append(c.got, cmd)
	return c.res, nil
}

func firstCmdDir(cmds []*core.Command) string {
	if len(cmds) == 0 {
		return ""
	}
	return cmds[0].Dir
}

// TestVerifyRunsInSnapshot pins where the command runs and what the receipt
// binds: a snapshot checkout of the content under the bundle — never the
// user's checkout, so nothing done there while it runs changes what it
// tested — with the content tree and the finding sig patch.json names.
func TestVerifyRunsInSnapshot(t *testing.T) {
	bundle(t, `{"verifyCmd":"printf ok","findingSig":"c123456789abcdef"}`)
	cmdr := &scriptCommander{res: core.Result{Stdout: "ok"}}
	if _, ref := verifycore.Run(context.Background(), verifyRequest(&testkit.Collector{}, cmdr, "10", []string{"PATH=/bin"}, true)); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if len(cmdr.got) != 1 || !strings.Contains(filepath.ToSlash(cmdr.got[0].Dir), "/.verdict/state/5201/.snapshot-") {
		t.Fatalf("command dir = %q, want a snapshot under the bundle", firstCmdDir(cmdr.got))
	}
	if _, err := os.Stat(cmdr.got[0].Dir); !os.IsNotExist(err) {
		t.Errorf("snapshot %s survived the run (%v)", cmdr.got[0].Dir, err)
	}
	checkReceiptFile(t, "c123456789abcdef")
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
