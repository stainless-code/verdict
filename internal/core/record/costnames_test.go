package recordcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// costNameCase is one --cost-names repair over a temp-dir bundle. want holds
// the files the pass must leave behind, as literal JSON: the expectation is
// independent of the repair's own encoder, so a reordering or a dropped
// count fails even when the names are right.
type costNameCase struct {
	name      string
	wantCode  string
	files     map[string]string
	want      map[string]string
	panel     []string
	wantNamed int
	dryRun    bool
}

// TestNameCostModels pins one repair per cost-record shape: naming,
// agreement, contradiction, the object shape, unequal counts, dry runs, and
// the per-chunk pass — each with the exact bytes the file must hold after.
func TestNameCostModels(t *testing.T) {
	cases := []costNameCase{
		{
			name: "names unnamed entries and keeps every count",
			files: map[string]string{
				readcommon.CostFile: `{"at":"2026-01-01T00:00:00Z","models":[
					{"model":"m1","role":"synthesizer","input":10,"output":2},
					{"model":"m2","role":"lens","input":5,"output":1}]}`,
			},
			panel:     []string{"lead", "lens-1"},
			wantNamed: 2,
			want: map[string]string{
				readcommon.CostFile: `{"at":"2026-01-01T00:00:00Z","models":[
					{"model":"m1","role":"synthesizer","input":10,"output":2,"name":"lead"},
					{"model":"m2","role":"lens","input":5,"output":1,"name":"lens-1"}]}`,
			},
		},
		{
			name: "keeps a name the panel agrees with",
			files: map[string]string{
				readcommon.CostFile: `{"models":[
					{"name":"lead","model":"m1","input":1},
					{"model":"m2","input":2}]}`,
			},
			panel:     []string{"lead", "lens-1"},
			wantNamed: 1,
			want: map[string]string{
				readcommon.CostFile: `{"models":[
					{"name":"lead","model":"m1","input":1},
					{"model":"m2","input":2,"name":"lens-1"}]}`,
			},
		},
		{
			name: "refuses a name the panel contradicts and writes nothing",
			files: map[string]string{
				readcommon.CostFile: `{"models":[{"name":"intruder","model":"m1","input":1}]}`,
			},
			panel:    []string{"lead"},
			wantCode: codeCostEntryContradict,
			want: map[string]string{
				readcommon.CostFile: `{"models":[{"name":"intruder","model":"m1","input":1}]}`,
			},
		},
		{
			name: "refuses the object shape and writes nothing",
			files: map[string]string{
				readcommon.CostFile: `{"models":{"m1":{"input":1}}}`,
			},
			panel:    []string{"lead"},
			wantCode: codeCostModelsNotArray,
			want: map[string]string{
				readcommon.CostFile: `{"models":{"m1":{"input":1}}}`,
			},
		},
		{
			name: "refuses a panel shorter than the entries",
			files: map[string]string{
				readcommon.CostFile: `{"models":[
					{"model":"m1","input":1},
					{"model":"m2","input":2}]}`,
			},
			panel:    []string{"lead"},
			wantCode: codeCostEntryUnnamed,
			want: map[string]string{
				readcommon.CostFile: `{"models":[
					{"model":"m1","input":1},
					{"model":"m2","input":2}]}`,
			},
		},
		{
			name: "refuses with no panel at all",
			files: map[string]string{
				readcommon.CostFile: `{"models":[{"model":"m1","input":1}]}`,
			},
			wantCode: codeCostPanelUnresolved,
			want: map[string]string{
				readcommon.CostFile: `{"models":[{"model":"m1","input":1}]}`,
			},
		},
		{
			name: "refuses an undecodable record",
			files: map[string]string{
				readcommon.CostFile: `{"models":`,
			},
			panel:    []string{"lead"},
			wantCode: codeCostRecordUnreadable,
			want: map[string]string{
				readcommon.CostFile: `{"models":`,
			},
		},
		{
			name: "repairs the per-chunk records in the same pass",
			files: map[string]string{
				"chunks/chunk-1.cost.json": `{"models":[{"model":"m1","input":3}]}`,
			},
			panel:     []string{"lens-1"},
			wantNamed: 1,
			want: map[string]string{
				"chunks/chunk-1.cost.json": `{"models":[{"model":"m1","input":3,"name":"lens-1"}]}`,
			},
		},
		{
			name: "names every record of an array document from the whole panel",
			files: map[string]string{
				readcommon.CostFile: `[{"models":[{"model":"m1","input":1},{"model":"m2","input":2}]},
					{"models":[{"model":"m3","input":3},{"model":"m4","input":4}]}]`,
			},
			panel:     []string{"lead", "lens-1"},
			wantNamed: 4,
			want: map[string]string{
				readcommon.CostFile: `[{"models":[{"model":"m1","input":1,"name":"lead"},{"model":"m2","input":2,"name":"lens-1"}]},
					{"models":[{"model":"m3","input":3,"name":"lead"},{"model":"m4","input":4,"name":"lens-1"}]}]`,
			},
		},
		{
			name: "refuses when one record of an array cannot take the whole panel",
			files: map[string]string{
				readcommon.CostFile: `[{"models":[{"model":"m1","input":1}]},
					{"models":[{"model":"m2","input":2},{"model":"m3","input":3}]}]`,
			},
			panel:    []string{"lead", "lens-1"},
			wantCode: codeCostEntryUnnamed,
			want: map[string]string{
				readcommon.CostFile: `[{"models":[{"model":"m1","input":1}]},
					{"models":[{"model":"m2","input":2},{"model":"m3","input":3}]}]`,
			},
		},
		{
			name: "refuses names sitting on the wrong seat",
			files: map[string]string{
				readcommon.CostFile: `{"models":[{"name":"lens-1","input":1},{"name":"lead","input":2}]}`,
			},
			panel:    []string{"lead", "lens-1"},
			wantCode: codeCostEntryContradict,
			want: map[string]string{
				readcommon.CostFile: `{"models":[{"name":"lens-1","input":1},{"name":"lead","input":2}]}`,
			},
		},
		{
			name: "refuses a panel that names one seat twice",
			files: map[string]string{
				readcommon.CostFile: `{"models":[{"input":1},{"input":2}]}`,
			},
			panel:    []string{"lead", "lead"},
			wantCode: codeCostPanelUnresolved,
			want: map[string]string{
				readcommon.CostFile: `{"models":[{"input":1},{"input":2}]}`,
			},
		},
		{
			name: "skips a null record, which the rollup prices as empty",
			files: map[string]string{
				readcommon.CostFile: `[null,{"models":[{"input":1}]}]`,
			},
			panel:     []string{"lead"},
			wantNamed: 1,
			want: map[string]string{
				readcommon.CostFile: `[null,{"models":[{"input":1,"name":"lead"}]}]`,
			},
		},
		{
			name: "a null-only document has nothing to name",
			files: map[string]string{
				readcommon.CostFile: `[null]`,
			},
			panel: []string{"lead"},
			want:  map[string]string{readcommon.CostFile: `[null]`},
		},
		{
			name: "refuses a document that is not a record at all",
			files: map[string]string{
				readcommon.CostFile: `5`,
			},
			panel:    []string{"lead"},
			wantCode: codeCostRecordUnreadable,
			want:     map[string]string{readcommon.CostFile: `5`},
		},
		{
			name: "refuses an array holding one non-record entry",
			files: map[string]string{
				readcommon.CostFile: `[5,{"models":[{"input":1}]}]`,
			},
			panel:    []string{"lead"},
			wantCode: codeCostRecordUnreadable,
			want: map[string]string{
				readcommon.CostFile: `[5,{"models":[{"input":1}]}]`,
			},
		},
		{
			name: "refuses a models entry that is not an object",
			files: map[string]string{
				readcommon.CostFile: `{"models":["junk",{"input":5}]}`,
			},
			panel:    []string{"lead"},
			wantCode: codeCostModelsNotArray,
			want: map[string]string{
				readcommon.CostFile: `{"models":["junk",{"input":5}]}`,
			},
		},
		{
			name: "a dry run names nothing",
			files: map[string]string{
				readcommon.CostFile: `{"models":[{"model":"m1","input":1}]}`,
			},
			panel:     []string{"lead"},
			dryRun:    true,
			wantNamed: 1,
			want: map[string]string{
				readcommon.CostFile: `{"models":[{"model":"m1","input":1}]}`,
			},
		},
	}
	for i := range cases {
		t.Run(cases[i].name, func(t *testing.T) { runCostNameCase(t, &cases[i]) })
	}
}

// runCostNameCase builds one fixture bundle, runs the pass, and asserts the
// count, the refusal, and the exact bytes left on disk — then proves the
// second pass changes nothing and does not refuse.
func runCostNameCase(t *testing.T, tc *costNameCase) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range tc.files {
		writeCostFile(t, filepath.Join(dir, name), body)
	}
	named, _, found, ref := NameCostModels(dir, tc.panel, tc.dryRun)
	if ref == nil && !found {
		t.Fatal("every fixture carries a cost record, so a successful pass must report found")
	}
	checkCostRefusal(t, tc, named, ref)
	checkCostFiles(t, dir, tc.want)
	if tc.wantCode != "" || tc.dryRun {
		return
	}
	again, _, _, ref := NameCostModels(dir, tc.panel, false)
	if ref != nil || again != 0 {
		t.Fatalf("second pass: named %d, refusal %v; want 0 and no refusal", again, ref)
	}
}

// checkCostRefusal asserts the refusal the case expects, and that a refusal
// always carries a message the operator can act on.
func checkCostRefusal(t *testing.T, tc *costNameCase, named int, ref *core.RefusalError) {
	t.Helper()
	checkRefusalCode(t, tc.wantCode, named, ref)
	if named != tc.wantNamed {
		t.Errorf("named %d entries, want %d", named, tc.wantNamed)
	}
}

// checkRefusalCode compares the refusal a case got with the one it expects.
func checkRefusalCode(t *testing.T, wantCode string, named int, ref *core.RefusalError) {
	t.Helper()
	if wantCode == "" {
		if ref != nil {
			t.Fatalf("want no refusal, got %q: %s", ref.Code, ref.Message)
		}
		return
	}
	if ref == nil {
		t.Fatalf("want refusal %q, got none (named %d)", wantCode, named)
	}
	if ref.Code != wantCode {
		t.Fatalf("want refusal %q, got %q: %s", wantCode, ref.Code, ref.Message)
	}
	if ref.Message == "" {
		t.Fatal("refusal carries no message")
	}
}

// checkCostFiles compares each file against its literal expectation.
func checkCostFiles(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	for name, body := range want {
		got := readCostFile(t, filepath.Join(dir, name))
		if canonical(t, got) != canonical(t, body) {
			t.Errorf("%s\n got: %s\nwant: %s", name, canonical(t, got), canonical(t, body))
		}
	}
}

// TestNameCostModelsNoCostFile pins an absent record as nothing to do: the
// repair never creates a cost file to have something to name.
func TestNameCostModelsNoCostFile(t *testing.T) {
	named, _, found, ref := NameCostModels(t.TempDir(), []string{"lead"}, false)
	if ref != nil {
		t.Fatalf("an absent record must read as nothing to do, got %q: %s", ref.Code, ref.Message)
	}
	if named != 0 {
		t.Fatalf("named %d entries in an empty bundle", named)
	}
	if found {
		t.Fatal("an empty bundle must report no cost record")
	}
}

// canonical renders JSON the way the comparison needs it: key order dropped,
// numbers preserved as their source literal.
func canonical(t *testing.T, body string) string {
	t.Helper()
	doc, err := jq.Decode([]byte(body))
	if err != nil {
		if strings.TrimSpace(body) == strings.TrimSpace(`{"models":`) {
			return strings.TrimSpace(body)
		}
		t.Fatalf("decode %q: %v", body, err)
	}
	return jq.Compact(doc)
}

func writeCostFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readCostFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// TestNameCostModelsFullyNamedDocument pins a fully named record as a
// byte-level no-op, so a re-run cannot rewrite a record it did not repair.
func TestNameCostModelsFullyNamedDocument(t *testing.T) {
	dir := t.TempDir()
	body := `{"models":[{"name":"lead","model":"m1","input":1},{"name":"lens-1","model":"m2","input":2}]}`
	writeCostFile(t, filepath.Join(dir, readcommon.CostFile), body)
	named, entries, _, ref := NameCostModels(dir, []string{"lead", "lens-1"}, false)
	if ref != nil || named != 0 {
		t.Fatalf("a fully named record must be a no-op, got named=%d refusal=%v", named, ref)
	}
	if entries != 2 {
		t.Fatalf("a no-op on a two-entry record must still report 2 entries, got %d", entries)
	}
	// Byte equality, not a decoded comparison: a decoded comparison passes while
	// the pass re-emits the file as pretty JSON, which a no-op must not do.
	if got := readCostFile(t, filepath.Join(dir, readcommon.CostFile)); got != body {
		t.Fatalf("a no-op pass rewrote the file:\n got %s\nwant %s", got, body)
	}
}

// TestNameCostModelsRefusesNameOutsidePanel pins a name the panel does not
// list as a contradiction, not as something to overwrite.
func TestNameCostModelsRefusesNameOutsidePanel(t *testing.T) {
	dir := t.TempDir()
	writeCostFile(t, filepath.Join(dir, readcommon.CostFile),
		`{"models":[{"name":"intruder","model":"m1","input":1}]}`)
	_, _, _, ref := NameCostModels(dir, []string{"lead"}, false)
	if ref == nil || ref.Code != codeCostEntryContradict {
		t.Fatalf("want %q, got %v", codeCostEntryContradict, ref)
	}
}

// TestPinnedPanelReadsResolvedDefaults pins the panel source: the resolved
// defaults a bundle recorded at fetch time, empty when it pinned none.
func TestPinnedPanelReadsResolvedDefaults(t *testing.T) {
	dir := t.TempDir()
	writeCostFile(t, filepath.Join(dir, "run.json"),
		`{"schema_version":"1","name":"x","base_sha":"abc","captured_at":"2026-01-01T00:00:00Z",
		  "resolved":{"defaults":{"values":{"panel":["lead","lens-1"]}}}}`)
	got := pinnedPanel(dir)
	if len(got) != 2 || got[0] != "lead" || got[1] != "lens-1" {
		t.Fatalf("pinnedPanel = %v, want [lead lens-1]", got)
	}
	if panel := pinnedPanel(t.TempDir()); panel != nil {
		t.Fatalf("an absent run.json must pin nothing, got %v", panel)
	}
}

// TestWrittenFiles pins the mid-pass failure sentence: an operator resuming a
// half-written pass needs the files that already landed, not a promise that
// some were kept.
func TestWrittenFiles(t *testing.T) {
	if got := writtenFiles(nil); got != "" {
		t.Fatalf("a failure before any write must add no clause at all, got %q", got)
	}
	one := writtenFiles([]string{"cost.json"})
	if !strings.Contains(one, "cost.json was rewritten") || !strings.Contains(one, "is kept") {
		t.Fatalf("one file must be named singularly, got %q", one)
	}
	two := writtenFiles([]string{"cost.json", "chunk-1.cost.json"})
	if !strings.Contains(two, "cost.json, chunk-1.cost.json were rewritten") {
		t.Fatalf("both files must be named, got %q", two)
	}
}

// TestCostNameWarning pins what the pass tells the operator, including the
// three no-op cases a user can reach: no record, a record with no entries, and
// entries that already carry their seat's name.
func TestCostNameWarning(t *testing.T) {
	cases := []struct {
		want           string
		named, entries int
		found, dryRun  bool
	}{
		{named: 0, entries: 0, found: false, want: "cost names: this bundle carries no cost record to name"},
		{named: 0, entries: 0, found: true, want: "cost names: the cost record holds no models[] entry to name"},
		{named: 0, entries: 3, found: true, want: "cost names: every models[] entry already names its panel seat"},
		{named: 2, entries: 2, found: true, dryRun: true, want: "cost names: would name 2 models[] entries"},
		{named: 2, entries: 2, found: true, want: "cost names: named 2 models[] entries"},
	}
	for _, tc := range cases {
		if got := costNameWarning(tc.named, tc.entries, tc.found, tc.dryRun); !strings.HasPrefix(got, tc.want) {
			t.Fatalf("named=%d entries=%d found=%v dryRun=%v: got %q, want prefix %q",
				tc.named, tc.entries, tc.found, tc.dryRun, got, tc.want)
		}
	}
}
