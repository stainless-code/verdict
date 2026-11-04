package testenv_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

// TestMain isolates the process before any fixture child inherits its env.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

// gitLocationNames are the variables git exports to hooks; each one would
// retarget a fixture at the launcher's checkout.
var gitLocationNames = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_PREFIX",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_COMMON_DIR",
}

// TestBaseScrubsGitLocation proves a hook-launched parent cannot retarget a
// child built through Base, and that the pinned identity survives the scrub.
func TestBaseScrubsGitLocation(t *testing.T) {
	leaked := slices.Concat(gitLocationNames, []string{"GIT_CONFIG_PARAMETERS", "GIT_AUTHOR_DATE", "GIT_AUTHOR_NAME"})
	for _, name := range leaked {
		t.Setenv(name, "/leaked")
	}
	base := envMap(testenv.Base(nil))
	checkDropped(t, base, slices.Concat(gitLocationNames, []string{"GIT_CONFIG_PARAMETERS", "GIT_AUTHOR_DATE"}))
	if value := base["GIT_AUTHOR_NAME"]; value == "" || value == "/leaked" {
		t.Errorf("GIT_AUTHOR_NAME = %q, want the pinned identity", value)
	}
}

func checkDropped(t *testing.T, env map[string]string, names []string) {
	t.Helper()
	for _, name := range names {
		if value, ok := env[name]; ok {
			t.Errorf("Base kept %s=%q", name, value)
		}
	}
}

// TestIsolateDropsInheritedGit proves a process launched with a hook's git
// variables loses them before any fixture runs.
func TestIsolateDropsInheritedGit(t *testing.T) {
	t.Setenv("GIT_DIR", "/leaked")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'user.name=leaked'")
	if err := testenv.Isolate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"GIT_DIR", "GIT_CONFIG_PARAMETERS"} {
		if value, ok := os.LookupEnv(name); ok {
			t.Errorf("%s = %q, want unset", name, value)
		}
	}
	if value := os.Getenv("GIT_AUTHOR_NAME"); value == "" {
		t.Error("GIT_AUTHOR_NAME unset, want the pinned identity")
	}
}

// TestIsolateLeavesTheLauncherRepoUntouched re-execs this suite with git's
// hook variables pointed at a victim repository: the child's fixture commit
// must land in its own checkout and leave the victim's config and refs alone.
func TestIsolateLeavesTheLauncherRepoUntouched(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	victim := filepath.Join(t.TempDir(), "victim")
	runGit(t, testenv.Base(nil), "", "init", "-q", victim)
	gitDir := filepath.Join(victim, ".git")
	configBefore := readText(t, filepath.Join(gitDir, "config"))

	fixture := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixture, "probe.txt"), []byte("probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestGitFixtureChild$")
	cmd.Env = append(os.Environ(),
		"TESTENV_FIXTURE_DIR="+fixture,
		"GIT_DIR="+gitDir,
		"GIT_WORK_TREE="+victim,
		"GIT_INDEX_FILE="+filepath.Join(gitDir, "index"),
		"GIT_CONFIG_PARAMETERS='user.name=leaked'",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child suite: %v\n%s", err, out)
	}

	if got := readText(t, filepath.Join(gitDir, "config")); got != configBefore {
		t.Fatalf("victim config changed:\n%s", got)
	}
	if refs := runGit(t, testenv.Base(nil), "", "--git-dir="+gitDir, "for-each-ref"); refs != "" {
		t.Fatalf("victim refs changed:\n%s", refs)
	}
	if count := runGit(t, testenv.Base(nil), fixture, "rev-list", "--count", "HEAD"); count != "1" {
		t.Fatalf("fixture commits = %q, want 1", count)
	}
}

// TestGitFixtureChild is the child half of
// TestIsolateLeavesTheLauncherRepoUntouched: it runs the fixture sequence that
// used to write through GIT_DIR, with the inherited environment untouched.
func TestGitFixtureChild(t *testing.T) {
	fixture := os.Getenv("TESTENV_FIXTURE_DIR")
	if fixture == "" {
		return
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"commit", "-qm", "seed"},
	} {
		runGit(t, os.Environ(), fixture, args...)
	}
}

func runGit(t *testing.T, env []string, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func readText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func envMap(env []string) map[string]string {
	got := make(map[string]string, len(env))
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			got[name] = value
		}
	}
	return got
}
