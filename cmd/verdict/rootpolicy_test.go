package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	committedMaxBytes = 123456
	dirtyMaxBytes     = 999999
	committedPanel    = "root-committed-lens"
	dirtyPanel        = "dirty-worktree-lens"
)

// gitIn runs one git command in dir with a fixed identity and no system
// config, so the fixture is reproducible on any machine.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeIn writes one fixture file, failing the test if it cannot.
func writeIn(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	committedPolicy = `{"schema_version":"1","defaults":{"max_bytes":123456,"panel":["root-committed-lens"]}}` + "\n"
	dirtyPolicy     = `{"schema_version":"1","defaults":{"max_bytes":999999,"panel":["dirty-worktree-lens"]}}` + "\n"
)

// commitRootPolicy seeds a checkout whose committed root config carries the
// distinctive policy and returns that commit's SHA, so callers compare the
// captured base against the commit rather than the working tree.
func commitRootPolicy(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeIn(t, filepath.Join(root, ".verdict", "config.json"), committedPolicy)
	writeIn(t, filepath.Join(root, "app.txt"), "base\n")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-qm", "base with committed root policy")
	return gitIn(t, root, "rev-parse", "HEAD")
}

// dirtyRootPolicy overwrites the root config and a tracked file after the
// base commit: the reviewed base is the commit, not the working tree.
func dirtyRootPolicy(t *testing.T, root string) {
	t.Helper()
	writeIn(t, filepath.Join(root, ".verdict", "config.json"), dirtyPolicy)
	writeIn(t, filepath.Join(root, "app.txt"), "base\nchange\n")
}

// gitRepo seeds a real checkout with a committed root policy, a dirty working
// tree, and a nested subdirectory to invoke the verbs from.
func gitRepo(t *testing.T) (root, sub, baseSHA string) {
	t.Helper()
	root = t.TempDir()
	gitIn(t, root, "init", "-q")
	baseSHA = commitRootPolicy(t, root)
	dirtyRootPolicy(t, root)
	sub = filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, sub, baseSHA
}

// assertRunJSONWithCommittedPolicy reads the captured bundle's run.json and
// checks the base commit, trust flag, and both committed distinct values.
func assertRunJSONWithCommittedPolicy(t *testing.T, bundle, baseSHA string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(bundle, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		BaseSHA  string `json:"base_sha"`
		Resolved struct {
			Defaults struct {
				Values struct {
					Panel    []string `json:"panel"`
					MaxBytes int64    `json:"max_bytes"`
				} `json:"values"`
			} `json:"defaults"`
		} `json:"resolved"`
		Trusted bool `json:"trusted"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("run.json: %v\n%s", err, raw)
	}
	if state.BaseSHA != baseSHA {
		t.Errorf("captured base_sha = %q, want the committed %q", state.BaseSHA, baseSHA)
	}
	if !state.Trusted {
		t.Error("captured base policy is not trusted")
	}
	if state.Resolved.Defaults.Values.MaxBytes != committedMaxBytes {
		t.Errorf("captured max_bytes = %d, want the committed %d", state.Resolved.Defaults.Values.MaxBytes, committedMaxBytes)
	}
	if values := state.Resolved.Defaults.Values.Panel; len(values) != 1 || values[0] != committedPanel {
		t.Errorf("captured panel = %v, want [%s]", values, committedPanel)
	}
}

// assertBundleAtRoot checks the artifact set landed at the Git root and that
// nothing was written under the invocation directory.
func assertBundleAtRoot(t *testing.T, sub, bundle string) {
	t.Helper()
	for _, name := range []string{"run.json", "full.diff", "source.json"} {
		if _, err := os.Stat(filepath.Join(bundle, name)); err != nil {
			t.Fatalf("bundle missing %s at the repository root: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sub, ".verdict")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat sub/.verdict = %v, want fs.ErrNotExist", err)
	}
}

// TestSubdirFetchLocalBindsCommittedRootPolicy pins the successful-subdir
// policy binding end to end: from a nested checkout directory, the real
// fetch-local verb anchors every artifact at the Git root and captures the
// committed base policy — not the dirty working-tree config and not the cwd.
func TestSubdirFetchLocalBindsCommittedRootPolicy(t *testing.T) {
	hermeticEnv(t)
	root, sub, baseSHA := gitRepo(t)
	t.Chdir(sub)

	if got := runOne(t, "fetch-local", "acme/app", "demo"); got.code != 0 {
		t.Fatalf("fetch-local from a subdirectory = %+v; want success", got)
	}

	bundle := filepath.Join(root, ".verdict", "state", "local-demo")
	assertBundleAtRoot(t, sub, bundle)
	assertRunJSONWithCommittedPolicy(t, bundle, baseSHA)

	body, err := os.ReadFile(filepath.Join(bundle, "full.diff"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "change") {
		t.Errorf("full.diff misses the dirty patch: %q", body)
	}
}

// explainFrom runs config --explain and asserts the reported values are the
// committed ones, never the dirty working-tree policy.
func explainFrom(t *testing.T, where string) invocation {
	t.Helper()
	got := runOne(t, "config", "acme/app", "demo", "--explain")
	if got.code != 0 {
		t.Fatalf("config --explain from the %s = %+v; want success", where, got)
	}
	for _, want := range []string{"123456", committedPanel} {
		if !strings.Contains(got.out, want) {
			t.Errorf("explain from the %s misses %q:\n%s", where, want, got.out)
		}
	}
	for _, unwanted := range []string{dirtyPanel, "999999"} {
		if strings.Contains(got.out, unwanted) {
			t.Errorf("explain from the %s reports the dirty working-tree config (%s):\n%s", where, unwanted, got.out)
		}
	}
	return got
}

// TestSubdirConfigExplainReadsRootCapturedPolicy pins the read side: config
// --explain from the same nested directory against the captured bundle
// reports the committed distinctive values, and the root agrees.
func TestSubdirConfigExplainReadsRootCapturedPolicy(t *testing.T) {
	hermeticEnv(t)
	root, sub, _ := gitRepo(t)
	t.Chdir(sub)
	if got := runOne(t, "fetch-local", "acme/app", "demo"); got.code != 0 {
		t.Fatalf("fetch-local from a subdirectory = %+v; want success", got)
	}

	fromSub := explainFrom(t, "subdir")

	t.Chdir(root)
	if fromRoot := runOne(t, "config", "acme/app", "demo", "--explain"); fromRoot != fromSub {
		t.Errorf("root explain = %+v; subdir explain = %+v; want agreement", fromRoot, fromSub)
	}
}
