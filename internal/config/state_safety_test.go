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

// dirEntries lists the names directly inside dir, failing the test on
// error: symlink escapes must leave the target exactly as found.
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

// TestEnsureStateIgnoreRefusesVerdictSymlinkOutside pins the escape rule:
// a .verdict symlink pointing out of the repository refuses, and the
// link target gains no state files.
func TestEnsureStateIgnoreRefusesVerdictSymlinkOutside(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "marker.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := symlinkFixture(t, outside)
	if err := NewLayout(root).EnsureStateIgnore(); err == nil {
		t.Fatal("EnsureStateIgnore followed a .verdict symlink outside the repository")
	} else if !strings.Contains(err.Error(), "symlink") && !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("error = %q, want a symlink or escape refusal", err)
	}
	if got := dirEntries(t, outside); len(got) != 1 || got[0] != "marker.txt" {
		t.Fatalf("outside target gained files: %v", got)
	}
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
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "marker.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	layout := NewLayout(root)
	if err := layout.EnsureStateIgnore(); err != nil {
		t.Fatalf("EnsureStateIgnore: %v", err)
	}
	if err := os.Symlink(outside, layout.StateDir()); err != nil {
		t.Fatalf("symlink state: %v", err)
	}
	if err := layout.EnsureStateDir(); err == nil {
		t.Fatal("EnsureStateDir followed a state symlink outside the repository")
	} else if !strings.Contains(err.Error(), "symlink") && !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("error = %q, want a symlink or escape refusal", err)
	}
	if got := dirEntries(t, outside); len(got) != 1 || got[0] != "marker.txt" {
		t.Fatalf("outside target gained files: %v", got)
	}
}

// TestEnsureStateIgnoreRefusesStateNegations pins the loud-negation rule:
// every negation able to match the state directory refuses naming the
// line, and the ignore file stays byte-identical.
func TestEnsureStateIgnoreRefusesStateNegations(t *testing.T) {
	for _, line := range []string{"!/state/", "!state", "!/state", "!st*", "!*", "!**", "!state/*"} {
		t.Run(line, func(t *testing.T) {
			root := t.TempDir()
			layout := NewLayout(root)
			if err := os.MkdirAll(layout.VerdictDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			before := "/state/\n" + line + "\n"
			if err := os.WriteFile(layout.GitignoreFile(), []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := layout.EnsureStateIgnore(); err == nil {
				t.Fatalf("EnsureStateIgnore honored negation %q", line)
			} else if !strings.Contains(err.Error(), line) {
				t.Fatalf("error = %q, want it to name %q", err, line)
			}
			kept, rerr := os.ReadFile(layout.GitignoreFile())
			if rerr != nil || string(kept) != before {
				t.Fatalf("ignore modified: %q, err = %v", kept, rerr)
			}
		})
	}
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
		t.Skip("git unavailable")
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
	if !checkIgnored(t, stateFile) {
		t.Fatalf("git does not ignore generated %s", stateFile)
	}
	if !checkIgnored(t, filepath.Join(layout.StateDir(), ".gitignore")) {
		t.Fatalf("git does not ignore the state ignore layer")
	}
	if checkIgnored(t, layout.ConfigFile()) {
		t.Fatalf("git ignores the shared configuration %s", layout.ConfigFile())
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
