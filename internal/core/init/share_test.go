package initcore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/contenttree"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

// shareConfigTail is the verify section init writes for a checkout whose only
// lockfile is npm's.
const shareConfigTail = `  },
  "verify": {
    "share": ["node_modules"]
  }
}`

// lockfileRepo is a git checkout holding the named files (path to body).
func lockfileRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	shareGitIn(t, repo, "init", "-q")
	for name, body := range files {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func shareGitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// hermeticGit points the process's git at no user or system configuration
// and no global excludes file: init's own git calls inherit the process
// environment, and a developer's ~/.config/git/ignore must not decide what
// these tests see as ignored.
func hermeticGit(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
}

// runShareInit runs init in repo with real git behind it.
func runShareInit(t *testing.T, repo string) string {
	t.Helper()
	req := ciRequest(repo, githubTarget(t))
	req.Commander = coreexec.Watchdog{}
	mustRunInit(t, req)
	raw, err := os.ReadFile(filepath.Join(repo, reviewDotName, configName))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestInitProposesTheDependencyDirAPackageLockImplies proves the proposal
// lands in the written configuration only when a lockfile implies it and
// .gitignore already covers the directory, whether or not it is installed.
func TestInitProposesTheDependencyDirAPackageLockImplies(t *testing.T) {
	hermeticGit(t)
	repo := lockfileRepo(t, map[string]string{"package-lock.json": "{}\n", ".gitignore": "node_modules/\n"})
	if got := runShareInit(t, repo); !strings.HasSuffix(strings.TrimRight(got, "\n"), shareConfigTail) {
		t.Fatalf(".verdict/config.json =\n%s\nwant it to end with the node_modules proposal:\n%s", got, shareConfigTail)
	}
	bare := lockfileRepo(t, map[string]string{".gitignore": "node_modules/\n"})
	if got := runShareInit(t, bare); strings.Contains(got, `"verify"`) {
		t.Fatalf("init proposed a share with no lockfile:\n%s", got)
	}
}

// TestInitProposesNoShareVerifyWouldRefuse keeps init from writing a share
// whose first verify refuses: a directory .gitignore does not cover, or one
// git tracks a file in.
func TestInitProposesNoShareVerifyWouldRefuse(t *testing.T) {
	hermeticGit(t)
	unignored := lockfileRepo(t, map[string]string{"package-lock.json": "{}\n"})
	if got := runShareInit(t, unignored); strings.Contains(got, `"verify"`) {
		t.Fatalf("init proposed node_modules though .gitignore does not cover it:\n%s", got)
	}
	tracked := lockfileRepo(t, map[string]string{
		"package-lock.json": "{}\n", ".gitignore": "node_modules/\n", "node_modules/patched/index.js": "x\n",
	})
	shareGitIn(t, tracked, "add", "-f", "node_modules/patched/index.js")
	if got := runShareInit(t, tracked); strings.Contains(got, `"verify"`) {
		t.Fatalf("init proposed node_modules though git tracks a file in it:\n%s", got)
	}
}

// TestProposedSharesReadTheLockfileTable covers every row once: one entry
// per directory, in table order, and nothing for a lockfile that is not a
// regular file.
func TestProposedSharesReadTheLockfileTable(t *testing.T) {
	t.Parallel()
	repo := lockfileRepo(t, map[string]string{
		"yarn.lock": "", "pnpm-lock.yaml": "", "poetry.lock": "", "uv.lock": "", ".gitignore": "node_modules/\n.venv/\n",
	})
	if err := os.Mkdir(filepath.Join(repo, "package-lock.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := contenttree.Git{Cmdr: coreexec.Watchdog{}, Root: repo, Env: testenv.Base(nil)}
	if got := strings.Join(proposedShares(t.Context(), g), ","); got != "node_modules,.venv" {
		t.Fatalf("proposedShares = %q, want node_modules,.venv", got)
	}
}
