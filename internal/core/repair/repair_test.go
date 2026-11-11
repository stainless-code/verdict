package repair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const repairDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old\n+new\n bottom\n"

// TestNonceIsUnpredictableHex pins the fence nonce shape: 16 lowercase hex
// chars, and no two draws agree.
func TestNonceIsUnpredictableHex(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 100 {
		assertNonce(t, seen, Nonce())
	}
}

// assertNonce pins one draw's shape and records it for the uniqueness check.
func assertNonce(t *testing.T, seen map[string]bool, n string) {
	t.Helper()
	if len(n) != 16 {
		t.Fatalf("nonce %q has length %d, want 16", n, len(n))
	}
	for _, c := range n {
		if c < '0' || (c > '9' && c < 'a') || c > 'f' {
			t.Fatalf("nonce %q is not lowercase hex", n)
		}
	}
	if seen[n] {
		t.Fatalf("nonce %q repeated", n)
	}
	seen[n] = true
}

// TestRoundPromptKeepsTheDirectiveFirst proves round 2 keeps the one-shot
// text byte-identical up front, then fences the prior bytes and the fix list
// under the run's nonce.
func TestRoundPromptKeepsTheDirectiveFirst(t *testing.T) {
	t.Parallel()
	base := "Review acme/app PR 42 and write exactly one review file.\n"
	prior := []byte(`{"schema_version":"1","body":"x"}`)
	problems := []core.Problem{
		{Path: "$.body", Code: core.ProblemCodeMissingField, Level: core.ProblemLevelError, Message: "m", Fix: "f"},
		{Path: "$", Code: core.ProblemCodeUnknownField, Level: core.ProblemLevelWarning, Message: "w", Fix: "f"},
	}
	got := RoundPrompt(base, "abc123", prior, 2, 3, problems)
	if !strings.HasPrefix(got, base) {
		t.Fatalf("round prompt does not start with the directive:\n%s", got)
	}
	for _, want := range []string{
		"<<<UNTRUSTED nonce=abc123 source=review.json:round-1>>>",
		`{"schema_version":"1","body":"x"}`,
		"<<<UNTRUSTED nonce=abc123 source=repair-feedback>>>",
		"Round 2/3:",
		"<<<END UNTRUSTED nonce=abc123>>>",
		"Fix every error. Reply with one JSON document only.\n<<<END UNTRUSTED nonce=abc123>>>\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("round prompt misses %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "<<<UNTRUSTED nonce=abc123") != 2 || strings.Count(got, "<<<END UNTRUSTED nonce=abc123") != 2 {
		t.Errorf("round prompt fences the wrong number of sections:\n%s", got)
	}
}

// TestFeedbackSortsErrorsFirst proves the fix list leads with errors: the
// agent fixes what refuses before what warns.
func TestFeedbackSortsErrorsFirst(t *testing.T) {
	t.Parallel()
	problems := []core.Problem{
		{Path: "$.x", Code: core.ProblemCodeUnknownField, Level: core.ProblemLevelWarning, Message: "w", Fix: "f"},
		{Path: "$.body", Code: core.ProblemCodeMissingField, Level: core.ProblemLevelError, Message: "e", Fix: "f"},
	}
	got := Feedback(2, 3, problems)
	errAt := strings.Index(got, `"level":"error"`)
	warnAt := strings.Index(got, `"level":"warning"`)
	if errAt < 0 || warnAt < 0 || errAt > warnAt {
		t.Errorf("feedback does not lead with the error:\n%s", got)
	}
	if !strings.Contains(got, "has 1 error(s) and 1 warning(s)") {
		t.Errorf("feedback misses the counts:\n%s", got)
	}
}

// TestCheckIsLenient proves the loop's checker matches record's default: an
// invalid document errors, a warnings-only one reads as valid.
func TestCheckIsLenient(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(repairDiff), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := Check(dir, []byte("not json")); res.Errors == 0 {
		t.Error("Check(not-json).Errors = 0, want errors")
	}
	warningsOnly := []byte(`{"body":"> ✅ Looks good overall with one note.","approved":false,"comments":[]}`)
	if res := Check(dir, warningsOnly); !Valid(res) {
		t.Errorf("Check(warnings-only) is invalid: %+v", res.Problems)
	}
	offAnchor := []byte(`{"schema_version":"1","body":"> ✅ Anchored note on the change.",` +
		`"approved":false,"comments":[{"path":"elsewhere.txt","line":1,"side":"RIGHT","body":"note","rigor":"walked"}]}`)
	if res := Check(dir, offAnchor); Valid(res) {
		t.Error("Check(off-diff anchor) is valid, want the path error")
	}
	if got := Problems(Check(dir, offAnchor)); len(got) == 0 || got[0].Level != core.ProblemLevelError {
		t.Errorf("Problems does not lead with the error: %+v", got)
	}
}
