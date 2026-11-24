package lsp

import (
	"testing"
)

// hashNewLine is sha256("new line"), the NEW-side text of notes.txt:2 in
// the fixture diff, computed independently of textHash.
const hashNewLine = "42b2829d7d41d79a3da85c511abad81f784e00cb0ee3ed8cc568a52d91fa1758"

// hashThreeLines is sha256("top\nnew line\nbottom"): the whole NEW side of
// the fixture hunk, the span a LEFT finding maps onto.
const hashThreeLines = "263af04181a9f8dd061ded63e7c72adad7fa4ee663ef48403460051a3f1dd0cf"

func fixtureFiles() map[string]diffFile {
	files := map[string]diffFile{
		"notes.txt": {Path: "notes.txt", Hunks: []diffHunk{{
			NewStart: 1, NewCount: 3,
			Lines: []diffLine{
				{Kind: "context", Text: "top", OldNo: 1, NewNo: 1},
				{Kind: kindDel, Text: "old line", OldNo: 2},
				{Kind: "add", Text: "new line", NewNo: 2},
				{Kind: "context", Text: "bottom", OldNo: 3, NewNo: 3},
			},
		}}},
		"bin.png":  {Path: "bin.png", IsBinary: true},
		"gone.txt": {Path: "gone.txt", IsDeleted: true, Hunks: []diffHunk{{Lines: []diffLine{{Kind: kindDel, Text: "x", OldNo: 1}}}}},
	}
	for path, f := range files {
		f.indexNewSide()
		files[path] = f
	}
	return files
}

// TestJoinFindingAnchors pins every anchor row: an exact RIGHT span hashes its
// NEW-side text, a LEFT finding maps onto its hunk's NEW side, and binary,
// deleted, or undiffed files fall back to the stored line, unmapped.
func TestJoinFindingAnchors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		anchor string
		hash   string
		side   string
		rec    record
		start  int
		line   int
	}{
		{"right exact", anchorExact, hashNewLine, sideRight, record{Path: "notes.txt", Side: "RIGHT", Line: 2}, 2, 2},
		{"side defaults right", anchorExact, hashNewLine, sideRight, record{Path: "notes.txt", Line: 2}, 2, 2},
		{"lower-case side", anchorExact, hashNewLine, sideRight, record{Path: "notes.txt", Side: "right", Line: 2}, 2, 2},
		{"start_line span", anchorExact, hashThreeLines, sideRight, record{Path: "notes.txt", Line: 3, StartLine: 1}, 1, 3},
		{"start_line after line is ignored", anchorExact, hashNewLine, sideRight, record{Path: "notes.txt", Line: 2, StartLine: 3}, 2, 2},
		{"right line outside the hunk", anchorUnmapped, "", sideRight, record{Path: "notes.txt", Line: 9}, 9, 9},
		{"left maps to hunk", anchorHunk, hashThreeLines, sideLeft, record{Path: "notes.txt", Side: "LEFT", Line: 2}, 1, 3},
		{"left not deleted", anchorUnmapped, "", sideLeft, record{Path: "notes.txt", Side: "LEFT", Line: 7}, 7, 7},
		{"binary", anchorUnmapped, "", sideRight, record{Path: "bin.png", Line: 1}, 1, 1},
		{"deleted file", anchorUnmapped, "", sideLeft, record{Path: "gone.txt", Side: "LEFT", Line: 1}, 1, 1},
		{"not in diff", anchorUnmapped, "", sideRight, record{Path: "other.go", Line: 4}, 4, 4},
	} {
		f, ok := joinFinding(&tc.rec, fixtureFiles())
		if !ok {
			t.Errorf("%s: joinFinding dropped the record", tc.name)
			continue
		}
		if f.anchor != tc.anchor || f.hunkSHA256 != tc.hash || f.side != tc.side || f.start != tc.start || f.line != tc.line {
			t.Errorf("%s: anchor=%s hash=%s side=%s span=%d..%d; want %s %s %s %d..%d", tc.name,
				f.anchor, f.hunkSHA256, f.side, f.start, f.line, tc.anchor, tc.hash, tc.side, tc.start, tc.line)
		}
	}
}

// TestJoinFindingDropsUnpublishable pins the records no diagnostic carries:
// section findings, lineless ones, and any path that is not a clean
// root-relative path.
func TestJoinFindingDropsUnpublishable(t *testing.T) {
	t.Parallel()
	for _, rec := range []record{
		{Path: "", Line: 2},
		{Path: "notes.txt", Line: 0},
		{Path: "notes.txt", Line: -1},
		{Path: "../../etc/passwd", Line: 1},
		{Path: "/etc/passwd", Line: 1},
		{Path: "C:/x", Line: 1},
		{Path: "a\x00b", Line: 1},
	} {
		if _, ok := joinFinding(&rec, fixtureFiles()); ok {
			t.Errorf("joinFinding(%q line %d) published an unpublishable record", rec.Path, rec.Line)
		}
	}
}

// TestSuggestionEligibility pins when a suggestion could become an edit:
// present, unfenced, RIGHT, exact, hashed.
func TestSuggestionEligibility(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		rec  record
		want bool
	}{
		{"eligible", record{Path: "notes.txt", Line: 2, Suggestion: new("new line!")}, true},
		{"absent", record{Path: "notes.txt", Line: 2}, false},
		{"empty", record{Path: "notes.txt", Line: 2, Suggestion: new("")}, false},
		{"fenced", record{Path: "notes.txt", Line: 2, Suggestion: new("```go\nx\n```")}, false},
		{"left side", record{Path: "notes.txt", Side: "LEFT", Line: 2, Suggestion: new("x")}, false},
		{"unmapped", record{Path: "other.go", Line: 2, Suggestion: new("x")}, false},
	} {
		f, _ := joinFinding(&tc.rec, fixtureFiles())
		if f.suggestion != tc.want {
			t.Errorf("%s: suggestion eligible = %v, want %v", tc.name, f.suggestion, tc.want)
		}
	}
}

// TestDiffFilesIndexesNewSideOnce pins that the NEW-side index is built when
// diffFiles decodes the bundle's diff, not lazily by the join: the decoded
// file carries the whole index, and the same file without it anchors
// nothing, so no join path can rebuild it per lookup.
func TestDiffFilesIndexesNewSideOnce(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	s := New(newConfig(root))
	files, err := s.newJob(&s.roots[0]).diffFiles(t.Context(), &s.roots[0], "local-1")
	if err != nil {
		t.Fatal(err)
	}
	notes := files["notes.txt"]
	if len(notes.newSide) != 3 || notes.newSide[1] != "top" || notes.newSide[2] != "new line" || notes.newSide[3] != "bottom" {
		t.Fatalf("decoded NEW-side index = %v, want lines 1-3 of the reviewed file", notes.newSide)
	}
	rec := record{Path: "notes.txt", Line: 2}
	if f, _ := joinFinding(&rec, files); f.anchor != anchorExact {
		t.Fatalf("indexed file anchored %s, want exact", f.anchor)
	}
	notes.newSide = nil
	if f, _ := joinFinding(&rec, map[string]diffFile{"notes.txt": notes}); f.anchor != anchorUnmapped {
		t.Fatalf("an unindexed file anchored %s; the join must not build its own index", f.anchor)
	}
}

// TestPureDeletionAnchors pins the `+N,0` case: git's N is the line before
// the deletion, so a LEFT finding anchors on line N+1 (the line now standing
// where the deleted code was), marked hunk, whether or not the diff shows
// that line; line 1 at the top of a file. The reviewed-text hash is the
// shown line's when the diff carries it and absent when it does not.
func TestPureDeletionAnchors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		hash     string
		next     []diffHunk
		newStart int
		want     int
	}{
		{"mid-file, zero-context diff", "", nil, 4, 5},
		{"mid-file, the next line shown by the following hunk", hashNext, []diffHunk{{
			NewStart: 5, NewCount: 1,
			Lines: []diffLine{{Kind: "context", Text: "next", OldNo: 7, NewNo: 5}},
		}}, 4, 5},
		{"top of file", "", nil, 0, 1},
	} {
		deletion := diffHunk{NewStart: tc.newStart, NewCount: 0, Lines: []diffLine{
			{Kind: kindDel, Text: "gone one", OldNo: 5},
			{Kind: kindDel, Text: "gone two", OldNo: 6},
		}}
		file := diffFile{Path: "a.go", Hunks: append([]diffHunk{deletion}, tc.next...)}
		file.indexNewSide()
		f, _ := joinFinding(&record{Path: "a.go", Side: "LEFT", Line: 6}, map[string]diffFile{"a.go": file})
		if f.anchor != anchorHunk || f.start != tc.want || f.line != tc.want || f.hunkSHA256 != tc.hash || !f.afterDeletion {
			t.Errorf("%s: anchor %s span %d..%d hash %q, want hunk %d..%d hash %q",
				tc.name, f.anchor, f.start, f.line, f.hunkSHA256, tc.want, tc.want, tc.hash)
		}
	}
}

// hashNext is sha256("next"), the shown line after the deletion.
const hashNext = "c6c1c9a9c8543f1e4cd980064cf1625eeb61a90703b2464fff039f21682508b3"

// TestDeletionAtEndOfFileAnchorsTheLastLine pins the end-of-file case through
// the file's own text: a `+4,0` deletion in a four-line file anchors line 4,
// never line 5 past the end; in a longer file it stays on line 5.
func TestDeletionAtEndOfFileAnchorsTheLastLine(t *testing.T) {
	t.Parallel()
	deletion := diffHunk{NewStart: 4, NewCount: 0, Lines: []diffLine{{Kind: kindDel, Text: "gone", OldNo: 5}}}
	file := diffFile{Path: "a.go", Hunks: []diffHunk{deletion}}
	file.indexNewSide()
	f, _ := joinFinding(&record{Path: "a.go", Side: "LEFT", Line: 5}, map[string]diffFile{"a.go": file})
	atEnd := diagnosticOf(&f, []string{"one", "two", "three", "four", ""})
	if atEnd.Range.Start.Line != 3 || atEnd.Data.Span != (span{StartLine: 4, Line: 4}) {
		t.Errorf("end-of-file deletion: range %+v span %+v, want line 4", atEnd.Range, atEnd.Data.Span)
	}
	midFile := diagnosticOf(&f, []string{"one", "two", "three", "four", "five", "six"})
	if midFile.Range.Start.Line != 4 || midFile.Data.Span != (span{StartLine: 5, Line: 5}) {
		t.Errorf("mid-file deletion: range %+v span %+v, want line 5", midFile.Range, midFile.Data.Span)
	}
}
