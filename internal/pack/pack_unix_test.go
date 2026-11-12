//go:build unix

package pack

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
)

// TestRunAddReportsBackupCleanupAsWarning proves a re-add whose old tree
// cannot be swept exits 0, prints the updated row, and names the leftover path
// in the envelope warning and on the diagnostic stream.
func TestRunAddReportsBackupCleanupAsWarning(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writePackFile(t, root, "src-packs/lenses/pack.json",
		`{"schema_version":"1","name":"acme/lenses","version":"1.0.0","lenses":[{"id":"perf"}]}`)
	writePackFile(t, root, "src-packs/lenses/lenses/perf.md", "perf body\n")
	req := Request{
		Runtime:  &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}},
		RepoRoot: root,
	}
	var out, diag strings.Builder
	first := &cli.Runner{Out: &out, Human: &out, Diag: &diag}
	if code := Run(t.Context(), first, []string{"add", "src-packs/lenses"}, req); code != cli.ExitOK {
		t.Fatalf("first add exit = %d, want 0 (stderr %q)", code, diag.String())
	}
	if err := os.Chmod(filepath.Join(root, ".verdict", "packs", "lenses", "lenses"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { makeWritable(t, filepath.Join(root, ".verdict", "packs")) })
	writePackFile(t, root, "src-packs/lenses/lenses/perf.md", "perf body v2\n")
	out.Reset()
	diag.Reset()
	second := &cli.Runner{
		Out: &out, Human: &out, Diag: &diag, JSONMode: true, Cmd: "pack",
		Now: func() time.Time { return time.Unix(0, 0) },
	}
	if code := Run(t.Context(), second, []string{"add", "src-packs/lenses"}, req); code != cli.ExitOK {
		t.Fatalf("second add exit = %d, want 0 (stderr %q)", code, diag.String())
	}
	assertAddWarning(t, out.String(), diag.String())
}

// assertAddWarning proves the envelope reports the re-add and the leftover
// path, and the diagnostic stream carries the same warning.
func assertAddWarning(t *testing.T, out, diag string) {
	t.Helper()
	var env struct {
		Warnings []string `json:"warnings"`
		Data     struct {
			Replaced bool `json:"replaced"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("decode envelope %q: %v", out, err)
	}
	if !env.Data.Replaced {
		t.Fatal("envelope did not report replaced")
	}
	if len(env.Warnings) != 1 || !strings.Contains(env.Warnings[0], ".verdict/packs/.lenses.old-") {
		t.Fatalf("warnings = %q, want the leftover path", env.Warnings)
	}
	if !strings.Contains(diag, "warn: "+env.Warnings[0]) {
		t.Fatalf("stderr = %q, want the warn line naming %q", diag, env.Warnings[0])
	}
}

// makeWritable restores directory permissions under root so the test sandbox
// can be removed after a deliberate chmod 0555.
func makeWritable(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		return os.Chmod(path, 0o755)
	})
	if err != nil {
		t.Errorf("restore permissions under %s: %v", root, err)
	}
}
