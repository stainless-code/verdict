package indexcore

import (
	"bytes"
	"testing"
)

// scanFile parses one source string as one language, uncapped.
func scanFile(t *testing.T, lang language, src string) shard {
	t.Helper()
	return parseShard("blob", []byte(src), lang, 5000)
}

// assertImports compares one shard's imports against literals, in order.
func assertImports(t *testing.T, got, want []importEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("imports = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("import %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// assertDecls compares one shard's declarations against literals, in order.
func assertDecls(t *testing.T, got, want []decl) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("decls = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("decl %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// assertRefs compares one shard's references against literals, in order.
func assertRefs(t *testing.T, got, want []ref) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("refs = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

// assertScannedPositions proves every recorded position names a byte inside the
// file the scanner read.
func assertScannedPositions(t *testing.T, s *shard, src string) {
	t.Helper()
	lines := bytes.Split([]byte(src), []byte("\n"))
	for _, d := range s.Decls {
		assertPosition(t, "decl "+d.Name, d.Line, 1, lines)
	}
	for _, r := range s.Refs {
		assertPosition(t, "ref "+r.Name, r.Line, r.Col, lines)
	}
}

// assertPosition checks one position against the file's real bounds.
func assertPosition(t *testing.T, what string, line, col int, lines [][]byte) {
	t.Helper()
	if line < 1 || line > len(lines) {
		t.Fatalf("%s: line %d outside a %d-line file", what, line, len(lines))
	}
	if col < 1 || col > len(lines[line-1])+1 {
		t.Fatalf("%s: column %d outside line %d of %d bytes", what, col, line, len(lines[line-1]))
	}
}
