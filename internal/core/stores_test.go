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

func TestFindingReproductionRoundTrip(t *testing.T) {
	raw := jq.NewObj("kind", "inline", "reproduction", jq.NewObj("path", "probe.py", "command", "python3 probe.py", "redacted", true))
	entry := core.FindingEntryOf(raw)
	if entry.Reproduction == nil || entry.Reproduction.Command != "python3 probe.py" || !entry.Reproduction.Redacted {
		t.Fatalf("decoded receipt = %+v", entry.Reproduction)
	}
	got, _ := entry.Wire().Get("reproduction")
	if jq.PrettyOrdered(got) != "{\n  \"path\": \"probe.py\",\n  \"command\": \"python3 probe.py\",\n  \"redacted\": true\n}\n" {
		t.Fatalf("wired receipt = %s", jq.Compact(got))
	}
}

func TestFindingReproductionPreservesUnknownMembers(t *testing.T) {
	const subtree = "{\n  \"path\": \"probe.py\",\n  \"command\": \"python3 probe.py\",\n  \"redacted\": false,\n  \"future\": {\n    \"z\": [\n      3,\n      2\n    ],\n    \"a\": \"retained\"\n  }\n}\n"
	value, err := jq.DecodeOrdered([]byte(subtree))
	if err != nil {
		t.Fatal(err)
	}
	entry := core.FindingEntryOf(jq.NewObj("kind", "inline", "reproduction", value))
	actual, _ := entry.Wire().Get("reproduction")
	if got := jq.PrettyOrdered(actual); got != subtree {
		t.Fatalf("reproduction changed:\n%s", got)
	}
}
