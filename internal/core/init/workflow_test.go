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

// ciRequest is one `init --ci github` invocation rooted at repoRoot.
func ciRequest(repoRoot string) *Request {
	req := &Request{Preset: "standard", CI: "github", Agent: "claude"}
	req.ReadRequest = core.ReadRequest{
		Runtime:  &core.Runtime{Commander: cleanCommander{}},
		RepoRoot: repoRoot,
	}
	return req
}

// mustRender is the workflow bytes the refusal tests plant behind a link.
func mustRender(t *testing.T) []byte {
	t.Helper()
	body, ref := RenderWorkflow("claude", "")
	if ref != nil {
		t.Fatalf("render: %s: %s", ref.Code, ref.Message)
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

// assertRefusedNoWrites proves the refusal happened before .verdict/ existed:
// zero partial writes, on every platform.
func assertRefusedNoWrites(t *testing.T, repo string) {
	t.Helper()
	_, ref := Run(t.Context(), ciRequest(repo))
	if ref == nil {
		t.Fatal("init wrote through the symlinked path instead of refusing")
	}
	if ref.Code != "init-failed" {
		t.Fatalf("refusal code = %q, want init-failed (%s)", ref.Code, ref.Message)
	}
	if _, err := os.Lstat(filepath.Join(repo, reviewDotName)); !os.IsNotExist(err) {
		t.Fatalf("the refused run created %s (err=%v)", reviewDotName, err)
	}
}

// TestWorkflowStatusRefusesASymlinkedParent proves the keep path cannot be
// reached through a link: an identical workflow behind a symlinked .github is
// still refused, so a run never reports keep over a path it did not write.
func TestWorkflowStatusRefusesASymlinkedParent(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "workflows", workflowBase), mustRender(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, ".github")); err != nil {
		t.Fatal(err)
	}
	assertRefusedNoWrites(t, repo)
}

// TestWorkflowStatusRefusesASymlinkedWorkflowsDir covers the deeper component:
// .github is real, .github/workflows is the link.
func TestWorkflowStatusRefusesASymlinkedWorkflowsDir(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, workflowBase), mustRender(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, workflowDir)); err != nil {
		t.Fatal(err)
	}
	assertRefusedNoWrites(t, repo)
}

// TestWorkflowStatusRefusesAPlainFileAtGithubDir proves a non-directory .github
// refuses on Unix and Windows alike, before .verdict/ exists.
func TestWorkflowStatusRefusesAPlainFileAtGithubDir(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".github"), []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertRefusedNoWrites(t, repo)
}

// TestWorkflowStatusRefusesASymlinkedWorkflowFile proves the last component is
// pinned too: a symlinked verdict-review.yml is refused, never followed.
func TestWorkflowStatusRefusesASymlinkedWorkflowFile(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, workflowDir), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "elsewhere.yml")
	if err := os.WriteFile(target, mustRender(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repo, workflowPath)); err != nil {
		t.Fatal(err)
	}
	assertRefusedNoWrites(t, repo)
}

// TestWorkflowWritesUnderASymlinkedAncestor proves the traversal stays strictly
// below the repository root: a repo reached through a symlinked workspace or a
// /var alias writes normally, because nothing above the root is inspected.
func TestWorkflowWritesUnderASymlinkedAncestor(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	realRepo := filepath.Join(base, "real")
	if err := os.MkdirAll(realRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realRepo, link); err != nil {
		t.Fatal(err)
	}
	resp := mustRunInit(t, ciRequest(link))
	if resp.Data.WorkflowStatus != statusCreate {
		t.Fatalf("workflow_status = %q, want %q", resp.Data.WorkflowStatus, statusCreate)
	}
	wrote, err := os.ReadFile(filepath.Join(realRepo, workflowPath))
	if err != nil {
		t.Fatalf("the workflow did not land under the real root: %v", err)
	}
	if !bytes.Equal(wrote, mustRender(t)) {
		t.Fatal("the written workflow is not the rendered bytes")
	}
	if _, err := os.Stat(filepath.Join(realRepo, reviewDotName, configName)); err != nil {
		t.Fatalf("the control files did not land under the real root: %v", err)
	}
}

// TestWorkflowStatusReportsKeepOnIdenticalBytes proves the ordinary keep path
// still answers keep: a clean .github/workflows with identical bytes is not
// rewritten.
func TestWorkflowStatusReportsKeepWithoutWriting(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, workflowDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, workflowPath), mustRender(t), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := mustRunInit(t, ciRequest(repo))
	if resp.Data.WorkflowStatus != statusKeep {
		t.Fatalf("workflow_status = %q, want %q", resp.Data.WorkflowStatus, statusKeep)
	}
}
