package verifycore

import (
	"regexp"
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
		"command": true, "env": true, "exec": true, "nice": true, "nohup": true,
		"stdbuf": true, "time": true, "timeout": true, "xargs": true,
	}
)

func checkIndirection(g *gateText) *core.RefusalError {
	word := indirectWord(g.seg)
	if word == "" {
		return nil
	}
	if exact := indirectWord(g.segExact); exact != "" {
		word = exact
	}
	return refuse(codeIndirection, word, msgIndirection)
}

func indirectWord(text string) string {
	for _, seg := range segments(text) {
		if word := segmentIndirection(seg); word != "" {
			return word
		}
	}
	return ""
}

func segmentIndirection(seg string) string {
	words := strings.Fields(seg)
	i := 0
	for i < len(words) && (reAssignPrefix.MatchString(words[i]) || cmdKeywords[words[i]]) {
		i++
	}
	if i == len(words) {
		return ""
	}
	// A wrapper's own option grammar decides which later word bash runs, so
	// after one every expansion refuses rather than being guessed apart.
	if cmdWrappers[strings.Trim(words[i], "()")] {
		return firstIndirect(words[i+1:])
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

func isIndirectWord(word string) bool {
	return rePureExpansion.MatchString(strings.Trim(word, "()"))
}

var (
	reAssignPrefix  = regexp.MustCompile(`^[A-Za-z0-9_]+=`)
	rePureExpansion = regexp.MustCompile(`^(\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*)+$`)
)
