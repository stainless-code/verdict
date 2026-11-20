package run

import (
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/agentcatalog"
)

// modelFlag is the argument every shipped row takes a model name on; a row
// whose binary spells it differently carries its own value.
const modelFlag = "--model"

// agentSourcePrefs marks a definition read from the operator's prefs file in
// the provenance record. A shipped row leaves Source empty.
const agentSourcePrefs = "prefs"

// Discovery preserves the prefix shipped before the catalog expanded.
const (
	nameClaude   = "claude"
	nameCodex    = "codex"
	nameCursor   = "cursor"
	nameOpenCode = "opencode"
)

// lookupAgent resolves one --agent name against the table.
func lookupAgent(name string) (agentcatalog.Launch, bool) {
	for _, row := range agentcatalog.All() {
		if row.Launch != nil && row.Name == name {
			return *row.Launch, true
		}
	}
	return agentcatalog.Launch{}, false
}

// AgentNames returns the known agent names, for the unknown-agent refusal and
// the other verbs that list them. The four rows shipped before STA-440 keep
// their prefix, so a caller that matched that fragment still finds it; the rest
// follow in table order.
func AgentNames() []string {
	first := []string{nameClaude, nameCodex, nameCursor, nameOpenCode}
	names := make([]string, 0, len(agentcatalog.All()))
	for _, name := range first {
		if _, ok := lookupAgent(name); ok {
			names = append(names, name)
		}
	}
	for _, row := range agentcatalog.All() {
		name := row.Name
		if row.Launch != nil && !slices.Contains(first, name) {
			names = append(names, name)
		}
	}
	return names
}

// argvFor builds the exact argv the launcher spawns, and the same argv
// --print renders: the binary, the definition's non-interactive flags, the
// model hint a --print --stage render declares (empty for everything else),
// the operator's pass-through arguments unchanged, an optional prompt flag,
// then the directive.
func argvFor(def *agentcatalog.Launch, model string, passthrough []string, prompt string) []string {
	var argv []string
	argv = append(argv, def.Binary)
	argv = append(argv, def.Args...)
	if model != "" && def.ModelFlag != "" {
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
