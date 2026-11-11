package backtest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// otherOwnerEnv is the snapshot that makes the fixture checkout read as owned
// by another uid, with the user's config scopes pinned to path.
func otherOwnerEnv(path string) map[string]string {
	return map[string]string{
		"GIT_CONFIG_GLOBAL":               path,
		"GIT_CONFIG_NOSYSTEM":             "1",
		"GIT_TEST_ASSUME_DIFFERENT_OWNER": "1",
	}
}

// TestMineHonoursUserSafeDirectory proves a checkout owned by another uid is
// minable when the user's protected config trusts it: the trust is read with
// the user's own git environment and forwarded into the scrubbed calls.
func TestMineHonoursUserSafeDirectory(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	global := writeGitConfig(t, "[safe]\n\tdirectory = *\n")
	data, _ := mineRun(t, b.r, b.base, otherOwnerEnv(global))
	want := "1d1518cf4adca036e1325e9666d54094dccacfa5"
	if len(data.Mine.Candidates) != 1 || data.Mine.Candidates[0].Head != want {
		t.Fatalf("candidates = %+v; want the canonical merged PR at %s", data.Mine.Candidates, want)
	}
}

// mineRefusal runs the canonical window against b and returns its refusal.
func mineRefusal(t *testing.T, b bugRepo, env map[string]string) *core.RefusalError {
	t.Helper()
	_, ref := Run(t.Context(), &Request{
		Runtime:  &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Env: userEnv(t, env)},
		RepoRoot: b.r.dir, Mode: modeMine, Since: b.base, Until: "HEAD",
	})
	return ref
}

// TestMineNamesDubiousOwnership proves a checkout git refuses to read is named
// as git names it: the refusal carries git's own line instead of claiming the
// path is not a repository.
func TestMineNamesDubiousOwnership(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	ref := mineRefusal(t, b, otherOwnerEnv(writeGitConfig(t, "")))
	if ref == nil {
		t.Fatal("refusal = nil; want backtest-failed naming dubious ownership")
	}
	want := "git rev-parse failed: fatal: detected dubious ownership in repository at '"
	if ref.Code != codeBacktestFail || !strings.HasPrefix(ref.Message, want) {
		t.Fatalf("refusal = %s: %s; want %s with prefix %q", ref.Code, ref.Message, codeBacktestFail, want)
	}
}

// TestMineHonoursSafeDirectoryReset proves a reset the user wrote after `*`
// still narrows the list: dropping the empty value, or reordering the pair,
// would grant a trust the user withdrew.
func TestMineHonoursSafeDirectoryReset(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	ref := mineRefusal(t, b, otherOwnerEnv(writeGitConfig(t, "[safe]\n\tdirectory = *\n\tdirectory =\n")))
	if ref == nil || ref.Code != codeBacktestFail || !strings.Contains(ref.Message, "detected dubious ownership") {
		t.Fatalf("refusal = %+v; want backtest-failed naming dubious ownership", ref)
	}
}

// TestMineForwardsOnlySafeDirectory proves safe.directory is the only value
// that crosses from the user's config: a global file that also sets
// color.ui=always and a core.attributesFile no command-scope pin covers yields
// the same mine.json bytes as one that sets neither.
func TestMineForwardsOnlySafeDirectory(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	attrs := filepath.Join(t.TempDir(), "attributes")
	if err := os.WriteFile(attrs, []byte("*.go binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean := writeGitConfig(t, "")
	hostile := writeGitConfig(t, fmt.Sprintf(
		"[safe]\n\tdirectory = *\n[core]\n\tattributesFile = %s\n", attrs)+hostileGlobalConfig)
	_, want := mineRun(t, b.r, b.base, map[string]string{"GIT_CONFIG_GLOBAL": clean})
	_, got := mineRun(t, b.r, b.base, map[string]string{"GIT_CONFIG_GLOBAL": hostile})
	if !bytes.Equal(want, got) {
		t.Errorf("mine.json differs under a hostile global config that also trusts every directory:\n%s\n---\n%s",
			want, got)
	}
}

// TestMineForwardsSafeDirectoryArgv pins the argv and environments: both reads
// carry the user's global config, and every mining call carries the user's
// values as command scope ahead of the git pins while its config stays nulled.
func TestMineForwardsSafeDirectoryArgv(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	global := writeGitConfig(t, "[safe]\n\tdirectory = /trusted/one\n\tdirectory = *\n"+hostileGlobalConfig)
	rec := &pinRecorder{inner: coreexec.Runner{MissingTool: true}}
	_, ref := Run(t.Context(), &Request{
		Runtime: &core.Runtime{
			Commander: rec,
			Env: userEnv(t, map[string]string{
				"HOME": t.TempDir(), "GIT_CONFIG_GLOBAL": global,
				"GIT_CONFIG_NOSYSTEM": "1",
			}),
		},
		RepoRoot: b.r.dir, Mode: modeMine, Since: b.base, Until: "HEAD",
	})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	var reads [][]string
	var mined int
	for _, call := range rec.calls {
		if isTrustRead(call.argv) {
			reads = append(reads, call.argv)
			checkReadConfig(t, call.argv, call.env, global)
			continue
		}
		mined++
		checkForwardedCall(t, call.argv, call.env)
	}
	if !slices.EqualFunc(reads, trustReadArgvs, slices.Equal) {
		t.Errorf("trust reads = %v; want %v", reads, trustReadArgvs)
	}
	if mined == 0 {
		t.Fatal("no mining call was recorded; the guard would pass vacuously")
	}
}

// checkReadConfig proves one safe.directory read carries the user's global
// config rather than the nulled one.
func checkReadConfig(t *testing.T, argv []string, env map[string]string, global string) {
	t.Helper()
	if got := env["GIT_CONFIG_GLOBAL"]; got != global {
		t.Errorf("read %v: GIT_CONFIG_GLOBAL = %q; want the user's %q", argv, got, global)
	}
}

// checkForwardedCall proves one mining call carries the forwarded values as
// command scope ahead of the git pins while its own config stays nulled.
func checkForwardedCall(t *testing.T, argv []string, env map[string]string) {
	t.Helper()
	if len(argv) < 3 {
		t.Fatalf("mining argv = %v; want `git -C <dir> ...`", argv)
	}
	want := []string{
		"git", "-C", argv[2],
		"-c", "safe.directory=/trusted/one",
		"-c", "safe.directory=*",
	}
	if len(argv) < len(want) || !slices.Equal(argv[:len(want)], want) {
		t.Fatalf("mining argv = %v; want prefix %v", argv, want)
	}
	if got := env["GIT_CONFIG_GLOBAL"]; got != os.DevNull {
		t.Errorf("mining argv %v: GIT_CONFIG_GLOBAL = %q; want %q", argv, got, os.DevNull)
	}
}

// TestSplitTrustKeepsEmptyReset proves an empty value survives the parse in
// its position: git reads it as resetting the list, so dropping it would widen
// the trust the miner forwards.
func TestSplitTrustKeepsEmptyReset(t *testing.T) {
	if got, want := splitTrust(""), []string(nil); !slices.Equal(got, want) {
		t.Errorf("splitTrust(\"\") = %q; want %q", got, want)
	}
	if got, want := splitTrust("\x00/trusted/one\x00"), []string{"", "/trusted/one"}; !slices.Equal(got, want) {
		t.Errorf("splitTrust = %q; want %q", got, want)
	}
}
