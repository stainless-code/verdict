package reportcore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	gatecli "github.com/stainless-code/verdict/internal/gate"
	reportcli "github.com/stainless-code/verdict/internal/report"
)

func writeRecordWithEvidence(t *testing.T, ev *core.EvidenceReceipt) {
	t.Helper()
	rec := core.Record{
		SchemaVersion: "1",
		Status:        "pr",
		Head:          storeHeadSHA,
		Evidence:      ev,
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	writeFile(t, ".verdict/state/5101/record.json", string(raw))
}

// runGateExitCode runs the gate verb, the only verb that gates the exit.
func runGateExitCode(t *testing.T, args ...string) int {
	t.Helper()
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, Now: fixedRuntime().Now}
	return gatecli.Run(context.Background(), r, args, gatecli.Request{Repo: "acme/app", Pr: "5101"})
}

func runReportExitCode(t *testing.T, args ...string) int {
	t.Helper()
	var out, diag bytes.Buffer
	r := &cli.Runner{Out: &out, Human: &out, Diag: &diag, Now: fixedRuntime().Now}
	return reportcli.Run(context.Background(), r, args, reportcli.Request{Repo: "acme/app", Pr: "5101"})
}

func findEvidenceDim(t *testing.T, resp *reportcore.Response) core.GateDimension {
	t.Helper()
	if resp.Data.Gate == nil {
		t.Fatal("data.gate is nil on a gated run")
	}
	for _, dim := range resp.Data.Gate.Dimensions {
		if dim.ID == "evidence" {
			return dim
		}
	}
	t.Fatalf("evidence dimension missing from %+v", resp.Data.Gate.Dimensions)
	return core.GateDimension{}
}

func TestEvidenceGatePass(t *testing.T) {
	req := fixture(t, "[]")
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:       storeHeadSHA,
		DiffSHA256: strings.Repeat("d", 64),
		Quotes: []core.EvidenceQuote{
			{Path: "a.go", Side: "RIGHT", Status: "verified", Line: 10},
			{Path: "b.go", Side: "RIGHT", Status: "verified", Line: 20},
		},
		Receipts:     core.EvidenceReceipts{Total: 4, Covered: 4, Exempt: 0, Complete: true},
		Spec:         core.EvidenceSpec{Total: 10, Covered: 8, Min: 80, Met: true},
		Insufficient: false,
	})
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.GateFailed {
		t.Fatalf("GateFailed = true, want false (lines %q)", resp.CheckLines)
	}
	dim := findEvidenceDim(t, resp)
	if dim.Status != "pass" {
		t.Fatalf("evidence status = %q, want %q", dim.Status, "pass")
	}
	const wantLine = "check: evidence checks satisfied (2 quotes verified, 4/4 hunks receipted, 8/10 spec items checked) — gate passes"
	if !containsLine(resp.CheckLines, wantLine) {
		t.Fatalf("CheckLines = %q, want %q", resp.CheckLines, wantLine)
	}
	if code := runGateExitCode(t, "--max-severity", "important", "--live", "skip", "--coverage", "warn"); code != cli.ExitOK {
		t.Fatalf("gate CLI exit = %d, want %d", code, cli.ExitOK)
	}
	requireApproval(t, resp, true, false, "no gating findings")
}

func TestEvidenceGateFailInsufficient(t *testing.T) {
	req := fixture(t, "[]")
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:       storeHeadSHA,
		DiffSHA256: strings.Repeat("d", 64),
		Quotes: []core.EvidenceQuote{
			{Path: "a.go", Side: "RIGHT", Status: "unverified", Reason: "text-differs", Line: 10},
			{Path: "b.go", Side: "RIGHT", Status: "unverified", Reason: "path-not-in-bundle", Line: 5},
		},
		Gaps:         []string{"quote gap 1", "quote gap 2", "hunk gap"},
		Receipts:     core.EvidenceReceipts{Total: 4, Covered: 3, Exempt: 0, Complete: false},
		Spec:         core.EvidenceSpec{Total: 0, Covered: 0, Min: 80, Met: true},
		Insufficient: true,
	})
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for insufficient evidence")
	}
	dim := findEvidenceDim(t, resp)
	if dim.Status != "fail" {
		t.Fatalf("evidence status = %q, want %q", dim.Status, "fail")
	}
	const wantLine = "check: evidence checks insufficient (2 quote gap(s), 1 hunk without receipt) [review-insufficient] — gate fails"
	if !containsLine(resp.CheckLines, wantLine) {
		t.Fatalf("CheckLines = %q, want %q", resp.CheckLines, wantLine)
	}
	if code := runGateExitCode(t, "--max-severity", "important", "--live", "skip", "--coverage", "warn"); code != cli.ExitRefused {
		t.Fatalf("gate CLI exit = %d, want %d", code, cli.ExitRefused)
	}
}

func TestEvidenceGateMissingIsUnknown(t *testing.T) {
	req := fixture(t, "[]")
	if err := os.Remove(".verdict/state/5101/record.json"); err != nil {
		t.Fatal(err)
	}
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for missing evidence receipt")
	}
	dim := findEvidenceDim(t, resp)
	if dim.Status != "unknown" {
		t.Fatalf("evidence status = %q, want %q", dim.Status, "unknown")
	}
	const wantLine = "check: evidence receipt missing for head aaaaaaa [review-insufficient] — gate fails"
	if !containsLine(resp.CheckLines, wantLine) {
		t.Fatalf("CheckLines = %q, want %q", resp.CheckLines, wantLine)
	}
	if code := runGateExitCode(t, "--max-severity", "important", "--live", "skip", "--coverage", "warn"); code != cli.ExitRefused {
		t.Fatalf("gate CLI exit = %d, want %d", code, cli.ExitRefused)
	}
	// R3: no legacy path. A bundle with no receipt may not read as approved
	// beside the failing gate dimension.
	requireApproval(t, resp, false, true, "review-insufficient: evidence receipt missing for head aaaaaaa")
}

func TestEvidenceGateStaleIsUnknown(t *testing.T) {
	req := fixture(t, "[]")
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:         strings.Repeat("c", 40),
		DiffSHA256:   strings.Repeat("d", 64),
		Receipts:     core.EvidenceReceipts{Total: 1, Covered: 1, Complete: true},
		Spec:         core.EvidenceSpec{Total: 0, Covered: 0, Min: 80, Met: true},
		Insufficient: false,
	})
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for stale evidence receipt")
	}
	dim := findEvidenceDim(t, resp)
	if dim.Status != "unknown" {
		t.Fatalf("evidence status = %q, want %q", dim.Status, "unknown")
	}
	const wantLine = "check: evidence receipt stale for head aaaaaaa (recorded ccccccc) [review-insufficient] — gate fails"
	if !containsLine(resp.CheckLines, wantLine) {
		t.Fatalf("CheckLines = %q, want %q", resp.CheckLines, wantLine)
	}
	if code := runGateExitCode(t, "--max-severity", "important", "--live", "skip", "--coverage", "warn"); code != cli.ExitRefused {
		t.Fatalf("gate CLI exit = %d, want %d", code, cli.ExitRefused)
	}
	requireApproval(t, resp, false, true,
		"review-insufficient: evidence receipt stale for head aaaaaaa (recorded ccccccc)")
}

func TestEvidenceGateUnreadableIsUnknown(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/record.json", `{broken`)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false, want true for unreadable record.json")
	}
	dim := findEvidenceDim(t, resp)
	if dim.Status != "unknown" {
		t.Fatalf("evidence status = %q, want %q", dim.Status, "unknown")
	}
	const wantLine = "check: evidence receipt unreadable for head aaaaaaa [review-insufficient] — gate fails"
	if !containsLine(resp.CheckLines, wantLine) {
		t.Fatalf("CheckLines = %q, want %q", resp.CheckLines, wantLine)
	}
}

func TestEvidenceAdvisoryReportWarnsAndNeverFails(t *testing.T) {
	req := fixture(t, "[]")
	writeRecordWithEvidence(t, &core.EvidenceReceipt{
		Head:         storeHeadSHA,
		DiffSHA256:   strings.Repeat("d", 64),
		Gaps:         []string{"hunk a.go\tNEW\t1\t3 has no matched receipt"},
		Receipts:     core.EvidenceReceipts{Total: 1, Covered: 0, Complete: false},
		Spec:         core.EvidenceSpec{Total: 0, Covered: 0, Min: 80, Met: true},
		Insufficient: true,
	})
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{ReadRequest: req})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.GateFailed {
		t.Fatal("advisory report set GateFailed = true")
	}
	const wantWarn = "evidence: evidence checks insufficient (1 hunk without receipt) [review-insufficient]"
	if !containsLine(resp.Warnings, wantWarn) {
		t.Fatalf("Warnings = %q, want %q", resp.Warnings, wantWarn)
	}
	if code := runReportExitCode(t); code != cli.ExitOK {
		t.Fatalf("advisory report CLI exit = %d, want %d", code, cli.ExitOK)
	}
}

// requireApproval asserts the report's approval decision, which a reviewer
// reads beside the gate: a withheld gate may never sit next to an approval.
func requireApproval(t *testing.T, resp *reportcore.Response, approved, insufficient bool, reason string) {
	t.Helper()
	got := resp.Data.Approval
	if got == nil {
		t.Fatal("report data omits the approval decision")
	}
	if got.Approved != approved || got.Insufficient != insufficient || got.Reason != reason {
		t.Fatalf("approval = %+v, want approved=%v insufficient=%v reason=%q",
			got, approved, insufficient, reason)
	}
}
