package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	finderParitySig    = "1fd35ceaaa1fc0a98e1486617b35249c646ee577"
	finderParityDiff   = "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-return safeValue\n+return unsafeValue\n"
	finderParityReview = `{"schema_version":"2","execution":{"id":"execution-A","finders":2},"findings":[{"path":"notes.txt","line":1,"side":"RIGHT","severity":"important","claim":"Unhandled await error path here.","quote":"return unsafeValue","confidence":"high"}],"receipts":[{"hunk":"notes.txt\tNEW\t1\t1","quote":"return unsafeValue"}]}`
)

func finderParityFixture(t *testing.T) string {
	t.Helper()
	fixture(t)
	dir := filepath.Join(".verdict", "state", "local-finders")
	writeFile(t, filepath.Join(dir, "source.json"), `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFile(t, filepath.Join(dir, "full.diff"), finderParityDiff)
	writeFile(t, filepath.Join(dir, "review.json"), finderParityReview)
	plan := `{"checks":[{"sig":"1fd35ceaaa1fc0a98e1486617b35249c646ee577","origin":"finder-1","checker":"finder-2"}]}`
	writeFile(t, filepath.Join(dir, "verification", "plan.json"), plan)
	bindings := jq.NewObj("schema_version", "2", "execution_id", "execution-A", "head_sha", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"diff_sha256", finderDigest(finderParityDiff), "review_sha256", finderDigest(finderParityReview), "plan_sha256", finderDigest(plan))
	receipt := jq.NewObj()
	for _, key := range bindings.Keys {
		receipt.Set(key, bindings.Fields[key])
	}
	receipt.Set("results", []any{jq.NewObj("sig", finderParitySig, "checker", "finder-2", "verdict", "confirmed",
		"evidence", "The selected branch changes the return value.", "quote", jq.NewObj("path", "notes.txt", "side", "RIGHT", "line", 1, "text", "return unsafeValue"))})
	writeFile(t, filepath.Join(dir, "verification", "verdicts.json"), receipt.MarshalCompact())
	bindings.Set("status", "complete")
	writeFile(t, filepath.Join(dir, "launcher.json"), bindings.MarshalCompact())
	finderControls(t, dir)
	return dir
}

func finderDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func finderControls(t *testing.T, dir string) {
	t.Helper()
	base := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	snapshot, ref := controls.Resolve(t.Context(), nil, &controls.Request{BundleDir: dir, BaseSHA: base, AllowHead: true})
	if ref != nil {
		t.Fatal(ref)
	}
	captured := &config.RunState{
		BaseSHA: base, Trusted: true, Inventory: config.InventoryHashes(snapshot),
		Execution: &core.ExecutionReference{ID: "execution-A", Finders: 2},
		Resolved:  &config.Resolved{Plan: config.Plan{Status: config.PendingStatus}},
		Evidence:  config.Evidence{SchemaVersion: "1", BaseSHA: base, Plan: config.Plan{Status: config.PendingStatus}},
	}
	if err := config.WriteRunState(dir, captured); err != nil {
		t.Fatal(err)
	}
}

func finderRecordRequest() *recordcore.Request {
	return &recordcore.Request{ReadRequest: finderReadRequest(), Effective: core.Defaults()}
}

func TestFinderRecordReportMCPShareRecomputedEligibility(t *testing.T) {
	dir := finderParityFixture(t)
	response, ref := recordcore.Run(t.Context(), finderRecordRequest())
	if ref != nil {
		t.Fatal(ref)
	}
	assertFinderDecision(t, response.Data.Wire(), false, 1)
	assertStoredFinderStamp(t, dir)
	req := finderRecordRequest()
	req.DryRun = true
	data, ref := findTool(t, "record").invoke(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	assertFinderDecision(t, data, false, 1)
	stripFinderStamp(t, dir)
	report := &reportcore.Request{ReadRequest: finderReadRequest(), Flags: reportcore.Flags{Check: "important"}}
	direct, ref := reportcore.Run(t.Context(), report)
	if ref != nil {
		t.Fatal(ref)
	}
	assertFinderDecision(t, direct.Data.Wire(), false, 1)
	data, ref = findTool(t, "report").invoke(t.Context(), report)
	if ref != nil {
		t.Fatal(ref)
	}
	assertFinderDecision(t, data, false, 1)
}

func TestFinderMCPMissingReceiptCannotUseStrippedStamp(t *testing.T) {
	dir := finderParityFixture(t)
	if _, ref := recordcore.Run(t.Context(), finderRecordRequest()); ref != nil {
		t.Fatal(ref)
	}
	stripFinderStamp(t, dir)
	if err := os.Remove(filepath.Join(dir, "verification", "verdicts.json")); err != nil {
		t.Fatal(err)
	}
	report := &reportcore.Request{ReadRequest: finderReadRequest(), Flags: reportcore.Flags{Check: "important"}}
	data, ref := findTool(t, "report").invoke(t.Context(), report)
	if ref != nil {
		t.Fatal(ref)
	}
	assertFinderDecision(t, data, true, 0)
}

func stripFinderStamp(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "findings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := jq.DecodeOrdered(raw)
	if err != nil {
		t.Fatal(err)
	}
	entries := jq.Elements(value)
	if len(entries) != 1 {
		t.Fatalf("findings = %s", raw)
	}
	verification, ok := readcommon.FieldOf(entries[0], "verification").(*jq.Obj)
	if !ok {
		t.Fatal("verification missing")
	}
	verification.Delete("cross")
	writeFile(t, path, jq.PrettyOrdered(entries))
}

func assertFinderDecision(t *testing.T, data *jq.Obj, insufficient bool, gating int) {
	t.Helper()
	value, err := jq.DecodeOrdered([]byte(data.MarshalCompact()))
	if err != nil {
		t.Fatal(err)
	}
	decision := readcommon.FieldOf(value, "approval")
	if decision == nil {
		t.Fatal("approval is missing")
	}
	actualInsufficient := readcommon.FieldOf(decision, "insufficient") == true
	if actualInsufficient != insufficient ||
		len(jq.Elements(readcommon.FieldOf(decision, "gating_sigs"))) != gating {
		t.Fatalf("approval = %s", jq.PrettyOrdered(decision))
	}
	counts := readcommon.FieldOf(readcommon.FieldOf(value, "verification"), "finders")
	completed, _ := jq.IntOf(readcommon.FieldOf(counts, "completed"))
	confirmed, _ := jq.IntOf(readcommon.FieldOf(counts, "confirmed"))
	if completed != 2 || confirmed != gating {
		t.Fatalf("finder summary = %s", jq.PrettyOrdered(counts))
	}
}

func finderReadRequest() core.ReadRequest {
	return core.ReadRequest{Repo: "acme/app", Pr: "local-finders", Runtime: &core.Runtime{Now: func() time.Time { return fixedNow }}}
}

func assertStoredFinderStamp(t *testing.T, dir string) {
	t.Helper()
	cross := storedFinderCross(t, dir)
	if readcommon.FieldOf(cross, "status") != "confirmed" {
		t.Fatal("bound cross stamp was not stored")
	}
}

func storedFinderCross(t *testing.T, dir string) any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := jq.DecodeOrdered(raw)
	if err != nil {
		t.Fatal(err)
	}
	entries := jq.Elements(value)
	if len(entries) != 1 {
		t.Fatalf("stored findings = %s", raw)
	}
	return readcommon.FieldOf(readcommon.FieldOf(entries[0], "verification"), "cross")
}

func TestFinderRefutationKeepsBoundQuote(t *testing.T) {
	dir := finderParityFixture(t)
	path := filepath.Join(dir, "verification", "verdicts.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, strings.ReplaceAll(string(raw), `"confirmed"`, `"refuted"`))
	response, ref := recordcore.Run(t.Context(), finderRecordRequest())
	if ref != nil {
		t.Fatal(ref)
	}
	if !response.Data.Approval.Insufficient || len(response.Data.Approval.GatingSigs) != 0 || response.Data.Verification.Finders.Contested != 1 {
		t.Fatalf("refutation standing = %+v", response.Data)
	}
	cross := storedFinderCross(t, dir)
	checks := jq.Elements(readcommon.FieldOf(cross, "checks"))
	if len(checks) != 1 || readcommon.FieldOf(readcommon.FieldOf(checks[0], "quote"), "text") != "return unsafeValue" {
		t.Fatalf("checker quote = %s", jq.PrettyOrdered(checks))
	}
}
