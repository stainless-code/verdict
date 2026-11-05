package postcore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const patchGuardPatch = "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"

const patchGuardOther = "0123456789abcdef0123456789abcdef0123456789"

// patchGuardBundle writes one bundle with the given identity and an
// optional completed post receipt stamped with the same patch.
func patchGuardBundle(t *testing.T, bundle, patch, kind, head string, posted bool) {
	t.Helper()
	dir := ".review/" + bundle
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ident := `{"schema_version":"1","patch_id":` + patchJSON(patch) + `,"patch_id_kind":"` + kind + `",` +
		`"diff_sha256":"d","raw_diff_sha256":"r","head_sha":"` + head + `","base_sha":"b"}`
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), []byte(ident), 0o644); err != nil {
		t.Fatal(err)
	}
	if posted {
		post := `{"schema_version":"1","review":{"id":1},"head":"` + head + `",` +
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
	patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", cur, false)
	patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", sib, true)
}

// TestPatchGuardRefusesSamePatchNewSHA proves the sibling completed-post
// scan refuses the same patch at a new SHA, naming the prior bundle and
// --force.
func TestPatchGuardRefusesSamePatchNewSHA(t *testing.T) {
	const cur = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	patchGuardPair(t, cur, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	var warnings []string
	p := postTestState(".review/1648", cur, core.PostFlags{}, &warnings)
	ref := p.patchGuard()
	if ref == nil || ref.Code != "already-reviewed-patch" {
		t.Fatalf("patchGuard = %v, want already-reviewed-patch", ref)
	}
	if !strings.Contains(ref.Message, ".review/1649") || !strings.Contains(ref.Message, "--force") {
		t.Errorf("refusal message names neither the prior bundle nor --force: %q", ref.Message)
	}
}

// TestPatchGuardForceRecords proves --force posts through and records the
// reason, prior bundle, and prior head in the forced audit.
func TestPatchGuardForceRecords(t *testing.T) {
	const cur, sib = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	patchGuardPair(t, cur, sib)
	var warnings []string
	forced := postTestState(".review/1648", cur, core.PostFlags{Force: true}, &warnings)
	if ref := forced.patchGuard(); ref != nil {
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
	p := postTestState(".review/1648", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", core.PostFlags{}, &warnings)
	if ref := p.patchGuard(); ref != nil {
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
		patchGuardBundle(t, "1648", patchGuardOther, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
		patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", true)
	}, "")
}

// TestPatchGuardKindMismatch proves a kind mismatch never matches.
func TestPatchGuardKindMismatch(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
		patchGuardBundle(t, "1649", patchGuardPatch, "diff-sha256", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", true)
	}, "")
}

// TestPatchGuardLegacy proves a bundle without identity warns and proceeds
// with the SHA-only dedupe.
func TestPatchGuardLegacy(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		if err := os.MkdirAll(".review/1648", 0o755); err != nil {
			t.Fatal(err)
		}
	}, "patch identity unavailable — dedupe by SHA only")
}

// TestPatchGuardMalformedIdentity proves a malformed identity warns and
// proceeds instead of refusing.
func TestPatchGuardMalformedIdentity(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		if err := os.MkdirAll(".review/1648", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(".review/1648/identity.json", []byte("not json"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, "patch identity unavailable — dedupe by SHA only")
}

// TestPatchGuardMalformedSibling proves a malformed sibling receipt cannot
// block a post.
func TestPatchGuardMalformedSibling(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
		if err := os.MkdirAll(".review/1649", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(".review/1649/post.json", []byte("{broken"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, "")
}

// TestPatchGuardUnpostedSibling proves an unposted bundle never blocks,
// even with the same patch id.
func TestPatchGuardUnpostedSibling(t *testing.T) {
	patchGuardProceeds(t, func(t *testing.T) {
		t.Helper()
		patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
		patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", false)
	}, "")
}

// TestPatchGuardDryRunSkips proves a dry run never refuses on patch id.
func TestPatchGuardDryRunSkips(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	patchGuardBundle(t, "1648", patchGuardPatch, "git-patch-id", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
	patchGuardBundle(t, "1649", patchGuardPatch, "git-patch-id", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", true)
	var warnings []string
	p := postTestState(".review/1648", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", core.PostFlags{DryRun: true}, &warnings)
	if ref := p.patchGuard(); ref != nil {
		t.Fatalf("dry-run patchGuard = %v, want nil", ref)
	}
}
