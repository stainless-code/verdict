package backtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

const partialCloneLiteral = "partial clone: blame would fetch each missing blob in its own round-trip; " +
	"re-clone without --filter, or unset the remote's promisor and partialclonefilter settings " +
	"and run 'git fetch --refetch'"

func bloblessClone(t *testing.T, src, remote string) string {
	t.Helper()
	if out, err := exec.CommandContext(t.Context(), "git", "-C", src,
		"config", "uploadpack.allowFilter", "true").CombinedOutput(); err != nil {
		t.Fatalf("git config uploadpack.allowFilter: %v\n%s", err, out)
	}
	dest := filepath.Join(t.TempDir(), "blobless")
	url := "file:///" + strings.TrimPrefix(filepath.ToSlash(src), "/")
	cmd := exec.CommandContext(t.Context(), "git", "clone", "-q", "--filter=blob:none", "--origin", remote, url, dest)
	cmd.Env = testenv.Base(map[string]string{"GIT_CONFIG_SYSTEM": os.DevNull})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone --filter=blob:none --origin %s: %v\n%s", remote, err, out)
	}
	return dest
}

// TestMineRefusesBloblessClone proves a partial clone is refused before the
// blame loop, which would fetch each missing blob on its own: on a cold clone
// that is one network round-trip per blamed line, so the run never finishes.
// A promisor remote demoted to false is a whole clone and mines.
func TestMineRefusesBloblessClone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		remote   string
		wantCode string
		demote   bool
	}{
		{name: "origin", remote: "origin", wantCode: "partial-clone"},
		{name: "named remote", remote: "upstream", wantCode: "partial-clone"},
		{name: "promisor false", remote: "origin", demote: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mineBloblessClone(t, tc.remote, tc.demote, tc.wantCode)
		})
	}
}

func mineBloblessClone(t *testing.T, remote string, demote bool, wantCode string) {
	t.Helper()
	b, _ := newMergedFixRepo(t)
	dest := bloblessClone(t, b.r.dir, remote)
	if demote {
		demotePromisor(t, dest, remote)
	} else {
		requirePromisor(t, dest, remote)
	}
	_, ref := Run(t.Context(), &Request{
		Runtime:  &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Env: userEnv(t, nil)},
		RepoRoot: dest, Mode: modeMine, Since: "90d", Until: "HEAD",
	})
	expectRefusal(t, ref, wantCode)
}

func expectRefusal(t *testing.T, ref *core.RefusalError, wantCode string) {
	t.Helper()
	if wantCode == "" {
		if ref != nil {
			t.Fatalf("refusal = %s: %s; want a demoted promisor to mine", ref.Code, ref.Message)
		}
		return
	}
	if ref == nil {
		t.Fatal("mine accepted a blobless clone; want the partial-clone refusal")
	}
	if ref.Code != wantCode || ref.Message != partialCloneLiteral {
		t.Fatalf("refusal = %s: %s; want partial-clone: %s", ref.Code, ref.Message, partialCloneLiteral)
	}
}

func requirePromisor(t *testing.T, dir, remote string) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "git", "-C", dir,
		"config", "--get", "remote."+remote+".promisor").Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		t.Fatalf("fixture is not a partial clone: promisor = %q, err = %v", out, err)
	}
}

func demotePromisor(t *testing.T, dir, remote string) {
	t.Helper()
	steps := [][]string{
		{"config", "remote." + remote + ".promisor", "false"},
		{"config", "--unset", "remote." + remote + ".partialclonefilter"},
		{"fetch", "--refetch"},
	}
	for _, args := range steps {
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = testenv.Base(map[string]string{"GIT_CONFIG_SYSTEM": os.DevNull})
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
