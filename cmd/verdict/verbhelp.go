package main

import "strings"

// ownHelpVerbs keep their own `--help` (a grammar the shared block cannot
// carry); every other verb answers `<verb> --help` with just its block.
var ownHelpVerbs = map[string]bool{verbSkill: true, "mcp": true}

// verbUsage returns one verb's usage block: its header line(s) through the
// indented continuation lines, "" when the verb has no entry.
func verbUsage(verb string) string {
	var out strings.Builder
	in := false
	for line := range strings.SplitSeq(usageText(), "\n") {
		switch {
		case line == "  "+verb || strings.HasPrefix(line, "  "+verb+" "):
			in = true
		case !strings.HasPrefix(line, "   "):
			in = false // the next header, the global sections, or the end
		}
		if in {
			out.WriteString(line + "\n")
		}
	}
	return out.String()
}

// verbHelp reports a `<verb> -h|--help` invocation (the flag before any
// `--`) and the block to print for it.
func verbHelp(args []string) (string, bool) {
	if len(args) < 2 || ownHelpVerbs[args[0]] {
		return "", false
	}
	block := verbUsage(args[0])
	if block == "" {
		return "", false
	}
	for _, arg := range args[1:] {
		switch arg {
		case "--":
			return "", false
		case flagShortHelp, flagHelp:
			return block, true
		}
	}
	return "", false
}
