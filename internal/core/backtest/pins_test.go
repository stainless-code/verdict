package backtest

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// pinRecorder records every command the miner spawns.
type pinRecorder struct {
	inner core.Commander
	calls []recordedCall
}

// recordedCall is one spawned git command.
type recordedCall struct {
	env  map[string]string
	argv []string
}

// Run records the command and delegates.
func (r *pinRecorder) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	env := make(map[string]string, len(cmd.Env))
	for _, pair := range cmd.Env {
		name, value, ok := strings.Cut(pair, "=")
		if ok {
			env[name] = value
		}
	}
	r.calls = append(r.calls, recordedCall{env: env, argv: slices.Clone(cmd.Argv)})
	return r.inner.Run(ctx, cmd)
}

// trustReadArgvs are the two protected-scope safe.directory reads, in order.
// safe.directory is honored from the command line and these two scopes only,
// so the read cannot be widened to the repository's own config.
var trustReadArgvs = [][]string{
	{"git", "config", "--system", "--null", "--get-all", "safe.directory"},
	{"git", "config", "--global", "--null", "--get-all", "safe.directory"},
}

// TestMinePinsEveryGitCall pins the mining git contract: every mining call
// carries the command-scope pins, diff calls carry the diff flags, and the
// child environment is rebuilt without the parent's git redirects. The two
// safe.directory reads run first, with the user's config scopes still live.
// A dropped pin fails here even where an explicit flag would mask its effect.
func TestMinePinsEveryGitCall(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	home := t.TempDir()
	global := writeGitConfig(t, "")
	rec := &pinRecorder{inner: coreexec.Runner{MissingTool: true}}
	_, ref := Run(t.Context(), &Request{
		Runtime: &core.Runtime{
			Commander: rec,
			Env: userEnv(t, map[string]string{
				"HOME": home, "MINE_MARKER": "kept", "GIT_CONFIG_NOSYSTEM": "1",
				"GIT_DIR": "/evil", "GIT_WORK_TREE": "/evil", "GIT_INDEX_FILE": "/evil",
				"GIT_OBJECT_DIRECTORY": "/evil", "GIT_ALTERNATE_OBJECT_DIRECTORIES": "/evil",
				"GIT_CONFIG_GLOBAL": global, "GIT_CONFIG_COUNT": "1",
				"GIT_CONFIG_KEY_0": "color.ui", "GIT_CONFIG_VALUE_0": "always",
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
		name := "git " + strings.Join(call.argv, " ")
		if isTrustRead(call.argv) {
			reads = append(reads, call.argv)
			checkUserEnv(t, name, call.env, home, global)
			continue
		}
		mined++
		checkPinArgv(t, name, call.argv)
		checkPinEnv(t, name, call.env, home)
	}
	if !slices.EqualFunc(reads, trustReadArgvs, slices.Equal) {
		t.Errorf("trust reads = %v; want %v", reads, trustReadArgvs)
	}
	if mined == 0 {
		t.Fatal("no mining call was recorded; the guard would pass vacuously")
	}
}

// isTrustRead reports the safe.directory reads, the only git calls that run
// with the user's config scopes.
func isTrustRead(argv []string) bool {
	return len(argv) > 1 && argv[1] == "config"
}

// checkPinArgv proves one command carries the pin prefix and, for a show or
// diff, the diff-flag prefix.
func checkPinArgv(t *testing.T, name string, argv []string) {
	t.Helper()
	wantPins := []string{
		"--literal-pathspecs",
		"-c", "core.quotepath=false",
		"-c", "color.ui=false",
		"-c", "log.showSignature=false",
	}
	if len(argv) < 3 || argv[0] != "git" || argv[1] != "-C" || argv[2] == "" {
		t.Errorf("%s: want `git -C <dir> ...`", name)
		return
	}
	rest := argv[3:]
	if len(rest) <= len(wantPins) || !slices.Equal(rest[:len(wantPins)], wantPins) {
		t.Errorf("%s: pins = %v; want prefix %v", name, rest, wantPins)
		return
	}
	args := rest[len(wantPins):]
	if args[0] == "show" || args[0] == "diff" {
		checkDiffPins(t, name, args)
	}
}

// checkDiffPins proves one diff-family call carries the diff-flag prefix.
func checkDiffPins(t *testing.T, name string, args []string) {
	t.Helper()
	wantDiff := append([]string{args[0]}, diffPins...)
	if len(args) < len(wantDiff) || !slices.Equal(args[:len(wantDiff)], wantDiff) {
		t.Errorf("%s: diff flags = %v; want prefix %v", name, args, wantDiff)
	}
}

// checkPinEnv proves one command's environment is the rebuilt one: pins
// present, the parent's git redirects absent, and the caller's benign values
// kept.
func checkPinEnv(t *testing.T, name string, env map[string]string, home string) {
	t.Helper()
	wantEnv := map[string]string{
		"HOME": home, "MINE_MARKER": "kept", "LC_ALL": "C", "GIT_PAGER": "cat",
		"GIT_OPTIONAL_LOCKS": "0", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_TERMINAL_PROMPT": "0",
		"GIT_NO_REPLACE_OBJECTS": "1",
	}
	for key, want := range wantEnv {
		if got := env[key]; got != want {
			t.Errorf("%s: env %s = %q; want %q", name, key, got, want)
		}
	}
	checkNoRedirects(t, name, env)
}

// checkNoRedirects proves none of git's location or config escape hatches
// reached the child.
func checkNoRedirects(t *testing.T, name string, env map[string]string) {
	t.Helper()
	for _, key := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0",
		"GIT_CONFIG_VALUE_0",
	} {
		if _, bad := env[key]; bad {
			t.Errorf("%s: inherited %s from the parent environment", name, key)
		}
	}
	for key := range env {
		if strings.HasPrefix(key, "GIT_CONFIG_") &&
			key != "GIT_CONFIG_GLOBAL" && key != "GIT_CONFIG_NOSYSTEM" {
			t.Errorf("%s: inherited %s from the parent environment", name, key)
		}
	}
}

// checkUserEnv proves one safe.directory read runs with the user's config
// scopes and overlays live — the test's own files, not the host's — git's
// location redirects dropped, and the locale and pager pins in place.
func checkUserEnv(t *testing.T, name string, env map[string]string, home, global string) {
	t.Helper()
	wantEnv := map[string]string{
		"HOME": home, "LC_ALL": "C", "GIT_PAGER": "cat",
		"GIT_OPTIONAL_LOCKS": "0", "GIT_TERMINAL_PROMPT": "0",
		"GIT_NO_REPLACE_OBJECTS": "1", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL": global, "GIT_CONFIG_SYSTEM": os.DevNull,
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "color.ui",
		"GIT_CONFIG_VALUE_0": "always",
	}
	for key, want := range wantEnv {
		if got := env[key]; got != want {
			t.Errorf("%s: env %s = %q; want %q", name, key, got, want)
		}
	}
	for _, key := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	} {
		if _, bad := env[key]; bad {
			t.Errorf("%s: inherited %s from the parent environment", name, key)
		}
	}
}
