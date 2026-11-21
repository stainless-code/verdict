package recordcore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/testkit"
)

// multiFileDiff builds a diff over files changed lines, one file each: the
// diagram gate counts changed files, so the fixture size is the gate's input.
func multiFileDiff(files int) string {
	var b strings.Builder
	for i := range files {
		name := "file" + string(rune('a'+i)) + ".txt"
		b.WriteString("diff --git a/" + name + " b/" + name + "\n")
		b.WriteString("index 1111111..2222222 100644\n")
		b.WriteString("--- a/" + name + "\n")
		b.WriteString("+++ b/" + name + "\n")
		b.WriteString("@@ -1,3 +1,3 @@\n")
		b.WriteString(" top\n-old line\n+new line\n bottom\n")
	}
	return b.String()
}

// bigBundle writes one local bundle carrying files changed files.
func bigBundle(t *testing.T, files int) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	testkit.WriteFile(t, ".verdict/state/local-big/source.json",
		`{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`)
	testkit.WriteFile(t, ".verdict/state/local-big/full.diff", multiFileDiff(files))
	testkit.WriteFile(t, ".verdict/state/local-big/review.json", multiFileReview(files))
}

// multiFileReview is one review over multiFileDiff: one finding quoting the
// changed line of the first file, and one read receipt per changed hunk, which
// is what the evidence pass asks a review of that diff to state.
func multiFileReview(files int) string {
	receipts := make([]string, 0, files)
	for i := range files {
		name := "file" + string(rune('a'+i)) + ".txt"
		receipts = append(receipts, `{"hunk":"`+name+`\tNEW\t1\t3","quote":"new line"}`)
	}
	return `{"schema_version":"1","summary":"Nit: one note per file.","findings":[` +
		`{"path":"filea.txt","line":2,"side":"RIGHT","claim":"Nit: rename this line.",` +
		`"quote":"new line","confidence":"high","severity":"informational","rigor":"walked"}],` +
		`"receipts":[` + strings.Join(receipts, ",") + `]}`
}

// TestRunCarriesTheRenderedBody proves the record response hands a local
// renderer the exact body post would publish: the coverage line, the footer,
// and the finding are all in it, and it is never wire data (`json:"-"`).
func TestRunCarriesTheRenderedBody(t *testing.T) {
	bundle(t, "local-body", reviewJSON(""))
	c := &testkit.Collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-body"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	for _, want := range []string{
		"Cited finding here with enough substance.",
		core.FooterDivider,
		"Files reviewed: 1",
	} {
		if !strings.Contains(resp.Body, want) {
			t.Errorf("body is missing %q:\n%s", want, resp.Body)
		}
	}
	// One file stays below the diagram gate, so the body carries no topology:
	// the renderer that wants one for a small change adds it itself.
	if strings.Contains(resp.Body, "Change topology") {
		t.Errorf("body carries a topology block below the size gate:\n%s", resp.Body)
	}
}

// TestRunBodyCarriesTopologyAboveTheGate proves the other half of the same
// contract: over the gate the body itself carries the computed topology, so the
// demo must not append a second copy.
func TestRunBodyCarriesTopologyAboveTheGate(t *testing.T) {
	bigBundle(t, 9)
	c := &testkit.Collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-big"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !strings.Contains(resp.Body, "Change topology (computed from the diff)") {
		t.Errorf("body carries no topology block above the size gate:\n%s", resp.Body)
	}
	if n := strings.Count(resp.Body, "Change topology"); n != 1 {
		t.Errorf("body carries %d topology blocks, want 1", n)
	}
}

// TestDryRunCarriesTheSameBody proves the dry run (what the demo reads for a
// GitHub PR) renders the same body: the URL form must show exactly the review a
// publication would carry, minus the publication.
func TestDryRunCarriesTheSameBody(t *testing.T) {
	bundle(t, "local-drybody", reviewJSON(""))
	c := &testkit.Collector{}
	req := recordRequest(t, c, "local-drybody")
	req.DryRun = true
	resp, ref := recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !strings.Contains(resp.Body, "Cited finding here with enough substance.") {
		t.Errorf("dry-run body is missing the finding:\n%s", resp.Body)
	}
	if resp.Data == nil || !resp.Data.DryRun {
		t.Error("dry-run response does not mark the data as a dry run")
	}
}
