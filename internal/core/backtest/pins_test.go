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
	argv []string
	env  map[string]string
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
	r.calls = append(r.calls, recordedCall{argv: slices.Clone(cmd.Argv), env: env})
	return r.inner.Run(ctx, cmd)
}

// TestMinePinsEveryGitCall pins the mining git contract: every call carries
// the command-scope pins, diff calls carry the diff flags, and the child
// environment is rebuilt without the parent's git redirects. A dropped pin
// fails here even where an explicit flag would mask its effect.
func TestMinePinsEveryGitCall(t *testing.T) {
	b, _ := newMergedFixRepo(t)
	home := t.TempDir()
	rec := &pinRecorder{inner: coreexec.Runner{MissingTool: true}}
	_, ref := Run(t.Context(), &Request{
		Runtime: &core.Runtime{
			Commander: rec,
			Env: map[string]string{
				"HOME": home, "MINE_MARKER": "kept",
				"GIT_DIR": "/evil", "GIT_WORK_TREE": "/evil", "GIT_INDEX_FILE": "/evil",
				"GIT_OBJECT_DIRECTORY": "/evil", "GIT_ALTERNATE_OBJECT_DIRECTORIES": "/evil",
				"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "color.ui", "GIT_CONFIG_VALUE_0": "always",
			},
		},
		RepoRoot: b.r.dir, Mode: modeMine, Since: b.base, Until: "HEAD",
	})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	if len(rec.calls) == 0 {
		t.Fatal("no git call was recorded; the guard would pass vacuously")
	}
	wantPins := []string{
		"--literal-pathspecs",
		"-c", "core.quotepath=false",
		"-c", "color.ui=false",
		"-c", "log.showSignature=false",
	}
	wantEnv := map[string]string{
		"HOME": home, "MINE_MARKER": "kept", "LC_ALL": "C", "GIT_PAGER": "cat",
		"GIT_OPTIONAL_LOCKS": "0", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_TERMINAL_PROMPT": "0",
		"GIT_NO_REPLACE_OBJECTS": "1",
	}
	forbidden := []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0",
		"GIT_CONFIG_VALUE_0",
	}
	for _, call := range rec.calls {
		name := "git " + strings.Join(call.argv, " ")
		if len(call.argv) < 3 || call.argv[0] != "git" || call.argv[1] != "-C" || call.argv[2] == "" {
			t.Errorf("%s: want `git -C <dir> ...`", name)
			continue
		}
		rest := call.argv[3:]
		if len(rest) <= len(wantPins) || !slices.Equal(rest[:len(wantPins)], wantPins) {
			t.Errorf("%s: pins = %v; want prefix %v", name, rest, wantPins)
			continue
		}
		args := rest[len(wantPins):]
		if args[0] == "show" || args[0] == "diff" {
			wantDiff := append([]string{args[0]}, diffPins...)
			if len(args) < len(wantDiff) || !slices.Equal(args[:len(wantDiff)], wantDiff) {
				t.Errorf("%s: diff flags = %v; want prefix %v", name, args, wantDiff)
			}
		}
		for key, want := range wantEnv {
			if got := call.env[key]; got != want {
				t.Errorf("%s: env %s = %q; want %q", name, key, got, want)
			}
		}
		for _, key := range forbidden {
			if _, bad := call.env[key]; bad {
				t.Errorf("%s: inherited %s from the parent environment", name, key)
			}
		}
		for key := range call.env {
			if strings.HasPrefix(key, "GIT_CONFIG_") &&
				key != "GIT_CONFIG_GLOBAL" && key != "GIT_CONFIG_NOSYSTEM" {
				t.Errorf("%s: inherited %s from the parent environment", name, key)
			}
		}
	}
}
