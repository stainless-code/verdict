//go:build !windows

package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

// probeIdentityCase is one shipped row whose binary name belongs to another
// tool too. The clash text is that tool's plausible help; the own text is the
// flag quoted in the row's source comment.
type probeIdentityCase struct {
	name      string
	match     string
	clashHelp string
	ownHelp   string
	probeArgs []string
}

var probeIdentityCases = []probeIdentityCase{
	{
		name: "bob", probeArgs: []string{"run", "--help"}, match: "--disable-tool-groups",
		clashHelp: "Usage: bob run <version>\n  list        show installed versions",
		ownHelp:   "Usage: bob run [options]\n  --disable-tool-groups <groups>  Disable specific tool groups",
	},
	{
		name: "cortex", probeArgs: []string{"--help"}, match: "--dangerously-allow-all-tool-calls",
		clashHelp: "Usage: cortex query <version>\n  -query.duration <duration>",
		ownHelp:   "Usage: cortex [options]\n  --bypass, --dangerously-allow-all-tool-calls  Enable bypass mode",
	},
	{
		name: "forge", probeArgs: []string{"--help"}, match: "--conversation-id",
		clashHelp: "Usage: forge render <version>\n  --benchmark  measure render time",
		ownHelp:   "Usage: forge [options]\n  --conversation-id <id>  Resume a named conversation (alias --cid)",
	},
	{
		name: "fx", probeArgs: []string{"ask", "--help"}, match: "--ultrafast",
		clashHelp: "Usage: fx run <version>\n  -r, --raw  print raw JSON",
		ownHelp:   "Usage: fx ask [options]\n  --fast, --ultrafast  request a faster answer",
	},
	{
		name: "grok", probeArgs: []string{"--help"}, match: "--prompt-json",
		clashHelp: "Usage: grok match <version>\n  -e, --expression <regex>",
		ownHelp:   "Usage: grok [options]\n  --prompt-json <JSON>  supply the prompt as JSON",
	},
	{
		name: "kilo", probeArgs: []string{"run", "--help"}, match: "--cloud-fork",
		clashHelp: "Usage: kilo run <version>\n  -v, --verbose  show editor details",
		ownHelp:   "Usage: kilo run [options]\n  --cloud-fork  fetch session from cloud and continue locally (use with --session)",
	},
}

// TestClashingBinaryNeedsDocumentedFlag proves both halves of each identity
// probe: a same-named tool that prints its own help, even with the subcommand
// the probe asks for, refuses before launch, and the documented flag passes.
func TestClashingBinaryNeedsDocumentedFlag(t *testing.T) {
	for i := range probeIdentityCases {
		tc := &probeIdentityCases[i]
		t.Run(tc.name+"/clash", func(t *testing.T) { assertClashRefused(t, tc) })
		t.Run(tc.name+"/own-help", func(t *testing.T) { assertDocumentedFlagPasses(t, tc) })
	}
}

func assertClashRefused(t *testing.T, tc *probeIdentityCase) {
	t.Helper()
	root := fixtureRoot(t)
	bin := plantStub(t, tc.name, "#!/bin/sh\nprintf '%s\\n' "+shellLiteral(tc.clashHelp)+"\n")
	_, ref := runWithStub(t, root, tc.name, bin)
	if ref == nil || ref.Code != "agent-mismatch" {
		t.Fatalf("ref = %+v, want agent-mismatch", ref)
	}
	if !strings.Contains(ref.Message, tc.match) {
		t.Errorf("message = %q, want the missing match %q", ref.Message, tc.match)
	}
	if _, err := os.Stat(filepath.Join(bundleDir(root), reviewFile)); !os.IsNotExist(err) {
		t.Errorf("review file exists after refusal (err = %v); want no launch", err)
	}
}

func assertDocumentedFlagPasses(t *testing.T, tc *probeIdentityCase) {
	t.Helper()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	bin := plantStub(t, tc.name, "#!/bin/sh\n"+
		"case \" $* \" in\n"+
		"  *' --help '*) printf '%s\\n' "+shellLiteral(tc.ownHelp)+"; exit 0 ;;\n"+
		"esac\n"+
		"printf '%s' "+shellLiteral(reviewDoc)+" > .verdict/state/42/review.json\n")
	_, ref := runWithStub(t, root, tc.name, bin)
	if ref != nil {
		t.Fatalf("launch refused: %v", ref)
	}
	if _, err := os.Stat(filepath.Join(dir, reviewFile)); err != nil {
		t.Fatalf("review file missing after a passing probe: %v", err)
	}
	rec := readLauncher(t, dir)
	probe, ok := jq.AsMap(rec["probe"])
	if !ok || probe["ok"] != true {
		t.Fatalf("launcher probe = %v, want ok=true", rec["probe"])
	}
	args, ok := probe["args"].([]any)
	if !ok || !slices.Equal(args, stringsToAny(tc.probeArgs)) {
		t.Errorf("launcher probe args = %v, want %q", probe["args"], tc.probeArgs)
	}
}

// plantStub writes an executable shell stand-in under a fresh temporary dir.
func plantStub(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

// runWithStub resolves the shipped row's agent to the stand-in and runs the
// real process commander, so the probe sees the script's own stdout.
func runWithStub(t *testing.T, root, name, bin string) (*core.RunResponse, *core.RefusalError) {
	t.Helper()
	req := request(root, coreexec.Watchdog{})
	req.Agent = name
	req.LookPath = func(string) (string, error) { return bin, nil }
	return Run(t.Context(), req)
}

func stringsToAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func shellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
