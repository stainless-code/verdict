package panel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestPanelArtifacts pins artifact writes and flat installs.
func TestPanelArtifacts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "review repo")
	dir := filepath.Join(root, ".verdict/state/local-test")
	for name, body := range map[string]string{
		"panel": "synth\na\nb\n", "source.json": `{"head_sha":"head"}`,
		"review.json":   `{"schema_version":"1","body":"review","comments":[{"path":"src/db.go","line":42,"body":"a reproducible crash"}]}`,
		"findings.json": "untouched store\n", "post.json": "untouched receipt\n",
	} {
		writeFixture(t, filepath.Join(dir, name), body)
	}
	writeFixture(t, filepath.Join(root, "prompts/verifier.md"), "trusted verifier prompt")
	var out bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &out, JSONMode: true, Effective: core.Defaults()}
	req := readcommon.Request{Root: &root, RepoRoot: root, Repo: "acme/app", Pr: "local-test", Runtime: &core.Runtime{Now: func() time.Time { return time.Unix(0, 0) }}}
	if code := Run(t.Context(), r, nil, req); code != 0 {
		t.Fatalf("exit=%d: %s", code, out.String())
	}
	first, err := os.ReadFile(filepath.Join(dir, "verification/dispatch.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(first), "trusted verifier prompt\n") {
		t.Fatalf("missing trusted prompt: %s", first)
	}
	out.Reset()
	if code := Run(t.Context(), r, nil, req); code != 0 {
		t.Fatalf("rewrite exit=%d: %s", code, out.String())
	}
	second, err := os.ReadFile(filepath.Join(dir, "verification/dispatch.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("dispatch appended instead of rewriting")
	}
	assertFile(t, filepath.Join(dir, "panel"), "synth\na\nb\n")
	assertFile(t, filepath.Join(dir, "findings.json"), "untouched store\n")
	assertFile(t, filepath.Join(dir, "post.json"), "untouched receipt\n")
}

// TestPanelRefusesSymlink keeps planner writes inside the bundle.
func TestPanelRefusesSymlink(t *testing.T) {
	dir, target := t.TempDir(), t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "verification")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := writePlan(&planned{dir: dir, plan: jq.NewObj(), dispatch: "dispatch"})
	if err == nil {
		t.Fatal("symlink accepted")
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("planner wrote outside bundle")
	}
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Fatalf("%s=%q, want %q", path, raw, want)
	}
}
