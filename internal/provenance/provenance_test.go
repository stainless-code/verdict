package provenance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
)

// writeBundle writes files into a temp bundle dir, returning its path.
func writeBundle(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestValidateBasisLine checks the basis-line garnish never fails the run.
func TestValidateBasisLine(t *testing.T) {
	if warns := provenance.ValidateBasisLine("0 0 0 1 0 0 0", "> Confidence: 3/5\n> Body here."); len(warns) != 1 {
		t.Errorf("missing basis line warns = %v; want one warning", warns)
	}
	if warns := provenance.ValidateBasisLine("0 0 0 1 0 0 0", "> Basis: 0 0 0 1 0 0 0"); len(warns) != 0 {
		t.Errorf("matching basis line warns = %v; want none", warns)
	}
	if warns := provenance.ValidateBasisLine("0 0 0 1 0 0 0", "> Basis: 1 2 3"); len(warns) != 1 {
		t.Errorf("short basis line warns = %v; want one warning", warns)
	}
	if warns := provenance.ValidateBasisLine("0 0 0 2 0 0 0", "> Basis: 0 0 0 1 0 0 0"); len(warns) != 1 {
		t.Errorf("mismatched basis line warns = %v; want one warning", warns)
	}
}

// TestReviewBasisCounts checks expected counts derive from review comments.
func TestReviewBasisCounts(t *testing.T) {
	review := jq.NewObj("comments", []any{
		jq.NewObj("rigor", "walked", "kb", []any{"r1"}),
		jq.NewObj("rigor", "said", "kb", []any{"r1", "r2"}),
		jq.NewObj("rigor", "bogus"),
	})
	if got := provenance.ReviewBasisCounts(review, nil, "now"); got != "0 0 1 1 1 2 0" {
		t.Errorf("ReviewBasisCounts = %q; want walked+said with 2 cited", got)
	}
}

// TestStripFooter checks the machine footer strip round-trips.
func TestStripFooter(t *testing.T) {
	body := "> ✅ done\n\n---\n\n<!-- verdict-footer -->\n> receipt\n"
	if got := provenance.StripFooter(body); got != "> ✅ done" {
		t.Errorf("StripFooter() = %q; want body without the machine block", got)
	}
	plain := "> ✅ done\n\nSome prose."
	if got := provenance.StripFooter(plain); got != plain {
		t.Errorf("StripFooter() = %q; want untouched prose", got)
	}
}

// TestStripFooterKeepsReceipt checks a hand-written receipt (no divider)
// survives the strip.
func TestStripFooterKeepsReceipt(t *testing.T) {
	body := "> ✅ done\n\n> verdict `abc1234` old receipt\n"
	if got := provenance.StripFooter(body); got != "> ✅ done\n\n> verdict `abc1234` old receipt" {
		t.Errorf("StripFooter() = %q; want the agent receipt kept", got)
	}
}

// TestHeadShortDirty checks the dirty-head readings, including the JSON
// string "true" form the shell's `jq -r` comparison accepts.
func TestHeadShortDirty(t *testing.T) {
	dir := writeBundle(t, map[string]string{
		"source.json": `{"head_ref":"work"}`,
	})
	_ = dir
	boolDir := writeBundle(t, map[string]string{"source.json": `{"dirty":true}`})
	if got := provenance.HeadShort(boolDir); got != "dirty" {
		t.Errorf("HeadShort(bool dirty) = %q; want dirty", got)
	}
	strDir := writeBundle(t, map[string]string{"source.json": `{"dirty":"true"}`})
	if got := provenance.HeadShort(strDir); got != "dirty" {
		t.Errorf("HeadShort(string dirty) = %q; want dirty", got)
	}
	shaDir := writeBundle(t, map[string]string{"source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`})
	if got := provenance.HeadShort(shaDir); got != "aaaaaaa" {
		t.Errorf("HeadShort(sha) = %q; want aaaaaaa", got)
	}
	unknownDir := writeBundle(t, map[string]string{"source.json": `{}`})
	if got := provenance.HeadShort(unknownDir); got != "unknown" {
		t.Errorf("HeadShort(empty) = %q; want unknown", got)
	}
}

// TestEvidenceLinkable checks the linkability gate.
func TestEvidenceLinkable(t *testing.T) {
	local := writeBundle(t, map[string]string{"source.json": `{}`})
	if got := provenance.EvidenceLinkable(local, "aaaaaaa"); got != "false" {
		t.Errorf("EvidenceLinkable(local) = %q; want false", got)
	}
	gh := writeBundle(t, map[string]string{
		"PR.json": `{"headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
	})
	if got := provenance.EvidenceLinkable(gh, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); got != "true" {
		t.Errorf("EvidenceLinkable(github) = %q; want true", got)
	}
	if got := provenance.EvidenceLinkable(gh, ""); got != "false" {
		t.Errorf("EvidenceLinkable(empty sha) = %q; want false", got)
	}
	other := writeBundle(t, map[string]string{
		"PR.json": `{"provider":"gitlab","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
	})
	if got := provenance.EvidenceLinkable(other, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); got != "false" {
		t.Errorf("EvidenceLinkable(gitlab) = %q; want false", got)
	}
}

// TestValidateEvidenceLinks checks blob-SHA pinning warnings.
func TestValidateEvidenceLinks(t *testing.T) {
	sha := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	ok := "see https://x/blob/aaaaaaa/p#L1 here sir"
	if warns := provenance.ValidateEvidenceLinks(ok, sha, "true"); len(warns) != 0 {
		t.Errorf("matching blob warns = %v; want none", warns)
	}
	bad := "see https://x/blob/bbbbbbb/p#L1 here sir"
	if warns := provenance.ValidateEvidenceLinks(bad, sha, "true"); len(warns) != 1 {
		t.Errorf("mismatched blob warns = %v; want one warning", warns)
	}
	if warns := provenance.ValidateEvidenceLinks(bad, sha, "false"); len(warns) != 1 {
		t.Errorf("non-github blob warns = %v; want one warning", warns)
	}
}

// TestFileCount checks the file-count words.
func TestFileCount(t *testing.T) {
	dir := writeBundle(t, map[string]string{
		"full.diff": "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-x\n+y\ndiff --git a/b b/b\n--- a/b\n+++ b/b\n@@ -1 +1 @@\n-x\n+y\n",
	})
	if got := provenance.FileCount(dir); got != "2 files" {
		t.Errorf("FileCount() = %q; want 2 files", got)
	}
	empty := writeBundle(t, map[string]string{})
	if got := provenance.FileCount(empty); got != "files unrecorded" {
		t.Errorf("FileCount() = %q; want files unrecorded", got)
	}
}

// TestLinkedVersionPreference checks the linked release version wins over
// package.json (installed binaries ship no checkout), and clearing it
// restores the file fallback.
func TestLinkedVersionPreference(t *testing.T) {
	dir := writeBundle(t, map[string]string{"package.json": `{"version":"0.3.1"}`})
	if got := provenance.ToolVersion(dir); got != "0.3.1" {
		t.Fatalf("ToolVersion(file) = %q; want 0.3.1", got)
	}
	provenance.SetBuildVersion("1.2.3")
	defer provenance.SetBuildVersion("")
	if got := provenance.ToolVersion(dir); got != "1.2.3" {
		t.Errorf("ToolVersion(linked) = %q; want 1.2.3", got)
	}
	if got := provenance.ToolVersion(filepath.Join(dir, "missing")); got != "1.2.3" {
		t.Errorf("ToolVersion(linked, no checkout) = %q; want 1.2.3", got)
	}
}
