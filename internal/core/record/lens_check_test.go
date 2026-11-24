package recordcore_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
)

// lensReview renders one review carrying one finding whose comment spells
// the given extra keys.
func lensReview(extra string) string {
	return `{"schema_version":"1","summary":"A short summary of the clean review.","findings":[` +
		checkComment(extra) + `]}`
}

// TestCheckReviewLensAcceptsValidLens proves one optional lens spelled in
// the canonical grammar checks clean: no error, no warning.
func TestCheckReviewLensAcceptsValidLens(t *testing.T) {
	for _, tc := range []struct{ name, lens string }{
		{"short slug", "perf"},
		{"max length", strings.Repeat("a", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := recordcore.CheckReview(
				[]byte(lensReview(`,"rigor":"pointed","lens":"`+tc.lens+`"`)), checkDiff)
			if res.Errors != 0 || res.Warnings != 0 {
				t.Fatalf("CheckReview = %+v, want a clean review", res.Problems)
			}
		})
	}
}

// TestCheckReviewLensRefusesMalformedLens proves every malformed spelling
// refuses at the lens key: a non-string, the empty string, and ids outside
// the lowercase slug grammar.
func TestCheckReviewLensRefusesMalformedLens(t *testing.T) {
	for _, tc := range []struct{ name, extra, code string }{
		{"non-string", `,"lens":7`, core.ProblemCodeWrongType},
		{"empty", `,"lens":""`, core.ProblemCodeOutOfRange},
		{"uppercase", `,"lens":"Perf"`, core.ProblemCodeBadEnum},
		{"separator", `,"lens":"perf_test"`, core.ProblemCodeBadEnum},
		{"overlong", `,"lens":"` + strings.Repeat("a", 65) + `"`, core.ProblemCodeBadEnum},
		{"traversal", `,"lens":"../evil"`, core.ProblemCodeBadEnum},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := recordcore.CheckReview([]byte(lensReview(`,"rigor":"pointed"`+tc.extra)), checkDiff)
			if res.Errors == 0 || !hasLensProblem(res, tc.code) {
				t.Fatalf("problems = %+v, want one %s at .lens", res.Problems, tc.code)
			}
		})
	}
}

// hasLensProblem reports whether res carries one problem at a `.lens` path
// with the given code.
func hasLensProblem(res recordcore.CheckResult, code string) bool {
	for _, p := range res.Problems {
		if strings.HasSuffix(p.Path, ".lens") && p.Code == code {
			return true
		}
	}
	return false
}

// TestNormalizeDropsNullLens proves a spelled null lens is trivia the
// normalizer drops, so the checker and the store see no lens at all.
func TestNormalizeDropsNullLens(t *testing.T) {
	normalized, repairs := recordcore.NormalizeReview([]byte(lensReview(`,"rigor":"pointed","lens":null`)))
	if !hasRepairAt(repairs, ".lens") {
		t.Fatalf("repairs = %+v, want one at .lens", repairs)
	}
	finding := normalizedFinding(t, normalized)
	if _, ok := finding.Get("lens"); ok {
		t.Fatalf("lens survived normalization: %s", finding.MarshalCompact())
	}
}

// hasRepairAt reports whether repairs carries one problem at a path with the
// given suffix.
func hasRepairAt(repairs []recordcore.Problem, suffix string) bool {
	for _, p := range repairs {
		if strings.HasSuffix(p.Path, suffix) {
			return true
		}
	}
	return false
}

// normalizedFinding decodes one normalized review's first finding.
func normalizedFinding(t *testing.T, normalized []byte) *jq.Obj {
	t.Helper()
	doc, err := jq.DecodeOrdered(normalized)
	if err != nil {
		t.Fatal(err)
	}
	root, ok := doc.(*jq.Obj)
	if !ok {
		t.Fatal("normalized review is not an object")
	}
	findings, _ := root.Get("findings")
	items, ok := findings.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("findings = %v", findings)
	}
	finding, ok := items[0].(*jq.Obj)
	if !ok {
		t.Fatal("finding is not an object")
	}
	return finding
}
