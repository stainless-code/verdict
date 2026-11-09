package run

import (
	"strings"
)

// agentDef is one launch definition: the --agent name an operator types, the
// binary it spawns, and the non-interactive argument shape that binary uses to
// answer once and exit.
//
// The launcher reads every field and branches on none, so adding an agent is
// adding a row to [agentDefs] — data, not a code path. The one convention every
// row obeys is the argument order the launcher builds (see [argvFor]):
//
//	<binary> <args...> [<passthrough...>] <prompt>
//
// The agent's own non-interactive flags come first so the operator's
// pass-through arguments cannot cancel them, the operator's flags come next so
// per-agent model/cost selection stays the operator's business, and the
// tool-generated directive is the last argument. A binary that wants its prompt
// somewhere else (a file path, stdin) does not fit this shape yet.
type agentDef struct {
	Name   string
	Binary string
	Args   []string
}

// agentDefs is the shipped agent table, sorted by name. Every row is data: a
// new agent is a new entry here, and nothing else in the launcher changes.
var agentDefs = []agentDef{
	{Name: "claude", Binary: "claude", Args: []string{"-p"}},
	{Name: "codex", Binary: "codex", Args: []string{"exec"}},
	{Name: "cursor", Binary: "cursor-agent", Args: []string{"-p"}},
	{Name: "opencode", Binary: "opencode", Args: []string{"run"}},
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
// renders: the binary, the definition's non-interactive flags, the operator's
// pass-through arguments unchanged, then the directive.
func argvFor(def agentDef, passthrough []string, prompt string) []string {
	argv := make([]string, 0, len(def.Args)+len(passthrough)+2)
	argv = append(argv, def.Binary)
	argv = append(argv, def.Args...)
	argv = append(argv, passthrough...)
	return append(argv, prompt)
}

// knownAgents renders the known-name list for a refusal message.
func knownAgents() string {
	return strings.Join(AgentNames(), ", ")
}
