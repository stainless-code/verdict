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
	_, ref := bwrapArgv(string(filepath.Separator), sandboxInputs{childEnv: []string{"HOME=" + t.TempDir()}})
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
	root := gitRoot(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".netrc"), []byte("machine example"), 0o600); err != nil {
		t.Fatal(err)
	}
	argv, ref := bwrapArgv(root, sandboxInputs{childEnv: []string{"HOME=" + home}})
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
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
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
		mech, ref := sandboxMechanism(goos, t.TempDir(), homeInputs(t.TempDir()), false)
		if mech != nil || ref == nil || ref.Code != codeSandboxUnavailable {
			t.Fatalf("%s: mech=%v ref=%v, want sandbox-unavailable", goos, mech, ref)
		}
		if !strings.Contains(ref.Message, goos) {
			t.Errorf("%s: message = %q, want the OS named", goos, ref.Message)
		}
	}
}

// TestSandboxMechanismRoutesLinuxToBwrap keeps --sandbox on bubblewrap: the
// network-only opt-in is a separate flag and never a fallback.
func TestSandboxMechanismRoutesLinuxToBwrap(t *testing.T) {
	root := gitRoot(t)
	mech, ref := sandboxMechanism("linux", root, homeInputs(t.TempDir()), false)
	if ref != nil {
		t.Fatalf("sandboxMechanism: %v", ref)
	}
	if mech.tool != "bwrap" || mech.mechanism != sandboxMechanismBwrap || !slices.Contains(mech.prefix, "--unshare-net") {
		t.Errorf("mechanism = %+v, want bwrap with --unshare-net", mech)
	}
}

// TestSandboxMechanismUnshareNetIsAnExplicitOptIn pins the Linux network-only
// argv: a user namespace mapped to root inside it, then a network namespace,
// and no bwrap mount flags at all.
func TestSandboxMechanismUnshareNetIsAnExplicitOptIn(t *testing.T) {
	mech, ref := sandboxMechanism("linux", t.TempDir(), homeInputs(t.TempDir()), true)
	if ref != nil {
		t.Fatalf("sandboxMechanism: %v", ref)
	}
	want := []string{"unshare", "--user", "--map-root-user", "--net", "--"}
	if mech.tool != unshareTool || mech.mechanism != sandboxMechanismUnshareNet {
		t.Errorf("mechanism = %+v, want the unshare-net mechanism", mech)
	}
	if !slices.Equal(mech.prefix, want) {
		t.Errorf("prefix = %v, want %v", mech.prefix, want)
	}
	if !mech.networkOnly {
		t.Error("networkOnly = false, want true")
	}
	if slices.Contains(mech.prefix, bwrapTool) || slices.Contains(mech.prefix, "--tmpfs") {
		t.Errorf("prefix = %v, want no mount mechanism", mech.prefix)
	}
}

// TestSandboxNetworkOnlyRefusesOtherHosts keeps the Linux opt-in from
// silently becoming a macOS or Windows mechanism.
func TestSandboxNetworkOnlyRefusesOtherHosts(t *testing.T) {
	for _, goos := range []string{"darwin", "windows", "freebsd"} {
		checkNetworkOnlyRefusal(t, goos)
	}
}

// checkNetworkOnlyRefusal asserts one non-Linux host refuses the opt-in with
// its own name and the Linux mechanism.
func checkNetworkOnlyRefusal(t *testing.T, goos string) {
	t.Helper()
	mech, ref := sandboxMechanism(goos, t.TempDir(), homeInputs(t.TempDir()), true)
	if mech != nil || ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("%s: mech=%v ref=%v, want sandbox-unavailable", goos, mech, ref)
	}
	for _, want := range []string{goos, "Linux", "unshare"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("%s: message = %q, want %q named", goos, ref.Message, want)
		}
	}
}

// homeInputs is the minimal mechanism input: a child environment carrying
// HOME alone, no host snapshot, and no account home.
func homeInputs(home string) sandboxInputs {
	return sandboxInputs{childEnv: []string{"HOME=" + home}}
}

// TestSeatbeltLiteralEscapesQuotesAndBackslashes pins the exact literal for a
// path Seatbelt would otherwise parse as syntax, and refuses the control
// characters it cannot represent.
func TestSeatbeltLiteralEscapesQuotesAndBackslashes(t *testing.T) {
	got, ref := seatbeltLiteral(`/tmp/a"b(c)\d`)
	if ref != nil {
		t.Fatalf("seatbeltLiteral: %v", ref)
	}
	const want = `"/tmp/a\"b(c)\\d"`
	if got != want {
		t.Errorf("literal = %s, want %s", got, want)
	}
	if _, ref := seatbeltLiteral("/tmp/a\nb"); ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("newline ref = %v, want sandbox-unavailable", ref)
	}
	if _, ref := seatbeltLiteral("/tmp/a\tb"); ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("tab ref = %v, want sandbox-unavailable", ref)
	}
}

// TestSandboxExecRefusesUnrepresentablePaths keeps a hostile HOME or root from
// being spliced into the profile.
func TestSandboxExecRefusesUnrepresentablePaths(t *testing.T) {
	home := t.TempDir()
	if _, ref := sandboxExecMechanism(t.TempDir(), homeInputs(home+"\n"), ""); ref == nil ||
		ref.Code != codeSandboxUnavailable || !strings.Contains(ref.Message, "control character") {
		t.Fatalf("newline HOME ref = %v, want a control-character refusal", ref)
	}
	if _, ref := sandboxExecMechanism(string(filepath.Separator), homeInputs(home), ""); ref == nil ||
		ref.Code != codeSandboxUnavailable || !strings.Contains(ref.Message, "filesystem root") {
		t.Fatalf("filesystem root ref = %v, want a filesystem-root refusal", ref)
	}
	if _, ref := sandboxExecMechanism(t.TempDir(), sandboxInputs{childEnv: []string{"PATH=/bin"}}, t.TempDir()); ref == nil ||
		ref.Code != codeSandboxUnavailable || !strings.Contains(ref.Message, "HOME") {
		t.Fatalf("missing HOME ref = %v, want the HOME cause", ref)
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

// TestProbeNamesTheUnshareRemedy points a failed network-only probe back at
// bubblewrap, because unshare cannot provide the mount shape.
func TestProbeNamesTheUnshareRemedy(t *testing.T) {
	sb := &sandbox{tool: unshareTool, prefix: []string{unshareTool}}
	cmdr := &probeCommander{res: core.Result{ExitCode: 1, Stderr: "unshare: unshare failed: Operation not permitted"}}
	ref := sb.probe(context.Background(), cmdr, "", nil)
	if ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("ref = %v, want sandbox-unavailable", ref)
	}
	if !strings.Contains(ref.Message, "unprivileged user namespaces may be restricted") ||
		!strings.Contains(ref.Message, "use --sandbox with bubblewrap for mount isolation") {
		t.Errorf("message = %q, want the namespace cause and the full-shape remedy", ref.Message)
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

// TestSandboxChildEnvOverridesPrivateTemp pins the override: the base
// snapshot's host TMPDIR/TMP/TEMP cannot survive into the macOS child.
func TestSandboxChildEnvOverridesPrivateTemp(t *testing.T) {
	sb := &sandbox{env: []string{"TMPDIR=/private/run/tmp", "TMP=/private/run/tmp", "TEMP=/private/run/tmp"}}
	got := sb.childEnv([]string{"PATH=/bin", "TMPDIR=/host/tmp", "TMP=/host/tmp", "TEMP=/host/tmp", "HOME=/home/eval"})
	want := []string{"PATH=/bin", "HOME=/home/eval", "TMPDIR=/private/run/tmp", "TMP=/private/run/tmp", "TEMP=/private/run/tmp"}
	if !slices.Equal(got, want) {
		t.Errorf("childEnv = %v, want %v", got, want)
	}
}
