package ingestcore

import (
	"context"
	"testing"

	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// TestRun_ReportSarifRoundTrip proves the two verbs speak one dialect:
// `report --sarif` bytes fed back to `ingest --from sarif` re-anchor, re-grade,
// and re-provenance the same findings instead of adding duplicates, relabeling
// the tool, or dropping a result the diff carries.
func TestRun_ReportSarifRoundTrip(t *testing.T) {
	repoRoot, dir := setupBundle(t, "60", "abc1234")
	first := mustRunIngest(t, repoRoot, "60", roundTripSARIF)
	if first.Data.Added != 2 || len(first.Data.Skipped) != 0 {
		t.Fatalf("first ingest = %+v, want two anchored findings", first.Data)
	}
	second := reingestReport(t, repoRoot)
	assertRoundTripCounts(t, second)
	if len(second.Data.Tools) != 1 || second.Data.Tools[0] != "semgrep" {
		t.Fatalf("round-trip tools = %v, want the restored [semgrep] provenance", second.Data.Tools)
	}
	assertRoundTripStore(t, dir)
}

const roundTripSARIF = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"semgrep","version":"1.90.0"}},"results":[
	{"ruleId":"go.jwt.none","level":"error","message":{"text":"JWT signature skipped"},"partialFingerprints":{"primaryLocationLineHash":"fp-jwt"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/auth.go"},"region":{"startLine":12,"snippet":{"text":"  checkToken()"}}}}]},
	{"ruleId":"go.vendor.rule","level":"warning","message":{"text":"Generated vendor issue"},"partialFingerprints":{"primaryLocationLineHash":"fp-vendor"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"vendor/gen.go"},"region":{"startLine":1}}}]}
]}]}`

// reingestReport exports the bundle with `report --sarif` and ingests the bytes
// back through the inline source, the round trip an operator runs by piping.
func reingestReport(t *testing.T, repoRoot string) *Response {
	t.Helper()
	report, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: newReadReq(repoRoot, "60"),
		Flags:       reportcore.Flags{Sarif: "out.sarif"},
	})
	if ref != nil {
		t.Fatalf("report refusal: %+v", ref)
	}
	if report.Sarif == nil {
		t.Fatal("report returned no SARIF artifact")
	}
	second, ref := runIngestBytes(repoRoot, "60", report.Sarif.Content)
	if ref != nil {
		t.Fatalf("round-trip refusal: %+v", ref)
	}
	return second
}

func assertRoundTripCounts(t *testing.T, second *Response) {
	t.Helper()
	if second.Data.Added != 0 || second.Data.Updated != 0 || second.Data.Unchanged != 2 {
		t.Fatalf("round-trip counts = %+v, want 0 added, 0 updated, 2 unchanged", second.Data)
	}
	if len(second.Data.Skipped) != 0 {
		t.Fatalf("round-trip skipped = %+v, want none: report exports only anchored findings", second.Data.Skipped)
	}
}

func assertRoundTripStore(t *testing.T, dir string) {
	t.Helper()
	entries := loadStoreEntries(t, dir)
	if len(entries) != 2 {
		t.Fatalf("store length = %d, want 2", len(entries))
	}
	if entries[0]["tool"] != "semgrep" || entries[0]["rule_id"] != "go.jwt.none" || entries[0]["fingerprint"] != "fp-jwt" {
		t.Fatalf("round-trip relabeled the finding: %+v", entries[0])
	}
	if entries[0]["rigor"] != "pointed" || entries[0]["quote"] != "  checkToken()" {
		t.Fatalf("round-trip lost the graded evidence: %+v", entries[0])
	}
}
