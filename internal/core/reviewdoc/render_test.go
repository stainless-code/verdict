package reviewdoc_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	testHeadFull = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	testBaseFull = "11223344556677889900aabbccddeeff00112233"
	testBaseURL  = "https://github.com/acme/app"
	testNow      = "2026-11-20T09:00:00Z"
)

func TestRenderBodyGoldens(t *testing.T) {
	cases := []struct {
		input *reviewdoc.RenderInput
		name  string
	}{
		{name: "clean", input: cleanRenderInput()},
		{name: "clean-informational", input: minorRenderInput()},
		{name: "blocking", input: blockingRenderInput()},
		{name: "insufficient", input: insufficientRenderInput()},
		{name: "local", input: localRenderInput()},
		{name: "adversarial", input: adversarialRenderInput()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reviewdoc.Render(tc.input) + "\n"
			golden.Check(t, tc.name, got)
		})
	}
}

func cleanRenderInput() *reviewdoc.RenderInput {
	review := jq.NewObj(
		"schema_version", "1",
		"summary", "Reviewed the token refresh refactor and its unit tests; expiration checks and error propagation match the existing contract.",
		"security_summary", "No new attack surface; auth paths untouched.",
		"findings", []any{},
	)
	return &reviewdoc.RenderInput{
		Review:   review,
		Approval: &core.ApprovalReceipt{Approved: true, Reason: "no gating findings", Tier: "important"},
		Evidence: &core.EvidenceReceipt{
			Receipts: core.EvidenceReceipts{Total: 2, Covered: 2, Complete: true},
			Spec:     core.EvidenceSpec{Total: 2, Covered: 2, Min: 2, Met: true},
		},
		BundleMeta: reviewdoc.BundleMeta{
			HeadSHA:     testHeadFull,
			HeadRef:     "feat/token-refresh",
			BaseSHA:     testBaseFull,
			BaseRef:     "main",
			Mode:        "Review (initial)",
			PriorReview: "none",
			Commits:     []reviewdoc.MetaCommit{{SHA: testHeadFull, Subject: "Refactor token refresh helper"}},
			Files: []reviewdoc.MetaFile{
				{Path: "src/auth.go", Additions: 14, Deletions: 6, HasStats: true},
				{Path: "src/auth_test.go", Additions: 32, Deletions: 0, HasStats: true},
			},
			FileCount: 2,
		},
		SpecChecks: []core.SpecCheck{
			{Item: "Refresh rejects expired tokens", Status: "met", Evidence: core.SpecEvidence{Path: "src/auth.go", Line: 42, Quote: "if exp.Before(now)"}},
			{Item: "Unit tests cover clock skew", Status: "met", Evidence: core.SpecEvidence{Path: "src/auth_test.go", Line: 18, Quote: "TestClockSkew"}},
		},
		BaseURL:  testBaseURL,
		HeadFull: testHeadFull,
		Now:      testNow,
	}
}

func minorRenderInput() *reviewdoc.RenderInput {
	f := jq.NewObj(
		"sig", "sig-info-1",
		"status", "open",
		"severity", "informational",
		"rigor", "pointed",
		"category", "style",
		"anchored", true,
		"inBounds", true,
		"path", "src/auth.go",
		"line", 15,
		"claim", "Helper name could be more descriptive.",
		"verification", jq.NewObj("quote", "verified"),
	)
	return &reviewdoc.RenderInput{
		Review:   jq.NewObj("schema_version", "1", "summary", "One minor naming suggestion only.", "findings", []any{f}),
		Approval: &core.ApprovalReceipt{Approved: true, Reason: "no gating findings", Tier: "important"},
		Kept:     []any{f},
		BundleMeta: reviewdoc.BundleMeta{
			HeadSHA:     testHeadFull,
			HeadRef:     "feat/naming",
			BaseSHA:     testBaseFull,
			BaseRef:     "main",
			Mode:        "Review (initial)",
			PriorReview: "none",
			Commits:     []reviewdoc.MetaCommit{{SHA: testHeadFull, Subject: "Clean up helper"}},
			FileCount:   1,
		},
		BaseURL:  testBaseURL,
		HeadFull: testHeadFull,
		Now:      testNow,
	}
}

func blockingRenderInput() *reviewdoc.RenderInput {
	f1 := jq.NewObj(
		"sig", "sig-crit-1",
		"status", "open",
		"severity", "critical",
		"rigor", "ran",
		"category", "security",
		"anchored", true,
		"inBounds", true,
		"path", "src/auth.go",
		"start_line", 42,
		"line", 44,
		"claim", "Token check skips `exp` validation when `leeway` is zero, allowing expired tokens to authenticate.",
		"verification", jq.NewObj("quote", "verified"),
		"brief", jq.NewObj(
			"sites", []any{
				jq.NewObj("path", "src/auth.go", "line", 44),
				jq.NewObj("path", "src/middleware.go", "line", 19),
			},
			"outcome", "Reject expired tokens regardless of whether leeway is zero.",
			"approach", "Check `now.After(claims.ExpiresAt.Add(leeway))` unconditionally.",
			"questions", []any{"Should clock skew default to 30s when config omits leeway?"},
		),
		"kb", []any{"jwt-exp"},
		"history", []any{jq.NewObj("at", testNow, "event", "reported")},
	)
	f2 := jq.NewObj(
		"sig", "sig-imp-2",
		"status", "open",
		"severity", "important",
		"rigor", "walked",
		"category", "logic",
		"anchored", true,
		"inBounds", true,
		"path", "src/audit.go",
		"line", 88,
		"claim", "Retry loop swallows the audit sink error instead of propagating it.",
		"verification", jq.NewObj("quote", "verified"),
	)
	moved1 := jq.NewObj(
		"sig", "sig-moved-1",
		"status", "open",
		"severity", "important",
		"rigor", "pointed",
		"anchored", false,
		"inBounds", false,
		"path", "src/config.go",
		"line", 12,
		"dropReason", "anchor 12-12 (RIGHT) outside reviewed hunks",
		"claim", "Default timeout constant stays at 0 after removing the fallback guard.",
		"verification", jq.NewObj("quote", "verified"),
	)
	movedUnknown := jq.NewObj(
		"sig", "sig-moved-2",
		"status", "open",
		"severity", "informational",
		"rigor", "said",
		"anchored", false,
		"inBounds", false,
		"path", "src/ghost.go",
		"line", 5,
		"dropReason", "path not in diff index: src/ghost.go",
		"claim", "Missing file should not produce a broken blob link.",
		"verification", jq.NewObj("quote", "verified"),
	)
	withheld := jq.NewObj(
		"sig", "sig-hidden",
		"status", "open",
		"severity", "critical",
		"path", "src/secret.go",
		"line", 1,
		"claim", "SECRET_WITHHELD_CLAIM",
		"withheld", jq.NewObj("source", "agent", "reason", "operator suppressed", "at", testNow),
	)
	review := jq.NewObj(
		"schema_version", "1",
		"summary", "Two defects in token verification allow expired tokens through and drop audit logging on retry.",
		"security_summary", "Token verification bypass affects unauthenticated callers.",
		"findings", []any{f1, f2},
	)
	return &reviewdoc.RenderInput{
		Review: review,
		Approval: &core.ApprovalReceipt{
			Approved: false,
			Reason:   "open findings at-or-above important",
			Tier:     "important",
			Clauses:  []string{"no-auth-regressions"},
		},
		Evidence: &core.EvidenceReceipt{
			Spec: core.EvidenceSpec{Total: 2, Covered: 2, Min: 2, Met: true},
		},
		Verification: &core.PanelSummary{Verifiers: 1, Confirmed: 1, Contested: 1, Single: 0},
		Risk:         &core.RiskClaim{Level: "high", Rationale: "Touches JWT signature validation on the public API edge."},
		Entries:      []any{f1, f2, moved1, movedUnknown, withheld},
		Kept:         []any{f2, f1, withheld},
		Moved:        []any{movedUnknown, moved1, withheld},
		SpecChecks: []core.SpecCheck{
			{Item: "Audit event records request ID", Status: "met", Evidence: core.SpecEvidence{Path: "src/audit.go", Line: 61, Quote: "reqID"}},
			{Item: "Expired tokens must be rejected on every path", Status: "violated", Evidence: core.SpecEvidence{Path: "src/auth.go", Line: 42, Quote: "if leeway > 0"}},
		},
		BundleMeta: reviewdoc.BundleMeta{
			HeadSHA:     testHeadFull,
			HeadRef:     "feat/token-refresh",
			BaseSHA:     testBaseFull,
			BaseRef:     "main",
			Mode:        "Review (initial)",
			PriorReview: "none",
			Commits:     []reviewdoc.MetaCommit{{SHA: testHeadFull, Subject: "Update JWT verification flow"}},
			FileCount:   3,
		},
		BaseURL:  testBaseURL,
		HeadFull: testHeadFull,
		Now:      testNow,
	}
}

func insufficientRenderInput() *reviewdoc.RenderInput {
	f1 := jq.NewObj(
		"sig", "sig-1",
		"status", "open",
		"severity", "important",
		"rigor", "walked",
		"anchored", true,
		"inBounds", true,
		"path", "pkg/auth.go",
		"line", 42,
		"claim", "Token check skips expiry.",
		"verification", jq.NewObj("quote", "verified"),
	)
	f2 := jq.NewObj(
		"sig", "sig-2",
		"status", "open",
		"severity", "critical",
		"rigor", "pointed",
		"anchored", true,
		"inBounds", true,
		"path", "pkg/cache.go",
		"line", 19,
		"claim", "Possible race on map write.",
		"verification", jq.NewObj("quote", "unverified"),
	)
	review := jq.NewObj(
		"schema_version", "1",
		"summary", "Reviewed auth and cache changes; one cache write appears unsynchronized.",
		"findings", []any{f1, f2},
	)
	return &reviewdoc.RenderInput{
		Review: review,
		Approval: &core.ApprovalReceipt{
			Approved:     false,
			Insufficient: true,
			Reason:       "review-insufficient: finding 2 quote does not match full.diff at pkg/cache.go:19 (text-differs)",
			Tier:         "important",
		},
		Evidence: &core.EvidenceReceipt{
			Quotes: []core.EvidenceQuote{
				{Path: "pkg/auth.go", Line: 42, Side: "RIGHT", Status: "verified"},
				{Path: "pkg/cache.go", Line: 19, Side: "RIGHT", Status: "unverified", Reason: "text-differs"},
			},
			Receipts: core.EvidenceReceipts{Total: 2, Covered: 0, Complete: false},
			Gaps: []string{
				"finding 2 quote does not match full.diff at pkg/cache.go:19 (text-differs)",
				"hunk pkg/cache.go\tNEW\t1\t25 has no matched receipt",
				"hunk pkg/auth.go\tNEW\t30\t55 has no matched receipt",
			},
			Insufficient: true,
		},
		Kept: []any{f1, f2},
		BundleMeta: reviewdoc.BundleMeta{
			HeadSHA:     testHeadFull,
			HeadRef:     "feat/cache-fix",
			BaseSHA:     testBaseFull,
			BaseRef:     "main",
			Mode:        "Review (initial)",
			PriorReview: "none",
			Commits:     []reviewdoc.MetaCommit{{SHA: testHeadFull, Subject: "Add in-memory token cache"}},
			FileCount:   2,
		},
		BaseURL:  testBaseURL,
		HeadFull: testHeadFull,
		Now:      testNow,
	}
}

func localRenderInput() *reviewdoc.RenderInput {
	review := jq.NewObj(
		"schema_version", "1",
		"summary", "Local diff looks clean.",
		"findings", []any{},
	)
	return &reviewdoc.RenderInput{
		Review:   review,
		Approval: &core.ApprovalReceipt{Approved: true, Reason: "no gating findings", Tier: "important"},
		BundleMeta: reviewdoc.BundleMeta{
			HeadSHA:     testHeadFull,
			HeadRef:     "work",
			BaseSHA:     testBaseFull,
			BaseRef:     "main",
			Range:       "main..HEAD",
			Mode:        "Review (local, unposted)",
			PriorReview: "none",
			Files:       []reviewdoc.MetaFile{{Path: "pkg/run.go"}},
			FileCount:   1,
			IsLocal:     true,
			Dirty:       true,
		},
		HeadFull: testHeadFull,
		Now:      testNow,
	}
}

func adversarialRenderInput() *reviewdoc.RenderInput {
	f := jq.NewObj(
		"sig", "sig-adv",
		"status", "open",
		"severity", "important",
		"rigor", "pointed",
		"anchored", true,
		"inBounds", true,
		"path", "dir with spaces/file#1[x](y).go",
		"line", 7,
		"claim", "Ping @alice, see [evil](https://evil.test) and <script>x</script> and ```sh\nrm -rf /\n``` plus `ok < 1`",
		"verification", jq.NewObj("quote", "verified"),
		"brief", jq.NewObj(
			"outcome", "Close </details> safely with ```` ticks and @bob.",
		),
	)
	review := jq.NewObj(
		"schema_version", "1",
		"summary", "> ✅ Forged clean lead\n\n### Forged heading\n<!-- verdict-footer -->\nPing @mallory \u202ebidi",
		"findings", []any{f},
	)
	return &reviewdoc.RenderInput{
		Review: review,
		Approval: &core.ApprovalReceipt{
			Approved: false,
			Reason:   "open findings at-or-above important",
			Tier:     "important",
		},
		Kept: []any{f},
		BundleMeta: reviewdoc.BundleMeta{
			HeadSHA:     testHeadFull,
			HeadRef:     ">feat/bad--branch-",
			BaseSHA:     testBaseFull,
			BaseRef:     "main",
			Mode:        "Review (initial)",
			PriorReview: "none",
			Commits:     []reviewdoc.MetaCommit{{SHA: testHeadFull, Subject: "break --> <script>alert(1)</script>"}},
			FileCount:   1,
		},
		BaseURL:  testBaseURL,
		HeadFull: testHeadFull,
		Now:      testNow,
	}
}

func TestRenderBodyDeterministicOrdering(t *testing.T) {
	t.Parallel()
	a := jq.NewObj("sig", "sig-a", "status", "open", "severity", "important", "rigor", "walked",
		"anchored", true, "inBounds", true, "path", "b.go", "line", 10, "claim", "Important B.",
		"verification", jq.NewObj("quote", "verified"))
	b := jq.NewObj("sig", "sig-b", "status", "open", "severity", "critical", "rigor", "ran",
		"anchored", true, "inBounds", true, "path", "z.go", "line", 2, "claim", "Critical Z.",
		"verification", jq.NewObj("quote", "verified"))
	c := jq.NewObj("sig", "sig-c", "status", "open", "severity", "important", "rigor", "walked",
		"anchored", true, "inBounds", true, "path", "a.go", "line", 5, "claim", "Important A.",
		"verification", jq.NewObj("quote", "verified"))

	mk := func(order []any) string {
		return reviewdoc.Render(&reviewdoc.RenderInput{
			Review:   jq.NewObj("findings", order),
			Approval: &core.ApprovalReceipt{Approved: false, Reason: "open findings", Tier: "important"},
			Kept:     order,
			BaseURL:  testBaseURL,
			HeadFull: testHeadFull,
		})
	}
	first := mk([]any{a, b, c})
	second := mk([]any{c, a, b})
	third := mk([]any{b, c, a})
	if first != second || first != third {
		t.Fatalf("RenderBody order-dependent:\nfirst:\n%s\nsecond:\n%s\nthird:\n%s", first, second, third)
	}
	idxCrit := strings.Index(first, "Critical Z.")
	idxImpA := strings.Index(first, "Important A.")
	idxImpB := strings.Index(first, "Important B.")
	if idxCrit >= idxImpA || idxImpA >= idxImpB {
		t.Fatalf("unexpected rank order: crit=%d impA=%d impB=%d\n%s", idxCrit, idxImpA, idxImpB, first)
	}
}

func TestRenderBodyCapsOverflow(t *testing.T) {
	t.Parallel()
	commits := make([]reviewdoc.MetaCommit, 23)
	for i := range commits {
		commits[i] = reviewdoc.MetaCommit{SHA: testHeadFull, Subject: "commit " + strconv.Itoa(i)}
	}
	files := make([]reviewdoc.MetaFile, 10)
	for i := range files {
		files[i] = reviewdoc.MetaFile{Path: "pkg/f" + strconv.Itoa(i) + ".go"}
	}
	gaps := make([]string, 8)
	for i := range gaps {
		gaps[i] = "gap " + strconv.Itoa(i)
	}
	got := reviewdoc.Render(&reviewdoc.RenderInput{
		Review:   jq.NewObj("findings", []any{}),
		Approval: &core.ApprovalReceipt{Approved: false, Insufficient: true, Reason: "missing evidence"},
		Evidence: &core.EvidenceReceipt{Gaps: gaps, Insufficient: true},
		BundleMeta: reviewdoc.BundleMeta{
			HeadSHA:   testHeadFull,
			Commits:   commits,
			Files:     files,
			FileCount: len(files),
		},
	})
	if !strings.Contains(got, "  - (+3 more)") {
		t.Errorf("missing commit overflow line:\n%s", got)
	}
	if !strings.Contains(got, "  - (+3 more — see record.json)") {
		t.Errorf("missing gap overflow line:\n%s", got)
	}

	cleanGot := reviewdoc.Render(&reviewdoc.RenderInput{
		Review:     jq.NewObj("findings", []any{}),
		Approval:   &core.ApprovalReceipt{Approved: true, Reason: "clean"},
		BundleMeta: reviewdoc.BundleMeta{HeadSHA: testHeadFull, Files: files, FileCount: len(files)},
	})
	if !strings.Contains(cleanGot, "(+3 more — see toc.txt)") {
		t.Errorf("missing checked-files overflow line:\n%s", cleanGot)
	}
}

func TestRenderBodyLeftSideFindingRendersCodeSpanNotHeadPermalink(t *testing.T) {
	t.Parallel()
	left := jq.NewObj(
		"sig", "sig-left", "status", "open", "severity", "important", "rigor", "walked",
		"anchored", true, "inBounds", true, "path", "src/deleted.go",
		"start_line", 10, "line", 12, "side", "LEFT",
		"claim", "Removed guard leaves caller exposed.",
		"verification", jq.NewObj("quote", "verified"),
	)
	right := jq.NewObj(
		"sig", "sig-right", "status", "open", "severity", "informational", "rigor", "pointed",
		"anchored", true, "inBounds", true, "path", "src/added.go",
		"line", 20, "side", "RIGHT",
		"claim", "New helper can be simplified.",
		"verification", jq.NewObj("quote", "verified"),
	)
	got := reviewdoc.Render(&reviewdoc.RenderInput{
		Review:   jq.NewObj("findings", []any{left, right}),
		Approval: &core.ApprovalReceipt{Approved: false, Reason: "open findings", Tier: "important"},
		Kept:     []any{left, right},
		BaseURL:  testBaseURL,
		HeadFull: testHeadFull,
	})
	if !strings.Contains(got, "1. ⚠️ **important** `src/deleted.go:10-12` (`walked`) — Removed guard leaves caller exposed.") {
		t.Errorf("LEFT finding did not render as a code span:\n%s", got)
	}
	if strings.Contains(got, "src/deleted.go#L10-L12") {
		t.Errorf("LEFT finding linked to HEAD blob at old line numbers:\n%s", got)
	}
	if !strings.Contains(got, "2. ℹ️ **informational** [src/added.go:20][ev-1] (`pointed`) — New helper can be simplified.") ||
		!strings.Contains(got, "[ev-1]: https://github.com/acme/app/blob/"+testHeadFull+"/src/added.go#L20") {
		t.Errorf("RIGHT finding lost its HEAD permalink:\n%s", got)
	}
}

func TestRenderBodyItemTenIndentAndHeadFullMetaFallback(t *testing.T) {
	t.Parallel()
	findings := make([]any, 0, 10)
	for i := 1; i <= 10; i++ {
		f := jq.NewObj(
			"sig", "sig-"+strconv.Itoa(100+i),
			"status", "open",
			"severity", "informational",
			"rigor", "pointed",
			"anchored", true,
			"inBounds", true,
			"path", "src/f_"+strconv.Itoa(100+i)+".go",
			"line", i,
			"claim", "Item "+strconv.Itoa(i)+" claim.",
			"verification", jq.NewObj("quote", "verified"),
		)
		if i == 10 {
			f.Set("brief", jq.NewObj("outcome", "Indent item 10 details by four spaces."))
		}
		findings = append(findings, f)
	}
	got := reviewdoc.Render(&reviewdoc.RenderInput{
		Review:   jq.NewObj("findings", findings),
		Approval: &core.ApprovalReceipt{Approved: true, Reason: "no gating findings", Tier: "important"},
		Kept:     findings,
		BaseURL:  testBaseURL,
		HeadFull: testHeadFull,
	})
	if !strings.Contains(got, "Findings written against a1b2c3d;") || !strings.Contains(got, "- Head: a1b2c3d") {
		t.Errorf("metadata comment did not fall back to HeadFull:\n%s", got)
	}
	want10 := "10. ℹ️ **informational** [src/f_110.go:10][ev-10] (`pointed`) — Item 10 claim.\n\n" +
		"    <details><summary>Technical details</summary>\n\n    ````markdown\n"
	if !strings.Contains(got, want10) {
		t.Errorf("item 10 continuation block not indented by 4 spaces:\n%s", got)
	}
}
