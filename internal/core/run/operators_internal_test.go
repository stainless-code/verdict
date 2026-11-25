package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/jq"
)

func operatorDef(name string) config.PersonalAgent {
	return config.PersonalAgent{
		Name: name, Binary: "local-agent", Args: []string{"exec"},
		PromptFlag: "-t", ModelFlag: "-m", Install: "brew install local-agent",
	}
}

// TestBuiltinNamesMatchTheConfigList keeps the prefs parser's collision list
// equal to the shipped table: a new row that the parser does not refuse as a
// name would let a prefs file shadow it.
func TestBuiltinNamesMatchTheConfigList(t *testing.T) {
	t.Parallel()
	got, want := config.BuiltinAgentNames(), AgentNames()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("config collision list = %q, run table = %q", got, want)
	}
}

// TestOperatorAgentLaunchAndPrint pins the custom-row contract: argv shape,
// pass-through, prompt placement, print parity, and the provenance marker that
// says where the definition came from.
func TestOperatorAgentLaunchAndPrint(t *testing.T) {
	t.Parallel()
	defs := []config.PersonalAgent{operatorDef("local")}
	root := fixtureRoot(t)
	dir := bundleDir(root)
	printReq := request(root, &scripted{})
	printReq.Agent = "local"
	printReq.PassThrough = []string{"--max-turns", "3"}
	printReq.Print = true
	printed, ref := WithAgents(t.Context(), printReq, defs)
	if ref != nil {
		t.Fatalf("print refused: %v", ref)
	}
	want := []string{"local-agent", "exec", "--max-turns", "3", "-t", printed.Data.Argv[len(printed.Data.Argv)-1]}
	if !slices.Equal(printed.Data.Argv, want) {
		t.Errorf("printed argv = %q, want %q", printed.Data.Argv, want)
	}

	cmdr := &scripted{write: func() { writeReview(t, dir) }}
	launchReq := request(root, cmdr)
	launchReq.Agent = "local"
	launchReq.PassThrough = []string{"--max-turns", "3"}
	if _, ref := WithAgents(t.Context(), launchReq, defs); ref != nil {
		t.Fatalf("launch refused: %v", ref)
	}
	if cmdr.cmd == nil || !slices.Equal(printed.Data.Argv, cmdr.cmd.Argv) {
		t.Fatalf("printed argv = %q, spawned %+v", printed.Data.Argv, cmdr.cmd)
	}
	rec := readLauncher(t, dir)
	if got := jq.StrOf(rec["agent_source"]); got != "prefs" {
		t.Errorf("launcher agent_source = %q, want prefs", got)
	}
	if got := jq.StrOf(rec["agent"]); got != "local" {
		t.Errorf("launcher agent = %q, want local", got)
	}
}

// TestOperatorAgentMissingBinaryKeepsItsInstallHint proves a custom row uses
// the built-in missing-binary refusal and carries its own install step.
func TestOperatorAgentMissingBinaryKeepsItsInstallHint(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	req := request(root, &scripted{})
	req.Agent = "local"
	req.LookPath = missingLookPath
	_, ref := WithAgents(t.Context(), req, []config.PersonalAgent{operatorDef("local")})
	if ref == nil || ref.Code != codeAgentMissing {
		t.Fatalf("refusal = %+v, want agent-missing", ref)
	}
	if !strings.Contains(ref.Message, "brew install local-agent") {
		t.Errorf("message = %q, want the definition's install hint", ref.Message)
	}
}

// TestOperatorAgentDiscoveryLabelsBothSources pins the unknown-agent list:
// built-in names keep their prefix and the prefs names are labeled separately.
func TestOperatorAgentDiscoveryLabelsBothSources(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	req := request(root, &scripted{})
	req.Agent = "nope"
	_, ref := WithAgents(t.Context(), req, []config.PersonalAgent{operatorDef("local")})
	if ref == nil || ref.Code != codeUnknownAgent {
		t.Fatalf("refusal = %+v, want unknown-agent", ref)
	}
	for _, fragment := range []string{
		"known: claude, codex, cursor, opencode",
		"operator-defined in prefs: local",
	} {
		if !strings.Contains(ref.Message, fragment) {
			t.Errorf("message = %q, want %q", ref.Message, fragment)
		}
	}
}

// TestOperatorAgentCollisionRefusesSettings pins the no-shadowing rule in the
// service too, for callers that supply definitions without the prefs parser.
func TestOperatorAgentCollisionRefusesSettings(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	req := request(root, &scripted{})
	req.Agent = "claude"
	_, ref := WithAgents(t.Context(), req, []config.PersonalAgent{operatorDef("claude")})
	if ref == nil || ref.Code != codeBadSettings {
		t.Fatalf("refusal = %+v, want bad-settings", ref)
	}
	if !strings.Contains(ref.Message, "claude") {
		t.Errorf("message = %q, want the colliding name", ref.Message)
	}
}

// TestOperatorAgentWithoutModelFlag proves an optional model flag renders no
// model argument, rather than an empty argument the binary would misread.
func TestOperatorAgentWithoutModelFlag(t *testing.T) {
	t.Parallel()
	def := agentcatalog.Launch{Name: "local", Binary: "local-agent", ModelFlag: "", PromptFlag: "-t"}
	if got := argvFor(&def, "M", nil, []string{"--x"}, "PROMPT"); !slices.Equal(got,
		[]string{"local-agent", "--x", "-t", "PROMPT"}) {
		t.Errorf("argv = %q, want no model argument", got)
	}
}
