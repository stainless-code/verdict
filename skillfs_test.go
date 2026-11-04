package skillfs_test

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

// repoFiles lists every file the repository would ship under the skill
// directory — tracked plus untracked-but-not-ignored, dotfiles included, no
// matter whether it is embedded — as slash paths relative to it. This is what
// the skills CLI copies to an agent from a clone, so it must equal the
// embedded payload. Ignored dev state (a bootstrapped bin/) is not shipped
// and not listed. Without git it falls back to every file on disk.
func repoFiles(t *testing.T) []string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", skillfs.Dir).Output()
	if err != nil {
		return diskFiles(t)
	}
	var files []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name == "" {
			continue
		}
		files = append(files, strings.TrimPrefix(name, skillfs.Dir+"/"))
	}
	slices.Sort(files)
	return files
}

// diskFiles lists every file under the skill directory, relative to it.
func diskFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(filepath.FromSlash(skillfs.Dir), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(filepath.FromSlash(skillfs.Dir), path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	return files
}

// onDisk is the checkout path of one payload file.
func onDisk(path string) string {
	return filepath.Join(filepath.FromSlash(skillfs.Dir), filepath.FromSlash(path))
}

// TestEmbedMatchesRepo is the drift guard: every embedded payload path has a
// byte-identical checked-in file, every file in the skill directory is
// embedded (so nothing else would reach `npx skills add`), and the
// executable bits agree.
func TestEmbedMatchesRepo(t *testing.T) {
	t.Parallel()
	want := repoFiles(t)
	got, err := skillfs.Paths()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("embedded paths = %v, want the files in %s: %v", got, skillfs.Dir, want)
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
	disk, err := os.ReadFile(onDisk(path))
	if err != nil {
		t.Fatalf("read checked-in %s: %v", path, err)
	}
	if !bytes.Equal(embedded, disk) {
		t.Errorf("embedded %s differs from the checked-in file", path)
	}
	if runtime.GOOS == "windows" {
		return // NTFS carries no executable bit: Linux and macOS CI pin the modes
	}
	info, err := os.Stat(onDisk(path))
	if err != nil {
		t.Fatal(err)
	}
	wantExec := info.Mode().Perm()&0o111 != 0
	if gotExec := skillfs.Mode(path)&0o111 != 0; gotExec != wantExec {
		t.Errorf("%s executable = %v, want %v", path, gotExec, wantExec)
	}
}

// TestRegistryDiscovery pins the layout the skills CLI relies on. Its
// discovery (github.com/vercel-labs/skills, discoverSkills) returns at once
// when the repo root holds a SKILL.md and then copies the whole root; with no
// root SKILL.md it walks skills/ and installs just the directory holding the
// SKILL.md it finds. So the root must stay SKILL.md-free and skills/ must
// hold exactly one skill.
func TestRegistryDiscovery(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat("SKILL.md"); !os.IsNotExist(err) {
		t.Fatalf("a root SKILL.md makes `npx skills add` copy the whole repo; stat err = %v", err)
	}
	entries, err := os.ReadDir("skills")
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	if want := []string{"verdict"}; !slices.Equal(dirs, want) {
		t.Fatalf("skills/ directories = %v, want %v (the CLI would offer or install each one)", dirs, want)
	}
	if _, err := os.Stat(onDisk(skillfs.SkillMD)); err != nil {
		t.Fatalf("skill entry point missing: %v", err)
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

// TestPayloadProsePathsResolve fails when SKILL.md or a prompt names a path a
// checkout resolves and the payload does not ship: an installed copy resolves
// it against the skill directory and finds nothing. Link the repository path
// by absolute URL instead, the way schemas/README.md names its generator.
func TestPayloadProsePathsResolve(t *testing.T) {
	t.Parallel()
	paths, err := skillfs.Paths()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if path == skillfs.SkillMD || strings.HasPrefix(path, "prompts/") {
			checkProsePaths(t, path)
		}
	}
}

// checkProsePaths reports every word in one payload file that names a file the
// payload does not ship.
func checkProsePaths(t *testing.T, path string) {
	t.Helper()
	for i, line := range strings.Split(string(mustRead(t, path)), "\n") {
		for word := range strings.FieldsSeq(line) {
			if ref, ok := danglingRef(word); ok {
				t.Errorf("%s:%d names %q, which %s does not ship: "+
					"link the repository path by absolute URL",
					path, i+1, ref, skillfs.Dir)
			}
		}
	}
}

// danglingRef returns the payload path of a word that names a file the payload
// lacks. A URL, an absolute path, a glob, or prose makes no such claim; a path
// the checkout resolves, or one that starts at a payload directory and is not
// there, does.
func danglingRef(word string) (string, bool) {
	if strings.Contains(word, "://") || strings.HasPrefix(word, "/") || strings.HasPrefix(word, "~") {
		return "", false
	}
	ref := strings.TrimPrefix(word, "./")
	ref = strings.TrimRight(ref, "`.,;:)/|")
	ref = strings.TrimLeft(ref, "`([*<")
	if !strings.Contains(ref, "/") || strings.ContainsAny(ref, "*$<>{}") {
		return "", false
	}
	if _, err := fs.Stat(skillfs.FS, ref); err == nil {
		return "", false
	}
	if _, err := os.Stat(filepath.FromSlash(ref)); err == nil {
		return ref, true
	}
	first, _, _ := strings.Cut(ref, "/")
	_, err := fs.Stat(skillfs.FS, first)
	return ref, err == nil
}
