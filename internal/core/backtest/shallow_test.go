package backtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

// shallowWarningLiteral is the published warning a depth-limited clone gets.
const shallowWarningLiteral = "shallow clone: an introducer at the clone boundary reads as no-pr; run 'git fetch --unshallow' for the full history"

// shallowClone clones src at depth 1, returning the clone path.
func shallowClone(t *testing.T, src string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "shallow")
	url := "file:///" + strings.TrimPrefix(filepath.ToSlash(src), "/")
	cmd := exec.CommandContext(t.Context(), "git", "clone", "-q", "--depth", "1", url, dest)
	cmd.Env = testenv.Base(map[string]string{"GIT_CONFIG_SYSTEM": os.DevNull})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone --depth 1: %v\n%s", err, out)
	}
	return dest
}

// TestMineShallowCloneWarns proves a boundary-limited clone warns on the
// envelope and through the progress observer, because its boundary introducers
// read as no-pr.
func TestMineShallowCloneWarns(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	rec := &recorder{}
	resp, ref := Run(t.Context(), &Request{
		Runtime:  &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Progress: rec, Env: userEnv(t, nil)},
		RepoRoot: shallowClone(t, b.r.dir), Mode: modeMine, Since: "90d", Until: "HEAD",
	})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	if !slices.Equal(resp.Warnings, []string{shallowWarningLiteral}) {
		t.Errorf("warnings = %q; want [%q]", resp.Warnings, shallowWarningLiteral)
	}
	if !slices.Contains(rec.warnings, shallowWarningLiteral) {
		t.Errorf("streamed warnings = %q; want the shallow warning", rec.warnings)
	}
}
