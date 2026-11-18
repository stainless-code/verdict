package run

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	skillfs "github.com/stainless-code/verdict"
	draincore "github.com/stainless-code/verdict/internal/core/drain"
)

// firstReadPath matches the opening clause of a procedure-first directive.
// The file name is captured, never spelled out in the expectation.
var firstReadPath = regexp.MustCompile("(?m)^Read `([^`]+)` first;")

func parseFirstReadPath(t *testing.T, text string) string {
	t.Helper()
	match := firstReadPath.FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("directive does not open with a backticked Procedure path:\n%s", text)
	}
	return match[1]
}

// bundleDisplay renders the fixture bundle the way the run directive does:
// relative to the reviewed repository root.
func bundleDisplay(t *testing.T, root, dir string) string {
	t.Helper()
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(rel)
}

func embeddedDrainProcedure(t *testing.T) []byte {
	t.Helper()
	var out []byte
	for _, name := range []string{
		skillfs.ProcedureMD, "prompts/system.md", "prompts/address-reviews.md",
	} {
		raw, err := skillfs.Read(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		out = append(out, raw...)
	}
	return out
}

// TestDrainDirectiveNamesBundleProcedureFile applies the same F1-class check
// to the drain prompt, whose Procedure is written by the drain plan.
func TestDrainDirectiveNamesBundleProcedureFile(t *testing.T) {
	root, dir := drainBundle(t)
	writeDrainFile(t, filepath.Join(dir, reviewFile))
	if err := draincore.WriteProcedure(dir); err != nil {
		t.Fatalf("WriteProcedure: %v", err)
	}
	want := embeddedDrainProcedure(t)

	st := drainState(root, "7", "digest")
	args, ref := st.argsFor(&agentDef{Name: "claude", Binary: "/bin/true"}, dir, 30)
	if ref != nil {
		t.Fatal(ref)
	}
	named := parseFirstReadPath(t, args.prompt)
	wantDisplay := filepath.ToSlash(filepath.Join(bundleDisplay(t, root, dir), filepath.Base(named)))
	if got := filepath.ToSlash(filepath.Clean(named)); got != wantDisplay {
		t.Fatalf("drain directive names %q, want the bundle file %q", got, wantDisplay)
	}
	raw, err := os.ReadFile(filepath.Join(dir, filepath.Base(named)))
	if err != nil {
		t.Fatalf("read named drain Procedure file: %v", err)
	}
	if !bytes.Equal(raw, want) {
		t.Fatalf("named drain Procedure file holds %d bytes, want the embedded assembly (%d)", len(raw), len(want))
	}
}
