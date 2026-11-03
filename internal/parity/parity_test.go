package parity

import (
	"fmt"
	"maps"
	"slices"
	"testing"
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
