package query

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func writeBundle(t *testing.T, dir, name string) {
	t.Helper()
	full := filepath.Join(dir, config.VerdictDir, config.StateDirName, name)
	must(t, os.MkdirAll(full, 0o700))
	writeDiff(t, full)
	writeFindings(t, full)
}

func writeDiff(t *testing.T, full string) {
	t.Helper()
	body := []byte("diff --git a/a.txt b/a.txt\nindex 1..2 100644\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n")
	must(t, os.WriteFile(filepath.Join(full, "full.diff"), body, 0o600))
}

func writeFindings(t *testing.T, full string) {
	t.Helper()
	body := []byte(`[{"sig":"s1","path":"a.txt","line":1,"side":"RIGHT","severity":"important","category":"logic","body":"fix this","status":"open"}]`)
	must(t, os.WriteFile(filepath.Join(full, "findings.json"), body, 0o600))
}

func testDiffLayer(t *testing.T) (*Layer, string) {
	t.Helper()
	root := t.TempDir()
	writeBundle(t, root, "local-1")
	writeBundle(t, root, "local-2")
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	l := New(Snapshot{Version: "1", Roots: []Root{{Path: root}}, Runtime: &core.Runtime{Now: clock}}, Options{Clock: clock, NoWatch: true})
	return l, RootID(root)
}

func dataField(t *testing.T, data *jq.Obj, key string) any {
	t.Helper()
	v, ok := data.Get(key)
	if !ok || v == nil {
		t.Fatalf("missing %s", key)
	}
	return v
}

func dataList(t *testing.T, data *jq.Obj, key string) []any {
	t.Helper()
	v := dataField(t, data, key)
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("%s is not a list", key)
	}
	return list
}

func objField(t *testing.T, v any, key string) any {
	t.Helper()
	obj, ok := v.(*jq.Obj)
	if !ok {
		t.Fatalf("%T is not an object", v)
	}
	w, ok := obj.Get(key)
	if !ok || w == nil {
		t.Fatalf("missing %s", key)
	}
	return w
}

func checkSources(t *testing.T, view *View) {
	t.Helper()
	if len(view.Sources) == 0 {
		t.Fatal("missing sources")
	}
	for i := range view.Sources {
		if view.Sources[i].Path != "" {
			return
		}
	}
	t.Fatal("empty sources")
}

func TestDiffRendersLanesAndWordSpans(t *testing.T) {
	l, rootID := testDiffLayer(t)
	view, err := l.Diff(t.Context(), rootID, "local-1")
	must(t, err)
	if len(dataList(t, view.Data, "lanes")) == 0 {
		t.Fatal("no lanes")
	}
	if len(dataList(t, view.Data, "files")) != 1 {
		t.Fatal("want one file")
	}
	checkSources(t, view)
}

func TestDiffNotFound(t *testing.T) {
	l, _ := testDiffLayer(t)
	if _, err := l.Diff(t.Context(), "missing", "local-1"); err == nil {
		t.Fatal("expected error")
	}
}

func firstHunkLines(t *testing.T, view *View) []any {
	t.Helper()
	files := dataList(t, view.Data, "files")
	hunksV := objField(t, files[0], "hunks")
	hunkList, ok := hunksV.([]any)
	if !ok || len(hunkList) == 0 {
		t.Fatal("no hunks")
	}
	linesV := objField(t, hunkList[0], "lines")
	lines, ok := linesV.([]any)
	if !ok || len(lines) == 0 {
		t.Fatal("no lines")
	}
	return lines
}

func lineHasSpans(t *testing.T, v any) bool {
	t.Helper()
	obj, ok := v.(*jq.Obj)
	if !ok {
		return false
	}
	if !isChangedKind(obj) {
		return false
	}
	return spansPresent(obj)
}

func isChangedKind(obj *jq.Obj) bool {
	kindV, ok := obj.Get("kind")
	if !ok {
		return false
	}
	kind, ok := kindV.(string)
	return ok && (kind == "add" || kind == "del")
}

func spansPresent(obj *jq.Obj) bool {
	spansV, ok := obj.Get("spans")
	if !ok || spansV == nil {
		return false
	}
	spans, ok := spansV.([]any)
	return ok && len(spans) > 0
}

func anySpans(t *testing.T, lines []any) bool {
	t.Helper()
	for i := range lines {
		if lineHasSpans(t, lines[i]) {
			return true
		}
	}
	return false
}

func TestDiffWordPassMutation(t *testing.T) {
	// The diff payload must carry word spans for changed lines; a payload
	// without spans would render no highlights and fail this test.
	l, rootID := testDiffLayer(t)
	view, err := l.Diff(t.Context(), rootID, "local-1")
	must(t, err)
	lines := firstHunkLines(t, view)
	if !anySpans(t, lines) {
		t.Fatal("no word spans on changed lines; word pass dropped?")
	}
}

func TestDiffAnchorsFindings(t *testing.T) { //nolint:gocognit,revive,cyclop // anchor + lane + by_lens checks need exhaustive branching
	l, rootID := testDiffLayer(t)
	view, err := l.Diff(t.Context(), rootID, "local-1")
	must(t, err)
	// Anchored finding must appear on the exact diff line (new_no=1).
	lines := firstHunkLines(t, view)
	var anchored *jq.Obj
	for _, v := range lines {
		obj, ok := v.(*jq.Obj)
		if !ok {
			continue
		}
		if kindV, _ := obj.Get("kind"); kindV == "add" {
			if noV, _ := obj.Get("new_no"); noV != nil {
				// toInt handles json.Number via UseNumber; check via String
				if isOne(noV) {
					anchored = obj
				}
			}
		}
	}
	if anchored == nil {
		t.Fatal("no add line with new_no 1")
	}
	fsV, ok := anchored.Get("findings")
	if !ok {
		t.Fatalf("anchored line has no findings: %v", anchored.MarshalCompact())
	}
	fs, ok := fsV.([]any)
	if !ok || len(fs) == 0 {
		t.Fatalf("findings = %v", fsV)
	}
	f0, ok := fs[0].(*jq.Obj)
	if !ok {
		t.Fatalf("finding is %T", fs[0])
	}
	pathV, _ := f0.Get("path")
	lineV, _ := f0.Get("line")
	sideV, _ := f0.Get("side")
	if pathV != "a.txt" {
		t.Fatalf("path = %v want a.txt", pathV)
	}
	if !isOne(lineV) {
		t.Fatalf("line = %v want 1", lineV)
	}
	if sideV != "RIGHT" {
		t.Fatalf("side = %v want RIGHT", sideV)
	}
	// Lane must report non-zero findings and by_lens for the logic category.
	lanes := dataList(t, view.Data, "lanes")
	if len(lanes) == 0 {
		t.Fatal("no lanes")
	}
	found := false
	for _, lv := range lanes {
		laneObj, ok := lv.(*jq.Obj)
		if !ok {
			continue
		}
		fcV, _ := laneObj.Get("findings")
		if !isOne(fcV) && !isNonZero(fcV) {
			continue
		}
		byLensV, ok := laneObj.Get("by_lens")
		if !ok {
			t.Fatal("lane has findings but no by_lens")
		}
		byLens, ok := byLensV.(*jq.Obj)
		if !ok {
			t.Fatal("by_lens not object")
		}
		v, ok := byLens.Get("logic")
		if !ok || !isOne(v) {
			t.Fatalf("by_lens = %v", byLens.MarshalCompact())
		}
		found = true
	}
	if !found {
		t.Fatal("no lane with non-zero findings/by_lens")
	}
	// Sources are view-level, not per-row.
	if len(view.Sources) == 0 {
		t.Fatal("view must carry sources[]")
	}
}

func isOne(v any) bool {
	// findings.json line decodes as json.Number (UseNumber).
	switch n := v.(type) {
	case int:
		return n == 1
	case int64:
		return n == 1
	case float64:
		return n == 1
	case string:
		return n == "1"
	case jq.Number:
		return string(n) == "1"
	default:
		// json.Number
		if s, ok := v.(interface{ String() string }); ok {
			return s.String() == "1"
		}
		return false
	}
}

func isNonZero(v any) bool {
	switch n := v.(type) {
	case int:
		return n != 0
	case int64:
		return n != 0
	case float64:
		return n != 0
	case jq.Number:
		return string(n) != "0"
	default:
		if s, ok := v.(interface{ String() string }); ok {
			return s.String() != "0"
		}
		return false
	}
}

func TestDiffOversizedMarksPartial(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, config.VerdictDir, config.StateDirName, "local-big")
	must(t, os.MkdirAll(full, 0o700))
	must(t, os.WriteFile(filepath.Join(full, "full.diff"), oversizedDiff(t), 0o600))
	view := diffLayerView(t, root, "local-big")
	assertPartial(t, view, "oversized")
}

func oversizedDiff(t *testing.T) []byte {
	t.Helper()
	big := make([]byte, 11<<20)
	for i := range big {
		big[i] = 'x'
	}
	return big
}

func diffLayerView(t *testing.T, root, bundle string) *View {
	t.Helper()
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	l := New(Snapshot{Version: "1", Roots: []Root{{Path: root}}, Runtime: &core.Runtime{Now: clock}}, Options{Clock: clock, NoWatch: true})
	view, err := l.Diff(t.Context(), RootID(root), bundle)
	must(t, err)
	return view
}

func assertPartial(t *testing.T, view *View, what string) {
	t.Helper()
	partialV, ok := view.Data.Get("partial")
	if !ok || partialV != true {
		t.Fatalf("partial for %s = %v want true", what, partialV)
	}
	errsV, ok := view.Data.Get("errors")
	if !ok || errsV == nil {
		t.Fatal("missing errors")
	}
	errs, ok := errsV.([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("errors for %s = %v want non-empty", what, errsV)
	}
	if len(view.Errors) == 0 {
		t.Fatal("view.Errors empty")
	}
}

func TestDiffUnreadableMarksPartial(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, config.VerdictDir, config.StateDirName, "local-missing")
	must(t, os.MkdirAll(full, 0o700))
	// No full.diff file at all is unreadable -> partial.
	must(t, os.WriteFile(filepath.Join(full, "findings.json"), []byte("[]"), 0o600))
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	l := New(Snapshot{Version: "1", Roots: []Root{{Path: root}}, Runtime: &core.Runtime{Now: clock}}, Options{Clock: clock, NoWatch: true})
	view, err := l.Diff(t.Context(), RootID(root), "local-missing")
	must(t, err)
	partialV, _ := view.Data.Get("partial")
	if partialV != true {
		t.Fatalf("partial = %v want true for missing full.diff", partialV)
	}
	if len(view.Errors) == 0 {
		t.Fatal("errors must be non-empty")
	}
}
