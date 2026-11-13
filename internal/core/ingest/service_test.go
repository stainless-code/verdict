package ingestcore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
)

func fixedRuntime() *core.Runtime {
	return &core.Runtime{
		Now: func() time.Time {
			return time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
		},
	}
}

func setupBundle(t *testing.T, pr, headSHA, diff string) (repoRoot, dir string) {
	t.Helper()
	repoRoot = t.TempDir()
	dir = filepath.Join(repoRoot, ".verdict", "state", pr)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PR.json"), []byte(`{"headRefOid":"`+headSHA+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoRoot, dir
}

func loadStoreEntries(t *testing.T, dir string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "findings.json"))
	if err != nil {
		t.Fatalf("read findings.json: %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("unmarshal findings.json: %v", err)
	}
	return entries
}

const sampleDiff = "" +
	"diff --git a/pkg/auth.go b/pkg/auth.go\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/pkg/auth.go\n" +
	"+++ b/pkg/auth.go\n" +
	"@@ -10,0 +11,4 @@ package auth\n" +
	"+func Verify() {\n" +
	"+  checkToken()\n" +
	"+}\n" +
	"diff --git a/vendor/gen.go b/vendor/gen.go\n" +
	"new file mode 100644\n" +
	"--- /dev/null\n" +
	"+++ b/vendor/gen.go\n" +
	"@@ -0,0 +1,2 @@\n" +
	"+package gen\n"

func TestRun_MappingAnchoringExclusionsAndIdempotency(t *testing.T) {
	repoRoot, dir := setupBundle(t, "42", "abc1234567890", sampleDiff)
	excluded := `{
  "schema_version": "1",
  "excluded": [
    {"path": "vendor/gen.go", "rule_id": "builtin:vendor", "bytes": 120}
  ]
}
`
	if err := os.WriteFile(filepath.Join(dir, "excluded.json"), []byte(excluded), 0o644); err != nil {
		t.Fatal(err)
	}

	sarifBody := `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {
        "driver": {
          "name": "semgrep",
          "rules": [
            {
              "id": "go.jwt.none",
              "defaultConfiguration": {"level": "warning"},
              "properties": {"security-severity": 8.5}
            },
            {
              "id": "go.style.nit",
              "defaultConfiguration": {"level": "note"}
            }
          ]
        }
      },
      "results": [
        {
          "ruleId": "go.jwt.none",
          "message": {"text": "JWT signature skipped for @team"},
          "partialFingerprints": {"primaryLocationLineHash": "fp-jwt-1"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "pkg/auth.go"}, "region": {"startLine": 12}}}]
        },
        {
          "ruleId": "go.jwt.none",
          "message": {"text": "JWT signature skipped for @team"},
          "partialFingerprints": {"primaryLocationLineHash": "fp-jwt-1"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "pkg/auth.go"}, "region": {"startLine": 12}}}]
        },
        {
          "ruleId": "go.style.nit",
          "level": "warning",
          "message": {"text": "Out of diff context note"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "pkg/auth.go"}, "region": {"startLine": 99}}}]
        },
        {
          "ruleId": "go.vendor.rule",
          "level": "error",
          "message": {"text": "Generated vendor issue"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "vendor/gen.go"}, "region": {"startLine": 1}}}]
        },
        {
          "ruleId": "go.repo.policy",
          "level": "note",
          "message": {"text": "Missing SECURITY.md"}
        }
      ]
    }
  ]
}`
	sarifPath := filepath.Join(t.TempDir(), "scan.sarif")
	if err := os.WriteFile(sarifPath, []byte(sarifBody), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, ref := Run(context.Background(), &Request{
		ReadRequest: core.ReadRequest{
			Runtime:  fixedRuntime(),
			RepoRoot: repoRoot,
			Pr:       "42",
		},
		SARIF: sarifPath,
	})
	if ref != nil {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	if resp.Data.Ingested != 4 || resp.Data.Added != 4 || resp.Data.Updated != 0 || resp.Data.Unchanged != 0 {
		t.Fatalf("unexpected counts after first ingest: %+v", resp.Data)
	}
	if resp.Data.Anchored != 2 || resp.Data.Withheld != 1 || resp.Data.Open != 4 {
		t.Fatalf("unexpected anchored/withheld/open: %+v", resp.Data)
	}
	if len(resp.Data.Tools) != 1 || resp.Data.Tools[0] != "semgrep" {
		t.Fatalf("tools = %v, want [semgrep]", resp.Data.Tools)
	}

	entries := loadStoreEntries(t, dir)
	if len(entries) != 4 {
		t.Fatalf("findings count = %d, want 4", len(entries))
	}

	// Verify in-diff anchored finding (security-severity 8.5 -> critical, @team neutralized).
	e0 := entries[0]
	if e0["source"] != "tool" || e0["tool"] != "semgrep" || e0["rule_id"] != "go.jwt.none" || e0["fingerprint"] != "fp-jwt-1" {
		t.Fatalf("unexpected tool metadata on entry 0: %+v", e0)
	}
	if e0["severity"] != "critical" || e0["anchored"] != true || e0["inBounds"] != true {
		t.Fatalf("unexpected severity/anchor on entry 0: %+v", e0)
	}
	if e0["rigor"] != "said" || e0["rigor_source"] != "default" {
		t.Fatalf("unexpected rigor on entry 0: %+v", e0)
	}
	if !strings.Contains(e0["body"].(string), "@\u200bteam") {
		t.Fatalf("expected mention neutralization in body, got %q", e0["body"])
	}

	// Verify out-of-diff finding is unanchored and never posted inline.
	e1 := entries[1]
	if e1["anchored"] != false || e1["inBounds"] != false || e1["severity"] != "important" {
		t.Fatalf("unexpected out-of-diff entry 1: %+v", e1)
	}
	if e1["rigor_source"] == "verified" {
		t.Fatalf("tool finding must never get verified rigor: %+v", e1)
	}

	// Verify excluded path finding is kept with withheld: {source: "exclusion", ...}.
	e2 := entries[2]
	w, ok := e2["withheld"].(map[string]any)
	if !ok || w["source"] != "exclusion" || w["exclusion_id"] != "builtin:vendor" {
		t.Fatalf("expected exclusion withheld on entry 2, got %+v", e2)
	}

	// Verify locationless finding becomes section entry.
	e3 := entries[3]
	if e3["kind"] != "section" || e3["anchored"] != false || e3["severity"] != "informational" {
		t.Fatalf("unexpected section entry 3: %+v", e3)
	}

	// Mark entry 0 false-positive via ordered store edit to verify triage persists across re-ingest.
	rawStore, err := os.ReadFile(filepath.Join(dir, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jq.DecodeOrdered(rawStore)
	if err != nil {
		t.Fatal(err)
	}
	arr := doc.([]any)
	arr[0].(*jq.Obj).Set("status", "false-positive")
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(jq.PrettyOrdered(arr)), 0o644); err != nil {
		t.Fatal(err)
	}

	// Re-ingest same SARIF -> must be completely idempotent.
	resp2, ref := Run(context.Background(), &Request{
		ReadRequest: core.ReadRequest{
			Runtime:  fixedRuntime(),
			RepoRoot: repoRoot,
			Pr:       "42",
		},
		SARIF: sarifPath,
	})
	if ref != nil {
		t.Fatalf("re-ingest refusal: %+v", ref)
	}
	if resp2.Data.Added != 0 || resp2.Data.Updated != 0 || resp2.Data.Unchanged != 4 || resp2.Data.Open != 3 {
		t.Fatalf("re-ingest not idempotent: %+v", resp2.Data)
	}
	afterReingest := loadStoreEntries(t, dir)
	if len(afterReingest) != 4 || afterReingest[0]["status"] != "false-positive" {
		t.Fatalf("status or count drifted on re-ingest: len=%d status=%v", len(afterReingest), afterReingest[0]["status"])
	}

	// Changed message with same partialFingerprint updates existing entry in place without duplicating.
	changedSARIF := strings.Replace(sarifBody, "JWT signature skipped for @team", "JWT none algorithm accepted", 2)
	if err := os.WriteFile(sarifPath, []byte(changedSARIF), 0o644); err != nil {
		t.Fatal(err)
	}
	resp3, ref := Run(context.Background(), &Request{
		ReadRequest: core.ReadRequest{
			Runtime:  fixedRuntime(),
			RepoRoot: repoRoot,
			Pr:       "42",
		},
		SARIF: sarifPath,
	})
	if ref != nil {
		t.Fatalf("update ingest refusal: %+v", ref)
	}
	if resp3.Data.Added != 0 || resp3.Data.Updated != 1 || resp3.Data.Unchanged != 3 {
		t.Fatalf("unexpected counts on updated result: %+v", resp3.Data)
	}
	afterUpdate := loadStoreEntries(t, dir)
	if len(afterUpdate) != 4 || afterUpdate[0]["body"] != "JWT none algorithm accepted" || afterUpdate[0]["status"] != "false-positive" {
		t.Fatalf("updated entry mismatch: len=%d body=%v status=%v", len(afterUpdate), afterUpdate[0]["body"], afterUpdate[0]["status"])
	}
}

func TestRun_PreservesModelFindingOnVerdictSigMatchAndBackfillClampsVerified(t *testing.T) {
	repoRoot, dir := setupBundle(t, "43", "deadbeef1234", sampleDiff)
	// Seed store with one model finding (by sig) and one tool finding whose
	// legacy rigor_source was somehow "verified" (must clamp back to "default").
	seed := `[
  {
    "sig": "modelsig111",
    "sig_version": "1",
    "kind": "inline",
    "path": "pkg/auth.go",
    "line": 11,
    "side": "RIGHT",
    "anchored": true,
    "body": "Existing model finding",
    "kb": [],
    "inBounds": true,
    "rigor": "walked",
    "rigor_source": "model",
    "severity": "important",
    "status": "open",
    "firstSeen": "deadbee",
    "lastSeen": "deadbee",
    "source": "model",
    "history": [{"at": "2026-04-15T10:00:00Z", "event": "reported"}]
  },
  {
    "sig": "toolsig222",
    "sig_version": "1",
    "kind": "inline",
    "path": "pkg/auth.go",
    "line": 12,
    "side": "RIGHT",
    "anchored": true,
    "body": "Legacy tool finding",
    "kb": [],
    "inBounds": true,
    "rigor": "said",
    "rigor_source": "verified",
    "severity": "important",
    "status": "open",
    "firstSeen": "deadbee",
    "lastSeen": "deadbee",
    "source": "tool",
    "tool": "gosec",
    "rule_id": "G101",
    "fingerprint": "fp-g101",
    "history": [{"at": "2026-04-15T10:00:00Z", "event": "reported"}]
  }
]
`
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	sarifPayload := `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {"driver": {"name": "verdict"}},
      "results": [
        {
          "ruleId": "verdict/important",
          "level": "warning",
          "message": {"text": "Existing model finding"},
          "partialFingerprints": {"verdict/sig": "modelsig111"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "pkg/auth.go"}, "region": {"startLine": 11}}}]
        }
      ]
    },
    {
      "tool": {"driver": {"name": "gosec"}},
      "results": [
        {
          "ruleId": "G101",
          "level": "warning",
          "message": {"text": "Legacy tool finding"},
          "partialFingerprints": {"id": "fp-g101"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "pkg/auth.go"}, "region": {"startLine": 12}}}]
        }
      ]
    }
  ]
}`
	resp, ref := Run(context.Background(), &Request{
		ReadRequest: core.ReadRequest{Runtime: fixedRuntime(), RepoRoot: repoRoot, Pr: "43"},
		SARIFData:   []byte(sarifPayload),
		SARIFInline: true,
	})
	if ref != nil {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	if resp.Data.Added != 0 || resp.Data.Unchanged < 1 {
		t.Fatalf("expected dedupe against verdict/sig and fp, got %+v", resp.Data)
	}
	entries := loadStoreEntries(t, dir)
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0]["source"] != "model" || entries[0]["rigor"] != "walked" || entries[0]["rigor_source"] != "model" {
		t.Fatalf("model finding downgraded: %+v", entries[0])
	}
	if entries[1]["rigor_source"] != "default" {
		t.Fatalf("tool finding must clamp verified rigor_source to default, got %+v", entries[1])
	}

	// Direct check on BackfillEntry for tool entries with missing rigor_source.
	obj := jq.NewObj("source", "tool", "rigor", "walked")
	recordcore.BackfillEntry(obj)
	if rs, _ := obj.Get("rigor_source"); rs != "default" {
		t.Fatalf("BackfillEntry(tool) rigor_source = %v, want default", rs)
	}
}

func TestRun_UntrustedSecurityAndRefusals(t *testing.T) {
	validMinimal := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":[]}]}`

	t.Run("missing-sarif flag", func(t *testing.T) {
		repoRoot, _ := setupBundle(t, "50", "abc1234", sampleDiff)
		_, ref := Run(context.Background(), &Request{
			ReadRequest: core.ReadRequest{Runtime: fixedRuntime(), RepoRoot: repoRoot, Pr: "50"},
		})
		if ref == nil || ref.Code != "missing-sarif" {
			t.Fatalf("got %+v, want missing-sarif", ref)
		}
	})

	t.Run("missing-bundle and missing-diff", func(t *testing.T) {
		repoRoot := t.TempDir()
		_, ref := Run(context.Background(), &Request{
			ReadRequest: core.ReadRequest{Runtime: fixedRuntime(), RepoRoot: repoRoot, Pr: "999"},
			SARIFData:   []byte(validMinimal),
			SARIFInline: true,
		})
		if ref == nil || ref.Code != "missing-bundle" {
			t.Fatalf("got %+v, want missing-bundle", ref)
		}

		bundleDir := filepath.Join(repoRoot, ".verdict", "state", "999")
		if err := os.MkdirAll(bundleDir, 0o755); err != nil {
			t.Fatal(err)
		}
		_, ref = Run(context.Background(), &Request{
			ReadRequest: core.ReadRequest{Runtime: fixedRuntime(), RepoRoot: repoRoot, Pr: "999"},
			SARIFData:   []byte(validMinimal),
			SARIFInline: true,
		})
		if ref == nil || ref.Code != "missing-diff" {
			t.Fatalf("got %+v, want missing-diff", ref)
		}
	})

	t.Run("oversize sarif-too-large", func(t *testing.T) {
		repoRoot, _ := setupBundle(t, "51", "abc1234", sampleDiff)
		oversize := make([]byte, MaxSARIFBytes+1)
		copy(oversize, `{"version":"2.1.0","runs":[]}`)
		p := filepath.Join(t.TempDir(), "big.sarif")
		if err := os.WriteFile(p, oversize, 0o644); err != nil {
			t.Fatal(err)
		}
		_, ref := Run(context.Background(), &Request{
			ReadRequest: core.ReadRequest{Runtime: fixedRuntime(), RepoRoot: repoRoot, Pr: "51"},
			SARIF:       p,
		})
		if ref == nil || ref.Code != "sarif-too-large" {
			t.Fatalf("got %+v, want sarif-too-large", ref)
		}

		_, ref = Run(context.Background(), &Request{
			ReadRequest: core.ReadRequest{Runtime: fixedRuntime(), RepoRoot: repoRoot, Pr: "51"},
			SARIFData:   oversize,
			SARIFInline: true,
		})
		if ref == nil || ref.Code != "sarif-too-large" {
			t.Fatalf("inline oversize got %+v, want sarif-too-large", ref)
		}
	})

	t.Run("symlink sarif rejected", func(t *testing.T) {
		repoRoot, _ := setupBundle(t, "52", "abc1234", sampleDiff)
		tmp := t.TempDir()
		target := filepath.Join(tmp, "real.sarif")
		if err := os.WriteFile(target, []byte(validMinimal), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(tmp, "link.sarif")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		_, ref := Run(context.Background(), &Request{
			ReadRequest: core.ReadRequest{Runtime: fixedRuntime(), RepoRoot: repoRoot, Pr: "52"},
			SARIF:       link,
		})
		if ref == nil || ref.Code != "bad-sarif" {
			t.Fatalf("got %+v, want bad-sarif", ref)
		}
	})

	badDocs := []struct {
		name string
		body string
	}{
		{"wrong version", `{"version":"2.0.0","runs":[]}`},
		{"missing runs", `{"version":"2.1.0"}`},
		{"missing driver name", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":""}},"results":[]}]}`},
		{"trailing json", `{"version":"2.1.0","runs":[]} {"extra":1}`},
		{"traversal uri", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":[{"message":{"text":"hi"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"../etc/passwd"}}}]}]}]}`},
		{"encoded traversal uri", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":[{"message":{"text":"hi"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"a/%2e%2e/%2e%2e/etc/passwd"}}}]}]}]}`},
		{"file scheme uri", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":[{"message":{"text":"hi"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"file:///etc/passwd"}}}]}]}]}`},
		{"absolute uri", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":[{"message":{"text":"hi"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"/etc/passwd"}}}]}]}]}`},
		{"negative startLine", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":[{"message":{"text":"hi"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":-1}}}]}]}]}`},
		{"empty message", `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"t"}},"results":[{"message":{"text":"   "}}]}]}`},
	}
	for _, tc := range badDocs {
		t.Run(tc.name, func(t *testing.T) {
			repoRoot, _ := setupBundle(t, "53", "abc1234", sampleDiff)
			_, ref := Run(context.Background(), &Request{
				ReadRequest: core.ReadRequest{Runtime: fixedRuntime(), RepoRoot: repoRoot, Pr: "53"},
				SARIFData:   []byte(tc.body),
				SARIFInline: true,
			})
			if ref == nil || ref.Code != "bad-sarif" {
				t.Fatalf("%s: got %+v, want bad-sarif", tc.name, ref)
			}
		})
	}
}
