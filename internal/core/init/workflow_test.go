package initcore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// cleanCommander answers check-tree with a clean worktree: the tests below
// grade the workflow write and its refusals, never git.
type cleanCommander struct{}

func (cleanCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{}, nil
}

// ciRequest is one `init --ci <provider>` invocation rooted at repoRoot.
func ciRequest(repoRoot string, ci *ciProvider) *Request {
	req := &Request{Preset: "standard", CI: ci.Name(), Agent: "claude"}
	req.ReadRequest = core.ReadRequest{
		Runtime:  &core.Runtime{Commander: cleanCommander{}},
		RepoRoot: repoRoot,
	}
	return req
}

// mustRender is the workflow bytes the refusal tests plant behind a link.
func mustRender(t *testing.T, ci *ciProvider) []byte {
	t.Helper()
	body, ref := RenderWorkflow(ci.Name(), "claude", "")
	if ref != nil {
		t.Fatalf("render %s: %s: %s", ci.Name(), ref.Code, ref.Message)
	}
	return body
}

// mustRunInit runs one ciRequest and answers the response.
func mustRunInit(t *testing.T, req *Request) *Response {
	t.Helper()
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("init refused: %s: %s", ref.Code, ref.Message)
	}
	return resp
}

// mustMkdirAll creates a fixture directory.
func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// mustWriteFile writes one fixture file.
func mustWriteFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustSymlink points link at target.
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// assertRefusedNoWrites proves the refusal happened before .verdict/ existed:
// zero partial writes, on every platform.
func assertRefusedNoWrites(t *testing.T, repo string, ci *ciProvider) {
	t.Helper()
	_, ref := Run(t.Context(), ciRequest(repo, ci))
	if ref == nil {
		t.Fatalf("%s: init wrote through the symlinked path instead of refusing", ci.Name())
	}
	if ref.Code != "init-failed" {
		t.Fatalf("%s: refusal code = %q, want init-failed (%s)", ci.Name(), ref.Code, ref.Message)
	}
	if _, err := os.Lstat(filepath.Join(repo, reviewDotName)); !os.IsNotExist(err) {
		t.Fatalf("%s: the refused run created %s (err=%v)", ci.Name(), reviewDotName, err)
	}
}

// TestWorkflowStatusRefusesASymlinkedParent proves the keep path cannot be
// reached through a link: an identical workflow behind a symlinked top-level
// directory is still refused, so a run never reports keep over a path it did
// not write.
func TestWorkflowStatusRefusesASymlinkedParent(t *testing.T) {
	t.Parallel()
	for _, ci := range Providers() {
		assertSymlinkedParentRefused(t, ci)
	}
}

// assertSymlinkedParentRefused plants the workflow behind a link at the
// provider's top-level directory.
func assertSymlinkedParentRefused(t *testing.T, ci *ciProvider) {
	t.Helper()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	outside := filepath.Join(base, "outside")
	mustMkdirAll(t, filepath.Join(outside, "workflows"))
	mustMkdirAll(t, repo)
	mustWriteFile(t, filepath.Join(outside, "workflows", ci.base), mustRender(t, ci))
	mustSymlink(t, outside, filepath.Join(repo, ci.dirs[0]))
	assertRefusedNoWrites(t, repo, ci)
}

// TestWorkflowStatusRefusesASymlinkedWorkflowsDir covers the deeper component:
// the top-level directory is real, its workflows child is the link.
func TestWorkflowStatusRefusesASymlinkedWorkflowsDir(t *testing.T) {
	t.Parallel()
	for _, ci := range Providers() {
		assertSymlinkedWorkflowsDirRefused(t, ci)
	}
}

// assertSymlinkedWorkflowsDirRefused plants the workflow behind a link at the
// workflows component.
func assertSymlinkedWorkflowsDirRefused(t *testing.T, ci *ciProvider) {
	t.Helper()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	outside := filepath.Join(base, "outside")
	mustMkdirAll(t, outside)
	mustMkdirAll(t, filepath.Join(repo, ci.dirs[0]))
	mustWriteFile(t, filepath.Join(outside, ci.base), mustRender(t, ci))
	mustSymlink(t, outside, filepath.Join(repo, ci.dirs[1]))
	assertRefusedNoWrites(t, repo, ci)
}

// TestWorkflowStatusRefusesAPlainFileAtTopDir proves a non-directory component
// refuses on Unix and Windows alike, before .verdict/ exists.
func TestWorkflowStatusRefusesAPlainFileAtTopDir(t *testing.T) {
	t.Parallel()
	for _, ci := range Providers() {
		repo := t.TempDir()
		mustWriteFile(t, filepath.Join(repo, ci.dirs[0]), []byte("not a directory\n"))
		assertRefusedNoWrites(t, repo, ci)
	}
}

// TestWorkflowStatusRefusesASymlinkedWorkflowFile proves the last component is
// pinned too: a symlinked verdict-review.yml is refused, never followed.
func TestWorkflowStatusRefusesASymlinkedWorkflowFile(t *testing.T) {
	t.Parallel()
	for _, ci := range Providers() {
		assertSymlinkedWorkflowRefused(t, ci)
	}
}

// assertSymlinkedWorkflowRefused plants a symlink at the workflow file itself.
func assertSymlinkedWorkflowRefused(t *testing.T, ci *ciProvider) {
	t.Helper()
	repo := t.TempDir()
	mustMkdirAll(t, filepath.Join(repo, ci.dirs[1]))
	target := filepath.Join(repo, "elsewhere.yml")
	mustWriteFile(t, target, mustRender(t, ci))
	mustSymlink(t, target, filepath.Join(repo, ci.path()))
	assertRefusedNoWrites(t, repo, ci)
}

// TestWorkflowWritesUnderASymlinkedAncestor proves the traversal stays strictly
// below the repository root: a repo reached through a symlinked workspace or a
// /var alias writes normally, because nothing above the root is inspected.
func TestWorkflowWritesUnderASymlinkedAncestor(t *testing.T) {
	t.Parallel()
	for _, ci := range Providers() {
		assertWritesUnderAncestor(t, ci)
	}
}

// assertWritesUnderAncestor runs init over a symlinked repository root.
func assertWritesUnderAncestor(t *testing.T, ci *ciProvider) {
	t.Helper()
	base := t.TempDir()
	realRepo := filepath.Join(base, "real")
	mustMkdirAll(t, realRepo)
	link := filepath.Join(base, "link")
	mustSymlink(t, realRepo, link)
	resp := mustRunInit(t, ciRequest(link, ci))
	if resp.Data.WorkflowStatus != statusCreate {
		t.Fatalf("workflow_status = %q, want %q", resp.Data.WorkflowStatus, statusCreate)
	}
	wrote, err := os.ReadFile(filepath.Join(realRepo, ci.path()))
	if err != nil {
		t.Fatalf("the workflow did not land under the real root: %v", err)
	}
	if !bytes.Equal(wrote, mustRender(t, ci)) {
		t.Fatal("the written workflow is not the rendered bytes")
	}
	if _, err := os.Stat(filepath.Join(realRepo, reviewDotName, configName)); err != nil {
		t.Fatalf("the control files did not land under the real root: %v", err)
	}
}

// gitlabTarget is the one provider that plans a second file at the repository
// root, so the cases below can name it once.
func gitlabTarget(t *testing.T) *ciProvider {
	t.Helper()
	ci, ok := LookupCI("gitlab")
	if !ok {
		t.Fatal("the gitlab provider is gone")
	}
	return ci
}

// mustRootFile is the rendered root pipeline the tests compare bytes against.
func mustRootFile(t *testing.T) []byte {
	t.Helper()
	_, body, ok := RootFile("gitlab")
	if !ok {
		t.Fatal("RootFile(gitlab) reports no second file")
	}
	return body
}

// TestRootPipelineIsCreatedOnlyWhenAbsent proves the one transition init owns
// at the repository root: an absent pipeline becomes the rendered include file,
// and the closing instruction says both files are now the operator's to commit.
func TestRootPipelineIsCreatedOnlyWhenAbsent(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	resp := mustRunInit(t, ciRequest(repo, gitlabTarget(t)))
	if resp.Data.RootStatus != statusCreate {
		t.Fatalf("root_status = %q, want %q", resp.Data.RootStatus, statusCreate)
	}
	wrote, err := os.ReadFile(filepath.Join(repo, ".gitlab-ci.yml"))
	if err != nil {
		t.Fatalf("the root pipeline did not land: %v", err)
	}
	if !bytes.Equal(wrote, mustRootFile(t)) {
		t.Fatalf("the written root pipeline is not the rendered bytes:\n%s", wrote)
	}
	if want := "commit .gitlab/ci/verdict-review.yml and .gitlab-ci.yml with .verdict/, " +
		"set the CI/CD variable VERDICT_VERSION to a release with run --agent, " +
		"then open a merge request"; resp.Next != want {
		t.Fatalf("next = %q, want %q", resp.Next, want)
	}
	if resp.Data.IncludeSnippet != IncludeSnippet("gitlab") {
		t.Fatalf("include_snippet = %q", resp.Data.IncludeSnippet)
	}
}

// TestRootPipelineIsNeverOverwritten proves an existing root belongs to the
// operator: a divergent file stays byte-identical with and without --force, and
// the next line tells the operator to add the include themselves.
func TestRootPipelineIsNeverOverwritten(t *testing.T) {
	t.Parallel()
	for _, force := range []bool{false, true} {
		t.Run(strconv.FormatBool(force), func(t *testing.T) {
			t.Parallel()
			assertRootPipelineKept(t, force)
		})
	}
}

// rootPresentNext is the closing instruction for a root pipeline init left
// alone: the operator adds the include themselves.
const rootPresentNext = "commit .gitlab/ci/verdict-review.yml with .verdict/, " +
	"add the include to .gitlab-ci.yml yourself, set the CI/CD variable " +
	"VERDICT_VERSION to a release with run --agent, then open a merge request"

// assertRootPipelineKept proves one --force setting leaves a divergent root
// pipeline byte-identical, reports present, and still writes the owned file.
func assertRootPipelineKept(t *testing.T, force bool) {
	t.Helper()
	seed := []byte("stages: [build, deploy]\n")
	repo := t.TempDir()
	mustWriteFile(t, filepath.Join(repo, ".gitlab-ci.yml"), seed)
	req := ciRequest(repo, gitlabTarget(t))
	req.Yes = force
	resp := mustRunInit(t, req)
	if resp.Data.RootStatus != statusPresent {
		t.Fatalf("root_status = %q, want %q", resp.Data.RootStatus, statusPresent)
	}
	got, err := os.ReadFile(filepath.Join(repo, ".gitlab-ci.yml"))
	if err != nil || !bytes.Equal(got, seed) {
		t.Fatalf("the operator's root pipeline changed: %q, %v", got, err)
	}
	if resp.Next != rootPresentNext {
		t.Fatalf("next = %q, want %q", resp.Next, rootPresentNext)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitlab", "ci", "verdict-review.yml")); err != nil {
		t.Fatalf("the owned pipeline did not land: %v", err)
	}
}

// TestRootPipelineIdenticalBytesReadAsKeep proves a second run over its own
// output reports keep instead of rewriting, so the command is idempotent.
func TestRootPipelineIdenticalBytesReadAsKeep(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	mustWriteFile(t, filepath.Join(repo, ".gitlab-ci.yml"), mustRootFile(t))
	resp := mustRunInit(t, ciRequest(repo, gitlabTarget(t)))
	if resp.Data.RootStatus != statusKeep {
		t.Fatalf("root_status = %q, want %q", resp.Data.RootStatus, statusKeep)
	}
}

// TestRootPipelineRefusesASymlink proves a symlinked root pipeline is never
// followed: it refuses by name before .verdict/ exists.
func TestRootPipelineRefusesASymlink(t *testing.T) {
	t.Parallel()
	assertRootPipelineRefused(t, plantSymlinkedRoot)
}

// TestRootPipelineRefusesADirectory proves a directory where the root pipeline
// belongs refuses on Unix and Windows alike, and writes nothing first.
func TestRootPipelineRefusesADirectory(t *testing.T) {
	t.Parallel()
	assertRootPipelineRefused(t, plantDirectoryRoot)
}

// plantSymlinkedRoot points the root pipeline at a file outside the repository.
func plantSymlinkedRoot(t *testing.T, repo string) {
	t.Helper()
	mustWriteFile(t, filepath.Join(repo, "elsewhere.yml"), mustRootFile(t))
	mustSymlink(t, filepath.Join(repo, "elsewhere.yml"), filepath.Join(repo, ".gitlab-ci.yml"))
}

// plantDirectoryRoot creates a directory where the root pipeline belongs.
func plantDirectoryRoot(t *testing.T, repo string) {
	t.Helper()
	mustMkdirAll(t, filepath.Join(repo, ".gitlab-ci.yml"))
}

// assertRootPipelineRefused plants one non-regular root pipeline and proves the
// refusal is typed, path-derived, and write-free.
func assertRootPipelineRefused(t *testing.T, plant func(t *testing.T, repo string)) {
	t.Helper()
	repo := t.TempDir()
	plant(t, repo)
	_, ref := Run(t.Context(), ciRequest(repo, gitlabTarget(t)))
	if ref == nil || ref.Code != codeInitFailed {
		t.Fatalf("refusal = %v, want init-failed", ref)
	}
	want := "init will not write .gitlab-ci.yml: the path is not a regular file"
	if ref.Message != want {
		t.Fatalf("refusal message = %q, want %q", ref.Message, want)
	}
	if _, err := os.Lstat(filepath.Join(repo, reviewDotName)); !os.IsNotExist(err) {
		t.Fatalf("the refused run created %s (err=%v)", reviewDotName, err)
	}
}

// TestPrintPlansBothFiles proves --print renders both bodies and the include
// without reading or writing the tree: no .gitlab/, no .gitlab-ci.yml, and no
// .verdict/.
func TestPrintPlansBothFiles(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	var out bytes.Buffer
	req := ciRequest(repo, gitlabTarget(t))
	req.Print = true
	req.Human = &out
	resp := mustRunInit(t, req)
	assertPrintedData(t, resp.Data)
	assertPrintedSections(t, out.String())
	assertNothingWritten(t, repo)
}

// assertPrintedData pins the envelope's two-file fields.
func assertPrintedData(t *testing.T, data *Data) {
	t.Helper()
	if data.RootPath != ".gitlab-ci.yml" {
		t.Fatalf("root_path = %q, want .gitlab-ci.yml", data.RootPath)
	}
	if data.RootWorkflow != string(mustRootFile(t)) {
		t.Fatalf("root_workflow is not the rendered root pipeline:\n%s", data.RootWorkflow)
	}
	if data.IncludeSnippet != IncludeSnippet("gitlab") {
		t.Fatalf("include_snippet = %q", data.IncludeSnippet)
	}
	if data.Workflow == "" || len(data.Files) != 0 {
		t.Fatalf("printed files = %q, pipeline bytes = %d", data.Files, len(data.Workflow))
	}
}

// assertPrintedSections pins the human --print output's three headed sections.
func assertPrintedSections(t *testing.T, out string) {
	t.Helper()
	for _, want := range []string{
		"--- .gitlab/ci/verdict-review.yml (rendered) ---",
		"--- .gitlab-ci.yml (created only when absent; never overwritten) ---",
		"--- add this to an existing .gitlab-ci.yml ---",
		IncludeSnippet("gitlab"),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("--print output carries no %q:\n%s", want, out)
		}
	}
}

// assertNothingWritten proves a render-only run created no path it previewed.
func assertNothingWritten(t *testing.T, repo string) {
	t.Helper()
	for _, rel := range []string{".gitlab", ".gitlab-ci.yml", reviewDotName} {
		if _, err := os.Lstat(filepath.Join(repo, rel)); !os.IsNotExist(err) {
			t.Fatalf("--print created %s (err=%v)", rel, err)
		}
	}
}

// TestWorkflowStatusReportsKeepWithoutWriting proves the ordinary keep path
// still answers keep: a clean workflow directory with identical bytes is not
// rewritten.
func TestWorkflowStatusReportsKeepWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, ci := range Providers() {
		repo := t.TempDir()
		mustMkdirAll(t, filepath.Join(repo, ci.dirs[1]))
		mustWriteFile(t, filepath.Join(repo, ci.path()), mustRender(t, ci))
		resp := mustRunInit(t, ciRequest(repo, ci))
		if resp.Data.WorkflowStatus != statusKeep {
			t.Fatalf("workflow_status = %q, want %q", resp.Data.WorkflowStatus, statusKeep)
		}
	}
}
