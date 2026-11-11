package backtest

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestMain pins the git environment every fixture assumes.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

// repo builds a hermetic git fixture with pinned identity and dates. Each
// commit gets the next calendar day, so merge dates and blame are stable.
type repo struct {
	t   *testing.T
	dir string
	day int
}

// newRepo creates an empty repository on main.
func newRepo(t *testing.T) *repo {
	t.Helper()
	r := &repo{t: t, dir: t.TempDir(), day: 1}
	r.git("init", "-q", "-b", "main")
	return r
}

// date renders the fixture's next pinned commit date.
func (r *repo) date() string {
	return time.Date(2024, 1, r.day, 0, 0, 0, 0, time.UTC).Format(gitDateLayout)
}

// git runs one git command with the pinned identity and the current date.
// The host's protected config files are pinned too: a runner's own
// safe.directory must not decide what the fixture repository contains.
func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), "git", args...)
	cmd.Dir = r.dir
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_DATE":    r.date(),
		"GIT_COMMITTER_DATE": r.date(),
		"GIT_CONFIG_SYSTEM":  os.DevNull,
	})
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// write writes one file under the repo.
func (r *repo) write(name, body string) {
	r.t.Helper()
	path := filepath.Join(r.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// commit stages everything and commits it, returning the commit id.
func (r *repo) commit(msg string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-qm", msg)
	r.day++
	return r.head()
}

// merge lands branch with a real merge commit, returning its id.
func (r *repo) merge(branch, msg string) string {
	r.t.Helper()
	r.git("merge", "--no-ff", branch, "-m", msg)
	r.day++
	return r.head()
}

// squash squash-merges branch under msg, returning the squash commit id.
func (r *repo) squash(branch, msg string) string {
	r.t.Helper()
	r.git("merge", "--squash", branch)
	r.git("commit", "-qm", msg)
	r.day++
	return r.head()
}

// switchNew creates and switches to branch.
func (r *repo) switchNew(branch string) {
	r.t.Helper()
	r.git("switch", "-qc", branch)
}

// switchMain switches back to main.
func (r *repo) switchMain() {
	r.t.Helper()
	r.git("switch", "-q", "main")
}

// gitAllow runs a git command that may fail, returning its output and success.
func (r *repo) gitAllow(args ...string) (string, bool) {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), "git", args...)
	cmd.Dir = r.dir
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_DATE":    r.date(),
		"GIT_COMMITTER_DATE": r.date(),
		"GIT_CONFIG_SYSTEM":  os.DevNull,
	})
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// head resolves HEAD to its full id.
func (r *repo) head() string {
	r.t.Helper()
	return stringTrim(r.git("rev-parse", "HEAD"))
}

// stringTrim removes trailing line endings from git output.
func stringTrim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// writeGitConfig writes one GIT_CONFIG_GLOBAL target and returns its path.
func writeGitConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// userEnv returns the environment snapshot the service resolves the user's
// protected git config from. The host's system and global files are replaced —
// the system scope with the null device, the global scope with one empty file
// per test — so a developer's or a runner's own safe.directory cannot decide
// the result. A test that sets its own value keeps it: override wins.
func userEnv(t *testing.T, override map[string]string) map[string]string {
	t.Helper()
	env := map[string]string{
		"GIT_CONFIG_SYSTEM": os.DevNull,
		"GIT_CONFIG_GLOBAL": writeGitConfig(t, ""),
	}
	maps.Copy(env, override)
	return env
}

// runMine runs the service against r over since..until.
func runMine(t *testing.T, r *repo, since string) *Data {
	t.Helper()
	resp, ref := Run(t.Context(), &Request{
		Runtime:  &core.Runtime{Commander: coreexec.Runner{MissingTool: true}, Env: userEnv(t, nil)},
		RepoRoot: r.dir, Mode: modeMine, Since: since, Until: "HEAD",
	})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	return resp.Data
}
