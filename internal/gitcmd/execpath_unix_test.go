//go:build unix

package gitcmd_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestEnvIgnoresAnInheritedExecPath pins that a GIT_EXEC_PATH a parent git
// exported cannot swap git's own helper programs: with it naming a directory
// whose git-upload-pack writes a marker, a hook-free fetch from a local
// remote runs git's real helper, fetches, and leaves no marker. The fake is
// a shell script, so the test is unix-only.
func TestEnvIgnoresAnInheritedExecPath(t *testing.T) {
	t.Parallel()
	src, dst, evil := t.TempDir(), t.TempDir(), t.TempDir()
	marker := filepath.Join(t.TempDir(), "hit")
	run(t, nil, "init", "-q", src)
	run(t, nil, "-C", src, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "c")
	run(t, nil, "init", "-q", dst)
	run(t, nil, "-C", dst, "remote", "add", "origin", src)
	helper := filepath.Join(evil, "git-upload-pack")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := testenv.Base(map[string]string{"GIT_EXEC_PATH": evil})
	argv := gitcmd.Argv(dst, "fetch", "-q", "origin")
	run(t, gitcmd.Env(base), argv[1:]...)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("the inherited GIT_EXEC_PATH ran its git-upload-pack (%v)", err)
	}
}

// run runs git with args and env (nil is the hermetic test environment),
// failing the test on an error.
func run(t *testing.T, env []string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	if env == nil {
		env = testenv.Base(nil)
	}
	cmd.Env = env
	cmd.WaitDelay = 10 * time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %q: %v\n%s", args, err, out)
	}
}
