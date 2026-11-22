package schemagen

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// TestRuntimeReportCompareValidates proves the --compare payload validates
// against the generated reportData definition and round-trips through the
// modeled shape, so the published schema and the runtime writer cannot drift.
//
//nolint:cyclop // one assertion block per compare dimension
func TestRuntimeReportCompareValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	resp := runCompareFixture(t)
	data := resp.Data.Wire()

	validateWire(t, doc, "$defs.reportData", defOf(t, doc, "reportData"), decodeWire(t, data))

	var modeled ReportData
	unmarshalWire(t, data, &modeled)
	if modeled.Compare == nil || resp.Data.Compare == nil {
		t.Fatal("compare missing from the modeled round-trip")
	}
	got := resp.Data.Compare
	if modeled.Compare.Counts.New != got.Counts.New ||
		modeled.Compare.Counts.Persisting != got.Counts.Persisting ||
		modeled.Compare.Counts.Resolved != got.Counts.Resolved ||
		modeled.Compare.Counts.NotReviewed != got.Counts.NotReviewed ||
		modeled.Compare.Baseline.Findings != got.Baseline.Findings ||
		len(modeled.Compare.Resolved) != len(got.Resolved) ||
		len(modeled.Compare.NotReviewed) != len(got.NotReviewed) {
		t.Errorf("compare round-trip = %+v, want %+v", modeled.Compare, got)
	}
	if modeled.Compare.Resolved[0].Reason != "removed-from-diff" {
		t.Errorf("resolved reason = %q", modeled.Compare.Resolved[0].Reason)
	}
	changes := modeled.Compare.Persisting[0].Changes
	if len(changes) != 2 || changes[0] != "severity: important→critical" ||
		changes[1] != "status: open→addressed" {
		t.Errorf("persisting changes = %v", changes)
	}

	env := core.RenderEnvelope("report", "acme/app", "5101", resp.At, resp.Next,
		resp.Warnings, data, nil)
	validateWire(t, doc, "$", doc, decodeWire(t, env))
}

// runCompareFixture builds a provider PR target and a local baseline, then
// runs report with --compare.
func runCompareFixture(t *testing.T) *reportcore.Response {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	base := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for path, body := range map[string]string{
		".verdict/state/5101/PR.json": `{"state":"OPEN","headRefOid":"` + head + `","baseRefOid":"` + base + `"}`,
		".verdict/state/5101/findings.json": `[
 {"kind":"inline","path":"a.go","side":"RIGHT","line":2,"body":"kept","severity":"critical","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01","status":"addressed"},
 {"kind":"inline","path":"b.go","side":"RIGHT","line":2,"body":"new","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa02","status":"open"}]`,
		".verdict/state/5101/full.diff": `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@
 a
-b
+B
 c
`,
		".verdict/state/5101/review.json": `{"coverage":[{"file":"a.go","ranges":"all"}]}`,
		".verdict/state/local-prior/findings.json": `[
 {"kind":"inline","path":"a.go","side":"RIGHT","line":2,"body":"kept","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01","status":"open"},
 {"kind":"inline","path":"gone.go","side":"RIGHT","line":2,"body":"gone","severity":"important","sig":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa03","status":"open"}]`,
		".verdict/state/local-prior/source.json": `{"repo":"acme/app"}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		Runtime: &core.Runtime{Now: func() time.Time { return now }},
		Repo:    "acme/app",
		Pr:      "5101",
		Flags:   reportcore.Flags{Compare: "local-prior"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	return resp
}
