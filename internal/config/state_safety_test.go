package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/testenv"
)

// symlinkFixture builds a repository root whose .verdict is a symlink to
// target, returning the root and the link target for untouched checks.
func symlinkFixture(t *testing.T, target string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, VerdictDir)); err != nil {
		t.Fatalf("symlink .verdict: %v", err)
	}
	return root
}

// dirEntries lists directory names: symlink escapes must leave the
// target exactly as found.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// escapeFixture builds a directory outside the repository holding one
// marker file: any write through an escaping link would land beside it.
func escapeFixture(t *testing.T) string {
	t.Helper()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "marker.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	return outside
}

// assertUntouched fails when a refused write left anything behind: the
// link target must still hold exactly its marker file.
func assertUntouched(t *testing.T, dir string) {
	t.Helper()
	if got := dirEntries(t, dir); len(got) != 1 || got[0] != "marker.txt" {
		t.Fatalf("%s gained files: %v", dir, got)
	}
}

// assertSymlinkOrEscapeRefusal fails unless the call refused with the
// symlink message or the outside-the-repository message.
func assertSymlinkOrEscapeRefusal(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s followed the link", what)
	}
	if !strings.Contains(err.Error(), "symlink") && !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("error = %q, want a symlink or escape refusal", err)
	}
}

// TestEnsureStateIgnoreRefusesVerdictSymlinkOutside pins the escape rule:
// a .verdict symlink pointing out of the repository refuses, and the
// link target gains no state files.
func TestEnsureStateIgnoreRefusesVerdictSymlinkOutside(t *testing.T) {
	outside := escapeFixture(t)
	root := symlinkFixture(t, outside)
	assertSymlinkOrEscapeRefusal(t, NewLayout(root).EnsureStateIgnore(), "EnsureStateIgnore")
	assertUntouched(t, outside)
}

// TestEnsureStateIgnoreRefusesVerdictSymlinkInside pins the unrelated
// target rule: even an in-repository .verdict symlink refuses, because
// the link target is not the managed directory.
func TestEnsureStateIgnoreRefusesVerdictSymlinkInside(t *testing.T) {
	r := symlinkInside(t)
	if err := NewLayout(r).EnsureStateIgnore(); err == nil {
		t.Fatal("EnsureStateIgnore wrote through an in-repository .verdict symlink")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %q, want it to name the symlink", err)
	}
	if got := dirEntries(t, filepath.Join(r, "elsewhere")); len(got) != 0 {
		t.Fatalf("link target gained files: %v", got)
	}
}

// symlinkInside builds a root whose .verdict links at a sibling directory
// inside the same root.
func symlinkInside(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	inner := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inner, filepath.Join(root, VerdictDir)); err != nil {
		t.Fatalf("symlink .verdict: %v", err)
	}
	return root
}

// TestEnsureStateIgnoreRefusesGitignoreSymlink pins the overwrite rule:
// a symlinked ignore file is never written through, and its target is
// left byte-identical.
func TestEnsureStateIgnoreRefusesGitignoreSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, VerdictDir), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "shared-ignore")
	if err := os.WriteFile(target, []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, GitignorePath)); err != nil {
		t.Fatalf("symlink ignore: %v", err)
	}
	if err := NewLayout(root).EnsureStateIgnore(); err == nil {
		t.Fatal("EnsureStateIgnore wrote through a symlinked ignore file")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %q, want it to name the symlink", err)
	}
	kept, err := os.ReadFile(target)
	if err != nil || string(kept) != "*.log\n" {
		t.Fatalf("link target modified: %q, err = %v", kept, err)
	}
}

// TestEnsureStateDirRefusesStateSymlink pins the state-root rule: a
// symlinked state directory refuses before any bundle lands outside.
func TestEnsureStateDirRefusesStateSymlink(t *testing.T) {
	outside := escapeFixture(t)
	layout := NewLayout(t.TempDir())
	if err := layout.EnsureStateIgnore(); err != nil {
		t.Fatalf("EnsureStateIgnore: %v", err)
	}
	if err := os.Symlink(outside, layout.StateDir()); err != nil {
		t.Fatalf("symlink state: %v", err)
	}
	assertSymlinkOrEscapeRefusal(t, layout.EnsureStateDir(), "EnsureStateDir")
	assertUntouched(t, outside)
}

// TestEnsureStateIgnoreRefusesStateNegations pins the loud-negation rule:
// every negation able to match the state directory refuses naming the
// line, and the ignore file stays byte-identical.
func TestEnsureStateIgnoreRefusesStateNegations(t *testing.T) {
	for _, line := range []string{"!/state/", "!state", "!/state", "!st*", "!*", "!**", "!state/*"} {
		t.Run(line, func(t *testing.T) {
			assertNegationRefused(t, line)
		})
	}
}

// assertNegationRefused proves one operator negation refuses loudly
// naming its line and leaves the ignore file byte-identical to what the
// operator wrote.
func assertNegationRefused(t *testing.T, line string) {
	t.Helper()
	layout, before := negationFixture(t, line)
	if err := layout.EnsureStateIgnore(); err == nil {
		t.Fatalf("EnsureStateIgnore honored negation %q", line)
	} else if !strings.Contains(err.Error(), line) {
		t.Fatalf("error = %q, want it to name %q", err, line)
	}
	kept, rerr := os.ReadFile(layout.GitignoreFile())
	if rerr != nil || string(kept) != before {
		t.Fatalf("ignore modified: %q, err = %v", kept, rerr)
	}
}

// negationFixture builds a layout whose ignore file carries /state/ plus
// the operator negation under test, returning the exact bytes on disk.
func negationFixture(t *testing.T, line string) (Layout, string) {
	t.Helper()
	layout := NewLayout(t.TempDir())
	if err := os.MkdirAll(layout.VerdictDir(), 0o755); err != nil {
		t.Fatalf("mkdir verdict dir: %v", err)
	}
	before := "/state/\n" + line + "\n"
	if err := os.WriteFile(layout.GitignoreFile(), []byte(before), 0o644); err != nil {
		t.Fatalf("write ignore: %v", err)
	}
	return layout, before
}

// TestEnsureStateIgnorePreservesBenignNegations pins the preservation
// boundary: file negations that cannot match the state directory stay
// untouched while the state line is ensured.
func TestEnsureStateIgnorePreservesBenignNegations(t *testing.T) {
	root := t.TempDir()
	layout := NewLayout(root)
	if err := os.MkdirAll(layout.VerdictDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "# operator notes\n*.log\n!keep.log\n!*.json\n"
	if err := os.WriteFile(layout.GitignoreFile(), []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureStateIgnore(); err != nil {
		t.Fatalf("EnsureStateIgnore: %v", err)
	}
	kept, err := os.ReadFile(layout.GitignoreFile())
	if err != nil || string(kept) != before+"/state/\n" {
		t.Fatalf("ignore = %q, err = %v", kept, err)
	}
}

// checkIgnored reports whether git considers path ignored, running the
// real ignore machinery instead of asserting line presence.
func checkIgnored(t *testing.T, path string) bool {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required: the ignore contract proves through real git check-ignore")
	}
	cmd := exec.CommandContext(t.Context(), "git", "check-ignore", "-q", path)
	cmd.Env = testenv.Base(nil)
	return cmd.Run() == nil
}

// TestEnsureStateGeneratedFilesStayIgnored proves the ignore contract
// with git itself: preserved operator content plus the ensured lines
// keep generated state ignored while the shared configuration stays
// trackable. The `!*.json` negation is the sharp edge: it stays
// preserved, and the nested `*` layer still holds state files ignored.
func TestEnsureStateGeneratedFilesStayIgnored(t *testing.T) {
	t.Chdir(t.TempDir())
	gitRun(t, "init", "-q")
	layout := NewLayout(".")
	if err := layout.EnsureStateIgnore(); err != nil {
		t.Fatalf("EnsureStateIgnore: %v", err)
	}
	if err := layout.EnsureStateDir(); err != nil {
		t.Fatalf("EnsureStateDir: %v", err)
	}
	// Operator content with the sharp `!*.json` negation, then the
	// generated files under test.
	if err := os.WriteFile(layout.GitignoreFile(), []byte("# notes\n*.log\n!keep.log\n!*.json\n/state/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.ConfigFile(), []byte("{\"schema_version\":\"1\"}"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(layout.StateDir(), "evidence.json")
	if err := os.WriteFile(stateFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertIgnored(t, stateFile, "generated state file")
	assertIgnored(t, filepath.Join(layout.StateDir(), ".gitignore"), "state ignore layer")
	assertTrackable(t, layout.ConfigFile(), "shared configuration")
}

// assertIgnored fails unless git's own ignore machinery ignores path.
func assertIgnored(t *testing.T, path, what string) {
	t.Helper()
	if !checkIgnored(t, path) {
		t.Fatalf("git does not ignore the %s %s", what, path)
	}
}

// assertTrackable fails when git ignores a path that must stay tracked.
func assertTrackable(t *testing.T, path, what string) {
	t.Helper()
	if checkIgnored(t, path) {
		t.Fatalf("git ignores the %s %s", what, path)
	}
}

// TestParseCreatesNoState pins the read-side contract: parsing never
// creates the managed directory, so reads cannot edit shared config.
func TestParseCreatesNoState(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if _, ref := Parse([]byte(`{"schema_version":"1"}`)); ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	if _, err := os.Lstat(filepath.Join(root, VerdictDir)); !os.IsNotExist(err) {
		t.Fatalf(".verdict created by a read: err = %v", err)
	}
}
