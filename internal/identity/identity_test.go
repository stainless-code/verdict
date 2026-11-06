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
// `git patch-id --stable` argv with the raw diff on stdin, kind git-patch-id,
// and the head/base SHAs rode through.
func TestBuildGitPrimary(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{stdout: "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d 0000000000000000000000000000000000000000\n"}
	id := Build(context.Background(), cmdr, []byte(testDiff), []byte(testDiff), "aaaa", "bbbb")
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

// assertIdentitySHAs proves both sha fields hash their inputs and the
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

// TestBuildSplitsFilteredAndRaw proves the two hashes stay independent:
// full.diff feeds DiffSHA256 while the patch identity tracks
// full.raw.diff, so a future exclusion pass cannot silently collapse them.
func TestBuildSplitsFilteredAndRaw(t *testing.T) {
	t.Parallel()
	filtered := []byte("filtered\n")
	raw := []byte(testDiff)
	id := Build(context.Background(), &scriptCommander{fail: true}, filtered, raw, "", "")
	if id.DiffSHA256 == id.RawDiffSHA256 {
		t.Error("filtered and raw bytes share one hash")
	}
	unchanged := Build(context.Background(), &scriptCommander{fail: true}, []byte("other filtered\n"), raw, "", "")
	if !Match(id, unchanged) {
		t.Error("refiltering the diff changed the patch id")
	}
	changed := Build(context.Background(), &scriptCommander{fail: true}, filtered, []byte("other raw\n"), "", "")
	if Match(id, changed) {
		t.Error("raw change kept the patch id")
	}
}

// TestBuildFallback proves a failed spawn falls back to the labeled local
// hash, and that the fallback never reads as a git id.
func TestBuildFallback(t *testing.T) {
	t.Parallel()
	id := Build(context.Background(), &scriptCommander{fail: true}, []byte(testDiff), []byte(testDiff), "aaaa", "bbbb")
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

// TestBuildEmptyDiff proves an empty raw diff carries a null patch id of
// kind none, which no guard matches.
func TestBuildEmptyDiff(t *testing.T) {
	t.Parallel()
	cmdr := &scriptCommander{}
	for _, diff := range []string{"", "  \n\t\n"} {
		id := Build(context.Background(), cmdr, []byte(diff), []byte(diff), "", "")
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
	if Match(
		Build(context.Background(), cmdr, []byte(""), []byte(""), "", ""),
		Build(context.Background(), cmdr, []byte(""), []byte(""), "", ""),
	) {
		t.Error("empty identities match")
	}
}

// TestFallbackIgnoresIndexLines proves a diff whose index lines change but
// whose hunks do not keeps the fallback id.
func TestFallbackIgnoresIndexLines(t *testing.T) {
	t.Parallel()
	other := strings.Replace(testDiff, "index 1111111..2222222 100644", "index 3333333..4444444 100644", 1)
	a := Build(context.Background(), &scriptCommander{fail: true}, []byte(testDiff), []byte(testDiff), "", "")
	b := Build(context.Background(), &scriptCommander{fail: true}, []byte(other), []byte(other), "", "")
	if !Match(a, b) {
		t.Error("index-only rewrite changed the fallback id")
	}
	changed := strings.Replace(testDiff, "-old line", "-other line", 1)
	c := Build(context.Background(), &scriptCommander{fail: true}, []byte(changed), []byte(changed), "", "")
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
	id := Build(context.Background(), &scriptCommander{stdout: patch + " 0\n"}, []byte(testDiff), []byte(testDiff), "aaaa", "bbbb")
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
		"garbage":     "not json{{{",
		"nonobject":   `[]`,
		"noversion":   `{"patch_id":"9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d","patch_id_kind":"git-patch-id","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"badversion":  `{"schema_version":"2","patch_id":"9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d","patch_id_kind":"git-patch-id","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"badkind":     `{"schema_version":"1","patch_id":"abc","patch_id_kind":"md5","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"badpatch":    `{"schema_version":"1","patch_id":42,"patch_id_kind":"git-patch-id","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"bogusid":     `{"schema_version":"1","patch_id":"not-a-patch-id","patch_id_kind":"git-patch-id","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"shortid":     `{"schema_version":"1","patch_id":"abc123","patch_id_kind":"git-patch-id","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"upperid":     `{"schema_version":"1","patch_id":"9F3C2A1B7D9F3C2A1B7D9F3C2A1B7D9F3C2A1B7D","patch_id_kind":"git-patch-id","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"shortsha":    `{"schema_version":"1","patch_id":"9f3c2a1b7d9f3c","patch_id_kind":"diff-sha256","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"nullgit":     `{"schema_version":"1","patch_id":null,"patch_id_kind":"git-patch-id","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"nonnullnone": `{"schema_version":"1","patch_id":"9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d","patch_id_kind":"none","diff_sha256":"d","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"nosha":       `{"schema_version":"1","patch_id":"9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d","patch_id_kind":"git-patch-id","raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
		"numsha":      `{"schema_version":"1","patch_id":"9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d","patch_id_kind":"git-patch-id","diff_sha256":42,"raw_diff_sha256":"r","head_sha":"h","base_sha":"b"}`,
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

// TestValidPatchID pins the algorithm-specific shapes: 40 lowercase hex for
// git-patch-id, 64 lowercase hex for diff-sha256, empty for none, and
// nothing else.
func TestValidPatchID(t *testing.T) {
	t.Parallel()
	git := "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"
	fallback := "94d2a258c8308641db7b0ada6e81eda8fae398466cb7652b30a837ba95076caa"
	for _, tc := range []struct {
		kind, patch string
		want        bool
	}{
		{KindGit, git, true},
		{KindFallback, fallback, true},
		{KindNone, "", true},
		{KindGit, "not-a-patch-id", false},
		{KindGit, "abc123", false},
		{KindGit, "9F3C2A1B7D9F3C2A1B7D9F3C2A1B7D9F3C2A1B7D", false},
		{KindGit, fallback, false},
		{KindFallback, git, false},
		{KindFallback, "xyz", false},
		{KindNone, git, false},
		{KindGit, "", false},
		{"md5", "abc", false},
		{"", git, false},
	} {
		if got := ValidPatchID(tc.kind, tc.patch); got != tc.want {
			t.Errorf("ValidPatchID(%q, %q) = %v, want %v", tc.kind, tc.patch, got, tc.want)
		}
	}
}

// verifyBundle writes a coherent bundle — full.diff bytes plus a matching
// identity — and returns the diff bytes.
func verifyBundle(t *testing.T, diff, head, base string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	id := Build(context.Background(), &scriptCommander{fail: true}, []byte(diff), []byte(diff), head, base)
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), []byte(jq.PrettyOrdered(id.Wire())), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestVerifyBindsBytes proves a coherent identity verifies, while stale
// diff bytes, a moved head, or a missing diff read false.
func TestVerifyBindsBytes(t *testing.T) {
	t.Parallel()
	dir := verifyBundle(t, testDiff, "aaaa", "bbbb")
	if _, ok := Verify(dir, "aaaa", "bbbb"); !ok {
		t.Error("Verify of a coherent bundle = false, want true")
	}
	if _, ok := Verify(dir, "cccc", "bbbb"); ok {
		t.Error("Verify with a moved head = true, want false")
	}
	if _, ok := Verify(dir, "aaaa", "dddd"); ok {
		t.Error("Verify with a moved base = true, want false")
	}
	assertVerifyRejectsStaleBytes(t, dir)
	if _, ok := Verify(t.TempDir(), "aaaa", "bbbb"); ok {
		t.Error("Verify with no bundle = true, want false")
	}
}

// assertVerifyRejectsStaleBytes proves an identity copied beside replaced
// bytes no longer verifies.
func assertVerifyRejectsStaleBytes(t *testing.T, dir string) {
	t.Helper()
	stale := t.TempDir()
	raw, err := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "identity.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "full.diff"), []byte("other bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Verify(stale, "aaaa", "bbbb"); ok {
		t.Error("Verify with stale diff bytes = true, want false")
	}
}

// TestCompletedPatch pins the completed-publication contract: a full
// receipt with a valid patch proves publication, while two-field,
// incomplete, pending, dry-run, failed, and invalid-id receipts do not.
func TestCompletedPatch(t *testing.T) {
	t.Parallel()
	git := "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"
	complete := map[string]any{
		"schema_version": "1",
		"review":         map[string]any{"id": float64(1), "state": "COMMENTED"},
		"head":           "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"posted_at":      "2026-01-01T00:00:00Z",
		"patch_id":       git,
		"patch_id_kind":  "git-patch-id",
	}
	if _, _, ok := CompletedPatch(complete); !ok {
		t.Error("CompletedPatch of a completed receipt = false, want true")
	}
	stub := map[string]any{"patch_id": git, "patch_id_kind": "git-patch-id"}
	incomplete := map[string]any{"schema_version": "1", "head": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "patch_id": git, "patch_id_kind": "git-patch-id"}
	pending := map[string]any{
		"schema_version": "1", "review": map[string]any{"state": "PENDING"},
		"head": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "posted_at": "2026-01-01T00:00:00Z",
		"patch_id": git, "patch_id_kind": "git-patch-id",
	}
	dry := map[string]any{
		"schema_version": "1", "review": map[string]any{"state": "COMMENTED"}, "dry_run": true,
		"head": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "posted_at": "2026-01-01T00:00:00Z",
		"patch_id": git, "patch_id_kind": "git-patch-id",
	}
	bogus := map[string]any{
		"schema_version": "1", "review": map[string]any{"id": float64(1)},
		"head": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "posted_at": "2026-01-01T00:00:00Z",
		"patch_id": "not-a-patch-id", "patch_id_kind": "git-patch-id",
	}
	for name, receipt := range map[string]map[string]any{
		"nil": nil, "stub": stub, "incomplete": incomplete, "pending": pending, "dryrun": dry, "bogus": bogus,
	} {
		if _, _, ok := CompletedPatch(receipt); ok {
			t.Errorf("CompletedPatch(%s) = true, want false", name)
		}
	}
}
