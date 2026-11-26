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

// discoveryRestriction is the shell denial one review launch carries: the
// row's documented tool flags, always, unless the operator allowed the shell
// back with --allow-shell. No operator flag replaces it — every exception
// grew a bypass — so it sits beside whatever the `--` tail spells. The one
// placement exception is an operator spelling of the restriction's own
// denylist flag (DiscoveryMergeFlags): mergeDenials has already folded the
// restriction's value into it, because a later spelling of that flag would
// replace an earlier one, so that pass-through carries the denial and the
// restriction is not repeated beside it.
func discoveryRestriction(def *agentcatalog.Launch, passthrough []string, allowShell bool) []string {
	if allowShell || len(def.DiscoveryArgs) == 0 || spellsMergeFlag(def, passthrough) {
		return nil
	}
	return slices.Clone(def.DiscoveryArgs)
}

// spellsMergeFlag reports whether the pass-through spells the restriction's
// own denylist flag anywhere, operand position included: mergeDenials merges
// into every such spelling or refuses the launch.
func spellsMergeFlag(def *agentcatalog.Launch, passthrough []string) bool {
	for _, arg := range passthrough {
		if name, _, _ := strings.Cut(arg, inlineValue); slices.Contains(def.DiscoveryMergeFlags, name) {
			return true
		}
	}
	return false
}

// mergeDenials folds the row's restriction value into every operator
// spelling of the restriction's own denylist flag: pi's `--exclude-tools
// markdown` becomes `--exclude-tools bash,powershell,markdown`, so the
// denylist keeps the shell denial even where a later spelling of the flag
// replaces an earlier one. Every spelling counts, whatever precedes it. Every
// other argument, and every position, is kept, so the recorded argv's
// redaction stays aligned. A spelling with no value — dangling at the end,
// followed by another option, or `--flag=` — is a usage error: bad names it,
// and the launch refuses. --allow-shell and a row without a mergeable
// restriction leave the pass-through as it is.
func mergeDenials(def *agentcatalog.Launch, passthrough []string, allowShell bool) (merged []string, bad string) {
	if allowShell || len(def.DiscoveryArgs) != 2 || len(def.DiscoveryMergeFlags) == 0 {
		return passthrough, ""
	}
	merged = slices.Clone(passthrough)
	for i := 0; i < len(merged); i++ {
		name, value, inline := strings.Cut(merged[i], inlineValue)
		switch {
		case !slices.Contains(def.DiscoveryMergeFlags, name):
		case inline && value != "":
			merged[i] = name + inlineValue + joinDenials(def.DiscoveryArgs[1], value)
		case !inline && i+1 < len(merged) && safeValue(merged[i+1]):
			merged[i+1] = joinDenials(def.DiscoveryArgs[1], merged[i+1])
			i++
		default:
			return passthrough, merged[i]
		}
	}
	return merged, ""
}

// optionPrefix starts every option spelling; inlineValue joins an option to
// its value in the `--flag=value` spelling.
const (
	optionPrefix = "-"
	inlineValue  = "="
)

// joinDenials appends the operator's denylist to the restriction's.
func joinDenials(ours, theirs string) string {
	return ours + "," + theirs
}

// operatorSpells reports whether the operator's pass-through spells one of
// flags as an option. Only the `--` tail counts — the restriction and the
// grant verdict adds are never in it — and an argument right after a bare
// option is read as that option's value (`--append-system-prompt --tools`),
// never as a flag: the doubt keeps verdict's own restriction and grant.
func operatorSpells(passthrough, flags []string) bool {
	for i, arg := range passthrough {
		if !strings.HasPrefix(arg, optionPrefix) || (i > 0 && bareOption(passthrough[i-1])) {
			continue
		}
		if name, _, _ := strings.Cut(arg, inlineValue); slices.Contains(flags, name) {
			return true
		}
	}
	return false
}

// bareOption reports an option spelled without an inline value, which may
// take the next argument as its value.
func bareOption(arg string) bool {
	return len(arg) > 1 && strings.HasPrefix(arg, optionPrefix) && !strings.Contains(arg, inlineValue)
}

// reviewGrant is the row's scoped review-write grant for one launch whose
// artifacts land in dir: nil when the row documents none or the operator's
// `--` tail decides the agent's permissions or tool set itself. why says why
// a grant the row documents could not be spelled for dir; the launch then
// runs without it and the caller warns.
func reviewGrant(def *agentcatalog.Launch, passthrough []string, cwd, stateRoot, dir string) (grant []string, why string) {
	if len(def.ReviewWriteArgs) == 0 ||
		operatorSpells(passthrough, slices.Concat(def.PermissionFlags, def.DiscoveryOverrideFlags)) {
		return nil, ""
	}
	path, why := rulePath(cwd, stateRoot, dir)
	if why != "" {
		return nil, why
	}
	for _, arg := range def.ReviewWriteArgs {
		grant = append(grant, strings.ReplaceAll(arg, agentcatalog.ReviewDir, path))
	}
	return grant, ""
}

// rulePathSafe is every byte a rule path may carry: anything else (a glob
// metacharacter, a parenthesis, a comma or a space the flag splits on) would
// widen or break the rule, so such a directory gets no grant.
const rulePathSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./~@+="

// rulePath spells dir as a Claude Code permission-rule path from the agent's
// working directory cwd: `./<rel>` inside it, `//<abs>` outside it, with a
// Windows drive in the POSIX form rules match (C:\x is //c/x). Every path is
// resolved through its symlinks first, and the resolved dir must lie strictly
// under the resolved state root and must not hold cwd: a symlinked or
// ancestor directory would widen the grant past the bundle. why names the
// failure; it is "" on success.
func rulePath(cwd, stateRoot, dir string) (path, why string) {
	realCwd, realDir, why := grantDirs(cwd, stateRoot, dir)
	if why != "" {
		return "", why
	}
	if rel, err := filepath.Rel(realCwd, realDir); err == nil && strictlyWithin(realCwd, realDir) {
		path = "./" + filepath.ToSlash(rel)
	} else {
		path = absoluteRulePath(realDir)
	}
	switch {
	case path == "":
		return "", "its volume has no permission-rule spelling"
	case strings.Trim(path, rulePathSafe) != "":
		return "", "its path holds a character the permission-rule syntax reads as syntax"
	}
	return path, ""
}

// grantDirs resolves the agent's working directory and the grant directory
// through their symlinks, refusing a directory outside the resolved state
// root or one that holds the working directory. why is "" on success.
func grantDirs(cwd, stateRoot, dir string) (realCwd, realDir, why string) {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "", "its directory cannot be resolved"
	}
	realState, err := filepath.EvalSymlinks(stateRoot)
	if err != nil || !strictlyWithin(realState, realDir) {
		return "", "", "its directory resolves outside .verdict/state"
	}
	realCwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", "", "the agent's working directory cannot be resolved"
	}
	if realCwd == realDir || strictlyWithin(realDir, realCwd) {
		return "", "", "its directory holds the agent's working directory"
	}
	return realCwd, realDir, ""
}

// strictlyWithin reports whether child lies under parent and is not parent.
func strictlyWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// absoluteRulePath is an absolute directory in the rule's `//` form, "" for
// a filesystem root and for a volume the rule syntax has no spelling for (a
// UNC share).
func absoluteRulePath(abs string) string {
	vol := filepath.VolumeName(abs)
	rest := abs[len(vol):]
	switch {
	case rest == "" || rest == string(filepath.Separator) || rest == "/":
		return ""
	case vol == "":
		return "/" + filepath.ToSlash(abs)
	case len(vol) == 2 && vol[1] == ':':
		return "//" + strings.ToLower(vol[:1]) + filepath.ToSlash(rest)
	}
	return ""
}

// EditArgs is the edit grant the named agent's row documents: the shipped
// row's EditArgs, nil for a prefs-defined agent (its definition declares no
// grant) and for an unknown name. The self-review loop gives it to its fix
// spawn.
func EditArgs(name string, agents []config.PersonalAgent) []string {
	st := &state{defs: agents}
	def, ok := st.lookupAgent(name)
	if !ok {
		return nil
	}
	return slices.Clone(def.EditArgs)
}

// LoopArgs checks the self-review loop's `--` tail against its allowlist:
// only the named agent's documented model flag, each with one value, in the
// spaced or the `=` form. That tail reaches the review of untrusted content,
// so a denylist of permission spellings would always miss one; anything else
// is refused, an empty argument included. ok is false when any argument is
// outside the list, bad is the first such argument (possibly ""), and
// allowed names the flags that are.
func LoopArgs(name string, agents []config.PersonalAgent, args []string) (bad string, ok bool, allowed []string) {
	st := &state{defs: agents}
	def, _ := st.lookupAgent(name)
	if def.ModelFlag != "" {
		allowed = append([]string{def.ModelFlag}, def.ModelFlagAliases...)
	}
	for i := 0; i < len(args); i++ {
		flag, value, inline := strings.Cut(args[i], inlineValue)
		switch {
		case !slices.Contains(allowed, flag):
			return args[i], false, allowed
		case inline && safeValue(value):
		case !inline && i+1 < len(args) && safeValue(args[i+1]):
			i++
		default:
			return args[i], false, allowed
		}
	}
	return "", true, allowed
}

// safeValue reports a flag value that cannot read as another option.
func safeValue(v string) bool {
	return v != "" && !strings.HasPrefix(v, optionPrefix)
}

// knownAgents renders the known-name list for a refusal message.
func knownAgents() string {
	return strings.Join(AgentNames(), ", ")
}
