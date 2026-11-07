package record_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func kbBundle(t *testing.T, dir, review string, kbLines []string) {
	t.Helper()
	files := map[string]string{
		".verdict/state/local-kb/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		".verdict/state/local-kb/full.diff":   genDiff(4),
		".verdict/state/local-kb/review.json": review,
	}
	writeFiles(t, dir, files)
	if len(kbLines) > 0 {
		var sb strings.Builder
		for _, ln := range kbLines {
			sb.WriteString(ln + "\n")
		}
		content := sb.String()
		writeFiles(t, dir, map[string]string{".verdict/kb.jsonl": content})
	}
}

func kbReview(cite string) string {
	kbFrag := ""
	if cite != "" {
		kbFrag = `,"kb":` + cite
	}
	return `{"schema_version":"1","body":"> [!IMPORTANT]\n> KB record check.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Cited finding here.","rigor":"walked","severity":"important"` + kbFrag + `}]}`
}

// TestGoldenKBCitations checks KB citations attach identically.
func TestGoldenKBCitations(t *testing.T) {
	active := `{"id":"rec-rule","status":"active"}`
	retired := `{"id":"old-rule","status":"retired"}`
	cases := []struct {
		name string
		cite string
		kb   []string
	}{
		{"valid", `["rec-rule"]`, []string{active}},
		{"unknown", `["nope-missing"]`, []string{active}},
		{"malformed-id", `[123]`, []string{active}},
		{"bad-shape", `"nope"`, []string{active}},
		{"retired", `["old-rule"]`, []string{active, retired}},
		{"absent", ``, []string{active}},
		{"null", `null`, []string{active}},
		{"empty", `[]`, []string{active}},
		{"no-store", `["rec-rule"]`, nil},
		{"broken-store", `["rec-rule"]`, []string{`{bad json`}},
		{"truncated", `["rec-rule","a","b","c","d","e","f"]`, []string{active}},
		{"bad-id-shape", `["BAD ID!"]`, []string{active}},
		{"duplicate", `["rec-rule","rec-rule"]`, []string{active}},
	}
	for _, tc := range cases {
		goldenBoth(t, "kb/"+tc.name, sequencedSlug, func(t *testing.T, dir string) {
			t.Helper()
			kbBundle(t, dir, kbReview(tc.cite), tc.kb)
		})
	}
}

// TestKBCitationIsLiteralAndIdempotent pins what the real .verdict store
// earns: the active rec-rule attaches to the finding exactly once, it never
// becomes a fabricated credit stamp, an unknown rule attaches nothing, and a
// second record of the same review neither duplicates the citation nor grows
// the history. Runs before any golden refresh, so the claim rests on literal
// assertions rather than a repinned file.
func TestKBCitationIsLiteralAndIdempotent(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	kbBundle(t, dir, kbReview(`["rec-rule"]`), []string{`{"id":"rec-rule","status":"active"}`})
	findings := filepath.Join(dir, ".verdict/state/local-kb/findings.json")
	first := runOne(t, dir, home, "record", "acme/app", "local-kb")
	if first.Status != 0 {
		t.Fatalf("record status = %d, want 0 (stderr %s)", first.Status, first.Stderr)
	}
	raw, err := os.ReadFile(findings)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if n := strings.Count(body, `"rec-rule"`); n != 1 {
		t.Fatalf("rec-rule appears %d times, want exactly one attached citation: %s", n, body)
	}
	if !strings.Contains(body, `"kb"`) {
		t.Fatalf("the active rule attached no kb citation: %s", body)
	}
	if strings.Contains(body, `"kb_credit"`) {
		t.Fatalf("a first record fabricated a credit stamp: %s", body)
	}
	assertRerecordStable(t, dir, home, findings, body)
}

// assertRerecordStable records the same review again and requires the store to
// keep exactly one citation and the same history length.
func assertRerecordStable(t *testing.T, dir, home, findings, first string) {
	t.Helper()
	if second := runOne(t, dir, home, "record", "acme/app", "local-kb"); second.Status != 0 {
		t.Fatalf("second record status = %d, want 0", second.Status)
	}
	again, err := os.ReadFile(findings)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(again), `"rec-rule"`); got != 1 {
		t.Fatalf("re-record left %d rec-rule citations, want exactly 1", got)
	}
	if got, want := strings.Count(string(again), `"event"`), strings.Count(first, `"event"`); got != want {
		t.Fatalf("re-record changed history entries %d, want %d", got, want)
	}
}

// TestUnknownKBAttachesNothing proves the same store refuses a rule it does not
// hold: a missing id leaves the finding without any kb citation.
func TestUnknownKBAttachesNothing(t *testing.T) {
	dir, home := t.TempDir(), t.TempDir()
	kbBundle(t, dir, kbReview(`["nope-missing"]`), []string{`{"id":"rec-rule","status":"active"}`})
	if out := runOne(t, dir, home, "record", "acme/app", "local-kb"); out.Status != 0 {
		t.Fatalf("record status = %d, want 0 (stderr %s)", out.Status, out.Stderr)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict/state/local-kb/findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "nope-missing") {
		t.Fatalf("an unknown rule leaked into findings: %s", raw)
	}
}

// sequenced runs phase 1 twice over, applies mutate to both bundles,
// then compares phase 2 — the shape re-review, uncite, and credit flows need.
// sequencedSlug is the bundle slug for the claims-sequence cases.
const sequencedSlug = "local-kb"

func sequenced(t *testing.T, name string, setup, mutate func(t *testing.T, dir string), args ...string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Helper()
		dirA := t.TempDir()
		dirB := t.TempDir()
		setup(t, dirA)
		setup(t, dirB)
		full := append([]string{"record", "acme/app", sequencedSlug}, args...)
		a := runOne(t, dirA, t.TempDir(), full...)
		b := runOne(t, dirB, t.TempDir(), full...)
		if a.Status != 0 || b.Status != 0 {
			t.Fatalf("phase 1 failed: go=%d/%d\n%s\n%s", a.Status, b.Status, a.Stderr, b.Stderr)
		}
		mutate(t, dirA)
		mutate(t, dirB)
		compareRuns(t, name+"/rereview", dirA, dirB, sequencedSlug, args)
	})
}

func sigsOf(t *testing.T, dir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict", "state", sequencedSlug, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	var sigs []string
	for _, e := range entries {
		if s, ok := sigOfEntry(e); ok {
			sigs = append(sigs, s)
		}
	}
	return sigs
}

// sigOfEntry reads one decoded finding's string sig.
func sigOfEntry(e any) (string, bool) {
	m, ok := e.(map[string]any)
	if !ok {
		return "", false
	}
	s, ok := m["sig"].(string)
	return s, ok
}

// TestGoldenClaimsSequence checks verify-claim re-reviews match.
func TestGoldenClaimsSequence(t *testing.T) {
	active := `{"id":"rec-rule","status":"active"}`
	sequenced(t, "claims/verify-triage-resolved", func(t *testing.T, dir string) {
		t.Helper()
		kbBundle(t, dir, kbReview(`["rec-rule"]`), []string{active})
	}, func(t *testing.T, dir string) {
		t.Helper()
		sigs := sigsOf(t, dir)
		if len(sigs) == 0 {
			t.Fatal("no sigs")
		}
		doc := map[string]any{
			"schema_version": "1",
			"body":           "> [!IMPORTANT]\n> Second pass with evidence.",
			"approved":       false,
			"comments":       []any{},
			"resolved":       []any{sigs[0], "deadbeef"},
			"triage": []any{
				map[string]any{"sig": sigs[0], "status": "addressed", "rationale": "agent triage with a long enough rationale here for sure"},
				map[string]any{"sig": "deadbeef", "status": "open", "rationale": "unknown sig triage with a long enough rationale here ok"},
				map[string]any{"sig": sigs[0], "status": "bogus", "rationale": "this status is unknown and must refuse the run here ok"},
			},
		}
		_ = doc
		// The bogus triage status refuses the run; drop it for the passing leg.
		doc["triage"] = []any{
			map[string]any{"sig": sigs[0], "status": "addressed", "rationale": "agent triage with a long enough rationale here for sure"},
			map[string]any{"sig": "deadbeef", "status": "open", "rationale": "unknown sig triage with a long enough rationale here ok"},
			map[string]any{"sig": sigs[0], "status": "open", "rationale": "short"},
		}
		writeFiles(t, dir, map[string]string{
			".verdict/state/local-kb/review.json": marshalJSON(t, doc),
			".verdict/state/local-kb/verification.json": fmt.Sprintf(
				`[{"sig":"%s","verdict":"VERIFIED","resultHead":"aaaaaaa","verifyCmd":"true"},{"sig":"deadbeef","verdict":"VERIFIED","resultHead":"aaaaaaa","verifyCmd":"true"},{"sig":"%s","verdict":"NOT VERIFIED","resultHead":"aaaaaaa","verifyCmd":"make check"}]`,
				sigs[0], sigs[0]),
		})
	}, "--json")
}

// TestGoldenUnciteSequence checks rule retirement matches.
func TestGoldenUnciteSequence(t *testing.T) {
	active := `{"id":"uncite-r","status":"active"}`
	for _, tc := range []struct {
		name string
		cite string
	}{
		{"absent-keeps", ``},
		{"null-keeps", `null`},
		{"explicit-clears", `[]`},
		{"malformed-clears", `["nope-missing"]`},
	} {
		sequenced(t, "uncite/"+tc.name, func(t *testing.T, dir string) {
			t.Helper()
			kbBundle(t, dir, kbReview(`["uncite-r"]`), []string{active})
		}, func(t *testing.T, dir string) {
			t.Helper()
			writeFiles(t, dir, map[string]string{
				".verdict/state/local-kb/review.json": kbReview(tc.cite),
			})
		}, "--json")
	}
}

// TestGoldenBadTriageStatus checks bad triage statuses refuse identically.
func TestGoldenBadTriageStatus(t *testing.T) {
	sequenced(t, "claims/bad-triage-status", func(t *testing.T, dir string) {
		t.Helper()
		kbBundle(t, dir, kbReview(`["rec-rule"]`), []string{`{"id":"rec-rule","status":"active"}`})
	}, func(t *testing.T, dir string) {
		t.Helper()
		sigs := sigsOf(t, dir)
		if len(sigs) == 0 {
			t.Fatal("no sigs")
		}
		doc := map[string]any{
			"schema_version": "1",
			"body":           "> [!IMPORTANT]\n> Bad status leg.",
			"approved":       false,
			"comments":       []any{},
			"triage": []any{
				map[string]any{"sig": sigs[0], "status": "bogus", "rationale": "this status is unknown and must refuse the run here ok"},
			},
		}
		writeFiles(t, dir, map[string]string{
			".verdict/state/local-kb/review.json": marshalJSON(t, doc),
		})
	}, "--json")
}

// TestGoldenIncrementalReopen checks delta-touch reopening matches.
func TestGoldenIncrementalReopen(t *testing.T) {
	sequenced(t, "incremental/reopen", func(t *testing.T, dir string) {
		t.Helper()
		kbBundle(t, dir, kbReview(``), nil)
	}, func(t *testing.T, dir string) {
		t.Helper()
		// Flip the stored entry to addressed with an old lastSeen, then touch
		// its path via incremental.diff so the run reopens it.
		entries := readFindings(t, dir)
		if !flipToAddressed(entries) {
			t.Fatal("no notes.txt entry")
		}
		writeFiles(t, dir, map[string]string{
			".verdict/state/local-kb/findings.json":    marshalJSON(t, entries),
			".verdict/state/local-kb/incremental.diff": "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,2 +1,2 @@\n a\n-b\n+c\n",
			".verdict/state/local-kb/review.json":      kbReview(``),
		})
	}, "--json")
}

// readFindings decodes the bundle's findings.json.
func readFindings(t *testing.T, dir string) []any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".verdict", "state", "local-kb", "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

// flipToAddressed marks the notes.txt entry addressed with a stale lastSeen.
func flipToAddressed(entries []any) bool {
	for _, e := range entries {
		if m, ok := e.(map[string]any); ok && m["path"] == "notes.txt" {
			m["status"] = "addressed"
			m["lastSeen"] = "bbbbbbb"
			return true
		}
	}
	return false
}

// TestGoldenOverrides checks fragment overrides behave identically.
func TestGoldenOverrides(t *testing.T) {
	good := map[string]string{
		".verdict/state/local-ov/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		".verdict/state/local-ov/full.diff":   genDiff(2),
		".verdict/state/local-ov/review.json": `{"schema_version":"1","body":"> ✅ marker plus enough length to pass.","approved":false,"comments":[]}`,
	}
	goldenBoth(t, "overrides/marker-fragment", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{".verdict/overrides/preamble.md": "> ✅ not allowed here\n"})
	})
	goldenBoth(t, "overrides/unknown-field", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{".verdict/overrides/footer-order.md": "bogus\n"})
	})
	goldenBoth(t, "overrides/bad-threshold", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{".verdict/overrides/walkthrough-threshold.md": "frobnicate: 3\n"})
	})
	goldenBoth(t, "overrides/multiline-attribution", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{".verdict/overrides/attribution.md": "line one\nline two\n"})
	})
	goldenBoth(t, "overrides/bad-placeholder", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{".verdict/overrides/preamble.md": "See {frobnicate} for details.\n"})
	})
	goldenBoth(t, "overrides/valid", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{
			".verdict/overrides/footer-order.md":          "link\ncost\n",
			".verdict/overrides/walkthrough-threshold.md": "files: 3\n",
			".verdict/overrides/attribution.md":           "Reviewed with {version} {tier}.",
		})
	})
}
