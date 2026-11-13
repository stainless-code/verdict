package recordcore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestRecordPanelReceipt drives receipt validation and merge through record.
func TestRecordPanelReceipt(t *testing.T) {
	coverageBundle(t, coverageDiff, coverageReview("notes.txt", ""))
	writeFile(t, ".verdict/state/local-cov/verification/verdicts.json", `{"schema_version":"1","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","results":[{"sig":"101d0761051f365731b9b857810c9b3603c50208","model":"verifier","verdict":"refuted","evidence":"The caller already validates this input."}]}`)
	c := &collector{}
	response, ref := recordcore.Run(context.Background(), recordRequest(t, c, coverageSlug))
	if ref != nil {
		t.Fatal(ref)
	}
	if response.Data.Verification == nil || response.Data.Verification.Contested != 1 {
		t.Fatalf("record verification=%+v, want one contested finding", response.Data.Verification)
	}
	if response.Data.Findings.Open != 1 {
		t.Fatalf("open=%d, want 1", response.Data.Findings.Open)
	}
}

// TestPostPanelDryRun keeps contested findings and posts only summary counts.
func TestPostPanelDryRun(t *testing.T) {
	postCoverageBundle(t, coverageReview("notes.txt", ""))
	writeFile(t, ".verdict/state/7/verification/verdicts.json", `{"schema_version":"1","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","results":[{"sig":"101d0761051f365731b9b857810c9b3603c50208","model":"verifier","verdict":"refuted","evidence":"PRIVATE_VERIFIER_EVIDENCE: caller already validates."}]}`)
	response, ref := postcore.Run(context.Background(), postRequest(t, core.PostFlags{DryRun: true, Approve: "false", ReviewFile: ".verdict/state/7/review.json"}))
	if ref != nil {
		t.Fatal(ref)
	}
	if response.Data.Verification == nil || response.Data.Verification.Contested != 1 {
		t.Fatalf("verification=%+v", response.Data.Verification)
	}
	raw := jq.PrettyOrdered(response.Data.Wire())
	if strings.Contains(raw, "PRIVATE_VERIFIER_EVIDENCE") {
		t.Fatalf("post envelope leaked evidence: %s", raw)
	}
	if response.Data.Anchors.Kept != 1 {
		t.Fatalf("refutation removed finding: %+v", response.Data.Anchors)
	}
}
