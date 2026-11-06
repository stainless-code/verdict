package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
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
	if _, ok := Verify(context.Background(), nil, dir, "aaaa", "bbbb"); !ok {
		t.Error("Verify of a coherent bundle = false, want true")
	}
	if _, ok := Verify(context.Background(), nil, dir, "cccc", "bbbb"); ok {
		t.Error("Verify with a moved head = true, want false")
	}
	if _, ok := Verify(context.Background(), nil, dir, "aaaa", "dddd"); ok {
		t.Error("Verify with a moved base = true, want false")
	}
	assertVerifyRejectsStaleBytes(t, dir)
	if _, ok := Verify(context.Background(), nil, t.TempDir(), "aaaa", "bbbb"); ok {
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
	if _, ok := Verify(context.Background(), nil, stale, "aaaa", "bbbb"); ok {
		t.Error("Verify with stale diff bytes = true, want false")
	}
}

// TestCompletedPatch pins the completed-publication contract: a full
// receipt with a valid patch proves publication, while two-field,
// incomplete, pending, dry-run, failed, and invalid-id receipts do not.
// completedReceipt builds a valid completed-publication receipt with the
// given review object, head, and posted_at.
func completedReceipt(review map[string]any, head, postedAt string) map[string]any {
	return map[string]any{
		"schema_version": "1",
		"review":         review,
		"head":           head,
		"posted_at":      postedAt,
		"patch_id":       "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d",
		"patch_id_kind":  "git-patch-id",
	}
}

const completedHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

const completedAt = "2026-01-01T00:00:00Z"

// TestCompletedPatchValidControls proves legitimate completed receipts
// prove publication: the recorded completed states, numeric and string
// review identifiers, and producer-shaped head/time metadata.
func TestCompletedPatchValidControls(t *testing.T) {
	t.Parallel()
	valid := map[string]map[string]any{
		"commented": completedReceipt(map[string]any{"id": float64(1), "state": "COMMENTED"}, completedHead, completedAt),
		"lowercase": completedReceipt(map[string]any{"id": float64(101), "state": "commented"}, completedHead, completedAt),
		"approved":  completedReceipt(map[string]any{"id": float64(2), "state": "APPROVED"}, completedHead, completedAt),
		"changes":   completedReceipt(map[string]any{"id": float64(3), "state": "CHANGES_REQUESTED"}, completedHead, completedAt),
		"dismissed": completedReceipt(map[string]any{"id": float64(4), "state": "DISMISSED"}, completedHead, completedAt),
		"string-id": completedReceipt(map[string]any{"id": "abc-123", "state": "COMMENTED"}, completedHead, completedAt),
		"json-num":  completedReceipt(map[string]any{"id": json.Number("101"), "state": "COMMENTED"}, completedHead, completedAt),
		"offset-tz": completedReceipt(map[string]any{"id": float64(5), "state": "COMMENTED"}, completedHead, "2026-01-01T02:00:00+02:00"),
	}
	for name, receipt := range valid {
		if _, _, ok := CompletedPatch(receipt); !ok {
			t.Errorf("CompletedPatch(%s) = false, want true", name)
		}
	}
}

// TestCompletedPatchInvalidMatrix proves fabricated, partial, and malformed
// receipts prove no publication: empty reviews, missing or unknown review
// identifiers and states, non-head/non-time metadata, and the previously
// covered stub/pending/dry-run/invalid-id shapes.
func TestCompletedPatchInvalidMatrix(t *testing.T) {
	t.Parallel()
	git := "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"
	stub := map[string]any{"patch_id": git, "patch_id_kind": "git-patch-id"}
	incomplete := map[string]any{"schema_version": "1", "head": completedHead, "patch_id": git, "patch_id_kind": "git-patch-id"}
	pending := map[string]any{
		"schema_version": "1", "review": map[string]any{"id": float64(1), "state": "PENDING"},
		"head": completedHead, "posted_at": completedAt,
		"patch_id": git, "patch_id_kind": "git-patch-id",
	}
	dry := map[string]any{
		"schema_version": "1", "review": map[string]any{"id": float64(1), "state": "COMMENTED"}, "dry_run": true,
		"head": completedHead, "posted_at": completedAt,
		"patch_id": git, "patch_id_kind": "git-patch-id",
	}
	bogus := map[string]any{
		"schema_version": "1", "review": map[string]any{"id": float64(1), "state": "COMMENTED"},
		"head": completedHead, "posted_at": completedAt,
		"patch_id": "not-a-patch-id", "patch_id_kind": "git-patch-id",
	}
	invalid := map[string]map[string]any{
		"nil":           nil,
		"stub":          stub,
		"incomplete":    incomplete,
		"pending":       pending,
		"dryrun":        dry,
		"bogus":         bogus,
		"empty-review":  completedReceipt(map[string]any{}, completedHead, completedAt),
		"missing-id":    completedReceipt(map[string]any{"state": "COMMENTED"}, completedHead, completedAt),
		"null-id":       completedReceipt(map[string]any{"id": nil, "state": "COMMENTED"}, completedHead, completedAt),
		"missing-state": completedReceipt(map[string]any{"id": float64(1)}, completedHead, completedAt),
		"unknown-state": completedReceipt(map[string]any{"id": float64(1), "state": "QUEUED"}, completedHead, completedAt),
		"failed-state":  completedReceipt(map[string]any{"id": float64(1), "state": "FAILED"}, completedHead, completedAt),
		"bad-head":      completedReceipt(map[string]any{"id": float64(1), "state": "COMMENTED"}, "not-a-head", completedAt),
		"short-head":    completedReceipt(map[string]any{"id": float64(1), "state": "COMMENTED"}, "aaaaaaa", completedAt),
		"bad-time":      completedReceipt(map[string]any{"id": float64(1), "state": "COMMENTED"}, completedHead, "not-a-time"),
	}
	for name, receipt := range invalid {
		if _, _, ok := CompletedPatch(receipt); ok {
			t.Errorf("CompletedPatch(%s) = true, want false", name)
		}
	}
}

// gitTestPatch is the canned stable id the map commander returns for
// testDiff.
const gitTestPatch = "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"

// mapCommander answers `git patch-id --stable` from a canned diff-to-id
// map, failing anything else exactly like an unavailable git: unknown input
// cannot be recomputed, so it stays unverified.
type mapCommander struct {
	ids map[string]string
}

func (c *mapCommander) Run(_ context.Context, cmd core.Command) (core.Result, error) {
	if len(cmd.Argv) == 3 && cmd.Argv[0] == "git" && cmd.Stdin != nil {
		raw, err := io.ReadAll(cmd.Stdin)
		if err == nil {
			if id, ok := c.ids[string(raw)]; ok {
				return core.Result{Stdout: id + " 0000000000000000000000000000000000000000\n"}, nil
			}
		}
	}
	return core.Result{ExitCode: 1}, errors.New("cannot compute patch id")
}

// gitBundle writes a coherent git-kind bundle — real byte hashes with the
// given patch claim — and returns the dir.
func gitBundle(t *testing.T, diff, head, base, patch string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256Of(diff)
	ident := `{"schema_version":"1","patch_id":"` + patch + `","patch_id_kind":"git-patch-id",` +
		`"diff_sha256":"` + sum + `","raw_diff_sha256":"` + sum + `","head_sha":"` + head + `","base_sha":"` + base + `"}`
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), []byte(ident), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func sha256Of(diff string) string {
	sum := sha256.Sum256([]byte(diff))
	return hex.EncodeToString(sum[:])
}

// TestVerifyRecomputesGitClaim proves a hex-valid id over the wrong bytes
// does not verify even with coherent hashes and head: the claim must
// reproduce `git patch-id --stable` over the raw input. Without a runner
// the claim cannot be recomputed and stays unverified.
func TestVerifyRecomputesGitClaim(t *testing.T) {
	t.Parallel()
	cmdr := &mapCommander{ids: map[string]string{testDiff: gitTestPatch}}
	dir := gitBundle(t, testDiff, "aaaa", "bbbb", gitTestPatch)
	if _, ok := Verify(context.Background(), cmdr, dir, "aaaa", "bbbb"); !ok {
		t.Error("Verify of a recomputed claim = false, want true")
	}
	tampered := t.TempDir()
	raw, err := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tampered, "identity.json"),
		bytesReplace(t, raw, gitTestPatch, strings.Repeat("1", 40)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tampered, "full.diff"), []byte(testDiff), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Verify(context.Background(), cmdr, tampered, "aaaa", "bbbb"); ok {
		t.Error("Verify of a tampered valid-hex claim = true, want false")
	}
	fresh := gitBundle(t, testDiff, "aaaa", "bbbb", gitTestPatch)
	if _, ok := Verify(context.Background(), nil, fresh, "aaaa", "bbbb"); ok {
		t.Error("Verify without a runner = true, want false")
	}
}

// TestVerifyFallbackRecompute proves the fallback claim recomputes locally
// without a spawn: a coherent fallback verifies, a swapped valid-length
// hash does not.
func TestVerifyFallbackRecompute(t *testing.T) {
	t.Parallel()
	dir := verifyBundle(t, testDiff, "aaaa", "bbbb")
	if _, ok := Verify(context.Background(), nil, dir, "aaaa", "bbbb"); !ok {
		t.Error("Verify of a coherent fallback = false, want true")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	swapped := t.TempDir()
	if err := os.WriteFile(filepath.Join(swapped, "identity.json"),
		bytesReplace(t, raw, fallbackHexOf(testDiff), strings.Repeat("2", 64)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(swapped, "full.diff"), []byte(testDiff), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Verify(context.Background(), nil, swapped, "aaaa", "bbbb"); ok {
		t.Error("Verify of a swapped fallback claim = true, want false")
	}
}

func fallbackHexOf(diff string) string {
	return fallbackHex([]byte(diff))
}

func bytesReplace(t *testing.T, raw []byte, old, replacement string) []byte {
	t.Helper()
	out := strings.Replace(string(raw), old, replacement, 1)
	if out == string(raw) {
		t.Fatalf("replace %q missed", old)
	}
	return []byte(out)
}

// TestVerifyRawUnreadable proves only a genuinely absent raw diff falls
// back to the filtered bytes: a present-but-unreadable raw artifact
// invalidates verification instead of certifying the wrong input.
func TestVerifyRawUnreadable(t *testing.T) {
	t.Parallel()
	dir := verifyBundle(t, testDiff, "aaaa", "bbbb")
	if err := os.Mkdir(filepath.Join(dir, "full.raw.diff"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := Verify(context.Background(), nil, dir, "aaaa", "bbbb"); ok {
		t.Error("Verify with an unreadable raw diff = true, want false")
	}
}

// TestLoadRequiresPatchID proves a none-kind document without the required
// patch_id field does not load, matching the published schema; an explicit
// null still loads and never matches.
func TestLoadRequiresPatchID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	doc := `{"schema_version":"1","patch_id_kind":"none","diff_sha256":"","raw_diff_sha256":"","head_sha":"","base_sha":""}`
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Load(dir); ok {
		t.Error("Load of none-kind without patch_id = true, want false")
	}
	nulled := t.TempDir()
	if err := os.WriteFile(filepath.Join(nulled, "identity.json"),
		[]byte(strings.Replace(doc, `"patch_id_kind":"none"`, `"patch_id":null,"patch_id_kind":"none"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, ok := Load(nulled)
	if !ok {
		t.Fatal("Load of explicit-null none identity failed")
	}
	if Match(loaded, loaded) {
		t.Error("none identity matches")
	}
}
