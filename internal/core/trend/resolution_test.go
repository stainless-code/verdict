package trendcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestTrendReviewCounts pins the published metrics contract.
func TestTrendReviewCounts(t *testing.T) {
	rows := []row{categoryRow("a"), categoryRow("b"), categoryRow("c")}
	for range 9 {
		rows = append(rows, categoryRow("finding", "security", "style"))
	}
	out := rollupRows(rows)
	doc := rollupDoc(&out)
	for k, want := range map[string]any{"reviews": 12, "reviews_clean": 3, "reviews_with_findings": 9, "findings_per_review": 1.5, "clean_rate": 0.25} {
		if got, _ := doc.Get(k); got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}
	empty := rollupRows(nil)
	for _, k := range []string{"clean_rate", "findings_per_review"} {
		if got, _ := rollupDoc(&empty).Get(k); got != nil {
			t.Errorf("%s = %v, want null", k, got)
		}
	}
}

// TestTrendResolutionUnmeasured pins the published metrics contract.
func TestTrendResolutionUnmeasured(t *testing.T) {
	root := t.TempDir()
	request := &Request{}
	request.RepoRoot = root
	st := state{req: request}
	rows := []row{{bundle: "missing"}, {bundle: "measured", models: []any{"a", "b"}}}
	writeResolutionFixture(t, root)
	got := resolutionAudit(&st, rows)
	doc, ok := got.(*jq.Obj)
	if !ok {
		t.Fatal("missing resolution")
	}
	if n, _ := doc.Get("unmeasured"); n != 1 {
		t.Fatalf("unmeasured = %v", n)
	}
	models, _ := doc.Get("by_model")
	if m, ok := models.(*jq.Obj); !ok || len(m.Keys) != 2 {
		t.Fatalf("models = %v", models)
	}
	panels, _ := doc.Get("by_panel")
	if p, ok := panels.(*jq.Obj); !ok || p.Keys[0] != "a,b" {
		t.Fatalf("panels = %v", panels)
	}
	empty := state{req: st.req}
	if resolutionAudit(&empty, []row{{bundle: "missing"}}) != nil {
		t.Fatal("missing receipt must render null")
	}
}

func writeResolutionFixture(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "state", "measured")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calibration.json"), []byte(validResolutionReceipt), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "panel"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const validResolutionReceipt = `{
 "schema_version":"1","bundle":"measured","as_of":"2026-11-03T12:00:00Z",
 "review_id":7,"head":{},"pr":{},"outcomes":[],"provenance":{},
 "precision":{"fixed":3,"resolved_unfixed":1,"open":1,"unknown":1,
 "judged":5,"sample_size":6,"resolution_rate":0.6,"judge":"human",
 "as_of":"2026-11-03T12:00:00Z","by_judge":{},"by_tier":{},
 "by_severity":{},"by_rule":{},"by_category":{}}
}`

// TestTrendResolutionMalformedReceipt keeps invalid audits out of every rate.
func TestTrendResolutionMalformedReceipt(t *testing.T) {
	for _, receipt := range []string{
		`{"schema_version":"1","precision":{"fixed":3,"judged":5,"open":"broken","resolution_rate":"broken"}}`,
		strings.Replace(validResolutionReceipt, `"fixed":3`, `"fixed":"broken"`, 1),
		strings.Replace(validResolutionReceipt, `"open":1`, `"open":"broken"`, 1),
		strings.Replace(validResolutionReceipt, `"resolution_rate":0.6`, `"resolution_rate":"broken"`, 1),
		strings.Replace(validResolutionReceipt, `"judged":5`, `"judged":4`, 1),
		strings.Replace(validResolutionReceipt, `"sample_size":6`, `"sample_size":7`, 1),
		strings.Replace(validResolutionReceipt, `"resolution_rate":0.6`, `"resolution_rate":0.5`, 1),
	} {
		t.Run(receipt, func(t *testing.T) { checkMalformedReceipt(t, receipt) })
	}
}

func checkMalformedReceipt(t *testing.T, receipt string) {
	t.Helper()
	root := t.TempDir()
	writeResolutionFixture(t, root)
	dir := filepath.Join(root, ".verdict", "state", "malformed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calibration.json"), []byte(receipt), 0o644); err != nil {
		t.Fatal(err)
	}
	request := &Request{}
	request.RepoRoot = root
	st := state{req: request}
	got := resolutionAudit(&st, []row{{bundle: "measured"}, {bundle: "malformed"}})
	want := `{"by_model":{"unmarked":{"reviews":1,"judged":5,"fixed":3,"resolution_rate":0.6,"n":5}},"by_panel":{"a,b":{"reviews":1,"judged":5,"fixed":3,"resolution_rate":0.6,"n":5}},"unmeasured":1}`
	if doc, ok := got.(*jq.Obj); !ok || doc.MarshalCompact() != want {
		t.Fatalf("resolution = %s, want one measured and one unmeasured review", jq.PrettyOrdered(got))
	}
	if len(st.warnings) != 1 || st.warnings[0] != "resolution unmeasured: malformed (missing or malformed calibration.json)" {
		t.Fatalf("warnings = %v, want exactly one malformed receipt warning", st.warnings)
	}
}
