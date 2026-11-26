package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestEvidenceReceiptJSONRoundTrip(t *testing.T) {
	t.Parallel()
	in := EvidenceReceipt{
		Head:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DiffSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Quotes: []EvidenceQuote{
			{Path: "pkg/a.go", Side: "RIGHT", Status: "verified", Line: 12},
			{Path: "pkg/b.go", Side: "LEFT", Status: "unverified", Reason: "text-differs", Line: 7},
		},
		Gaps: []string{"quote mismatch at pkg/b.go:7 (text-differs)"},
		Receipts: EvidenceReceipts{
			Total:    2,
			Covered:  1,
			Exempt:   1,
			Complete: false,
		},
		Spec: EvidenceSpec{
			Total:   5,
			Covered: 4,
			Min:     80,
			Met:     true,
		},
		Insufficient: true,
	}

	gotBytes, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	const wantJSON = `{"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
		`"diff_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",` +
		`"quotes":[{"path":"pkg/a.go","side":"RIGHT","status":"verified","line":12},` +
		`{"path":"pkg/b.go","side":"LEFT","status":"unverified","reason":"text-differs","line":7}],` +
		`"gaps":["quote mismatch at pkg/b.go:7 (text-differs)"],` +
		`"receipts":{"total":2,"covered":1,"exempt":1,"complete":false},` +
		`"spec":{"total":5,"covered":4,"min":80,"met":true},` +
		`"insufficient":true}`
	if string(gotBytes) != wantJSON {
		t.Fatalf("marshal =\n%s\nwant:\n%s", gotBytes, wantJSON)
	}

	var decoded EvidenceReceipt
	if err := json.Unmarshal(gotBytes, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(decoded, in) {
		t.Fatalf("round-trip decoded = %+v, want %+v", decoded, in)
	}
}

func TestApprovalReceiptInsufficientJSON(t *testing.T) {
	t.Parallel()
	clean, err := json.Marshal(ApprovalReceipt{
		Reason:     "no gating findings",
		Tier:       "important",
		GatingSigs: []string{},
		Approved:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	const wantClean = `{"reason":"no gating findings","tier":"important","gating_sigs":[],"approved":true}`
	if string(clean) != wantClean {
		t.Fatalf("clean approval = %s, want %s", clean, wantClean)
	}

	insuf, err := json.Marshal(ApprovalReceipt{
		Reason:       "review-insufficient: hunk pkg/a.go\tNEW\t1\t3 has no matched receipt",
		Tier:         "important",
		GatingSigs:   []string{},
		Approved:     false,
		Insufficient: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	const wantInsuf = `{"reason":"review-insufficient: hunk pkg/a.go\tNEW\t1\t3 has no matched receipt",` +
		`"tier":"important","gating_sigs":[],"approved":false,"insufficient":true}`
	if string(insuf) != wantInsuf {
		t.Fatalf("insufficient approval = %s, want %s", insuf, wantInsuf)
	}
}

// TestReviewInsufficientHintNamesListedGapsOnly proves the hint points at a
// list of evidence gaps only when the refusal carries one: a refusal whose
// message is the one gap gets a hint that sends the reader to the message.
func TestReviewInsufficientHintNamesListedGapsOnly(t *testing.T) {
	t.Parallel()
	listed := InsufficientRefusal("1 hunk without receipt", []Problem{ReceiptMissingProblem("pkg/a.go\tNEW\t1\t5")})
	if got := RefusalHint(listed); !strings.HasPrefix(got, "try: fix the evidence gaps named above ") {
		t.Errorf("listed gaps hint = %q, want the hint naming the listed gaps", got)
	}
	unlisted := &RefusalError{Code: CodeReviewInsufficient, Message: "execution: execution manifest is incomplete"}
	if got := RefusalHint(unlisted); got != "try: address the gap the message above names, then re-run" {
		t.Errorf("unlisted gap hint = %q", got)
	}
}

func TestReviewInsufficientHintAndRefusal(t *testing.T) {
	t.Parallel()
	const wantHint = "try: fix the evidence gaps named above " +
		"(quote at path:line, receipt hunk key, spec item), rewrite review.json, then re-run record or run --rounds"
	if got := HintFor("review-insufficient"); got != wantHint {
		t.Fatalf("HintFor(review-insufficient) = %q, want %q", got, wantHint)
	}

	prob := ReceiptMissingProblem("pkg/a.go\tNEW\t1\t5")
	ref := InsufficientRefusal("1 hunk without receipt", []Problem{prob})
	if ref.Code != "review-insufficient" {
		t.Fatalf("ref.Code = %q, want %q", ref.Code, "review-insufficient")
	}
	if ref.Message != "1 hunk without receipt" {
		t.Fatalf("ref.Message = %q, want %q", ref.Message, "1 hunk without receipt")
	}
	if len(ref.Problems) != 1 || ref.Problems[0].Code != "receipt-missing" {
		t.Fatalf("ref.Problems = %+v, want one receipt-missing problem", ref.Problems)
	}

	fallback := InsufficientRefusal("   ", nil)
	if fallback.Message != "evidence checks insufficient" {
		t.Fatalf("fallback.Message = %q, want %q", fallback.Message, "evidence checks insufficient")
	}
}

func TestEvidenceProblemCodesInPublishedSchema(t *testing.T) {
	t.Parallel()
	wantNew := []string{
		"quote-mismatch",
		"quote-not-checkable",
		"receipt-missing",
		"receipt-hunk-unknown",
		"receipt-mismatch",
		"spec-item-unknown",
		"spec-check-missing",
		"spec-evidence-not-code",
		"spec-evidence-mismatch",
	}
	published := ProblemCodes()
	assertContainsAll(t, "ProblemCodes()", published, wantNew)
	if slices.Contains(published, "quote-range-invalid") {
		t.Errorf("ProblemCodes() must not contain removed code %q", "quote-range-invalid")
	}

	enum := readEnvelopeProblemEnum(t)
	assertContainsAll(t, "envelope.schema.json problem.code enum", enum, wantNew)
}

func assertContainsAll(t *testing.T, label string, got, want []string) {
	t.Helper()
	for _, code := range want {
		if !slices.Contains(got, code) {
			t.Errorf("%s missing %q", label, code)
		}
	}
}

func readEnvelopeProblemEnum(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "envelope.schema.json"))
	if err != nil {
		t.Fatalf("read envelope.schema.json: %v", err)
	}
	var doc struct {
		Defs struct {
			Problem struct {
				Properties struct {
					Code struct {
						Enum []string `json:"enum"`
					} `json:"code"`
				} `json:"properties"`
			} `json:"problem"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal envelope.schema.json: %v", err)
	}
	return doc.Defs.Problem.Properties.Code.Enum
}

func TestQuoteProblemCodeMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		reason string
		want   string
	}{
		{"", ""},
		{"text-differs", "quote-mismatch"},
		{"deleted-line", "quote-mismatch"},
		{"range-crosses-hunks", "quote-mismatch"},
		{"range-invalid", "quote-mismatch"},
		{"path-not-in-bundle", "quote-not-checkable"},
		{"line-not-in-bundle", "quote-not-checkable"},
		{"file-absent-at-head", "quote-not-checkable"},
		{"quote-too-large", "quote-not-checkable"},
	}
	for _, tc := range cases {
		if got := QuoteProblemCode(tc.reason); got != tc.want {
			t.Errorf("QuoteProblemCode(%q) = %q, want %q", tc.reason, got, tc.want)
		}
	}
}

func TestMissingProblemConstructors(t *testing.T) {
	t.Parallel()
	gotReceipt := ReceiptMissingProblem("a.go\tNEW\t1\t4")
	wantReceipt := Problem{
		Path:    "$.receipts",
		Code:    "receipt-missing",
		Level:   "error",
		Message: "hunk a.go\tNEW\t1\t4 has no matched receipt",
		Fix:     `add {"hunk":"a.go\tNEW\t1\t4","quote":"<exact line from that hunk>"}`,
	}
	if !reflect.DeepEqual(gotReceipt, wantReceipt) {
		t.Fatalf("ReceiptMissingProblem = %+v, want %+v", gotReceipt, wantReceipt)
	}

	gotSpec := SpecCheckMissingProblem(2, "must reject empty tokens")
	wantSpec := Problem{
		Path:    "$.spec_checks",
		Code:    "spec-check-missing",
		Level:   "error",
		Message: `spec item 2 ("must reject empty tokens") has no check`,
		Fix:     "add a spec_checks[] entry quoting that item",
	}
	if !reflect.DeepEqual(gotSpec, wantSpec) {
		t.Fatalf("SpecCheckMissingProblem = %+v, want %+v", gotSpec, wantSpec)
	}
}
