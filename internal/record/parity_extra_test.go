package record_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claimsSetup seeds a one-comment bundle with an active KB rule for the
// verification-claim matrix legs.
func claimsSetup(t *testing.T, dir string) {
	t.Helper()
	kbBundle(t, dir, kbReview(`["rec-rule"]`), []string{`{"id":"rec-rule","status":"active"}`})
}

// claimMutate replaces the review with the given verification claims,
// keeping comments empty so the second pass merges against phase-1 entries.
func claimMutate(t *testing.T, dir string, claims string) {
	t.Helper()
	writeFiles(t, dir, map[string]string{
		".review/local-kb/review.json":       `{"schema_version":"1","body":"> [!IMPORTANT]\n> Second pass with evidence.","approved":false,"comments":[]}`,
		".review/local-kb/verification.json": claims,
	})
}

// claimsOf builds verification claims for every phase-1 sig.
func claimsOf(t *testing.T, dir string, fn func(sig string) string) string {
	t.Helper()
	sigs := sigsOf(t, dir)
	if len(sigs) == 0 {
		t.Fatal("no sigs")
	}
	parts := make([]string, 0, len(sigs))
	for _, s := range sigs {
		parts = append(parts, fn(s))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// sharedPrefix finds a sig prefix matching at least two entries (for the
// ambiguous-prefix leg). Fixtures are deterministic, so this is stable.
func sharedPrefix(t *testing.T, dir string) string {
	t.Helper()
	sigs := sigsOf(t, dir)
	seen := prefixCounts(sigs)
	for _, s := range sigs {
		if p := firstShared(s, seen); p != "" {
			return p
		}
	}
	t.Fatal("no shared sig prefix")
	return ""
}

// prefixCounts counts short prefixes across sigs.
func prefixCounts(sigs []string) map[string]int {
	seen := map[string]int{}
	for _, s := range sigs {
		for i := 1; i <= len(s) && i <= maxPrefixLen; i++ {
			seen[s[:i]]++
		}
	}
	return seen
}

// firstShared returns the first prefix of s shared with another sig.
func firstShared(s string, seen map[string]int) string {
	for i := 1; i <= len(s) && i <= maxPrefixLen; i++ {
		if seen[s[:i]] > 1 {
			return s[:i]
		}
	}
	return ""
}

// maxPrefixLen bounds the ambiguous-prefix search depth.
const maxPrefixLen = 3

// TestParityVerifyMatrix checks the full verification-claim matrix (I6).
// verifyLeg is one verification-claim matrix leg: phase 1 records, phase 2
// applies mutate, then both sides are compared.
type verifyLeg struct {
	setup  func(t *testing.T, dir string)
	mutate func(t *testing.T, dir string)
	name   string
}

// claimAll builds one verification claim per phase-1 sig.
func claimAll(t *testing.T, dir, verdict, head, cmd string) string {
	t.Helper()
	return claimsOf(t, dir, func(sig string) string {
		return fmt.Sprintf(`{"sig":%q,"verdict":%q,"resultHead":%q,"verifyCmd":%q}`, sig, verdict, head, cmd)
	})
}

// mutateClaims replaces phase 2 with empty comments plus built claims.
func mutateClaims(verdict, head, cmd string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		claimMutate(t, dir, claimAll(t, dir, verdict, head, cmd))
	}
}

// verifyLegs lists the I6 matrix legs.
func verifyLegs() []verifyLeg {
	return []verifyLeg{
		{claimsSetup, mutateClaims("VERIFIED", "bbbbbbb", "make check"), "verify/bad-head"},
		{claimsSetup, mutateClaims("VERIFIED", "aaaaaaa", ""), "verify/empty-cmd"},
		{claimsSetup, mutateClaims("INCONCLUSIVE", "aaaaaaa", "make check"), "verify/inconclusive"},
		{claimsSetup, mutatePatchClaims(`{"verifyExit":1}`), "verify/contradict-exit"},
		{claimsSetup, mutatePatchClaims(`{"timedOut":true}`), "verify/contradict-timeout"},
		{claimsSetup, mutateAddressed, "verify/on-addressed"},
		{setupMany, mutateAmbiguous, "verify/ambiguous-prefix"},
	}
}

// mutatePatchClaims applies VERIFIED-for-every-sig plus a patch.json.
func mutatePatchClaims(patch string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		claimMutate(t, dir, claimAll(t, dir, "VERIFIED", "aaaaaaa", "make check"))
		writeFiles(t, dir, map[string]string{".review/local-kb/patch.json": patch})
	}
}

// mutateAddressed flips the first sig addressed by triage while VERIFIED
// evidence covers it (flip stands, credit stays idempotent).
func mutateAddressed(t *testing.T, dir string) {
	t.Helper()
	sigs := sigsOf(t, dir)
	if len(sigs) == 0 {
		t.Fatal("no sigs")
	}
	writeFiles(t, dir, map[string]string{
		".review/local-kb/review.json": fmt.Sprintf(
			`{"schema_version":"1","body":"> [!IMPORTANT]\n> Second pass with evidence.","approved":false,"comments":[],"triage":[{"sig":%q,"status":"addressed","rationale":"verified by hand with a long enough rationale here"}]}`,
			sigs[0]),
		".review/local-kb/verification.json": fmt.Sprintf(
			`[{"sig":%q,"verdict":"VERIFIED","resultHead":"aaaaaaa","verifyCmd":"make check"}]`, sigs[0]),
	})
}

// setupMany seeds many entries so two sigs share a short prefix.
func setupMany(t *testing.T, dir string) {
	t.Helper()
	comments := make([]any, 0, 24)
	for i := range 24 {
		comments = append(comments, map[string]any{
			"path": "notes.txt", "line": (i%4)*10 + 3, "side": "RIGHT",
			"body":  fmt.Sprintf("Comment %d body with enough substance.", i),
			"rigor": "walked",
		})
	}
	doc := map[string]any{
		"schema_version": "1",
		"body":           "> [!IMPORTANT]\n> Many findings here.",
		"approved":       false,
		"comments":       comments,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{
		".review/local-kb/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		".review/local-kb/full.diff":   genDiff(4),
		".review/local-kb/review.json": string(raw),
	})
}

// mutateAmbiguous claims the first shared sig prefix (matches 2+ entries).
func mutateAmbiguous(t *testing.T, dir string) {
	t.Helper()
	prefix := sharedPrefix(t, dir)
	writeFiles(t, dir, map[string]string{
		".review/local-kb/review.json":       `{"schema_version":"1","body":"> [!IMPORTANT]\n> Second pass with evidence.","approved":false,"comments":[]}`,
		".review/local-kb/verification.json": fmt.Sprintf(`[{"sig":%q,"verdict":"VERIFIED","resultHead":"aaaaaaa","verifyCmd":"make check"}]`, prefix),
	})
}

// TestParityVerifyMatrix checks the full verification-claim matrix (I6).
func TestParityVerifyMatrix(t *testing.T) {
	for _, leg := range verifyLegs() {
		sequenced(t, leg.name, leg.setup, leg.mutate, "--json")
	}
}

// kbLeg is one KB end-to-end leg: phase 1 records, phase 2 mutates.
type kbLeg struct {
	setup  func(t *testing.T, dir string)
	mutate func(t *testing.T, dir string)
	name   string
}

// mutateVerifyFirstSig applies a VERIFIED claim for the first phase-1 sig.
func mutateVerifyFirstSig(t *testing.T, dir string) {
	t.Helper()
	sigs := sigsOf(t, dir)
	if len(sigs) == 0 {
		t.Fatal("no sigs")
	}
	claimMutate(t, dir, fmt.Sprintf(
		`[{"sig":%q,"verdict":"VERIFIED","resultHead":"aaaaaaa","verifyCmd":"make check"}]`, sigs[0]))
}

// mutateFalsePositive triages the first sig false-positive.
func mutateFalsePositive(t *testing.T, dir string) {
	t.Helper()
	sigs := sigsOf(t, dir)
	if len(sigs) == 0 {
		t.Fatal("no sigs")
	}
	writeFiles(t, dir, map[string]string{
		".review/local-kb/review.json": fmt.Sprintf(
			`{"schema_version":"1","body":"> [!IMPORTANT]\n> Second pass with evidence.","approved":false,"comments":[],"triage":[{"sig":%q,"status":"false-positive","rationale":"rule misfires on test fixtures with this rationale here"}]}`,
			sigs[0]),
	})
}

// citedSetup seeds a one-comment bundle with the given kb store lines.
func citedSetup(kbLines ...string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		kbBundle(t, dir, kbReview(`["rec-rule"]`), kbLines)
	}
}

// kbLegs lists the KB end-to-end legs (credit, idempotency, counters).
func kbLegs() []kbLeg {
	active := `{"id":"rec-rule","status":"active"}`
	return []kbLeg{
		{citedSetup(active), mutateFalsePositive, "kb/false-positive"},
		{citedSetup(active), mutateVerifyFirstSig, "kb/joins-idempotent"},
		{citedSetup(`{"id":"rec-rule","status":"active","hits":2,"false_hits":1,"judged":5}`), mutateVerifyFirstSig, "kb/counter-adds"},
		{citedSetup(`{"id":"rec-rule","status":"active","hits":"3","judged":"5"}`), mutateVerifyFirstSig, "kb/string-counters"},
	}
}

// TestParityKBMatrix checks the KB end-to-end legs: false-positive credit,
// joins idempotency, counter arithmetic, and invalid policy values.
func TestParityKBMatrix(t *testing.T) {
	for _, leg := range kbLegs() {
		sequenced(t, leg.name, leg.setup, leg.mutate, "--json")
	}
	// Invalid policy file values warn and fall back on both sides.
	runBoth(t, "kb/bad-policy-file", "local-kb", func(t *testing.T, dir string) {
		t.Helper()
		kbBundle(t, dir, kbReview(`["rec-rule"]`), []string{`{"id":"rec-rule","status":"active"}`})
		writeFiles(t, dir, map[string]string{
			".review/kb-policy": "MIN_JUDGED=abc\nMAX_FP_RATE=zzz\n",
		})
	}, "--json")
	// Invalid policy env knobs warn and fall back on both sides.
	runBothEnv(t, "kb/bad-policy-env", "local-kb", func(t *testing.T, dir string) {
		t.Helper()
		kbBundle(t, dir, kbReview(`["rec-rule"]`), []string{`{"id":"rec-rule","status":"active"}`})
	}, map[string]string{"REVIEW_KB_MIN_JUDGED": "abc", "REVIEW_KB_MAX_FP_RATE": "2"}, "--json")
}

// stripSigVersion removes sig_version from every stored entry (legacy shape).
func stripSigVersion(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".review", "local-kb", "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if m, ok := e.(map[string]any); ok {
			delete(m, "sig_version")
			m["x-custom"] = map[string]any{"nested": []any{1, 2}}
		}
	}
	out, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{".review/local-kb/findings.json": string(out)})
}

// TestParityLegacyMerge checks legacy stores merge identically: missing
// sig_version backfills, unknown keys pass through.
func TestParityLegacyMerge(t *testing.T) {
	sequenced(t, "legacy/backfill-passthrough", func(t *testing.T, dir string) {
		t.Helper()
		kbBundle(t, dir, kbReview(``), nil)
	}, func(t *testing.T, dir string) {
		t.Helper()
		stripSigVersion(t, dir)
		writeFiles(t, dir, map[string]string{
			".review/local-kb/review.json": kbReview(``),
		})
	}, "--json")
}

// TestParityIncrementalStill checks a delta that touches other paths reopens
// nothing (the auto-reopen negative leg).
func TestParityIncrementalStill(t *testing.T) {
	sequenced(t, "incremental/non-touching", func(t *testing.T, dir string) {
		t.Helper()
		kbBundle(t, dir, kbReview(``), nil)
	}, func(t *testing.T, dir string) {
		t.Helper()
		entries := readFindings(t, dir)
		if !flipToAddressed(entries) {
			t.Fatal("no notes.txt entry")
		}
		writeFiles(t, dir, map[string]string{
			".review/local-kb/findings.json":    marshalJSON(t, entries),
			".review/local-kb/incremental.diff": "diff --git a/other.txt b/other.txt\nindex 3333333..4444444 100644\n--- a/other.txt\n+++ b/other.txt\n@@ -1,2 +1,2 @@\n a\n-b\n+c\n",
			".review/local-kb/review.json":      kbReview(``),
		})
	}, "--json")
}
