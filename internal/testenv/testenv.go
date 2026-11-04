// Package testenv builds the deterministic child environment shared by every
// harness that spawns git, gh, or the verdict binary. It exists so CI
// determinism is structural, not a per-test habit: the developer's (or
// runner's) git config, credentials, and harness knobs never leak into a
// hermetic child.
//
// Three rules live here:
//
//   - Git runs with a fixed configuration (core.autocrlf=false,
//     commit.gpgsign=false, tag.gpgsign=false, init.defaultBranch=main,
//     core.longpaths=true, maintenance.auto=false, gc.auto=0, no system or
//     user config) and a fixed identity, so a fixture commit cannot fail
//     because the host signs commits, defaults to another branch name, or
//     rewrites line endings.
//   - Secret-shaped variables (*_TOKEN, *_KEY, *_SECRET, AWS_*) are dropped
//     from the inherited environment. A child may still receive one through
//     extra, which is how a test that *needs* an auth variable plants it.
//   - Git's own location variables (GIT_DIR and friends) are dropped too.
//     Git exports them to hooks, so a suite launched from a pre-push hook would
//     otherwise retarget every fixture command at the developer's checkout.
//   - Waits poll through [Until] under one fixed bound, so a child that never
//     reaches the awaited state fails the test instead of the suite's global
//     timeout.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stainless-code/verdict/internal/envsecret"
	"github.com/stainless-code/verdict/internal/jq"
)

// dropPrefixes are harness/CI knobs that never belong in a hermetic child.
// GH_ and GITHUB_ cover the runner's gh auth and host selection (GH_TOKEN,
// GITHUB_TOKEN, GH_HOST), AWS_ covers cloud credentials, and GIT_CONFIG_* is
// re-emitted by GitConfig so the caller's values cannot shadow the pinned ones.
var dropPrefixes = []string{"REVIEW_", "GH_", "GITHUB_", "GIT_CONFIG_", "AWS_"}

// dropNames are exact variables the harness always sets itself.
var dropNames = []string{"HOME", "TMPDIR", "XDG_CONFIG_HOME", "NO_COLOR"}

// gitLocation is git's escape hatch set: each one overrides the repository,
// index, or object store that -C and the working directory would select, and
// git exports them to hooks.
var gitLocation = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_PREFIX",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_COMMON_DIR",
}

// gitClock is the pair git would otherwise inherit from the launcher, which
// would make commit hashes depend on the host.
var gitClock = []string{"GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE"}

// gitIdentity is the fixed author and committer pinned through the environment,
// so no fixture writes user.* into a repository config.
var gitIdentity = [][2]string{
	{"GIT_AUTHOR_NAME", "verdict-test"},
	{"GIT_AUTHOR_EMAIL", "test@verdict.invalid"},
	{"GIT_COMMITTER_NAME", "verdict-test"},
	{"GIT_COMMITTER_EMAIL", "test@verdict.invalid"},
}

// Fixed values and separators shared by the emitted environment.
const (
	envSep     = "="
	configPerm = 0o600
	falseValue = "false"
)

// gitConfig is the fixed repository behavior every fixture assumes. The order
// is stable so the emitted GIT_CONFIG_KEY_*/VALUE_* pairs are reproducible.
var gitConfig = [][2]string{
	{"core.autocrlf", falseValue},
	{"commit.gpgsign", falseValue},
	{"tag.gpgsign", falseValue},
	{"init.defaultBranch", "main"},
	{"core.longpaths", "true"},
	{"maintenance.auto", falseValue},
	{"gc.auto", "0"},
}

// emptyGlobalConfig is the GIT_CONFIG_GLOBAL target: a path that exists but is
// empty, so the host's ~/.gitconfig is never read. [os.DevNull] is not
// portable to every Git-for-Windows build, hence a real file.
func emptyGlobalConfig() string {
	emptyOnce.Do(func() {
		path := filepath.Join(os.TempDir(), "verdict-testenv-empty-gitconfig")
		if err := os.WriteFile(path, nil, configPerm); err != nil {
			emptyPath = os.DevNull
			return
		}
		emptyPath = path
	})
	return emptyPath
}

var (
	emptyOnce sync.Once
	emptyPath string
)

// GitConfig returns the exported environment that pins git: no system config,
// an empty global config, the fixed keys at command scope (highest precedence,
// so a repository's own config cannot override them), and the fixed identity.
func GitConfig() []string {
	env := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + emptyGlobalConfig(),
		"GIT_CONFIG_COUNT=" + strconv.Itoa(len(gitConfig)),
	}
	for i, kv := range gitConfig {
		env = append(env,
			"GIT_CONFIG_KEY_"+strconv.Itoa(i)+"="+kv[0],
			"GIT_CONFIG_VALUE_"+strconv.Itoa(i)+"="+kv[1])
	}
	for _, kv := range gitIdentity {
		env = append(env, kv[0]+envSep+kv[1])
	}
	return env
}

// Isolate scrubs the calling process so every child it later spawns — including
// the inherited-environment git and gh calls the code under test makes — sees
// the hermetic environment instead of the launcher's.
func Isolate() error {
	if err := scrubGitEnv(); err != nil {
		return err
	}
	return pinGitEnv()
}

// scrubGitEnv removes every inherited variable that would retarget git or
// re-pin its configuration.
func scrubGitEnv() error {
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, envSep)
		if !ok || !gitLeak(name) {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			return jq.Sealed("unset "+name, err)
		}
	}
	return nil
}

// pinGitEnv exports the fixed configuration and identity.
func pinGitEnv() error {
	for _, kv := range GitConfig() {
		name, value, ok := strings.Cut(kv, envSep)
		if !ok {
			continue
		}
		if err := os.Setenv(name, value); err != nil {
			return jq.Sealed("set "+name, err)
		}
	}
	return nil
}

// gitLeak reports whether an inherited variable would retarget git away from
// the fixture or re-pin its configuration.
func gitLeak(name string) bool {
	return strings.HasPrefix(name, "GIT_CONFIG_") ||
		slices.Contains(gitLocation, name) ||
		slices.Contains(gitClock, name)
}

// Main isolates the process and runs m, so a package's TestMain is one line.
func Main(m *testing.M) int {
	if err := Isolate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return m.Run()
}

// Base returns the child environment: the parent's variables minus harness
// leaks, secret-shaped names, and anything extra overrides, then the pinned
// git config, then extra itself (last wins, so a test can override any key).
// Callers append HOME/PATH/TMPDIR and any per-run values.
func Base(extra map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		if entry, ok := keep(kv, extra); ok {
			env = append(env, entry)
		}
	}
	env = append(env, GitConfig()...)
	for k, v := range extra {
		env = append(env, k+envSep+v)
	}
	return env
}

// keep returns the parent entry when it survives into the child.
func keep(kv string, extra map[string]string) (string, bool) {
	name, _, ok := strings.Cut(kv, envSep)
	if !ok {
		return kv, true
	}
	if _, override := extra[name]; override {
		return "", false
	}
	if dropName(name) || envsecret.IsSecret(name) {
		return "", false
	}
	return kv, true
}

// dropName reports whether a parent variable is a harness leak or one the
// pinned environment re-emits.
func dropName(name string) bool {
	return slices.Contains(dropNames, name) ||
		gitPinned(name) ||
		slices.ContainsFunc(dropPrefixes, func(prefix string) bool { return strings.HasPrefix(name, prefix) })
}

// gitPinned reports whether name belongs to the environment the harness always
// sets itself.
func gitPinned(name string) bool {
	return gitLeak(name) || slices.ContainsFunc(gitIdentity, func(kv [2]string) bool { return kv[0] == name })
}
