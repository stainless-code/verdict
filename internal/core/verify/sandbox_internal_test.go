package verifycore

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// wantVarRunMount mirrors the host fact the wrapper reads, so a supported
// platform that symlinks /var/run keeps the literal expectation.
func wantVarRunMount() []string {
	if info, err := os.Lstat("/var/run"); err == nil && info.IsDir() {
		return []string{"--tmpfs", "/var/run"}
	}
	return nil
}

// TestSandboxArgvRefusesTheFilesystemRoot keeps the writable bind from ever
// covering the whole host.
func TestSandboxArgvRefusesTheFilesystemRoot(t *testing.T) {
	_, ref := bwrapArgv(string(filepath.Separator), []string{"HOME=" + t.TempDir()})
	if ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("ref = %v, want sandbox-unavailable", ref)
	}
	if !strings.Contains(ref.Message, "filesystem root") {
		t.Errorf("message = %q, want the root cause", ref.Message)
	}
}

// TestSandboxArgvMasksCredentialFiles pins a file credential behind a
// read-only /dev/null bind and leaves every missing entry out.
func TestSandboxArgvMasksCredentialFiles(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".netrc"), []byte("machine example"), 0o600); err != nil {
		t.Fatal(err)
	}
	argv, ref := bwrapArgv(root, []string{"HOME=" + home})
	if ref != nil {
		t.Fatalf("bwrapArgv: %v", ref)
	}
	want := []string{
		"bwrap",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--unshare-pid",
		"--tmpfs", "/tmp",
		"--tmpfs", "/run",
	}
	want = append(want, wantVarRunMount()...)
	want = append(want,
		"--bind", root, root,
		"--ro-bind", "/dev/null", filepath.Join(home, ".netrc"),
		"--unshare-net",
		"--new-session",
		"--die-with-parent",
		"--",
	)
	if !slices.Equal(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
}

func TestSandboxMechanismRefusesHostsWithoutOne(t *testing.T) {
	for _, goos := range []string{"windows", "freebsd"} {
		mech, ref := sandboxMechanism(goos, t.TempDir(), []string{"HOME=" + t.TempDir()})
		if mech != nil || ref == nil || ref.Code != codeSandboxUnavailable {
			t.Fatalf("%s: mech=%v ref=%v, want sandbox-unavailable", goos, mech, ref)
		}
		if !strings.Contains(ref.Message, goos) {
			t.Errorf("%s: message = %q, want the OS named", goos, ref.Message)
		}
	}
}

func TestSandboxMechanismUsesSandboxExecOnDarwin(t *testing.T) {
	mech, ref := sandboxMechanism("darwin", t.TempDir(), []string{"PATH=/bin"})
	if ref != nil {
		t.Fatalf("sandboxMechanism: %v", ref)
	}
	want := []string{"/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)(deny network*)"}
	if mech.tool != "sandbox-exec" || !slices.Equal(mech.prefix, want) {
		t.Errorf("mechanism = %+v, want tool sandbox-exec and prefix %v", mech, want)
	}
}

func TestSandboxMechanismRoutesLinuxToBwrap(t *testing.T) {
	mech, ref := sandboxMechanism("linux", t.TempDir(), []string{"HOME=" + t.TempDir()})
	if ref != nil {
		t.Fatalf("sandboxMechanism: %v", ref)
	}
	if mech.tool != "bwrap" || !slices.Contains(mech.prefix, "--unshare-net") {
		t.Errorf("mechanism = %+v, want bwrap with --unshare-net", mech)
	}
}

// TestSandboxVarRunMountSkipsSymlinks proves the second runtime tmpfs is only
// added for a real directory, never for the symlink most distros ship.
func TestSandboxVarRunMountSkipsSymlinks(t *testing.T) {
	cases := []struct {
		name string
		want []string
		mode os.FileMode
		ok   bool
	}{
		{"directory", []string{"--tmpfs", "/var/run"}, os.ModeDir | 0o755, true},
		{"symlink", nil, os.ModeSymlink | 0o777, true},
		{"missing", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sandboxVarRunMount(func(string) (os.FileMode, bool) { return tc.mode, tc.ok })
			if !slices.Equal(got, tc.want) {
				t.Errorf("mount = %v, want %v", got, tc.want)
			}
		})
	}
}

type probeCommander struct {
	res   core.Result
	calls int
}

func (c *probeCommander) Run(_ context.Context, _ *core.Command) (core.Result, error) {
	c.calls++
	return c.res, nil
}

func TestProbeNamesTheLinuxNamespaceRemedy(t *testing.T) {
	sb := &sandbox{tool: bwrapTool, prefix: []string{bwrapTool}}
	cmdr := &probeCommander{res: core.Result{
		ExitCode: 1,
		Stdout:   "bwrap: No permissions to creating new namespace, likely because the kernel does not allow non-privileged user namespaces.",
	}}
	ref := sb.probe(context.Background(), cmdr, "", nil)
	if ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("ref = %v, want sandbox-unavailable", ref)
	}
	want := "sandbox unavailable: bwrap could not create the sandbox (exit 1: bwrap: No permissions to creating new namespace, " +
		"likely because the kernel does not allow non-privileged user namespaces.) — unprivileged user namespaces may be restricted"
	if ref.Message != want {
		t.Errorf("message = %q, want %q", ref.Message, want)
	}
	if cmdr.calls != 1 {
		t.Errorf("spawns = %d, want the lone probe", cmdr.calls)
	}
}

func TestProbeKeepsTheNamespaceRemedyOffMacOS(t *testing.T) {
	sb := &sandbox{tool: sandboxExecTool, prefix: []string{sandboxExecPath}}
	cmdr := &probeCommander{res: core.Result{ExitCode: 1, Stderr: "sandbox-exec: invalid profile"}}
	ref := sb.probe(context.Background(), cmdr, "", nil)
	if ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("ref = %v, want sandbox-unavailable", ref)
	}
	if strings.Contains(ref.Message, "unprivileged user namespaces") {
		t.Errorf("message = %q, want no Linux namespace remedy on macOS", ref.Message)
	}
	if !strings.Contains(ref.Message, "invalid profile") {
		t.Errorf("message = %q, want sandbox-exec's own diagnostic", ref.Message)
	}
}
