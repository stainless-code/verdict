package learncore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/kb"
)

func seedProposal(t *testing.T) (string, string) {
	t.Helper()
	root := learningFixture(t)
	if _, ref := Run(t.Context(), proposeRequest(root, false)); ref != nil {
		t.Fatal(ref)
	}
	dirs, err := os.ReadDir(filepath.Join(root, learningsDir))
	if err != nil {
		t.Fatal(err)
	}
	return root, dirs[0].Name()
}

func TestAcceptLearning(t *testing.T) {
	root, id := seedProposal(t)
	req := &Request{Accept: id, DryRun: true, JSONMode: true}
	req.RepoRoot = root
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if _, err := os.Stat(filepath.Join(root, kb.File)); !os.IsNotExist(err) {
		t.Fatalf("accept preview wrote KB: %v", err)
	}
	req = &Request{Accept: id, JSONMode: true}
	req.RepoRoot = root
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	raw, err := os.ReadFile(filepath.Join(root, kb.File))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"provenance":"threads#71"`, `"source":"learning:`, `"scope":"src/main.go"`, `"status":"active"`, `"hits":0`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s in %s", want, raw)
		}
	}
	resp, ref := Run(t.Context(), proposeRequest(root, false))
	if ref != nil {
		t.Fatal(ref)
	}
	if !strings.Contains(resp.Data.Wire().MarshalCompact(), "skipped-kb") {
		t.Fatalf("accepted candidate reproposed: %s", resp.Data.Wire().MarshalCompact())
	}
}

func TestRejectLearning(t *testing.T) {
	root, id := seedProposal(t)
	req := &Request{Reject: id, Reason: "short", JSONMode: true}
	req.RepoRoot = root
	if _, ref := Run(t.Context(), req); ref == nil || ref.Code != "bad-reason" {
		t.Fatalf("short reason: %v", ref)
	}
	req.Reason = "This concern does not apply to our current implementation."
	req.DryRun = true
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if _, err := os.Stat(filepath.Join(root, rejectionDir)); !os.IsNotExist(err) {
		t.Fatalf("reject preview wrote tombstone: %v", err)
	}
	req.DryRun = false
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	if _, err := os.Stat(filepath.Join(root, learningsDir, id)); !os.IsNotExist(err) {
		t.Fatalf("proposal not removed: %v", err)
	}
	resp, ref := Run(t.Context(), proposeRequest(root, false))
	if ref != nil {
		t.Fatal(ref)
	}
	if !strings.Contains(resp.Data.Wire().MarshalCompact(), "skipped-rejected") {
		t.Fatalf("rejected candidate reproposed: %s", resp.Data.Wire().MarshalCompact())
	}
	req = &Request{Accept: id, JSONMode: true}
	req.RepoRoot = root
	if _, ref := Run(t.Context(), req); ref == nil || ref.Code != "bad-rule" {
		t.Fatalf("accepted rejection: %v", ref)
	}
	if _, err := os.Stat(filepath.Join(root, kb.File)); !os.IsNotExist(err) {
		t.Fatalf("rejected rule reached KB: %v", err)
	}
}
