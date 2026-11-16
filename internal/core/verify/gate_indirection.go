package verifycore

import (
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

const codeIndirection = "verify-indirection"

const msgIndirection = "a command word built from a variable hides the tool — write the tool " +
	"word plainly, or run the step under verify --sandbox"

// indirectWrappers are the words a command word may follow without being the
// invoked command: the shell keywords that open a command list, and the
// wrappers that run the word after their flags.
var indirectWrappers = map[string]bool{
	"!": true, "(": true, "{": true, "command": true, "do": true, "elif": true,
	"else": true, "env": true, "exec": true, "if": true, "nice": true,
	"nohup": true, "stdbuf": true, "then": true, "time": true, "timeout": true,
	"until": true, "while": true, "xargs": true,
}

// checkIndirection refuses a command word the gate cannot read: with no tool
// word in the text, every class below is blind to it, and resolving the
// variable statically is unsound (the value can come from the environment or
// a later assignment). Refusing the shape is the whole answer.
func checkIndirection(g *gateText) *core.RefusalError {
	word := indirectWord(g.bare)
	if word == "" {
		return nil
	}
	if exact := indirectWord(g.csbare); exact != "" {
		word = exact
	}
	return refuse(codeIndirection, word, msgIndirection)
}

func indirectWord(text string) string {
	for _, seg := range commandSegments(text) {
		if word := commandWord(seg); isIndirectWord(word) {
			return word
		}
	}
	return ""
}

func commandSegments(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return r == ';' || r == '|' || r == '&'
	})
}

func commandWord(seg string) string {
	for tok := range strings.FieldsSeq(seg) {
		if isCommandPrefix(tok) {
			continue
		}
		return tok
	}
	return ""
}

func isCommandPrefix(tok string) bool {
	return indirectWrappers[tok] || reAssignPrefix.MatchString(tok) ||
		strings.HasPrefix(tok, "-") || reDigits.MatchString(tok)
}

func isIndirectWord(word string) bool {
	return rePureExpansion.MatchString(strings.Trim(word, "()"))
}

var (
	reAssignPrefix  = regexp.MustCompile(`^[A-Za-z0-9_]+=`)
	reDigits        = regexp.MustCompile(`^[0-9]+$`)
	rePureExpansion = regexp.MustCompile(`^(\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*)+$`)
)
