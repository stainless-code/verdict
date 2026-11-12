package pack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestMain scrubs the git environment the launcher exported.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

// TestCheckLockReadsBaseNotHead proves the lock comes from the trusted base:
// a head edit stays unread, and the base lock still passes the check.
func TestCheckLockReadsBaseNotHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	repo := t.TempDir()
	baseLock := lockBody(lockHash, lockSigDir)
	writeBaseLockFixture(t, repo, baseLock)
	base := gitIn(t, repo, "rev-parse", "HEAD")

	headLock := lockBody(strings.Repeat("0", 64), lockSigDir)
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(LockFile)), []byte(headLock), 0o644); err != nil {
		t.Fatal(err)
	}
	if headLock == baseLock {
		t.Fatal("fixture head lock equals the base lock")
	}

	snap, ref := controls.Resolve(t.Context(), coreexec.Runner{
		OutputCap: coreexec.FetchOutputCap, MissingTool: true,
	}, &controls.Request{BundleDir: t.TempDir(), BaseSHA: base, Root: repo})
	if ref != nil {
		t.Fatalf("controls.Resolve: %s: %s", ref.Code, ref.Message)
	}
	if got, ok := snap.ReadFile(LockFile); !ok || string(got) != baseLock {
		t.Fatalf("%s = %q (ok=%v), want the base bytes", LockFile, got, ok)
	}
	if ref := CheckLock(snap); ref != nil {
		t.Fatalf("CheckLock = %s: %s, want the base lock accepted", ref.Code, ref.Message)
	}
}

// writeBaseLockFixture commits one locked pack and the given lock body.
func writeBaseLockFixture(t *testing.T, repo, lock string) {
	t.Helper()
	gitIn(t, repo, "init", "-q")
	files := map[string]string{
		".verdict/packs/lenses/pack.json":      lockManifest,
		".verdict/packs/lenses/lenses/perf.md": "perf body\n",
		LockFile:                               lock,
	}
	for rel, body := range files {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "add", "-f", "-A")
	gitIn(t, repo, "commit", "-qm", "base")
}

// gitIn runs one fixture git command in dir and returns its trimmed stdout.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = testenv.Base(nil)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
