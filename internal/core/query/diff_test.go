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
