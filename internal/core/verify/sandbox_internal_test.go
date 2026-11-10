package verifycore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
	_, ref := sandboxArgv(string(filepath.Separator), []string{"HOME=" + t.TempDir()})
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
	argv, ref := sandboxArgv(root, []string{"HOME=" + home})
	if ref != nil {
		t.Fatalf("sandboxArgv: %v", ref)
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
