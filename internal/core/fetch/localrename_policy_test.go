package fetchcore

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/testenv"
)

// localRenamePolicy is the trusted-base typed policy for the rename
// regression: an old-path-only clause and defaults ride the legacy scope,
// a distinct new-path scope carries its own defaults, and the root clause
// covers every path. Only the old path names the critical clause.
const localRenamePolicy = `{"schema_version":"1",` +
	`"defaults":{"display_min_severity":"important"},` +
	`"requirements":{"block_clauses":[{"name":"root-any","threshold":"informational","paths":["**"]}]},` +
	`"scopes":[` +
	`{"name":"legacy","include":["old/**"],"defaults":{"display_min_severity":"critical"},` +
	`"requirements":{"block_clauses":[{"name":"legacy-rule","threshold":"critical"}]}},` +
	`{"name":"newzone","include":["new/**"],"defaults":{"display_min_severity":"informational"}}]}`

// localRenameOldPath and localRenameNewPath are the two sides of the real
// staged rename Git records in the fixtures.
const (
	localRenameOldPath = "old/secret.go"
	localRenameNewPath = "new/secret.go"
	localRenameDeleted = "victim.go"
)

// realGitRepo returns a fresh repository sandbox with Git's own plumbing, so
// every fixture byte is what the producer reads on a real checkout. The child
// environment is the pinned hermetic test one.
func realGitRepo(t *testing.T) (dir string, git func(...string) string, write func(string, string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required: the rename producer is proven against a real staged rename")
	}
	dir = t.TempDir()
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = testenv.Base(map[string]string{
			"GIT_AUTHOR_NAME":     "rename",
			"GIT_AUTHOR_EMAIL":    "rename@example.invalid",
			"GIT_COMMITTER_NAME":  "rename",
			"GIT_COMMITTER_EMAIL": "rename@example.invalid",
		})
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	write = func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, git, write
}

// commitBase commits the fixture tree, moves the old path to the new one, and
// leaves the rename staged for the caller's edit.
func commitBase(t *testing.T, dir string, git func(...string) string, base string) {
	t.Helper()
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "base")
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(localRenameNewPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	git("mv", localRenameOldPath, localRenameNewPath)
	_ = base
}

// stagedRenameFixture builds a real repository, stages a real rename with a
// content edit plus a real deletion, and returns Git's own staged diff, the
// --no-renames file list the producer reads, and its numstat. Nothing here is
// canned: every byte comes from Git.
func stagedRenameFixture(t *testing.T) (string, string, string) {
	t.Helper()
	dir, git, write := realGitRepo(t)
	write(localRenameOldPath, "package old\n\n// SECRET_SENTINEL_OLD_PATH_CONTENT\nfunc Secret() int { return 1 }\n")
	write(localRenameDeleted, "package main\n\nfunc Victim() int { return 0 }\n")
	commitBase(t, dir, git, "")
	write(localRenameNewPath, "package old\n\n// SECRET_SENTINEL_OLD_PATH_CONTENT\nfunc Secret() int { return 1 }\n\n// rename edit line\nfunc Secret2() int { return 2 }\n")
	git("add", "-A")
	git("rm", "-q", localRenameDeleted)
	return git("diff", "--cached"), git("diff", "--no-renames", "--name-only", "--cached"), git("diff", "--numstat", "--cached")
}

// stagedPureRename builds a real repository and stages a rename with no
// content change, returning Git's staged diff and the --no-renames list.
func stagedPureRename(t *testing.T) (string, string) {
	t.Helper()
	dir, git, write := realGitRepo(t)
	write(localRenameOldPath, "package old\n\nfunc Secret() int { return 1 }\n")
	commitBase(t, dir, git, "")
	return git("diff", "--cached"), git("diff", "--no-renames", "--name-only", "--cached")
}

// TestLocalRenameJoinsOldPathPolicy proves the local producer carries the
// rename identity Git recorded: the captured new-path resolution keeps the old
// path beside it, so the old-path-only clause and the new-path scope both
// apply, the old-path provenance is recorded, no ghost old-path file or group
// survives, and one open critical finding on the new path blocks on both
// clauses. A real staged rename and deletion supply the bytes.
func TestLocalRenameJoinsOldPathPolicy(t *testing.T) {
	diff, listed, numstat := stagedRenameFixture(t)
	if !strings.Contains(diff, "rename from "+localRenameOldPath) || !strings.Contains(diff, "rename to "+localRenameNewPath) {
		t.Fatalf("fixture diff carries no real rename section:\n%s", diff)
	}
	cmdr := stagedGroupCommander(t, diff, listed, numstat, localRenamePolicy)
	if _, err := runStagedGroups(t, cmdr); err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	rs := capturedLocalRunState(t)
	assertRenameFiles(t, rs)
	assertRenameGroups(t, rs)
	assertRenameBlockers(t, rs)
}

// TestListedInputsPureRenameKeepsIdentity proves a rename with no content
// change still resolves as one joined file: the old-side list entry folds into
// the new path, so an old-only clause keeps applying and no ghost file stands
// alone. A real staged pure rename supplies the bytes.
func TestListedInputsPureRenameKeepsIdentity(t *testing.T) {
	diff, listed := stagedPureRename(t)
	if !strings.Contains(diff, "rename from "+localRenameOldPath) || strings.Contains(diff, "@@ ") {
		t.Fatalf("fixture is not a hunkless rename:\n%s", diff)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o600); err != nil {
		t.Fatal(err)
	}
	got := listedInputs(dir, listed)
	want := []config.FileInput{{Path: localRenameNewPath, OldPath: localRenameOldPath}}
	if !slices.Equal(got, want) {
		t.Fatalf("listedInputs = %+v, want %+v", got, want)
	}
}

// capturedLocalRunState reads the run bundle a staged local run persisted: the
// same record report and gate later evaluate.
func capturedLocalRunState(t *testing.T) *config.RunState {
	t.Helper()
	rs, found, err := config.ReadRunState(filepath.Join(".verdict", "state", "local-mine"))
	if err != nil {
		t.Fatalf("ReadRunState: %v", err)
	}
	if !found || rs == nil {
		t.Fatal("run.json missing from the local bundle")
	}
	return rs
}

// fileResolution finds one captured file resolution by path.
func fileResolution(t *testing.T, rs *config.RunState, path string) config.FileResolution {
	t.Helper()
	for i := range rs.Resolved.Files {
		if rs.Resolved.Files[i].Path == path {
			return rs.Resolved.Files[i]
		}
	}
	t.Fatalf("run.json has no file resolution for %q", path)
	return config.FileResolution{}
}

// clauseNames lists one file's accumulated clause names.
func clauseNames(file *config.FileResolution) []string {
	names := make([]string, 0, len(file.Requirements.Clauses))
	for i := range file.Requirements.Clauses {
		names = append(names, file.Requirements.Clauses[i].Clause.Name)
	}
	return names
}

// clauseSources lists one named clause's recorded provenances.
func clauseSources(file *config.FileResolution, name string) []string {
	for i := range file.Requirements.Clauses {
		if file.Requirements.Clauses[i].Clause.Name == name {
			return file.Requirements.Clauses[i].Sources
		}
	}
	return nil
}

// assertRenameFiles pins the captured file set: the rename is one file holding
// both paths and both clauses, the deletion survives on its own, and no ghost
// old-path entry stands alone.
func assertRenameFiles(t *testing.T, rs *config.RunState) {
	t.Helper()
	paths := make([]string, 0, len(rs.Resolved.Files))
	for i := range rs.Resolved.Files {
		paths = append(paths, rs.Resolved.Files[i].Path)
	}
	if want := []string{localRenameNewPath, localRenameDeleted}; !slices.Equal(paths, want) {
		t.Fatalf("captured file paths = %v, want %v (no ghost old-path file)", paths, want)
	}
	assertRenameIdentity(t, rs)
	deleted := fileResolution(t, rs, localRenameDeleted)
	if got, want := clauseNames(&deleted), []string{"root-any"}; !slices.Equal(got, want) {
		t.Fatalf("deleted file clauses = %v, want %v", got, want)
	}
}

// assertRenameIdentity pins the renamed file's joined identity: both paths,
// both clauses, both scopes, the old-path provenance, and the new scope's
// winning defaults.
func assertRenameIdentity(t *testing.T, rs *config.RunState) {
	t.Helper()
	renamed := fileResolution(t, rs, localRenameNewPath)
	if renamed.OldPath != localRenameOldPath {
		t.Fatalf("renamed file old_path = %q, want %q", renamed.OldPath, localRenameOldPath)
	}
	if got, want := clauseNames(&renamed), []string{"root-any", "legacy-rule"}; !slices.Equal(got, want) {
		t.Fatalf("renamed file clauses = %v, want %v", got, want)
	}
	if got, want := renamed.Scopes, []string{"legacy", "newzone"}; !slices.Equal(got, want) {
		t.Fatalf("renamed file scopes = %v, want %v", got, want)
	}
	sources := clauseSources(&renamed, "legacy-rule")
	if !slices.Contains(sources, "scope legacy (old path "+localRenameOldPath+")") {
		t.Fatalf("legacy-rule sources = %v, want the old-path provenance", sources)
	}
	if sev := renamed.Defaults.Values.DisplayMinSeverity; sev == nil || *sev != "informational" {
		t.Fatalf("renamed file display_min_severity = %v, want informational from scope newzone", sev)
	}
}

// assertRenameGroups pins the plan: one group holds the renamed file with both
// scopes, the deletion keeps its own group, and no ghost group stands for the
// old path alone.
func assertRenameGroups(t *testing.T, rs *config.RunState) {
	t.Helper()
	groups := rs.Resolved.Plan.Groups
	if len(groups) != 2 {
		t.Fatalf("plan groups = %d %+v, want 2", len(groups), groups)
	}
	for i := range groups {
		if slices.Contains(groups[i].Files, localRenameOldPath) {
			t.Fatalf("plan carries a ghost old-path group: %+v", groups[i])
		}
	}
	idx := renameGroupIndex(groups)
	if idx < 0 {
		t.Fatalf("plan has no group for %q: %+v", localRenameNewPath, groups)
	}
	if got, want := groups[idx].Files, []string{localRenameNewPath}; !slices.Equal(got, want) {
		t.Fatalf("renamed group files = %v, want %v", got, want)
	}
	if got, want := groups[idx].Scopes, []string{"legacy", "newzone"}; !slices.Equal(got, want) {
		t.Fatalf("renamed group scopes = %v, want %v", got, want)
	}
}

// renameGroupIndex returns the plan group holding the renamed file, or -1.
func renameGroupIndex(groups []config.PlanGroup) int {
	for i := range groups {
		if slices.Contains(groups[i].Files, localRenameNewPath) {
			return i
		}
	}
	return -1
}

// assertRenameBlockers pins compliance: one open critical finding on the new
// path meets both applicable clauses through the captured resolution.
func assertRenameBlockers(t *testing.T, rs *config.RunState) {
	t.Helper()
	got := config.EvaluateReadCompliance(rs, localTestHead, []config.FindingView{{
		Path: localRenameNewPath, Category: "logic", Severity: "critical", Status: "open",
	}})
	if len(got.Blockers) != 2 {
		t.Fatalf("blockers = %d %+v, want 2 (root-any and legacy-rule)", len(got.Blockers), got.Blockers)
	}
	blocking := []string{got.Blockers[0].Clause, got.Blockers[1].Clause}
	slices.Sort(blocking)
	if want := []string{"legacy-rule", "root-any"}; !slices.Equal(blocking, want) {
		t.Fatalf("blocking clauses = %v, want %v", blocking, want)
	}
}
