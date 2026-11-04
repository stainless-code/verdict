package reportcore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
)

// benchOverviewShapes spans the multi-file gate, a review-scale PR, and a
// monorepo-sized one.
var benchOverviewShapes = []int{defaultMaxFiles + 1, 200, 2000}

func benchSectionHead(i int) string {
	return fmt.Sprintf("diff --git a/pkg/f%04d.txt b/pkg/f%04d.txt\n"+
		"index 1111111..2222222 100644\n--- a/pkg/f%04d.txt\n+++ b/pkg/f%04d.txt\n", i, i, i, i)
}

func benchHunkHead(lines int) string {
	return fmt.Sprintf("@@ -1,%d +1,%d @@\n", lines, lines)
}

func benchBodyLine(i, files, j int) string {
	if j%12 == 0 {
		return fmt.Sprintf("+added line %d mentioning f%04d.txt\n", j, (i+1)%files)
	}
	return " context line padding the synthetic diff to review scale\n"
}

func writeBenchSection(sb *strings.Builder, i, files, budget int) {
	head := benchSectionHead(i)
	var body strings.Builder
	lines := 0
	for {
		line := benchBodyLine(i, files, lines)
		if len(head)+len(benchHunkHead(lines+1))+body.Len()+len(line) > budget {
			break
		}
		body.WriteString(line)
		lines++
	}
	if room := budget - len(head) - len(benchHunkHead(lines+1)) - body.Len(); room >= 2 {
		body.WriteString(" ")
		body.WriteString(strings.Repeat("x", room-2))
		body.WriteString(newline)
		lines++
	}
	sb.WriteString(head)
	sb.WriteString(benchHunkHead(lines))
	sb.WriteString(body.String())
}

func benchOverviewDiff(files, limit int) string {
	var sb strings.Builder
	sb.Grow(limit)
	perFile := limit / files
	for i := range files {
		budget := perFile
		if i == files-1 {
			budget = limit - sb.Len()
		}
		writeBenchSection(&sb, i, files, budget)
	}
	return sb.String()
}

// BenchmarkRenderOverview measures the collapsed file table plus topology
// block over synthetic many-file diffs held at the fetched-diff byte cap, with
// no hunks.tsv snapshot, so every hunk count comes from the diff fallback.
func BenchmarkRenderOverview(b *testing.B) {
	for _, files := range benchOverviewShapes {
		b.Run(fmt.Sprintf("files=%d", files), func(b *testing.B) {
			runOverviewBench(b, files)
		})
	}
}

func runOverviewBench(b *testing.B, files int) {
	b.Helper()
	dir := b.TempDir()
	b.Chdir(dir)
	diff := benchOverviewDiff(files, fetchcore.DefaultDiffCap)
	if len(diff) != fetchcore.DefaultDiffCap {
		b.Fatalf("fixture is %d bytes, want exactly the %d-byte cap fetch accepts",
			len(diff), fetchcore.DefaultDiffCap)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o644); err != nil {
		b.Fatal(err)
	}
	st := &state{
		dir: dir,
		req: &core.ReportRequest{Effective: core.Effective{
			PublishOverview:    strTrue,
			DiagramsStructural: strTrue,
		}},
	}
	b.SetBytes(int64(len(diff)))
	b.ReportAllocs()
	for b.Loop() {
		if renderOverview(st) == "" {
			b.Fatal("overview rendered empty")
		}
	}
}
