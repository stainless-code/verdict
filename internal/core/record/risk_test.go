package recordcore_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

type riskCheckCase struct {
	name      string
	extra     string
	wantCode  string
	wantLevel string
	wantPath  string
	wantCount int
}

func TestCheckReviewRiskTable(t *testing.T) {
	t.Parallel()
	const validRisk = `,"risk":{"level":"medium","rationale":"Touches core state transitions in two modules.","assessed_head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	tests := []riskCheckCase{
		{
			name:      "valid",
			extra:     validRisk,
			wantCount: 0,
		},
		{
			name:      "bad-enum",
			extra:     `,"risk":{"level":"extreme","rationale":"Touches core state transitions.","assessed_head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
			wantCount: 1,
			wantCode:  core.ProblemCodeBadEnum,
			wantLevel: core.ProblemLevelError,
			wantPath:  "$.risk.level",
		},
		{
			name:      "missing-rationale",
			extra:     `,"risk":{"level":"low","assessed_head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
			wantCount: 1,
			wantCode:  core.ProblemCodeMissingField,
			wantLevel: core.ProblemLevelError,
			wantPath:  "$.risk.rationale",
		},
		{
			name:      "missing-assessed-head",
			extra:     `,"risk":{"level":"low","rationale":"Pure comment tweak with no behavior change."}`,
			wantCount: 1,
			wantCode:  core.ProblemCodeMissingField,
			wantLevel: core.ProblemLevelError,
			wantPath:  "$.risk.assessed_head",
		},
		{
			name:      "short-rationale",
			extra:     `,"risk":{"level":"low","rationale":"too short","assessed_head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
			wantCount: 1,
			wantCode:  core.ProblemCodeOutOfRange,
			wantLevel: core.ProblemLevelError,
			wantPath:  "$.risk.rationale",
		},
		{
			name:      "long-rationale",
			extra:     `,"risk":{"level":"low","rationale":"` + strings.Repeat("r", 501) + `","assessed_head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
			wantCount: 1,
			wantCode:  core.ProblemCodeOutOfRange,
			wantLevel: core.ProblemLevelError,
			wantPath:  "$.risk.rationale",
		},
		{
			name:      "unknown-field",
			extra:     `,"risk":{"level":"low","rationale":"Pure comment tweak with no behavior change.","assessed_head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","score":3}`,
			wantCount: 1,
			wantCode:  core.ProblemCodeUnknownField,
			wantLevel: core.ProblemLevelWarning,
			wantPath:  "$.risk.score",
		},
		{
			name:      "non-object",
			extra:     `,"risk":"low"`,
			wantCount: 1,
			wantCode:  core.ProblemCodeWrongType,
			wantLevel: core.ProblemLevelError,
			wantPath:  "$.risk",
		},
	}

	for i := range tests {
		tc := tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertRiskCheckCase(t, &tc)
		})
	}
}

func assertRiskCheckCase(t *testing.T, tc *riskCheckCase) {
	t.Helper()
	res := recordcore.CheckReview([]byte(checkReview(tc.extra)), checkDiff)
	checkProblems(t, res, tc.wantCount)
	if tc.wantCount == 0 {
		return
	}
	p := res.Problems[0]
	if p.Code != tc.wantCode || p.Level != tc.wantLevel || p.Path != tc.wantPath {
		t.Errorf("problem = %+v, want code=%s level=%s path=%s", p, tc.wantCode, tc.wantLevel, tc.wantPath)
	}
}

func TestNormalizeRiskCaseFoldsLevel(t *testing.T) {
	t.Parallel()
	in := []byte(checkReview(`,"risk":{"level":"HIGH","rationale":"Cross-cutting refactor with sparse tests.","assessed_head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	norm, repairs := recordcore.NormalizeReview(in)
	if len(repairs) != 1 || repairs[0].Code != core.ProblemCodeEnumSynonym {
		t.Fatalf("NormalizeReview repairs = %+v, want 1 enum-synonym", repairs)
	}
	res := recordcore.CheckReview(norm, checkDiff)
	checkProblems(t, res, 0)
	if !strings.Contains(string(norm), `"level": "high"`) {
		t.Errorf("normalized output did not lowercase level: %s", string(norm))
	}
}

func TestCheckBundleRiskStaleHead(t *testing.T) {
	const slug = "local-stale-risk"
	bundle(t, slug, checkReview(`,"risk":{"level":"low","rationale":"Small refactor against older commit.","assessed_head":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`))
	dir := ".verdict/state/" + slug

	res, ref := recordcore.CheckBundle(dir, []byte(checkReview(`,"risk":{"level":"low","rationale":"Small refactor against older commit.","assessed_head":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)))
	if ref != nil {
		t.Fatalf("CheckBundle refused: %+v", ref)
	}
	checkProblems(t, res, 1)
	p := res.Problems[0]
	if p.Code != core.ProblemCodeRiskStaleHead || p.Level != core.ProblemLevelWarning || p.Path != "$.risk.assessed_head" {
		t.Fatalf("problem = %+v, want stale-head warning at $.risk.assessed_head", p)
	}

	fresh := checkReview(`,"risk":{"level":"low","rationale":"Small refactor against current commit.","assessed_head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	res2, ref2 := recordcore.CheckBundle(dir, []byte(fresh))
	if ref2 != nil {
		t.Fatalf("CheckBundle refused on fresh head: %+v", ref2)
	}
	checkProblems(t, res2, 0)
}

func TestRunRecordRiskPersistence(t *testing.T) {
	t.Run("fresh-claim-persists", testFreshRiskRecord)
	t.Run("stale-claim-marks-stale-and-strict-refuses", testStaleRiskRecord)
	t.Run("absent-risk-omitted-in-record-json", testAbsentRiskRecord)
}

func testFreshRiskRecord(t *testing.T) {
	const slug = "local-risk-fresh"
	bundle(t, slug, reviewJSON("")[:len(reviewJSON(""))-1]+`,"risk":{"level":"medium","rationale":"Changes validation rules and adds new fields.","assessed_head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`)
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, slug))
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	if resp.Data.Risk == nil || resp.Data.Risk.Level != "medium" || resp.Data.Risk.Stale {
		t.Fatalf("Data.Risk = %+v, want medium non-stale", resp.Data.Risk)
	}
	rm := readRecordRiskMap(t, slug)
	if rm["level"] != "medium" || rm["assessed_head"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("record.json risk = %#v", rm)
	}
	if _, hasStale := rm["stale"]; hasStale {
		t.Fatalf("fresh risk claim should omit stale flag: %#v", rm)
	}
}

func readRecordRiskMap(t *testing.T, slug string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(".verdict/state/" + slug + "/record.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	rm, ok := doc["risk"].(map[string]any)
	if !ok {
		t.Fatalf("record.json risk = %#v", doc["risk"])
	}
	return rm
}

func testStaleRiskRecord(t *testing.T) {
	const slug = "local-risk-stale"
	bundle(t, slug, reviewJSON("")[:len(reviewJSON(""))-1]+`,"risk":{"level":"low","rationale":"Assessed against stale head commit before push.","assessed_head":"cccccccccccccccccccccccccccccccccccccccc"}}`)
	req := recordRequest(t, &collector{}, slug)
	req.Strict = true
	if _, ref := recordcore.Run(context.Background(), req); ref == nil || ref.Code != "bad-review-input" {
		t.Fatalf("strict Run ref = %+v, want bad-review-input", ref)
	}

	req.Strict = false
	resp, ref := recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("non-strict Run refused: %+v", ref)
	}
	if resp.Data.Risk == nil || !resp.Data.Risk.Stale {
		t.Fatalf("Data.Risk = %+v, want Stale=true", resp.Data.Risk)
	}
}

func testAbsentRiskRecord(t *testing.T) {
	const slug = "local-risk-none"
	bundle(t, slug, reviewJSON(""))
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, &collector{}, slug))
	if ref != nil {
		t.Fatalf("Run refused: %+v", ref)
	}
	if resp.Data.Risk != nil {
		t.Fatalf("Data.Risk = %+v, want nil", resp.Data.Risk)
	}
	raw, err := os.ReadFile(".verdict/state/" + slug + "/record.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"risk"`) {
		t.Fatalf("record.json unexpectedly contains risk key: %s", string(raw))
	}
}
