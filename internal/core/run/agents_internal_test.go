package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/agentcatalog"
)

type builtinRow struct {
	name          string
	binary        string
	promptFlag    string
	probeMatch    string
	install       string
	launchWarning string
	args          []string
	probeArgs     []string
	wantArgv      []string
	wantPrintArgv []string
	noModel       bool
}

// builtinRows is the literal contract for the shipped table: every row's
// binary, flags, probe, install hint, composed argv, and --print argv. A new
// row cannot join the table without a reviewed expectation here.
func builtinRows() []builtinRow {
	return []builtinRow{
		{
			name: "adal", binary: "adal", args: []string{}, promptFlag: "-q",
			install:       "curl -fsSL https://adal.sylph.ai/install.sh | bash",
			wantArgv:      []string{"adal", "--model", "M", "--x", "-q", "PROMPT"},
			wantPrintArgv: []string{"adal", "--model", "M", "-q", "PROMPT"},
		},
		{
			name: "agy", binary: "agy", args: []string{}, promptFlag: "-p",
			install:       "curl -fsSL https://antigravity.google/cli/install.sh | bash",
			launchWarning: "agy print mode (-p) soft-denies file edits outside trusted workspaces, and a review runs in a fresh copy outside them, so review.json may never be written; allow write_file(.verdict/state/) under permissions.allow in agy's settings or pass -- --dangerously-skip-permissions (your approval bypass)",
			wantArgv:      []string{"agy", "--model", "M", "--x", "-p", "PROMPT"},
			wantPrintArgv: []string{"agy", "--model", "M", "-p", "PROMPT"},
		},
		{
			name: "amp", binary: "amp", args: []string{}, promptFlag: "-x",
			install:       "npm install -g @ampcode/cli",
			wantArgv:      []string{"amp", "--model", "M", "--x", "-x", "PROMPT"},
			wantPrintArgv: []string{"amp", "--model", "M", "-x", "PROMPT"},
		},
		{
			name: "auggie", binary: "auggie", args: []string{"--print"}, promptFlag: "",
			install:       "npm install -g @augmentcode/auggie@prerelease",
			wantArgv:      []string{"auggie", "--print", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"auggie", "--print", "--model", "M", "PROMPT"},
		},
		{
			name: "autohand", binary: "autohand", args: []string{}, promptFlag: "",
			install:       "curl -fsSL https://autohand.ai/install.sh | bash",
			wantArgv:      []string{"autohand", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"autohand", "--model", "M", "PROMPT"},
		},
		{
			name: "bob", binary: "bob", args: []string{"run"}, promptFlag: "",
			noModel:   true,
			probeArgs: []string{"run", "--help"}, probeMatch: "--disable-tool-groups",
			install:       "curl -fsSL https://bob.ibm.com/download/bobshell.sh | bash",
			wantArgv:      []string{"bob", "run", "--disable-tool-groups", "execute,mcp", "--x", "PROMPT"},
			wantPrintArgv: []string{"bob", "run", "--disable-tool-groups", "execute,mcp", "--model", "M", "PROMPT"},
		},
		{
			name: "claude", binary: "claude", args: []string{"-p"}, promptFlag: "",
			install:       "curl -fsSL https://claude.ai/install.sh | bash",
			wantArgv:      []string{"claude", "-p", "--disallowedTools=Bash,PowerShell", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"claude", "-p", "--disallowedTools=Bash,PowerShell", settingsFlag, mcpFlag, reviewGrantFlag, "--model", "M", "PROMPT"},
		},
		{
			name: "cline", binary: "cline", args: []string{}, promptFlag: "",
			install:       "npm install -g cline",
			wantArgv:      []string{"cline", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"cline", "--model", "M", "PROMPT"},
		},
		{
			name: "codearts", binary: "codearts", args: []string{"run"}, promptFlag: "",
			install:       "sh -c \"$(curl -L https://cnnorth4-cloudide-marketplace.obs.cn-north-4.myhuaweicloud.com/codearts/cli_tui/install_script/install.sh)\"",
			wantArgv:      []string{"codearts", "run", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"codearts", "run", "--model", "M", "PROMPT"},
		},
		{
			name: "codebuddy", binary: "codebuddy", args: []string{"-p"}, promptFlag: "",
			install:       "npm install -g @tencent-ai/codebuddy-code",
			wantArgv:      []string{"codebuddy", "-p", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"codebuddy", "-p", "--model", "M", "PROMPT"},
		},
		{
			name: "codex", binary: "codex", args: []string{"exec"}, promptFlag: "",
			install:       "npm install -g @openai/codex",
			wantArgv:      []string{"codex", "exec", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"codex", "exec", "--model", "M", "PROMPT"},
		},
		{
			name: "commandcode", binary: "commandcode", args: []string{}, promptFlag: "-p",
			install:       "npm i -g command-code@latest",
			wantArgv:      []string{"commandcode", "--model", "M", "--x", "-p", "PROMPT"},
			wantPrintArgv: []string{"commandcode", "--model", "M", "-p", "PROMPT"},
		},
		{
			name: "continue", binary: "cn", args: []string{"-p"}, promptFlag: "",
			install:       "npm i -g @continuedev/cli",
			wantArgv:      []string{"cn", "-p", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"cn", "-p", "--model", "M", "PROMPT"},
		},
		{
			name: "copilot", binary: "copilot", args: []string{}, promptFlag: "-p",
			install:       "npm install -g @github/copilot",
			wantArgv:      []string{"copilot", "--model", "M", "--x", "-p", "PROMPT"},
			wantPrintArgv: []string{"copilot", "--model", "M", "-p", "PROMPT"},
		},
		{
			name: "cortex", binary: "cortex", args: []string{}, promptFlag: "-p",
			probeArgs: []string{"--help"}, probeMatch: "--dangerously-allow-all-tool-calls",
			install:       "curl -qLsS https://ai.snowflake.com/static/cc-scripts/install.sh | sh",
			wantArgv:      []string{"cortex", "--model", "M", "--x", "-p", "PROMPT"},
			wantPrintArgv: []string{"cortex", "--model", "M", "-p", "PROMPT"},
		},
		{
			name: "crush", binary: "crush", args: []string{"run"}, promptFlag: "",
			install:       "brew install charmbracelet/tap/crush",
			wantArgv:      []string{"crush", "run", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"crush", "run", "--model", "M", "PROMPT"},
		},
		{
			name: "cursor", binary: "cursor-agent", args: []string{"-p"}, promptFlag: "",
			install:       "curl https://cursor.com/install -fsS | bash",
			wantArgv:      []string{"cursor-agent", "-p", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"cursor-agent", "-p", "--model", "M", "PROMPT"},
		},
		{
			name: "dcode", binary: "dcode", args: []string{}, promptFlag: "-n",
			install:       "curl -LsSf https://langch.in/dcode | bash",
			wantArgv:      []string{"dcode", "--model", "M", "--x", "-n", "PROMPT"},
			wantPrintArgv: []string{"dcode", "--model", "M", "-n", "PROMPT"},
		},
		{
			name: "devin", binary: "devin", args: []string{}, promptFlag: "-p",
			install:       "curl -fsSL https://cli.devin.ai/install.sh | bash",
			wantArgv:      []string{"devin", "--model", "M", "--x", "-p", "PROMPT"},
			wantPrintArgv: []string{"devin", "--model", "M", "-p", "PROMPT"},
		},
		{
			name: "dexto", binary: "dexto", args: []string{"run"}, promptFlag: "",
			install:       "npm install -g dexto",
			wantArgv:      []string{"dexto", "run", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"dexto", "run", "--model", "M", "PROMPT"},
		},
		{
			name: "droid", binary: "droid", args: []string{"exec"}, promptFlag: "",
			install:       "curl -fsSL https://app.factory.ai/cli | sh",
			wantArgv:      []string{"droid", "exec", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"droid", "exec", "--model", "M", "PROMPT"},
		},
		{
			name: "forge", binary: "forge", args: []string{}, promptFlag: "-p",
			noModel:   true,
			probeArgs: []string{"--help"}, probeMatch: "--conversation-id",
			install:       "curl -fsSL https://forgecode.dev/cli | sh",
			wantArgv:      []string{"forge", "--x", "-p", "PROMPT"},
			wantPrintArgv: []string{"forge", "--model", "M", "-p", "PROMPT"},
		},
		{
			name: "fx", binary: "fx", args: []string{"ask"}, promptFlag: "",
			probeArgs: []string{"ask", "--help"}, probeMatch: "--ultrafast",
			install:       "curl -fsSL https://fx.sh/setup.sh | bash",
			wantArgv:      []string{"fx", "ask", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"fx", "ask", "--model", "M", "PROMPT"},
		},
		{
			name: "gemini", binary: "gemini", args: []string{}, promptFlag: "-p",
			install:       "npm install -g @google/gemini-cli",
			wantArgv:      []string{"gemini", "--model", "M", "--x", "-p", "PROMPT"},
			wantPrintArgv: []string{"gemini", "--model", "M", "-p", "PROMPT"},
		},
		{
			name: "goose", binary: "goose", args: []string{"run"}, promptFlag: "-t",
			install:       "brew install block-goose-cli",
			wantArgv:      []string{"goose", "run", "--model", "M", "--x", "-t", "PROMPT"},
			wantPrintArgv: []string{"goose", "run", "--model", "M", "-t", "PROMPT"},
		},
		{
			name: "grok", binary: "grok", args: []string{}, promptFlag: "-p",
			probeArgs: []string{"--help"}, probeMatch: "--prompt-json",
			install:       "curl -fsSL https://x.ai/cli/install.sh | bash",
			wantArgv:      []string{"grok", "--model", "M", "--x", "-p", "PROMPT"},
			wantPrintArgv: []string{"grok", "--model", "M", "-p", "PROMPT"},
		},
		{
			name: "hermes", binary: "hermes", args: []string{}, promptFlag: "-z",
			install:       "curl -fsSL https://hermes-agent.nousresearch.com/install.sh | bash",
			wantArgv:      []string{"hermes", "--model", "M", "--x", "-z", "PROMPT"},
			wantPrintArgv: []string{"hermes", "--model", "M", "-z", "PROMPT"},
		},
		{
			name: "jazz", binary: "jazz", args: []string{"run"}, promptFlag: "",
			noModel:       true,
			install:       "curl -fsSL https://github.com/lvndry/jazz/releases/latest/download/install.sh | bash",
			wantArgv:      []string{"jazz", "run", "--x", "PROMPT"},
			wantPrintArgv: []string{"jazz", "run", "--model", "M", "PROMPT"},
		},
		{
			name: "junie", binary: "junie", args: []string{}, promptFlag: "",
			install:       "curl -fsSL https://junie.jetbrains.com/install.sh | bash",
			wantArgv:      []string{"junie", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"junie", "--model", "M", "PROMPT"},
		},
		{
			name: "kilo", binary: "kilo", args: []string{"run"}, promptFlag: "",
			probeArgs: []string{"run", "--help"}, probeMatch: "--cloud-fork",
			install:       "npm install -g @kilocode/cli",
			wantArgv:      []string{"kilo", "run", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"kilo", "run", "--model", "M", "PROMPT"},
		},
		{
			name: "kimchi", binary: "kimchi", args: []string{}, promptFlag: "-p",
			install:       "curl -fsSL https://github.com/getkimchi/kimchi/releases/latest/download/install.sh | bash",
			wantArgv:      []string{"kimchi", "--model", "M", "--x", "-p", "PROMPT"},
			wantPrintArgv: []string{"kimchi", "--model", "M", "-p", "PROMPT"},
		},
		{
			name: "kimi", binary: "kimi", args: []string{}, promptFlag: "-p",
			install:       "curl -fsSL https://code.kimi.com/kimi-code/install.sh | bash",
			wantArgv:      []string{"kimi", "--model", "M", "--x", "-p", "PROMPT"},
			wantPrintArgv: []string{"kimi", "--model", "M", "-p", "PROMPT"},
		},
		{
			name: "kiro", binary: "kiro-cli", args: []string{"chat", "--no-interactive"}, promptFlag: "",
			install:       "curl -fsSL https://cli.kiro.dev/install | bash",
			wantArgv:      []string{"kiro-cli", "chat", "--no-interactive", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"kiro-cli", "chat", "--no-interactive", "--model", "M", "PROMPT"},
		},
		{
			name: "kode", binary: "kode", args: []string{"-p"}, promptFlag: "",
			install:       "npm install -g @shareai-lab/kode",
			wantArgv:      []string{"kode", "-p", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"kode", "-p", "--model", "M", "PROMPT"},
		},
		{
			name: "mcode", binary: "mcode", args: []string{"exec"}, promptFlag: "",
			install:       "curl -fsSL https://filecdn.minimax.chat/public/install.sh | bash",
			wantArgv:      []string{"mcode", "exec", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"mcode", "exec", "--model", "M", "PROMPT"},
		},
		{
			name: "neovate", binary: "neovate", args: []string{"-q"}, promptFlag: "",
			install:       "npm install -g @neovate/code",
			wantArgv:      []string{"neovate", "-q", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"neovate", "-q", "--model", "M", "PROMPT"},
		},
		{
			name: "openclaw", binary: "openclaw", args: []string{"agent", "exec"}, promptFlag: "",
			install:       "curl -fsSL https://openclaw.ai/install.sh | bash",
			wantArgv:      []string{"openclaw", "agent", "exec", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"openclaw", "agent", "exec", "--model", "M", "PROMPT"},
		},
		{
			name: "opencode", binary: "opencode", args: []string{"run"}, promptFlag: "",
			install:       "npm install -g @opencode/cli",
			wantArgv:      []string{"opencode", "run", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"opencode", "run", "--model", "M", "PROMPT"},
		},
		{
			name: "pa", binary: "pa", args: []string{}, promptFlag: "",
			install:       "curl -fsSL https://assistant.posit.co/builds/install-tui.sh | bash",
			wantArgv:      []string{"pa", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"pa", "--model", "M", "PROMPT"},
		},
		{
			name: "pi", binary: "pi", args: []string{"-p"}, promptFlag: "",
			install:       "curl -fsSL https://pi.dev/install.sh | sh",
			wantArgv:      []string{"pi", "-p", "--exclude-tools", "bash,powershell", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"pi", "-p", "--exclude-tools", "bash,powershell", "--model", "M", "PROMPT"},
		},
		{
			name: "qoder", binary: "qoder", args: []string{"-p"}, promptFlag: "",
			install:       "curl -fsSL https://qoder.com/install | bash",
			wantArgv:      []string{"qoder", "-p", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"qoder", "-p", "--model", "M", "PROMPT"},
		},
		{
			name: "qodercn", binary: "qodercn", args: []string{"-p"}, promptFlag: "",
			install:       "curl -fsSL https://static.qoder.com.cn/qoder-cli-cn/install.sh | bash",
			wantArgv:      []string{"qodercn", "-p", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"qodercn", "-p", "--model", "M", "PROMPT"},
		},
		{
			name: "qwen", binary: "qwen", args: []string{}, promptFlag: "",
			install:       "npm install -g @qwen-code/qwen-code@latest",
			wantArgv:      []string{"qwen", "--model", "M", "--x", "PROMPT"},
			wantPrintArgv: []string{"qwen", "--model", "M", "PROMPT"},
		},
		{
			name: "rovodev", binary: "acli", args: []string{"rovodev", "run"}, promptFlag: "",
			noModel:       true,
			install:       "install Atlassian CLI (acli)",
			wantArgv:      []string{"acli", "rovodev", "run", "--x", "PROMPT"},
			wantPrintArgv: []string{"acli", "rovodev", "run", "--model", "M", "PROMPT"},
		},
		{
			name: "vibe", binary: "vibe", args: []string{}, promptFlag: "--prompt",
			install:       "curl -LsSf https://mistral.ai/vibe/install.sh | bash",
			wantArgv:      []string{"vibe", "--model", "M", "--x", "--prompt", "PROMPT"},
			wantPrintArgv: []string{"vibe", "--model", "M", "--prompt", "PROMPT"},
		},
		{
			name: "zcode", binary: "zcode", args: []string{}, promptFlag: "-p",
			noModel:       true,
			install:       "build @zcode/cli from https://github.com/zai-org/ZCode",
			wantArgv:      []string{"zcode", "--x", "-p", "PROMPT"},
			wantPrintArgv: []string{"zcode", "--model", "M", "-p", "PROMPT"},
		},
	}
}

// TestBuiltinAgentRows pins every shipped row: its binary, its non-interactive
// flags, its identity probe, its official install hint, and the exact argv a
// model hint and a pass-through flag produce. Prompt-bearing rows put their
// flag immediately before the directive; the others keep the positional shape.
// The name set is literal, so a new row cannot join the table unnoticed.
func TestBuiltinAgentRows(t *testing.T) {
	t.Parallel()
	tests := builtinRows()
	wantNames := make([]string, 0, len(tests))
	for _, tc := range tests {
		wantNames = append(wantNames, tc.name)
		t.Run(tc.name, func(t *testing.T) { assertBuiltinRow(t, &tc) })
	}
	names := make([]string, 0, len(AgentNames()))
	for _, row := range agentcatalog.All() {
		if row.Launch != nil {
			names = append(names, row.Name)
		}
	}
	if !slices.Equal(names, wantNames) {
		t.Errorf("table names = %q, want %q", names, wantNames)
	}
}

func assertBuiltinRow(t *testing.T, tc *builtinRow) {
	t.Helper()
	def, ok := lookupAgent(tc.name)
	if !ok {
		t.Fatalf("lookupAgent(%q) found no row", tc.name)
	}
	assertRowFields(t, tc, &def)
	assertRowArgv(t, tc, &def)
}

// assertRowFields pins the data a user can read in --print and launcher.json.
func assertRowFields(t *testing.T, tc *builtinRow, def *agentcatalog.Launch) {
	t.Helper()
	if def.Binary != tc.binary || def.PromptFlag != tc.promptFlag || def.Install != tc.install {
		t.Errorf("row = %+v, want binary %q, prompt flag %q, install %q",
			def, tc.binary, tc.promptFlag, tc.install)
	}
	if !slices.Equal(def.Args, tc.args) || !slices.Equal(def.ProbeArgs, tc.probeArgs) ||
		def.ProbeMatch != tc.probeMatch || def.LaunchWarning != tc.launchWarning {
		t.Errorf("row = %+v, want args %q, probe %q / %q, launch warning %q",
			def, tc.args, tc.probeArgs, tc.probeMatch, tc.launchWarning)
	}
}

// assertRowArgv pins the composed launch argv and the model flag it reflects.
func assertRowArgv(t *testing.T, tc *builtinRow, def *agentcatalog.Launch) {
	t.Helper()
	wantModel := modelFlag
	if tc.noModel {
		wantModel = ""
	}
	if def.ModelFlag != wantModel {
		t.Errorf("model flag = %q, want %q", def.ModelFlag, wantModel)
	}
	// The row's own documented discovery restriction is what `run` emits by
	// default, so the table pins it beside the definition flags.
	got := argvFor(def, "M", def.DiscoveryArgs, []string{"--x"}, "PROMPT")
	if !slices.Equal(got, tc.wantArgv) {
		t.Errorf("argv = %q, want %q", got, tc.wantArgv)
	}
}

// TestBuiltinAgentPrint pins the --print path for every row: the same binary,
// agent flags, pass-through model, prompt flag, and directive, with no process
// spawned. The pass-through model replaces a stage hint on a launch, so no
// second --model comes from the definition.
func TestBuiltinAgentPrint(t *testing.T) {
	t.Parallel()
	for _, tc := range builtinRows() {
		t.Run(tc.name, func(t *testing.T) { assertPrintedRow(t, &tc) })
	}
}

// assertPrintedRow drives one --print render and compares its argv and spawn
// count with the literal row expectation.
func assertPrintedRow(t *testing.T, tc *builtinRow) {
	t.Helper()
	root := fixtureRoot(t)
	cmdr := &scripted{}
	req := request(root, cmdr)
	req.Agent = tc.name
	req.Print = true
	req.PassThrough = []string{"--model", "M"}
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("print refused: %v", ref)
	}
	argv := slices.Clone(resp.Data.Argv)
	if len(argv) == 0 || !strings.HasPrefix(argv[len(argv)-1], "Review acme/app ") {
		t.Fatalf("printed argv = %q, want a directive", argv)
	}
	argv[len(argv)-1] = "PROMPT"
	if !slices.Equal(argv, tc.wantPrintArgv) {
		t.Errorf("print argv = %q, want %q", argv, tc.wantPrintArgv)
	}
	if cmdr.calls != 0 {
		t.Errorf("print spawned %d processes, want zero", cmdr.calls)
	}
}

// TestArgvForPromptPlacement pins the one shape change in STA-440: a row that
// takes its prompt as a flag value puts that flag immediately before the
// directive, after every operator argument, so a pass-through flag cannot be
// swallowed as its value. Rows without PromptFlag keep the positional shape.
func TestArgvForPromptPlacement(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		def  agentcatalog.Launch
		want []string
	}{
		{
			name: "positional prompt",
			def: agentcatalog.Launch{
				Name: "claude", Binary: "claude", Args: []string{"-p"}, ModelFlag: modelFlag,
			},
			want: []string{"claude", "-p", "--model", "M", "--x", "PROMPT"},
		},
		{
			name: "flag value prompt",
			def: agentcatalog.Launch{
				Name: "amp", Binary: "amp", Args: nil, PromptFlag: "-x", ModelFlag: modelFlag,
			},
			want: []string{"amp", "--model", "M", "--x", "-x", "PROMPT"},
		},
		{
			name: "flag value prompt after a subcommand",
			def: agentcatalog.Launch{
				Name: "goose", Binary: "goose", Args: []string{"run"}, PromptFlag: "-t", ModelFlag: modelFlag,
			},
			want: []string{"goose", "run", "--model", "M", "--x", "-t", "PROMPT"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			def := tc.def
			got := argvFor(&def, "M", nil, []string{"--x"}, "PROMPT")
			if !slices.Equal(got, tc.want) {
				t.Fatalf("argv = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestArgvForOmitsEmptyFields pins the no-model, no-passthrough shape: no
// empty argument is ever handed to a binary, with or without PromptFlag.
func TestArgvForOmitsEmptyFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		def  agentcatalog.Launch
		want []string
	}{
		{
			def:  agentcatalog.Launch{Name: "codex", Binary: "codex", Args: []string{"exec"}, ModelFlag: modelFlag},
			want: []string{"codex", "exec", "PROMPT"},
		},
		{
			def:  agentcatalog.Launch{Name: "amp", Binary: "amp", PromptFlag: "-x", ModelFlag: modelFlag},
			want: []string{"amp", "-x", "PROMPT"},
		},
	}
	for _, tc := range tests {
		def := tc.def
		if got := argvFor(&def, "", nil, nil, "PROMPT"); !slices.Equal(got, tc.want) {
			t.Errorf("%s argv = %q, want %q", def.Name, got, tc.want)
		}
	}
}
