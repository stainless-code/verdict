package postcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const patchGuardPatch = "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"

const patchGuardOther = "0123456789abcdef0123456789abcdef01234567"

const patchGuardFallback = "94d2a258c8308641db7b0ada6e81eda8fae398466cb7652b30a837ba95076caa"

const patchGuardBase = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

const patchGuardDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

const patchGuardOtherDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+other line\n bottom\n"

// patchGuardCommander answers `git patch-id --stable` for the fixture
// diffs and fails anything else exactly like an unavailable git: unknown
// input cannot be recomputed, so it stays unverified.
type patchGuardCommander struct {
	ids map[string]string
}

func (c *patchGuardCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) == 3 && cmd.Argv[0] == "git" && cmd.Stdin != nil {
		raw, err := io.ReadAll(cmd.Stdin)
		if err == nil {
			if id, ok := c.ids[string(raw)]; ok {
				return core.Result{Stdout: id + " 0000000000000000000000000000000000000000\n"}, nil
			}
		}
	}
	return core.Result{ExitCode: 1}, errPatchGuardNoGit
}

var errPatchGuardNoGit = errors.New("cannot compute patch id")

// guardCommander owns the canned diff-to-id map for guard fixtures.
func guardCommander() core.Commander {
	return &patchGuardCommander{ids: map[string]string{
		patchGuardDiff:      patchGuardPatch,
		patchGuardOtherDiff: patchGuardOther,
	}}
}

// patchGuardState builds a guard test state over the canonical pair
// whose injected commander recomputes the fixture identities.
func patchGuardState(flags core.PostFlags, warnings *[]string) *state {
	p := postTestState(".verdict/state/1648", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", flags, warnings)
	p.req.Commander = guardCommander()
	return p
}

// patchGuardBundle writes one coherent bundle: full.diff bytes bound to a
// matching identity at the given head, a PR record carrying the head, base,
// and repository URL, and an optional completed post receipt stamped with
// the same patch.
func patchGuardBundle(t *testing.T, bundle, patch, kind, head, diff string, posted bool) {
	t.Helper()
	patchGuardBundleAt(t, bundle, patch, kind, head, diff, "https://github.com/acme/app/pull/"+bundle, posted)
}

// patchGuardBundleAt is patchGuardBundle with an explicit PR record URL,
// for the cross-repository regression.
func patchGuardBundleAt(t *testing.T, bundle, patch, kind, head, diff, url string, posted bool) {
	t.Helper()
	dir := ".verdict/state/" + bundle
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(diff))
	sha := hex.EncodeToString(sum[:])
	ident := `{"schema_version":"1","patch_id":` + patchJSON(patch) + `,"patch_id_kind":"` + kind + `",` +
		`"diff_sha256":"` + sha + `","raw_diff_sha256":"` + sha + `","head_sha":"` + head + `","base_sha":"` + patchGuardBase + `"}`
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), []byte(ident), 0o644); err != nil {
		t.Fatal(err)
	}
	pr := `{"state":"OPEN","headRefOid":"` + head + `","baseRefOid":"` + patchGuardBase + `","url":"` + url + `"}`
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(pr), 0o644); err != nil {
		t.Fatal(err)
	}
	if posted {
		post := `{"schema_version":"1","review":{"id":1,"state":"COMMENTED"},"head":"` + head + `",` +
			`"posted_at":"2026-01-01T00:00:00Z","patch_id":` + patchJSON(patch) +
			`,"patch_id_kind":"` + kind + `"}`
		if err := os.WriteFile(filepath.Join(dir, "post.json"), []byte(post), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func patchJSON(patch string) string {
	if patch == "" {
		return "null"
	}
	return `"` + patch + `"`
}

// patchGuardPair writes the canonical sibling pair: the current bundle at
// cur and a completed same-patch sibling at sib.
func patchGuardPair(t *testing.T, cur, sib string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", cur, patchGuardDiff, false)
	patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", sib, patchGuardDiff, true)
}

// TestPatchGuardRefusesSamePatchNewSHA proves the sibling completed-post
// scan refuses the same patch at a new SHA, naming the prior bundle and
// --force.
func TestPatchGuardRefusesSamePatchNewSHA(t *testing.T) {
	const cur = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	patchGuardPair(t, cur, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	var warnings []string
	p := patchGuardState(core.PostFlags{}, &warnings)
	ref := p.patchGuard(context.Background())
	if ref == nil || ref.Code != "already-reviewed-patch" {
		t.Fatalf("patchGuard = %v, want already-reviewed-patch", ref)
	}
	if !strings.Contains(ref.Message, ".verdict/state/1649") || !strings.Contains(ref.Message, "--force") {
		t.Errorf("refusal message names neither the prior bundle nor --force: %q", ref.Message)
	}
}

// TestPatchGuardForceRecords proves --force posts through and records the
// reason, prior bundle, and prior head in the forced audit.
func TestPatchGuardForceRecords(t *testing.T) {
	const cur, sib = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	patchGuardPair(t, cur, sib)
	var warnings []string
	forced := patchGuardState(core.PostFlags{Force: true}, &warnings)
	if ref := forced.patchGuard(context.Background()); ref != nil {
		t.Fatalf("forced patchGuard = %v, want nil", ref)
	}
	assertForcedPatch(t, forced)
}

// assertForcedPatch proves the recorded audit entry names the fired guard
// with the sibling bundle and head.
func assertForcedPatch(t *testing.T, forced *state) {
	t.Helper()
	if len(forced.forced) != 1 {
		t.Fatalf("forced entries = %d, want 1", len(forced.forced))
	}
	got := forced.forced[0]
	if got.Reason != "already-reviewed-patch" || got.PatchID == nil || *got.PatchID != patchGuardPatch {
		t.Errorf("forced entry = %+v, want reason + patch id", got)
	}
	if got.PreviousBundle == nil || *got.PreviousBundle != "1649" {
		t.Errorf("forced previous_bundle = %v, want 1649", got.PreviousBundle)
	}
	if got.PreviousHead == nil || *got.PreviousHead != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Errorf("forced previous_head = %v, want the sibling head", got.PreviousHead)
	}
}

// patchGuardProceeds runs the guard over a fresh bundle pair and proves it
// proceeds, optionally proving the legacy warning.
func patchGuardProceeds(t *testing.T, setup func(t *testing.T), wantWarn string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	setup(t)
	var warnings []string
	p := patchGuardState(core.PostFlags{}, &warnings)
	if ref := p.patchGuard(context.Background()); ref != nil {
		t.Fatalf("patchGuard = %v, want nil", ref)
	}
	if wantWarn != "" && !slices.Contains(warnings, wantWarn) {
		t.Errorf("warnings = %q, want %q", warnings, wantWarn)
	}
}

// TestPatchGuardDifferentPatch proves a different patch posts normally.
func TestPatchGuardDifferentPatch(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		patchGuardBundle(t, "1648", patchGuardOther, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardOtherDiff, false)
		patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", patchGuardDiff, true)
	}, "")
}

// TestPatchGuardKindMismatch proves a kind mismatch never matches.
func TestPatchGuardKindMismatch(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
		patchGuardBundle(t, "1649", patchGuardFallback, "diff-sha256", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", patchGuardDiff, true)
	}, "")
}

// TestPatchGuardLegacy proves a bundle without identity warns and proceeds
// with the SHA-only dedupe.
func TestPatchGuardLegacy(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		if err := os.MkdirAll(".verdict/state/1648", 0o755); err != nil {
			t.Fatal(err)
		}
	}, "patch identity unavailable — dedupe by SHA only")
}

// TestPatchGuardMalformedIdentity proves a malformed identity warns and
// proceeds instead of refusing.
func TestPatchGuardMalformedIdentity(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		if err := os.MkdirAll(".verdict/state/1648", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(".verdict/state/1648/identity.json", []byte("not json"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, "patch identity unavailable — dedupe by SHA only")
}

// TestPatchGuardMalformedSibling proves a malformed sibling receipt cannot
// block a post.
func TestPatchGuardMalformedSibling(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
		if err := os.MkdirAll(".verdict/state/1649", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(".verdict/state/1649/post.json", []byte("{broken"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, "")
}

// TestPatchGuardUnpostedSibling proves an unposted bundle never blocks,
// even with the same patch id.
func TestPatchGuardUnpostedSibling(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
		patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", patchGuardDiff, false)
	}, "")
}

// TestPatchGuardDryRunSkips proves a dry run never refuses on patch id.
func TestPatchGuardDryRunSkips(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
	patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", patchGuardDiff, true)
	var warnings []string
	p := patchGuardState(core.PostFlags{DryRun: true}, &warnings)
	if ref := p.patchGuard(context.Background()); ref != nil {
		t.Fatalf("dry-run patchGuard = %v, want nil", ref)
	}
}

// TestPatchGuardStaleBytesProceeds proves a same-patch sibling cannot block
// when the current identity no longer binds the bundle bytes: the guard
// warns and falls back to SHA dedupe instead of trusting stored strings.
func TestPatchGuardStaleBytesProceeds(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
		patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", patchGuardDiff, true)
		if err := os.WriteFile(".verdict/state/1648/full.diff", []byte("replaced bytes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, "patch identity unavailable — dedupe by SHA only")
}

// TestPatchGuardInvalidIdentityProceeds proves an algorithm-invalid patch
// id warns and proceeds instead of matching.
func TestPatchGuardInvalidIdentityProceeds(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
		patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", patchGuardDiff, true)
		raw, err := os.ReadFile(".verdict/state/1648/identity.json")
		if err != nil {
			t.Fatal(err)
		}
		bogus := strings.Replace(string(raw), patchGuardPatch, "not-a-patch-id", 1)
		if err := os.WriteFile(".verdict/state/1648/identity.json", []byte(bogus), 0o644); err != nil {
			t.Fatal(err)
		}
	}, "patch identity unavailable — dedupe by SHA only")
}

// incompleteReceipts owns the receipts that prove no publication: a bare
// patch stub, a pending review, and a dry-run marker, all naming the same
// patch the sibling scan would otherwise match.
func incompleteReceipts() map[string]string {
	return map[string]string{
		"two-field": `{"patch_id":"` + patchGuardPatch + `","patch_id_kind":"git-patch-id"}`,
		"pending": `{"schema_version":"1","review":{"id":1,"state":"PENDING"},` +
			`"head":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","posted_at":"2026-01-01T00:00:00Z",` +
			`"patch_id":"` + patchGuardPatch + `","patch_id_kind":"git-patch-id"}`,
		"dry-run": `{"schema_version":"1","review":{"id":1,"state":"COMMENTED"},"dry_run":true,` +
			`"head":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","posted_at":"2026-01-01T00:00:00Z",` +
			`"patch_id":"` + patchGuardPatch + `","patch_id_kind":"git-patch-id"}`,
	}
}

// TestPatchGuardIncompleteReceiptProceeds proves two-field, pending, and
// dry-run sibling receipts prove no publication and never block.
func TestPatchGuardIncompleteReceiptProceeds(t *testing.T) {
	for name, receipt := range incompleteReceipts() {
		t.Run(name, func(t *testing.T) {
			patchGuardProceeds(t, func(t *testing.T) {
				t.Helper()
				incompleteReceiptBundle(t, receipt)
			}, "")
		})
	}
}

// incompleteReceiptBundle writes the canonical pair with a crafted sibling
// receipt in place of a completed publication.
func incompleteReceiptBundle(t *testing.T, receipt string) {
	t.Helper()
	patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
	patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", patchGuardDiff, false)
	if err := os.WriteFile(".verdict/state/1649/post.json", []byte(receipt), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPatchGuardCrossRepoSiblingProceeds proves a same-patch receipt from a
// foreign repository never blocks: patch identity stays inside one repo.
func TestPatchGuardCrossRepoSiblingProceeds(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
		patchGuardBundleAt(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			patchGuardDiff, "https://github.com/other/repository/pull/1649", true)
	}, "")
}

// TestPatchGuardSameSHAWinsFirst proves guard order: a bundle already
// posted at the same head refuses already-posted even with a same-patch
// sibling waiting, and --force records both bypassed guards in guard order.
func TestPatchGuardSameSHAWinsFirst(t *testing.T) {
	const cur = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const sib = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	patchGuardPair(t, cur, sib)
	if err := os.WriteFile(".verdict/state/1648/post.json", []byte(
		`{"schema_version":"1","review":{"id":1,"state":"COMMENTED"},"head":"`+cur+`",`+
			`"posted_at":"2026-01-01T00:00:00Z","patch_id":"`+patchGuardPatch+`","patch_id_kind":"git-patch-id"}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	p := patchGuardState(core.PostFlags{}, &warnings)
	if ref := p.runGuards(context.Background()); ref == nil || ref.Code != "already-posted" {
		t.Fatalf("runGuards = %v, want already-posted first", ref)
	}
	forced := patchGuardState(core.PostFlags{Force: true}, &warnings)
	if ref := forced.runGuards(context.Background()); ref != nil {
		t.Fatalf("forced runGuards = %v, want nil", ref)
	}
	if len(forced.forced) != 2 {
		t.Fatalf("forced entries = %d, want the ordered pair", len(forced.forced))
	}
	if forced.forced[0].Reason != "already-posted" || forced.forced[1].Reason != "already-reviewed-patch" {
		t.Errorf("forced order = %q, want already-posted then already-reviewed-patch",
			[]string{forced.forced[0].Reason, forced.forced[1].Reason})
	}
}

// TestPatchGuardTamperedClaimProceeds proves a hex-valid but unrecomputable
// current patch claim never blocks: coherent hashes and head plus a sibling
// receipt matching the tampered strings still warn and fall back to SHA
// dedupe, because the claim does not reproduce the raw input.
func TestPatchGuardTamperedClaimProceeds(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
	patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", patchGuardDiff, true)
	raw, err := os.ReadFile(".verdict/state/1648/identity.json")
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), patchGuardPatch, strings.Repeat("1", 40), 1)
	if err := os.WriteFile(".verdict/state/1648/identity.json", []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	p := patchGuardState(core.PostFlags{}, &warnings)
	if ref := p.patchGuard(context.Background()); ref != nil {
		t.Fatalf("patchGuard = %v, want nil", ref)
	}
	if !slices.Contains(warnings, "patch identity unavailable — dedupe by SHA only") {
		t.Errorf("warnings = %q, want the fallback warning", warnings)
	}
}

// TestPatchGuardEmptyReceiptProceeds proves an empty review object proves
// no publication even when every other receipt field is well-formed.
func TestPatchGuardEmptyReceiptProceeds(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
	patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", patchGuardDiff, false)
	receipt := `{"schema_version":"1","review":{},"head":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",` +
		`"posted_at":"2026-01-01T00:00:00Z","patch_id":"` + patchGuardPatch + `","patch_id_kind":"git-patch-id"}`
	if err := os.WriteFile(".verdict/state/1649/post.json", []byte(receipt), 0o644); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	p := patchGuardState(core.PostFlags{}, &warnings)
	if ref := p.patchGuard(context.Background()); ref != nil {
		t.Fatalf("patchGuard = %v, want nil", ref)
	}
}

// TestPatchGuardInvalidHeadTimeProceeds proves non-head/non-time receipt
// metadata proves no publication.
func TestPatchGuardInvalidHeadTimeProceeds(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
	patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", patchGuardDiff, false)
	receipt := `{"schema_version":"1","review":{},"head":"not-a-head","posted_at":"not-a-time",` +
		`"patch_id":"` + patchGuardPatch + `","patch_id_kind":"git-patch-id"}`
	if err := os.WriteFile(".verdict/state/1649/post.json", []byte(receipt), 0o644); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	p := patchGuardState(core.PostFlags{}, &warnings)
	if ref := p.patchGuard(context.Background()); ref != nil {
		t.Fatalf("patchGuard = %v, want nil", ref)
	}
}

// TestPatchGuardRawUnreadableProceeds proves a present-but-unreadable raw
// diff invalidates verification instead of certifying the filtered bytes.
func TestPatchGuardRawUnreadableProceeds(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", patchGuardDiff, false)
	patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", patchGuardDiff, true)
	if err := os.Mkdir(".verdict/state/1648/full.raw.diff", 0o755); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	p := patchGuardState(core.PostFlags{}, &warnings)
	if ref := p.patchGuard(context.Background()); ref != nil {
		t.Fatalf("patchGuard = %v, want nil", ref)
	}
	if !slices.Contains(warnings, "patch identity unavailable — dedupe by SHA only") {
		t.Errorf("warnings = %q, want the fallback warning", warnings)
	}
}
