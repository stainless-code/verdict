package fixcore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// readOnlyCommander answers every spawn empty and records the argv, so a test
// can pin the exact git calls fix makes on a PR bundle. rev-parse answers head.
type readOnlyCommander struct {
	head  string
	calls [][]string
}

func (c *readOnlyCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.calls = append(c.calls, slices.Clone(cmd.Argv))
	if slices.Contains(cmd.Argv, "rev-parse") {
		return core.Result{Stdout: c.head + "\n"}, nil
	}
	return core.Result{}, nil
}

type recordingProgress struct {
	lines []string
}

func (p *recordingProgress) Human(line string) { p.lines = append(p.lines, line) }
func (p *recordingProgress) Warn(string)       {}
func (p *recordingProgress) Diag(string)       {}

const (
	prHead    = "1111111111111111111111111111111111111111"
	otherHead = "2222222222222222222222222222222222222222"
	prRecord  = `{"state":"OPEN","headRefOid":"` + prHead + `"}`
)

const prBundleFindings = `[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
	`"anchored":true,"body":"PR finding.","suggestion":null,"inBounds":true,` +
	`"rigor":"pointed","severity":"informational","kb":[],` +
	`"sig":"1123456789abcdef0123456789abcdef01234567","sig_version":"1",` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]`

// prBundleRequest builds a PR-bundle fix invocation over root with no real git
// behind it: the commander answers every call empty, like a clean worktree.
func prBundleRequest(root string, cmd *readOnlyCommander, prog *recordingProgress, human *strings.Builder) *Request {
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Now:       func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
			Env:       map[string]string{},
			Progress:  prog,
			Commander: cmd,
		},
		Repo:     "acme/app",
		Pr:       "4291",
		RepoRoot: root,
	}
	return &Request{ReadRequest: read, Human: human}
}

// seedPRBundle writes the findings.json a PR-bundle fix pass reads and the
// PR.json that names the head the bundle reviewed.
func seedPRBundle(t *testing.T, root string) {
	t.Helper()
	bdir := filepath.Join(root, ".verdict", "state", "4291")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "findings.json"), []byte(prBundleFindings), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "PR.json"), []byte(prRecord), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFixPRBundlePass pins that a PR bundle runs a default fix pass without
// --selected, and that the instructions name post, not record.
func TestFixPRBundlePass(t *testing.T) {
	root := t.TempDir()
	seedPRBundle(t, root)
	cmd := &readOnlyCommander{head: prHead}
	prog := &recordingProgress{}
	human := &strings.Builder{}
	resp, ref := Run(t.Context(), prBundleRequest(root, cmd, prog, human))
	if ref != nil {
		t.Fatalf("fix on PR bundle refused: %+v", ref)
	}
	if resp.Data == nil || resp.Data.Pass == nil || *resp.Data.Pass != 1 {
		t.Fatalf("pass = %+v, want pass 1", resp.Data)
	}
	if !strings.Contains(human.String(), "land it with post acme/app 4291") {
		t.Errorf("human output lacks the post landing:\n%s", human.String())
	}
	if !strings.Contains(resp.Next, "run post to land it") {
		t.Errorf("next = %q, want the post landing", resp.Next)
	}
}

// TestFixPRBundleNoWritesOutsideCheckout proves the side-effect boundary: on a
// PR bundle fix spawns only read-only git commands and writes only inside its
// own bundle directory.
func TestFixPRBundleNoWritesOutsideCheckout(t *testing.T) {
	root := t.TempDir()
	seedPRBundle(t, root)
	findingsBefore := readBundleFindings(t, root)
	cmd := &readOnlyCommander{head: prHead}
	prog := &recordingProgress{}
	human := &strings.Builder{}
	if _, ref := Run(t.Context(), prBundleRequest(root, cmd, prog, human)); ref != nil {
		t.Fatalf("fix on PR bundle refused: %+v", ref)
	}
	requireReadOnlyGitCalls(t, cmd.calls, root)
	requireOnlyBundleFiles(t, root)
	requireFindingsUnchanged(t, root, findingsBefore)
}

// requireReadOnlyGitCalls pins the exact git argv fix may issue on a PR
// bundle: reads and a diff only, never push, post, or a host CLI.
func requireReadOnlyGitCalls(t *testing.T, calls [][]string, root string) {
	t.Helper()
	want := [][]string{
		{"git", "ls-files", "-u"},
		{"git", "-C", root, "rev-parse", "HEAD"},
		{"git", "diff", "HEAD", "--no-color", "--no-ext-diff"},
		{"git", "status", "--porcelain", "-z"},
	}
	if len(calls) != len(want) {
		t.Fatalf("git calls = %v, want %v", calls, want)
	}
	for i := range want {
		if !slices.Equal(calls[i], want[i]) {
			t.Errorf("call %d = %v, want %v", i, calls[i], want[i])
		}
	}
}

// requireOnlyBundleFiles walks root and requires the only regular files are
// the seeded PR record and findings and the fix ledger beside them.
func requireOnlyBundleFiles(t *testing.T, root string) {
	t.Helper()
	var written []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		written = append(written, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		".verdict/state/4291/PR.json",
		".verdict/state/4291/findings.json",
		".verdict/state/4291/fix.json",
	}
	if !slices.Equal(written, want) {
		t.Errorf("written files = %v, want %v", written, want)
	}
}

func readBundleFindings(t *testing.T, root string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".verdict", "state", "4291", "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func requireFindingsUnchanged(t *testing.T, root string, before []byte) {
	t.Helper()
	if !bytes.Equal(before, readBundleFindings(t, root)) {
		t.Error("findings.json changed")
	}
}

// TestFixPRBundleHeadMismatch pins the checkout binding: on a PR bundle every
// mode refuses head-mismatch when the checked-out head is not the reviewed
// head, before it writes a ledger, a patch, or a test, and before any git
// call that could change the checkout.
func TestFixPRBundleHeadMismatch(t *testing.T) {
	modes := []struct {
		set  func(*Request)
		name string
	}{
		{func(*Request) {}, "default"},
		{func(r *Request) { r.Sig = "1123456789" }, "sig"},
		{func(r *Request) { r.All = true; r.VerifyCmd = "go test ./..." }, "all"},
		{func(r *Request) { r.Selected = true; r.VerifyCmd = "go test ./..." }, "selected"},
		{func(r *Request) { r.Recipe = "security"; r.RecipeSet = true; r.VerifyCmd = "go test ./..." }, "recipe"},
		{func(r *Request) { r.Test = "1123456789"; r.TestSet = true }, "test"},
		{func(r *Request) { r.Test = "1123456789"; r.TestSet = true; r.Commit = true }, "test-commit"},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			root := t.TempDir()
			seedPRBundle(t, root)
			cmd := &readOnlyCommander{head: otherHead}
			req := prBundleRequest(root, cmd, &recordingProgress{}, &strings.Builder{})
			m.set(req)
			_, ref := Run(t.Context(), req)
			requireHeadMismatch(t, ref)
			requireNothingWritten(t, root, cmd.calls)
		})
	}
}

// requireHeadMismatch pins the refusal code and that its message names both
// commits and the fix.
func requireHeadMismatch(t *testing.T, ref *core.RefusalError) {
	t.Helper()
	if ref == nil || ref.Code != "head-mismatch" {
		t.Fatalf("refusal = %+v, want head-mismatch", ref)
	}
	for _, want := range []string{"2222222", "1111111", "check out the PR head"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("message %q lacks %q", ref.Message, want)
		}
	}
}

// requireNothingWritten proves the refusal came before any ledger write or
// any git call that could change the checkout.
func requireNothingWritten(t *testing.T, root string, calls [][]string) {
	t.Helper()
	for _, call := range calls {
		if slices.Contains(call, "add") || slices.Contains(call, "commit") || slices.Contains(call, "diff") {
			t.Errorf("git call %v ran after the head mismatch", call)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".verdict", "state", "4291", "fix.json")); err == nil {
		t.Error("fix.json written despite the head mismatch")
	}
}

// TestFixPRBundleNoReviewedHead refuses a PR bundle that names no head: the
// binding cannot be proved, so fix does not guess.
func TestFixPRBundleNoReviewedHead(t *testing.T) {
	root := t.TempDir()
	seedPRBundle(t, root)
	if err := os.WriteFile(filepath.Join(root, ".verdict", "state", "4291", "PR.json"),
		[]byte(`{"state":"OPEN"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := &readOnlyCommander{head: prHead}
	_, ref := Run(t.Context(), prBundleRequest(root, cmd, &recordingProgress{}, &strings.Builder{}))
	if ref == nil || ref.Code != "head-mismatch" || !strings.Contains(ref.Message, "carries no reviewed head") {
		t.Fatalf("refusal = %+v, want head-mismatch naming the missing head", ref)
	}
}

// TestFixLocalBundleSkipsHeadBinding records the choice: a local bundle reviews
// the working tree and the loop may commit between passes, so a moved HEAD
// does not refuse it.
func TestFixLocalBundleSkipsHeadBinding(t *testing.T) {
	root := t.TempDir()
	bdir := filepath.Join(root, ".verdict", "state", "local-x")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "findings.json"), []byte(prBundleFindings), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "source.json"),
		[]byte(`{"head_sha":"`+prHead+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := &readOnlyCommander{head: otherHead}
	req := prBundleRequest(root, cmd, &recordingProgress{}, &strings.Builder{})
	req.Pr = "local-x"
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatalf("local bundle refused on a moved head: %+v", ref)
	}
}
