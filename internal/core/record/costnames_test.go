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
			name: "names every record of an array document",
			files: map[string]string{
				readcommon.CostFile: `[{"models":[{"model":"m1","input":1}]},{"models":[{"model":"m2","input":2}]}]`,
			},
			panel:     []string{"lead", "lens-1"},
			wantNamed: 2,
			want: map[string]string{
				readcommon.CostFile: `[{"models":[{"model":"m1","input":1,"name":"lead"}]},
					{"models":[{"model":"m2","input":2,"name":"lens-1"}]}]`,
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
	named, ref := NameCostModels(dir, tc.panel, tc.dryRun)
	checkCostRefusal(t, tc, named, ref)
	checkCostFiles(t, dir, tc.want)
	if tc.wantCode != "" || tc.dryRun {
		return
	}
	again, ref := NameCostModels(dir, tc.panel, false)
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
	named, ref := NameCostModels(t.TempDir(), []string{"lead"}, false)
	if ref != nil {
		t.Fatalf("an absent record must read as nothing to do, got %q: %s", ref.Code, ref.Message)
	}
	if named != 0 {
		t.Fatalf("named %d entries in an empty bundle", named)
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
	named, ref := NameCostModels(dir, []string{"lead", "lens-1"}, false)
	if ref != nil || named != 0 {
		t.Fatalf("a fully named record must be a no-op, got named=%d refusal=%v", named, ref)
	}
	if got := readCostFile(t, filepath.Join(dir, readcommon.CostFile)); canonical(t, got) != canonical(t, body) {
		t.Fatalf("a no-op pass rewrote the file: %s", got)
	}
}

// TestNameCostModelsRefusesNameOutsidePanel pins a name the panel does not
// list as a contradiction, not as something to overwrite.
func TestNameCostModelsRefusesNameOutsidePanel(t *testing.T) {
	dir := t.TempDir()
	writeCostFile(t, filepath.Join(dir, readcommon.CostFile),
		`{"models":[{"name":"intruder","model":"m1","input":1}]}`)
	_, ref := NameCostModels(dir, []string{"lead"}, false)
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
