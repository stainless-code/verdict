package archtest

import (
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

const handoffCoreByteLimit = 3072

// TestReviewCoreStaysLean pins the pushed handoff, not the pull refs: the
// agent receives this assembly with every bundle, so a ref or conditional
// section may grow without moving the number this guard measures.
func TestReviewCoreStaysLean(t *testing.T) {
	t.Parallel()
	core, err := skillfs.ReviewCore()
	if err != nil {
		t.Fatalf("ReviewCore: %v", err)
	}
	if len(core) > handoffCoreByteLimit {
		t.Fatalf("pushed review core is %d bytes, limit %d", len(core), handoffCoreByteLimit)
	}
}
