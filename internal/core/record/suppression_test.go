package recordcore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func TestSuppressionAgentReceiptLifecycle(t *testing.T) {
	e := jq.NewObj("sig", "abcdef012345", "path", "a.go", "line", 2, "status", "open", "body", "finding", "history", []any{})
	s := jq.NewObj("path", "a.go", "line", 2, "source", "agent", "reason", "This is an intentional compatibility exception.")
	st := &state{review: jq.NewObj("suppressed", []any{s}), entries: []any{e}, now: "2026-11-13T12:00:00Z", cmd: "record"}
	applySuppressionForTest(t, st)
	if readcommon.WithheldSource(e) != "agent" || suppressionString(e, "status") != "open" {
		t.Fatalf("entry = %s", e.MarshalCompact())
	}
	kept, _ := PublicComments(st.entries, []any{jq.NewObj("body", "finding")}, []string{"abcdef012345"})
	if len(kept) != 0 {
		t.Fatal("withheld finding remained publishable")
	}
	st.now = "2026-11-14T12:00:00Z"
	applySuppressionForTest(t, st)
	w, _ := e.Get("withheld")
	obj, ok := w.(*jq.Obj)
	if !ok || suppressionString(obj, "at") != "2026-11-13T12:00:00Z" {
		t.Fatal("repeat suppression changed first timestamp")
	}
	st.review = jq.NewObj()
	applySuppressionForTest(t, st)
	if e.Has("withheld") {
		t.Fatal("suppression was not cleared")
	}
	h, _ := e.Get("history")
	if jq.NewObj("history", h).MarshalCompact() != `{"history":[{"at":"2026-11-13T12:00:00Z","event":"withheld","actor":"record","note":"This is an intentional compatibility exception."},{"at":"2026-11-14T12:00:00Z","event":"reopened","actor":"record","note":"withheld cleared"}]}` {
		t.Fatalf("history = %s", jq.NewObj("history", h).MarshalCompact())
	}
}

func TestBadSuppression(t *testing.T) {
	cases := []any{
		"typo",
		jq.NewObj("path", "a.go", "source", "agent", "reason", "too short"),
		jq.NewObj("path", "a.go", "source", "exclusion", "exclusion_id", "ignore:1", "reason", "excluded"),
		jq.NewObj("path", "a.go", "source", "below-severity", "reason", "tool only"),
	}
	for _, s := range cases {
		t.Run(jq.Compact(s), func(t *testing.T) {
			st := &state{dir: t.TempDir(), review: jq.NewObj("suppressed", []any{s})}
			_, err := st.parseSuppressions()
			var ref *core.RefusalError
			if !errors.As(err, &ref) || ref.Code != "bad-suppression" {
				t.Fatalf("got %v; want bad-suppression", err)
			}
		})
	}
}

func applySuppressionForTest(t *testing.T, st *state) {
	t.Helper()
	if err := st.applySuppressions(); err != nil {
		t.Fatal(err)
	}
}

func TestSuppressionExclusionReceipt(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/suppression/excluded.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/excluded.json", raw, 0o600); err != nil {
		t.Fatal(err)
	}
	e := jq.NewObj("sig", "abcdef", "path", "vendor/.env", "status", "open", "history", []any{})
	s := jq.NewObj("path", "vendor/.env", "source", "exclusion", "exclusion_id", "ignore:1", "reason", "Excluded at the trusted base.")
	st := &state{dir: dir, entries: []any{e}, review: jq.NewObj("suppressed", []any{s}), now: "2026-11-13T12:00:00Z"}
	applySuppressionForTest(t, st)
	w := readcommon.FieldOf(e, "withheld")
	if readcommon.WithheldSource(e) != "exclusion" || readcommon.FieldOf(w, "exclusion_id") != "ignore:1" {
		t.Fatalf("receipt = %s", e.MarshalCompact())
	}
}

func TestSuppressionMovedBodyHidden(t *testing.T) {
	moved := jq.NewObj("path", "a.go", "line", 2, "body", "finding", "dropReason", "out of range")
	entry := jq.NewObj("path", "a.go", "line", 2, "body", "finding", "withheld", jq.NewObj("source", "agent", "reason", "An intentional compatibility exception.", "at", "2026-11-13T12:00:00Z"))
	st := &state{entries: []any{entry}, moved: []any{moved}}
	got := st.hideWithheldMoved(appendMoveSection("Review", st.moved))
	if got != "Review" {
		t.Fatalf("body leaked withheld finding: %q", got)
	}
}

func TestExcludedPathCommentGetsImplicitReceipt(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/suppression/excluded.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/excluded.json", raw, 0o600); err != nil {
		t.Fatal(err)
	}
	entry := jq.NewObj("path", "vendor/.env", "status", "open", "history", []any{})
	st := &state{dir: dir, review: jq.NewObj(), entries: []any{entry}, now: "2026-11-13T12:00:00Z"}
	applySuppressionForTest(t, st)
	if readcommon.WithheldSource(entry) != "exclusion" {
		t.Fatalf("entry = %s", entry.MarshalCompact())
	}
}

func TestCheckerAllowsReceiptedExclusion(t *testing.T) {
	raw := []byte(`{"schema_version":"1","body":"> Review complete. Policy exceptions recorded.","comments":[{"path":"vendor/.env","line":1,"body":"Credential policy exception.","rigor":"pointed"}]}`)
	result := CheckReviewInput(raw, CheckInput{Diff: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-a\n+b\n", ExcludedPaths: map[string]bool{"vendor/.env": true}})
	if result.Errors != 0 {
		t.Fatalf("receipted path refused: %+v", result.Problems)
	}
}

func TestHiddenMovedSectionDoesNotInventFinding(t *testing.T) {
	moved := jq.NewObj("path", "a.go", "line", 2, "body", "finding")
	entry := jq.NewObj("path", "a.go", "line", 2, "body", "finding", "withheld", jq.NewObj("source", "agent", "reason", "An intentional compatibility exception.", "at", "2026-11-13T12:00:00Z"))
	derived := jq.NewObj("kind", "section", "heading", core.MovedSectionHeading)
	st := &state{entries: []any{entry, derived}, moved: []any{moved}}
	st.hideWithheldMoved(appendMoveSection("Review", st.moved))
	if len(st.entries) != 1 {
		t.Fatalf("withheld move invented %d findings; want one original", len(st.entries))
	}
}

func TestExcludedPathsReusesLoadedReceipt(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/suppression/excluded.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/excluded.json", raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st := &state{dir: dir}
	review := []byte(`{"suppressed":[{"path":"vendor/.env","source":"exclusion","exclusion_id":"ignore:1","reason":"Excluded at trusted base."}]}`)
	if ref := st.validateSuppressionBytes(review); ref != nil {
		t.Fatalf("validateSuppressionBytes: %v", ref)
	}
	if err := os.Remove(dir + "/excluded.json"); err != nil {
		t.Fatal(err)
	}
	got := st.excludedPaths()
	if !got["vendor/.env"] {
		t.Fatalf("excludedPaths = %v; want cached vendor/.env", got)
	}
}
