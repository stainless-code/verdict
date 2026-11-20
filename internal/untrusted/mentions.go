package untrusted

import "strings"

// zeroWidthSpace breaks @mentions and HTML comment/link sequences without
// changing visible rendering or stripping bytes.
const zeroWidthSpace = "\u200b"

const (
	commentOpen  = "<!--"
	commentClose = "-->"
)

const (
	fenceIndentMax = 3
	fenceRunMin    = 3
)

// NeutralizeMentions escapes every `@handle` mention in one review body,
// leaving code and machine markers byte-identical. It is pure, and applying
// it twice returns the bytes it returned once.
//
// Skipped, because a `@handle` there is not a mention and rewriting it would
// silently corrupt the text:
//
//   - fenced code blocks (``` and ~~~, up to three leading spaces, closed by
//     a run of the same fence character at least as long);
//   - inline code spans (a backtick run closes on an equal run; the state
//     ends at a blank line, so one stray backtick cannot swallow the body);
//   - HTML comments, including the `<!-- verdict-finding … -->` markers.
//
// A mention opens at line start or after anything that is not a local-part
// byte, so `(@alice)`, `**@alice**`, `_@alice_`, and `~~@alice~~` are mentions
// while `user@example.com` and `~@example.com` stay addresses.
func NeutralizeMentions(body string) string {
	if !strings.Contains(body, "@") {
		return body
	}
	chunks := strings.Split(body, "\n")
	scan := mentionScan{lines: chunks, lastCommentClose: strings.LastIndex(body, commentClose)}
	var out strings.Builder
	out.Grow(len(body))
	for i, chunk := range chunks {
		if i > 0 {
			out.WriteByte('\n')
		}
		scan.lineIdx = i
		out.WriteString(scan.rewriteChunk(chunk))
		scan.lineOffset += len(chunk) + 1
	}
	return out.String()
}

func (s *mentionScan) rewriteChunk(text string) string {
	line, cr := cutCR(text)
	var out strings.Builder
	out.WriteString(s.line(line))
	out.WriteString(cr)
	return out.String()
}

func cutCR(text string) (line, cr string) {
	if strings.HasSuffix(text, "\r") {
		return text[:len(text)-1], "\r"
	}
	return text, ""
}

type mentionScan struct {
	lines            []string
	lastCommentClose int
	lineOffset       int
	lineIdx          int
	fenceLen         int
	code             int
	fence            byte
	comment          bool
}

func (s *mentionScan) line(line string) string {
	if s.comment {
		return s.prose(line)
	}
	if s.fence != 0 {
		if fenceCloses(line, s.fence, s.fenceLen) {
			s.fence, s.fenceLen = 0, 0
		}
		return line
	}
	if char, n, ok := fenceOpens(line); ok {
		s.fence, s.fenceLen = char, n
		return line
	}
	if strings.TrimSpace(line) == "" {
		// A blank line ends the paragraph so one stray backtick cannot hide
		// mentions in later paragraphs.
		s.code = 0
		return line
	}
	return s.prose(line)
}

func (s *mentionScan) prose(line string) string {
	var out strings.Builder
	out.Grow(len(line))
	for i := 0; i < len(line); {
		switch {
		case s.comment:
			i = s.copyComment(&out, line, i)
		case s.code > 0:
			i = s.copyCodeSpan(&out, line, i)
		case s.opensComment(line, i):
			s.comment = true
			out.WriteString(commentOpen)
			i += len(commentOpen)
		case line[i] == '`':
			i = s.openCodeSpan(&out, line, i)
		case line[i] == '@' && opensMention(line, i):
			i = copyMention(&out, line, i)
		default:
			out.WriteByte(line[i])
			i++
		}
	}
	return out.String()
}

func (s *mentionScan) opensComment(line string, i int) bool {
	return strings.HasPrefix(line[i:], commentOpen) &&
		!isEscaped(line, i) &&
		s.hasCommentClose(i+len(commentOpen))
}

func (s *mentionScan) hasCommentClose(from int) bool {
	return s.lastCommentClose >= s.lineOffset+from
}

func (s *mentionScan) copyComment(out *strings.Builder, line string, i int) int {
	if strings.HasPrefix(line[i:], commentClose) {
		s.comment = false
		out.WriteString(commentClose)
		return i + len(commentClose)
	}
	out.WriteByte(line[i])
	return i + 1
}

func (s *mentionScan) copyCodeSpan(out *strings.Builder, line string, i int) int {
	if line[i] != '`' {
		out.WriteByte(line[i])
		return i + 1
	}
	run := runLen(line[i:], '`')
	if run == s.code {
		s.code = 0
	}
	out.WriteString(line[i : i+run])
	return i + run
}

func (s *mentionScan) openCodeSpan(out *strings.Builder, line string, i int) int {
	if isEscaped(line, i) {
		out.WriteByte('`')
		return i + 1
	}
	run := runLen(line[i:], '`')
	if s.hasClosingTicks(line[i+run:], run) {
		s.code = run
	}
	out.WriteString(line[i : i+run])
	return i + run
}

func (s *mentionScan) hasClosingTicks(rest string, want int) bool {
	if lineHasTickRun(rest, want) {
		return true
	}
	for k := s.lineIdx + 1; k < len(s.lines); k++ {
		next, _ := cutCR(s.lines[k])
		if endsParagraph(next) {
			return false
		}
		if lineHasTickRun(next, want) {
			return true
		}
	}
	return false
}

func endsParagraph(line string) bool {
	if strings.TrimSpace(line) == "" {
		return true
	}
	_, _, ok := fenceOpens(line)
	return ok
}

func lineHasTickRun(s string, want int) bool {
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		run := runLen(s[i:], '`')
		if run == want {
			return true
		}
		i += run
	}
	return false
}

func isEscaped(s string, i int) bool {
	bs := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		bs++
	}
	return bs%2 == 1
}

func copyMention(out *strings.Builder, line string, i int) int {
	n := handleLen(line[i+1:])
	if n == 0 {
		out.WriteByte(line[i])
		return i + 1
	}
	out.WriteByte('@')
	out.WriteString(zeroWidthSpace)
	out.WriteString(line[i+1 : i+1+n])
	return i + 1 + n
}

func opensMention(line string, i int) bool {
	j := i
	for j > 0 && isEmphasisMarkup(line[j-1]) {
		j--
	}
	if i-j == 1 && line[i-1] == '~' {
		// A lone `~` is a local-part byte only when followed by a domain, so
		// `~@example.com` stays intact while `~@alice` escapes.
		return !domainShaped(line[i+1:])
	}
	if j < i {
		return j == 0 || !isLocalPartByte(line[j-1])
	}
	return i == 0 || !isLocalPartByte(line[i-1])
}

func isEmphasisMarkup(c byte) bool {
	return c == '*' || c == '_' || c == '~'
}

func domainShaped(s string) bool {
	n := handleLen(s)
	if n == 0 || n+1 >= len(s) || s[n] != '.' {
		return false
	}
	return isHandleByte(s[n+1])
}

func isLocalPartByte(c byte) bool {
	if isHandleByte(c) {
		return true
	}
	switch c {
	case '.', '+', '%', '_', '=', '&', '\'', '!', '#', '$', '/', '~':
		return true
	}
	return false
}

func handleLen(s string) int {
	n := 0
	for n < len(s) && isHandleByte(s[n]) {
		n++
	}
	return n
}

func isHandleByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
}

func runLen(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

func leadingSpaces(line string) int {
	i := 0
	for i < len(line) && i < fenceIndentMax && line[i] == ' ' {
		i++
	}
	return i
}

func fenceOpens(line string) (char byte, n int, ok bool) {
	i := leadingSpaces(line)
	if i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return 0, 0, false
	}
	char = line[i]
	n = runLen(line[i:], char)
	if n < fenceRunMin {
		return 0, 0, false
	}
	if char == '`' && strings.IndexByte(line[i+n:], '`') >= 0 {
		return 0, 0, false
	}
	return char, n, true
}

func fenceCloses(line string, char byte, open int) bool {
	i := leadingSpaces(line)
	n := runLen(line[i:], char)
	if n < open {
		return false
	}
	return strings.TrimRight(line[i+n:], " \t") == ""
}
