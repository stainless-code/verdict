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
// discovery restriction (nil when the row declares none, the operator opted
// out, or a pass-through flag already decides tool selection), the model hint
// a --print --stage render declares (empty for everything else), the
// operator's pass-through arguments unchanged, an optional prompt flag, then
// the directive.
func argvFor(def *agentcatalog.Launch, model string, restriction, passthrough []string, prompt string) []string {
	var argv []string
	argv = append(argv, def.Binary)
	argv = append(argv, def.Args...)
	argv = append(argv, restriction...)
	if model != "" && def.ModelFlag != "" {
		argv = append(argv, def.ModelFlag, model)
	}
	argv = append(argv, passthrough...)
	if def.PromptFlag != "" {
		argv = append(argv, def.PromptFlag)
	}
	return append(argv, prompt)
}

// argvPrefixLen counts the argv entries before the operator's pass-through:
// the binary, the definition's flags, the discovery restriction, and the model
// hint. The recorded argv shape redacts the pass-through at these indexes, so
// it counts the same four groups argvFor lays down.
func argvPrefixLen(def *agentcatalog.Launch, model string, restriction []string) int {
	n := 1 + len(def.Args) + len(restriction)
	if model != "" && def.ModelFlag != "" {
		n += 2
	}
	return n
}

// discoveryRestriction is the read-only restriction one launch carries: the
// row's documented tool flags, unless the operator allowed the shell back with
// --allow-shell or already decided tool selection after `--`. A pass-through
// spelling suppresses it rather than riding beside it, so one argv never holds
// two answers to the same question.
func discoveryRestriction(def *agentcatalog.Launch, passthrough []string, allowShell bool) []string {
	if allowShell || len(def.DiscoveryArgs) == 0 {
		return nil
	}
	for _, arg := range passthrough {
		if name, _, _ := strings.Cut(arg, "="); slices.Contains(def.DiscoveryOverrideFlags, name) {
			return nil
		}
	}
	return slices.Clone(def.DiscoveryArgs)
}

// knownAgents renders the known-name list for a refusal message.
func knownAgents() string {
	return strings.Join(AgentNames(), ", ")
}
