package run

import (
	"slices"
	"strings"
)

// agentDef is one launch definition: the --agent name an operator types, the
// binary it spawns, the flag that binary takes a model on, the flag that takes
// its prompt as a value (empty for a positional prompt), and the non-interactive
// argument shape that binary uses to answer once and exit.
//
// The launcher reads every field and branches on none, so adding an agent is
// adding a row to [agentDefs] — data, not a code path. The one convention every
// row obeys is the argument order the launcher builds (see [argvFor]):
//
//	<binary> <args...> [<modelflag> <model>] [<passthrough...>] <prompt>
//	<binary> <args...> [<modelflag> <model>] [<passthrough...>] <promptflag> <prompt>
//
// The agent's own non-interactive flags come first so the operator's
// pass-through arguments cannot cancel them, the operator's flags come next so
// per-agent model/cost selection stays the operator's business, and the
// tool-generated directive is the last argument. A PromptFlag sits immediately
// before that directive, so a pass-through flag can never be swallowed as its
// value. A binary that wants its prompt somewhere else (a file path, stdin)
// does not fit this shape yet. ModelFlag is read only by a --print --stage
// render, which prints the stage's declared hint as that argument; a launch
// never composes one.
type agentDef struct {
	Name       string
	Binary     string
	Install    string
	ModelFlag  string
	PromptFlag string
	Args       []string
}

// modelFlag is the argument every shipped row takes a model name on; a row
// whose binary spells it differently carries its own value.
const modelFlag = "--model"

// agentDefs is the shipped agent table, sorted by name. Every row is data: a
// new agent is a new entry here, and nothing else in the launcher changes.
// Permission and approval flags stay with the operator via `-- <args>`.
var agentDefs = []agentDef{
	// https://github.com/google-antigravity/antigravity-cli — read 2026-11-17
	{
		Name: "agy", Binary: "agy", ModelFlag: modelFlag, PromptFlag: "-p",
		Install: "curl -fsSL https://antigravity.google/cli/install.sh | bash",
	},
	// https://ampcode.com/docs/markdown/cli/execute-mode — read 2026-11-17
	{
		Name: "amp", Binary: "amp", ModelFlag: modelFlag, PromptFlag: "-x",
		Install: "npm install -g @ampcode/cli",
	},
	// https://code.claude.com/docs/en/cli-reference.md — read 2026-11-17
	{
		Name: "claude", Binary: "claude", ModelFlag: modelFlag, Args: []string{"-p"},
		Install: "curl -fsSL https://claude.ai/install.sh | bash",
	},
	// https://github.com/openai/codex/blob/main/README.md — read 2026-11-17
	{
		Name: "codex", Binary: "codex", ModelFlag: modelFlag, Args: []string{"exec"},
		Install: "npm install -g @openai/codex",
	},
	// https://docs.github.com/en/copilot/how-tos/copilot-cli/automate-copilot-cli/run-cli-programmatically — read 2026-11-17
	{
		Name: "copilot", Binary: "copilot", ModelFlag: modelFlag, PromptFlag: "-p",
		Install: "npm install -g @github/copilot",
	},
	// https://github.com/charmbracelet/crush/blob/main/internal/cmd/run.go — read 2026-11-17
	{
		Name: "crush", Binary: "crush", ModelFlag: modelFlag, Args: []string{"run"},
		Install: "brew install charmbracelet/tap/crush",
	},
	// https://cursor.com/docs/cli/headless — read 2026-11-17
	{
		Name: "cursor", Binary: "cursor-agent", ModelFlag: modelFlag, Args: []string{"-p"},
		Install: "curl https://cursor.com/install -fsS | bash",
	},
	// https://docs.factory.com/droid-cli/cli-reference.md — read 2026-11-17
	{
		Name: "droid", Binary: "droid", ModelFlag: modelFlag, Args: []string{"exec"},
		Install: "curl -fsSL https://app.factory.ai/cli | sh",
	},
	// https://github.com/google-gemini/gemini-cli/blob/main/README.md — read 2026-11-17
	{
		Name: "gemini", Binary: "gemini", ModelFlag: modelFlag, PromptFlag: "-p",
		Install: "npm install -g @google/gemini-cli",
	},
	// https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/running-tasks.md — read 2026-11-17
	{
		Name: "goose", Binary: "goose", ModelFlag: modelFlag, PromptFlag: "-t", Args: []string{"run"},
		Install: "brew install block-goose-cli",
	},
	// https://kiro.dev/docs/cli/headless.md — read 2026-11-17
	{
		Name: "kiro", Binary: "kiro-cli", ModelFlag: modelFlag,
		Args:    []string{"chat", "--no-interactive"},
		Install: "curl -fsSL https://cli.kiro.dev/install | bash",
	},
	// https://opencode.ai/docs/cli/ — read 2026-11-17
	{
		Name: "opencode", Binary: "opencode", ModelFlag: modelFlag, Args: []string{"run"},
		Install: "npm install -g @opencode/cli",
	},
	// https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/quickstart.md — read 2026-11-17
	{
		Name: "pi", Binary: "pi", ModelFlag: modelFlag, Args: []string{"-p"},
		Install: "curl -fsSL https://pi.dev/install.sh | sh",
	},
	// https://github.com/QwenLM/qwen-code/blob/main/packages/cli/src/config/top-level-options.ts — read 2026-11-17
	{
		Name: "qwen", Binary: "qwen", ModelFlag: modelFlag,
		Install: "npm install -g @qwen-code/qwen-code@latest",
	},
}

// lookupAgent resolves one --agent name against the table.
func lookupAgent(name string) (agentDef, bool) {
	for _, def := range agentDefs {
		if def.Name == name {
			return def, true
		}
	}
	return agentDef{}, false
}

// AgentNames returns the known agent names, for the unknown-agent refusal and
// the other verbs that list them. The four rows shipped before STA-440 keep
// their prefix, so a caller that matched that fragment still finds it; the rest
// follow in table order.
func AgentNames() []string {
	first := []string{"claude", "codex", "cursor", "opencode"}
	names := make([]string, 0, len(agentDefs))
	for _, name := range first {
		if _, ok := lookupAgent(name); ok {
			names = append(names, name)
		}
	}
	for _, def := range agentDefs {
		if !slices.Contains(first, def.Name) {
			names = append(names, def.Name)
		}
	}
	return names
}

// argvFor builds the exact argv the launcher spawns, and the same argv --print
// renders: the binary, the definition's non-interactive flags, the model hint a
// --print --stage render declares (empty for everything else), the operator's
// pass-through arguments unchanged, an optional prompt flag, then the directive.
func argvFor(def *agentDef, model string, passthrough []string, prompt string) []string {
	var argv []string
	argv = append(argv, def.Binary)
	argv = append(argv, def.Args...)
	if model != "" {
		argv = append(argv, def.ModelFlag, model)
	}
	argv = append(argv, passthrough...)
	if def.PromptFlag != "" {
		argv = append(argv, def.PromptFlag)
	}
	return append(argv, prompt)
}

// knownAgents renders the known-name list for a refusal message.
func knownAgents() string {
	return strings.Join(AgentNames(), ", ")
}
