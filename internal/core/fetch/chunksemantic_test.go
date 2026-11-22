package fetchcore

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// pathsOf lists the paths of a table in order.
func pathsOf(table []fileSlice) []string {
	out := make([]string, len(table))
	for i := range table {
		out[i] = table[i].path
	}
	return out
}

// diffOrder builds a table from paths in the order a diff would carry them.
func diffOrder(paths ...string) []fileSlice {
	out := make([]fileSlice, len(paths))
	for i, p := range paths {
		out[i] = fileSlice{path: p, size: 10}
	}
	return out
}

// TestSemanticOrderPairsInterleavedModules pins the ordering itself: two
// packages are interleaved in the table the diff produced, and the semantic
// order puts each package's implementation and test file together, in package
// order. This is what keeps coupled files in one chunk.
func TestSemanticOrderPairsInterleavedModules(t *testing.T) {
	table := diffOrder(
		"internal/core/fetch/chunkwrite.go",
		"internal/core/verify/service.go",
		"internal/core/fetch/chunkwrite_test.go",
		"internal/core/verify/service_test.go",
		"cmd/verdict/main.go",
	)
	want := []string{
		"cmd/verdict/main.go",
		"internal/core/fetch/chunkwrite.go",
		"internal/core/fetch/chunkwrite_test.go",
		"internal/core/verify/service.go",
		"internal/core/verify/service_test.go",
	}
	if got := pathsOf(semanticOrder(table)); !slices.Equal(got, want) {
		t.Fatalf("semantic order = %q, want %q", got, want)
	}
}

// TestSemanticOrderPairsEveryEcosystem pins the implementation/test pairing for
// the three naming conventions this repository reviews: Go's `_test` suffix,
// TypeScript's `.test` and Python's `test_` prefix, each beside the file it
// exercises and before the next stem.
func TestSemanticOrderPairsEveryEcosystem(t *testing.T) {
	table := diffOrder(
		"pkg/foo_test.go",
		"pkg/bar.test.ts",
		"pkg/test_baz.py",
		"pkg/foo.go",
		"pkg/bar.ts",
		"pkg/baz.py",
		"pkg/baz_test.py",
		"pkg/zed.go",
	)
	want := []string{
		"pkg/bar.ts",
		"pkg/bar.test.ts",
		"pkg/baz.py",
		"pkg/baz_test.py",
		"pkg/test_baz.py",
		"pkg/foo.go",
		"pkg/foo_test.go",
		"pkg/zed.go",
	}
	if got := pathsOf(semanticOrder(table)); !slices.Equal(got, want) {
		t.Fatalf("semantic order = %q, want %q", got, want)
	}
}

// TestSemanticOrderKeepsSubsystemsAdjacent pins the directory order: sorted by
// path segments, so `engine` sorts before its own subpackages and before
// `engine-extras`, which a byte comparison would place first (`-` sorts below
// `/`). The repository root sorts first, as the empty prefix.
func TestSemanticOrderKeepsSubsystemsAdjacent(t *testing.T) {
	table := diffOrder(
		"engine-extras/plugin.go",
		"engine/graph/edge.go",
		"main.go",
		"engine/render.go",
		"engine/graph/edge_test.go",
	)
	want := []string{
		"main.go",
		"engine/render.go",
		"engine/graph/edge.go",
		"engine/graph/edge_test.go",
		"engine-extras/plugin.go",
	}
	if got := pathsOf(semanticOrder(table)); !slices.Equal(got, want) {
		t.Fatalf("semantic order = %q, want %q", got, want)
	}
}

// TestPackChunksKeepsPackageTogether is the chunking payoff: two packages whose
// files the diff interleaved still pack one package per chunk when each pair
// fits, because the ordering runs before the greedy pack.
func TestPackChunksKeepsPackageTogether(t *testing.T) {
	table := []fileSlice{
		{path: "pkg/a/x.go", size: 40},
		{path: "pkg/b/y.go", size: 40},
		{path: "pkg/a/x_test.go", size: 40},
		{path: "pkg/b/y_test.go", size: 40},
	}
	plan := packChunks(semanticOrder(table), 100)
	got := make([][]string, len(plan))
	for i := range plan {
		got[i] = pathsOf(plan[i].members)
	}
	want := [][]string{{"pkg/a/x.go", "pkg/a/x_test.go"}, {"pkg/b/y.go", "pkg/b/y_test.go"}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("plan = %q, want %q", got, want)
	}
	for _, chunk := range plan {
		if chunk.estSize > 100 {
			t.Fatalf("chunk %s estimated %d bytes, over the 100-byte cap", chunk.name, chunk.estSize)
		}
	}
}

// TestPackChunksWithLimitsNeverMixesGroups pins the per-group contract: a
// group's members are packed together at that group's own cap, no chunk holds
// two groups, and a group split by its cap is never joined by a neighbor's
// files.
func TestPackChunksWithLimitsNeverMixesGroups(t *testing.T) {
	limits, table := groupedFixture()
	plan := packChunksWithLimits(semanticOrder(table), limits, 512000, true)
	assertGroupsIsolated(t, plan, limits)
	// The small group's two 60-byte files do not fit one 100-byte chunk, so it
	// takes two; the wide group's two fit one. Neither group spills into the
	// other.
	if small := countGroupChunks(plan, "small"); small != 2 || len(plan) != 3 {
		t.Fatalf("chunks = %d (%d small), want 3 with the small group in 2: %+v", len(plan), small, plan)
	}
	if got, want := pathsOf(plan[0].members), []string{"a/one.go"}; !slices.Equal(got, want) {
		t.Fatalf("first chunk = %q, want %q", got, want)
	}
	if got, want := pathsOf(plan[2].members), []string{"c/three.go", "d/four.go"}; !slices.Equal(got, want) {
		t.Fatalf("last chunk = %q, want %q", got, want)
	}
}

// assertGroupsIsolated fails when a chunk mixes two config groups or holds more
// bytes than its own group's cap.
func assertGroupsIsolated(t *testing.T, plan []chunkEntry, limits []groupLimit) {
	t.Helper()
	for _, chunk := range plan {
		ids := chunkGroupsOf(chunk)
		if len(ids) != 1 {
			t.Fatalf("chunk %s mixes groups: %q", chunk.name, pathsOf(chunk.members))
		}
		if chunk.estSize > chunkCap(limits, ids[0]) {
			t.Fatalf("chunk %s holds %d bytes over its cap: %q", chunk.name, chunk.estSize, pathsOf(chunk.members))
		}
	}
}

// countGroupChunks counts the planned chunks whose one group is id.
func countGroupChunks(plan []chunkEntry, id string) int {
	count := 0
	for _, chunk := range plan {
		if ids := chunkGroupsOf(chunk); ids[0] == id {
			count++
		}
	}
	return count
}

// groupedFixture is the two-group table: the small group's files interleave
// with the wide group's in diff order, and the small group caps at 100 bytes.
func groupedFixture() ([]groupLimit, []fileSlice) {
	limits := []groupLimit{
		{ID: "small", MaxBytes: 100, Files: map[string]bool{"a/one.go": true, "b/two.go": true}},
		{ID: "wide", MaxBytes: 1000, Files: map[string]bool{"c/three.go": true, "d/four.go": true}},
	}
	return limits, []fileSlice{
		{path: "a/one.go", size: 60},
		{path: "c/three.go", size: 60},
		{path: "d/four.go", size: 60},
		{path: "b/two.go", size: 60},
	}
}

// chunkGroupsOf lists the config groups one chunk's members belong to, in
// member order.
func chunkGroupsOf(chunk chunkEntry) []string {
	var ids []string
	for _, member := range chunk.members {
		if id := groupOfPath(member.path); !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// groupOfPath names the group the two-group fixture puts one path in.
func groupOfPath(p string) string {
	switch p {
	case "a/one.go", "b/two.go":
		return "small"
	default:
		return "wide"
	}
}

// chunkCap reports the cap one group runs at.
func chunkCap(limits []groupLimit, id string) int64 {
	for i := range limits {
		if limits[i].ID == id {
			return limits[i].MaxBytes
		}
	}
	return 0
}

// TestSemanticOrderIsDeterministic pins the two determinism claims: the same
// table packs the same way on every call, and a table whose separators are
// Windows spellings orders exactly like the forward-slash one.
func TestSemanticOrderIsDeterministic(t *testing.T) {
	posix := diffOrder("pkg/a/x_test.go", "pkg/b/y.go", "pkg/a/x.go")
	windows := diffOrder(`pkg\a\x_test.go`, `pkg\b\y.go`, `pkg\a\x.go`)
	wantPosix := []string{"pkg/a/x.go", "pkg/a/x_test.go", "pkg/b/y.go"}
	wantWindows := []string{`pkg\a\x.go`, `pkg\a\x_test.go`, `pkg\b\y.go`}
	first := packChunks(semanticOrder(posix), 100)
	second := packChunks(semanticOrder(posix), 100)
	if !slices.EqualFunc(first, second, sameChunk) {
		t.Fatalf("repack differs:\nfirst %+v\nagain %+v", first, second)
	}
	if got := pathsOf(semanticOrder(posix)); !slices.Equal(got, wantPosix) {
		t.Fatalf("posix order = %q, want %q", got, wantPosix)
	}
	if got := pathsOf(semanticOrder(windows)); !slices.Equal(got, wantWindows) {
		t.Fatalf("windows order = %q, want %q", got, wantWindows)
	}
}

// sortedSections sorts a copy of sections, so two orderings of the same
// sections compare equal.
func sortedSections(sections []string) []string {
	out := slices.Clone(sections)
	slices.Sort(out)
	return out
}

// TestSliceChunksWritesSectionsInManifestOrder proves the chunk payload matches
// its checklist: each chunk holds its members' sections in the manifest's
// order, byte for byte, and every `full.diff` section appears exactly once
// across the chunks.
func TestSliceChunksWritesSectionsInManifestOrder(t *testing.T) {
	t.Parallel()
	full := orderedDiffFixture
	plan, cdir := writtenChunks(t, full)
	assertPayloadOrder(t, plan, cdir)
	wantMembers := []string{"pkg/a/x.go", "pkg/a/x_test.go", "pkg/b/y.go"}
	if got := pathsOf(plan[0].members); !slices.Equal(got, wantMembers) {
		t.Fatalf("chunk members = %q, want the semantic order %q", got, wantMembers)
	}
	_, fullSections := hunks.Sections(full)
	gots := chunkSectionsOf(t, plan, cdir)
	want := []string{fullSections[1], fullSections[2], fullSections[0]}
	if !slices.Equal(gots, want) {
		t.Fatalf("chunk sections = %q, want %q", gots, want)
	}
	// No hunk is lost: the chunks carry exactly the diff's sections, once each,
	// byte for byte — only their order moved.
	if !slices.Equal(sortedSections(gots), sortedSections(fullSections)) {
		t.Fatalf("chunk sections = %q, want the full diff's sections %q", gots, fullSections)
	}
}

// orderedDiffFixture is a two-package diff whose sections arrive in diff order
// (b before a) and whose a-package pair must be written first.
const orderedDiffFixture = "diff --git a/pkg/b/y.go b/pkg/b/y.go\n--- a/pkg/b/y.go\n+++ b/pkg/b/y.go\n@@ -1 +1 @@\n-a\n+b\n" +
	"diff --git a/pkg/a/x.go b/pkg/a/x.go\n--- a/pkg/a/x.go\n+++ b/pkg/a/x.go\n@@ -1 +1 @@\n-c\n+d\n" +
	"diff --git a/pkg/a/x_test.go b/pkg/a/x_test.go\n--- a/pkg/a/x_test.go\n+++ b/pkg/a/x_test.go\n@@ -1 +1 @@\n-e\n+f\n"

// writtenChunks plans and slices one diff into a temp bundle exactly as
// maybeWriteChunks does at the whole-diff cap, and returns the plan and the
// chunks directory.
func writtenChunks(t *testing.T, full string) ([]chunkEntry, string) {
	t.Helper()
	return plannedChunks(t, full, 10_000)
}

// plannedChunks is writtenChunks at an explicit cap, so a test can force a
// multi-chunk split.
func plannedChunks(t *testing.T, full string, maxBytes int64) ([]chunkEntry, string) {
	t.Helper()
	dir := t.TempDir()
	diffPath := filepath.Join(dir, "full.diff")
	if err := os.WriteFile(diffPath, []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, _, _ := chunkPlanFor(chunkFileTableText(full), nil, maxBytes, "flag", chunkOrderAllowed(full))
	cdir := filepath.Join(dir, "chunks")
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sliceChunks(full, cdir, plan); err != nil {
		t.Fatalf("sliceChunks: %v", err)
	}
	return plan, cdir
}

// assertPayloadOrder fails unless each chunk diff carries its sections in the
// order the plan names them, which is what the manifest and its checklist
// publish.
func assertPayloadOrder(t *testing.T, plan []chunkEntry, cdir string) {
	t.Helper()
	for i := range plan {
		raw, err := os.ReadFile(filepath.Join(cdir, plan[i].name+".diff"))
		if err != nil {
			t.Fatal(err)
		}
		_, sections := hunks.Sections(string(raw))
		got := make([]string, 0, len(sections))
		for j := range sections {
			_, newPath := hunks.HeaderPaths(firstLine(sections[j]))
			got = append(got, newPath)
		}
		if want := pathsOf(plan[i].members); !slices.Equal(got, want) {
			t.Fatalf("%s payload = %q, want the manifest order %q", plan[i].name, got, want)
		}
	}
}

// chunkSectionsOf reads every planned chunk diff and returns its sections in
// chunk order.
func chunkSectionsOf(t *testing.T, plan []chunkEntry, cdir string) []string {
	t.Helper()
	var out []string
	for i := range plan {
		raw, err := os.ReadFile(filepath.Join(cdir, plan[i].name+".diff"))
		if err != nil {
			t.Fatal(err)
		}
		_, sections := hunks.Sections(string(raw))
		out = append(out, sections...)
	}
	return out
}

// TestSliceChunksKeepsByteOrderForAnUnterminatedDiff is the fallback: a diff
// whose last section has no trailing newline cannot be reordered without
// gluing that section's last line to the next chunk's first header, so a
// multi-chunk split keeps the diff's own order, the unterminated section stays
// last, and the ordered concatenation is still the input, byte for byte.
func TestSliceChunksKeepsByteOrderForAnUnterminatedDiff(t *testing.T) {
	t.Parallel()
	full := "diff --git a/pkg/b/y.go b/pkg/b/y.go\n--- a/pkg/b/y.go\n+++ b/pkg/b/y.go\n@@ -1 +1 @@\n-a\n+b\n" +
		"diff --git a/pkg/a/x.go b/pkg/a/x.go\n--- a/pkg/a/x.go\n+++ b/pkg/a/x.go\n@@ -1 +1 @@\n-c\n+d"
	if chunkOrderAllowed(full) {
		t.Fatal("an unterminated diff was read as reorderable")
	}
	// 100 bytes fits one section of this fixture but not two, so the diff
	// really splits and the unterminated section has a following chunk to
	// corrupt.
	plan, cdir := plannedChunks(t, full, 100)
	if len(plan) != 2 {
		t.Fatalf("plan has %d chunks, want 2", len(plan))
	}
	if got, want := pathsOf(plan[0].members), []string{"pkg/b/y.go"}; !slices.Equal(got, want) {
		t.Fatalf("first chunk = %q, want the diff order %q", got, want)
	}
	if got, want := pathsOf(plan[1].members), []string{"pkg/a/x.go"}; !slices.Equal(got, want) {
		t.Fatalf("last chunk = %q, want the unterminated section %q", got, want)
	}
	assertPayloadOrder(t, plan, cdir)
	joined := chunkDiffsOf(t, plan, cdir)
	if strings.Contains(joined, "+d"+diffHeaderPrefix) {
		t.Fatalf("the unterminated last line merged with the next header: %q", joined)
	}
	if joined != full {
		t.Fatalf("chunk concat = %q, want the input %q", joined, full)
	}
}

// diffHeaderPrefix opens every diff section, spelled here so a test's
// expectation does not read the constant the implementation matches.
const diffHeaderPrefix = "diff --git "

// chunkDiffsOf concatenates the planned chunk diffs in chunk order.
func chunkDiffsOf(t *testing.T, plan []chunkEntry, cdir string) string {
	t.Helper()
	var joined strings.Builder
	for i := range plan {
		raw, err := os.ReadFile(filepath.Join(cdir, plan[i].name+".diff"))
		if err != nil {
			t.Fatal(err)
		}
		joined.Write(raw)
	}
	return joined.String()
}

// withheldSentinel marks the bytes a privacy exclusion must keep out of every
// chunk payload.
const withheldSentinel = "PRIVACY-EXCLUDED-SENTINEL"

// withheldChunkFixture is a multi-package diff with three kinds of section: a
// plainly excluded file, an excluded rename whose new path collides with a kept
// section's path (the shape a post-filter keyed by path would mis-route), and
// the kept package files. Both excluded sections carry the sentinel.
const withheldChunkFixture = "diff --git a/secret/plan.md b/secret/plan.md\n" +
	"--- a/secret/plan.md\n+++ b/secret/plan.md\n@@ -1 +1 @@\n-" + withheldSentinel + "\n+kept plan\n" +
	"diff --git a/old/hidden.go b/pkg/a/hidden.go\n" +
	"similarity index 90%\nrename from old/hidden.go\nrename to pkg/a/hidden.go\n" +
	"--- a/old/hidden.go\n+++ b/pkg/a/hidden.go\n@@ -1 +1 @@\n-" + withheldSentinel + "\n+package a\n" +
	"diff --git a/pkg/a/hidden.go b/pkg/a/hidden.go\n" +
	"--- a/pkg/a/hidden.go\n+++ b/pkg/a/hidden.go\n@@ -1 +1 @@\n-x\n+y\n" +
	"diff --git a/pkg/a/x.go b/pkg/a/x.go\n--- a/pkg/a/x.go\n+++ b/pkg/a/x.go\n@@ -2 +2 @@\n-c\n+d\n" +
	"diff --git a/pkg/a/x_test.go b/pkg/a/x_test.go\n" +
	"--- a/pkg/a/x_test.go\n+++ b/pkg/a/x_test.go\n@@ -3 +3 @@\n-e\n+f\n" +
	"diff --git a/pkg/b/y.go b/pkg/b/y.go\n--- a/pkg/b/y.go\n+++ b/pkg/b/y.go\n@@ -4 +4 @@\n-g\n+h\n"

// withheldChunkExclusions drops the plain exclusion and the rename by its old
// endpoint, leaving the pkg files.
var withheldChunkExclusions = []string{"secret/**", "old/**"}

// TestMaybeWriteChunksWithholdsExcludedSections is the privacy regression:
// payloads are sliced from the diff the exclusions already filtered, so a
// withheld section never reaches a chunk file, an excluded rename whose new
// path collides with a kept section cannot mis-route one, and every kept
// section survives byte for byte in semantic order.
func TestMaybeWriteChunksWithholdsExcludedSections(t *testing.T) {
	t.Parallel()
	dir := writeDiffBundle(t, withheldChunkFixture)
	if _, err := maybeWriteChunks(io.Discard, dir, 200, true, nil, "", "", "flag", withheldChunkExclusions); err != nil {
		t.Fatalf("maybeWriteChunks: %v", err)
	}
	chunks := readChunkManifest(t, dir)
	if len(chunks) < 2 {
		t.Fatalf("chunk count = %d, want a multi-chunk split", len(chunks))
	}
	assertChunkFileCount(t, dir, len(chunks))
	wantFiles := []string{"pkg/a/hidden.go", "pkg/a/x.go", "pkg/a/x_test.go", "pkg/b/y.go"}
	if got := manifestFiles(chunks); !slices.Equal(got, wantFiles) {
		t.Fatalf("manifest files = %q, want the semantic order %q", got, wantFiles)
	}
	assertNoWithheldBytes(t, dir, chunks)
	assertChunkConcat(t, dir, chunks, config.WithoutExcludedSections(withheldChunkFixture, withheldChunkExclusions))
}

// assertChunkFileCount fails unless the bundle wrote one diff per manifest
// chunk.
func assertChunkFileCount(t *testing.T, dir string, want int) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "chunks", "*.diff"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != want {
		t.Fatalf("chunk files = %v, want %d", files, want)
	}
}

// assertNoWithheldBytes fails when a chunk diff carries a withheld section or
// its sentinel, and proves each checklist lists that chunk's manifest files.
func assertNoWithheldBytes(t *testing.T, dir string, chunks []chunkManifestEntry) {
	t.Helper()
	for _, chunk := range chunks {
		body := readChunkBody(t, dir, chunk.name)
		if strings.Contains(body, withheldSentinel) {
			t.Fatalf("%s leaks a privacy-excluded byte: %q", chunk.name, body)
		}
		if strings.Contains(body, "secret/plan.md") || strings.Contains(body, "old/hidden.go") {
			t.Fatalf("%s carries an excluded section: %q", chunk.name, body)
		}
		assertTOCFiles(t, dir, chunk)
	}
}

// assertChunkConcat fails unless the chunk diffs in order hold exactly want.
func assertChunkConcat(t *testing.T, dir string, chunks []chunkManifestEntry, want string) {
	t.Helper()
	var joined strings.Builder
	for _, chunk := range chunks {
		joined.WriteString(readChunkBody(t, dir, chunk.name))
	}
	if joined.String() != want {
		t.Fatalf("chunk concat = %q, want %q", joined.String(), want)
	}
}

// readChunkBody reads one chunk's diff text.
func readChunkBody(t *testing.T, dir, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "chunks", name+".diff"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestMaybeWriteChunksAllSectionsWithheld proves the degenerate case: when
// every section is withheld there is nothing to slice, so no chunk file is
// written, nothing panics, and the manifest publishes a valid empty list.
func TestMaybeWriteChunksAllSectionsWithheld(t *testing.T) {
	t.Parallel()
	dir := writeDiffBundle(t, withheldChunkFixture)
	if _, err := maybeWriteChunks(io.Discard, dir, 200, true, nil, "", "", "flag", []string{"**"}); err != nil {
		t.Fatalf("maybeWriteChunks: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "chunks", "*.diff"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("chunk files = %v, want none", files)
	}
	chunks := readChunkManifest(t, dir)
	if len(chunks) != 0 {
		t.Fatalf("manifest chunks = %+v, want an empty list", chunks)
	}
}

// chunkManifestEntry is one manifest chunk as a test reads it.
type chunkManifestEntry struct {
	name  string
	files []string
}

// readChunkManifest decodes the chunked bundle's manifest into its chunk list.
func readChunkManifest(t *testing.T, dir string) []chunkManifestEntry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "chunks", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	manifest, ok := jq.AsMap(doc)
	if !ok {
		t.Fatalf("manifest is not an object: %s", raw)
	}
	out := make([]chunkManifestEntry, 0, len(jq.Elements(manifest["chunks"])))
	for _, chunk := range jq.Elements(manifest["chunks"]) {
		out = append(out, manifestChunk(t, chunk))
	}
	return out
}

// manifestChunk reads one manifest chunk entry.
func manifestChunk(t *testing.T, chunk any) chunkManifestEntry {
	t.Helper()
	entry, ok := jq.AsMap(chunk)
	if !ok {
		t.Fatalf("chunk entry is not an object: %v", chunk)
	}
	files := make([]string, 0, len(jq.Elements(entry["files"])))
	for _, file := range jq.Elements(entry["files"]) {
		files = append(files, jq.Str(file))
	}
	return chunkManifestEntry{name: jq.Str(entry["name"]), files: files}
}

// manifestFiles flattens every manifest chunk's file list, in chunk order.
func manifestFiles(chunks []chunkManifestEntry) []string {
	var out []string
	for _, chunk := range chunks {
		out = append(out, chunk.files...)
	}
	return out
}

// assertTOCFiles fails unless one chunk's checklist lists exactly its manifest
// files, in the same order.
func assertTOCFiles(t *testing.T, dir string, chunk chunkManifestEntry) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "chunks", chunk.name+".toc.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	inFiles := false
	for line := range strings.SplitSeq(string(raw), "\n") {
		switch {
		case line == "## files":
			inFiles = true
		case !inFiles || !strings.HasPrefix(line, "- "):
		default:
			path, _, _ := strings.Cut(strings.TrimPrefix(line, "- "), " (")
			got = append(got, path)
		}
	}
	if !slices.Equal(got, chunk.files) {
		t.Fatalf("%s toc = %q, want %q", chunk.name, got, chunk.files)
	}
}

// writeDiffBundle writes one diff as a bundle's full.diff and returns the
// bundle directory.
func writeDiffBundle(t *testing.T, diff string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSemanticOrderTieBreaksOnOriginalBytes pins the final key: two spellings
// of one path normalize to the same directory and stem, so the original bytes
// decide, and the result never depends on the order the table arrived in.
func TestSemanticOrderTieBreaksOnOriginalBytes(t *testing.T) {
	t.Parallel()
	posix := diffOrder("pkg/foo.go")
	backslash := diffOrder(`pkg\foo.go`)
	forward, backward := semanticOrder(append(slices.Clone(posix), backslash...)),
		semanticOrder(append(slices.Clone(backslash), posix...))
	if got := pathsOf(forward); !slices.Equal(got, pathsOf(backward)) {
		t.Fatalf("input order changed the result: forward %q, backward %q", got, pathsOf(backward))
	}
	// `/` (0x2f) sorts below `\` (0x5c), so the forward-slash spelling leads.
	if got, want := pathsOf(forward), []string{"pkg/foo.go", `pkg\foo.go`}; !slices.Equal(got, want) {
		t.Fatalf("tie-break order = %q, want %q", got, want)
	}
}

// runsFixture is a diff whose config groups alternate A, B, A and whose last
// section is unterminated: the shape that must not be reordered.
const runsFixture = "diff --git a/pkg/a/one.go b/pkg/a/one.go\n" +
	"--- a/pkg/a/one.go\n+++ b/pkg/a/one.go\n@@ -1 +1 @@\n-a\n+first\n" +
	"diff --git a/pkg/b/two.go b/pkg/b/two.go\n" +
	"--- a/pkg/b/two.go\n+++ b/pkg/b/two.go\n@@ -1 +1 @@\n-b\n+second\n" +
	"diff --git a/pkg/a/three.go b/pkg/a/three.go\n" +
	"--- a/pkg/a/three.go\n+++ b/pkg/a/three.go\n@@ -1 +1 @@\n-c\n+last"

// TestPackChunkRunsKeepOrderedGroupRunsTogether is the alternating-group
// regression: hoisting the later A section ahead of B would move the
// unterminated section out of the final chunk, and concatenating the chunks
// would glue its last line to the next header. Contiguous runs keep every
// section where the diff put it.
func TestPackChunkRunsKeepOrderedGroupRunsTogether(t *testing.T) {
	t.Parallel()
	if chunkOrderAllowed(runsFixture) {
		t.Fatal("an unterminated diff was read as reorderable")
	}
	limits := []groupLimit{
		{ID: "small", MaxBytes: 100, Files: map[string]bool{"pkg/a/one.go": true, "pkg/a/three.go": true}},
		{ID: "wide", MaxBytes: 1000, Files: map[string]bool{"pkg/b/two.go": true}},
	}
	plan, cdir := plannedGroupChunks(t, runsFixture, limits)
	got := make([][]string, len(plan))
	for i := range plan {
		got[i] = pathsOf(plan[i].members)
	}
	want := [][]string{{"pkg/a/one.go"}, {"pkg/b/two.go"}, {"pkg/a/three.go"}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("plan = %q, want the diff order %q", got, want)
	}
	assertPayloadOrder(t, plan, cdir)
	joined := chunkDiffsOf(t, plan, cdir)
	if strings.Contains(joined, "+last"+diffHeaderPrefix) {
		t.Fatalf("the unterminated last line merged with the next header: %q", joined)
	}
	if joined != runsFixture {
		t.Fatalf("chunk concat = %q, want the input %q", joined, runsFixture)
	}
}

// plannedGroupChunks plans and slices one diff at explicit per-group caps, so a
// test can drive the grouped packer the way maybeWriteChunks does.
func plannedGroupChunks(t *testing.T, full string, limits []groupLimit) ([]chunkEntry, string) {
	t.Helper()
	plan, _, _ := chunkPlanFor(chunkFileTableText(full), limits, 1000, "flag", chunkOrderAllowed(full))
	cdir := filepath.Join(t.TempDir(), "chunks")
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sliceChunks(full, cdir, plan); err != nil {
		t.Fatalf("sliceChunks: %v", err)
	}
	return plan, cdir
}
