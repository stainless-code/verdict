package run

import (
	"slices"
	"testing"
)

type builtinRow struct {
	name       string
	binary     string
	args       []string
	promptFlag string
	install    string
	wantArgv   []string
}

// TestBuiltinAgentRows pins every shipped row: its binary, its non-interactive
// flags, its official install hint, and the exact argv a model hint and a
// pass-through flag produce. Prompt-bearing rows put their flag immediately
// before the directive; the others keep the positional shape. The name set is
// literal, so a new row cannot join the table without a reviewed expectation.
func TestBuiltinAgentRows(t *testing.T) {
	tests := []builtinRow{
		{
			name: "agy", binary: "agy", promptFlag: "-p",
			install:  "curl -fsSL https://antigravity.google/cli/install.sh | bash",
			wantArgv: []string{"agy", "--model", "M", "--x", "-p", "PROMPT"},
		},
		{
			name: "amp", binary: "amp", promptFlag: "-x",
			install:  "npm install -g @ampcode/cli",
			wantArgv: []string{"amp", "--model", "M", "--x", "-x", "PROMPT"},
		},
		{
			name: "claude", binary: "claude", args: []string{"-p"},
			install:  "curl -fsSL https://claude.ai/install.sh | bash",
			wantArgv: []string{"claude", "-p", "--model", "M", "--x", "PROMPT"},
		},
		{
			name: "codex", binary: "codex", args: []string{"exec"},
			install:  "npm install -g @openai/codex",
			wantArgv: []string{"codex", "exec", "--model", "M", "--x", "PROMPT"},
		},
		{
			name: "copilot", binary: "copilot", promptFlag: "-p",
			install:  "npm install -g @github/copilot",
			wantArgv: []string{"copilot", "--model", "M", "--x", "-p", "PROMPT"},
		},
		{
			name: "crush", binary: "crush", args: []string{"run"},
			install:  "brew install charmbracelet/tap/crush",
			wantArgv: []string{"crush", "run", "--model", "M", "--x", "PROMPT"},
		},
		{
			name: "cursor", binary: "cursor-agent", args: []string{"-p"},
			install:  "curl https://cursor.com/install -fsS | bash",
			wantArgv: []string{"cursor-agent", "-p", "--model", "M", "--x", "PROMPT"},
		},
		{
			name: "droid", binary: "droid", args: []string{"exec"},
			install:  "curl -fsSL https://app.factory.ai/cli | sh",
			wantArgv: []string{"droid", "exec", "--model", "M", "--x", "PROMPT"},
		},
		{
			name: "gemini", binary: "gemini", promptFlag: "-p",
			install:  "npm install -g @google/gemini-cli",
			wantArgv: []string{"gemini", "--model", "M", "--x", "-p", "PROMPT"},
		},
		{
			name: "goose", binary: "goose", args: []string{"run"}, promptFlag: "-t",
			install:  "brew install block-goose-cli",
			wantArgv: []string{"goose", "run", "--model", "M", "--x", "-t", "PROMPT"},
		},
		{
			name: "kiro", binary: "kiro-cli", args: []string{"chat", "--no-interactive"},
			install:  "curl -fsSL https://cli.kiro.dev/install | bash",
			wantArgv: []string{"kiro-cli", "chat", "--no-interactive", "--model", "M", "--x", "PROMPT"},
		},
		{
			name: "opencode", binary: "opencode", args: []string{"run"},
			install:  "npm install -g @opencode/cli",
			wantArgv: []string{"opencode", "run", "--model", "M", "--x", "PROMPT"},
		},
		{
			name: "pi", binary: "pi", args: []string{"-p"},
			install:  "curl -fsSL https://pi.dev/install.sh | sh",
			wantArgv: []string{"pi", "-p", "--model", "M", "--x", "PROMPT"},
		},
		{
			name: "qwen", binary: "qwen",
			install:  "npm install -g @qwen-code/qwen-code@latest",
			wantArgv: []string{"qwen", "--model", "M", "--x", "PROMPT"},
		},
	}
	wantNames := make([]string, 0, len(tests))
	for _, tc := range tests {
		wantNames = append(wantNames, tc.name)
		t.Run(tc.name, func(t *testing.T) { assertBuiltinRow(t, &tc) })
	}
	names := make([]string, 0, len(agentDefs))
	for _, def := range agentDefs {
		names = append(names, def.Name)
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
	if def.Binary != tc.binary || def.PromptFlag != tc.promptFlag || def.Install != tc.install {
		t.Errorf("row = %+v, want binary %q, prompt flag %q, install %q",
			def, tc.binary, tc.promptFlag, tc.install)
	}
	if !slices.Equal(def.Args, tc.args) {
		t.Errorf("args = %q, want %q", def.Args, tc.args)
	}
	if def.ModelFlag != modelFlag {
		t.Errorf("model flag = %q, want %q", def.ModelFlag, modelFlag)
	}
	got := argvFor(&def, "M", []string{"--x"}, "PROMPT")
	if !slices.Equal(got, tc.wantArgv) {
		t.Errorf("argv = %q, want %q", got, tc.wantArgv)
	}
}

// TestArgvForPromptPlacement pins the one shape change in STA-440: a row that
// takes its prompt as a flag value puts that flag immediately before the
// directive, after every operator argument, so a pass-through flag cannot be
// swallowed as its value. Rows without PromptFlag keep the positional shape.
func TestArgvForPromptPlacement(t *testing.T) {
	tests := []struct {
		name string
		def  agentDef
		want []string
	}{
		{
			name: "positional prompt",
			def: agentDef{
				Name: "claude", Binary: "claude", Args: []string{"-p"}, ModelFlag: modelFlag,
			},
			want: []string{"claude", "-p", "--model", "M", "--x", "PROMPT"},
		},
		{
			name: "flag value prompt",
			def: agentDef{
				Name: "amp", Binary: "amp", Args: nil, PromptFlag: "-x", ModelFlag: modelFlag,
			},
			want: []string{"amp", "--model", "M", "--x", "-x", "PROMPT"},
		},
		{
			name: "flag value prompt after a subcommand",
			def: agentDef{
				Name: "goose", Binary: "goose", Args: []string{"run"}, PromptFlag: "-t", ModelFlag: modelFlag,
			},
			want: []string{"goose", "run", "--model", "M", "--x", "-t", "PROMPT"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			def := tc.def
			got := argvFor(&def, "M", []string{"--x"}, "PROMPT")
			if !slices.Equal(got, tc.want) {
				t.Fatalf("argv = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestArgvForOmitsEmptyFields pins the no-model, no-passthrough shape: no
// empty argument is ever handed to a binary, with or without PromptFlag.
func TestArgvForOmitsEmptyFields(t *testing.T) {
	tests := []struct {
		def  agentDef
		want []string
	}{
		{
			def:  agentDef{Name: "codex", Binary: "codex", Args: []string{"exec"}, ModelFlag: modelFlag},
			want: []string{"codex", "exec", "PROMPT"},
		},
		{
			def:  agentDef{Name: "amp", Binary: "amp", PromptFlag: "-x", ModelFlag: modelFlag},
			want: []string{"amp", "-x", "PROMPT"},
		},
	}
	for _, tc := range tests {
		def := tc.def
		if got := argvFor(&def, "", nil, "PROMPT"); !slices.Equal(got, tc.want) {
			t.Errorf("%s argv = %q, want %q", def.Name, got, tc.want)
		}
	}
}
