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
// keeping comments empty so the second pass merges against phase-1 entries,
// beside the passing verify receipt at the bundle head that a VERIFIED claim
// needs before it flips.
func claimMutate(t *testing.T, dir string, claims string) {
	t.Helper()
	writeFiles(t, dir, map[string]string{
		".verdict/state/local-kb/review.json":       `{"schema_version":"1","body":"> [!IMPORTANT]\n> Second pass with evidence.","approved":false,"comments":[]}`,
		".verdict/state/local-kb/verification.json": claims,
		receiptPath: kbReceipt("make check"),
	})
}

// kbTree is the content tree every local-kb fixture's source.json records.
const kbTree = "7777777777777777777777777777777777777777"

// receiptPath is where `verdict verify` writes its tool-only receipt.
const receiptPath = ".verdict/state/local-kb/verify-receipt.json"

// toolReceipt renders a verify-receipt.json fixture: what `verdict verify`
// writes after running cmd on the content tree ("" when it could not
// snapshot one).
func toolReceipt(cmd, tree string, exit int, timedOut bool) string {
	treeField := ""
	if tree != "" {
		treeField = `"tree":"` + tree + `",`
	}
	return fmt.Sprintf(`{"schema_version":"2","command":%q,%s"at":"2026-01-01T00:00:00Z","exit":%d,"timed_out":%t}`,
		cmd, treeField, exit, timedOut)
}

// kbReceipt is the receipt a passing `verdict verify` run of cmd leaves on
// kbTree.
func kbReceipt(cmd string) string { return toolReceipt(cmd, kbTree, 0, false) }

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

// TestGoldenVerifyMatrix checks the full verification-claim matrix.
// verifyLeg is one verification-claim matrix leg: phase 1 records, phase 2
// applies mutate, then the two runs are compared.
type verifyLeg struct {
	setup  func(t *testing.T, dir string)
	mutate func(t *testing.T, dir string)
	name   string
}

// claimAll builds one verification claim per phase-1 sig.
func claimAll(t *testing.T, dir, verdict, cmd string) string {
	t.Helper()
	return claimsOf(t, dir, func(sig string) string {
		return fmt.Sprintf(`{"sig":%q,"verdict":%q,"verifyCmd":%q}`, sig, verdict, cmd)
	})
}

// mutateClaims replaces phase 2 with empty comments plus built claims.
func mutateClaims(verdict, cmd string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		claimMutate(t, dir, claimAll(t, dir, verdict, cmd))
	}
}

// verifyLegs lists the verification-claim matrix legs.
func verifyLegs() []verifyLeg {
	return []verifyLeg{
		{claimsSetup, mutateClaims("VERIFIED", ""), "verify/empty-cmd"},
		{claimsSetup, mutateClaims("INCONCLUSIVE", "make check"), "verify/inconclusive"},
		{claimsSetup, mutateReceiptClaims(receiptPath, toolReceipt("make check", kbTree, 1, false)), "verify/receipt-exit"},
		{claimsSetup, mutateReceiptClaims(receiptPath, toolReceipt("make check", kbTree, 0, true)), "verify/receipt-timeout"},
		{claimsSetup, mutateReceiptClaims(receiptPath, toolReceipt("make check", "cccccccccccccccccccccccccccccccccccccccc", 0, false)), "verify/receipt-stale"},
		{claimsSetup, mutateReceiptClaims(receiptPath, toolReceipt("true", kbTree, 0, false)), "verify/receipt-command"},
		// The outcome an agent can write into patch.json, the file it is told
		// to write, is no receipt: the forged pass must not flip.
		{claimsSetup, mutateReceiptClaims(".verdict/state/local-kb/patch.json",
			`{"verifyCmd":"make check","verifyExit":0,"timedOut":false,"tree":"`+kbTree+`"}`), "verify/receipt-forged-patch"},
		{claimsSetup, mutateAddressed, "verify/on-addressed"},
		{setupMany, mutateAmbiguous, "verify/ambiguous-prefix"},
	}
}

// mutateReceiptClaims applies VERIFIED-for-every-sig and writes body at path
// in place of the passing receipt.
func mutateReceiptClaims(path, body string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		claimMutate(t, dir, claimAll(t, dir, "VERIFIED", "make check"))
		if err := os.Remove(filepath.Join(dir, receiptPath)); err != nil {
			t.Fatal(err)
		}
		writeFiles(t, dir, map[string]string{path: body})
	}
}

// mutateAddressed claims the first sig addressed by triage beside a backed
// VERIFIED claim: record refuses the review, because no triage rationale can
// claim addressed — only the VERIFIED claim and its receipt can.
func mutateAddressed(t *testing.T, dir string) {
	t.Helper()
	sigs := sigsOf(t, dir)
	if len(sigs) == 0 {
		t.Fatal("no sigs")
	}
	writeFiles(t, dir, map[string]string{
		".verdict/state/local-kb/review.json": fmt.Sprintf(
			`{"schema_version":"1","body":"> [!IMPORTANT]\n> Second pass with evidence.","approved":false,"comments":[],"triage":[{"sig":%q,"status":"addressed","rationale":"verified by hand with a long enough rationale here"}]}`,
			sigs[0]),
		".verdict/state/local-kb/verification.json": fmt.Sprintf(
			`[{"sig":%q,"verdict":"VERIFIED","verifyCmd":"make check"}]`, sigs[0]),
		receiptPath: kbReceipt("make check"),
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
		".verdict/state/local-kb/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work","content_tree":"7777777777777777777777777777777777777777"}`,
		".verdict/state/local-kb/full.diff":   genDiff(4),
		".verdict/state/local-kb/review.json": string(raw),
	})
}

// mutateAmbiguous claims the first shared sig prefix (matches 2+ entries).
func mutateAmbiguous(t *testing.T, dir string) {
	t.Helper()
	prefix := sharedPrefix(t, dir)
	writeFiles(t, dir, map[string]string{
		".verdict/state/local-kb/review.json":       `{"schema_version":"1","body":"> [!IMPORTANT]\n> Second pass with evidence.","approved":false,"comments":[]}`,
		".verdict/state/local-kb/verification.json": fmt.Sprintf(`[{"sig":%q,"verdict":"VERIFIED","verifyCmd":"make check"}]`, prefix),
		receiptPath: kbReceipt("make check"),
	})
}

// TestGoldenVerifyMatrix checks the full verification-claim matrix.
func TestGoldenVerifyMatrix(t *testing.T) {
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
		`[{"sig":%q,"verdict":"VERIFIED","verifyCmd":"make check"}]`, sigs[0]))
}

// mutateFalsePositive triages the first sig false-positive.
func mutateFalsePositive(t *testing.T, dir string) {
	t.Helper()
	sigs := sigsOf(t, dir)
	if len(sigs) == 0 {
		t.Fatal("no sigs")
	}
	writeFiles(t, dir, map[string]string{
		".verdict/state/local-kb/review.json": fmt.Sprintf(
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

// TestGoldenKBMatrix checks the KB end-to-end legs: false-positive credit,
// joins idempotency, counter arithmetic, and invalid policy values.
func TestGoldenKBMatrix(t *testing.T) {
	for _, leg := range kbLegs() {
		sequenced(t, leg.name, leg.setup, leg.mutate, "--json")
	}
	// Invalid policy env knobs cannot override the typed defaults the bundle
	// carries, so the record run stays warning-free. The EnvDefaults fallback
	// for bundles without typed evidence is unit-covered in internal/config;
	// the legacy .review/kb-policy file leg was deleted with PolicyFile itself.
	goldenBothEnv(t, "kb/bad-policy-env", "local-kb", func(t *testing.T, dir string) {
		t.Helper()
		kbBundle(t, dir, kbReview(`["rec-rule"]`), []string{`{"id":"rec-rule","status":"active"}`})
	}, map[string]string{"REVIEW_KB_MIN_JUDGED": "abc", "REVIEW_KB_MAX_FP_RATE": "2"}, "--json")
}

// stripSigVersion removes sig_version from every stored entry (legacy shape).
func stripSigVersion(t *testing.T, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict", "state", "local-kb", "findings.json"))
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
	writeFiles(t, dir, map[string]string{".verdict/state/local-kb/findings.json": string(out)})
}

// TestGoldenLegacyMerge checks legacy stores merge identically: missing
// sig_version backfills, unknown keys pass through.
func TestGoldenLegacyMerge(t *testing.T) {
	sequenced(t, "legacy/backfill-passthrough", func(t *testing.T, dir string) {
		t.Helper()
		kbBundle(t, dir, kbReview(``), nil)
	}, func(t *testing.T, dir string) {
		t.Helper()
		stripSigVersion(t, dir)
		writeFiles(t, dir, map[string]string{
			".verdict/state/local-kb/review.json": kbReview(``),
		})
	}, "--json")
}

// TestGoldenIncrementalStill checks a delta that touches other paths reopens
// nothing (the auto-reopen negative leg).
func TestGoldenIncrementalStill(t *testing.T) {
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
			".verdict/state/local-kb/findings.json":    marshalJSON(t, entries),
			".verdict/state/local-kb/incremental.diff": "diff --git a/other.txt b/other.txt\nindex 3333333..4444444 100644\n--- a/other.txt\n+++ b/other.txt\n@@ -1,2 +1,2 @@\n a\n-b\n+c\n",
			".verdict/state/local-kb/review.json":      kbReview(``),
		})
	}, "--json")
}
