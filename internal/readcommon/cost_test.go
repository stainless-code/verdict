package readcommon

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestCostFilesListsBundleAndChunks pins the file list the rollup and the
// cost-name repair share: the bundle's own record first, then each chunk
// record once, sorted — a chunk named by the manifest and found by the glob
// must not be read twice, because pricing it twice would double real spend.
func TestCostFilesListsBundleAndChunks(t *testing.T) {
	dir := t.TempDir()
	writeCostFixture(t, dir, CostFile, `{"models":[]}`)
	writeCostFixture(t, dir, "chunks/manifest.json", `{"chunks":[{"name":"chunk-2"}]}`)
	writeCostFixture(t, dir, "chunks/chunk-1.cost.json", `{"models":[]}`)
	writeCostFixture(t, dir, "chunks/chunk-2.cost.json", `{"models":[]}`)
	writeCostFixture(t, dir, "chunks/notes.txt", "not a cost record")

	want := []string{
		filepath.Join(dir, CostFile),
		dir + "/chunks/chunk-1.cost.json",
		dir + "/chunks/chunk-2.cost.json",
	}
	got := CostFiles(dir)
	if len(got) != len(want) {
		t.Fatalf("CostFiles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CostFiles[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestCostFilesWithoutChunks pins the list as candidates, not as existing
// files: the bundle's own record is named whether or not it is on disk, so a
// caller decides how to treat its absence, and a bundle with no chunk
// directory contributes no chunk paths at all.
func TestCostFilesWithoutChunks(t *testing.T) {
	for _, tt := range []struct {
		prepare func(t *testing.T, dir string)
		name    string
	}{
		{
			name:    "empty bundle",
			prepare: func(*testing.T, string) {},
		},
		{
			name: "bundle record only",
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, CostFile), []byte(`{"models":[]}`), 0o644); err != nil {
					t.Fatalf("write %s: %v", CostFile, err)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.prepare(t, dir)
			got := CostFiles(dir)
			if len(got) != 1 || got[0] != filepath.Join(dir, CostFile) {
				t.Fatalf("CostFiles = %v, want only the bundle record path", got)
			}
		})
	}
}

// writeCostFixture writes one relative file under dir.
func writeCostFixture(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// objectElements counts the record objects a document carries, which is what an
// accepted classification must return: a lenient classifier returning too few
// records would name part of a file and call it repaired.
func objectElements(doc any) int {
	switch d := doc.(type) {
	case *jq.Obj:
		return 1
	case []any:
		n := 0
		for _, item := range d {
			if _, isObj := item.(*jq.Obj); isObj {
				n++
			}
		}
		return n
	}
	return 0
}

// TestCostRecordObjectsPinsTheRollupRule holds the classifier to the rollup's
// own verdict: a shape it accepts must price without an unreadable warning, and
// a shape it rejects must be exactly the one the rollup warns about. The
// `record --cost-names` repair refuses on the same predicate, so the two cannot
// drift into disagreeing about what a cost record is.
func TestCostRecordObjectsPinsTheRollupRule(t *testing.T) {
	cases := map[string]string{
		"record object":         `{"models":[{"input_tokens":1}]}`,
		"array of records":      `[{"models":[{"input_tokens":1}]},{"models":[{"input_tokens":2}]}]`,
		"empty array":           `[]`,
		"record without models": `{}`,
		"scalar document":       `5`,
		"string document":       `"text"`,
		"array with junk":       `[5,{"models":[{"input_tokens":1}]}]`,
		"array of arrays":       `[[1]]`,
		"null document":         `null`,
		"array of null":         `[null]`,
		"null then a record":    `[null,{"models":[{"input_tokens":1}]}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			classifyAndPrice(t, body)
		})
	}
}

// classifyAndPrice holds one document to both readers and fails when they
// disagree: accepted shapes must price, rejected shapes must warn unreadable.
func classifyAndPrice(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, CostFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := jq.DecodeOrdered([]byte(body))
	if err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	recs, accepted := CostRecordObjects(doc)
	rollup := CostRollup(dir)
	unreadable := slices.Contains(rollup.Warns, warnCostUnreadable)
	if accepted == unreadable {
		t.Fatalf("classifier and rollup disagree on %s: accepted=%v recs=%d warns=%v", body, accepted, len(recs), rollup.Warns)
	}
	if accepted && len(recs) != objectElements(doc) {
		t.Fatalf("%s yields %d records for %d record objects", body, len(recs), objectElements(doc))
	}
}

// TestCostRollupKeepsChunkSpendWhenTopIsUnreadable pins the top-level half of
// the chunk-loss guard: an unreadable cost.json must not void the spend of a
// chunk that prices on its own, and it must blame itself exactly once.
func TestCostRollupKeepsChunkSpendWhenTopIsUnreadable(t *testing.T) {
	dir := t.TempDir()
	writeCostFixture(t, dir, CostFile, `{"models":{"a":1}}`)
	writeCostFixture(t, dir, "chunks/manifest.json", `{"chunks":[{"name":"c1"}]}`)
	writeCostFixture(t, dir, "chunks/c1.cost.json",
		`[{"models":[{"input_tokens":10,"output_tokens":5,"est_usd":0.02}]}]`)
	res := CostRollup(dir)
	text := fmt.Sprint(res.Cost)
	for _, want := range []string{"input_tokens:10", "output_tokens:5"} {
		if !strings.Contains(text, want) {
			t.Fatalf("cost = %s, want the chunk's %s", text, want)
		}
	}
	if n := countWarn(res.Warns, warnCostUnreadable); n != 1 {
		t.Fatalf("warns = %v, want %q exactly once", res.Warns, warnCostUnreadable)
	}
}

// countWarn counts exact warn lines, so one repeated warning fails the test.
func countWarn(warns []string, want string) int {
	n := 0
	for _, w := range warns {
		if w == want {
			n++
		}
	}
	return n
}
