package golden

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

// TestScrubRootsSlashForm pins the golden scrub to both path spellings: a
// Windows sandbox root carries backslashes while fixtures and product output
// render slash paths, so a slash-spelled root must still become ROOT instead
// of leaking the machine path into Windows goldens.
func TestScrubRootsSlashForm(t *testing.T) {
	t.Parallel()

	root := `C:\tmp\sandbox`
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"native", `C:\tmp\sandbox\remote`, `ROOT\remote`},
		{"slash", `C:/tmp/sandbox/remote`, "ROOT/remote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := scrubRoots(tc.in, []string{root}); got != tc.want {
				t.Fatalf("scrubRoots(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// recorder collects the read-only proof's failure messages, so a test can
// assert the helper fails instead of trusting that it would.
type recorder struct{ msgs []string }

// Helper marks the recorder as a test helper.
func (r *recorder) Helper() {}

// Errorf records one failure message.
func (r *recorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

// noWritesCase is one read-only proof scenario: the tree before and after the
// run, the artifacts the leg may write, and the exact failures expected.
type noWritesCase struct {
	name    string
	before  map[string]string
	after   map[string]string
	allowed map[string]bool
	want    []string
}

// TestAssertNoWrites pins the read-only proof in both directions: a reader
// that writes, changes, or deletes fails, while an untouched (or only
// timestamp-different) tree and an explicitly requested artifact pass. A proof
// that never fails would let a read verb grow a store write unnoticed.
func TestAssertNoWrites(t *testing.T) {
	t.Parallel()

	seed := map[string]string{
		"C/.review/5101/findings.json": "[]",
		"H/.skill/prompt.md":           "at 2026-01-01T00:00:00Z\n",
	}
	cases := []noWritesCase{
		{name: "unchanged", before: seed, after: maps.Clone(seed)},
		{
			name:   "scrubbed-only",
			before: seed,
			after:  changed(seed, "H/.skill/prompt.md", "at 2026-11-03T09:15:00Z\n"),
		},
		{
			name:   "wrote",
			before: seed,
			after:  changed(seed, "C/out.md", "report\n"),
			want:   []string{"go wrote unexpected file: C/out.md"},
		},
		{
			name:   "changed",
			before: seed,
			after:  changed(seed, "C/.review/5101/findings.json", `[{"sig":"a"}]`),
			want:   []string{"go changed file: C/.review/5101/findings.json"},
		},
		{
			name:   "deleted",
			before: seed,
			after:  without(seed, "C/.review/5101/findings.json"),
			want:   []string{"go deleted file: C/.review/5101/findings.json"},
		},
		{
			name:    "allowed-write",
			before:  seed,
			after:   changed(seed, "C/out.md", "report\n"),
			allowed: map[string]bool{"C/out.md": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			verifyNoWrites(t, tc)
		})
	}
}

// verifyNoWrites runs the proof for one case and compares the failures.
func verifyNoWrites(t *testing.T, tc noWritesCase) {
	t.Helper()
	rec := &recorder{}
	assertNoWrites(rec, tc.before, tc.after, tc.allowed)
	if !slices.Equal(rec.msgs, tc.want) {
		t.Fatalf("failures = %v, want %v", rec.msgs, tc.want)
	}
}

// changed returns a copy of files with one key set to value.
func changed(files map[string]string, key, value string) map[string]string {
	out := maps.Clone(files)
	out[key] = value
	return out
}

// without returns a copy of files with one key removed.
func without(files map[string]string, key string) map[string]string {
	out := maps.Clone(files)
	delete(out, key)
	return out
}

// TestCaseEnvAt pins the TMPDIR opt-in the record store depends on: the
// sandbox is the temp dir unless the case names its own.
func TestCaseEnvAt(t *testing.T) {
	t.Parallel()

	if got := (&Case{Env: map[string]string{"A": "1"}}).envAt("/sandbox"); got["TMPDIR"] != "" {
		t.Fatalf("default TMPDIR = %q, want unset", got["TMPDIR"])
	}
	if got := (&Case{TmpInSandbox: true}).envAt("/sandbox"); got["TMPDIR"] != "/sandbox" {
		t.Fatalf("sandbox TMPDIR = %q, want /sandbox", got["TMPDIR"])
	}
	own := &Case{Env: map[string]string{"TMPDIR": "/elsewhere"}, TmpInSandbox: true}
	if got := own.envAt("/sandbox"); got["TMPDIR"] != "/elsewhere" {
		t.Fatalf("explicit TMPDIR = %q, want /elsewhere", got["TMPDIR"])
	}
}

// TestRenderPinsOnlyTouched proves the golden stores product output, not
// fixture input: a seeded file the run left alone never reaches the golden
// (its bytes are the setup's, reproducible from code), while a created,
// changed, or deleted file is still pinned.
func TestRenderPinsOnlyTouched(t *testing.T) {
	t.Parallel()

	before := map[string]string{
		"C/canned/full.diff": "fixture input\n",
		"C/keep.txt":         "untouched\n",
		"C/changed.txt":      "old\n",
		"C/gone.txt":         "seeded then deleted\n",
	}
	files := map[string]string{
		"C/canned/full.diff":        "fixture input\n",
		"C/keep.txt":                "untouched\n",
		"C/changed.txt":             "new\n",
		"C/.review/1/findings.json": "[]\n",
	}
	want := "exit: 0\n" +
		"--- stdout ---\n\n" +
		"--- stderr ---\n\n" +
		"--- files ---\n" +
		"--- file C/.review/1/findings.json ---\n[]\n" +
		"--- file C/changed.txt ---\nnew\n" +
		"--- file C/gone.txt (deleted) ---\n"
	if got := Render(Outputs{Before: before, Files: files}, nil); got != want {
		t.Fatalf("Render() =\n%s\nwant\n%s", got, want)
	}
}

// TestRenderDigestForLargePayload proves a payload above the threshold pins
// size+sha256 of the exact bytes the full form would have held, for files and
// streams alike; the digest is what makes a one-byte change fail while the
// golden stays reviewable.
func TestRenderDigestForLargePayload(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("p", largeFileBytes+1)
	sum := sha256.Sum256([]byte(body + newline))
	wantDigest := hex.EncodeToString(sum[:])

	got := Render(Outputs{Files: map[string]string{"C/big.diff": body}, Stdout: body}, nil)
	wantFile := fmt.Sprintf("--- file C/big.diff (size=%d sha256=%s) ---\n", len(body)+1, wantDigest)
	if !strings.Contains(got, wantFile) {
		t.Fatalf("file line missing from\n%s\nwant %s", got, wantFile)
	}
	wantStream := fmt.Sprintf("--- stdout (size=%d sha256=%s) ---\n", len(body)+1, wantDigest)
	if !strings.Contains(got, wantStream) {
		t.Fatalf("stream line missing from\n%s\nwant %s", got, wantStream)
	}
	if strings.Contains(got, body) {
		t.Fatal("large body leaked into the golden instead of its digest")
	}
}

// TestDivergenceBounded proves a mismatched multi-megabyte golden reports a
// bounded window instead of printing want and got: the failing line is named,
// with only its neighborhood rendered.
func TestDivergenceBounded(t *testing.T) {
	t.Parallel()

	lines := make([]string, 0, 5000)
	for i := range 5000 {
		lines = append(lines, fmt.Sprintf("line %05d", i))
	}
	want := strings.Join(lines, newline)
	lines[4000] = "line 04000 changed"
	got := strings.Join(lines, newline)

	report := divergence(want, got)
	if n := strings.Count(report, newline); n > 2*diffLines+6 {
		t.Fatalf("report has %d lines, want at most %d", n, 2*diffLines+6)
	}
	if !strings.Contains(report, "want (line 4001 of 5000)") || !strings.Contains(report, "got (line 4001 of 5000)") {
		t.Fatalf("report does not name the first divergence:\n%s", report)
	}
	if !strings.Contains(report, "more lines") {
		t.Fatalf("report does not truncate:\n%s", report)
	}
}

func mustEmbedded(t *testing.T, label string, load func() ([]byte, error)) string {
	t.Helper()
	raw, err := load()
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	return string(raw)
}

// TestScrubNormalizesEmbeddedProcedures proves a golden renders one marker for
// each embedded assembly instead of pasting the Procedure into every case,
// including the escaped prompt_dump string in a JSON envelope.
func TestScrubNormalizesEmbeddedProcedures(t *testing.T) {
	review := mustEmbedded(t, "ReviewProcedure", skillfs.ReviewProcedure)
	drain := mustEmbedded(t, "DrainProcedure", skillfs.DrainProcedure)
	rendered := "--- file C/procedure.md ---\n" + review + "tail\n" +
		"--- file C/drain.procedure.md ---\n" + drain + "tail\n" +
		`{"prompt_dump":"` + jsonStringBody(review) + `rest"}` + newline

	out := Scrub(rendered)
	if strings.Contains(out, "name: verdict") {
		t.Fatal("golden still carries the Procedure frontmatter")
	}
	wants := []string{
		embeddedReviewMarker + newline + "tail",
		embeddedDrainMarker + newline + "tail",
		`"prompt_dump":"` + embeddedReviewMarker + `\n`,
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("golden lacks %q:\n%s", want, out)
		}
	}
}

type scrubbedBody struct {
	name   string
	marker string
	raw    []byte
}

func handoffBodies(t *testing.T, entries []scrubbedBody) []scrubbedBody {
	t.Helper()
	for i, entry := range entries {
		raw, err := skillfs.Read(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		entries[i].raw = raw
	}
	return entries
}

func renderBodies(entries []scrubbedBody) string {
	var rendered strings.Builder
	for _, entry := range entries {
		rendered.Write(entry.raw)
		rendered.WriteString(`{"prompt_dump":"` + jsonStringBody(string(entry.raw)) + `"}` + newline)
	}
	return rendered.String()
}

func assertBodiesScrubbed(t *testing.T, out string, entries []scrubbedBody) {
	t.Helper()
	for _, entry := range entries {
		if strings.Contains(out, string(entry.raw)) {
			t.Errorf("%s body survived the golden scrub", entry.name)
		}
		// renderBodies emits both spellings. Checking them separately keeps
		// the escaped assertion alive when only the raw replacement runs.
		escaped := jsonStringBody(string(entry.raw))
		if strings.Contains(out, escaped) {
			t.Errorf("%s JSON-escaped body survived the golden scrub", entry.name)
		}
		if !strings.Contains(out, entry.marker) {
			t.Errorf("golden lacks the %s marker", entry.marker)
		}
	}
}

// TestScrubNormalizesConditionalSections proves each fact section gets its own
// marker, including in an escaped prompt dump assembled with that fact true.
func TestScrubNormalizesConditionalSections(t *testing.T) {
	sections := handoffBodies(t, []scrubbedBody{
		{name: "prompts/handoff/chunked.md", marker: embeddedChunkedMarker},
		{name: "prompts/handoff/threads.md", marker: embeddedThreadsMarker},
		{name: "prompts/handoff/kb.md", marker: embeddedKBMarker},
		{name: "prompts/handoff/lenses.md", marker: embeddedLensesMarker},
		{name: "prompts/handoff/context.md", marker: embeddedContextMarker},
	})
	assertBodiesScrubbed(t, Scrub(renderBodies(sections)), sections)
}

// TestScrubNormalizesPullRefs keeps the review checklist and other ref
// bodies out of every golden, including escaped prompt dumps.
func TestScrubNormalizesPullRefs(t *testing.T) {
	var refs []scrubbedBody
	for _, ref := range skillfs.HandoffRefs() {
		raw, err := skillfs.ReadRef(ref)
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, scrubbedBody{name: ref.Path, raw: raw, marker: embeddedRefMarker + ref.Path + ">"})
	}
	assertBodiesScrubbed(t, Scrub(renderBodies(refs)), refs)
}
