package indexcore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The published scorecard: docs/benchmark.md republishes the measured table
// between these markers, and the test fails when the two differ. The header,
// separator, and column widths are literals the document holds, not a second
// rendering of the scorecard's own row type.
const (
	docsBenchmark      = "docs/benchmark.md"
	docsScorecardBegin = "<!-- scorecard:begin -->"
	docsScorecardEnd   = "<!-- scorecard:end -->"
	docsTableHeader    = "| row                         | precision | recall | matched | reported | labeled |"
	docsTableSeparator = "| --------------------------- | --------- | ------ | ------- | -------- | ------- |"
	docsTableRow       = "| %-27s | %-9s | %-6s | %-7d | %-8d | %-7d |\n"
)

// assertDocsScorecard fails when the table published in docs/benchmark.md is
// not the measured one, and under -update republishes it instead.
func assertDocsScorecard(t *testing.T, rows []scoreCard) {
	t.Helper()
	p := repoPath(t, docsBenchmark)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", docsBenchmark, err)
	}
	published := docsBlock(t, string(body))
	measured := renderDocsTable(rows)
	if published == measured {
		return
	}
	if *updateFloors {
		republishDocs(t, p, string(body), measured)
		return
	}
	for _, line := range tableDiff(published, measured) {
		t.Errorf("%s: %s", docsBenchmark, line)
	}
}

// renderDocsTable renders the measured rows as the published markdown block, in
// scorecard order: every count sits beside its rate, so a deleted label or a
// rate that rose without a note changes a cell the test compares.
func renderDocsTable(rows []scoreCard) string {
	var out strings.Builder
	out.WriteString("\n" + docsTableHeader + "\n")
	out.WriteString(docsTableSeparator + "\n")
	for _, r := range rows {
		fmt.Fprintf(&out, docsTableRow, "`"+r.Name+"`",
			ratio(r.Tally.tp, r.Tally.reported), ratio(r.Tally.tp, r.Tally.expected),
			r.Tally.tp, r.Tally.reported, r.Tally.expected)
	}
	return out.String() + "\n"
}

// docsBlock returns the scorecard lines the document publishes between its
// markers, including the blank lines the markdown formatter keeps around them.
func docsBlock(t *testing.T, body string) string {
	t.Helper()
	beg, end := docsSpan(t, body)
	return body[beg+len(docsScorecardBegin) : end]
}

// docsSpan returns the offsets of the two scorecard markers.
func docsSpan(t *testing.T, body string) (int, int) {
	t.Helper()
	beg := strings.Index(body, docsScorecardBegin)
	end := strings.Index(body, docsScorecardEnd)
	if beg < 0 || end < beg {
		t.Fatalf("%s: wants %s and %s around the published table",
			docsBenchmark, docsScorecardBegin, docsScorecardEnd)
	}
	return beg, end
}

// republishDocs rewrites the published table and logs the lines that moved.
func republishDocs(t *testing.T, p, body, table string) {
	t.Helper()
	beg, end := docsSpan(t, body)
	updated := body[:beg+len(docsScorecardBegin)] + table + body[end:]
	if err := os.WriteFile(p, []byte(updated), 0o644); err != nil {
		t.Fatalf("write %s: %v", docsBenchmark, err)
	}
	for _, line := range tableDiff(docsBlock(t, body), table) {
		t.Logf("%s: %s", docsBenchmark, line)
	}
}

// tableDiff renders the lines two scorecard tables disagree on, paired by
// position so a moved row is visible instead of canceling out.
func tableDiff(published, measured string) []string {
	old, fresh := lines(published), lines(measured)
	var out []string
	for i := range max(len(old), len(fresh)) {
		before, after := lineAt(old, i), lineAt(fresh, i)
		switch {
		case before == after:
		case after == "":
			out = append(out, "- "+before)
		case before == "":
			out = append(out, "+ "+after)
		default:
			out = append(out, "- "+before, "+ "+after)
		}
	}
	return out
}

// lineAt returns one line of a rendered block, or "" past its end.
func lineAt(table []string, i int) string {
	if i < len(table) {
		return table[i]
	}
	return ""
}

// repoPath resolves one repository-relative path from this package's test
// directory.
func repoPath(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, rel)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("%s: no go.mod above the test directory", rel)
		}
		dir = parent
	}
}
