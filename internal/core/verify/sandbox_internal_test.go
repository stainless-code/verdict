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
		mech, ref := sandboxMechanism(goos, t.TempDir(), []string{"HOME=" + t.TempDir()}, false)
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
	root := t.TempDir()
	mech, ref := sandboxMechanism("linux", root, []string{"HOME=" + t.TempDir()}, false)
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
	mech, ref := sandboxMechanism("linux", t.TempDir(), []string{"HOME=" + t.TempDir()}, true)
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
	mech, ref := sandboxMechanism(goos, t.TempDir(), []string{"HOME=" + t.TempDir()}, true)
	if mech != nil || ref == nil || ref.Code != codeSandboxUnavailable {
		t.Fatalf("%s: mech=%v ref=%v, want sandbox-unavailable", goos, mech, ref)
	}
	for _, want := range []string{goos, "Linux", "unshare"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("%s: message = %q, want %q named", goos, ref.Message, want)
		}
	}
}

// TestSandboxMechanismBuildsTheSeatbeltProfile pins the macOS shape: the
// network denial survives, the credential table and shared host temp roots
// are denied, and the private TMPDIR plus the reviewed root are allowed.
func TestSandboxMechanismBuildsTheSeatbeltProfile(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	mech, ref := sandboxMechanism("darwin", root, []string{"HOME=" + home}, false)
	if ref != nil {
		t.Fatalf("sandboxMechanism: %v", ref)
	}
	checkSeatbeltMechanism(t, mech, root, home)
}

// checkSeatbeltMechanism asserts the tool, the profile rules, and the private
// TMPDIR lifecycle of one prepared macOS mechanism.
func checkSeatbeltMechanism(t *testing.T, mech *sandbox, root, home string) {
	t.Helper()
	defer mech.close()
	requireSandboxExecPrefix(t, mech)
	checkSeatbeltProfileRules(t, mech.prefix[2], root, home)
	private := checkPrivateTempEnv(t, mech)
	if !strings.Contains(mech.prefix[2], `(allow file-read* file-write* (subpath "`+private+`"))`) {
		t.Errorf("profile does not allow the private TMPDIR %q:\n%s", private, mech.prefix[2])
	}
	if info, err := os.Stat(private); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("private TMPDIR %q = %v, %v; want an existing 0700 directory", private, info, err)
	}
	mech.close()
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Errorf("private TMPDIR %q survived close (%v)", private, err)
	}
}

// requireSandboxExecPrefix pins the shipped executable and profile argv.
func requireSandboxExecPrefix(t *testing.T, mech *sandbox) {
	t.Helper()
	if mech.tool != "sandbox-exec" || mech.mechanism != sandboxMechanismSandboxExec {
		t.Fatalf("mechanism = %+v, want sandbox-exec", mech)
	}
	if len(mech.prefix) != 3 || mech.prefix[0] != sandboxExecPath || mech.prefix[1] != "-p" {
		t.Fatalf("prefix = %v, want %s -p <profile>", mech.prefix, sandboxExecPath)
	}
}

// checkSeatbeltProfileRules pins the literal rules the profile must carry.
func checkSeatbeltProfileRules(t *testing.T, profile, root, home string) {
	t.Helper()
	for _, want := range []string{
		"(version 1)(allow default)(deny network*)",
		`(deny file-read* file-write* (subpath "` + home + `/.ssh"))`,
		`(deny file-read* file-write* (subpath "` + home + `/.aws"))`,
		`(deny file-read* file-write* (subpath "` + home + `/.netrc"))`,
		`(deny file-read* file-write* (subpath "/private/tmp"))`,
		`(deny file-read* file-write* (subpath "/tmp"))`,
		`(allow file-read* file-write* (subpath "` + root + `"))`,
	} {
		if !strings.Contains(profile, want) {
			t.Errorf("profile misses %q:\n%s", want, profile)
		}
	}
}

// checkPrivateTempEnv proves TMPDIR/TMP/TEMP point at one fresh per-run
// directory and returns it.
func checkPrivateTempEnv(t *testing.T, mech *sandbox) string {
	t.Helper()
	if len(mech.env) != 3 {
		t.Fatalf("env = %v, want TMPDIR/TMP/TEMP pointing at one private dir", mech.env)
	}
	private := strings.TrimPrefix(mech.env[0], "TMPDIR=")
	wantEnv := []string{"TMPDIR=" + private, "TMP=" + private, "TEMP=" + private}
	if !slices.Equal(mech.env, wantEnv) {
		t.Fatalf("env = %v, want %v", mech.env, wantEnv)
	}
	if private == "" || private == "/tmp" || private == os.TempDir() {
		t.Fatalf("private TMPDIR = %q, want a per-run directory", private)
	}
	return private
}

// TestSandboxExecProfileOrdersCredentialDenialsLast proves a reviewed root
// under HOME cannot re-expose a credential path: the deny rules come after
// every allow, so the last matching rule wins.
func TestSandboxExecProfileOrdersCredentialDenialsLast(t *testing.T) {
	home := t.TempDir()
	profile, ref := sandboxExecProfile(home, filepath.Join(home, "repo"), "/private/var/tmp/private", "/private/var/tmp")
	if ref != nil {
		t.Fatalf("sandboxExecProfile: %v", ref)
	}
	allow := strings.Index(profile, `(allow file-read* file-write* (subpath "`+filepath.Join(home, "repo")+`"))`)
	deny := strings.Index(profile, `(deny file-read* file-write* (subpath "`+home+`/.ssh"))`)
	if allow < 0 || deny < 0 {
		t.Fatalf("profile misses root allow (%d) or credential deny (%d):\n%s", allow, deny, profile)
	}
	if deny < allow {
		t.Errorf("credential deny at %d precedes root allow at %d; the allow would win:\n%s", deny, allow, profile)
	}
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
	if _, ref := sandboxExecMechanism(t.TempDir(), []string{"HOME=" + home + "\n"}, ""); ref == nil ||
		ref.Code != codeSandboxUnavailable || !strings.Contains(ref.Message, "control character") {
		t.Fatalf("newline HOME ref = %v, want a control-character refusal", ref)
	}
	if _, ref := sandboxExecMechanism(string(filepath.Separator), []string{"HOME=" + home}, ""); ref == nil ||
		ref.Code != codeSandboxUnavailable || !strings.Contains(ref.Message, "filesystem root") {
		t.Fatalf("filesystem root ref = %v, want a filesystem-root refusal", ref)
	}
	if _, ref := sandboxExecMechanism(t.TempDir(), []string{"PATH=/bin"}, ""); ref == nil ||
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
