package fetchcore

import (
	"bytes"
	"context"
	"os"
	"testing"
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
// never deletes a bundle that existed before the run: the prior bundle's
// files stay where they were.
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
