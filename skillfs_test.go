package skillfs_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

// repoFiles is the checked-in payload set the embed must mirror exactly.
func repoFiles(t *testing.T) []string {
	t.Helper()
	files := []string{skillfs.SkillMD}
	files = append(files, dirFiles(t, "prompts")...)
	files = append(files, dirFiles(t, "schemas")...)
	files = append(files, "scripts/verdict")
	slices.Sort(files)
	return files
}

// dirFiles lists every regular file under dir as a slash path.
func dirFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestEmbedMatchesRepo is the drift guard: every embedded payload path has a
// byte-identical checked-in file, every checked-in payload file is embedded,
// and the executable bits agree.
func TestEmbedMatchesRepo(t *testing.T) {
	t.Parallel()
	want := repoFiles(t)
	got, err := skillfs.Paths()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("embedded paths = %v, want checked-in %v", got, want)
	}
	for _, path := range want {
		assertEmbeddedFile(t, path)
	}
	if !strings.HasSuffix(string(mustRead(t, skillfs.SkillMD)), "\n") {
		t.Error("SKILL.md does not end with a newline")
	}
}

// assertEmbeddedFile checks one path's bytes and mode against the checkout.
func assertEmbeddedFile(t *testing.T, path string) {
	t.Helper()
	embedded := mustRead(t, path)
	disk, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatalf("read checked-in %s: %v", path, err)
	}
	if !bytes.Equal(embedded, disk) {
		t.Errorf("embedded %s differs from the checked-in file", path)
	}
	info, err := os.Stat(filepath.FromSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	wantExec := info.Mode().Perm()&0o111 != 0
	if gotExec := skillfs.Mode(path)&0o111 != 0; gotExec != wantExec {
		t.Errorf("%s executable = %v, want %v", path, gotExec, wantExec)
	}
}

// TestSkillFrontmatterName pins the served skill name to the envelope name.
func TestSkillFrontmatterName(t *testing.T) {
	t.Parallel()
	body := string(mustRead(t, skillfs.SkillMD))
	if !strings.HasPrefix(body, "---\nname: verdict\n") {
		t.Fatalf("SKILL.md frontmatter does not start with name: verdict:\n%s", body[:min(len(body), 80)])
	}
}

// mustRead reads one embedded file, failing the test on error.
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := skillfs.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
