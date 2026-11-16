package verifycore

import (
	"regexp"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

const codeIndirection = "verify-indirection"

const msgIndirection = "a command word built from a variable hides the tool — write the tool " +
	"word plainly, or run the step under verify --sandbox"

var (
	cmdKeywords = map[string]bool{
		"!": true, "(": true, "{": true, "do": true, "then": true, "else": true,
		"elif": true, "if": true, "while": true, "until": true,
	}
	cmdWrappers = map[string]bool{
		"builtin": true, "command": true, "env": true, "exec": true, "nice": true,
		"nohup": true, "stdbuf": true, "time": true, "timeout": true, "xargs": true,
	}
	cmdShells = map[string]bool{
		"bash": true, "dash": true, "fish": true, "ksh": true, "sh": true, "zsh": true,
	}
)

func checkIndirection(g *gateText) *core.RefusalError {
	return indirectionIn(g, 0)
}

func indirectionIn(g *gateText, depth int) *core.RefusalError {
	if word := indirectText(g); word != "" {
		return refuse(codeIndirection, word, msgIndirection)
	}
	if depth == maxSentinelPasses {
		return nil
	}
	for _, body := range substitutionBodies(g.raw) {
		if ref := indirectionIn(normalize(body), depth+1); ref != nil {
			return ref
		}
	}
	return nil
}

func indirectText(g *gateText) string {
	word := indirectWord(g.seg)
	if word == "" {
		return ""
	}
	if exact := indirectWord(g.segExact); exact != "" {
		return exact
	}
	return word
}

func indirectWord(text string) string {
	for _, seg := range segments(text) {
		if word := segmentIndirection(seg); word != "" {
			return word
		}
	}
	return ""
}

func commandIndex(words []string) int {
	i := 0
	for i < len(words) {
		if next, ok := skipRedirect(words, i); ok {
			i = next
			continue
		}
		if !reAssignPrefix.MatchString(words[i]) && !cmdKeywords[words[i]] {
			return i
		}
		i++
	}
	return i
}

func segmentIndirection(seg string) string {
	words := strings.Fields(seg)
	i := commandIndex(words)
	if i >= len(words) {
		return ""
	}
	rest := words[i+1:]
	word := strings.Trim(words[i], "()")
	// A wrapper's own option grammar decides which later word bash runs, so
	// after one every expansion refuses rather than being guessed apart.
	if cmdWrappers[word] || cmdShells[word] && hasInlineCommand(rest) {
		return firstIndirect(rest)
	}
	if isIndirectWord(words[i]) {
		return words[i]
	}
	return ""
}

func firstIndirect(words []string) string {
	for _, word := range words {
		if isIndirectWord(word) {
			return word
		}
	}
	return ""
}

func hasInlineCommand(words []string) bool {
	return slices.ContainsFunc(words, reInlineCommand.MatchString)
}

func isIndirectWord(word string) bool {
	return rePureExpansion.MatchString(strings.Trim(word, "()"))
}

func skipRedirect(words []string, i int) (int, bool) {
	op := reRedirect.FindString(words[i])
	if op == "" {
		return i, false
	}
	if len(op) < len(words[i]) {
		return i + 1, true
	}
	return i + 2, true
}

func substitutionBodies(raw string) []string {
	var out []string
	for i := 0; i < len(raw); i++ {
		body, end, ok := substitutionAt(raw, i)
		if !ok {
			continue
		}
		out = append(out, body)
		i = end
	}
	return out
}

func substitutionAt(raw string, i int) (string, int, bool) {
	switch raw[i] {
	case '$':
		return dollarBody(raw, i)
	case '<', '>':
		return processBody(raw, i)
	case '`':
		return backtickBody(raw, i)
	}
	return "", 0, false
}

func dollarBody(raw string, i int) (string, int, bool) {
	if !parenAt(raw, i+1) || parenAt(raw, i+2) {
		return "", 0, false
	}
	return parenBody(raw, i+2)
}

func processBody(raw string, i int) (string, int, bool) {
	if !parenAt(raw, i+1) {
		return "", 0, false
	}
	return parenBody(raw, i+2)
}

func backtickBody(raw string, i int) (string, int, bool) {
	end := strings.IndexByte(raw[i+1:], '`')
	if end < 0 {
		return "", 0, false
	}
	return raw[i+1 : i+1+end], i + end + 1, true
}

func parenAt(raw string, i int) bool {
	return i < len(raw) && raw[i] == '('
}

func parenBody(raw string, start int) (string, int, bool) {
	depth := 1
	for i := start; i < len(raw); i++ {
		switch raw[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return raw[start:i], i, true
			}
		}
	}
	return "", 0, false
}

var (
	reAssignPrefix  = regexp.MustCompile(`^[A-Za-z0-9_]+=`)
	rePureExpansion = regexp.MustCompile(`^(\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*)+$`)
	reRedirect      = regexp.MustCompile(`^[0-9]*(<<<|<<|<>|>>|>\||>&|<&|&>>|&>|<|>)`)
	reInlineCommand = regexp.MustCompile(`^-[a-z]*c$`)
)
