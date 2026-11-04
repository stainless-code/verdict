package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fuzzMembers is the contract allowlist for a linux archive.
var fuzzMembers = []string{"verdict", "LICENSE", "README.md"}

// fuzzEntry is one archive member the fuzzer describes.
type fuzzEntry struct {
	name string
	link string
	body []byte
	kind byte // 0 regular, 1 symlink, 2 directory, 3 hardlink
}

// entriesFrom builds two entries; names come from the fuzzer, so traversal
// spellings, absolute paths and allowlisted names all appear.
func entriesFrom(nameA, nameB, link string, kindA, kindB byte, bodyA, bodyB []byte) []fuzzEntry {
	return []fuzzEntry{
		{name: nameA, link: link, body: bodyA, kind: kindA % 4},
		{name: nameB, link: link, body: bodyB, kind: kindB % 4},
	}
}

// tarGz renders entries as a tar.gz byte stream.
func tarGz(t *testing.T, entries []fuzzEntry) []byte {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch e.kind {
		case 1:
			hdr.Typeflag, hdr.Linkname, hdr.Size = tar.TypeSymlink, e.link, 0
		case 2:
			hdr.Typeflag, hdr.Size = tar.TypeDir, 0
		case 3:
			hdr.Typeflag, hdr.Linkname, hdr.Size = tar.TypeLink, e.link, 0
		}
		if tw.WriteHeader(hdr) != nil {
			return nil
		}
		if hdr.Size > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if tw.Close() != nil || gz.Close() != nil {
		return nil
	}
	return raw.Bytes()
}

// zipOf renders entries as a zip archive.
func zipOf(t *testing.T, entries []fuzzEntry) []byte {
	t.Helper()
	var raw bytes.Buffer
	zw := zip.NewWriter(&raw)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Store}
		mode := fs.FileMode(0o755)
		switch e.kind {
		case 1:
			mode |= fs.ModeSymlink
		case 2:
			mode |= fs.ModeDir
		}
		hdr.SetMode(mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil
		}
		if e.kind == 2 || strings.HasSuffix(e.name, "/") {
			continue // a directory entry has no body
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if zw.Close() != nil {
		return nil
	}
	return raw.Bytes()
}

// wantExtraction is the independent verdict on a described archive: the
// extraction may succeed only if every member is allowlisted and regular and
// a regular "verdict" member exists; then the staged bytes are the last such
// member's body.
func wantExtraction(entries []fuzzEntry) (ok bool, body []byte) {
	found := false
	for _, e := range entries {
		allowed := false
		for _, m := range fuzzMembers {
			allowed = allowed || e.name == m
		}
		if !allowed || e.kind != 0 {
			return false, nil
		}
		if e.name == "verdict" {
			found, body = true, e.body
		}
	}
	return found, body
}

// assertSandboxed checks the extraction touched nothing but stage/verdict:
// the tree under root holds exactly the files the test created, and the
// sentinel outside the stage dir is intact.
func assertSandboxed(t *testing.T, root, archive string) {
	t.Helper()
	allowed := map[string]bool{
		".": true, filepath.Base(archive): true, "outside": true, filepath.Join("outside", "keep"): true,
		"stage": true, filepath.Join("stage", "verdict"): true,
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if !allowed[rel] {
			t.Errorf("extraction created %s", rel)
		}
		if rel == filepath.Join("stage", "verdict") && !d.Type().IsRegular() {
			t.Errorf("stage/verdict is %v, not a regular file", d.Type())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "outside", "keep")); err != nil || string(got) != "keep" {
		t.Fatalf("sentinel outside the stage dir changed: %q, %v", got, err)
	}
}

// runExtraction stages one archive and runs extractBinary against it.
func runExtraction(t *testing.T, name string, archive []byte, entries []fuzzEntry) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside", "keep"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, archive, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := extractBinary(path, stage, "verdict", fuzzMembers)
	assertSandboxed(t, root, path)
	wantOK, wantBody := wantExtraction(entries)
	if (err == nil) != wantOK {
		t.Fatalf("extractBinary err=%v, want success=%v for %+v", err, wantOK, entries)
	}
	if err != nil {
		return
	}
	if got != filepath.Join(stage, "verdict") {
		t.Fatalf("staged path %q, want %q", got, filepath.Join(stage, "verdict"))
	}
	if b, err := os.ReadFile(got); err != nil || !bytes.Equal(b, wantBody) {
		t.Fatalf("staged bytes %q (%v), want %q", b, err, wantBody)
	}
}

// seedArchives adds the shapes a tampered release archive takes.
func seedArchives(f *testing.F) {
	f.Helper()
	for _, s := range []struct {
		a, b, link string
		ka, kb     byte
	}{
		{"verdict", "LICENSE", "", 0, 0}, {"verdict", "README.md", "", 0, 0}, {"../verdict", "verdict", "", 0, 0},
		{"/etc/cron.d/x", "verdict", "", 0, 0}, {"verdict", "evil", "", 0, 0}, {"verdict", "LICENSE", "../outside/keep", 0, 1},
		{"verdict", "verdict", "", 0, 0}, {"verdict", "verdict", "../outside/keep", 1, 0}, {"stage/../verdict", "verdict", "", 0, 0},
		{"verdict", "LICENSE", "verdict", 0, 3}, {"verdict/", "verdict", "", 2, 0}, {"./verdict", "verdict", "", 0, 0},
	} {
		f.Add(s.a, s.b, s.link, s.ka, s.kb, []byte("bin-a"), []byte("bin-b"))
	}
}

// FuzzExtractTarGz holds release tar extraction to the zip-slip invariants,
// each checked against the test's own description of the archive rather than
// the extractor: nothing is written outside stage/verdict (traversal,
// absolute and symlink names included), the archive is accepted exactly when
// every member is allowlisted and regular and a regular verdict member
// exists, and the staged bytes are that member's.
func FuzzExtractTarGz(f *testing.F) {
	seedArchives(f)
	f.Fuzz(func(t *testing.T, nameA, nameB, link string, kindA, kindB byte, bodyA, bodyB []byte) {
		entries := entriesFrom(nameA, nameB, link, kindA, kindB, bodyA, bodyB)
		raw := tarGz(t, entries)
		if raw == nil {
			return
		}
		runExtraction(t, "a.tar.gz", raw, entries)
	})
}

// FuzzExtractZip is the same contract for the Windows zip archives.
func FuzzExtractZip(f *testing.F) {
	seedArchives(f)
	f.Fuzz(func(t *testing.T, nameA, nameB, link string, kindA, kindB byte, bodyA, bodyB []byte) {
		entries := entriesFrom(nameA, nameB, link, kindA, kindB, bodyA, bodyB)
		for i := range entries {
			if entries[i].kind == 3 { // zip has no hardlink type
				entries[i].kind = 0
			}
		}
		raw := zipOf(t, entries)
		if raw == nil {
			return
		}
		runExtraction(t, "a.zip", raw, entries)
	})
}
