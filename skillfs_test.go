package skillfs_test

import (
	"bytes"
	"errors"
	"io"
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

// repoFiles lists every file the repository would ship under dir — tracked
// plus untracked-but-not-ignored, dotfiles included — as slash paths relative
// to it. This is what a clone carries, so it must equal what is embedded.
// Ignored dev state (a bootstrapped bin/) is not shipped and not listed.
// Without git it falls back to every file on disk.
func repoFiles(t *testing.T, dir string) []string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", dir).Output()
	if err != nil {
		return diskFiles(t, dir)
	}
	var files []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name == "" {
			continue
		}
		files = append(files, strings.TrimPrefix(name, dir+"/"))
	}
	slices.Sort(files)
	return files
}

// diskFiles lists every file under dir, relative to it.
func diskFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(filepath.FromSlash(dir), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(filepath.FromSlash(dir), path)
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

// servedFiles is the checkout files served under payload paths: the stub, the
// Procedure, and the repository's published schemas.
func servedFiles(t *testing.T) []string {
	t.Helper()
	files := repoFiles(t, skillfs.Dir)
	files = append(files, repoFiles(t, skillfs.ProcedureDir)...)
	for _, name := range repoFiles(t, skillfs.SchemasDir) {
		files = append(files, skillfs.SchemasDir+"/"+name)
	}
	slices.Sort(files)
	return files
}

// onDisk is the checkout path of one served payload file.
func onDisk(path string) string {
	if path == skillfs.SchemasDir || strings.HasPrefix(path, skillfs.SchemasDir+"/") {
		return filepath.FromSlash(path)
	}
	if path == skillfs.ProcedureMD || strings.HasPrefix(path, "prompts/") ||
		strings.HasPrefix(path, "packs/") || strings.HasPrefix(path, "references/") {
		return filepath.Join(filepath.FromSlash(skillfs.ProcedureDir), filepath.FromSlash(path))
	}
	return filepath.Join(filepath.FromSlash(skillfs.Dir), filepath.FromSlash(path))
}

// TestEmbedMatchesRepo is the drift guard: every embedded payload path has a
// byte-identical checked-in file, every file in the skill directory and the
// published schemas directory is embedded (so nothing else would reach
// `npx skills add`), and the executable bits agree.
func TestEmbedMatchesRepo(t *testing.T) {
	t.Parallel()
	want := servedFiles(t)
	got, err := skillfs.Paths()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("embedded paths = %v, want the files in %s and %s: %v", got, skillfs.Dir, skillfs.SchemasDir, want)
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

// TestProcedureLivesOutsideTheStub pins the payload split: the Procedure
// moves to procedure/ at the inventory paths, and the installable skill keeps
// only the bootstrap files.
func TestProcedureLivesOutsideTheStub(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"PROCEDURE.md", filepath.Join("prompts", "review.md"),
		filepath.Join("packs", "builtin", "pack.json"), filepath.Join("references", "commands.md"),
	} {
		if _, err := os.Stat(filepath.Join(skillfs.ProcedureDir, path)); err != nil {
			t.Errorf("procedure/%s: %v", path, err)
		}
	}
	for _, path := range []string{"PROCEDURE.md", "prompts", "packs", "references"} {
		if _, err := os.Stat(filepath.Join(skillfs.Dir, path)); !os.IsNotExist(err) {
			t.Errorf("stub still holds %s: stat err = %v", path, err)
		}
	}
}

// TestRootReadDirReportsEOF pins ReadDir's end-of-listing contract: reading
// one entry at a time must end with [io.EOF], never an empty successful read.
//
//nolint:revive // the one-entry paging loop is the assertion
func TestRootReadDirReportsEOF(t *testing.T) {
	t.Parallel()
	paths, err := skillfs.Paths()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, path := range paths {
		name, _, _ := strings.Cut(path, "/")
		want[name] = true
	}
	f, err := skillfs.FS.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dir, ok := f.(fs.ReadDirFile)
	if !ok {
		t.Fatal("payload root is not a ReadDirFile")
	}
	got := map[string]bool{}
	for {
		entries, err := dir.ReadDir(1)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("ReadDir(1) returned %d entries and err %v, want one or io.EOF", len(entries), err)
		}
		got[entries[0].Name()] = true
	}
	if len(got) != len(want) {
		t.Fatalf("root entries = %v, want the top-level names of %v", got, want)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("root ReadDir never returned %q", name)
		}
	}
}

// TestRootReadDirContract pins both halves of [fs.ReadDirFile]: a non-positive
// n drains the directory without an end error, while a positive n that finds
// no entries reports [io.EOF].
//
//nolint:cyclop,gocognit,revive // the table covers four distinct paging sequences
func TestRootReadDirContract(t *testing.T) {
	t.Parallel()
	paths, err := skillfs.Paths()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, path := range paths {
		name, _, _ := strings.Cut(path, "/")
		want[name] = true
	}
	var top []string
	for name := range want {
		top = append(top, name)
	}
	slices.Sort(top)

	type step struct {
		n    int
		want int
		eof  bool
	}
	for _, tc := range []struct {
		name  string
		steps []step
	}{
		{name: "drain twice", steps: []step{{n: -1, want: len(top)}, {n: -1, want: 0}}},
		{name: "zero after exhaustion", steps: []step{{n: -1, want: len(top)}, {n: 0, want: 0}}},
		{name: "page to end", steps: func() []step {
			out := make([]step, 0, len(top)+1)
			for range top {
				out = append(out, step{n: 1, want: 1})
			}
			return append(out, step{n: 1, want: 0, eof: true})
		}()},
		{name: "mixed", steps: []step{{n: 1, want: 1}, {n: -1, want: len(top) - 1}, {n: 1, want: 0, eof: true}, {n: 0, want: 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, err := skillfs.FS.Open(".")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			dir, ok := f.(fs.ReadDirFile)
			if !ok {
				t.Fatal("payload root is not a ReadDirFile")
			}
			var seen []string
			for i, call := range tc.steps {
				entries, err := dir.ReadDir(call.n)
				if call.eof != errors.Is(err, io.EOF) {
					t.Fatalf("step %d ReadDir(%d) err = %v, want eof=%v", i, call.n, err, call.eof)
				}
				if !call.eof && err != nil {
					t.Fatalf("step %d ReadDir(%d) = %v", i, call.n, err)
				}
				if len(entries) != call.want {
					t.Fatalf("step %d ReadDir(%d) returned %d entries, want %d", i, call.n, len(entries), call.want)
				}
				for _, entry := range entries {
					seen = append(seen, entry.Name())
				}
			}
			slices.Sort(seen)
			if !slices.Equal(seen, top) {
				t.Errorf("root entries = %v, want %v", seen, top)
			}
		})
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
		if path == skillfs.ProcedureMD || strings.HasPrefix(path, "prompts/") {
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

// isRuntimeState reports a path the tool creates in the checkout when it runs
// rather than one the repository ships with it.
func isRuntimeState(ref string) bool {
	return strings.HasPrefix(ref, ".verdict/") || strings.HasPrefix(ref, ".review/")
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
	// Runtime review state is created in the checkout by the tool itself, not
	// shipped with it: naming it describes where runs write, so it must not
	// read as a dangling repo path once someone has actually used the tool
	// here. Without this, a used checkout fails the payload check that a
	// pristine one passes.
	if isRuntimeState(ref) {
		return "", false
	}
	if _, err := os.Stat(filepath.FromSlash(ref)); err == nil {
		return ref, true
	}
	first, _, _ := strings.Cut(ref, "/")
	_, err := fs.Stat(skillfs.FS, first)
	return ref, err == nil
}
