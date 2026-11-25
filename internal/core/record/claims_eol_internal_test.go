package recordcore

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
)

// TestCRLFCheckoutReceiptBindsAPRClaim pins the end to end binding on Git
// for Windows' default, core.autocrlf=true: a PR head commit of LF files is
// checked out as CRLF, verify's snapshot names its content, and record must
// grade the PR bundle's VERIFIED claim against that receipt as the head
// commit's content — not refuse it as a receipt for other content.
func TestCRLFCheckoutReceiptBindsAPRClaim(t *testing.T) {
	root, head := crlfCheckout(t)
	ctx := context.Background()
	g := contenttree.Git{Cmdr: coreexec.Runner{}, Root: root}
	dest := filepath.Join(t.TempDir(), "snap")
	content, err := g.Snapshot(ctx, dest)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	t.Cleanup(func() {
		if err := g.RemoveCheckout(context.Background(), dest); err != nil {
			t.Errorf("RemoveCheckout: %v", err)
		}
	})
	const cmd = "go test ./..."
	receipt := core.VerifyReceipt{Command: cmd, Tree: content.Tree}
	tree, gap, err := gradedTree(ctx, coreexec.Runner{}, root, t.TempDir(), "github", head)
	if err != nil || gap != "" {
		t.Fatalf("gradedTree = %s, gap %q (%v)", tree, gap, err)
	}
	if refused := receiptBindingGap(&receipt, tree, gap, "", cmd, nil); refused != "" {
		t.Fatalf("the PR claim did not bind: %s", refused)
	}
}

// crlfCheckout commits LF files and checks them out again under
// core.autocrlf=true (set at command scope, after testenv's pinned false), so
// they are CRLF on disk. It returns the root and the commit.
func crlfCheckout(t *testing.T) (root, head string) {
	t.Helper()
	root = t.TempDir()
	eolGit(t, root, "init", "-q")
	rels := []string{"app.go", "README.md"}
	for i, body := range []string{"package app\n", "# app\n\nnotes\n"} {
		if err := os.WriteFile(filepath.Join(root, rels[i]), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	eolGit(t, root, "add", "-A")
	eolGit(t, root, "commit", "-q", "-m", "head")
	head = eolGit(t, root, "rev-parse", "HEAD")
	autocrlf(t)
	for _, rel := range rels {
		if err := os.Remove(filepath.Join(root, rel)); err != nil {
			t.Fatal(err)
		}
	}
	eolGit(t, root, "checkout", "--", ".")
	raw, err := os.ReadFile(filepath.Join(root, "README.md"))
	if !bytes.Contains(raw, []byte("\r\n")) {
		t.Fatalf("README.md = %q (%v): the fixture is no CRLF checkout", raw, err)
	}
	return root, head
}

// autocrlf sets core.autocrlf=true at command scope for the test.
func autocrlf(t *testing.T) {
	t.Helper()
	n, err := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_KEY_"+strconv.Itoa(n), "core.autocrlf")
	t.Setenv("GIT_CONFIG_VALUE_"+strconv.Itoa(n), "true")
	t.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(n+1))
}

func eolGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = root
	cmd.WaitDelay = 10 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return string(bytes.TrimSpace(out))
}
