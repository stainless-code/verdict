package reportcore_test

import (
	"context"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// revalidatedFinding is a finding a VERIFIED claim citing `npm test` flipped.
const revalidatedFinding = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
	`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,` +
	`"inBounds":true,"rigor":"walked","severity":"important","category":"bug",` +
	`"sig":"dddddddddddddddddddddddddddddddddddddddd","sig_version":"1",` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"addressed","rationale":"",` +
	`"history":[{"at":"2026-01-01T00:00:00Z","event":"addressed","actor":"revalidator","note":"npm test","sha":"aaaaaaa"}]}`

// sharedReceipt is a passing verify receipt for `npm test` that shared
// node_modules into its snapshot.
const sharedReceipt = `{"schema_version":"2","command":"npm test","tree":"` +
	`eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","at":"2026-01-01T00:00:00Z",` +
	`"shared":["node_modules"],"exit":0,"timed_out":false}`

const evidenceSatisfied = "evidence checks satisfied (0 quotes verified, 0/0 hunks receipted, 0/0 spec items checked)"

// TestEvidenceGateNamesTheSharedDirectoriesBehindAVerifiedClaim makes the
// trust trade-off legible where the gate reports: the receipt behind a
// VERIFIED claim depended on shared directories, so the evidence detail names
// them. It is information only: the dimension and the gate still pass.
func TestEvidenceGateNamesTheSharedDirectoriesBehindAVerifiedClaim(t *testing.T) {
	req := fixture(t, "["+revalidatedFinding+"]")
	writeFile(t, ".verdict/state/5101/verify-receipt.json", sharedReceipt)
	dim, failed := evidenceGate(t, req)
	const want = evidenceSatisfied + "; receipt used shared directories: node_modules"
	if dim.Status != "pass" || dim.Detail != want || failed {
		t.Fatalf("evidence = %s %q (gate failed %v), want pass %q and a passing gate", dim.Status, dim.Detail, failed, want)
	}
	if code := runGateExitCode(t, "--max-severity", "important", "--live", "skip", "--coverage", "warn"); code != cli.ExitOK {
		t.Fatalf("gate CLI exit = %d, want %d", code, cli.ExitOK)
	}
}

// TestEvidenceGateLeavesASharedReceiptNoClaimUsedUnnamed keeps the note to
// receipts a VERIFIED claim actually rests on.
func TestEvidenceGateLeavesASharedReceiptNoClaimUsedUnnamed(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/verify-receipt.json", sharedReceipt)
	if dim, _ := evidenceGate(t, req); dim.Detail != evidenceSatisfied {
		t.Fatalf("evidence detail = %q, want %q", dim.Detail, evidenceSatisfied)
	}
}

// evidenceGate runs the gate at the important tier and returns its evidence
// dimension and whether the gate failed.
func evidenceGate(t *testing.T, req core.ReadRequest) (core.GateDimension, bool) {
	t.Helper()
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	return findEvidenceDim(t, resp), resp.GateFailed
}
