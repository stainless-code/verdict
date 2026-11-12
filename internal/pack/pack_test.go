package pack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
)

// TestRunWarnsAboutStrayFileUnderRepoPacks proves the CLI surfaces the pack
// list warning on the diagnostic stream and still exits 0 with the built-in
// row on stdout.
func TestRunWarnsAboutStrayFileUnderRepoPacks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	packs := filepath.Join(root, ".verdict", "packs")
	if err := os.MkdirAll(packs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packs, "README.md"), []byte("docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, diag strings.Builder
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag}
	code := Run(t.Context(), r, []string{"list"}, Request{
		Runtime:  &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}},
		RepoRoot: root,
	})
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "warn: .verdict/packs/README.md is not a pack directory — ignored\n"
	if diag.String() != want {
		t.Fatalf("stderr = %q, want %q", diag.String(), want)
	}
	if got := out.String(); !strings.Contains(got, "verdict/builtin") {
		t.Fatalf("stdout = %q, want the built-in row", got)
	}
}

// TestRunRefusalCarriesTheStrayFileWarning proves a refusal after the repo scan
// keeps the warning it collected: the envelope reports both the stray file and
// the layering conflict instead of dropping the warning when the run refused.
func TestRunRefusalCarriesTheStrayFileWarning(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePackFile(t, root, ".verdict/packs/README.md", "not a pack\n")
	for _, name := range []string{"alpha", "beta"} {
		writePackFile(t, root, ".verdict/packs/"+name+"/pack.json",
			`{"schema_version":"1","name":"acme/`+name+`","version":"1.0.0","lenses":[{"id":"security"}]}`)
		writePackFile(t, root, ".verdict/packs/"+name+"/lenses/security.md", name+" body\n")
	}
	var out, diag strings.Builder
	r := &cli.Runner{
		Out: &out, Human: &out, Diag: &diag, JSONMode: true, Cmd: "pack",
		Now: func() time.Time { return time.Unix(0, 0) },
	}
	code := Run(t.Context(), r, []string{"list"}, Request{
		Runtime:  &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}},
		RepoRoot: root,
	})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d", code, cli.ExitRefused)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out.String()), &env); err != nil {
		t.Fatalf("decode envelope %q: %v", out.String(), err)
	}
	want := ".verdict/packs/README.md is not a pack directory — ignored"
	if len(env.Warnings) != 1 || env.Warnings[0] != want {
		t.Fatalf("warnings = %q, want [%q]", env.Warnings, want)
	}
	if env.Error.Code != "bad-pack" {
		t.Fatalf("error code = %q, want bad-pack", env.Error.Code)
	}
}

// TestRunPackTestSkipExitsNonzero pins the skip exit code and JSON summary.
func TestRunPackTestSkipExitsNonzero(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	writePackFile(t, root, "pack.json", `{"schema_version":"1","name":"acme/test","version":"1.0.0",`+
		`"severity":{"s":"important"},"lenses":[{"id":"test"}],`+
		`"rules":[{"id":"rule-a","severity":"s","globs":["**/*.go"],"tools":["pack-test-tool-missing"]}]}`)
	writePackFile(t, root, "lenses/test.md", "lens body\n")
	writePackFile(t, root, "rules/rule-a.md", "rule body\n")
	writePackFile(t, root, "evals/case/diff.patch",
		"diff --git a/src/a.go b/src/a.go\n--- a/src/a.go\n+++ b/src/a.go\n@@ -1 +1 @@\n-old\n+new\n")
	writePackFile(t, root, "evals/case/findings.json", "[]")
	writePackFile(t, root, "evals/case/expect.json", `{"rules":[]}`)
	var out, diag strings.Builder
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, JSONMode: true, Cmd: "pack"}
	code := Run(t.Context(), r, []string{"test", "--dir", root}, Request{
		Runtime: &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}}, RepoRoot: root,
	})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d; output=%s", code, cli.ExitRefused, out.String())
	}
	if !strings.Contains(out.String(), `"status": "SKIP"`) || !strings.Contains(out.String(), `"skip": 1`) {
		t.Fatalf("envelope = %q; want SKIP and a skipped count", out.String())
	}
}

// writePackFile writes one fixture file under root, creating parents.
func writePackFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
