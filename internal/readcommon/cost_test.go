package readcommon

import (
	"os"
	"path/filepath"
	"slices"
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
	unreadable := slices.Contains(CostRollup(dir).Warns, warnCostUnreadable)
	if accepted == unreadable {
		t.Fatalf("classifier and rollup disagree on %s: accepted=%v recs=%d", body, accepted, len(recs))
	}
	if accepted {
		if arr, isArr := doc.([]any); isArr && len(arr) > 0 && len(recs) == 0 {
			t.Fatalf("%s classifies as records but yields none", body)
		}
	}
}
