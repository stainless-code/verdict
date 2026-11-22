package recordcore

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// TestSameDiffSections pins the drift comparison: a regrouped chunk map (the
// same file sections in another order) is not drift, while a lost, doubled, or
// edited section is. The ordered concatenation is no longer the invariant —
// semantic chunk membership moves sections — so this is what the warning reads.
func TestSameDiffSections(t *testing.T) {
	t.Parallel()
	const (
		a = "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-a\n+b\n"
		b = "diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-c\n+d\n"
	)
	tests := []struct {
		name string
		got  string
		want string
		same bool
	}{
		{name: "identical", got: a + b, want: a + b, same: true},
		{name: "regrouped", got: b + a, want: a + b, same: true},
		{name: "lost section", got: a, want: a + b},
		{name: "doubled section", got: a + a + b, want: a + b},
		{name: "edited section", got: a + b + "+extra\n", want: a + b},
		{name: "edited line", got: a + "diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n", want: a + b},
		{name: "preamble differs", got: "before\n" + a, want: a},
		{name: "prefix only", got: "diff --", want: a},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sameDiffSections(tc.got, tc.want); got != tc.same {
				t.Fatalf("sameDiffSections(%q, %q) = %t, want %t", tc.got, tc.want, got, tc.same)
			}
		})
	}
}

// TestConcatEqualsToleratesWithheldSections pins the record's view of a bundle
// whose privacy exclusions withheld a section: the chunks carry the filtered
// diff, so the same chunk map is drift only when the exclusions say none were
// dropped.
func TestConcatEqualsToleratesWithheldSections(t *testing.T) {
	t.Parallel()
	const (
		kept   = "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-a\n+b\n"
		secret = "diff --git a/secret/plan.md b/secret/plan.md\n--- a/secret/plan.md\n+++ b/secret/plan.md\n@@ -1 +1 @@\n-c\n+d\n"
	)
	dir := t.TempDir()
	writeRecordFile(t, filepath.Join(dir, "full.diff"), kept+secret)
	chunk := filepath.Join(dir, "chunk-01.diff")
	writeRecordFile(t, chunk, kept)
	paths := []string{chunk}
	if !concatEquals(paths, filepath.Join(dir, "full.diff"), []string{"secret/**"}) {
		t.Fatal("a withheld section read as drift")
	}
	if concatEquals(paths, filepath.Join(dir, "full.diff"), nil) {
		t.Fatal("a lost section passed without the exclusion that explains it")
	}
	if concatEquals(paths, filepath.Join(dir, "full.diff"), []string{"elsewhere/**"}) {
		t.Fatal("an unrelated exclusion excused a lost section")
	}
}

// writeRecordFile writes one record-test fixture file.
func writeRecordFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestPrivacyExclusionsReadCapturedConfig pins how the chunk comparison learns
// about withheld sections: from the run state the fetch captured, and not at
// all when the bundle carries none.
func TestPrivacyExclusionsReadCapturedConfig(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), ".verdict", "state", "7")
	rs := &config.RunState{Resolved: &config.Resolved{
		Privacy: config.PrivacyView{Exclusions: []string{"secret/**"}},
	}}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("write run state: %v", serr)
	}
	if got, want := privacyExclusions(dir), []string{"secret/**"}; !slices.Equal(got, want) {
		t.Fatalf("exclusions = %q, want %q", got, want)
	}
	if got := privacyExclusions(t.TempDir()); got != nil {
		t.Fatalf("bundle without run state = %q, want none", got)
	}
}

// TestEmptyChunkManifestAllowedWhenAllSectionsWithheld is the all-withheld
// bundle: the fetch wrote a valid manifest with no chunks, and record accepts
// it because the privacy exclusions left full.diff with nothing to slice.
func TestEmptyChunkManifestAllowedWhenAllSectionsWithheld(t *testing.T) {
	t.Parallel()
	dir := withheldBundle(t, secretsOnly)
	if !noRetainedSections(dir, privacyExclusions(dir)) {
		t.Fatal("the withheld bundle still reports retained sections")
	}
	manifest := writeEmptyChunkManifest(t, dir)
	chunks, names, err := readChunkManifest(manifest, refetchLine, noRetainedSections(dir, privacyExclusions(dir)))
	if err != nil {
		t.Fatalf("valid empty manifest refused: %v", err)
	}
	if len(chunks) != 0 || len(names) != 0 {
		t.Fatalf("parsed %d chunks (%q), want none", len(chunks), names)
	}
	if ref := emptyManifestRefusal(t, manifest, false); ref.Code != codeMissingChunkSnap {
		t.Fatalf("strict reading = %+v, want %s", ref, codeMissingChunkSnap)
	}
	st := &state{dir: dir, pr: "local-work", inputChecked: true, dryRun: true}
	if _, err := st.validateChunkMerge(); err != nil {
		t.Fatalf("record refused the all-withheld bundle: %v", err)
	}
	if slices.Contains(st.warnings, msgChunkDrifted) {
		t.Fatalf("record reported drift on a fully withheld bundle: %q", st.warnings)
	}
}

// TestEmptyChunkManifestRefusedWhenSectionsRemain keeps the refusal honest: one
// retained section means the empty chunk list is a missing snapshot, not a
// withheld one.
func TestEmptyChunkManifestRefusedWhenSectionsRemain(t *testing.T) {
	t.Parallel()
	dir := withheldBundle(t, keptSectionText+secretsOnly)
	if noRetainedSections(dir, privacyExclusions(dir)) {
		t.Fatal("a retained section was read as fully withheld")
	}
	manifest := writeEmptyChunkManifest(t, dir)
	if ref := emptyManifestRefusal(t, manifest, false); ref.Code != codeMissingChunkSnap {
		t.Fatalf("refusal = %+v, want %s", ref, codeMissingChunkSnap)
	}
	st := &state{dir: dir, pr: "local-work", inputChecked: true, dryRun: true}
	_, err := st.validateChunkMerge()
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok || ref.Code != codeMissingChunkSnap {
		t.Fatalf("validateChunkMerge = %v, want %s", err, codeMissingChunkSnap)
	}
}

// refetchLine is the re-fetch hint the manifest refusals quote.
const refetchLine = "verdict fetch-local acme/app 7 --chunk -- <paths>"

// secretsOnly is a diff whose only section the captured exclusions withhold.
const secretsOnly = "diff --git a/secret/plan.md b/secret/plan.md\n" +
	"--- a/secret/plan.md\n+++ b/secret/plan.md\n@@ -1 +1 @@\n-a\n+b\n"

// keptSectionText is a section the captured exclusions keep.
const keptSectionText = "diff --git a/notes.txt b/notes.txt\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-c\n+d\n"

// withheldBundle writes a bundle whose run state withholds secret/** and whose
// full.diff is diff.
func withheldBundle(t *testing.T, diff string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "7")
	rs := &config.RunState{Resolved: &config.Resolved{
		Privacy: config.PrivacyView{Exclusions: []string{"secret/**"}},
	}}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("write run state: %v", serr)
	}
	writeRecordFile(t, filepath.Join(dir, "full.diff"), diff)
	return dir
}

// writeEmptyChunkManifest writes the manifest a fully withheld fetch leaves:
// schema 1, no chunks, and no chunk files.
func writeEmptyChunkManifest(t *testing.T, dir string) string {
	t.Helper()
	cdir := filepath.Join(dir, "chunks")
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cdir, "manifest.json")
	writeRecordFile(t, path, `{"schema_version": "1", "chunk_count": 0, "chunks": []}`)
	return path
}

// emptyManifestRefusal reads an empty manifest and returns the refusal it
// states at the given allowEmpty gate.
func emptyManifestRefusal(t *testing.T, manifest string, allowEmpty bool) *core.RefusalError {
	t.Helper()
	_, _, err := readChunkManifest(manifest, refetchLine, allowEmpty)
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok {
		t.Fatalf("readChunkManifest = %v, want a refusal", err)
	}
	return ref
}
