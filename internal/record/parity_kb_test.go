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
		".review/local-kb/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		".review/local-kb/full.diff":   genDiff(4),
		".review/local-kb/review.json": review,
	}
	writeFiles(t, dir, files)
	if len(kbLines) > 0 {
		var sb strings.Builder
		for _, ln := range kbLines {
			sb.WriteString(ln + "\n")
		}
		content := sb.String()
		writeFiles(t, dir, map[string]string{".review/kb.jsonl": content})
	}
}

func kbReview(cite string) string {
	kbFrag := ""
	if cite != "" {
		kbFrag = `,"kb":` + cite
	}
	return `{"schema_version":"1","body":"> [!IMPORTANT]\n> KB record check.","approved":false,"comments":[{"path":"notes.txt","line":2,"side":"RIGHT","body":"Cited finding here.","rigor":"walked","severity":"important"` + kbFrag + `}]}`
}

// TestParityKBCitations checks KB citations attach identically.
func TestParityKBCitations(t *testing.T) {
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

// sequenced runs phase 1 on both sides, applies mutate to both bundles,
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
		if a.status != 0 || b.status != 0 {
			t.Fatalf("phase 1 failed: go=%d go=%d\n%s\n%s", a.status, b.status, a.stderr, b.stderr)
		}
		mutate(t, dirA)
		mutate(t, dirB)
		compareRuns(t, name+"/rereview", dirA, dirB, sequencedSlug, args)
	})
}

func sigsOf(t *testing.T, dir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".review", sequencedSlug, "findings.json"))
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

// TestParityClaimsSequence checks verify-claim re-reviews match.
func TestParityClaimsSequence(t *testing.T) {
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
			".review/local-kb/review.json": marshalJSON(t, doc),
			".review/local-kb/verification.json": fmt.Sprintf(
				`[{"sig":"%s","verdict":"VERIFIED","resultHead":"aaaaaaa","verifyCmd":"true"},{"sig":"deadbeef","verdict":"VERIFIED","resultHead":"aaaaaaa","verifyCmd":"true"},{"sig":"%s","verdict":"NOT VERIFIED","resultHead":"aaaaaaa","verifyCmd":"make check"}]`,
				sigs[0], sigs[0]),
		})
	}, "--json")
}

// TestParityUnciteSequence checks rule retirement matches.
func TestParityUnciteSequence(t *testing.T) {
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
				".review/local-kb/review.json": kbReview(tc.cite),
			})
		}, "--json")
	}
}

// TestParityBadTriageStatus checks bad triage statuses refuse identically.
func TestParityBadTriageStatus(t *testing.T) {
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
			".review/local-kb/review.json": marshalJSON(t, doc),
		})
	}, "--json")
}

// TestParityIncrementalReopen checks delta-touch reopening matches.
func TestParityIncrementalReopen(t *testing.T) {
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
			".review/local-kb/findings.json":    marshalJSON(t, entries),
			".review/local-kb/incremental.diff": "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,2 +1,2 @@\n a\n-b\n+c\n",
			".review/local-kb/review.json":      kbReview(``),
		})
	}, "--json")
}

// readFindings decodes the bundle's findings.json.
func readFindings(t *testing.T, dir string) []any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".review", "local-kb", "findings.json"))
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

// TestParityOverrides checks fragment overrides behave identically.
func TestParityOverrides(t *testing.T) {
	good := map[string]string{
		".review/local-ov/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}`,
		".review/local-ov/full.diff":   genDiff(2),
		".review/local-ov/review.json": `{"schema_version":"1","body":"> ✅ marker plus enough length to pass.","approved":false,"comments":[]}`,
	}
	goldenBoth(t, "overrides/marker-fragment", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{".review/overrides/preamble.md": "> ✅ not allowed here\n"})
	})
	goldenBoth(t, "overrides/unknown-field", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{".review/overrides/footer-order.md": "bogus\n"})
	})
	goldenBoth(t, "overrides/bad-threshold", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{".review/overrides/walkthrough-threshold.md": "frobnicate: 3\n"})
	})
	goldenBoth(t, "overrides/multiline-attribution", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{".review/overrides/attribution.md": "line one\nline two\n"})
	})
	goldenBoth(t, "overrides/bad-placeholder", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{".review/overrides/preamble.md": "See {frobnicate} for details.\n"})
	})
	goldenBoth(t, "overrides/valid", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, good)
		writeFiles(t, dir, map[string]string{
			".review/overrides/footer-order.md":          "link\ncost\n",
			".review/overrides/walkthrough-threshold.md": "files: 3\n",
			".review/overrides/attribution.md":           "Reviewed with {version} {tier}.",
		})
	})
}
