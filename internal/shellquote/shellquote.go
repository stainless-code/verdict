// Package shellquote renders one string as a POSIX shell word, so a command
// verdict prints can be pasted back into sh, bash, zsh, or Git Bash.
package shellquote

import "strings"

// safeRunes is the set a POSIX shell reads back verbatim; every other rune
// gets single quotes.
const safeRunes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:@%+=,-"

// Arg renders s as one shell word: bare when every rune is safe, else POSIX
// single-quoted, closing the quote around an embedded single quote.
func Arg(s string) string {
	if strings.Trim(s, safeRunes) == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
