package exclude

import "strings"

// LiteralPath renders one repo path as the pattern that matches that path and
// nothing else: Match reads \ * ? and [ ] as syntax, so each is escaped, and a
// leading ! would otherwise turn the rule into a negation.
func LiteralPath(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	for i := range len(path) {
		switch c := path[i]; {
		case c == '\\', c == '*', c == '?', c == '[', c == ']':
			b.WriteByte('\\')
		case c == '!' && i == 0:
			b.WriteByte('\\')
		}
		b.WriteByte(path[i])
	}
	return b.String()
}
