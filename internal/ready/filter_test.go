package ready

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	"github.com/stainless-code/verdict/internal/testenv"
)

func TestParseFilterFlag(t *testing.T) {
	q := &readycore.Request{}
	if err := parseFlags([]string{"--filter", "--check"}, q); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !q.Filter || !q.Check {
		t.Fatalf("Filter=%v Check=%v, want both true", q.Filter, q.Check)
	}
}

// TestReadyFilterCheckKeepsTypedExit proves the --check gate cannot recode a
// typed trigger miss as a tool failure. The fixture fetch resolves a real
// base snapshot, so the refusal is the whole gate, not a stubbed code.
func TestReadyFilterCheckKeepsTypedExit(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	mkdir(t, filepath.Join(root, ".verdict"))
	write(t, filepath.Join(root, ".verdict", "config.json"),
		`{"schema_version":"1","trigger":{"paths_include":["protected/**"]}}`)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		cmd.Env = testenv.Base(nil)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", ".verdict/config.json")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")

	bundle := filepath.Join(root, ".verdict", "state", "5201")
	mkdir(t, bundle)
	write(t, filepath.Join(bundle, "PR.json"),
		`{"provider":"github","title":"fix: retry","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"author":{"login":"ana"},"baseRefName":"main","files":[{"path":"docs/README.md"}]}`)
	if err := config.WriteRunState(bundle, &config.RunState{BaseSHA: base, Trusted: true}); err != nil {
		t.Fatal(err)
	}
	if _, ref := controls.Resolve(t.Context(), coreexec.Runner{}, &controls.Request{
		BundleDir: bundle, Root: root, BaseSHA: base,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}

	var out bytes.Buffer
	r := &cli.Runner{
		Out: &out, Human: &out, Diag: &out, Now: time.Now, JSONMode: true,
		Cmd: "ready", Repo: "acme/app", Pr: "5201",
	}
	code := Run(t.Context(), r, []string{"--filter", "--check"},
		Request{RepoRoot: root, Repo: "acme/app", Pr: "5201"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 for filtered-out under --check: %s", code, out.String())
	}
	if !strings.Contains(out.String(), `"code": "filtered-out"`) {
		t.Errorf("envelope misses the typed refusal: %s", out.String())
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
