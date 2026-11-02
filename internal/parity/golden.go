package parity

// Golden-file runner for the Go CLI contract (go-port 08): every converted
// parity case runs the built binary on its hermetic fixtures and pins the
// scrubbed exit code, stdout, stderr, and written files against a
// checked-in golden under testdata/. Regenerate with -update, then review
// the testdata diff like code.

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// updateGolden regenerates testdata goldens instead of comparing.
var updateGolden = flag.Bool("update", false, "regenerate testdata golden files")

// Golden runs the Go binary in a fresh sandbox and home, pins the scrubbed
// result against testdata/<name>.golden, and returns the outputs for extra
// assertions. setup writes identical fixtures per run. extraEnv overlays
// variables. args are the direct-protocol CLI argv (no dispatch
// placeholders).
func Golden(
	t *testing.T, h *Harness, name string,
	setup func(t *testing.T, dir, home string),
	extraEnv map[string]string, args ...string,
) Outputs {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	setup(t, dir, home)
	out := h.Run(t, dir, home, extraEnv, args...)
	Check(t, name, Render(out, []string{dir, home}))
	return out
}

// Render scrubs one run into the golden file shape: exit, stdout, stderr,
// then every written file sorted by key with slash-normalized keys
// (Windows walks join with backslashes; content stays byte-exact).
func Render(out Outputs, roots []string) string {
	var sb strings.Builder
	sb.WriteString("exit: " + strconv.Itoa(out.Status) + "\n")
	sb.WriteString("--- stdout ---\n")
	sb.WriteString(scrub(scrubRoots(out.Stdout, roots)))
	if !strings.HasSuffix(out.Stdout, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("--- stderr ---\n")
	sb.WriteString(scrub(scrubRoots(out.Stderr, roots)))
	if !strings.HasSuffix(out.Stderr, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("--- files ---\n")
	keys := make([]string, 0, len(out.Files))
	for k := range out.Files {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		sb.WriteString("--- file " + filepath.ToSlash(k) + " ---\n")
		sb.WriteString(scrub(scrubRoots(out.Files[k], roots)))
		if !strings.HasSuffix(out.Files[k], "\n") {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// Check compares the rendered run against testdata/<name>.golden,
// regenerating it under -update.
func Check(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update to generate): %v", path, err)
	}
	if string(want) != got {
		t.Errorf("golden differs: %s (run with -update to regenerate, then review the diff)\n--- want ---\n%s\n--- got ---\n%s",
			path, want, got)
	}
}
