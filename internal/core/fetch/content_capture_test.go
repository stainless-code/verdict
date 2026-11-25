package fetchcore

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// editAfterNaming runs git for real and, the moment the bundle's content has
// been named (`write-tree` returned), rewrites a tracked file — a working
// tree that moves between the naming and every later read.
type editAfterNaming struct {
	path, body string
	edited     bool
}

func (c *editAfterNaming) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	res, err := coreexec.Runner{}.Run(ctx, cmd)
	if !c.edited && len(cmd.Argv) > 3 && cmd.Argv[3] == "write-tree" {
		c.edited = true
		if werr := os.WriteFile(c.path, []byte(c.body), 0o600); werr != nil {
			return res, werr
		}
	}
	return res, err
}

// LookPath finds every tool on PATH, as the real commander does.
func (c *editAfterNaming) LookPath(tool string) error {
	_, err := exec.LookPath(tool)
	return err
}

// TestRunLocalBundleIsTheNamedContent pins that a working-tree bundle's diff,
// file list, and stats describe exactly the tree source.json names: an edit
// made right after the content is named — which a later read of the live
// tree would see, even if it is reverted before the capture ends — never
// reaches the bundle.
func TestRunLocalBundleIsTheNamedContent(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.WaitDelay = 10 * time.Second
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	app := filepath.Join(root, "app.txt")
	mustWrite(t, app, "base\n")
	git("add", "app.txt")
	git("commit", "-q", "-m", "base")
	mustWrite(t, app, "reviewed edit\n")
	cmdr := &editAfterNaming{path: app, body: "unreviewed edit\n"}
	var human, diag bytes.Buffer
	var streamed []string
	req := worktreeReq(nil, &human, &diag, &streamed)
	req.Commander = cmdr
	req.Flags = LocalFlags{HeadControls: true}
	if _, err := RunLocal(context.Background(), req); err != nil {
		t.Fatalf("RunLocal: %v\n%s", err, diag.String())
	}
	if !cmdr.edited {
		t.Fatal("the content was never named")
	}
	bundle := filepath.Join(".verdict", "state", "local-mine")
	diff := mustRead(t, filepath.Join(bundle, "full.diff"))
	if !strings.Contains(diff, "+reviewed edit") || strings.Contains(diff, "unreviewed") {
		t.Fatalf("full.diff holds content other than the named tree's:\n%s", diff)
	}
	tree := readcommon.JSONStr(filepath.Join(bundle, "source.json"), "content_tree")
	if got := git("cat-file", "blob", tree+":app.txt"); got != "reviewed edit" {
		t.Errorf("content_tree %s holds app.txt %q, want the reviewed edit", tree, got)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
