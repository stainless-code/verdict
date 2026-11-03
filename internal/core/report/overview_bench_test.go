package reportcore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// benchOverviewFiles and benchOverviewBytes size the synthetic large-diff
// fixture: a review-scale bundle whose overview block renders from one diff
// read, not one per file row.
const (
	benchOverviewFiles = 200
	benchOverviewBytes = 2 << 20
)

// benchOverviewDiff builds a synthetic diff with fileCount sections totalling
// about target bytes. Every section carries a hunk with context padding and a
// few added lines that mention a sibling, so the overview renders the full
// table plus the change-topology edges.
func benchOverviewDiff(files, target int) string {
	perFile := max(target/files, 1)
	var sb strings.Builder
	sb.Grow(target)
	for i := range files {
		fmt.Fprintf(&sb, "diff --git a/pkg/f%03d.txt b/pkg/f%03d.txt\n", i, i)
		fmt.Fprintf(&sb, "index 1111111..2222222 100644\n--- a/pkg/f%03d.txt\n+++ b/pkg/f%03d.txt\n", i, i)
		fmt.Fprintf(&sb, "@@ -1,%d +1,%d @@\n", perFile, perFile)
		for j := range perFile {
			if j%12 == 0 {
				fmt.Fprintf(&sb, "+added line %d mentioning f%03d.txt\n", j, (i+1)%files)
				continue
			}
			sb.WriteString(" context line padding the synthetic diff to review scale\n")
		}
	}
	return sb.String()
}

// BenchmarkRenderOverview measures the collapsed file table plus topology
// block over a synthetic 200-file/2 MB diff with no hunks.tsv snapshot, so
// every hunk count comes from the diff fallback.
func BenchmarkRenderOverview(b *testing.B) {
	dir := b.TempDir()
	b.Chdir(dir)
	diff := benchOverviewDiff(benchOverviewFiles, benchOverviewBytes)
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
