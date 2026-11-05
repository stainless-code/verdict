package identity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

type scriptCommander struct {
	stdout string
	fail   bool
	calls  int
}

func (c *scriptCommander) Run(_ context.Context, cmd core.Command) (core.Result, error) {
	c.calls++
	if len(cmd.Argv) != 3 || cmd.Argv[0] != "git" || cmd.Argv[1] != "patch-id" || cmd.Argv[2] != "--stable" {
		return core.Result{}, errors.New("unexpected argv")
	}
	if cmd.Stdin == nil {
		return core.Result{}, errors.New("patch-id needs the diff on stdin")
	}
	if c.fail {
		return core.Result{ExitCode: 1}, nil
	}
	return core.Result{Stdout: c.stdout}, nil
}

const testDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// TestBuildGitPrimary proves the commander path wins: the exact
// `git patch-id --stable` argv with the diff on stdin, kind git-patch-id,
// and both sha fields over the raw diff.
func TestBuildGitPrimary(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{stdout: "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d 0000000000000000000000000000000000000000\n"}
	id := Build(context.Background(), cmdr, []byte(testDiff), "aaaa", "bbbb")
	assertPrimaryIdentity(t, cmdr, id)
}

// assertPrimaryIdentity proves one git-built identity: one spawn, the
// parsed id and kind, equal non-empty sha fields, and the head/base SHAs.
func assertPrimaryIdentity(t *testing.T, cmdr *scriptCommander, id *Identity) {
	t.Helper()
	if cmdr.calls != 1 {
		t.Fatalf("patch-id spawns = %d, want 1", cmdr.calls)
	}
	if id.PatchID == nil || *id.PatchID != "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d" {
		t.Errorf("patch_id = %v", id.PatchID)
	}
	if id.PatchIDKind != KindGit {
		t.Errorf("kind = %q, want %q", id.PatchIDKind, KindGit)
	}
	assertIdentitySHAs(t, id)
}

// assertIdentitySHAs proves both sha fields hash the raw diff and the
// head/base SHAs rode through.
func assertIdentitySHAs(t *testing.T, id *Identity) {
	t.Helper()
	if id.DiffSHA256 == "" || id.DiffSHA256 != id.RawDiffSHA256 {
		t.Errorf("sha fields = %q/%q, want equal non-empty", id.DiffSHA256, id.RawDiffSHA256)
	}
	if id.HeadSHA != "aaaa" || id.BaseSHA != "bbbb" {
		t.Errorf("heads = %q/%q", id.HeadSHA, id.BaseSHA)
	}
}

// TestBuildFallback proves a failed spawn falls back to the labeled local
// hash, and that the fallback never reads as a git id.
func TestBuildFallback(t *testing.T) {
	t.Parallel()
	id := Build(context.Background(), &scriptCommander{fail: true}, []byte(testDiff), "aaaa", "bbbb")
	if id.PatchID == nil || *id.PatchID == "" {
		t.Fatal("fallback patch_id is empty")
	}
	if id.PatchIDKind != KindFallback {
		t.Errorf("kind = %q, want %q", id.PatchIDKind, KindFallback)
	}
	git := &Identity{PatchID: id.PatchID, PatchIDKind: KindGit}
	if Match(id, git) {
		t.Error("fallback id matches a git-patch-id value across kinds")
	}
	none := &Identity{PatchID: nil, PatchIDKind: KindNone}
	if Match(none, none) {
		t.Error("none identity matches")
	}
}

// TestBuildEmptyDiff proves an empty diff carries a null patch id of kind
// none, which no guard matches.
func TestBuildEmptyDiff(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{}
	for _, diff := range []string{"", "  \n\t\n"} {
		id := Build(context.Background(), cmdr, []byte(diff), "", "")
		if id.PatchID != nil {
			t.Errorf("empty diff patch_id = %q, want null", *id.PatchID)
		}
		if id.PatchIDKind != KindNone {
			t.Errorf("empty diff kind = %q, want none", id.PatchIDKind)
		}
	}
	if cmdr.calls != 0 {
		t.Errorf("empty diff spawns = %d, want 0", cmdr.calls)
	}
	if Match(Build(context.Background(), cmdr, []byte(""), "", ""), Build(context.Background(), cmdr, []byte(""), "", "")) {
		t.Error("empty identities match")
	}
}

// TestFallbackIgnoresIndexLines proves a diff whose index lines change but
// whose hunks do not keeps the fallback id.
func TestFallbackIgnoresIndexLines(t *testing.T) {
	t.Parallel()
	other := strings.Replace(testDiff, "index 1111111..2222222 100644", "index 3333333..4444444 100644", 1)
	a := Build(context.Background(), &scriptCommander{fail: true}, []byte(testDiff), "", "")
	b := Build(context.Background(), &scriptCommander{fail: true}, []byte(other), "", "")
	if !Match(a, b) {
		t.Error("index-only rewrite changed the fallback id")
	}
	changed := strings.Replace(testDiff, "-old line", "-other line", 1)
	c := Build(context.Background(), &scriptCommander{fail: true}, []byte(changed), "", "")
	if Match(a, c) {
		t.Error("content change kept the fallback id")
	}
}

// TestParsePatchID pins the first-field read against patch-id output shapes.
func TestParsePatchID(t *testing.T) {
	t.Parallel()
	if got := ParsePatchID("abc123 0000000000000000000000000000000000000000\n"); got != "abc123" {
		t.Errorf("ParsePatchID = %q", got)
	}
	if got := ParsePatchID(""); got != "" {
		t.Errorf("empty ParsePatchID = %q", got)
	}
}

// TestLoadRoundTrip proves the written artifact reloads, and that missing,
// undecodable, and malformed identities read false (warn-and-proceed).
func TestLoadRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	patch := "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"
	id := Build(context.Background(), &scriptCommander{stdout: patch + " 0\n"}, []byte(testDiff), "aaaa", "bbbb")
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), []byte(jq.PrettyOrdered(id.Wire())), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, ok := Load(dir)
	if !ok {
		t.Fatal("Load of written identity failed")
	}
	if !Match(id, loaded) {
		t.Error("reloaded identity does not match")
	}
	if _, ok := Load(t.TempDir()); ok {
		t.Error("Load of a missing identity = true, want false")
	}
	assertLoadRejects(t)
}

// assertLoadRejects proves missing, undecodable, and malformed identities
// read false, so the caller warns and proceeds.
func assertLoadRejects(t *testing.T) {
	t.Helper()
	for name, content := range map[string]string{
		"garbage":   "not json{{{",
		"nonobject": `[]`,
		"badkind":   `{"patch_id":"abc","patch_id_kind":"md5","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"badpatch":  `{"patch_id":42,"patch_id_kind":"git-patch-id","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
	} {
		sub := t.TempDir()
		if err := os.WriteFile(filepath.Join(sub, "identity.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := Load(sub); ok {
			t.Errorf("Load(%s) = true, want false", name)
		}
	}
}
