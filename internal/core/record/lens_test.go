package recordcore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestBuildEntriesPersistsLens proves one finding's stored lens reaches the
// entry record writes: the fresh wire carries the key, and a finding that
// spells none writes no key at all.
func TestBuildEntriesPersistsLens(t *testing.T) {
	withLens := jq.NewObj(fieldPath, "notes.txt", fieldLine, 2, "claim", "A defect with enough substance.",
		fieldQuote, "new line", "confidence", "high", fieldLens, "perf")
	st := &state{kept: []any{withLens}, now: "2026-11-13T12:00:00Z", headShort: "aaaaaaa"}
	st.buildEntries()
	if len(st.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(st.entries))
	}
	if v, ok := st.entries[0].(*jq.Obj); !ok {
		t.Fatalf("entry is not an object")
	} else if got, _ := v.Get(fieldLens); got != "perf" {
		t.Fatalf("lens = %v, want %q", got, "perf")
	}

	withoutLens := jq.NewObj(fieldPath, "notes.txt", fieldLine, 2, "claim", "A defect with enough substance.",
		fieldQuote, "new line", "confidence", "high")
	st = &state{kept: []any{withoutLens}, now: "2026-11-13T12:00:00Z", headShort: "aaaaaaa"}
	st.buildEntries()
	if len(st.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(st.entries))
	}
	if v, ok := st.entries[0].(*jq.Obj); !ok {
		t.Fatalf("entry is not an object")
	} else if _, ok := v.Get(fieldLens); ok {
		t.Fatalf("entry spells lens without an incoming one: %s", v.MarshalCompact())
	}
}

// TestMergeLensUpdatesAndPreserves proves the re-review merge: a spelled
// incoming lens replaces the prior one, an omitted one keeps it, and a pair
// that spells none stays lens-free.
func TestMergeLensUpdatesAndPreserves(t *testing.T) {
	st := &state{now: "2026-11-13T12:00:00Z", headShort: "aaaaaaa"}
	prior := jq.NewObj(fieldSig, "abc", fieldLens, "perf", fieldStatus, statusOpen,
		fieldPath, "notes.txt", fieldLine, 2, fieldBody, "Body here.", fieldAnchored, true,
		fieldInBounds, true, fieldRigor, "pointed", fieldRigorSource, "model", fieldSeverity, "important")
	fresh := func(lens string) *jq.Obj {
		n := jq.NewObj(fieldSig, "abc", fieldPath, "notes.txt", fieldLine, 2, fieldBody, "Body here.",
			fieldAnchored, true, fieldInBounds, true, fieldRigor, "pointed", fieldRigorSource, "model",
			fieldSeverity, "important")
		if lens != "" {
			n.Set(fieldLens, lens)
		}
		return n
	}

	merged := mergeFields(st, prior.Clone(), fresh("security"))
	if got, _ := merged.Get(fieldLens); got != "security" {
		t.Fatalf("lens = %v, want the incoming %q", got, "security")
	}
	merged = mergeFields(st, prior.Clone(), fresh(""))
	if got, _ := merged.Get(fieldLens); got != "perf" {
		t.Fatalf("lens = %v, want the prior %q preserved", got, "perf")
	}
	priorNoLens := prior.Clone()
	priorNoLens.Delete(fieldLens)
	merged = mergeFields(st, priorNoLens, fresh(""))
	if _, ok := merged.Get(fieldLens); ok {
		t.Fatalf("lens present on a pair that spells none: %s", merged.MarshalCompact())
	}
}

// TestPriorShapeLens proves a corrupt (non-string) lens in the prior store
// refuses as corrupt-store, while string or absent lenses pass.
func TestPriorShapeLens(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "findings.json"),
		[]byte(`[{"sig":"abc","lens":7},{"sig":"def","lens":"perf"},{"sig":"ghi"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := checkPriorShape(dir)
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != codeCorruptStore {
		t.Fatalf("ref = %+v, want %s", err, codeCorruptStore)
	}

	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "findings.json"),
		[]byte(`[{"sig":"def","lens":"perf"},{"sig":"ghi"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ref := checkPriorShape(dir); ref != nil {
		t.Fatalf("ref = %v, want nil for string or absent lens", ref)
	}
}
