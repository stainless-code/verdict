package fetchcore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestFetchLocalMissingRelativeContextRefusal proves a missing relative
// --context operand refused by the fetch-local service names the operand as
// typed, its absolute resolution, and the path-repair hint.
func TestFetchLocalMissingRelativeContextRefusal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "work")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	typed := filepath.Join("..", "nope.md")
	cmdr := stagedLocalCommander(t, cleanStagedDiff)
	var human, diag bytes.Buffer
	var streamed []string
	_, err := RunLocal(t.Context(),
		localScriptReq(cmdr, &human, &diag, &streamed, nil,
			LocalFlags{Staged: true, Context: []string{typed}}))
	ref := requireRefusal(t, err, "context-invalid")
	resolved := filepath.Join(root, "nope.md")
	for _, want := range []string{typed, resolved} {
		if !strings.Contains(filepath.ToSlash(ref.Message), filepath.ToSlash(want)) {
			t.Errorf("refusal = %q, want it to name %q", ref.Message, want)
		}
	}
	const wantHint = "try: check each --context path (relative paths resolve against the current directory), then re-run"
	if got := core.HintFor(ref.Code); got != wantHint {
		t.Errorf("hint = %q, want %q", got, wantHint)
	}
}

// TestFetchLocalGoodContextStillBundles proves a valid relative --context
// operand reaches the bundle, so the typed refusal cannot come from a blanket
// rejection of relative context inputs.
func TestFetchLocalGoodContextStillBundles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeContextFixture(t, "evidence.txt", "HANDED")
	cmdr := stagedLocalCommander(t, cleanStagedDiff)
	var human, diag bytes.Buffer
	var streamed []string
	_, err := RunLocal(t.Context(),
		localScriptReq(cmdr, &human, &diag, &streamed, nil,
			LocalFlags{Staged: true, Context: []string{"evidence.txt"}}))
	if err != nil {
		t.Fatalf("RunLocal with a valid relative context: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(".verdict", "state", "local-mine", "context.json"))
	if err != nil {
		t.Fatalf("context.json: %v", err)
	}
	if !strings.Contains(string(raw), `"source": "evidence.txt"`) {
		t.Errorf("context.json misses the handed source:\n%s", raw)
	}
}
