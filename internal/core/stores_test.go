package core_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestFindingEntryOfPreservesNullCategoryPresence proves the typed decode
// keeps an explicitly null category key: the re-emit must carry the key with
// null, while an absent key stays absent. Presence is the repost contract
// (absent preserves the prior label, explicit null clears it).
func TestFindingEntryOfPreservesNullCategoryPresence(t *testing.T) {
	explicit := jq.NewObj("kind", "inline", "category", nil)
	if !explicit.Has("category") {
		t.Fatal("fixture lost the explicit null category key")
	}
	wired := core.FindingEntryOf(explicit).Wire()
	v, ok := wired.Get("category")
	if !ok {
		t.Error("Wire dropped the explicit null category key")
	}
	if v != nil {
		t.Errorf("Wire category = %#v, want null", v)
	}

	absent := jq.NewObj("kind", "inline")
	if _, ok := core.FindingEntryOf(absent).Wire().Get("category"); ok {
		t.Error("Wire gained a category key the input never carried")
	}

	labeled := jq.NewObj("kind", "inline", "category", "security")
	if v, ok := core.FindingEntryOf(labeled).Wire().Get("category"); !ok || v != "security" {
		t.Errorf("Wire category = %#v (present=%v), want security", v, ok)
	}
}
