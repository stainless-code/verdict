package hunks_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/hunks"
)

// seedDiffs adds the repo's real diff fixtures and the odd-path shapes git
// emits (C-quoted, spaced, renamed) to the fuzz corpus.
func seedDiffs(f *testing.F) {
	f.Helper()
	paths, err := filepath.Glob("../../bench/corpus/*/full.diff")
	if err != nil {
		f.Fatal(err)
	}
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(string(b))
		}
	}
	for _, s := range []string{
		"diff --git a/a b/a\n@@ -1,2 +1,2 @@\n-x\n+y\n",
		"diff --git \"a/h\\303\\251llo\" \"b/h\\303\\251llo\"\n@@ -1 +1 @@\n-a\n+b\n",
		"diff --git a/sp ace b/sp ace\n@@ -3,0 +4,2 @@\n+a\n+b\n",
		"diff --git a/o b/n\nrename from o\nrename to n\n@@ -1 +1 @@\n",
		"diff --git a/../../etc/passwd b/../../etc/passwd\n@@ -1 +1 @@\n",
		"diff --git \"a/x\\ty\" \"b/x\\ty\"\n@@ -1 +1,3 @@\n",
		"diff --git \"a/x\\nNEW\" \"b/x\\nNEW\"\n@@ -1 +1 @@\n",
		"diff --git a/x b/x\n@@ -99999999999999999999,5 +9223372036854775807,9 @@\n",
		"diff --git a/x b/x\n@@ -1,9223372036854775807 +1 @@\n",
	} {
		f.Add(s)
	}
}

// FuzzParse holds the diff indexer to invariants that do not call its own
// helpers: hunks never wrap into negative or inverted spans (an anchor test
// against one would accept lines outside the diff), and the hunks.tsv text
// it feeds to the review gates reads back, field for field, with plain
// string splitting — so a hostile path cannot forge or shift a row.
func FuzzParse(f *testing.F) {
	seedDiffs(f)
	f.Fuzz(func(t *testing.T, diff string) {
		parsed := hunks.Parse(diff)
		for _, h := range parsed {
			assertSaneSpan(t, h, diff)
		}
		assertTSVReadsBack(t, parsed, diff)
	})
}

// assertSaneSpan checks one hunk's side and span.
func assertSaneSpan(t *testing.T, h hunks.Hunk, diff string) {
	t.Helper()
	if h.Side != "OLD" && h.Side != "NEW" {
		t.Fatalf("hunk side %q", h.Side)
	}
	if h.Start < 0 || h.End < h.Start {
		t.Fatalf("hunk %+v is negative or inverted (diff %q)", h, diff)
	}
}

// assertTSVReadsBack splits the rendered hunks.tsv with plain string
// functions and compares each row with the hunk it came from.
func assertTSVReadsBack(t *testing.T, parsed []hunks.Hunk, diff string) {
	t.Helper()
	tsv := hunks.Format(parsed)
	if tsv == "" {
		return
	}
	rows := strings.Split(strings.TrimSuffix(tsv, "\n"), "\n")
	if len(rows) != len(parsed) {
		t.Fatalf("hunks.tsv has %d rows for %d hunks:\n%q\ndiff %q", len(rows), len(parsed), tsv, diff)
	}
	for i, row := range rows {
		want := []string{parsed[i].File, parsed[i].Side, strconv.Itoa(parsed[i].Start), strconv.Itoa(parsed[i].End)}
		if strings.Join(strings.Split(row, "\t"), "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("hunks.tsv row %d = %q does not read back as %q (diff %q)", i, row, want, diff)
		}
	}
}

// grepCounts is what a line-by-line scan of a diff finds: the independent
// oracle for the section scanner.
type grepCounts struct {
	headers, added, removed int
	firstOffset             int // byte offset of the first header line, -1 if none
}

// grepDiff counts header lines and +/- content lines inside sections, the way
// `grep -c` would, over the diff minus its final newline.
func grepDiff(body string) grepCounts {
	c := grepCounts{firstOffset: -1}
	offset := 0
	for line := range strings.SplitSeq(body, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			c.headers++
			if c.firstOffset < 0 {
				c.firstOffset = offset
			}
		case c.firstOffset >= 0:
			a, r := contentLine(line)
			c.added, c.removed = c.added+a, c.removed+r
		}
		offset += len(line) + 1
	}
	return c
}

// contentLine classifies a diff line: 1,0 added, 0,1 removed (the +++ and ---
// file headers excluded).
func contentLine(line string) (added, removed int) {
	return b2i(strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++")),
		b2i(strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"))
}

// FuzzFiles holds the section scanner to counts a line-by-line grep gives:
// one File per `diff --git ` line, sections tile the diff in order, and the
// byte sizes add up to the diff text from the first header on.
func FuzzFiles(f *testing.F) {
	seedDiffs(f)
	f.Fuzz(func(t *testing.T, diff string) {
		files := hunks.Files(diff)
		body := strings.TrimSuffix(diff, "\n")
		want := grepDiff(body)
		if len(files) != want.headers {
			t.Fatalf("Files = %d sections, %d header lines in %q", len(files), want.headers, diff)
		}
		assertSections(t, files, want, body)
	})
}

// assertSections checks section ordering, sizes and +/- totals.
func assertSections(t *testing.T, files []hunks.File, want grepCounts, body string) {
	t.Helper()
	var size int64
	var added, removed, prevEnd int
	for i := range files {
		fl := &files[i]
		assertSectionOrder(t, fl, prevEnd)
		prevEnd = fl.EndLine
		size += fl.Size
		added += fl.Additions
		removed += fl.Deletions
	}
	if len(files) > 0 && size != int64(len(body)-want.firstOffset+1) {
		t.Fatalf("section sizes sum to %d, diff text from the first header is %d bytes", size, len(body)-want.firstOffset+1)
	}
	if added != want.added || removed != want.removed {
		t.Fatalf("+%d/-%d, grep counts +%d/-%d", added, removed, want.added, want.removed)
	}
}

// assertSectionOrder checks one section starts after the previous ends and
// carries as many added lines as it counts.
func assertSectionOrder(t *testing.T, fl *hunks.File, prevEnd int) {
	t.Helper()
	if fl.StartLine <= prevEnd || fl.EndLine < fl.StartLine || len(fl.AddedLines) != fl.Additions {
		t.Fatalf("section is inconsistent: %+v after line %d", fl, prevEnd)
	}
}

// b2i is 1 for true.
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FuzzHeaderPaths holds the header path decoder to the shapes it must never
// invent: a non-header line yields no paths, and a header built from clean
// names decodes to exactly those names.
func FuzzHeaderPaths(f *testing.F) {
	for _, s := range []string{
		"diff --git a/x b/x", "diff --git \"a/h\\303\\251\" \"b/h\\303\\251\"", "diff --git ", "diff --git \"", "diff --git \"a/\\",
		"diff --git a/a b/b b/c", "index 1..2", "diff --git a/ b/", "diff --git \"a/x\" b/y",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		oldP, newP := hunks.HeaderPaths(line)
		if !strings.HasPrefix(line, "diff --git ") && (oldP != "" || newP != "") {
			t.Fatalf("non-header %q gave paths %q, %q", line, oldP, newP)
		}
		assertCleanHeaderDecodes(t, cleanName(line))
	})
}

// assertCleanHeaderDecodes builds a header from two clean names and expects
// exactly those names back.
func assertCleanHeaderDecodes(t *testing.T, name string) {
	t.Helper()
	plainNew := name + "2"
	o, n := hunks.HeaderPaths("diff --git a/" + name + " b/" + plainNew)
	if o != name || n != plainNew {
		t.Fatalf("clean header for %q,%q decoded to %q,%q", name, plainNew, o, n)
	}
}

// cleanName keeps only the path-safe letters of s, so a header built from it
// has an unambiguous expected decoding.
func cleanName(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '/' || r == '.' {
			sb.WriteRune(r)
		}
	}
	if sb.Len() == 0 {
		return "f"
	}
	return sb.String()
}
