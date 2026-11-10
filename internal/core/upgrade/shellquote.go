package upgrade

import "strings"

// shellSafeRunes is the set a POSIX shell (and Git Bash, the shell these hints
// target on Windows) reads back verbatim; every other rune gets single quotes.
const shellSafeRunes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:@%+=,-"

func shellQuote(s string) string {
	if !strings.ContainsFunc(s, shellUnsafe) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellUnsafe(r rune) bool {
	return !strings.ContainsRune(shellSafeRunes, r)
}
