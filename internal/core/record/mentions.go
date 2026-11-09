package recordcore

import "strings"

// A review body is written by an agent and published verbatim: an `@handle`
// in prose notifies that person the moment the review is submitted. The
// neutralization therefore happens here, once, where the shared merge
// constructs the body into the record and the payload — never at submit
// time, which would make the posted bytes diverge from the recorded body,
// the dry-run output, and the payload digest.
//
// The escape is a U+200B ZERO WIDTH SPACE between `@` and the handle: it is
// invisible in rendered Markdown, still plain text in the recorded bytes, and
// hosts do not resolve a mention across it. A code span would change the
// rendering, so it is not the form.
const zeroWidthSpace = "\u200b"

// commentOpen and commentClose bound an HTML comment, which no rewrite may
// enter: finding markers are comments too.
const (
	commentOpen  = "<!--"
	commentClose = "-->"
)

// A fence may be indented by at most fenceIndentMax spaces and must open with
// a run of at least fenceRunMin markers, so a deeper indented line is never a
// fence.
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
// byte, so `(@alice)`, `**@alice**`, and `_@alice_` are mentions while
// `user@example.com` stays an address.
func NeutralizeMentions(body string) string {
	if !strings.Contains(body, "@") {
		return body
	}
	var scan mentionScan
	var out strings.Builder
	out.Grow(len(body))
	first := true
	for chunk := range strings.SplitSeq(body, newline) {
		if !first {
			out.WriteByte('\n')
		}
		first = false
		out.WriteString(scan.rewriteChunk(chunk))
	}
	return out.String()
}

// rewriteChunk rewrites one line's content and restores its terminator:
// SplitSeq drops the newlines, and a trailing CR belongs to the terminator so
// a CRLF body stays byte-identical outside the escape.
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

// mentionScan carries the block state one body's lines leave behind.
type mentionScan struct {
	fenceLen int  // the open fence's marking run, 0 outside a block
	code     int  // the open inline code span's backtick run, 0 outside
	fence    byte // 0, '`' or '~' while a fenced block is open
	comment  bool // inside an HTML comment
}

// line rewrites one line's prose mentions, updating the block state. Fence
// lines come back unchanged: the opening and closing fences of a code block
// belong to the block, not to its prose.
func (s *mentionScan) line(line string) string {
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
		// A blank line ends the paragraph, and a code span cannot cross one:
		// without this, one stray backtick would hide every mention after it.
		s.code = 0
		return line
	}
	return s.prose(line)
}

// prose walks one prose line and escapes the mentions it finds.
func (s *mentionScan) prose(line string) string {
	var out strings.Builder
	out.Grow(len(line))
	for i := 0; i < len(line); {
		switch {
		case s.comment:
			i = s.copyComment(&out, line, i)
		case s.code > 0:
			i = s.copyCodeSpan(&out, line, i)
		case strings.HasPrefix(line[i:], commentOpen):
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

// copyComment copies one byte of an open comment, or closes it.
func (s *mentionScan) copyComment(out *strings.Builder, line string, i int) int {
	if strings.HasPrefix(line[i:], commentClose) {
		s.comment = false
		out.WriteString(commentClose)
		return i + len(commentClose)
	}
	out.WriteByte(line[i])
	return i + 1
}

// copyCodeSpan copies one byte of an open code span, or closes it on an equal
// backtick run. A shorter or longer run is literal content, not a close.
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

// openCodeSpan opens a code span on the backtick run at i.
func (s *mentionScan) openCodeSpan(out *strings.Builder, line string, i int) int {
	s.code = runLen(line[i:], '`')
	out.WriteString(line[i : i+s.code])
	return i + s.code
}

// copyMention escapes the handle after the `@` at i, or copies the byte when
// no handle starts there.
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

// opensMention reports whether the `@` at i can open a mention. Whitespace and
// punctuation do, because `(@alice)`, `**@alice**`, and `_@alice_` all notify;
// a local-part byte does not, because `user@example.com` is an address and an
// escape there would corrupt it.
func opensMention(line string, i int) bool {
	j := i
	for j > 0 && (line[j-1] == '*' || line[j-1] == '_') {
		j--
	}
	if j < i {
		// Emphasis markup right before the `@` is markup, not the tail of an
		// address. A local part that genuinely ends in `_` or `*`
		// (`foo_@example.com`) keeps it because a local-part byte precedes the
		// run.
		return j == 0 || !isLocalPartByte(line[j-1])
	}
	return i == 0 || !isLocalPartByte(line[i-1])
}

// isLocalPartByte reports whether c may sit in the local part of an address
// immediately before its `@`. A superset of the handle charset, so every
// address-shaped run stays verbatim.
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

// handleLen counts the leading bytes of s that a host accepts in a handle.
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

// runLen counts the leading run of c in s.
func runLen(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

// leadingSpaces counts the indentation a fence may carry: never more than
// fenceIndentMax, so a deeper indented line is code of another kind.
func leadingSpaces(line string) int {
	i := 0
	for i < len(line) && i < fenceIndentMax && line[i] == ' ' {
		i++
	}
	return i
}

// fenceOpens reports an opening fence: up to three leading spaces, then at
// least three backticks or tildes. A backtick fence's info string may not
// carry a backtick, so “ ```@alice``` “ stays an inline code span.
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

// fenceCloses reports a closing fence for the open block: the same character,
// a run at least as long as the opener, and nothing but spaces or tabs after
// it. Fence lines themselves are returned unchanged, so the block's bytes stay
// intact.
func fenceCloses(line string, char byte, open int) bool {
	i := leadingSpaces(line)
	n := runLen(line[i:], char)
	if n < open {
		return false
	}
	return strings.TrimRight(line[i+n:], " \t") == ""
}
