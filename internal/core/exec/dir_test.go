//go:build !windows

// Platform note: these proofs use the product's documented POSIX execution
// environment (bash, pwd/cat, git). Cross-building for Windows is checked
// separately; the behavior itself is not asserted on a Windows runtime.

package exec_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// TestRunnerHonorsCommandDir proves the child's working directory is the
// command's Dir: a deep-validation command must run in the canonical
// reviewed root, not the caller's subdirectory.
func TestRunnerHonorsCommandDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := coreexec.Runner{}.Run(context.Background(), &core.Command{
		Argv: []string{"pwd"}, Dir: root, OutputCap: 4096,
	})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("run = %v %v", res, err)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("child cwd = %q, want the command Dir %q (not %q)", got, want, sub)
	}
}
