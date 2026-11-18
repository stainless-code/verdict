//go:build unix

package fetchcore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestFetchLocalSymlinkedContextRefusal proves a symlinked --context operand
// refused by the fetch-local service is typed context-invalid and names the
// operand as typed and its absolute resolution.
func TestFetchLocalSymlinkedContextRefusal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeContextFixture(t, "target.md", "secret")
	if err := os.Symlink("target.md", "link.md"); err != nil {
		t.Fatal(err)
	}
	cmdr := stagedLocalCommander(t, cleanStagedDiff)
	var human, diag bytes.Buffer
	var streamed []string
	_, err := RunLocal(t.Context(),
		localScriptReq(cmdr, &human, &diag, &streamed, nil,
			core.FetchLocalFlags{Staged: true, Context: []string{"link.md"}}))
	ref := requireRefusal(t, err, "context-invalid")
	for _, want := range []string{"link.md", filepath.Join(dir, "link.md"), "symlink"} {
		if !strings.Contains(filepath.ToSlash(ref.Message), filepath.ToSlash(want)) {
			t.Errorf("refusal = %q, want it to name %q", ref.Message, want)
		}
	}
}
