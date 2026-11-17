package run

import (
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
var agentDefs = []agentDef{
	{
		Name: "claude", Binary: "claude", ModelFlag: modelFlag, Args: []string{"-p"},
		Install: "install claude using its official installer",
	},
	{
		Name: "codex", Binary: "codex", ModelFlag: modelFlag, Args: []string{"exec"},
		Install: "npm install -g @openai/codex",
	},
	{
		Name: "cursor", Binary: "cursor-agent", ModelFlag: modelFlag, Args: []string{"-p"},
		Install: "install cursor-agent using the Cursor CLI installer",
	},
	{
		Name: "opencode", Binary: "opencode", ModelFlag: modelFlag, Args: []string{"run"},
		Install: "npm install -g opencode-ai",
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

// AgentNames returns the known agent names in table order, for the
// unknown-agent refusal and the usage block.
func AgentNames() []string {
	names := make([]string, 0, len(agentDefs))
	for _, def := range agentDefs {
		names = append(names, def.Name)
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
