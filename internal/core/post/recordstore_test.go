package postcore

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
)

const storedHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// cleanFindingsReview is a review with nothing to report: the renderer writes
// its clean lead for it. It carries the read receipt the fixture bundle's one
// changed hunk needs, so the clean lead is one evidence does not question.
const cleanFindingsReview = `{"schema_version":"1","summary":"A short summary of the clean review.","findings":[],` +
	`"receipts":[{"hunk":"notes.txt\tNEW\t1\t3","quote":"new line"}]}`

// recordedDecision is the receipt record stores for that review once a block
// clause holds it back.
const recordedDecision = `{"approved":false,"reason":"open findings at-or-above informational",` +
	`"tier":"informational","gating_sigs":["b222222222222222222222222222222222222222"],` +
	`"clauses":["no-important"]}`

// storedFinding is the prior open finding the block clause fires on. It is not
// in the current review, so only the store and the clause withhold approval; it
// carries the verified quote standing record stamps, so the clause is one that
// may gate.
const storedFinding = `[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
	`"anchored":true,"body":"Prior finding with enough substance.","suggestion":null,` +
	`"inBounds":true,"rigor":"walked","severity":"informational",` +
	`"sig":"b222222222222222222222222222222222222222","sig_version":"1",` +
	`"verification":{"quote":"verified"},` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]`

// writeRecordedBundle writes a bundle plus a record.json that decision
// describes, hashing the files the way record does.
func writeRecordedBundle(t *testing.T, dir, decision string) {
	t.Helper()
	postBundle(t, cleanFindingsReview)
	recordPath := dir + "/record.json"
	if err := writeTestFile(recordPath, fmt.Sprintf(
		`{"schema_version":"1","status":"github","head":"%s","diff_sha256":"%s","review_sha256":"%s",`+
			`"recorded_at":"2026-11-02T12:00:00Z","findings":0,"open":0,"approval":%s}`,
		storedHead, fileSHA256(dir+"/full.diff"), fileSHA256(dir+"/review.json"), decision)); err != nil {
		t.Fatal(err)
	}
}

// TestAdoptStoredApprovalPublishesTheRecordedDecision proves post publishes
// record's decision instead of the second opinion its own merge computed.
func TestAdoptStoredApprovalPublishesTheRecordedDecision(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeRecordedBundle(t, dir, recordedDecision)
	var warnings []string
	p := postTestState(dir, storedHead, core.PostFlags{ReviewFile: dir + "/review.json"}, &warnings)
	p.merged = recordcore.MergeResult{Approval: &core.ApprovalReceipt{Approved: true, Reason: "no gating findings", Tier: "important"}}

	if ref := p.adoptStoredApproval(); ref != nil {
		t.Fatalf("adoptStoredApproval = %v, want the recorded decision", ref)
	}
	got := p.merged.Approval
	if got.Approved || got.Reason != "open findings at-or-above informational" || len(got.Clauses) != 1 || got.Clauses[0] != "no-important" {
		t.Fatalf("approval = %+v, want the record's withheld receipt", got)
	}
}

// TestAdoptStoredApprovalRefusesAMovedReview proves an edit to review.json
// after record refuses instead of silently recomputing over it.
func TestAdoptStoredApprovalRefusesAMovedReview(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeRecordedBundle(t, dir, recordedDecision)
	if err := writeTestFile(dir+"/review.json",
		`{"schema_version":"1","summary":"edited after record","findings":[]}`); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	p := postTestState(dir, storedHead, core.PostFlags{ReviewFile: dir + "/review.json"}, &warnings)
	computed := &core.ApprovalReceipt{Approved: true, Reason: "no gating findings", Tier: "important"}
	p.merged = recordcore.MergeResult{Approval: computed}

	ref := p.adoptStoredApproval()
	if ref == nil || ref.Code != "record-stale" {
		t.Fatalf("adoptStoredApproval = %v, want record-stale", ref)
	}
	if p.merged.Approval != computed {
		t.Errorf("approval = %+v, want the computed decision untouched on refusal", p.merged.Approval)
	}
}

// TestAdoptStoredApprovalIgnoresAnUnrecordedBundle proves a bundle with no
// record keeps post's own decision: post without record is the supported
// handoff for a dry run, and a record carrying no decision is not one.
func TestAdoptStoredApprovalIgnoresAnUnrecordedBundle(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, cleanFindingsReview)
	var warnings []string
	p := postTestState(dir, storedHead, core.PostFlags{ReviewFile: dir + "/review.json"}, &warnings)
	computed := &core.ApprovalReceipt{Approved: true, Reason: "no gating findings", Tier: "important"}
	p.merged = recordcore.MergeResult{Approval: computed}

	if ref := p.adoptStoredApproval(); ref != nil {
		t.Fatalf("adoptStoredApproval = %v, want no refusal without a record", ref)
	}
	if p.merged.Approval != computed {
		t.Errorf("approval = %+v, want the computed decision", p.merged.Approval)
	}
}

// TestBlockClauseBodyDoesNotSayClean proves the rendered body follows the
// authoritative decision: a block clause withholds approval for a review that
// reports nothing, so the body must not carry the clean lead, and post must not
// publish an approval.
func TestBlockClauseBodyDoesNotSayClean(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, cleanFindingsReview)
	if err := writeTestFile(".verdict/state/7/findings.json", storedFinding); err != nil {
		t.Fatal(err)
	}
	clause := core.BlockClause{Name: "no-important", Threshold: "informational"}
	rs := &config.RunState{
		BaseSHA:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:    true,
		CapturedAt: "2026-11-02T12:00:00Z",
		Evidence:   config.Evidence{SchemaVersion: "1", BaseSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Plan: config.Plan{Status: config.PendingStatus}},
		Resolved:   &config.Resolved{Plan: config.Plan{Status: config.PendingStatus}},
		Root:       config.RequirementSet{Clauses: []config.ClauseProvenance{{Clause: clause, Sources: []string{"config"}}}},
	}
	if err := config.WriteRunState(".verdict/state/7", rs); err != nil {
		t.Fatalf("seed run state: %v", err)
	}

	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	body := postedBody(t, cmdr)
	if strings.Contains(body, "> ✅") {
		t.Errorf("posted body still claims a clean review:\n%.300q", body)
	}
	if !strings.HasPrefix(body, "> ⚠️ Findings require attention.") {
		t.Errorf("posted body does not lead with the attention lead:\n%.300q", body)
	}
	if resp.Data.Event == "APPROVE" {
		t.Errorf("event = %q, want a non-approving event while a clause blocks approval", resp.Data.Event)
	}
}

// insufficientDecision is the decision record stores when a clean review
// receipts none of the bundle's changed hunks.
const insufficientDecision = `{"approved":false,"reason":"review-insufficient: hunk notes.txt\tNEW\t1\t3 has no matched receipt",` +
	`"tier":"important","gating_sigs":[],"insufficient":true}`

// receiptlessReview is a clean review that receipts nothing: the merge reads it
// as insufficient, so post may publish it but never approve it.
const receiptlessReview = `{"schema_version":"1","summary":"A short summary of the clean review.","findings":[]}`

// TestInsufficientRecordIsPublishedNotApproved proves an insufficient record
// still reaches the PR as a comment whose lead says the evidence is short, and
// that post never turns it into an approval.
func TestInsufficientRecordIsPublishedNotApproved(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeRecordedBundle(t, dir, insufficientDecision)
	if err := writeTestFile(dir+"/review.json", receiptlessReview); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(dir+"/record.json", fmt.Sprintf(
		`{"schema_version":"1","status":"github","head":"%s","diff_sha256":"%s","review_sha256":"%s",`+
			`"recorded_at":"2026-11-02T12:00:00Z","findings":0,"open":0,"approval":%s}`,
		storedHead, fileSHA256(dir+"/full.diff"), fileSHA256(dir+"/review.json"), insufficientDecision)); err != nil {
		t.Fatal(err)
	}

	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: dir + "/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	body := postedBody(t, cmdr)
	if !strings.HasPrefix(body, "> ⛔ Evidence checks insufficient — no approval.") {
		t.Errorf("posted body does not lead with the insufficient lead:\n%.300q", body)
	}
	if strings.Contains(body, "> ✅") {
		t.Errorf("posted body claims a clean review:\n%.300q", body)
	}
	if resp.Data.Event == "APPROVE" {
		t.Errorf("event = %q, want a comment while the evidence is insufficient", resp.Data.Event)
	}
}

// TestStoredInsufficientDecisionRelandsBody proves R7: post publishes record's
// decision, so a stored insufficient approval puts the insufficient lead on the
// body even when the merge's own evidence checks pass.
func TestStoredInsufficientDecisionRelandsBody(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	bundle := ".verdict/state/7"
	// The review receipts the fixture's one hunk, so the merge itself approves
	// and its body leads with the clean sentence.
	writeRecordedBundle(t, bundle, insufficientDecision)
	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: bundle + "/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	body := postedBody(t, cmdr)
	if !strings.HasPrefix(body, reviewdoc.EvidenceInsufficientLead) {
		t.Errorf("posted body does not lead with the insufficient lead:\n%.300q", body)
	}
	if resp.Data.Event == "APPROVE" {
		t.Errorf("event = %q, want a comment while the stored decision is insufficient", resp.Data.Event)
	}
}

// TestBindingApprovalRefusesInsufficientDecision proves R8: a decision the
// evidence checks withheld refuses a bound approval with review-insufficient,
// and --force cannot waive it because force never supplies evidence.
func TestBindingApprovalRefusesInsufficientDecision(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, receiptlessReview)
	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{
		Approve: "true", Bind: true, Force: true, ReviewFile: ".verdict/state/7/review.json",
	}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil || ref.Code != core.CodeReviewInsufficient {
		t.Fatalf("refusal = %+v, want review-insufficient", ref)
	}
	if strings.Count(ref.Error(), core.CodeReviewInsufficient) != 1 {
		t.Errorf("refusal = %q, want the code to appear once", ref.Error())
	}
	if !strings.Contains(ref.Message, "has no matched receipt") {
		t.Errorf("message = %q, want the gap that withheld the decision", ref.Message)
	}
	if posted := postedPayloads(t, cmdr); len(posted) != 0 {
		t.Errorf("posted %d review(s), want none for a refused approval", len(posted))
	}
}

// TestStoredInsufficientDecisionRefusesBindingApproval proves the stored
// decision wins over a merge that would approve: a record whose evidence was
// insufficient refuses the bound approval it never granted.
func TestStoredInsufficientDecisionRefusesBindingApproval(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeRecordedBundle(t, ".verdict/state/7", insufficientDecision)
	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{
		Approve: "true", Bind: true, Force: true, ReviewFile: ".verdict/state/7/review.json",
	}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil || ref.Code != core.CodeReviewInsufficient {
		t.Fatalf("refusal = %+v, want review-insufficient for the stored decision", ref)
	}
	if posted := postedPayloads(t, cmdr); len(posted) != 0 {
		t.Errorf("posted %d review(s), want none for a refused approval", len(posted))
	}
}
