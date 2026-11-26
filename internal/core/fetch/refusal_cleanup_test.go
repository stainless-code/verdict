package fetchcore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// unmarkedConfig is a repository configuration without its format marker:
// the parser refuses it after fetch-local already wrote controls and the diff.
const unmarkedConfig = `{"defaults":{"chunk":false}}`

// unmarkedConfigRefusal is the refusal it earns, naming the file it read.
const unmarkedConfigRefusal = `.verdict/config.json: schema_version: missing format marker, want the string "1"`

// TestRunLocalBadConfigRefusalNamesFileAndLeavesNoBundle proves a config
// parse refusal names the file it read and removes the bundle directory the
// refused run created, so no half-written local-<name> is left behind.
func TestRunLocalBadConfigRefusalNamesFileAndLeavesNoBundle(t *testing.T) {
	t.Chdir(t.TempDir())
	_, _, err := runLocalStagedDiff(t, capTestDiff, nil,
		map[string]string{".verdict/config.json": unmarkedConfig}, false)
	if ref := requireRefusal(t, err, "bad-config"); ref.Message != unmarkedConfigRefusal {
		t.Errorf("message = %q, want %q", ref.Message, unmarkedConfigRefusal)
	}
	if _, err := os.Lstat(".verdict/state/local-mine"); !os.IsNotExist(err) {
		t.Errorf("refused run left .verdict/state/local-mine behind (stat err %v)", err)
	}
}

// TestRunLocalBadConfigRefusalKeepsExistingBundle proves a refused re-fetch
// never deletes a bundle directory that existed before the run: the prior
// bundle's files stay, and so do the files the refused run wrote before it
// refused (controls.json here), exactly as before this cleanup existed.
func TestRunLocalBadConfigRefusalKeepsExistingBundle(t *testing.T) {
	t.Chdir(t.TempDir())
	const prior = ".verdict/state/local-mine/findings.json"
	if err := os.MkdirAll(".verdict/state/local-mine", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prior, []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := runLocalStagedDiff(t, capTestDiff, nil,
		map[string]string{".verdict/config.json": unmarkedConfig}, false)
	if ref := requireRefusal(t, err, "bad-config"); ref.Message != unmarkedConfigRefusal {
		t.Errorf("message = %q, want %q", ref.Message, unmarkedConfigRefusal)
	}
	raw, rerr := os.ReadFile(prior)
	if rerr != nil || string(raw) != "[]\n" {
		t.Errorf("refused re-fetch touched the prior bundle: %q, %v", raw, rerr)
	}
	if _, serr := os.Stat(".verdict/state/local-mine/controls.json"); serr != nil {
		t.Errorf("the refused re-fetch's own controls.json is gone: %v", serr)
	}
}

// TestRunGitHubBadConfigRefusalLeavesNoBundle proves the PR fetch matches
// fetch-local: the refusal names the config file and the bundle directory the
// refused run created is gone.
func TestRunGitHubBadConfigRefusalLeavesNoBundle(t *testing.T) {
	t.Chdir(t.TempDir())
	cmdr := fetchHappyCommander(t)
	scriptBasePolicy(cmdr, fetchTestBase, unmarkedConfig)
	var human, diag bytes.Buffer
	var streamed []string
	_, err := Run(context.Background(), fetchTestReq(cmdr, &human, &diag, &streamed))
	if ref := requireRefusal(t, err, "bad-config"); ref.Message != unmarkedConfigRefusal {
		t.Errorf("message = %q, want %q", ref.Message, unmarkedConfigRefusal)
	}
	if _, err := os.Lstat(".verdict/state/7"); !os.IsNotExist(err) {
		t.Errorf("refused fetch left .verdict/state/7 behind (stat err %v)", err)
	}
}

// hookCommander runs hook once, just before the first spawn whose argv
// contains match: the seam where another fetch of the same bundle name acts
// in the middle of this run.
type hookCommander struct {
	*scriptCommander
	hook  func()
	match string
	fired bool
}

func (h *hookCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if !h.fired && slices.Contains(cmd.Argv, h.match) {
		h.fired = true
		h.hook()
	}
	return h.scriptCommander.Run(ctx, cmd)
}

// runLocalHooked runs the unmarked-config fetch-local with hook firing on the
// first spawn that names match.
func runLocalHooked(t *testing.T, match string, hook func()) error {
	t.Helper()
	cmdr := stagedLocalCommander(t, capTestDiff)
	scriptBasePolicy(cmdr, localTestHead, unmarkedConfig)
	var human, diag bytes.Buffer
	var streamed []string
	req := localScriptReq(cmdr, &human, &diag, &streamed, nil, LocalFlags{Staged: true})
	req.Commander = &hookCommander{scriptCommander: cmdr, match: match, hook: hook}
	_, err := RunLocal(context.Background(), req)
	return err
}

// TestRunLocalRefusalKeepsABundleAnotherRunCreated proves the cleanup only
// removes what this run's own create made: a concurrent run that creates the
// bundle after this one resolved the name, but before this one creates it,
// keeps its bundle when this run refuses.
func TestRunLocalRefusalKeepsABundleAnotherRunCreated(t *testing.T) {
	t.Chdir(t.TempDir())
	const theirs = ".verdict/state/local-mine/source.json"
	err := runLocalHooked(t, "--is-inside-work-tree", func() {
		if err := os.MkdirAll(".verdict/state/local-mine", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(theirs, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if ref := requireRefusal(t, err, "bad-config"); ref.Message != unmarkedConfigRefusal {
		t.Errorf("message = %q, want %q", ref.Message, unmarkedConfigRefusal)
	}
	if raw, rerr := os.ReadFile(theirs); rerr != nil || string(raw) != "{}\n" {
		t.Errorf("refused run removed the other run's bundle: %q, %v", raw, rerr)
	}
}

// TestRunLocalRefusalWarnsWhenTheBundleCannotBeRemoved proves a leaf swapped
// for a symlink after the create is never removed through, and the refusal
// says the cleanup failed instead of staying silent.
func TestRunLocalRefusalWarnsWhenTheBundleCannotBeRemoved(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	target := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	err := runLocalHooked(t, "diff", func() {
		if err := os.RemoveAll(".verdict/state/local-mine"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, ".verdict/state/local-mine"); err != nil {
			t.Skipf("this platform cannot create symlinks here: %v", err)
		}
	})
	ref := requireRefusal(t, err, "bad-config")
	want := "could not remove the refused run's bundle .verdict/state/local-mine: " +
		filepath.Join(".verdict", "state", "local-mine") + " is a symlink: refusing to remove through it"
	if !slices.Contains(ref.Warnings, want) {
		t.Errorf("warnings = %q, want %q", ref.Warnings, want)
	}
	if _, lerr := os.Lstat(".verdict/state/local-mine"); lerr != nil {
		t.Errorf("the link was removed: %v", lerr)
	}
}

// TestRunGitHubRefusalAfterCreateLeavesNoBundle proves a refusal on the step
// right after the bundle leaf is created (the state home's ignore layer is a
// symlink) still removes the leaf: the run records the bundle the moment it
// creates it.
func TestRunGitHubRefusalAfterCreateLeavesNoBundle(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(".verdict/state", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignore-target"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "ignore-target"), ".verdict/state/.gitignore"); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	cmdr := fetchHappyCommander(t)
	var human, diag bytes.Buffer
	var streamed []string
	_, err := Run(context.Background(), fetchTestReq(cmdr, &human, &diag, &streamed))
	if _, ok := errors.AsType[*core.RefusalError](err); !ok {
		t.Fatalf("err = %v, want a typed refusal", err)
	}
	if _, err := os.Lstat(".verdict/state/7"); !os.IsNotExist(err) {
		t.Errorf("refused fetch left .verdict/state/7 behind (stat err %v)", err)
	}
}
