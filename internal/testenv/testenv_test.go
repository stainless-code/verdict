package testenv_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/envsecret"
	"github.com/stainless-code/verdict/internal/testenv"
)

// secretNames are planted in the parent and must never reach a child.
var secretNames = []string{
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"ANTHROPIC_API_KEY",
	"NPM_TOKEN",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_REGION",
	"VERDICT_TOKEN",
	"SSH_AUTH_SOCK",
}

// TestBaseKeepsSecretsOutOfChildren proves the deny predicate end to end: the
// parent plants a fake GH_TOKEN (and friends), and a real child process
// reports which of them it can still read. None may survive.
func TestBaseKeepsSecretsOutOfChildren(t *testing.T) {
	for _, name := range secretNames {
		t.Setenv(name, "fake-credential-value")
	}
	report := filepath.Join(t.TempDir(), "child-env.txt")
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestBaseChildProcess$")
	cmd.Env = append(testenv.Base(nil), "TESTENV_REPORT="+report)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("read child report: %v", err)
	}
	if leaked := strings.TrimSpace(string(raw)); leaked != "" {
		t.Fatalf("child inherited secret-shaped variables:\n%s", leaked)
	}
}

// TestBaseChildProcess is the child half of TestBaseKeepsSecretsOutOfChildren.
// It is inert in a normal suite run (no TESTENV_REPORT).
func TestBaseChildProcess(t *testing.T) {
	report := os.Getenv("TESTENV_REPORT")
	if report == "" {
		return
	}
	var leaked []string
	for _, name := range secretNames {
		if _, ok := os.LookupEnv(name); ok {
			leaked = append(leaked, name)
		}
	}
	if err := os.WriteFile(report, []byte(strings.Join(leaked, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestGitConfigPinsRepositories proves the emitted GIT_CONFIG_* entries
// actually govern git, and that a hostile host global config is ignored.
func TestGitConfigPinsRepositories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	hostile := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(hostile,
		[]byte("[commit]\n\tgpgsign = true\n[core]\n\tautocrlf = true\n[init]\n\tdefaultBranch = trunk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", hostile)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.autocrlf")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	want := map[string]string{
		"core.autocrlf":      "false",
		"commit.gpgsign":     "false",
		"tag.gpgsign":        "false",
		"init.defaultBranch": "main",
		"core.longpaths":     "true",
	}
	for key, value := range want {
		cmd := exec.CommandContext(t.Context(), "git", "config", "--get", key)
		cmd.Dir = t.TempDir()
		cmd.Env = testenv.Base(nil)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git config --get %s: %v", key, err)
		}
		if got := strings.TrimSpace(string(out)); got != value {
			t.Errorf("git config --get %s = %q, want %q", key, got, value)
		}
	}
}

// TestEvalsHelperMatchesGo proves the shell twin (evals/lib/testenv.sh) drops
// the same names and exports the same git config as testenv.Base, so the Go
// harnesses and the evals cannot drift into two policies.
func TestEvalsHelperMatchesGo(t *testing.T) {
	got := evalsHelperEnv(t)
	checkEvalsDropped(t, got)
	checkEvalsGitConfig(t, got)
}

// checkEvalsDropped asserts the shell helper removed every planted name.
func checkEvalsDropped(t *testing.T, got map[string]string) {
	t.Helper()
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "NPM_TOKEN", "AWS_SECRET_ACCESS_KEY", "AWS_REGION", "SOME_KEY"} {
		if _, ok := got[name]; ok {
			t.Errorf("evals helper kept %s", name)
		}
	}
}

// checkEvalsGitConfig asserts the shell helper exported the same pinned keys
// and values as GitConfig.
func checkEvalsGitConfig(t *testing.T, got map[string]string) {
	t.Helper()
	for _, kv := range testenv.GitConfig() {
		name, value, _ := strings.Cut(kv, "=")
		if name == "GIT_CONFIG_GLOBAL" {
			checkGlobalSet(t, got, name)
			continue
		}
		checkEnvValue(t, got, name, value)
	}
}

// checkGlobalSet accepts any path: both point at an empty file, but the
// eval harness owns its sandbox copy.
func checkGlobalSet(t *testing.T, got map[string]string, name string) {
	t.Helper()
	if got[name] == "" {
		t.Errorf("evals helper left %s unset", name)
	}
}

// checkEnvValue asserts one exported value matches the Go helper's.
func checkEnvValue(t *testing.T, got map[string]string, name, value string) {
	t.Helper()
	if got[name] != value {
		t.Errorf("evals helper %s = %q, want %q", name, got[name], value)
	}
}

// evalsHelperEnv runs the shell twin with planted credentials and returns
// the environment it exports.
func evalsHelperEnv(t *testing.T) map[string]string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("evals helper is bash; the Windows lane runs Go tests only")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	helper, err := filepath.Abs(filepath.Join("..", "..", "evals", "lib", "testenv.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), bash, "-c", `. "$1"; testenv_init "$2"; env`,
		"bash", helper, t.TempDir())
	cmd.Env = append(os.Environ(),
		"GH_TOKEN=fake", "GITHUB_TOKEN=fake", "NPM_TOKEN=fake",
		"AWS_SECRET_ACCESS_KEY=fake", "AWS_REGION=fake", "SOME_KEY=fake")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("evals helper: %v\n%s", err, out)
	}
	return parseEnv(t, string(out))
}

// TestSecretPredicateMatchesShell pins the shared suffix rule on the shapes
// this predicate names, including rotation suffixes.
func TestSecretPredicateMatchesShell(t *testing.T) {
	names := []string{
		"GH_TOKEN", "GITHUB_TOKEN", "NPM_TOKEN", "NPM_TOKEN_2", "SOME_KEY",
		"ANTHROPIC_API_KEY", "AWS_SECRET_ACCESS_KEY", "SSH_AUTH_SOCK",
		"VERDICT_TOKEN_10", "PASSWORD", "PATH", "HOME", "PWD", "TOKENIZER",
	}
	for _, name := range names {
		want := envsecret.IsSecret(name)
		got := shellSaysSecret(t, name)
		if got != want {
			t.Errorf("%s: shell says secret=%v, Go says %v", name, got, want)
		}
	}
}

// shellSaysSecret runs testenv_is_secret from the shell helper for one name.
func shellSaysSecret(t *testing.T, name string) bool {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	helper, err := filepath.Abs(filepath.Join("..", "..", "evals", "lib", "testenv.sh"))
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(name)
	script := `. "$1"; if testenv_is_secret "$2"; then echo yes; else echo no; fi`
	out, err := exec.CommandContext(t.Context(), bash, "-c", script, "bash", helper, lower).Output()
	if err != nil {
		t.Fatalf("shell predicate: %v", err)
	}
	return strings.TrimSpace(string(out)) == "yes"
}

// parseEnv turns env(1) output into a map.
func parseEnv(t *testing.T, out string) map[string]string {
	t.Helper()
	env := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		env[name] = value
	}
	return env
}
