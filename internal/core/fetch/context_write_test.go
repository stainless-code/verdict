package fetchcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestSaveContextWriteFailureKeepsFetchFailed proves a bundle whose context
// destination cannot be written refuses fetch-failed, not the input-only
// context-invalid, and keeps the pre-existing provider hint.
func TestSaveContextWriteFailureKeepsFetchFailed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "context"), []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	files := []contextFile{{source: "evidence.txt", body: []byte("HANDED")}}
	err = saveContext(root, files, "2026-01-01T00:00:00Z")
	ref := requireRefusal(t, err, CodeFetchFailed)
	if !strings.Contains(ref.Message, "context copy") {
		t.Errorf("refusal = %q, want the context copy cause", ref.Message)
	}
	const wantHint = "try: run 'gh auth status', confirm the repo/PR spelling, then re-run"
	if got := core.HintFor(ref.Code); got != wantHint {
		t.Errorf("hint = %q, want %q", got, wantHint)
	}
}
