package trendcore

import (
	"os"
	"path/filepath"
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
	if err := os.WriteFile(filepath.Join(dir, "calibration.json"), []byte(`{"schema_version":"1","precision":{"fixed":3,"judged":5}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "panel"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
