package identity

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestCompletedPatchReviewIDEdges checks numeric precision and invalid IDs.
func TestCompletedPatchReviewIDEdges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id   any
		name string
		want bool
	}{
		{float64(0), "zero", false},
		{float64(-1), "negative", false},
		{1.5, "fractional", false},
		{math.NaN(), "nan", false},
		{math.Inf(1), "infinite", false},
		{float64(17), "positive", true},
		{json.Number("0"), "json-zero", false},
		{json.Number("-1"), "json-negative", false},
		{json.Number("1.5"), "json-fractional", false},
		{json.Number("9007199254740993.5"), "json-rounded-fraction", false},
		{json.Number("9007199254740993"), "json-large-integer", true},
		{json.Number("17.0"), "json-integral-decimal", true},
		{json.Number("17e2"), "json-integral-exponent", true},
		{json.Number("01"), "json-malformed", false},
		{jq.Number("0"), "jq-zero", false},
		{jq.Number("-1"), "jq-negative", false},
		{jq.Number("1.5"), "jq-fractional", false},
		{jq.Number("17"), "jq-positive", true},
		{" \t\n", "blank-string", false},
		{"review-17", "backend-string", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := completedReceipt(map[string]any{"id": tt.id, "state": "COMMENTED"}, completedHead, completedAt)
			_, _, got := CompletedPatch(receipt)
			if got != tt.want {
				t.Errorf("CompletedPatch(id=%v) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}

// TestCompletedPatchSHA256Head accepts SHA-256 publication heads.
func TestCompletedPatchSHA256Head(t *testing.T) {
	t.Parallel()
	receipt := completedReceipt(map[string]any{"id": json.Number("17"), "state": "COMMENTED"}, strings.Repeat("a", 64), completedAt)
	if _, _, ok := CompletedPatch(receipt); !ok {
		t.Fatal("CompletedPatch rejected a valid SHA-256 publication head")
	}
}

// TestVerifyRawDanglingSymlink distinguishes absence from a dangling entry.
func TestVerifyRawDanglingSymlink(t *testing.T) {
	t.Parallel()
	diff := testDiff + "\n"
	head, base := strings.Repeat("a", 64), strings.Repeat("b", 64)
	dir := verifyBundle(t, diff, head, base)
	if _, ok := Verify(context.Background(), nil, dir, head, base); !ok {
		t.Fatal("Verify rejected the genuinely absent raw control")
	}
	if err := os.Symlink("missing-raw-input", filepath.Join(dir, "full.raw.diff")); err != nil {
		t.Fatal(err)
	}
	if _, ok := Verify(context.Background(), nil, dir, head, base); ok {
		t.Fatal("Verify certified filtered bytes despite a present dangling raw entry")
	}
	if _, err := ReadRawDiff(dir, []byte(diff)); err == nil {
		t.Fatal("ReadRawDiff returned no error for a dangling raw entry")
	}
}
