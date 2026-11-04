package golden

// Golden-file runner for the Go CLI contract: every case runs the built
// binary on its hermetic fixtures and pins the scrubbed exit code, stdout,
// stderr, and written files against a checked-in golden under testdata/.
// Regenerate with -update, then review the testdata diff like code.

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Golden-file constants the helpers share: separators, permissions, and the
// size gate.
const (
	// newline terminates every rendered golden line.
	newline = "\n"
	// goldenPerm keeps regenerated goldens owner read/write. gosec G306 wants
	// 0600 or stricter, so a wider mode stays off.
	goldenPerm = 0o600
	// largeFileBytes is the payload above which a golden pins size+sha256
	// instead of the body. A generated 800 KB diff is reproducible from the
	// fixture that writes it, and a digest still detects every byte change
	// while keeping the golden reviewable.
	largeFileBytes = 64 << 10
	// diffLines bounds each side of a mismatch report: a multi-megabyte
	// golden must never dump want and got into the test log.
	diffLines   = 80
	diffContext = 3
)

var updateGolden = flag.Bool("update", false, "regenerate testdata golden files")

// Render scrubs one run into the golden file shape: exit, stdout, stderr,
// then every file the run touched, sorted by key with slash-normalized keys
// (Windows walks join with backslashes; content stays byte-exact).
//
// Only touched files are pinned. A file the case seeded and the run left
// alone is fixture input — reproducible from the setup code — so pinning it
// would store the same bytes once per case and bury the product's diff in
// noise. Bodies above largeFileBytes render as size+sha256; deletions render
// as a marker so the golden still proves them.
func Render(out Outputs, roots []string) string {
	var sb strings.Builder
	sb.WriteString("exit: " + strconv.Itoa(out.Status) + "\n")
	writeStream(&sb, "--- stdout ---", out.Stdout, roots)
	writeStream(&sb, "--- stderr ---", out.Stderr, roots)
	sb.WriteString("--- files ---\n")
	for _, key := range touchedKeys(out) {
		if body, ok := out.Files[key]; ok {
			renderFile(&sb, key, body, roots)
			continue
		}
		sb.WriteString("--- file " + filepath.ToSlash(key) + " (deleted) ---\n")
	}
	return sb.String()
}

// writeStream scrubs one stream into the golden, ending it with a newline so
// every section starts on its own line. A stream above largeFileBytes pins
// its size and digest instead: an envelope that echoes an over-cap diff is
// fixture-sized payload, and its bytes still change the digest.
func writeStream(sb *strings.Builder, header, stream string, roots []string) {
	body := Scrub(scrubRoots(stream, roots))
	if !strings.HasSuffix(stream, newline) {
		body += newline
	}
	if len(body) > largeFileBytes {
		fmt.Fprintf(sb, "%s (size=%d sha256=%s) ---\n", strings.TrimSuffix(header, " ---"), len(body), digest(body))
		return
	}
	sb.WriteString(header + "\n")
	sb.WriteString(body)
}

// renderFile writes one pinned file: full bytes for a reviewable payload, a
// digest for a large one. The digest covers exactly the bytes the full form
// would have held (scrubbed, newline-terminated), so switching forms never
// changes what a change looks like.
func renderFile(sb *strings.Builder, key, body string, roots []string) {
	body = Scrub(scrubRoots(body, roots))
	if !strings.HasSuffix(body, newline) {
		body += newline
	}
	name := filepath.ToSlash(key)
	if len(body) > largeFileBytes {
		fmt.Fprintf(sb, "--- file %s (size=%d sha256=%s) ---\n", name, len(body), digest(body))
		return
	}
	sb.WriteString("--- file " + name + " ---\n")
	sb.WriteString(body)
}

// touchedKeys returns the sorted keys the run created, changed, or deleted,
// comparing scrubbed bytes: a file whose only movement is a timestamp is not
// a change the golden would show, and grading it as one makes a rerun's
// goldens depend on whether the clock happened to tick inside the run. Keys
// absent from Before (a nil Before pins everything, for callers that render
// a filtered map) are treated as created.
func touchedKeys(out Outputs) []string {
	keys := make([]string, 0, len(out.Files)+len(out.Before))
	for key, after := range out.Files {
		if before, ok := out.Before[key]; ok && Scrub(before) == Scrub(after) {
			continue
		}
		keys = append(keys, key)
	}
	for key := range out.Before {
		if _, ok := out.Files[key]; !ok {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func digest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// Check compares the rendered run against testdata/<name>.golden,
// regenerating it under -update.
func Check(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), goldenPerm); err != nil {
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
		t.Errorf("golden differs: %s (run with -update to regenerate, then review the diff)\n%s",
			path, divergence(string(want), got))
	}
}

// divergence reports the first differing line with its neighborhood, capped
// at diffLines per side. golden.Check is the only place a golden mismatch is
// printed, so bounding it here keeps a multi-megabyte golden from flooding
// the test log while still naming the line that moved.
func divergence(want, got string) string {
	wantLines := strings.Split(want, newline)
	gotLines := strings.Split(got, newline)
	first := firstDivergence(wantLines, gotLines)
	var sb strings.Builder
	for _, side := range []struct {
		header string
		lines  []string
	}{{"want", wantLines}, {"got", gotLines}} {
		start := max(first-diffContext, 0)
		fmt.Fprintf(&sb, "--- %s (line %d of %d) ---\n", side.header, first+1, len(side.lines))
		for i := start; i < len(side.lines) && i < start+diffLines; i++ {
			sb.WriteString(side.lines[i] + newline)
		}
		if start+diffLines < len(side.lines) {
			sb.WriteString("... " + strconv.Itoa(len(side.lines)-start-diffLines) + " more lines\n")
		}
	}
	return sb.String()
}

func firstDivergence(want, got []string) int {
	for i, line := range want {
		if i >= len(got) || line != got[i] {
			return i
		}
	}
	return len(want)
}
