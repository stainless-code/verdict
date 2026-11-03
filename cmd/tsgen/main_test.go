package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// seed writes the generated file under a temp repository root, creating the
// parent directories execute expects.
func seed(t *testing.T, root, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(codesRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRenderRefusalCodes pins the generated union: one sorted entry per
// exact code, closed with a semicolon and no trailing blank line.
func TestRenderRefusalCodes(t *testing.T) {
	t.Parallel()
	got := string(renderRefusalCodes([]string{"zeta", "alpha"}))
	want := "// GENERATED from internal/core/hint.go hintRules by cmd/tsgen. Do not edit.\n" +
		"// Open families (missing-*, bad-*) accept any suffix; keep this union\n" +
		"// exact and widen at the use site with ` | (string & {})`.\n" +
		"export type RefusalCode =\n" +
		"  | \"zeta\"\n" +
		"  | \"alpha\";\n"
	if got != want {
		t.Errorf("renderRefusalCodes =\n%s\nwant\n%s", got, want)
	}
}

// TestRenderRefusalCodesQuotesSafely proves a code carrying a quote cannot
// break out of the generated string literal.
func TestRenderRefusalCodesQuotesSafely(t *testing.T) {
	t.Parallel()
	got := string(renderRefusalCodes([]string{`we"ird`}))
	if !strings.Contains(got, "  | \"we\\\"ird\";") {
		t.Errorf("renderRefusalCodes did not escape the quote:\n%s", got)
	}
}

// runCapture runs one invocation and collects its streams.
func runCapture(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errs bytes.Buffer
	code = run(args, &out, &errs)
	return code, out.String(), errs.String()
}

// TestRunRejectsStaleGeneratedFile covers the drift gate's failing half: a
// checked-in file that no longer matches the table fails with the fix.
func TestRunRejectsStaleGeneratedFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seed(t, root, "stale\n")
	code, _, stderr := runCapture(t, "-root", root)
	if code != 1 {
		t.Fatalf("check of a stale tree exit %d; want 1 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "is stale") || !strings.Contains(stderr, "make tsgen") {
		t.Errorf("stale check stderr = %q; want the regeneration hint", stderr)
	}
}

// TestRunWritesThenChecks covers the writing half: -write renders the union,
// and the same invocation as a check then passes.
func TestRunWritesThenChecks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seed(t, root, "stale\n")
	if code, _, stderr := runCapture(t, "-root", root, "-write"); code != 0 {
		t.Fatalf("write exit %d (stderr %q)", code, stderr)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(codesRelPath)))
	if err != nil {
		t.Fatal(err)
	}
	if want := string(renderRefusalCodes(core.ExactRefusalCodes())); string(raw) != want {
		t.Errorf("written file is not the rendered union:\n%s", raw)
	}
	code, stdout, stderr := runCapture(t, "-root", root)
	if code != 0 {
		t.Fatalf("check after write exit %d (stderr %q)", code, stderr)
	}
	if !strings.Contains(stdout, "is current") {
		t.Errorf("current check stdout = %q; want an is-current line", stdout)
	}
}

// TestRunRejectsStrayArguments keeps the command's usage error honest.
func TestRunRejectsStrayArguments(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-root", t.TempDir(), "extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("stray argument exit %d; want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: tsgen") {
		t.Errorf("stderr = %q; want the usage line", stderr.String())
	}
}

// TestExactRefusalCodesIsSortedAndMatched proves the generator's input is
// deduplicated, sorted, and every entry is answered by the hint table.
func TestExactRefusalCodesIsSortedAndMatched(t *testing.T) {
	t.Parallel()
	codes := core.ExactRefusalCodes()
	if len(codes) == 0 {
		t.Fatal("ExactRefusalCodes returned nothing")
	}
	if !slices.IsSorted(codes) {
		t.Error("ExactRefusalCodes is not sorted")
	}
	if compact := slices.Compact(slices.Clone(codes)); len(compact) != len(codes) {
		t.Errorf("ExactRefusalCodes repeats a code: %v", codes)
	}
	for _, code := range codes {
		if hint := core.HintFor(code); !strings.HasPrefix(hint, "try: ") {
			t.Errorf("HintFor(%q) = %q; want a try: hint", code, hint)
		}
	}
}
