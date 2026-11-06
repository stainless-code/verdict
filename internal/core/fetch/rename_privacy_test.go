package fetchcore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
)

// renamePrivacyDiff is a hand-authored git rename diff: the old path carries
// a sentinel inside a hunk-bearing section, and an unrelated second file
// pins the byte-exact kept surface. The shape mirrors a real
// `git diff --cached` rename (similarity header, rename from/to lines).
const renamePrivacyDiff = `diff --git a/old/secret.go b/new/secret.go
similarity index 60%
rename from old/secret.go
rename to new/secret.go
index 8566869..2c93831 100644
--- a/old/secret.go
+++ b/new/secret.go
@@ -2,3 +2,6 @@ package old
 // SENTINEL-RENAME-7
 func Secret() int { return 1 }
+// rename edit line
+func Secret2() int { return 2 }
diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1 +1 @@
-hello
+world
`

// renamePrivacyNotesSection is the exact unrelated section the filter must
// keep byte-identical while a rename section is withheld.
const renamePrivacyNotesSection = `diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1 +1 @@
-hello
+world
`

// TestRenamePrivacyOldOnlyExclusionWithholds pins the reported leak: an
// exclusion matching only the pre-rename path withholds the whole renamed
// section from model-facing bytes, while the unrelated section survives
// byte-exact.
func TestRenamePrivacyOldOnlyExclusionWithholds(t *testing.T) {
	filtered := string(filterDiffBytes([]byte(renamePrivacyDiff), []string{"old/**"}))
	if strings.Contains(filtered, "SENTINEL-RENAME-7") {
		t.Fatalf("old-path-excluded rename content survived:\n%s", filtered)
	}
	if strings.Contains(filtered, "secret.go") {
		t.Fatalf("withheld rename section header survived:\n%s", filtered)
	}
	if filtered != renamePrivacyNotesSection {
		t.Fatalf("filtered = %q, want the notes section byte-exact", filtered)
	}
}

// TestRenamePrivacyNewOnlyExclusionWithholds pins the symmetric direction:
// an exclusion matching only the post-rename path withholds the section too.
func TestRenamePrivacyNewOnlyExclusionWithholds(t *testing.T) {
	filtered := string(filterDiffBytes([]byte(renamePrivacyDiff), []string{"new/**"}))
	if strings.Contains(filtered, "SENTINEL-RENAME-7") {
		t.Fatalf("new-path-excluded rename content survived:\n%s", filtered)
	}
	if filtered != renamePrivacyNotesSection {
		t.Fatalf("filtered = %q, want the notes section byte-exact", filtered)
	}
}

// TestRenamePrivacyUnrelatedExclusionKeepsBytes pins the no-over-withholding
// rule: an exclusion matching neither rename endpoint returns the input
// untouched, byte for byte.
func TestRenamePrivacyUnrelatedExclusionKeepsBytes(t *testing.T) {
	if got := string(filterDiffBytes([]byte(renamePrivacyDiff), []string{"elsewhere/**"})); got != renamePrivacyDiff {
		t.Fatalf("unrelated exclusion rewrote the diff: %q", got)
	}
	if got := string(filterDiffBytes([]byte(renamePrivacyDiff), nil)); got != renamePrivacyDiff {
		t.Fatal("empty exclusions rewrote the diff")
	}
}

// TestRenamePrivacyWithheldPathsNamesSection pins the withheld proof: the
// old-path-only exclusion names the renamed section (by its section path)
// and never the unrelated file.
func TestRenamePrivacyWithheldPathsNamesSection(t *testing.T) {
	files := hunks.Files(renamePrivacyDiff)
	withheld := withheldPaths(files, []string{"old/**"})
	if len(withheld) != 1 || !withheld["new/secret.go"] {
		t.Fatalf("withheld = %v, want only the renamed section", withheld)
	}
	withheld = withheldPaths(files, []string{"elsewhere/**"})
	if len(withheld) != 0 {
		t.Fatalf("withheld = %v, want none for an unrelated exclusion", withheld)
	}
}

// TestRenamePrivacyChunkTableDropsRename pins the chunk choke point: the
// chunk file table carries the pre-rename path, and an exclusion matching
// either endpoint drops the renamed section before any chunk byte is written.
func TestRenamePrivacyChunkTableDropsRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "full.diff")
	if err := os.WriteFile(path, []byte(renamePrivacyDiff), 0o600); err != nil {
		t.Fatal(err)
	}
	assertRenameChunkEndpoints(t, chunkFileTable(path))
	assertChunkWithholdKeepsNotes(t, func() []fileSlice { return chunkFileTable(path) })
	if kept := withholdChunkFiles(chunkFileTable(path), nil); len(kept) != 2 {
		t.Fatalf("empty exclusions dropped chunk files: %+v", kept)
	}
}

// assertRenameChunkEndpoints pins the chunk table metadata: the rename
// entry records both endpoints for privacy matching.
func assertRenameChunkEndpoints(t *testing.T, table []fileSlice) {
	t.Helper()
	if len(table) != 2 {
		t.Fatalf("table = %+v, want the rename plus the notes file", table)
	}
	if table[0].path != "new/secret.go" || table[0].oldPath != "old/secret.go" {
		t.Fatalf("rename entry = %+v, want both endpoints recorded", table[0])
	}
}

// assertChunkWithholdKeepsNotes pins the chunk choke point: an exclusion
// matching either rename endpoint drops the renamed section before any
// chunk byte is written, keeping only the unrelated file.
func assertChunkWithholdKeepsNotes(t *testing.T, fresh func() []fileSlice) {
	t.Helper()
	for _, exclusions := range [][]string{{"old/**"}, {"new/**"}} {
		kept := withholdChunkFiles(fresh(), exclusions)
		if len(kept) != 1 || kept[0].path != "notes.txt" {
			t.Fatalf("kept = %+v for %q, want only notes.txt", kept, exclusions)
		}
	}
}

// stagedRenameCommander scripts one non-isolated --staged run over a rename
// diff: the file list names old and new as separate entries (what
// `git diff --no-renames --name-only` reports), while the diff itself
// carries the rename header with content on both sides.
func stagedRenameCommander(t *testing.T, diff string) *scriptCommander {
	t.Helper()
	cmdr := &scriptCommander{
		t: t,
		out: map[string]scriptResult{
			"git\x00rev-parse\x00--is-inside-work-tree":              {stdout: "true\n"},
			"git\x00branch\x00--show-current":                        {stdout: "main\n"},
			"git\x00rev-parse\x00HEAD":                               {stdout: localTestHead + "\n"},
			"git\x00write-tree":                                      {stdout: localTestTree + "\n"},
			"git\x00diff\x00--cached":                                {stdout: diff},
			"git\x00diff\x00--no-renames\x00--name-only\x00--cached": {stdout: "new/secret.go\nold/secret.go\nnotes.txt\n"},
			"git\x00diff\x00--numstat\x00--cached":                   {stdout: "3\t3\told/secret.go => new/secret.go\n1\t1\tnotes.txt\n"},
			"git\x00ls-files\x00--others\x00--exclude-standard":      {stdout: ""},
		},
	}
	scriptEmptyBasePolicy(cmdr, localTestHead)
	return cmdr
}

// TestRenamePrivacyRunLocalPromptOmitsOldPathContent is the end-to-end proof
// through the real RunLocal --show-prompts path: with an old-path-only
// personal exclusion, the raw bundle keeps every byte for patch identity
// while the model-facing prompt dump omits the renamed content.
func TestRenamePrivacyRunLocalPromptOmitsOldPathContent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	dump, bundle := runRenameStagedPrompt(t, writeRenamePromptSettings(t, dir))
	if strings.Contains(dump, "SENTINEL-RENAME-7") {
		t.Fatalf("old-path-excluded rename content reached the prompt dump:\n%s", dump)
	}
	if !strings.Contains(dump, "+world") {
		t.Fatal("unrelated kept content missing from the prompt dump")
	}
	raw, err := os.ReadFile(filepath.Join(bundle, "full.diff"))
	if err != nil {
		t.Fatalf("read full.diff: %v", err)
	}
	if !strings.Contains(string(raw), "SENTINEL-RENAME-7") {
		t.Fatal("raw bundle lost the sentinel: patch identity must stay complete")
	}
}

// writeRenamePromptSettings stages the operator settings plus the minimal
// trusted prompt the dump requires, returning the settings path.
func writeRenamePromptSettings(t *testing.T, dir string) string {
	t.Helper()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"privacy":{"exclusions":["old/**"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("prompts", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("prompts", "system.md"), []byte("system\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return settingsPath
}

// runRenameStagedPrompt runs the real RunLocal --show-prompts path over the
// scripted rename and returns the model-facing dump plus the bundle dir.
func runRenameStagedPrompt(t *testing.T, settingsPath string) (string, string) {
	t.Helper()
	cmdr := stagedRenameCommander(t, renamePrivacyDiff)
	var human, diag bytes.Buffer
	var streamed []string
	flags := core.FetchLocalFlags{Staged: true, ShowPrompts: true}
	req := localScriptReq(cmdr, &human, &diag, &streamed, nil, flags)
	req.SettingsPath = settingsPath
	req.JSONMode = true
	resp, err := RunLocal(context.Background(), req)
	if err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	if resp.Data.PromptDump == nil {
		t.Fatal("no prompt dump staged")
	}
	return *resp.Data.PromptDump, filepath.Join(".verdict", "state", "local-mine")
}
