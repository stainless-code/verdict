package fetchcore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestRunLocalRefusesSymlinkedBundleDir proves fetch-local refuses a bundle
// directory that is a symlink before the first write: controls.json, the diff,
// and its sidecar would otherwise land in the link's target, outside the bundle
// home.
func TestRunLocalRefusesSymlinkedBundleDir(t *testing.T) {
	target := symlinkedBundleTarget(t, "local-mine")
	cmdr := stagedLocalCommander(t, cleanStagedDiff)
	var human, diag bytes.Buffer
	var streamed []string
	_, err := RunLocal(context.Background(),
		localScriptReq(cmdr, &human, &diag, &streamed, nil, core.FetchLocalFlags{Staged: true}))
	assertSymlinkedBundleRefused(t, err, "local-mine", target)
}

// TestRunGitHubRefusesSymlinkedBundleDir proves the same refusal on the PR
// fetch: the bundle directory is checked before the run's first write.
func TestRunGitHubRefusesSymlinkedBundleDir(t *testing.T) {
	target := symlinkedBundleTarget(t, "7")
	cmdr := fetchHappyCommander(t)
	var human, diag bytes.Buffer
	var streamed []string
	_, err := Run(context.Background(), fetchTestReq(cmdr, &human, &diag, &streamed))
	assertSymlinkedBundleRefused(t, err, "7", target)
}

// symlinkedBundleTarget chdirs into a fresh root whose state home carries name
// as a symlink to a sibling directory, returning the link target a bundle write
// would land in.
func symlinkedBundleTarget(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	state := filepath.Join(root, ".verdict", "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(state, name)); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	return target
}

// assertSymlinkedBundleRefused pins the typed refusal and the untouched target.
func assertSymlinkedBundleRefused(t *testing.T, err error, name, target string) {
	t.Helper()
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok {
		t.Fatalf("run on a symlinked bundle dir = %v, want a typed refusal", err)
	}
	want := filepath.Join(".verdict", "state", name) + " is a symlink: refusing to write through it"
	if ref.Code != "bad-config" || ref.Message != want {
		t.Fatalf("refusal = %s: %q; want bad-config: %q", ref.Code, ref.Message, want)
	}
	if entries, rerr := os.ReadDir(target); rerr != nil || len(entries) != 0 {
		t.Fatalf("link target gained files: %v, %v", entries, rerr)
	}
}
