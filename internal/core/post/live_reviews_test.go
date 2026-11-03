package postcore

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	liveGuardHead  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	liveGuardOther = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	liveGuardHash  = "cccccccccccccccccccccccccccccccccccccccc"
)

// writeLiveReview writes one reviews.json page with a single review.
func writeLiveReview(t *testing.T, dir, body, commitID string) {
	t.Helper()
	raw := fmt.Sprintf(`[{"id":1,"body":%s,"commit_id":%s,"submitted_at":"2026-01-01T00:00:00Z"}]`,
		strconv.Quote(body), strconv.Quote(commitID))
	if err := writeTestFile(dir+"/reviews.json", raw); err != nil {
		t.Fatal(err)
	}
}

// postedMarker renders the hidden tail for guard tests.
func postedMarker(hash, head string) string {
	return fmt.Sprintf(reviewMarkerFormat, hash, head)
}

// markedReview is a verdict-posted body: prose plus the hidden marker.
func markedReview(head string) string {
	return "> ✅ Looks good.\n\n" + postedMarker(liveGuardHash, head)
}

// TestFinalizerGuardLiveReviews proves the coarse guard also reads the
// bundle's fetched reviews: a verdict-marked review on the reviewed SHA
// refuses, while another commit, another bot's review, a marker quoted
// mid-body, and an old hash-less marker all proceed.
//
//nolint:gocognit,revive // table legs assert code and message inline
func TestFinalizerGuardLiveReviews(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		commitID string
		want     string
		force    bool
	}{
		{"marker-on-same-sha", markedReview(liveGuardHead), liveGuardHead, "already-posted", false},
		{"marker-on-other-sha", markedReview(liveGuardOther), liveGuardOther, "", false},
		{"no-marker", "> ✅ another bot's review\n\nReviewed by tooling.", liveGuardHead, "", false},
		{"marker-quoted-mid-body", "quoted marker:\n" + postedMarker(liveGuardHash, liveGuardHead) + "\ntail", liveGuardHead, "", false},
		{"old-hash-less-marker", "> ✅ old\n\n<!-- verdict-review head=" + liveGuardHead + " -->", liveGuardHead, "", false},
		{"force-overrides", markedReview(liveGuardHead), liveGuardHead, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeLiveReview(t, dir, tc.body, tc.commitID)
			var warnings []string
			p := postTestState(dir, liveGuardHead, core.PostFlags{Force: tc.force}, &warnings)
			ref := p.finalizerGuard()
			if tc.want == "" {
				if ref != nil {
					t.Fatalf("proceed case refused: %v", ref)
				}
				return
			}
			if ref == nil || ref.Code != tc.want {
				t.Fatalf("refusal = %v, want code %q", ref, tc.want)
			}
			for _, piece := range []string{"--force", "--base-sha aaaaaaa", "2026-01-01T00:00:00Z"} {
				if !strings.Contains(ref.Error(), piece) {
					t.Errorf("refusal misses %q: %v", piece, ref)
				}
			}
		})
	}
}

// TestFinalizerGuardLiveReviewsUnreadable proves a corrupt reviews.json
// warns and proceeds, never wedging the run.
func TestFinalizerGuardLiveReviewsUnreadable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := writeTestFile(dir+"/reviews.json", "not json{"); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	p := postTestState(dir, liveGuardHead, core.PostFlags{}, &warnings)
	if ref := p.finalizerGuard(); ref != nil {
		t.Fatalf("corrupt reviews refused: %v", ref)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "live reviews record unparseable") {
		t.Errorf("warnings = %q, want the unparseable notice", warnings)
	}
}

// TestDuplicateGuardLiveReviews proves the fine guard reads the fetched
// reviews: the same payload hash refuses, a different hash, another bot's
// review, a mid-body quote, and --force all proceed.
//
//nolint:revive // table legs assert code and message inline
func TestDuplicateGuardLiveReviews(t *testing.T) {
	t.Parallel()
	otherHash := strings.Repeat("d", markerHashLen)
	tests := []struct {
		name  string
		body  string
		phash string
		want  string
		force bool
	}{
		{"same-hash", markedReview(liveGuardHead), liveGuardHash, "duplicate-payload", false},
		{"different-hash", "> ✅ looks alike\n\n" + postedMarker(otherHash, liveGuardHead), liveGuardHash, "", false},
		{"no-marker", "> ✅ another bot's review\n\n<!-- verdict-footer -->", liveGuardHash, "", false},
		{"marker-quoted-mid-body", "quoted:\n" + postedMarker(liveGuardHash, liveGuardHead) + "\ntail", liveGuardHash, "", false},
		{"force-overrides", markedReview(liveGuardHead), liveGuardHash, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeLiveReview(t, dir, tc.body, liveGuardHead)
			var warnings []string
			p := postTestState(dir, liveGuardHead, core.PostFlags{Force: tc.force}, &warnings)
			p.phash = tc.phash
			ref := p.duplicateGuard()
			if tc.want == "" {
				if ref != nil {
					t.Fatalf("proceed case refused: %v", ref)
				}
				return
			}
			if ref == nil || ref.Code != tc.want {
				t.Fatalf("refusal = %v, want code %q", ref, tc.want)
			}
			if !strings.Contains(ref.Error(), "--force") {
				t.Errorf("refusal misses --force: %v", ref)
			}
		})
	}
}

// TestDuplicateGuardLocalMarker proves the local dot-marker still refuses
// without reading reviews.json.
func TestDuplicateGuardLocalMarker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := writeTestFile(dir+"/.posted_"+liveGuardHash, ""); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	p := postTestState(dir, liveGuardHead, core.PostFlags{}, &warnings)
	p.phash = liveGuardHash
	if ref := p.duplicateGuard(); ref == nil || ref.Code != "duplicate-payload" {
		t.Fatalf("local marker refusal = %v, want duplicate-payload", ref)
	}
}

// TestReviewMarker proves the parser accepts the exact posted tail (with
// trailing whitespace) and rejects a marker that is not the last line.
func TestReviewMarker(t *testing.T) {
	t.Parallel()
	body := "> ✅ done\n\n" + postedMarker(liveGuardHash, liveGuardHead) + "\n"
	hash, head, ok := reviewMarker(body)
	if !ok || hash != liveGuardHash || head != liveGuardHead {
		t.Fatalf("reviewMarker = %q, %q, %v", hash, head, ok)
	}
	if _, _, ok := reviewMarker(postedMarker(liveGuardHash, liveGuardHead) + "\ntrailing"); ok {
		t.Error("mid-body marker parsed as the posted tail")
	}
}
