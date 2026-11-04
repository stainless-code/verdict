package hunks

import (
	"strconv"
	"strings"
)

// gitQuote is the byte that opens a C-quoted git path.
const gitQuote = "\""

// HeaderPaths parses one `diff --git` header into its old and new paths.
// Git C-quotes a path that carries a quote, backslash, control byte, or (with
// core.quotePath=true) a non-ASCII byte: `diff --git "a/h\303\251llo" "b/…"`.
// Quoted tokens decode in pairs; the unquoted form has no reliable delimiter
// other than the last " b/" (paths may contain spaces), which is what the
// awk port used and what git's own output makes unambiguous from the right.
func HeaderPaths(line string) (oldPath, newPath string) {
	rest, ok := strings.CutPrefix(line, "diff --git ")
	if !ok {
		return "", ""
	}
	if token, after, quoted := cutGitPath(rest); quoted {
		oldPath = stripGitPrefix(unquoteGitPath(token), "a/")
		after = strings.TrimLeft(after, " \t")
		if strings.HasPrefix(after, gitQuote) {
			token, _, _ := cutGitPath(after)
			return oldPath, stripGitPrefix(unquoteGitPath(token), "b/")
		}
		return oldPath, stripGitPrefix(strings.TrimSpace(after), "b/")
	}
	if before, after, ok := strings.CutLast(rest, " b/"); ok {
		return stripGitPrefix(before, "a/"), after
	}
	return stripGitPrefix(rest, "a/"), ""
}

// cutGitPath reads one header token: a C-quoted literal keeps its quotes in
// token and reports quoted=true, while an unquoted token ends at the first
// space or tab. An empty input reports quoted=false.
func cutGitPath(s string) (token, rest string, quoted bool) {
	if s == "" {
		return "", "", false
	}
	if !strings.HasPrefix(s, gitQuote) {
		if idx := strings.IndexAny(s, " \t"); idx >= 0 {
			return s[:idx], s[idx+1:], false
		}
		return s, "", false
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return s[:i+1], s[i+1:], true
		}
	}
	return s, "", true
}

// unquoteGitPath decodes git's C-quoted path form. Invalid escapes keep the
// literal bytes inside the quotes rather than dropping the path.
func unquoteGitPath(token string) string {
	if len(token) < 2 || token[0] != '"' || token[len(token)-1] != '"' {
		return token
	}
	if unquoted, err := strconv.Unquote(token); err == nil {
		return unquoted
	}
	return token[1 : len(token)-1]
}

// UnquotePath decodes a standalone git path (for example a `rename to` line).
func UnquotePath(s string) string { return unquoteGitPath(s) }

// stripGitPrefix removes the side marker from a header path.
func stripGitPrefix(path, prefix string) string {
	if after, ok := strings.CutPrefix(path, prefix); ok {
		return after
	}
	return path
}
