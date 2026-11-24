package lsp

import (
	"strings"
	"testing"
)

// nestedFixture is the reviewer's two-bundle nested layout: the outer root
// reviews sub/a.go line 1 and top.go, the inner root (a checkout inside the
// outer one) reviews the same file as a.go line 2.
func nestedFixture(t *testing.T) (outer, inner string) {
	t.Helper()
	outer = checkout(t)
	inner = outer + "/sub"
	writeBundle(t, outer, "local-outer",
		findingJSON(sigCrit, "open", "critical", "sub/a.go", 1),
		findingJSON(sigInfo, "open", "informational", "top.go", 3),
	)
	writeBundle(t, inner, "local-inner", findingJSON(sigImp, "open", "important", "a.go", 2))
	return outer, inner
}

// TestNestedRootsMergePerURI pins the per-URI merge: a file two nested roots
// both reach gets one publish carrying both roots' findings (each with its own
// data.root), a second refresh sends nothing (the set never flips), and the
// file clears only when no root contributes any more.
func TestNestedRootsMergePerURI(t *testing.T) {
	t.Parallel()
	outer, inner := nestedFixture(t)
	s := startSession(t, newConfig(outer, inner))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	shared := fileURI(outer, "sub/a.go")
	got := summarize(publishes(t, s.barrier(2, 1)))
	want := shared + " v- " + sigCrit + "," + sigImp + "\n" + fileURI(outer, "top.go") + " v- " + sigInfo
	if got != want {
		t.Fatalf("initial publishes =\n%s\nwant\n%s", got, want)
	}
	roots := rootsOfPublish(t, publishes(t, s.snapshot())[0])
	if roots != newServedRoot(outer, false).ID+","+newServedRoot(inner, false).ID {
		t.Fatalf("data.root per finding = %s", roots)
	}
	if got := refreshPublishes(t, s, 3) + refreshPublishes(t, s, 4); got != "" {
		t.Fatalf("repeat refreshes republished an unchanged merged set:\n%s", got)
	}
	writeBundle(t, inner, "local-inner", findingJSON(sigImp, "false-positive", "important", "a.go", 2))
	if got := refreshPublishes(t, s, 5); got != shared+" v- "+sigCrit {
		t.Fatalf("after the inner root dropped its finding:\n%s\nwant the outer finding kept", got)
	}
	writeBundle(t, outer, "local-outer", findingJSON(sigInfo, "open", "informational", "top.go", 3))
	if got := refreshPublishes(t, s, 6); got != shared+" v- " {
		t.Fatalf("after both roots dropped the file:\n%s\nwant one clearing publish", got)
	}
	if err := s.finish(7); err != nil {
		t.Fatal(err)
	}
}

// TestNestedRootsOpenDocumentMerges pins the merge for an open document: one
// versioned publish under the client's URI spelling carries both roots.
func TestNestedRootsOpenDocumentMerges(t *testing.T) {
	t.Parallel()
	outer, inner := nestedFixture(t)
	s := startSession(t, newConfig(outer, inner))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	mark := len(s.barrier(2, 0)) + 1
	spelled := "file://localhost" + strings.TrimPrefix(fileURI(outer, "sub/a.go"), "file://")
	openDoc(s, spelled, 4, "one\ntwo\n")
	got := summarize(publishes(t, s.barrier(3, mark)))
	want := fileURI(outer, "sub/a.go") + " v- \n" + spelled + " v4 " + sigCrit + "," + sigImp
	if got != want {
		t.Fatalf("open document publishes =\n%s\nwant the old spelling cleared, then one merged set", got)
	}
	if err := s.finish(4); err != nil {
		t.Fatal(err)
	}
}

// TestSiblingRootsStayDisjoint pins that roots which share no file publish
// independently under their own URIs.
func TestSiblingRootsStayDisjoint(t *testing.T) {
	t.Parallel()
	left, right := checkout(t), checkout(t)
	writeBundle(t, left, "local-1", findingJSON(sigCrit, "open", "critical", "a.go", 1))
	writeBundle(t, right, "local-1", findingJSON(sigImp, "open", "important", "a.go", 1))
	s := startSession(t, newConfig(left, right))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	got := publishes(t, s.barrier(2, 1))
	byURI := map[string]string{}
	for i := range got {
		byURI[got[i].URI] = sigsOf(&got[i])
	}
	if len(got) != 2 || byURI[fileURI(left, "a.go")] != sigCrit || byURI[fileURI(right, "a.go")] != sigImp {
		t.Fatalf("sibling publishes = %s", summarize(got))
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}

func rootsOfPublish(t *testing.T, p publishParams) string {
	t.Helper()
	ids := make([]string, 0, len(p.Diagnostics))
	for i := range p.Diagnostics {
		ids = append(ids, p.Diagnostics[i].Data.Root)
	}
	return strings.Join(ids, ",")
}
