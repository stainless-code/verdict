package verifycore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestSandboxRefusesARootInsideACredentialPath keeps a reviewed root under a
// credential store from being either exposed or hidden: both platforms
// refuse before any profile or argv exists.
func TestSandboxRefusesARootInsideACredentialPath(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".config", "gh", "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, goos := range []string{"linux", "darwin"} {
		mech, ref := sandboxMechanism(goos, root, homeInputs(home), false)
		if mech != nil || ref == nil || ref.Code != codeSandboxUnavailable {
			t.Fatalf("%s: mech=%v ref=%v, want sandbox-unavailable", goos, mech, ref)
		}
		want := "sandbox unavailable: the reviewed root " + root + " is inside the credential path " +
			filepath.Join(home, ".config", "gh") + "; refusing to expose or hide it"
		if ref.Message != want {
			t.Errorf("%s: message = %q, want %q", goos, ref.Message, want)
		}
	}
}

// TestSandboxArgvMasksAccountHomeAndRelocations proves the Linux table reaches
// past the child HOME: an existing store under the account home and one moved
// by a host-only relocation variable are both mounted away.
func TestSandboxArgvMasksAccountHomeAndRelocations(t *testing.T) {
	root, child, account, moved := gitRoot(t), t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(account, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(account, ".vault-token"), []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	argv, ref := bwrapArgv(root, sandboxInputs{
		childEnv:    []string{"HOME=" + child},
		hostEnv:     []string{"GH_CONFIG_DIR=" + moved},
		accountHome: account,
	})
	if ref != nil {
		t.Fatalf("bwrapArgv: %v", ref)
	}
	want := []string{
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--tmpfs", filepath.Join(account, ".ssh"),
		"--ro-bind", "/dev/null", filepath.Join(account, ".vault-token"),
		"--tmpfs", moved,
		"--unshare-net",
	}
	start := slices.Index(argv, "--bind")
	if start < 0 || !slices.Equal(argv[start:start+len(want)], want) {
		t.Errorf("argv = %v, want the mounts %v after the repo bind", argv, want)
	}
}

// TestRecordOutcomeNetworkOnlyIsNotSandboxed pins what `sandboxed` means in
// the stored record: the network-only mechanism is named, but a stale
// `sandboxed: true` from an earlier full run does not survive it, and a later
// full run overwrites the mechanism rather than keeping the older one.
func TestRecordOutcomeNetworkOnlyIsNotSandboxed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, patchFile), []byte(`{"verifyCmd":"true","sandboxed":true,"sandboxMechanism":"bwrap"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, ref := openBundle(dir)
	if ref != nil {
		t.Fatalf("openBundle: %v", ref)
	}
	defer bundle.close()
	if ref := recordOutcome(bundle, "2026-01-01T00:00:00Z", 0, false, "", unshareNetMechanism()); ref != nil {
		t.Fatalf("recordOutcome: %v", ref)
	}
	const wantNetworkOnly = `{
  "verifyCmd": "true",
  "sandboxMechanism": "unshare-net",
  "verifyExit": 0,
  "verifiedAt": "2026-01-01T00:00:00Z",
  "timedOut": false,
  "verifiedTail": ""
}
`
	if got := readPatchRecord(t, dir); got != wantNetworkOnly {
		t.Errorf("network-only record =\n%s\nwant\n%s", got, wantNetworkOnly)
	}
	full := &sandbox{tool: sandboxExecTool, mechanism: sandboxMechanismSandboxExec}
	if ref := recordOutcome(bundle, "2026-01-01T00:00:01Z", 0, false, "", full); ref != nil {
		t.Fatalf("recordOutcome: %v", ref)
	}
	if got := readPatchRecord(t, dir); !strings.Contains(got, `"sandboxMechanism": "sandbox-exec"`) || !strings.Contains(got, `"sandboxed": true`) {
		t.Errorf("full record = %s, want sandboxed true and the sandbox-exec mechanism", got)
	}
}

func readPatchRecord(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, patchFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestReleaseSandboxWarnsOnCleanupFailure proves a stranded private tree is
// reported on the warnings channel instead of being dropped.
func TestReleaseSandboxWarnsOnCleanupFailure(t *testing.T) {
	st := &state{req: &Request{}}
	st.releaseSandbox(&sandbox{cleanup: func() error { return errors.New("remove /p: permission denied") }})
	want := []string{"sandbox cleanup: the private directory could not be fully removed " +
		"(remove /p: permission denied); remove it by hand"}
	if !slices.Equal(st.warnings, want) {
		t.Errorf("warnings = %q, want %q", st.warnings, want)
	}
	st.releaseSandbox(nil)
	if len(st.warnings) != 1 {
		t.Errorf("a nil sandbox warned: %q", st.warnings)
	}
}

// TestProbeSpawnErrorNamesTheMechanismTool keeps a runner that cannot spawn
// from naming the wrong tool: each mechanism names what it needs and the
// alternative boundary.
func TestProbeSpawnErrorNamesTheMechanismTool(t *testing.T) {
	spawn := errors.New("executable file not found in $PATH")
	for _, tc := range []struct{ tool, want string }{
		{bwrapTool, "sandbox unavailable: bwrap could not start (executable file not found in $PATH) — " +
			"--sandbox on Linux needs bubblewrap (bwrap) on PATH; install it, " +
			"or pass --sandbox-network-only to accept network isolation alone"},
		{unshareTool, "sandbox unavailable: unshare could not start (executable file not found in $PATH) — " +
			"--sandbox-network-only needs util-linux unshare on PATH; install it, " +
			"or use --sandbox with bubblewrap for the full shape"},
		{sandboxExecTool, "sandbox unavailable: sandbox-exec could not start (executable file not found in $PATH) — " +
			"--sandbox on macOS needs the built-in /usr/bin/sandbox-exec; " +
			"restore it, or run the bwrap recipe in a Linux VM or container"},
	} {
		sb := &sandbox{tool: tc.tool, prefix: []string{tc.tool}}
		ref := sb.probe(context.Background(), errCommander{err: spawn}, "", nil)
		if ref == nil || ref.Message != tc.want {
			t.Errorf("%s: ref = %v, want %q", tc.tool, ref, tc.want)
		}
	}
}

type errCommander struct{ err error }

func (c errCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{}, c.err
}
