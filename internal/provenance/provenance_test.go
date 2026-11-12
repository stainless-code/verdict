package provenance_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
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

// TestReviewBasisCounts checks expected counts derive from review comments
// for rigors and from this run's stored entries for cites: a dropped cite
// never reaches the line, and a carried-over finding stays silent.
func TestReviewBasisCounts(t *testing.T) {
	review := jq.NewObj("comments", []any{
		jq.NewObj("rigor", "walked", "kb", []any{"r1"}),
		jq.NewObj("rigor", "said", "kb", []any{"r1", "r2"}),
		jq.NewObj("rigor", "bogus"),
	})
	fresh := func(kb []any) any {
		return jq.NewObj("kb", kb, "history", []any{
			jq.NewObj("at", "now", "event", "reported"),
		})
	}
	carried := jq.NewObj("kb", []any{"old-rule"}, "history", []any{
		jq.NewObj("at", "earlier", "event", "reported"),
	})
	entries := []any{fresh([]any{"r1"}), fresh([]any{"r1", "r2"}), fresh([]any{}), carried}
	if got := provenance.ReviewBasisCounts(review, entries, "now"); got != "0 0 1 1 1 2 0" {
		t.Errorf("ReviewBasisCounts = %q; want walked+said with 2 stored cites", got)
	}
	if got := provenance.ReviewBasisCounts(review, nil, "now"); got != "0 0 1 1 1 0 0" {
		t.Errorf("ReviewBasisCounts without entries = %q; want no cites", got)
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
// string "true" form the comparison accepts.
func TestHeadShortDirty(t *testing.T) {
	refDir := writeBundle(t, map[string]string{
		"source.json": `{"head_ref":"work"}`,
	})
	if got := provenance.HeadShort(refDir); got != "unknown" {
		t.Errorf("HeadShort(branch only) = %q; want unknown", got)
	}
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

// TestToolVersionDegrade checks the footer's no-metadata fallback is the
// shared version constant, so --version and the footer agree (F3).
func TestToolVersionDegrade(t *testing.T) {
	dir := writeBundle(t, map[string]string{})
	if got := provenance.ToolVersion(filepath.Join(dir, "missing")); got != core.VersionUnknown {
		t.Errorf("ToolVersion(no metadata) = %q; want %q", got, core.VersionUnknown)
	}
}

// TestPromptsHashLayouts proves the prompt-set hash reads a flat skill
// directory and a checkout's skills/verdict alike, prefers the flat copy, and
// hashes the empty set when neither exists. The expected digests are SHA-256
// of the documented "== <path> ==\n<bytes>\n" text, written out literally.
func TestPromptsHashLayouts(t *testing.T) {
	sum := func(text string) string {
		h := sha256.Sum256([]byte(text))
		return hex.EncodeToString(h[:])
	}
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "flat skill dir",
			files: map[string]string{"SKILL.md": "S", "prompts/review.md": "R"},
			want:  sum("== SKILL.md ==\nS\n== prompts/review.md ==\nR\n"),
		},
		{
			name:  "checkout skills/verdict",
			files: map[string]string{"skills/verdict/SKILL.md": "S", "skills/verdict/prompts/review.md": "R"},
			want:  sum("== SKILL.md ==\nS\n== prompts/review.md ==\nR\n"),
		},
		{
			name:  "flat file wins over the checkout copy",
			files: map[string]string{"SKILL.md": "flat", "skills/verdict/SKILL.md": "nested"},
			want:  sum("== SKILL.md ==\nflat\n"),
		},
		{
			name:  "a missing flat file falls back per file",
			files: map[string]string{"SKILL.md": "flat", "skills/verdict/prompts/system.md": "Y"},
			want:  sum("== SKILL.md ==\nflat\n== prompts/system.md ==\nY\n"),
		},
		{name: "no prompt files", files: map[string]string{"notes.txt": "x"}, want: sum("")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := provenance.PromptsHash(writeBundle(t, tc.files)); got != tc.want {
				t.Errorf("PromptsHash = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestApplyBasisLine proves the CLI owns the line: a matching line is left
// alone, a disagreeing line is replaced, and a missing line is inserted under
// the leading blockquote lead.
func TestApplyBasisLine(t *testing.T) {
	const expected = "0 0 0 1 0 0 0"
	checkMatchingBasis(t, expected)
	checkReplacedBasis(t, expected)
	checkInsertedBasis(t, expected)
}

// checkMatchingBasis proves a matching Basis line is left untouched.
func checkMatchingBasis(t *testing.T, expected string) {
	t.Helper()
	body := "> Basis: " + expected + "\n> body"
	if got, p := provenance.ApplyBasisLine(expected, body); p != nil || got != body {
		t.Errorf("matching line changed: %q (%+v)", got, p)
	}
}

// checkReplacedBasis proves a disagreeing Basis line is replaced with the
// computed counts and reported.
func checkReplacedBasis(t *testing.T, expected string) {
	t.Helper()
	got, p := provenance.ApplyBasisLine(expected, "> Confidence: 3/5\n> Basis: 9 9 9 9 9 9 9\n> body")
	if p == nil || p.Code != core.ProblemCodeBasisReplaced || p.Example != "> Basis: "+expected {
		t.Fatalf("mismatch problem = %+v", p)
	}
	if got != "> Confidence: 3/5\n> Basis: "+expected+"\n> body" {
		t.Errorf("mismatch body = %q", got)
	}
}

// checkInsertedBasis proves a missing Basis line is inserted with a warning.
func checkInsertedBasis(t *testing.T, expected string) {
	t.Helper()
	got, p := provenance.ApplyBasisLine(expected, "> ✅ body")
	if p == nil || p.Code != core.ProblemCodeBasisReplaced {
		t.Fatalf("missing problem = %+v", p)
	}
	if p.Message != "no Basis line in the leading quote block — inserted the computed counts (basis-replaced)" {
		t.Errorf("missing Basis message = %q", p.Message)
	}
	if got != "> ✅ body\n> Basis: "+expected {
		t.Errorf("missing body = %q", got)
	}
}

// TestApplyBasisLineSkipsFencedBasis proves a Basis-looking line inside a
// fenced example is never rewritten: the CLI inserts the computed line under
// the lead and leaves the fenced example byte-identical.
func TestApplyBasisLineSkipsFencedBasis(t *testing.T) {
	t.Parallel()
	const expected = "0 0 0 1 0 0 0"
	for name, body := range map[string]string{
		"bare-fence":   "> ✅ Reviewed.\n\n```\n> Basis: 9 9 9 9 9 9 9\n```",
		"quoted-fence": "> ✅ Reviewed.\n> ```\n> Basis: 9 9 9 9 9 9 9\n> ```\n> Tail",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkInsertedFencedBasis(t, expected, body)
		})
	}
}

// checkInsertedFencedBasis proves one fenced body gains the computed line
// under the lead while the fenced example stays byte-identical.
func checkInsertedFencedBasis(t *testing.T, expected, body string) {
	t.Helper()
	got, p := provenance.ApplyBasisLine(expected, body)
	if p == nil || p.Code != core.ProblemCodeBasisReplaced {
		t.Fatalf("missing problem = %+v", p)
	}
	if !strings.Contains(got, "> Basis: "+expected) {
		t.Errorf("computed line missing:\n%s", got)
	}
	if !strings.Contains(got, "> Basis: 9 9 9 9 9 9 9") {
		t.Errorf("fenced example rewritten:\n%s", got)
	}
}

// TestApplyBasisLineInsertsBlankBeforeHeading proves an inserted Basis line
// keeps a blank line before a following heading instead of sitting directly
// above it.
func TestApplyBasisLineInsertsBlankBeforeHeading(t *testing.T) {
	t.Parallel()
	const expected = "0 0 0 1 0 0 0"
	got, p := provenance.ApplyBasisLine(expected, "> ✅ Reviewed.\n## Summary")
	if p == nil || p.Code != core.ProblemCodeBasisReplaced {
		t.Fatalf("missing problem = %+v", p)
	}
	if want := "> ✅ Reviewed.\n> Basis: " + expected + "\n\n## Summary"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// TestPlainBasisBelowConfidence accepts matching plain Basis text.
func TestPlainBasisBelowConfidence(t *testing.T) {
	const body = "> Confidence: 3/5\nBasis: 0 0 0 1 0 0 0\n\n## Summary\nReviewed."
	got, problem := provenance.ApplyBasisLine("0 0 0 1 0 0 0", body)
	if got != body || problem != nil {
		t.Fatalf("plain Basis = %q, %v; want unchanged body without warning", got, problem)
	}
}
