package democore

import (
	"strings"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/core"
)

// agentPreference is the order the demo looks for an installed agent CLI: the
// first row that resolves wins, so the demo needs no configuration to pick a
// sensible default. Rows the filter below drops stay in the list on purpose —
// they document the intent, and a unit test pins every name here against the
// catalog so a rename fails loudly instead of silently shrinking the list.
var agentPreference = []string{
	"claude", "codex", "opencode", "pi", "cursor", "gemini", "qwen",
	"goose", "amp", "droid", "kimi", "kiro", "crush", "copilot",
}

// guideAgents are the four rows the "nothing installed" guide names: the
// shortlist an operator is most likely to reach for.
var guideAgents = []string{"claude", "codex", "opencode", "pi"}

// msgNoAgent is the first line of the agent-missing refusal: what happened, and
// the one action that fixes it.
const msgNoAgent = "no coding agent CLI found on PATH — install one and re-run"

// detectAgent picks the first runnable shipped agent on PATH. Detection is a
// binary-presence probe, not an authentication check: it is deterministic,
// offline, and one lookup per candidate, and an installed-but-unauthenticated
// agent fails later as its own typed refusal with its stderr recorded.
func detectAgent(look func(string) (string, error)) (agentcatalog.Launch, bool) {
	candidates := agentCandidates()
	for i := range candidates {
		if _, err := look(candidates[i].Binary); err == nil {
			return candidates[i], true
		}
	}
	return agentcatalog.Launch{}, false
}

// agentCandidates is the filtered, ordered row list detection walks: the
// preference names first (in their documented order), then every remaining
// runnable launch row in table order.
func agentCandidates() []agentcatalog.Launch {
	out := make([]agentcatalog.Launch, 0, len(agentPreference))
	taken := make(map[string]bool, len(agentPreference))
	for _, name := range agentPreference {
		if row, ok := runnableRow(name); ok {
			out = append(out, row)
			taken[name] = true
		}
	}
	for _, row := range agentcatalog.All() {
		if taken[row.Name] || !autoRunnable(row) {
			continue
		}
		out = append(out, *row.Launch)
		taken[row.Name] = true
	}
	return out
}

// runnableRow resolves one preference name to its row when the demo may pick
// it by itself.
func runnableRow(name string) (agentcatalog.Launch, bool) {
	row, ok := agentcatalog.Lookup(name)
	if !ok || !autoRunnable(row) {
		return agentcatalog.Launch{}, false
	}
	return *row.Launch, true
}

// autoRunnable reports whether the demo may pick this row by itself. A row
// carrying a launch warning soft-denies the writes a review needs, and a row
// probing for its identity shares its binary name with another tool, so
// neither can be attributed to the agent the operator actually has.
func autoRunnable(row agentcatalog.Agent) bool {
	return row.Launch != nil && row.Launch.LaunchWarning == "" && len(row.Launch.ProbeArgs) == 0
}

// noAgentRefusal is the two-line setup guide the demo refuses with when
// nothing on PATH is a shipped agent: the shortlist with the first row's own
// install line, read from the catalog rather than retyped, then the `--agent`
// escape hatch.
func noAgentRefusal() *core.RefusalError {
	return &core.RefusalError{
		Code:    codeAgentMissing,
		Message: core.Msgf(msgNoAgent+"\ntry: %s, or pass --agent <name>", guideLine()),
	}
}

// guideLine renders `claude (<install>), codex, opencode, or pi`. The install
// string belongs to the first row's catalog entry, so a changed command cannot
// drift from the refusal.
func guideLine() string {
	names := make([]string, 0, len(guideAgents))
	names = append(names, guideLabel(guideAgents[0], true))
	for _, name := range guideAgents[1:] {
		names = append(names, guideLabel(name, false))
	}
	return strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
}

// guideLabel names one shortlist row, quoting its install command when asked
// and the row carries one.
func guideLabel(name string, withInstall bool) string {
	if !withInstall {
		return name
	}
	if row, ok := agentcatalog.Lookup(name); ok && row.Launch != nil && row.Launch.Install != "" {
		return name + " (`" + row.Launch.Install + "`)"
	}
	return name
}
