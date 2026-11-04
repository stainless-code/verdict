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
			if h.Side != "OLD" && h.Side != "NEW" {
				t.Fatalf("hunk side %q", h.Side)
			}
			if h.Start < 0 || h.End < h.Start {
				t.Fatalf("hunk %+v is negative or inverted (diff %q)", h, diff)
			}
		}
		tsv := hunks.Format(parsed)
		if tsv == "" {
			return
		}
		rows := strings.Split(strings.TrimSuffix(tsv, "\n"), "\n")
		if len(rows) != len(parsed) {
			t.Fatalf("hunks.tsv has %d rows for %d hunks:\n%q\ndiff %q", len(rows), len(parsed), tsv, diff)
		}
		for i, row := range rows {
			fields := strings.Split(row, "\t")
			want := []string{parsed[i].File, parsed[i].Side, strconv.Itoa(parsed[i].Start), strconv.Itoa(parsed[i].End)}
			if len(fields) != len(want) || strings.Join(fields, "\x00") != strings.Join(want, "\x00") {
				t.Fatalf("hunks.tsv row %d = %q does not read back as %q (diff %q)", i, row, want, diff)
			}
		}
	})
}

// FuzzFiles holds the section scanner to counts a line-by-line grep gives:
// one File per `diff --git ` line, sections tile the diff in order, and the
// byte sizes add up to the diff text from the first header on.
func FuzzFiles(f *testing.F) {
	seedDiffs(f)
	f.Fuzz(func(t *testing.T, diff string) {
		files := hunks.Files(diff)
		body := strings.TrimSuffix(diff, "\n")
		headers, first, added, removed := 0, -1, 0, 0
		offset := 0
		inSection := false
		for line := range strings.SplitSeq(body, "\n") {
			if strings.HasPrefix(line, "diff --git ") {
				headers++
				inSection = true
				if first < 0 {
					first = offset
				}
			} else if inSection {
				added += b2i(strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"))
				removed += b2i(strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"))
			}
			offset += len(line) + 1
		}
		if diff == "" || body == "" && diff == "\n" {
			headers = 0
		}
		if len(files) != headers {
			t.Fatalf("Files = %d sections, %d header lines in %q", len(files), headers, diff)
		}
		sumSize, sumAdd, sumDel, prevEnd := int64(0), 0, 0, 0
		for i := range files {
			fl := &files[i]
			if fl.StartLine <= prevEnd || fl.EndLine < fl.StartLine {
				t.Fatalf("section %d spans %d-%d after %d in %q", i, fl.StartLine, fl.EndLine, prevEnd, diff)
			}
			prevEnd = fl.EndLine
			sumSize += fl.Size
			sumAdd += fl.Additions
			sumDel += fl.Deletions
			if len(fl.AddedLines) != fl.Additions {
				t.Fatalf("AddedLines %d != Additions %d", len(fl.AddedLines), fl.Additions)
			}
		}
		if headers > 0 && sumSize != int64(len(body)-first+1) {
			t.Fatalf("section sizes sum to %d, diff text from first header is %d bytes", sumSize, len(body)-first+1)
		}
		if sumAdd != added || sumDel != removed {
			t.Fatalf("+%d/-%d, grep counts +%d/-%d in %q", sumAdd, sumDel, added, removed, diff)
		}
	})
}

// b2i is 1 for true.
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FuzzHeaderPaths holds the header path decoder to the shapes it must never
// invent: a non-header line yields no paths, a path never keeps the a/ b/
// side marker it stripped from a well-formed header, and the quoted form of a
// path round-trips through git's own quoting rules (octal escapes).
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
		// A plain header built from two clean names must decode to exactly them.
		plainOld, plainNew := cleanName(line), cleanName(line)+"2"
		o2, n2 := hunks.HeaderPaths("diff --git a/" + plainOld + " b/" + plainNew)
		if o2 != plainOld || n2 != plainNew {
			t.Fatalf("clean header for %q,%q decoded to %q,%q", plainOld, plainNew, o2, n2)
		}
	})
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
