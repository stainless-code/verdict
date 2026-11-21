package democore

import (
	"strings"
	"testing"
)

// TestParseTargetAccepts proves the accepted spellings: the URL itself, a
// trailing slash, the browser sub-pages, and a query or fragment.
func TestParseTargetAccepts(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"https://github.com/acme/app/pull/7",
		"https://github.com/acme/app/pull/7/",
		"https://github.com/acme/app/pull/7/files",
		"https://github.com/acme/app/pull/7/commits",
		"https://github.com/acme/app/pull/7/checks",
		"https://github.com/acme/app/pull/7?diff=split",
		"https://github.com/acme/app/pull/7#discussion_r1",
		"https://GitHub.com/acme/app/pull/7",
	} {
		assertAcceptedTarget(t, raw)
	}
}

// assertAcceptedTarget proves one accepted spelling reads as acme/app#7.
func assertAcceptedTarget(t *testing.T, raw string) {
	t.Helper()
	target, ref := ParseTarget(raw)
	if ref != nil {
		t.Errorf("ParseTarget(%q) refused: %v", raw, ref)
		return
	}
	if target.Owner != "acme" || target.Repo != "app" || target.Pr != "7" {
		t.Errorf("ParseTarget(%q) = %+v, want acme/app#7", raw, target)
	}
	if target.Slug() != "acme/app" {
		t.Errorf("ParseTarget(%q).Slug() = %q", raw, target.Slug())
	}
}

// TestValidRepoName pins the segment charset directly: the accepted spellings
// are the ones GitHub allows, and the refused ones are the shapes that would
// reach a provider argv as a flag, a traversal, or an escape.
func TestValidRepoName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		segment string
		want    bool
	}{
		{"acme", true},
		{"Acme.App", true},
		{"a_b-c.d", true},
		{"a", true},
		{"", false},
		{".", false},
		{"..", false},
		{"-owner", false},
		{"owner-", true},
		{"a b", false},
		{"a/b", false},
		{"a%2fb", false},
		{"a\\b", false},
		{"a\tb", false},
		{"a\nb", false},
		{"café", false},
	} {
		if got := validRepoName(tc.segment); got != tc.want {
			t.Errorf("validRepoName(%q) = %v, want %v", tc.segment, got, tc.want)
		}
	}
}

// TestParseTargetTrimsSurroundingSpace proves a copied URL with padding reads
// as the same pull request, since a shell paste carries it.
func TestParseTargetTrimsSurroundingSpace(t *testing.T) {
	t.Parallel()
	target, ref := ParseTarget("  https://github.com/acme/app/pull/1234  ")
	if ref != nil {
		t.Fatalf("ParseTarget refused: %v", ref)
	}
	if target.Pr != "1234" || target.URL != "https://github.com/acme/app/pull/1234" {
		t.Errorf("target = %+v, want acme/app#1234", target)
	}
}

// TestParseTargetRefuses proves every input the demo will not repair: the
// refusal is typed bad-target and names the wanted shape, so the operator
// learns the invocation instead of guessing why nothing happened.
func TestParseTargetRefuses(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"",
		"   ",
		"acme/app",
		"7",
		"github.com/acme/app/pull/7",
		"http://github.com/acme/app/pull/7",
		"git@github.com:acme/app.git",
		"https://gitlab.com/acme/app/pull/7",
		"https://github.com.evil.invalid/acme/app/pull/7",
		"https://user:token@github.com/acme/app/pull/7",
		"https://github.com:8443/acme/app/pull/7",
		"https://github.com/acme/app/issues/7",
		"https://github.com/acme/app/pull/0",
		"https://github.com/acme/app/pull/007",
		"https://github.com/acme/app/pull/abc",
		"https://github.com/acme/app/pull/",
		"https://github.com/acme/app",
		"https://github.com/acme/app/pull/7/deep",
		"https://github.com//app/pull/7",
		"https://github.com/acme/app/pulls/7",
		// No owner or repository segment may carry a path traversal, a flag, an
		// escape, or a space: each would be interpolated into a provider API
		// path (evals/fixtures prove the argv, this proves the refusal).
		"https://github.com/../app/pull/7",
		"https://github.com/acme/../pull/7",
		"https://github.com/acme/..%2F/pull/7",
		"https://github.com/./app/pull/7",
		"https://github.com/acme/./pull/7",
		"https://github.com/-owner/app/pull/7",
		"https://github.com/acme/-repo/pull/7",
		"https://github.com/acme%2Fsub/app/pull/7",
		"https://github.com/acme/app%2fsub/pull/7",
		"https://github.com/acme%20evil/app/pull/7",
		"https://github.com/acme/app%20evil/pull/7",
		"https://github.com/acme%2e%2e/app/pull/7",
		"https://github.com/acme/app%00/pull/7",
		"https://github.com/acme/app/pull/7%2F..",
		// Any escape refuses, not only the ones a decoder would turn into a
		// separator: the accepted segments are plain ASCII, so the refusal
		// rests on the URL as written rather than on how it decodes.
		"https://github.com/%61cme/app/pull/7",
		"https://github.com/acme/%61pp/pull/7",
	} {
		target, ref := ParseTarget(raw)
		if ref == nil {
			t.Errorf("ParseTarget(%q) accepted as %+v, want a refusal", raw, target)
			continue
		}
		if ref.Code != codeBadTarget {
			t.Errorf("ParseTarget(%q) code = %q, want %q", raw, ref.Code, codeBadTarget)
		}
		if !strings.Contains(ref.Message, publicURLShape) {
			t.Errorf("ParseTarget(%q) message = %q, want the accepted shape named", raw, ref.Message)
		}
	}
}
