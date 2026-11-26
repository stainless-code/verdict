package run

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/config"
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
	if allowShell || len(def.DiscoveryArgs) == 0 || operatorDecidesTools(def, passthrough) {
		return nil
	}
	return slices.Clone(def.DiscoveryArgs)
}

// operatorDecidesTools reports whether the operator's pass-through already
// spells one of the row's tool-permission flags. Only the `--` tail counts:
// the restriction and the review grant verdict adds are never in it.
func operatorDecidesTools(def *agentcatalog.Launch, passthrough []string) bool {
	for _, arg := range passthrough {
		if name, _, _ := strings.Cut(arg, "="); slices.Contains(def.DiscoveryOverrideFlags, name) {
			return true
		}
	}
	return false
}

// reviewGrant is the row's scoped review-write grant for one launch whose
// artifacts land in dir: nil when the row documents none, when the
// operator's `--` tail decides tool permissions itself, or when dir cannot
// be spelled as a rule path (the launch then runs as before, and a missing
// review refuses no-review-artifact).
func reviewGrant(def *agentcatalog.Launch, passthrough []string, cwd, dir string) []string {
	if len(def.ReviewWriteArgs) == 0 || operatorDecidesTools(def, passthrough) {
		return nil
	}
	path, ok := rulePath(cwd, dir)
	if !ok {
		return nil
	}
	grant := make([]string, 0, len(def.ReviewWriteArgs))
	for _, arg := range def.ReviewWriteArgs {
		grant = append(grant, strings.ReplaceAll(arg, agentcatalog.ReviewDir, path))
	}
	return grant
}

// rulePathSafe is every byte a rule path may carry: anything else (a glob
// metacharacter, a parenthesis, a comma or a space the flag splits on) would
// widen or break the rule, so such a directory gets no grant.
const rulePathSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./~@+="

// rulePath spells dir as a Claude Code permission-rule path from the agent's
// working directory cwd: `./<rel>` inside it, `//<abs>` outside it, with a
// Windows drive in the POSIX form rules match (C:\x is //c/x).
func rulePath(cwd, dir string) (string, bool) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	path, inside := relativeRulePath(cwd, absDir)
	if path == "" && !inside {
		path = absoluteRulePath(absDir)
	}
	if path == "" || strings.Trim(path, rulePathSafe) != "" {
		return "", false
	}
	return path, true
}

// relativeRulePath is absDir as `./<rel>` when it lies under cwd. inside is
// true for cwd itself, which gets no path: a grant there is the whole tree.
func relativeRulePath(cwd, absDir string) (path string, inside bool) {
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absCwd, absDir)
	switch {
	case err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)):
		return "", false
	case rel == ".":
		return "", true
	}
	return "./" + filepath.ToSlash(rel), true
}

// absoluteRulePath is an absolute directory in the rule's `//` form, "" for
// a volume the rule syntax has no spelling for (a UNC share).
func absoluteRulePath(abs string) string {
	vol := filepath.VolumeName(abs)
	switch {
	case vol == "":
		return "/" + filepath.ToSlash(abs)
	case len(vol) == 2 && vol[1] == ':':
		return "//" + strings.ToLower(vol[:1]) + filepath.ToSlash(abs[len(vol):])
	}
	return ""
}

// EditArgs is the edit grant the named agent's row documents: the shipped
// row's EditArgs, nil for a prefs-defined agent (its definition declares no
// grant) and for an unknown name. The self-review loop appends it to every
// spawn unless the operator passed their own agent arguments.
func EditArgs(name string, agents []config.PersonalAgent) []string {
	st := &state{defs: agents}
	def, ok := st.lookupAgent(name)
	if !ok {
		return nil
	}
	return slices.Clone(def.EditArgs)
}

// knownAgents renders the known-name list for a refusal message.
func knownAgents() string {
	return strings.Join(AgentNames(), ", ")
}
