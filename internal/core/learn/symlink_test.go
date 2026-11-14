package learncore

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/kb"
)

// symlinkSeed is planted outside the repository: the escape fixture holds a
// store the veto modes can find, so their refusals decide nothing before the
// lock is asked for.
const symlinkSeed = `{"id":"active-one","rule":"Retire the first active rule","scope":"internal","provenance":"manual","rigor":"walked","status":"active"}
{"id":"retired-one","rule":"Restore the first retired rule","scope":"internal","provenance":"manual","rigor":"walked","status":"retired"}
`

// TestKBWritesRefuseAnEscapingVerdictSymlink proves every knowledge-base write
// mode maps the layout refusal: with `.verdict` symlinked outside the
// repository, --rule, --pack, --retire, and --restore refuse bad-config in the
// layout's own sentence — `.verdict` named once, not "learn could not write
// .verdict/kb.jsonl: lock .verdict/kb.jsonl: …" — and the link target gains
// nothing.
func TestKBWritesRefuseAnEscapingVerdictSymlink(t *testing.T) {
	pack := importFixture(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, filepath.Base(kb.File)), []byte(symlinkSeed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, config.VerdictDir)); err != nil {
		t.Fatalf("symlink .verdict: %v", err)
	}
	before := dirNames(t, outside)
	for _, mode := range writeModes(pack) {
		assertLayoutRefusal(t, root, mode)
	}
	if got := dirNames(t, outside); !slices.Equal(got, before) {
		t.Fatalf("the link target gained files: %v; want %v", got, before)
	}
}

// writeMode is one knowledge-base write mode and the request that drives it.
type writeMode struct {
	req  *Request
	mode string
}

// writeModes builds one request per knowledge-base write mode.
func writeModes(pack string) []writeMode {
	return []writeMode{
		{mode: "--rule", req: &Request{Rule: "Prefer literal argv", Scope: "internal", Rigor: "walked"}},
		{mode: "--pack", req: &Request{Pack: pack, ID: "safe-rule", Rigor: "pointed"}},
		{mode: "--retire", req: &Request{Retire: "active-one", Reason: vetoReason}},
		{mode: "--restore", req: &Request{Restore: "retired-one", Reason: vetoReason}},
	}
}

// assertLayoutRefusal runs one mode and pins the layout's bad-config sentence.
func assertLayoutRefusal(t *testing.T, root string, mode writeMode) {
	t.Helper()
	req := mode.req
	req.RepoRoot = root
	req.Runtime = &core.Runtime{}
	_, ref := Run(t.Context(), req)
	if ref == nil {
		t.Fatalf("%s on an escaping .verdict symlink = nil; want bad-config", mode.mode)
	}
	if ref.Code != config.CodeBadConfig {
		t.Errorf("%s code = %q (%s); want %q", mode.mode, ref.Code, ref.Message, config.CodeBadConfig)
	}
	if ref.Message != "refuses to write outside the repository: .verdict" {
		t.Errorf("%s message = %q; want the layout sentence naming .verdict once", mode.mode, ref.Message)
	}
}

// dirNames lists one directory's names in readdir order.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
