package approval

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// currentEvidence is a receipt that backs the reviewed head, covered its one
// changed hunk, and found nothing to withhold: every decision below starts from
// it, because an absent receipt is itself an insufficient decision, and so is a
// receipt that leaves a hunk unmatched.
func currentEvidence() Evidence {
	return Evidence{Receipt: &core.EvidenceReceipt{
		Head:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Receipts: core.EvidenceReceipts{Total: 1, Covered: 1, Complete: true},
	}}
}

// TestNoConfigDefaultThreshold pins the operator decision: with no policy,
// important and critical findings withhold approval and informational ones do
// not. Each row is a literal severity, never a value derived from the code
// under test.
func TestNoConfigDefaultThreshold(t *testing.T) {
	tests := []struct {
		name     string
		severity string
		approved bool
	}{
		{"critical withholds", "critical", false},
		{"important withholds", "important", false},
		{"informational approves", "informational", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkDefaultThreshold(t, tc.severity, tc.approved) })
	}
}

// checkDefaultThreshold proves one severity's default decision and the named
// tier that decided it.
func checkDefaultThreshold(t *testing.T, severity string, approved bool) {
	t.Helper()
	got := Evaluate([]any{openFinding("sig-1", severity, "verified")}, "", "", nil, nil, currentEvidence())
	if got.Approved != approved {
		t.Fatalf("approved = %v, want %v (receipt %+v)", got.Approved, approved, got)
	}
	if got.Tier != DefaultApprovalTier {
		t.Fatalf("tier = %q, want the named default %q", got.Tier, DefaultApprovalTier)
	}
}

// openFinding builds one stored open finding with the given sig, severity,
// and quote standing.
func openFinding(sig, severity, quote string) *jq.Obj {
	return jq.NewObj("sig", sig, "status", "open", "severity", severity, "path", "notes.txt",
		"line", 2, "verification", jq.NewObj("quote", quote))
}

// TestUnbackedFindingNeverGatesButIsInsufficient proves R8: an open finding
// at-or-above the tier that carries no verified quote is non-gating, and the
// decision it leaves is insufficient rather than clean.
func TestUnbackedFindingNeverGatesButIsInsufficient(t *testing.T) {
	tests := []struct {
		name     string
		quote    string
		severity string
	}{
		{"explicitly unverified important", "unverified", "important"},
		{"explicitly unverified critical", "unverified", "critical"},
		{"legacy unchecked important", "unchecked", "important"},
		{"legacy unchecked critical", "unchecked", "critical"},
		{"unknown standing important", "made-up", "important"},
		{"unknown standing critical", "made-up", "critical"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { requireUnbackedGating(t, tc.severity, tc.quote) })
	}
}

// requireUnbackedGating proves one unbacked finding at-or-above the tier never
// gates and leaves an insufficient decision naming its sig.
func requireUnbackedGating(t *testing.T, severity, quote string) {
	t.Helper()
	got := Evaluate([]any{openFinding("sig-unbacked", severity, quote)}, "", "", nil, nil, currentEvidence())
	if got.Approved || !got.Insufficient {
		t.Fatalf("%s finding with quote %q = %+v, want Approved=false Insufficient=true", severity, quote, got)
	}
	if len(got.GatingSigs) != 0 {
		t.Fatalf("got.GatingSigs = %v, want empty", got.GatingSigs)
	}
	const wantReason = "review-insufficient: finding sig-unbacked has no verified quote"
	if got.Reason != wantReason {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, wantReason)
	}
}

// TestFindingWithoutVerificationRecordIsUnbacked proves the same for a store
// that carries no verification object at all.
func TestFindingWithoutVerificationRecordIsUnbacked(t *testing.T) {
	bare := jq.NewObj("sig", "sig-bare", "status", "open", "severity", "critical", "path", "notes.txt")
	got := Evaluate([]any{bare}, "", "", nil, nil, currentEvidence())
	if got.Approved || !got.Insufficient || len(got.GatingSigs) != 0 {
		t.Fatalf("finding without verification = %+v, want unbacked and insufficient", got)
	}
}

// TestUnbackedInformationalStaysWarning proves the insufficiency is scoped to
// findings that would have gated: a below-tier finding without a quote is a
// warning elsewhere and never withholds here.
func TestUnbackedInformationalStaysWarning(t *testing.T) {
	got := Evaluate([]any{openFinding("sig-info", "informational", "unverified")}, "", "", nil, nil, currentEvidence())
	if !got.Approved || got.Insufficient {
		t.Fatalf("unbacked informational finding = %+v, want approved", got)
	}
}

// TestMissingReceiptNeverApproves proves R3: a nil receipt is an insufficient
// decision, not a clean one, and it names what is missing.
func TestMissingReceiptNeverApproves(t *testing.T) {
	tests := []struct {
		name    string
		missing string
		want    string
	}{
		{"unspecified", "", "review-insufficient: evidence receipt missing"},
		{
			"report names the head", "evidence receipt missing for head aaaaaaa",
			"review-insufficient: evidence receipt missing for head aaaaaaa",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { requireMissingReceipt(t, tc.missing, tc.want) })
	}
}

// requireMissingReceipt proves one absent receipt refuses the review with the
// named reason.
func requireMissingReceipt(t *testing.T, missing, want string) {
	t.Helper()
	got := Evaluate(nil, "", "", nil, nil, Evidence{Missing: missing})
	if got.Approved || !got.Insufficient {
		t.Fatalf("missing receipt = %+v, want Approved=false Insufficient=true", got)
	}
	if got.Reason != want {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, want)
	}
}

// TestStaleReceiptReasonIsCarried proves the report's stale sentence reaches
// the decision verbatim behind the code.
func TestStaleReceiptReasonIsCarried(t *testing.T) {
	got := Evaluate(nil, "", "", nil, nil, Evidence{
		Missing: "evidence receipt stale for head aaaaaaa (recorded bbbbbbb)",
	})
	if got.Approved || !got.Insufficient {
		t.Fatalf("stale receipt = %+v, want insufficient", got)
	}
	const want = "review-insufficient: evidence receipt stale for head aaaaaaa (recorded bbbbbbb)"
	if got.Reason != want {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, want)
	}
}

func TestEligibleFindingsKeepsOnlyVerified(t *testing.T) {
	entries := []any{
		openFinding("sig-ver", "important", "verified"),
		openFinding("sig-unv", "critical", "unverified"),
		openFinding("sig-leg", "informational", "unchecked"),
	}
	got := EligibleFindings(entries)
	if len(got) != 1 {
		t.Fatalf("len(EligibleFindings) = %d, want 1", len(got))
	}
	obj, ok := got[0].(*jq.Obj)
	if !ok {
		t.Fatalf("entry has type %T, want *jq.Obj", got[0])
	}
	if sig, _ := obj.Get("sig"); jq.R(sig) != "sig-ver" {
		t.Fatalf("EligibleFindings kept %v, want sig-ver", jq.R(sig))
	}
}

// TestUnverifiedBlockClauseNeverGatesAndIsInsufficient proves the clause
// evaluator sees an unverified blocker in the UNFILTERED set and refuses it.
func TestUnverifiedBlockClauseNeverGatesAndIsInsufficient(t *testing.T) {
	entries := []any{openFinding("sig-unv", "informational", "unverified")}
	comp := &config.ReadCompliance{
		HasPolicy: true,
		Blockers: []config.BlockingFinding{{
			Clause:  "security-block",
			Finding: config.FindingView{Sig: "sig-unv", Path: "notes.txt", Severity: "informational", Status: "open"},
		}},
	}
	got := Evaluate(entries, "", "", comp, nil, currentEvidence())
	if got.Approved {
		t.Fatalf("dropping unverified block-clause finding manufactured green: %+v", got)
	}
	if !got.Insufficient {
		t.Fatalf("got.Insufficient = false, want true (%+v)", got)
	}
	if len(got.GatingSigs) != 0 {
		t.Fatalf("got.GatingSigs = %v, want empty (unverified sig must never gate)", got.GatingSigs)
	}
	if len(got.Clauses) != 0 {
		t.Fatalf("got.Clauses = %v, want empty", got.Clauses)
	}
	const wantReason = "review-insufficient: unverified finding named by block clause"
	if got.Reason != wantReason {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, wantReason)
	}
}

func TestVerifiedTierFindingGates(t *testing.T) {
	got := Evaluate([]any{openFinding("sig-ver", "important", "verified")}, "", "", nil, nil, currentEvidence())
	if got.Approved || got.Insufficient {
		t.Fatalf("verified important finding = %+v, want Approved=false Insufficient=false", got)
	}
	if !slices.Equal(got.GatingSigs, []string{"sig-ver"}) {
		t.Fatalf("got.GatingSigs = %v, want [sig-ver]", got.GatingSigs)
	}
	if got.Reason != "open findings at-or-above important" {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, "open findings at-or-above important")
	}
}

func TestVerifiedBlockClauseGatesDropsUnverifiedSibling(t *testing.T) {
	entries := []any{
		openFinding("sig-ver", "informational", "verified"),
		openFinding("sig-unv", "informational", "unverified"),
	}
	comp := &config.ReadCompliance{
		HasPolicy: true,
		Blockers: []config.BlockingFinding{
			{Clause: "auth-sec", Finding: config.FindingView{Sig: "sig-ver", Status: "open"}},
			{Clause: "unv-clause", Finding: config.FindingView{Sig: "sig-unv", Status: "open"}},
		},
	}
	got := Evaluate(entries, "", "", comp, nil, currentEvidence())
	if got.Approved || got.Insufficient {
		t.Fatalf("mixed block clauses = %+v, want Approved=false Insufficient=false", got)
	}
	if !slices.Equal(got.GatingSigs, []string{"sig-ver"}) {
		t.Fatalf("got.GatingSigs = %v, want [sig-ver]", got.GatingSigs)
	}
	if !slices.Equal(got.Clauses, []string{"auth-sec"}) {
		t.Fatalf("got.Clauses = %v, want [auth-sec]", got.Clauses)
	}
}

func TestInsufficientEvidenceWithholdsWithReason(t *testing.T) {
	ev := Evidence{Receipt: &core.EvidenceReceipt{
		Head:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DiffSHA256:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Gaps:         []string{"hunk notes.txt\tNEW\t1\t3 has no matched receipt", "second gap"},
		Insufficient: true,
	}}
	got := Evaluate([]any{openFinding("sig-unv", "critical", "unverified")}, "", "", nil, nil, ev)
	if got.Approved {
		t.Fatalf("insufficient evidence approved: %+v", got)
	}
	if !got.Insufficient {
		t.Fatalf("got.Insufficient = false, want true")
	}
	if len(got.GatingSigs) != 0 {
		t.Fatalf("got.GatingSigs = %v, want empty (unverified sig must not pollute GatingSigs)", got.GatingSigs)
	}
	const wantReason = "review-insufficient: hunk notes.txt\tNEW\t1\t3 has no matched receipt"
	if got.Reason != wantReason {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, wantReason)
	}

	clean := Evaluate(nil, "", "", nil, nil, currentEvidence())
	if !clean.Approved || clean.Insufficient || clean.Reason != "no gating findings" {
		t.Fatalf("sufficient clean review = %+v, want Approved=true Insufficient=false", clean)
	}
}

// TestViolatedSpecCheckWithholds pins the sentence a violations-only review has
// always carried: widening the failure list to a second kind must not move the
// reason byte for byte.
func TestViolatedSpecCheckWithholds(t *testing.T) {
	got := Evaluate(nil, "", "", nil, []core.SpecFailure{
		{Status: "violated", Item: "Reject expired tokens"},
	}, currentEvidence())
	if got.Approved {
		t.Fatalf("violated spec check approved: %+v", got)
	}
	if got.Insufficient {
		t.Fatalf("violated spec check read as insufficient: %+v", got)
	}
	if want := "violated spec check"; got.Reason != want {
		t.Fatalf("reason = %q, want %q", got.Reason, want)
	}
}

// TestUnmetSpecCheckWithholds proves a requirement nothing implements withholds
// approval and names itself, without reading as an unverifiable review.
func TestUnmetSpecCheckWithholds(t *testing.T) {
	got := Evaluate(nil, "", "", nil, []core.SpecFailure{
		{Status: "unmet", Item: "A 429 response resets the budget"},
	}, currentEvidence())
	if got.Approved {
		t.Fatalf("unmet spec check approved: %+v", got)
	}
	if got.Insufficient {
		t.Fatalf("unmet spec check read as insufficient: %+v", got)
	}
	if want := "unmet spec check: A 429 response resets the budget"; got.Reason != want {
		t.Fatalf("reason = %q, want %q", got.Reason, want)
	}
}

// TestMixedSpecCheckFailuresNameBoth proves a review with a violated row and an
// unmet one states both facts, and still withholds.
// TestTwoUnmetSpecChecksNameOnlyUnmet proves a review whose failures are all
// unmet rows never claims a violation: the reason names the first unmet item
// and nothing else, so the decision cannot tell an author their code is wrong
// when nothing implements the requirement.
func TestTwoUnmetSpecChecksNameOnlyUnmet(t *testing.T) {
	got := Evaluate(nil, "", "", nil, []core.SpecFailure{
		{Status: "unmet", Item: "A 429 response resets the budget"},
		{Status: "unmet", Item: "Rotate the signing key on deploy"},
	}, currentEvidence())
	if got.Approved {
		t.Fatalf("unmet spec failures approved: %+v", got)
	}
	if want := "unmet spec check: A 429 response resets the budget"; got.Reason != want {
		t.Fatalf("reason = %q, want %q", got.Reason, want)
	}
}

func TestMixedSpecCheckFailuresNameBoth(t *testing.T) {
	got := Evaluate(nil, "", "", nil, []core.SpecFailure{
		{Status: "violated", Item: "Reject expired tokens"},
		{Status: "unmet", Item: "A 429 response resets the budget"},
	}, currentEvidence())
	if got.Approved {
		t.Fatalf("mixed spec failures approved: %+v", got)
	}
	if want := "violated spec check; unmet spec check: A 429 response resets the budget"; got.Reason != want {
		t.Fatalf("reason = %q, want %q", got.Reason, want)
	}
}

// TestIncompleteReceiptsNeverApprove proves the last rule of the ladder: with
// nothing gating, approval needs a matched receipt for every non-exempt hunk,
// so a receipt that leaves hunks unmatched refuses and names how many. This is
// the decision's own guard, and it holds even when the evidence pass read the
// review as sufficient — a finding withheld after the merge cannot switch the
// requirement off.
func TestIncompleteReceiptsNeverApprove(t *testing.T) {
	ev := Evidence{Receipt: &core.EvidenceReceipt{
		Head:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Receipts: core.EvidenceReceipts{Total: 3, Covered: 1, Complete: false},
	}}
	got := Evaluate(nil, "", "", nil, nil, ev)
	if got.Approved || !got.Insufficient {
		t.Fatalf("approval = %+v, want insufficient: 2 of 3 hunks carry no receipt", got)
	}
	const wantReason = "review-insufficient: receipts missing for 2 of 3 hunks"
	if got.Reason != wantReason {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, wantReason)
	}
}

// TestUnkeyableReceiptApprovalNamesTheGap proves the coverage shortfall that is
// a diff with no keyable hunk at all: there is no count to report, so the
// reason carries the sentence the pass recorded.
func TestUnkeyableReceiptApprovalNamesTheGap(t *testing.T) {
	const gap = "full.diff carries changed hunks but none of their paths can be keyed"
	ev := Evidence{Receipt: &core.EvidenceReceipt{
		Head:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Receipts: core.EvidenceReceipts{Complete: false},
		Gaps:     []string{gap},
	}}
	got := Evaluate(nil, "", "", nil, nil, ev)
	if got.Approved || !got.Insufficient {
		t.Fatalf("approval = %+v, want insufficient", got)
	}
	if want := core.CodeReviewInsufficient + ": " + gap; got.Reason != want {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, want)
	}
}

// TestGatingFindingOutranksTheEvidenceGap proves the reason precedence: a
// verified open finding at-or-above the tier is the decisive fact, so it leads
// the reason, and the decision stays insufficient because the receipt still
// cannot back the head.
func TestGatingFindingOutranksTheEvidenceGap(t *testing.T) {
	const gap = "hunk notes.txt\tNEW\t1\t3 has no matched receipt"
	ev := Evidence{Receipt: &core.EvidenceReceipt{
		Head:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Receipts:     core.EvidenceReceipts{Total: 1, Complete: false},
		Gaps:         []string{gap},
		Insufficient: true,
	}}
	got := Evaluate([]any{openFinding("sig-ver", "important", "verified")}, "", "", nil, nil, ev)
	if got.Approved || !got.Insufficient {
		t.Fatalf("approval = %+v, want withheld and insufficient", got)
	}
	const wantReason = "open findings at-or-above important; review-insufficient: " + gap
	if got.Reason != wantReason {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, wantReason)
	}
	if !slices.Equal(got.GatingSigs, []string{"sig-ver"}) {
		t.Fatalf("got.GatingSigs = %v, want [sig-ver]", got.GatingSigs)
	}
}

// TestGatingFindingOutranksAnIncompleteReceipt proves the same precedence for
// the receipt the pass never called insufficient: the finding withholds the
// approval, so the decision is blocked rather than insufficient, and the gate
// that reads it still refuses the write.
func TestGatingFindingOutranksAnIncompleteReceipt(t *testing.T) {
	ev := Evidence{Receipt: &core.EvidenceReceipt{
		Head:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Receipts: core.EvidenceReceipts{Total: 2, Complete: false},
	}}
	got := Evaluate([]any{openFinding("sig-ver", "critical", "verified")}, "", "", nil, nil, ev)
	if got.Approved || got.Insufficient {
		t.Fatalf("approval = %+v, want blocked by the finding, not insufficient", got)
	}
	if want := "open findings at-or-above important"; got.Reason != want {
		t.Fatalf("got.Reason = %q, want %q", got.Reason, want)
	}
}
