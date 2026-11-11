package pack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
