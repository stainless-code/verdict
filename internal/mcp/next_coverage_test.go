package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
)

// coverage on the next tool is a read mode, not a decoration. The decoder sets
// the flag and the tools list advertises it, so a dispatch that ignores it
// returns the findings item and exits 0: the caller believes it read a coverage
// report it never received.
func TestNextToolDispatchesCoverageRead(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := ".verdict/state/local-cov"
	diff := "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n" +
		"@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n"
	writeFile(t, dir+"/source.json", `{"head_sha":"`+strings.Repeat("a", 40)+
		`","base_sha":"`+strings.Repeat("b", 40)+`","head_ref":"work"}`)
	writeFile(t, dir+"/full.diff", diff)
	writeFile(t, dir+"/hunks.tsv", hunks.Format(hunks.Parse(diff)))
	writeFile(t, dir+"/review.json", `{"schema_version":"1",`+
		`"body":"> [!IMPORTANT]\n> Coverage read fixture with enough length to pass.","approved":false,`+
		`"coverage":[{"file":"notes.txt","ranges":"1-3"}],`+
		`"comments":[{"path":"notes.txt","line":2,"side":"RIGHT",`+
		`"body":"Cited finding here with enough substance.","rigor":"walked","severity":"important"}]}`)

	registered := findTool(t, "next")
	req := &core.NextRequest{Coverage: true}
	req.ReadRequest = core.ReadRequest{Repo: "acme/app", Pr: "local-cov"}
	data, ref := registered.invoke(context.Background(), req)
	if ref != nil {
		t.Fatalf("coverage invoke refusal: %v", ref)
	}
	if _, ok := data.Get("item"); ok {
		t.Error("a coverage request returned the findings item: the flag was dropped at dispatch")
	}
	cov, ok := data.Get("coverage")
	if !ok {
		t.Fatal("coverage request returned no coverage report")
	}
	rep, ok := cov.(*core.CoverageReport)
	if !ok {
		t.Fatalf("coverage is %T, want *core.CoverageReport", cov)
	}
	if rep.Percent == nil || *rep.Percent != 100 {
		t.Errorf("coverage percent = %v, want 100", rep.Percent)
	}
	if rep.Total != 3 {
		t.Errorf("coverage total = %d, want 3", rep.Total)
	}
}
