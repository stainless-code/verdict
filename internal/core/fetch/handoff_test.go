package fetchcore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

// TestBundleFactsPresentAndAbsent pins each presence read against literal
// bundle files. An empty rules file is absent: it carries no fact to explain.
func TestBundleFactsPresentAndAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	empty := bundleFacts(dir)
	if empty != (skillfs.HandoffFacts{}) {
		t.Fatalf("empty bundle facts = %+v, want all false", empty)
	}

	writeFactFile(t, dir, "chunks/manifest.json", `{"schema_version":"1","chunks":[]}`)
	writeFactFile(t, dir, "threads.json", `{"schema_version":"1","threads":[{"id":"t1"}]}`)
	writeFactFile(t, dir, "kb.txt", "rule\n")
	writeFactFile(t, dir, "lenses.txt", "lens\n")
	writeFactFile(t, dir, "context.txt", "spec\n")
	full := bundleFacts(dir)
	want := skillfs.HandoffFacts{Chunked: true, PriorThreads: true, KB: true, Lenses: true, Context: true}
	if full != want {
		t.Fatalf("populated bundle facts = %+v, want %+v", full, want)
	}

	writeFactFile(t, dir, "kb.txt", "")
	writeFactFile(t, dir, "lenses.txt", "")
	got := bundleFacts(dir)
	if got.KB || got.Lenses {
		t.Errorf("empty rule files still set KB=%v Lenses=%v, want false", got.KB, got.Lenses)
	}
}

// TestWriteBundleProcedureUsesFacts proves the bundle writer assembles the
// same bytes the fact table promises, and that each ref lands beside it.
func TestWriteBundleProcedureUsesFacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFactFile(t, dir, "context.txt", "spec\n")
	if err := writeBundleProcedure(dir); err != nil {
		t.Fatal(err)
	}
	want := embeddedReviewBytes(t)
	want = append(want, '\n')
	for _, name := range []string{"prompts/handoff/context.md"} {
		part, err := skillfs.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, part...)
	}
	got, err := os.ReadFile(filepath.Join(dir, "procedure.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is not the context-only review assembly", filepath.ToSlash(filepath.Join(dir, "procedure.md")))
	}
}

const refIndexPrefix = "Trusted refs in <bundle>/refs/: "

func refIndexLine(t *testing.T) string {
	t.Helper()
	paths := make([]string, 0, len(skillfs.HandoffRefs()))
	for _, ref := range skillfs.HandoffRefs() {
		paths = append(paths, ref.Path)
	}
	return refIndexPrefix + strings.Join(paths, ", ") + "\n"
}

func assertNoRefBodies(t *testing.T, dump string) {
	t.Helper()
	for _, ref := range skillfs.HandoffRefs() {
		body, err := skillfs.ReadRef(ref)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) > 0 && strings.Contains(dump, string(body)) {
			t.Errorf("prompt dump still prints the body of %s", ref.Path)
		}
	}
}

// TestPromptDumpListsRefsWithoutBodies keeps the assembled reading list lean:
// it names every pull ref on one trusted line but never prints ref bytes.
func TestPromptDumpListsRefsWithoutBodies(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	if ref := dumpPrompts(&out, skillfs.HandoffFacts{}); ref != nil {
		t.Fatalf("dumpPrompts: %+v", ref)
	}
	dump := out.String()
	if !strings.HasPrefix(dump, string(embeddedReviewBytes(t))) {
		t.Fatal("prompt dump no longer starts with the exact procedure.md bytes")
	}
	index := refIndexLine(t)
	if !strings.Contains(dump, index) {
		t.Fatalf("prompt dump omits the one trusted ref line %q", index)
	}
	assertNoRefBodies(t, dump)
}

func writeFactFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBundleFactsSpecRole proves the spec fact reads the context manifest: a
// role: spec source turns the checklist method on, and supporting context that
// is not a spec leaves it off.
func TestBundleFactsSpecRole(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFactFile(t, dir, "context.txt", "notes body\n")
	writeFactFile(t, dir, "context.json", `[{"source":"notes.md","role":"context"}]`)
	if bundleFacts(dir).Spec {
		t.Error("a context-only bundle set Spec, want the checklist method off")
	}
	writeFactFile(t, dir, "context.json", `[{"source":"notes.md","role":"context"},{"source":"spec.md","role":"spec"}]`)
	if !bundleFacts(dir).Spec {
		t.Error("a role: spec source left Spec false")
	}
}

// TestWriteBundleProcedureCarriesTheSpecSection proves the bundle writer's
// bytes: procedure.md ends with the checklist method the fact asked for, not
// with the context section a context-only bundle gets.
func TestWriteBundleProcedureCarriesTheSpecSection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFactFile(t, dir, "context.txt", "spec body\n")
	writeFactFile(t, dir, "context.json", `[{"source":"spec.md","role":"spec"}]`)
	if err := writeBundleProcedure(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "procedure.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := embeddedReviewBytes(t)
	for _, name := range []string{"prompts/handoff/context.md", "prompts/handoff/spec.md"} {
		part, err := skillfs.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, '\n')
		want = append(want, part...)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("procedure.md = %d bytes, want the literal %d-byte assembly", len(got), len(want))
	}
}
