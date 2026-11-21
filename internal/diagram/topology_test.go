package diagram

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/overrides"
)

const topologyDiff = `diff --git a/src/app.ts b/src/app.ts
--- a/src/app.ts
+++ b/src/app.ts
@@ -1,2 +1,3 @@
 top
+import { theme } from "./theme";
@@ -9,2 +10,3 @@
 ctx
+const accent = "blue";
`

func writeBundleDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestLarge pins the walkthrough gate to the built-in 8 files / 24576 bytes
// with a strict comparison on both sides.
func TestLarge(t *testing.T) {
	t.Parallel()
	ref := overrides.At(t.TempDir())
	tests := []struct {
		name string
		b    Bundle
		want bool
	}{
		{name: "8 files stays below", b: Bundle{FileCount: 8, DiffBytes: 1024}, want: false},
		{name: "9 files crosses", b: Bundle{FileCount: 9, DiffBytes: 1024}, want: true},
		{name: "24576 bytes stays below", b: Bundle{FileCount: 2, DiffBytes: 24576}, want: false},
		{name: "24577 bytes crosses", b: Bundle{FileCount: 2, DiffBytes: 24577}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Large(tc.b, ref); got != tc.want {
				t.Errorf("Large(%+v) = %v, want %v", tc.b, got, tc.want)
			}
		})
	}
}

// TestLargeReadsTheFragment proves the gate reads the operator's
// walkthrough-threshold fragment through the set it is given.
func TestLargeReadsTheFragment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "walkthrough-threshold.md"), []byte("files: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := Bundle{FileCount: 3, DiffBytes: 1024}
	if !Large(b, overrides.At(dir)) {
		t.Error("Large with files: 2 = false, want the fragment to move the gate")
	}
	if Large(b, overrides.At(t.TempDir())) {
		t.Error("Large without the fragment = true, want the built-in 8-file gate")
	}
}

// TestBlockMatchesReportFixture pins the block bytes for the bundle
// evals/cases/publish-overview.sh renders, so the table surface and the posted
// body cannot drift apart.
func TestBlockMatchesReportFixture(t *testing.T) {
	t.Parallel()
	b := Bundle{
		Files: []File{
			{ID: "F1", Path: "src/app.ts", Role: "source", Adds: 10, Dels: 2, Hunks: 2},
			{ID: "F2", Path: "src/theme.ts", Role: "source", Adds: 18, Dels: 9, Hunks: 1},
			{ID: "F3", Path: "test/app.test.ts", Role: "test", Adds: 5, Dels: 0, Hunks: 1},
			{ID: "F4", Path: "schema.json", Role: "config", Adds: 27, Dels: 52, Hunks: 0},
		},
		Added: [][2]string{
			{"src/app.ts", `import { theme } from "./theme.ts";`},
			{"src/app.ts", "new line"},
			{"src/app.ts", "replacement"},
			{"src/theme.ts", "export const theme = {};"},
			{"test/app.test.ts", "// validates src/app.ts contract"},
		},
	}
	want := "<details>\n" +
		"<summary>Change topology (computed from the diff)</summary>\n" +
		"\n" +
		"```mermaid\n" +
		"flowchart TD\n" +
		"    PR[\"PR #4601 +60/-63\"]\n" +
		"    PR --> F1[\"F1 app.ts\"]\n" +
		"    PR --> F2[\"F2 theme.ts\"]\n" +
		"    PR --> F3[\"F3 app.test.ts\"]\n" +
		"    PR --> F4[\"F4 schema.json\"]\n" +
		"    F1 -->|\"uses theme.ts\"| F2\n" +
		"    F3 -->|\"validates app.ts\"| F1\n" +
		"```\n" +
		"\n" +
		"</details>"
	if got := Block(b, "4601"); got != want {
		t.Errorf("Block bytes drifted\n got:\n%s\nwant:\n%s", got, want)
	}
}

// traceBundle is nine changed files whose added lines name two of them and two
// unchanged paths.
func traceBundle() Bundle {
	paths := []string{
		"src/app.ts", "src/theme.ts", "src/api.ts", "src/cli.ts", "src/db.ts",
		"src/log.ts", "src/props.ts", "src/util.ts", "docs/notes.md",
	}
	files := make([]File, 0, len(paths))
	for i, p := range paths {
		files = append(files, File{ID: "F" + strconv.Itoa(i+1), Path: p, Role: "source"})
	}
	return Bundle{Files: files, Added: [][2]string{
		{"src/app.ts", `import { theme } from "./theme.ts";`},
		{"docs/notes.md", "see src/api.ts for the shape"},
		{"src/app.ts", "README.md and CHANGELOG.md"},
	}}
}

// TestNodesAndEdgesTraceToInput proves every node comes from the file list and
// every edge from an added line naming another changed file: an unchanged path
// named on a diff line yields nothing.
func TestNodesAndEdgesTraceToInput(t *testing.T) {
	t.Parallel()
	block := Block(traceBundle(), "4602")
	assertTraceNodes(t, block, 9)
	want := []string{
		"    F1 -->|\"uses theme.ts\"| F2\n",
		"    F9 -->|\"uses api.ts\"| F3\n",
	}
	for _, edge := range want {
		if !strings.Contains(block, edge) {
			t.Errorf("block is missing the derived edge %q:\n%s", edge, block)
		}
	}
	for _, unchanged := range []string{"README.md", "CHANGELOG.md"} {
		if strings.Contains(block, unchanged) {
			t.Errorf("block names the unchanged file %q:\n%s", unchanged, block)
		}
	}
}

func assertTraceNodes(t *testing.T, block string, want int) {
	t.Helper()
	for i := 1; i <= want; i++ {
		node := fmt.Sprintf("    PR --> F%d[\"F%d ", i, i)
		if !strings.Contains(block, node) {
			t.Errorf("block is missing node %q", node)
		}
	}
	if got := strings.Count(block, "    PR --> "); got != want {
		t.Errorf("block renders %d nodes, want %d", got, want)
	}
}

// capBundle is 80 changed files whose added lines name every one of them, so
// both caps bind: 60 kept nodes and 3540 candidate edges.
func capBundle() Bundle {
	const files = 80
	b := Bundle{Files: make([]File, 0, files)}
	for i := 1; i <= files; i++ {
		b.Files = append(b.Files, File{ID: "F" + strconv.Itoa(i), Path: fmt.Sprintf("pkg/f%d.txt", i), Role: "source"})
	}
	b.Added = append(droppedMentions(maxTopoNodes+1, files), nodeMentions(maxTopoNodes)...)
	return b
}

// nodeMentions names every kept node from every kept node.
func nodeMentions(nodes int) [][2]string {
	var added [][2]string
	for src := 1; src <= nodes; src++ {
		for dst := 1; dst <= nodes; dst++ {
			if src != dst {
				added = append(added, [2]string{
					fmt.Sprintf("pkg/f%d.txt", src),
					fmt.Sprintf("uses f%d.txt", dst),
				})
			}
		}
	}
	return added
}

// droppedMentions names the nodes past the cap from the first kept one, so an
// unfiltered edge derivation would draw an orphan node.
func droppedMentions(from, to int) [][2]string {
	var added [][2]string
	for dst := from; dst <= to; dst++ {
		added = append(added, [2]string{"pkg/f1.txt", fmt.Sprintf("uses f%d.txt", dst)})
	}
	return added
}

// TestBlockCaps proves the caps keep the rendered head of the bundle and never
// point an edge at a node the block does not render.
func TestBlockCaps(t *testing.T) {
	t.Parallel()
	block := Block(capBundle(), "4603")
	if node := "    PR --> F60[\"F60 f60.txt\"]"; !strings.Contains(block, node) {
		t.Errorf("block is missing the last kept node %q", node)
	}
	if strings.Contains(block, "F61") {
		t.Errorf("block renders or points at the dropped node F61:\n%s", block)
	}
	if got := strings.Count(block, " -->|\""); got != maxTopoEdges {
		t.Errorf("block renders %d edges, want the %d cap", got, maxTopoEdges)
	}
	notes := omissionNotes(t, block)
	if notes != 1 {
		t.Errorf("block carries %d omission notes, want exactly 1", notes)
	}
	if len(block) >= 12000 {
		t.Errorf("capped block is %d bytes, want under the 12000-byte budget", len(block))
	}
}

// crowdedPrefix is the ~1 KB directory prefix every crowded path carries: a
// monorepo deep enough that one edge label alone overruns a sane byte budget.
var crowdedPrefix = strings.Repeat("deeply/nested/segment/", 45)

// crowdedBundle is count files behind crowdedPrefix, every one ending in base,
// each naming the next by full path. The first file also names the last one,
// so a graph derived from a node list the count cap or the byte budget later
// trims would draw an edge to a node the block never renders.
func crowdedBundle(count int, base string) Bundle {
	b := Bundle{Files: make([]File, 0, count)}
	for i := 1; i <= count; i++ {
		b.Files = append(b.Files, File{
			ID:   "F" + strconv.Itoa(i),
			Path: crowdedPrefix + "w" + strconv.Itoa(i) + "/" + base,
			Role: "source",
		})
	}
	b.Added = append(b.Added, [2]string{b.Files[0].Path, b.Files[count-1].Path})
	for i := range count {
		b.Added = append(b.Added, [2]string{b.Files[i].Path, b.Files[(i+1)%count].Path})
	}
	return b
}

// duplicateBaseBundle is 61 files that all end in `index.ts`, one more than the
// node cap renders: the shared basename forces every edge label onto the whole
// ~1 KB path, which is the shape that used to run past GitHub's 65,536-byte
// comment limit.
func duplicateBaseBundle() Bundle { return crowdedBundle(61, "index.ts") }

// longNameBundle is 60 files that share one 400-byte basename, so the node
// labels alone spend the block's byte budget.
func longNameBundle() Bundle {
	return crowdedBundle(60, strings.Repeat("index-of-everything-", 20)+".ts")
}

// renderedNodes and renderedEdges read the block back from its own text: an
// expectation rebuilt from the builder's input would reproduce its bugs.
var (
	renderedNodes = regexp.MustCompile(`(?m)^    PR --> (F\d+)\["`)
	renderedEdges = regexp.MustCompile(`(?m)^    (F\d+) -->\|"[^"]*"\| (F\d+)$`)
)

// TestBlockByteBudget proves the block holds its byte ceiling and its framing
// whatever the bundle's path names cost: a crowded label is truncated, and a
// line that no longer fits is dropped whole so no fence is ever cut.
func TestBlockByteBudget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		bundle   func() Bundle
		name     string
		wantNote string
	}{
		{bundle: duplicateBaseBundle, name: "duplicate basename at long paths", wantNote: "more edges"},
		{bundle: longNameBundle, name: "long labels crowd out nodes", wantNote: "more files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			block := Block(tc.bundle(), "4620")
			assertBlockFraming(t, block)
			assertNoOrphanEdges(t, block, renderedNodeIDs(t, block))
			assertBlockOmission(t, block, tc.wantNote)
		})
	}
}

// assertBlockFraming proves the block holds its ceiling with both fences and
// the whole <details> region intact.
func assertBlockFraming(t *testing.T, block string) {
	t.Helper()
	if len(block) > 12000 {
		t.Errorf("block is %d bytes, want at most the 12000-byte ceiling", len(block))
	}
	if !strings.HasPrefix(block, "<details>\n") || !strings.HasSuffix(block, "\n```\n\n</details>") {
		t.Errorf("block framing was cut: starts %q, ends %q",
			block[:min(len(block), 12)], block[max(len(block)-16, 0):])
	}
	if !strings.Contains(block, "```mermaid\nflowchart TD\n") {
		t.Errorf("block lost its mermaid fence:\n%s", block)
	}
}

// assertBlockOmission proves a crowded block renders nodes, names what the
// byte budget left out, and shortened the labels that crowded it.
func assertBlockOmission(t *testing.T, block, wantNote string) {
	t.Helper()
	if kept := strings.Count(block, "    PR --> "); kept == 0 || kept > 60 {
		t.Errorf("block renders %d nodes, want 1..60", kept)
	}
	if note := omissionNoteLine(t, block); !strings.Contains(note, wantNote) {
		t.Errorf("omission note %q does not name the %q the byte budget left out", note, wantNote)
	}
	if !strings.Contains(block, "...") {
		t.Error("no label was truncated, so the fixture never crowded the renderer")
	}
}

// renderedNodeIDs reads the node ids back from the rendered block.
func renderedNodeIDs(t *testing.T, block string) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	for _, m := range renderedNodes.FindAllStringSubmatch(block, -1) {
		ids[m[1]] = true
	}
	return ids
}

// assertNoOrphanEdges proves every edge endpoint is a node the block renders.
func assertNoOrphanEdges(t *testing.T, block string, nodes map[string]bool) {
	t.Helper()
	for _, m := range renderedEdges.FindAllStringSubmatch(block, -1) {
		for _, id := range m[1:] {
			if !nodes[id] {
				t.Errorf("edge %s --> %s points at the unrendered node %s", m[1], m[2], id)
			}
		}
	}
}

// omissionNoteLine returns the block's one mermaid comment, which must exist
// once the byte budget leaves anything out.
func omissionNoteLine(t *testing.T, block string) string {
	t.Helper()
	note := ""
	for line := range strings.SplitSeq(block, "\n") {
		if strings.HasPrefix(line, "    %% ") {
			note = line
		}
	}
	if note == "" {
		t.Error("block carries no omission note, want one naming what the byte budget left out")
	}
	return note
}

// omissionNotes counts the block's mermaid comments, each of which must name
// the order the caps kept.
func omissionNotes(t *testing.T, block string) int {
	t.Helper()
	notes := 0
	for line := range strings.SplitSeq(block, "\n") {
		if !strings.HasPrefix(line, "    %% ") {
			continue
		}
		notes++
		if !strings.HasSuffix(line, "omitted (bundle order)") {
			t.Errorf("omission note %q does not name the kept order", line)
		}
	}
	return notes
}

// TestBlockSkipsEmptyBundle keeps an empty file list from rendering an empty
// <details> shell.
func TestBlockSkipsEmptyBundle(t *testing.T) {
	t.Parallel()
	if got := Block(Bundle{}, "4604"); got != "" {
		t.Errorf("Block(empty) = %q, want no block", got)
	}
}

// TestLoadBundle pins the tolerant reader: PR.json counts as strings, role
// headings, hunk snapshots, and each unusable `files` shape — a string or a
// number truncates the rows, null and absence fall back to the diff headers.
func TestLoadBundle(t *testing.T) {
	t.Parallel()
	cases := loadBundleCases()
	for i := range cases {
		t.Run(cases[i].name, func(t *testing.T) {
			t.Parallel()
			assertBundle(t, LoadBundle(writeBundleDir(t, cases[i].files)), &cases[i])
		})
	}
}

type loadBundleCase struct {
	name      string
	files     map[string]string
	wantFiles []File
	wantAdded [][2]string
	wantCount int
}

// twoFileDiff adds a deletions-only schema.json section to the app.ts diff.
const twoFileDiff = topologyDiff + `diff --git a/schema.json b/schema.json
--- a/schema.json
+++ b/schema.json
@@ -5,3 +5,0 @@
-old a
-old b
-old c
`

func loadBundleCases() []loadBundleCase {
	return []loadBundleCase{
		{
			name: "string counts and the hunk snapshot",
			files: map[string]string{
				"PR.json":   `{"files":[{"path":"src/app.ts","additions":"12","deletions":"0"}]}`,
				"full.diff": topologyDiff,
				"hunks.tsv": "src/app.ts\tNEW\t1\t3\nsrc/app.ts\tNEW\t10\t12\n",
			},
			wantFiles: []File{{ID: "F1", Path: "src/app.ts", Role: "source", Adds: 12, Dels: 0, Hunks: 2}},
			wantAdded: [][2]string{
				{"src/app.ts", `import { theme } from "./theme";`},
				{"src/app.ts", `const accent = "blue";`},
			},
			wantCount: 1,
		},
		{
			name: "role headings",
			files: map[string]string{
				"PR.json":   `{"files":[{"path":"schema.json"},{"path":"test/app.test.ts"}]}`,
				"full.diff": twoFileDiff,
				"roles.txt": "## config\n- schema.json\n\n## test\n- test/app.test.ts\n",
			},
			wantFiles: []File{
				{ID: "F1", Path: "schema.json", Role: "config", Hunks: 1},
				{ID: "F2", Path: "test/app.test.ts", Role: "test"},
			},
			wantAdded: [][2]string{
				{"src/app.ts", `import { theme } from "./theme";`},
				{"src/app.ts", `const accent = "blue";`},
			},
			wantCount: 2,
		},
		{
			name: "files as a string truncates the rows",
			files: map[string]string{
				"PR.json":   `{"files":"x"}`,
				"full.diff": topologyDiff,
			},
			wantAdded: [][2]string{
				{"src/app.ts", `import { theme } from "./theme";`},
				{"src/app.ts", `const accent = "blue";`},
			},
			wantCount: 1,
		},
		{
			name: "numeric files counts without rows",
			files: map[string]string{
				"PR.json":   `{"files":7}`,
				"full.diff": topologyDiff,
			},
			wantAdded: [][2]string{
				{"src/app.ts", `import { theme } from "./theme";`},
				{"src/app.ts", `const accent = "blue";`},
			},
			wantCount: 7,
		},
		{
			name: "null files keeps the gate closed at zero",
			files: map[string]string{
				"PR.json":   `{"files":null}`,
				"full.diff": topologyDiff,
			},
			wantFiles: []File{{ID: "F1", Path: "src/app.ts", Role: "source", Adds: 2, Dels: 0, Hunks: 2}},
			wantAdded: [][2]string{
				{"src/app.ts", `import { theme } from "./theme";`},
				{"src/app.ts", `const accent = "blue";`},
			},
			wantCount: 0,
		},
		{
			name:  "no PR.json reads the diff",
			files: map[string]string{"full.diff": twoFileDiff},
			wantFiles: []File{
				{ID: "F1", Path: "src/app.ts", Role: "source", Adds: 2, Dels: 0, Hunks: 2},
				{ID: "F2", Path: "schema.json", Role: "source", Adds: 0, Dels: 3, Hunks: 1},
			},
			wantAdded: [][2]string{
				{"src/app.ts", `import { theme } from "./theme";`},
				{"src/app.ts", `const accent = "blue";`},
			},
			wantCount: 2,
		},
	}
}

func assertBundle(t *testing.T, got Bundle, want *loadBundleCase) {
	t.Helper()
	if !slices.Equal(got.Files, want.wantFiles) {
		t.Errorf("Files = %+v, want %+v", got.Files, want.wantFiles)
	}
	if !slices.Equal(got.Added, want.wantAdded) {
		t.Errorf("Added = %v, want %v", got.Added, want.wantAdded)
	}
	if got.FileCount != want.wantCount {
		t.Errorf("FileCount = %d, want %d", got.FileCount, want.wantCount)
	}
}

// TestLoadBundleDiffBytes pins the byte measure the size gate reads.
func TestLoadBundleDiffBytes(t *testing.T) {
	t.Parallel()
	b := LoadBundle(writeBundleDir(t, map[string]string{"full.diff": topologyDiff}))
	if b.DiffBytes != len(topologyDiff) {
		t.Errorf("DiffBytes = %d, want %d", b.DiffBytes, len(topologyDiff))
	}
}
