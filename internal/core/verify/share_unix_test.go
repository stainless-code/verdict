//go:build unix

package verifycore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
	"github.com/stainless-code/verdict/internal/testkit"
)

// shareBase is the PR base the fixture's controls snapshot is pinned to.
const shareBase = "abcdef1234567890abcdef1234567890abcdef12"

// shareBundle is the fixture's patch bundle, relative to the checkout.
const shareBundle = ".verdict/state/5201"

// stubScript is what the fixture installs at node_modules/.bin/stub.
const stubScript = "#!/bin/sh\necho stub-ran\n"

// shareCheckout makes a fresh checkout the working directory: one committed
// file, a committed .gitignore that ignores node_modules, an installed
// node_modules/.bin/stub, and a patch bundle running vcmd whose base
// configuration is cfg.
func shareCheckout(t *testing.T, cfg, vcmd string) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	gitCommittedRepo(t, root)
	writeShareFile(t, filepath.Join(root, ".gitignore"), "node_modules\n", 0o644)
	runGit(t, root, "add", ".gitignore")
	runGit(t, root, "commit", "-q", "-m", "ignore")
	writeShareFile(t, filepath.Join(root, "node_modules", ".bin", "stub"), stubScript, 0o755)
	writeShareFile(t, filepath.Join(root, shareBundle, "patch.json"),
		`{"verifyCmd":`+strconv.Quote(vcmd)+`}`, 0o644)
	writeBaseConfig(t, filepath.Join(root, shareBundle), cfg)
	return root
}

func writeShareFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// writeBaseConfig pins body as the bundle's base-snapshotted
// .verdict/config.json, as fetch would.
func writeBaseConfig(t *testing.T, bundleDir, body string) {
	t.Helper()
	testkit.WriteFile(t, filepath.Join(bundleDir, "controls", "config.json"), body)
	sum := sha256.Sum256([]byte(body))
	testkit.WriteFile(t, filepath.Join(bundleDir, controls.FileName), fmt.Sprintf(
		`{"schema_version":"1","policy":"base","base_sha":%q,"sha":%q,`+
			`"files":[{"path":%q,"status":"used","bytes":%d,"sha256":%q}]}`,
		shareBase, shareBase, config.ConfigPath, len(body), hex.EncodeToString(sum[:])))
	testkit.WriteFile(t, filepath.Join(bundleDir, "PR.json"), `{"baseRefOid":"`+shareBase+`"}`)
}

// shareConfig is a base configuration sharing the listed entries.
func shareConfig(entries ...string) string {
	quoted := make([]string, 0, len(entries))
	for _, e := range entries {
		quoted = append(quoted, strconv.Quote(e))
	}
	return `{"schema_version":"1","verify":{"share":[` + strings.Join(quoted, ",") + `]}}`
}

// shareRequest runs the real watchdog and git, with a pinned HOME and
// account home so the sandbox's credential table stays inside the test.
func shareRequest(t *testing.T, sandboxed bool) *Request {
	t.Helper()
	req := &Request{
		Runtime:    &core.Runtime{Now: testkit.FixedNow, Progress: &testkit.Collector{}, Commander: coreexec.Watchdog{}},
		Repo:       "acme/app",
		Pr:         "5201",
		TimeoutRaw: "60",
		Environ:    testenv.Base(map[string]string{"HOME": t.TempDir()}),
		Sandbox:    sandboxed,
	}
	req.accountHome = t.TempDir()
	return req
}

// requireHostSandbox keeps the sandboxed leg on the shipped mechanism, never
// a stub: sandbox-exec on macOS, a working bubblewrap on Linux.
func requireHostSandbox(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		if _, err := os.Stat(sandboxExecPath); err != nil {
			t.Skipf("%s is not present: %v", sandboxExecPath, err)
		}
	case "linux":
		cmd := exec.CommandContext(t.Context(), bwrapTool, "--ro-bind", "/", "/", "--unshare-net", "true")
		cmd.Env = testenv.Base(nil)
		if err := cmd.Run(); err != nil {
			t.Skipf("bubblewrap cannot run here: %v", err)
		}
	default:
		t.Skipf("%s has no sandbox mechanism", runtime.GOOS)
	}
}

// shareProbe runs the shared stub, then tries to write into the shared
// directory and reports which way it went.
const shareProbe = `node_modules/.bin/stub && if (printf x > node_modules/planted) 2>/dev/null; ` +
	`then echo WROTE; else echo DENIED; fi`

// TestSandboxedShareRunsTheStubButDeniesWrites is the case verify.share
// exists for: under --sandbox the command executes a dependency the checkout
// installed, cannot write into it, and the receipt names what it shared.
func TestSandboxedShareRunsTheStubButDeniesWrites(t *testing.T) {
	requireHostSandbox(t)
	root := shareCheckout(t, shareConfig("node_modules"), shareProbe)
	resp, ref := Run(t.Context(), shareRequest(t, true))
	if ref != nil {
		t.Fatalf("Run refusal: %s: %s", ref.Code, ref.Message)
	}
	if resp.Exit != 0 || !strings.Contains(resp.Tail, "stub-ran") || !strings.Contains(resp.Tail, "DENIED") {
		t.Fatalf("exit=%d tail=%q, want the stub to run and the write to be denied", resp.Exit, resp.Tail)
	}
	if _, err := os.Lstat(filepath.Join(root, "node_modules", "planted")); !os.IsNotExist(err) {
		t.Fatalf("the sandboxed command wrote into the checkout's node_modules (err=%v)", err)
	}
	requireSharedRecorded(t, resp, []string{"node_modules"})
	requireStubIntact(t, root)
}

// TestUnsandboxedShareLinksTheDirectory proves the unsandboxed shape: the
// stub runs through the link, the receipt names the share, and removing the
// snapshot never follows the link into the checkout.
func TestUnsandboxedShareLinksTheDirectory(t *testing.T) {
	root := shareCheckout(t, shareConfig("node_modules"), "node_modules/.bin/stub")
	resp, ref := Run(t.Context(), shareRequest(t, false))
	if ref != nil {
		t.Fatalf("Run refusal: %s: %s", ref.Code, ref.Message)
	}
	if resp.Exit != 0 || !strings.Contains(resp.Tail, "stub-ran") {
		t.Fatalf("exit=%d tail=%q, want the shared stub to run", resp.Exit, resp.Tail)
	}
	requireSharedRecorded(t, resp, []string{"node_modules"})
	requireStubIntact(t, root)
	for _, w := range resp.Warnings {
		if strings.Contains(w, "ignored entr") {
			t.Errorf("a shared directory is still reported as left out: %q", w)
		}
	}
}

// TestShareOfAMissingDirectoryWarnsAndRunsClean keeps a fresh clone usable:
// an entry the checkout has not installed is skipped with a warning, and the
// receipt records nothing shared.
func TestShareOfAMissingDirectoryWarnsAndRunsClean(t *testing.T) {
	shareCheckout(t, shareConfig(".venv"), "test ! -e .venv")
	resp, ref := Run(t.Context(), shareRequest(t, false))
	if ref != nil {
		t.Fatalf("Run refusal: %s: %s", ref.Code, ref.Message)
	}
	if resp.Exit != 0 {
		t.Fatalf("exit=%d tail=%q, want a clean snapshot without .venv", resp.Exit, resp.Tail)
	}
	if !slices.ContainsFunc(resp.Warnings, func(w string) bool { return strings.Contains(w, "verify.share: .venv is not in the checkout") }) {
		t.Errorf("warnings = %q, want the skipped entry named", resp.Warnings)
	}
	requireSharedRecorded(t, resp, nil)
}

// TestShareRefusesEntriesThatAreNotIgnoredCheckoutDirectories proves every
// refusal leaves nothing run and no receipt: an entry that holds tracked
// content, one git does not ignore, one that resolves outside the checkout
// or onto tracked content through a link, and the spellings the base
// configuration itself rejects.
func TestShareRefusesEntriesThatAreNotIgnoredCheckoutDirectories(t *testing.T) {
	for _, tc := range []shareRefusalCase{
		{name: "tracked", entry: "vendor", want: "holds tracked content", plant: plantTrackedVendor},
		{name: "not ignored", entry: "build", want: "is not gitignored", plant: plantUnignoredBuild},
		{name: "link outside", entry: "node_modules", want: "resolves outside the checkout", plant: plantLinkOutside},
		{name: "link onto tracked", entry: "node_modules", want: "holds tracked content", plant: plantLinkOntoTracked},
		{name: "dotdot", entry: "../outside", want: "verify.share"},
		{name: "absolute", entry: "/etc", want: "verify.share"},
	} {
		t.Run(tc.name, func(t *testing.T) { checkShareRefusal(t, &tc) })
	}
}

type shareRefusalCase struct {
	plant func(t *testing.T, root string)
	name  string
	entry string
	want  string
}

func checkShareRefusal(t *testing.T, tc *shareRefusalCase) {
	t.Helper()
	root := shareCheckout(t, shareConfig(tc.entry), "touch ran")
	if tc.plant != nil {
		tc.plant(t, root)
	}
	_, ref := Run(t.Context(), shareRequest(t, false))
	if ref == nil || ref.Code != config.CodeBadConfig || !strings.Contains(ref.Message, tc.want) {
		t.Fatalf("Run refusal = %v, want bad-config naming %q", ref, tc.want)
	}
	if _, err := os.Lstat(filepath.Join(root, shareBundle, ReceiptFile)); !os.IsNotExist(err) {
		t.Fatalf("a refused share left a receipt (err=%v)", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, shareBundle, "patch.json"))
	if err != nil || strings.Contains(string(raw), "verifyExit") {
		t.Fatalf("a refused share recorded an outcome: %s (err=%v)", raw, err)
	}
}

func plantTrackedVendor(t *testing.T, root string) {
	t.Helper()
	writeShareFile(t, filepath.Join(root, "vendor", "lib.go"), "package lib\n", 0o644)
	runGit(t, root, "add", "vendor")
	runGit(t, root, "commit", "-q", "-m", "vendor")
}

func plantUnignoredBuild(t *testing.T, root string) {
	t.Helper()
	writeShareFile(t, filepath.Join(root, "build", "out"), "x", 0o644)
}

func plantLinkOutside(t *testing.T, root string) {
	t.Helper()
	outside := t.TempDir()
	mustShareFS(t, os.RemoveAll(filepath.Join(root, "node_modules")))
	mustShareFS(t, os.Symlink(outside, filepath.Join(root, "node_modules")))
}

func plantLinkOntoTracked(t *testing.T, root string) {
	t.Helper()
	writeShareFile(t, filepath.Join(root, "src", "a.js"), "x\n", 0o644)
	runGit(t, root, "add", "src")
	runGit(t, root, "commit", "-q", "-m", "src")
	mustShareFS(t, os.RemoveAll(filepath.Join(root, "node_modules")))
	mustShareFS(t, os.Symlink("src", filepath.Join(root, "node_modules")))
}

// TestSweepNeverFollowsASharedLink strands a snapshot holding a share's link
// and sweeps it: the snapshot goes, the checkout's directory stays whole.
func TestSweepNeverFollowsASharedLink(t *testing.T) {
	root := shareCheckout(t, shareConfig("node_modules"), "true")
	tmp := t.TempDir()
	stranded := filepath.Join(tmp, snapshotPrefix+"1-stranded")
	mustShareFS(t, os.MkdirAll(stranded, 0o755))
	mustShareFS(t, os.Symlink(filepath.Join(root, "node_modules"), filepath.Join(stranded, "node_modules")))
	sweepSnapshots(t.Context(), shareGit(root), tmp, time.Now())
	if _, err := os.Lstat(stranded); !os.IsNotExist(err) {
		t.Fatalf("the stranded snapshot survived the sweep (err=%v)", err)
	}
	requireStubIntact(t, root)
}

func requireSharedRecorded(t *testing.T, resp *Response, want []string) {
	t.Helper()
	if !slices.Equal(resp.Data.Shared, want) {
		t.Errorf("data.shared = %q, want %q", resp.Data.Shared, want)
	}
	receipt, found, problem := ReadVerifyReceipt(resp.PatchDir)
	if !found || problem != "" {
		t.Fatalf("receipt found=%v problem=%q", found, problem)
	}
	if !slices.Equal(receipt.Shared, want) {
		t.Errorf("receipt shared = %q, want %q", receipt.Shared, want)
	}
}

// requireStubIntact proves cleanup left the checkout's shared directory as
// the fixture installed it.
func requireStubIntact(t *testing.T, root string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "node_modules", ".bin", "stub"))
	if err != nil || string(raw) != stubScript {
		t.Fatalf("the checkout's node_modules/.bin/stub = %q (err=%v), want it untouched", raw, err)
	}
}

func shareGit(root string) contenttree.Git {
	return contenttree.Git{Cmdr: coreexec.Watchdog{}, Root: root, Env: testenv.Base(nil)}
}

func mustShareFS(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestBwrapBindsSharesReadOnlyOverTheRoot pins the Linux shape: each share is
// a read-only bind of the checkout's directory at its place in the reviewed
// root, after the root's writable bind (a bind before it would be covered)
// and before the credential masks, which must still win.
func TestBwrapBindsSharesReadOnlyOverTheRoot(t *testing.T) {
	root, home, target := gitRoot(t), t.TempDir(), t.TempDir()
	mustShareFS(t, os.WriteFile(filepath.Join(home, ".netrc"), []byte("machine example"), 0o600))
	argv, ref := bwrapArgv(root, sandboxInputs{
		childEnv: []string{"HOME=" + home},
		shared:   &[]sharedDir{{rel: "web/node_modules", real: target}},
	})
	if ref != nil {
		t.Fatalf("bwrapArgv: %v", ref)
	}
	joined := strings.Join(argv, "\x00")
	rootBind := strings.Join([]string{"--bind", root, root}, "\x00")
	shareBind := strings.Join([]string{"--ro-bind", target, root + "/web/node_modules"}, "\x00")
	credMask := strings.Join([]string{"--ro-bind", "/dev/null", filepath.Join(home, ".netrc")}, "\x00")
	r, s, c := strings.Index(joined, rootBind), strings.Index(joined, shareBind), strings.Index(joined, credMask)
	if r < 0 || s < 0 || c < 0 || r >= s || s >= c {
		t.Fatalf("argv = %q, want the root bind, then the share's read-only bind, then the credential mask", argv)
	}
}
