package pack

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

// baseGit runs one git command in the test's repository, with the launcher's
// git environment scrubbed.
func baseGit(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required: the trusted-base read proves against real commits")
	}
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_NAME":     "pack",
		"GIT_AUTHOR_EMAIL":    "pack@example.invalid",
		"GIT_COMMITTER_NAME":  "pack",
		"GIT_COMMITTER_EMAIL": "pack@example.invalid",
	})
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// commitConfig writes the repository configuration, commits everything, and
// returns the new commit.
func commitConfig(t *testing.T, body, message string) string {
	t.Helper()
	if err := os.MkdirAll(".verdict", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".verdict", "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	baseGit(t, "add", "-A")
	baseGit(t, "commit", "-qm", message)
	return baseGit(t, "rev-parse", "HEAD")
}

// TestBaseTogglesReadsTheMergeBaseNotTheBranch proves the security property:
// a branch commit that tries to disable a builtin pack or the whole builtin
// layer never reaches the pack layer, because the read is anchored at the
// merge base with the default branch.
func TestBaseTogglesReadsTheMergeBaseNotTheBranch(t *testing.T) {
	t.Chdir(t.TempDir())
	baseGit(t, "init", "-q", "-b", "main")
	baseSHA := commitConfig(t, `{"schema_version":"1","packs":{"disable_rules":["sec-ssrf"]}}`, "base")
	baseGit(t, "checkout", "-q", "-b", "feature")
	commitConfig(t, `{"schema_version":"1","packs":{"merge_builtin":false,`+
		`"disable_packs":["verdict/go","go"],"disable_lenses":["go"]}}`, "head tampering")

	toggles, source, ref := BaseToggles(t.Context(), coreexec.Runner{}, ".")
	if ref != nil {
		t.Fatalf("BaseToggles: %s: %s", ref.Code, ref.Message)
	}
	if want := BaseConfigPrefix + baseSHA[:12]; source != want {
		t.Errorf("source = %q, want %q", source, want)
	}
	if toggles.MergeBuiltin != nil {
		t.Error("the branch's merge_builtin=false reached the pack layer")
	}
	if len(toggles.DisablePacks) != 0 || len(toggles.DisableLenses) != 0 {
		t.Errorf("the branch's disable lists reached the pack layer: %+v", toggles)
	}
	if want := []string{"sec-ssrf"}; !equalStrings(toggles.DisableRules, want) {
		t.Errorf("disable rules = %v, want the base's %v", toggles.DisableRules, want)
	}
}

// TestBaseTogglesIgnoresTheBranchesOwnUpstream proves the pushed-PR bypass is
// closed: on a branch whose upstream is `origin/feature`, `@{upstream}` names
// the PR head, so a base read anchored there would honor the head's tampering.
// The read must stay on the default branch even though the tracking ref exists
// and points at a commit that disables the whole builtin layer.
func TestBaseTogglesIgnoresTheBranchesOwnUpstream(t *testing.T) {
	sha := seedTrackingBranch(t)
	toggles, source, ref := BaseToggles(t.Context(), coreexec.Runner{}, ".")
	if ref != nil {
		t.Fatalf("BaseToggles: %s: %s", ref.Code, ref.Message)
	}
	if want := BaseConfigPrefix + sha[:12]; source != want {
		t.Errorf("source = %q, want the default branch's %q", source, want)
	}
	if toggles.MergeBuiltin != nil {
		t.Error("the pushed PR head's merge_builtin=false reached the pack layer")
	}
	if want := []string{"verdict/shell"}; !equalStrings(toggles.DisablePacks, want) {
		t.Errorf("disable packs = %v, want the base's %v", toggles.DisablePacks, want)
	}
	if want := []string{"sec-ssrf"}; !equalStrings(toggles.DisableRules, want) {
		t.Errorf("disable rules = %v, want the base's %v", toggles.DisableRules, want)
	}
	requireCheckFromBase(t, &toggles, source)
}

// requireCheckFromBase proves pack check applies the same base resolution: the
// pushed head disabled the security pack, so its lens still being offered is
// the proof the check ignored the head.
func requireCheckFromBase(t *testing.T, toggles *Toggles, source string) {
	t.Helper()
	req := checkRequest(t, ".", "cmd/app/main.go")
	req.Toggles, req.ConfigSource = *toggles, source
	resp, ref := RunCheck(t.Context(), req)
	if ref != nil {
		t.Fatalf("RunCheck: %s: %s", ref.Code, ref.Message)
	}
	if resp.Data.Config != source {
		t.Errorf("check config = %q, want %q", resp.Data.Config, source)
	}
	if findLens(resp.Data.Lenses, "security") == nil {
		t.Error("the pushed PR head's disable_packs removed the security lens from pack check")
	}
	if findPack(resp.Data.Packs, "verdict/shell") != nil {
		t.Error("the base's disable_packs no longer removed the shell pack")
	}
}

// TestBaseTogglesRefusesACanceledContext proves an interrupted base read
// refuses instead of reporting a checkout with nothing disabled.
func TestBaseTogglesRefusesACanceledContext(t *testing.T) {
	t.Chdir(t.TempDir())
	baseGit(t, "init", "-q", "-b", "main")
	commitConfig(t, `{"schema_version":"1","packs":{"disable_packs":["verdict/shell"]}}`, "base")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, ref := BaseToggles(ctx, coreexec.Runner{}, ".")
	if ref == nil || ref.Code != canceledCode {
		t.Fatalf("refusal = %v, want %s", ref, canceledCode)
	}
}

// seedTrackingBranch builds a clone whose `feature` branch tracks
// `origin/feature`: `main` disables one rule at the base and the pushed feature
// head tries to disable the layer and a whole pack. It returns main's commit.
func seedTrackingBranch(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	baseGit(t, "init", "-q", "--bare", bare)
	work := filepath.Join(root, "work")
	baseGit(t, "clone", "-q", bare, work)
	t.Chdir(work)
	baseGit(t, "checkout", "-q", "-b", "main")
	baseSHA := commitConfig(t, `{"schema_version":"1","packs":{"disable_packs":["verdict/shell"],`+
		`"disable_rules":["sec-ssrf"]}}`, "base")
	baseGit(t, "push", "-q", "-u", "origin", "main")
	baseGit(t, "checkout", "-q", "-b", "feature")
	commitConfig(t, `{"schema_version":"1","packs":{"merge_builtin":false,`+
		`"disable_packs":["verdict/security"]}}`, "head tampering")
	baseGit(t, "push", "-q", "-u", "origin", "feature")
	if got, want := baseGit(t, "rev-parse", "--abbrev-ref", "@{upstream}"), "origin/feature"; got != want {
		t.Fatalf("upstream = %q, want %q: the fixture must reproduce the bypass", got, want)
	}
	return baseSHA
}

// TestBaseTogglesFollowsTheDefaultBranchName proves a repository whose default
// branch is `master` resolves the same way, rather than degrading to no
// configuration.
func TestBaseTogglesFollowsTheDefaultBranchName(t *testing.T) {
	t.Chdir(t.TempDir())
	baseGit(t, "init", "-q", "-b", "master")
	sha := commitConfig(t, `{"schema_version":"1","packs":{"disable_packs":["verdict/shell"]}}`, "base")
	baseGit(t, "checkout", "-q", "-b", "topic")
	commitConfig(t, `{"schema_version":"1"}`, "unrelated head")

	toggles, source, ref := BaseToggles(t.Context(), coreexec.Runner{}, ".")
	if ref != nil {
		t.Fatalf("BaseToggles: %s: %s", ref.Code, ref.Message)
	}
	if want := BaseConfigPrefix + sha[:12]; source != want {
		t.Errorf("source = %q, want %q", source, want)
	}
	if want := []string{"verdict/shell"}; !equalStrings(toggles.DisablePacks, want) {
		t.Errorf("disable packs = %v, want %v", toggles.DisablePacks, want)
	}
}

// TestBaseTogglesWithoutAConfiguration proves an absent base configuration
// turns nothing off while the row still names the commit that was read.
func TestBaseTogglesWithoutAConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())
	baseGit(t, "init", "-q", "-b", "main")
	if err := os.WriteFile("README.md", []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseGit(t, "add", "-A")
	baseGit(t, "commit", "-qm", "base")
	sha := baseGit(t, "rev-parse", "HEAD")

	toggles, source, ref := BaseToggles(t.Context(), coreexec.Runner{}, ".")
	if ref != nil {
		t.Fatalf("BaseToggles: %s: %s", ref.Code, ref.Message)
	}
	if want := BaseConfigPrefix + sha[:12]; source != want {
		t.Errorf("source = %q, want %q", source, want)
	}
	if toggles.MergeBuiltin != nil || len(toggles.DisablePacks) != 0 || len(toggles.DisableRules) != 0 {
		t.Errorf("toggles = %+v, want zero", toggles)
	}
}

// TestBaseTogglesRefusesAMalformedBaseConfig proves a base document that does
// not parse refuses instead of silently turning the toggles off.
func TestBaseTogglesRefusesAMalformedBaseConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	baseGit(t, "init", "-q", "-b", "main")
	commitConfig(t, `{"schema_version":"1","packs":{"disable_packs":["Not A Pack"]}}`, "base")

	_, _, ref := BaseToggles(t.Context(), coreexec.Runner{}, ".")
	if ref == nil || ref.Code != "bad-config" {
		t.Fatalf("refusal = %v, want bad-config", ref)
	}
	if !strings.Contains(ref.Message, "disable_packs") {
		t.Errorf("message = %q, want it to name the key", ref.Message)
	}
}

// TestBaseTogglesWithoutACommanderOrBase proves the read degrades: no
// commander, and a checkout with no default branch, both read as no base
// configuration rather than a refusal.
func TestBaseTogglesWithoutACommanderOrBase(t *testing.T) {
	toggles, source, ref := BaseToggles(t.Context(), nil, t.TempDir())
	if ref != nil {
		t.Fatalf("nil commander: %v", ref)
	}
	if source != ConfigSourceNone || toggles.MergeBuiltin != nil {
		t.Errorf("nil commander = %+v / %q", toggles, source)
	}
	if _, source, ref := BaseToggles(t.Context(), coreexec.Runner{}, t.TempDir()); ref != nil || source != ConfigSourceNone {
		t.Errorf("no checkout = %q / %v, want none", source, ref)
	}
}

// equalStrings compares two id lists.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBaseTogglesRefusesAMissingMergeBase proves the read fails closed: a
// default branch that resolves but shares no merge base with HEAD (an orphan
// branch, an unrelated history) refuses instead of reading as "no base
// configuration", which would drop every base-owned pack toggle and pin.
func TestBaseTogglesRefusesAMissingMergeBase(t *testing.T) {
	t.Chdir(t.TempDir())
	baseGit(t, "init", "-q", "-b", "main")
	commitConfig(t, `{"schema_version":"1","packs":{"disable_packs":["verdict/shell"]}}`, "base")
	baseGit(t, "checkout", "-q", "--orphan", "unrelated")
	commitConfig(t, `{"schema_version":"1"}`, "unrelated head")

	toggles, source, ref := BaseToggles(t.Context(), coreexec.Runner{}, ".")
	if ref == nil || ref.Code != CodePackGitFailed {
		t.Fatalf("BaseToggles = %+v / %q / %v, want %s", toggles, source, ref, CodePackGitFailed)
	}
	if want := "git merge-base failed"; !strings.Contains(ref.Message, want) {
		t.Errorf("message = %q, want it to contain %q", ref.Message, want)
	}
	if source != "" {
		t.Errorf("source = %q, want no source label beside a refusal", source)
	}
}
