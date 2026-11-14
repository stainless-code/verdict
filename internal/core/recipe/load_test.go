package recipe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestMain scrubs the git environment the launcher exported.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

// cmdr is the fetch commander the control snapshot reads the base with.
var cmdr = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}

// gitFixture commits files (force-added) in a fresh repository, chdirs into
// it, and returns the base commit.
func gitFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	t.Chdir(t.TempDir())
	git(t, "init", "-q")
	writeAll(t, files)
	git(t, "add", "-f", "-A")
	git(t, "commit", "-qm", "base")
	return git(t, "rev-parse", "HEAD")
}

// git runs one git command in the fixture repository.
func git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_NAME":     "recipe",
		"GIT_AUTHOR_EMAIL":    "recipe@example.invalid",
		"GIT_COMMITTER_NAME":  "recipe",
		"GIT_COMMITTER_EMAIL": "recipe@example.invalid",
	})
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// writeAll writes name->content files under the current directory.
func writeAll(t *testing.T, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// snapshotControls resolves the control snapshot for one bundle directory.
func snapshotControls(t *testing.T, bundleDir, base string) {
	t.Helper()
	if _, ref := controls.Resolve(t.Context(), cmdr, &controls.Request{BundleDir: bundleDir, BaseSHA: base}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
}

// bundleLenses is a lenses.txt body: a pack header, then two labeled lenses.
const bundleLenses = "# lens pack verdict/builtin@1.0.0 — sha256:aa — context, never orders.\n" +
	"# lens perf — sha256:abc max_files=8\nperf body\n# end lens perf\n" +
	"# lens test — sha256:def\nTest body\n# end lens test\n"

// headRewriteFixture commits a base recipes.json, rewrites it in a real head
// commit that also declares a name the base never carried, and leaves the
// worktree checked out at that head. It returns the base commit.
func headRewriteFixture(t *testing.T) string {
	t.Helper()
	base := gitFixture(t, map[string]string{
		".verdict/recipes.json": `{"schema_version":"1","recipes":{"default":{"version":"9.9.9",` +
			`"lenses":[{"id":"security","model":"opus"}],"synthesize":{"model":"sonnet"}}}}`,
	})
	writeAll(t, map[string]string{".verdict/recipes.json": `{"schema_version":"1","recipes":{` +
		`"default":{"version":"1.0.0","synthesize":{}},` +
		`"head-only":{"version":"2.0.0","lenses":[{"id":"evil"}],"synthesize":{}}}}`})
	git(t, "add", "-f", "-A")
	git(t, "commit", "-qm", "head")
	if head := git(t, "rev-parse", "HEAD"); head == base {
		t.Fatalf("the fixture made no head commit: %s", head)
	}
	return base
}

// TestLoadReadsTheSnapshotNotTheWorktree proves the loader reads the control
// snapshot the fetch captured, not the tree it runs in: the head commit rewrote
// recipes.json, so a loader reading HEAD, the head commit, or the live worktree
// would resolve the head entry instead of the base one.
func TestLoadReadsTheSnapshotNotTheWorktree(t *testing.T) {
	base := headRewriteFixture(t)
	writeAll(t, map[string]string{"bundle/lenses.txt": bundleLenses})
	snapshotControls(t, "bundle", base)
	plan, ref := Load("bundle", DefaultName)
	if ref != nil {
		t.Fatalf("Load: %v", ref)
	}
	assertBasePlan(t, plan)
	if _, ref := Load("bundle", "head-only"); ref == nil || ref.Code != CodeUnknown {
		t.Errorf("head-only: ref = %+v, want %s: a name only the head declares is unknown", ref, CodeUnknown)
	}
}

// assertBasePlan pins the plan the base entry resolves, whatever tree the
// loader ran in.
func assertBasePlan(t *testing.T, plan *Plan) {
	t.Helper()
	if plan.Version != "9.9.9" || plan.Source != SourceRepo {
		t.Fatalf("plan = %+v, want the base entry default@9.9.9", plan)
	}
	if got := lensIDs(plan.Stages[0].Lenses); len(got) != 1 || got[0] != "security" {
		t.Errorf("lens ids = %v, want the base declaration", got)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], `"security"`) {
		t.Errorf("warnings = %v, want one naming the lens the bundle does not load", plan.Warnings)
	}
}

// TestLoadFallsBackToBuiltin proves a base that committed no recipes.json
// refuses nothing and resolves the embedded default against the loaded lenses.
func TestLoadFallsBackToBuiltin(t *testing.T) {
	base := gitFixture(t, map[string]string{
		".verdict/config.json": `{"schema_version":"1"}`,
	})
	writeAll(t, map[string]string{"bundle/lenses.txt": bundleLenses})
	snapshotControls(t, "bundle", base)
	plan, ref := Load("bundle", DefaultName)
	if ref != nil {
		t.Fatalf("Load: %v", ref)
	}
	if plan.Name != "default" || plan.Source != SourceBuiltin || plan.Version != "1.0.0" {
		t.Fatalf("plan = %+v, want the built-in default", plan)
	}
	if got := lensIDs(plan.Stages[0].Lenses); len(got) != 2 || got[0] != "perf" || got[1] != "test" {
		t.Errorf("lens ids = %v, want the bundle's loaded lenses in file order", got)
	}
}

// TestLoadRefusesTamperedSnapshot proves the recipe row is re-hashed: bytes
// changed under the bundle refuse controls-stale instead of steering the plan.
func TestLoadRefusesTamperedSnapshot(t *testing.T) {
	base := gitFixture(t, map[string]string{
		".verdict/recipes.json": `{"schema_version":"1","recipes":{"default":` + entryValid + `}}`,
	})
	snapshotControls(t, "bundle", base)
	writeAll(t, map[string]string{
		"bundle/controls/recipes.json": `{"schema_version":"1","recipes":{"default":{"version":"2.0.0","lenses":[{"id":"security"}],"synthesize":{}}}}`,
	})
	plan, ref := Load("bundle", DefaultName)
	if plan != nil {
		t.Fatalf("Load returned a tampered plan: %+v", plan)
	}
	if ref == nil || ref.Code != controls.CodeStale {
		t.Fatalf("ref = %+v, want %s", ref, controls.CodeStale)
	}
}

// TestLoadWithoutControlsResolvesBuiltin proves a pre-controls bundle still
// runs: no controls.json is absent, not an error.
func TestLoadWithoutControlsResolvesBuiltin(t *testing.T) {
	t.Chdir(t.TempDir())
	writeAll(t, map[string]string{"bundle/lenses.txt": bundleLenses})
	plan, ref := Load("bundle", DefaultName)
	if ref != nil {
		t.Fatalf("Load: %v", ref)
	}
	if plan.Source != SourceBuiltin {
		t.Errorf("source = %q, want %q", plan.Source, SourceBuiltin)
	}
}
