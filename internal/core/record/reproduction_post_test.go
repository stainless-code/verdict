package recordcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

func TestPostMergeRetainsReproduction(t *testing.T) {
	t.Run("post", func(t *testing.T) { checkPostReproduction(t, false, "findings.json") })
	t.Run("post dry-run", func(t *testing.T) { checkPostReproduction(t, true, "findings.dryrun.json") })
}

func checkPostReproduction(t *testing.T, dry bool, store string) {
	t.Helper()
	dir := t.TempDir()
	entry := jq.NewObj("sig", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "rigor", "reproduced",
		"reproduction", jq.NewObj("path", "", "command", "false"))
	st := &state{dir: dir, cmd: "post", dryRun: dry, findingsFile: filepath.Join(dir, store), kb: &kb.Ctx{}, entries: []any{entry}}
	st.captureReproductions()
	if err := st.persistMerged(); err != nil {
		t.Fatal(err)
	}
	raw := readRetainedFile(t, filepath.Join(dir, store))
	if !strings.Contains(raw, `"path": "tests/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sh"`) {
		t.Fatalf("stored receipt missing: %s", raw)
	}
	if got := readRetainedFile(t, filepath.Join(dir, "tests", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sh")); got != "false\n" {
		t.Fatalf("retained test = %q", got)
	}
	checkRetainedCommand(t, dir)
}

func checkRetainedCommand(t *testing.T, dir string) {
	t.Helper()
	got := readRetainedFile(t, filepath.Join(dir, "tests", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.command.json"))
	if got != `{"path":"tests/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sh","command":"sh tests/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sh","redacted":false}` {
		t.Fatalf("retained command = %q", got)
	}
}

func readRetainedFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
