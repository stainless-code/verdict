package initcore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
func ciRequest(repoRoot string, ci ciProvider) *Request {
	req := &Request{Preset: "standard", CI: ci.Name(), Agent: "claude"}
	req.ReadRequest = core.ReadRequest{
		Runtime:  &core.Runtime{Commander: cleanCommander{}},
		RepoRoot: repoRoot,
	}
	return req
}

// mustRender is the workflow bytes the refusal tests plant behind a link.
func mustRender(t *testing.T, ci ciProvider) []byte {
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
func assertRefusedNoWrites(t *testing.T, repo string, ci ciProvider) {
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
func assertSymlinkedParentRefused(t *testing.T, ci ciProvider) {
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
func assertSymlinkedWorkflowsDirRefused(t *testing.T, ci ciProvider) {
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
func assertSymlinkedWorkflowRefused(t *testing.T, ci ciProvider) {
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
func assertWritesUnderAncestor(t *testing.T, ci ciProvider) {
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
