package backtest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// hostileGlobalConfig is an operator's ~/.gitconfig that would reshape every
// line the miner parses: colored diffs, prefixes, the patience algorithm,
// and signature output.
const hostileGlobalConfig = `[color]
	ui = always
[color "diff"]
	ui = always
[diff]
	noprefix = true
	mnemonicPrefix = true
	algorithm = patience
[log]
	showSignature = true
`

// hostileRepoConfig is the same hostile set written into the repository's own
// config, which the miner still reads and which no environment variable clears.
var hostileRepoConfig = [][2]string{
	{"color.ui", "always"},
	{"color.diff", "always"},
	{"diff.noprefix", "true"},
	{"diff.mnemonicPrefix", "true"},
	{"diff.algorithm", "patience"},
	{"log.showSignature", "true"},
}

// TestMineIgnoresHostileGitConfig proves the artifact is independent of the
// operator's git setup: a hostile global file and a hostile repository config
// yield the same mine.json bytes as a clean run.
func TestMineIgnoresHostileGitConfig(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	clean, cleanBytes := mineRun(t, b.r, b.base, nil)
	if len(clean.Mine.Candidates) != 1 || clean.Mine.Candidates[0].Head != "1d1518cf4adca036e1325e9666d54094dccacfa5" {
		t.Fatalf("clean candidates = %+v; want the canonical merged PR", clean.Mine.Candidates)
	}
	global := filepath.Join(t.TempDir(), "hostile.gitconfig")
	if err := os.WriteFile(global, []byte(hostileGlobalConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kv := range hostileRepoConfig {
		b.r.git("config", kv[0], kv[1])
	}
	hostile, hostileBytes := mineRun(t, b.r, b.base, map[string]string{"GIT_CONFIG_GLOBAL": global})
	if len(hostile.Mine.Candidates) != 1 {
		t.Fatalf("hostile candidates = %d; want 1 (skipped %+v)", len(hostile.Mine.Candidates), hostile.Mine.Skipped)
	}
	if !bytes.Equal(cleanBytes, hostileBytes) {
		t.Errorf("mine.json differs under hostile git config:\n%s\n---\n%s", cleanBytes, hostileBytes)
	}
}

// mineRun runs the service over base..HEAD with an injected environment
// snapshot and returns the envelope data and the bytes it wrote to mine.json.
func mineRun(t *testing.T, r *repo, since string, env map[string]string) (*Data, []byte) {
	t.Helper()
	resp, ref := Run(t.Context(), &Request{
		Runtime:  &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Env: env},
		RepoRoot: r.dir, Mode: modeMine, Since: since, Until: "HEAD",
	})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	return resp.Data, readMine(t, r, resp.Data.Run)
}
