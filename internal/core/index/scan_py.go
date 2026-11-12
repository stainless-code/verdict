package indexcore

import "strings"

// Token kinds a scanned Python expression can carry.
const (
	pyIdent = iota
	pyPunct
	pyValue
)

// The nesting budget of one file: an f-string inside an f-string.
const pyMaxDepth = 32

// pyTripleLen is the width of a triple quote.
const pyTripleLen = 3

// pyAliasWidth is the token width of one `name as alias`.
const pyAliasWidth = 3

// pyToken is one lexical token of a Python source file.
type pyToken struct {
	text    string
	kind    int
	line    int
	col     int
	logical bool
}

// pyReserved are the words that can never name a callee or a recorded
// declaration.
var pyReserved = map[string]bool{
	"and": true, "as": true, "assert": true, wordAsync: true, "await": true,
	"break": true, wordClass: true, "continue": true, wordDef: true, "del": true,
	"elif": true, wordElse: true, "except": true, "False": true, "finally": true,
	wordFor: true, wordFrom: true, "global": true, "if": true, wordImport: true,
	"in": true, "is": true, "lambda": true, "None": true, "nonlocal": true,
	"not": true, "or": true, "pass": true, "raise": true, wordReturn: true,
	"True": true, "try": true, wordWhile: true, wordWith: true, "yield": true,
}

// pyStringPrefixes are the case-folded prefixes a string literal can carry.
var pyStringPrefixes = map[string]bool{
	"r": true, "b": true, "u": true, "f": true,
	"rb": true, "br": true, "fr": true, "rf": true,
}

// pyPuncts are the punctuators the scanner joins, longest form first.
var pyPuncts = []string{
	"**=", "//=", ">>=", "<<=", "...", ":=", "->", "**", "//", "==", "!=", "<=",
	">=", "<<", ">>", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "@=",
}

// pyLexer tokenizes one Python source file.
type pyLexer struct {
	src       []byte
	tokens    []pyToken
	pos       int
	line      int
	lineStart int
	brackets  int
	fresh     bool
	depth     int
}

// scanPython extracts one Python file's declarations, imports, and call sites.
func scanPython(src []byte, buf *fileBuf) {
	l := &pyLexer{src: src, line: 1, fresh: true}
	for l.pos < len(src) {
		l.lex()
	}
	pyExtract(l.tokens, buf)
}

// lex reads the next token, or skips trivia.
func (l *pyLexer) lex() {
	l.skipSpace()
	if l.pos >= len(l.src) {
		return
	}
	line, col := l.line, l.pos-l.lineStart+1
	switch c := l.src[l.pos]; {
	case c == '#':
		l.skipComment()
	case c == '\'' || c == '"':
		l.lexString("", c, line, col)
	case isDigit(c) || (c == '.' && l.pos+1 < len(l.src) && isDigit(l.src[l.pos+1])):
		l.lexNumber(line, col)
	case isIdentStart(c):
		l.lexWord(line, col)
	default:
		l.lexPunct(line, col)
	}
}

// skipSpace consumes whitespace, comments, and line continuations, tracking
// where a logical line begins.
func (l *pyLexer) skipSpace() {
	for l.pos < len(l.src) {
		switch l.src[l.pos] {
		case ' ', '\t', '\r', '\f', '\v':
			l.advance(1)
		case '\n':
			if l.brackets == 0 && !l.continued() {
				l.fresh = true
			}
			l.advance(1)
		case '\\':
			l.advance(1)
		case '#':
			l.skipComment()
		default:
			return
		}
	}
}

// continued reports whether the byte before the cursor is a line
// continuation, which joins the next line to this statement.
func (l *pyLexer) continued() bool { return l.pos > 0 && l.src[l.pos-1] == '\\' }

// skipComment consumes one `#` comment.
func (l *pyLexer) skipComment() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.advance(1)
	}
}

// advance moves the cursor forward and keeps the line accounting current.
func (l *pyLexer) advance(n int) {
	end := min(l.pos+n, len(l.src))
	for i := l.pos; i < end; i++ {
		if l.src[i] == '\n' {
			l.line++
			l.lineStart = i + 1
		}
	}
	l.pos = end
}

// emit appends one token, carrying whether it starts a logical line.
func (l *pyLexer) emit(kind int, text string, line, col int) {
	l.tokens = append(l.tokens, pyToken{text: text, kind: kind, line: line, col: col, logical: l.fresh})
	l.fresh = false
}

// bracket records one bracket the stream opened or closed.
func (l *pyLexer) bracket(text string) {
	switch text {
	case punctParen, "[", punctBrace:
		l.brackets++
	case punctParenEnd, "]", punctBraceEnd:
		if l.brackets > 0 {
			l.brackets--
		}
	}
}

// lexWord consumes one identifier, or one string literal when the identifier
// is a string prefix.
func (l *pyLexer) lexWord(line, col int) {
	start := l.pos
	for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
		l.advance(1)
	}
	word := string(l.src[start:l.pos])
	if l.pos < len(l.src) && (l.src[l.pos] == '\'' || l.src[l.pos] == '"') && pyStringPrefixes[strings.ToLower(word)] {
		l.lexString(word, l.src[l.pos], line, col)
		return
	}
	l.emit(pyIdent, word, line, col)
}

// lexString consumes one string literal, splitting an f-string's `{ … }`
// interpolations out as code.
func (l *pyLexer) lexString(prefix string, quote byte, line, col int) {
	raw := strings.ContainsAny(prefix, "rR")
	formatted := strings.ContainsAny(prefix, "fF")
	triple := l.triple(quote)
	if triple {
		l.advance(pyTripleLen)
	} else {
		l.advance(1)
	}
	for l.pos < len(l.src) {
		if l.stringStep(raw, formatted, triple, quote, line, col) {
			return
		}
	}
	l.emit(pyValue, "", line, col)
}

// stringStep consumes one piece of a string literal, reporting whether the
// literal ended there.
func (l *pyLexer) stringStep(raw, formatted, triple bool, quote byte, line, col int) bool {
	c := l.src[l.pos]
	if l.braceStep(formatted, line, col) {
		return false
	}
	switch {
	case c == '\\' && !raw:
		l.advance(2)
		return false
	case triple && l.triple(quote):
		l.advance(pyTripleLen)
	case !triple && c == quote:
		l.advance(1)
	case !triple && c == '\n':
	default:
		l.advance(1)
		return false
	}
	l.emit(pyValue, "", line, col)
	return true
}

// braceStep consumes one f-string brace: a doubled pair renders as one literal
// brace, and a single `{` opens an interpolation. It reports whether the cursor
// moved.
func (l *pyLexer) braceStep(formatted bool, line, col int) bool {
	if !formatted {
		return false
	}
	if l.doubledBrace() {
		l.advance(2)
		return true
	}
	if l.src[l.pos] != '{' {
		return false
	}
	l.advance(1)
	l.emit(pyValue, "", line, col)
	l.interpolation()
	return true
}

// doubledBrace reports whether the cursor sits on the `{{` or `}}` escape an
// f-string renders as one literal brace.
func (l *pyLexer) doubledBrace() bool {
	c := l.src[l.pos]
	return (c == '{' || c == '}') && l.next(1) == c
}

// triple reports whether three copies of one quote start at the cursor.
func (l *pyLexer) triple(quote byte) bool {
	return l.next(0) == quote && l.next(1) == quote && l.next(2) == quote
}

// next returns the byte one offset ahead, 0 past the end.
func (l *pyLexer) next(n int) byte {
	if l.pos+n >= len(l.src) {
		return 0
	}
	return l.src[l.pos+n]
}

// interpolation tokenizes one f-string `{ … }` body as code, up to the brace
// that closes it.
func (l *pyLexer) interpolation() {
	if l.depth >= pyMaxDepth {
		return
	}
	l.depth++
	brackets, fresh := l.brackets, l.fresh
	l.brackets = 0
	for l.pos < len(l.src) {
		l.skipSpace()
		if l.pos >= len(l.src) {
			break
		}
		if l.brackets == 0 && l.src[l.pos] == '}' {
			l.advance(1)
			break
		}
		l.lex()
	}
	l.depth--
	l.brackets, l.fresh = brackets, fresh
}

// lexNumber consumes one numeric literal.
func (l *pyLexer) lexNumber(line, col int) {
	for l.pos < len(l.src) && (isIdentPart(l.src[l.pos]) || l.src[l.pos] == '.') {
		l.advance(1)
	}
	l.emit(pyValue, "", line, col)
}

// lexPunct consumes one punctuator.
func (l *pyLexer) lexPunct(line, col int) {
	text := l.punctText()
	l.advance(len(text))
	l.emit(pyPunct, text, line, col)
	l.bracket(text)
	if text == punctSemi {
		l.fresh = true
	}
}

// punctText returns the punctuator at the cursor, longest form first.
func (l *pyLexer) punctText() string {
	rest := l.src[l.pos:]
	for _, form := range pyPuncts {
		if len(form) <= len(rest) && string(rest[:len(form)]) == form {
			return form
		}
	}
	return string(rest[:1])
}

// pyExtract records the declarations, imports, and call sites of one token
// stream.
func pyExtract(tokens []pyToken, buf *fileBuf) {
	x := &pyExtractor{tokens: tokens, buf: buf, skip: map[int]bool{}}
	x.declarations()
	x.imports()
	x.calls()
}

// pyExtractor walks one token stream.
type pyExtractor struct {
	skip   map[int]bool
	buf    *fileBuf
	tokens []pyToken
	scopes []pyScope
}

// pyScope is one class or function body a later line can still sit in.
type pyScope struct {
	name   string
	indent int
	class  bool
}

// declarations records every `def`, `async def`, and `class` at any indent and
// qualifies a method by the class that encloses it.
func (x *pyExtractor) declarations() {
	for i, t := range x.tokens {
		if t.kind != pyIdent || !t.logical {
			continue
		}
		x.closeScopes(t.col)
		if kind, name, ok := x.declaration(i); ok {
			x.pushScope(name, t.col, kind == wordType)
		}
	}
}

// closeScopes drops every scope a line at this indent has left.
func (x *pyExtractor) closeScopes(indent int) {
	for len(x.scopes) > 0 && x.scopes[len(x.scopes)-1].indent >= indent {
		x.scopes = x.scopes[:len(x.scopes)-1]
	}
}

// pushScope records one class or function body.
func (x *pyExtractor) pushScope(name string, indent int, class bool) {
	x.scopes = append(x.scopes, pyScope{name: name, indent: indent, class: class})
}

// receiver returns the class a method is declared in, or "" for a function.
func (x *pyExtractor) receiver() string {
	if n := len(x.scopes); n > 0 && x.scopes[n-1].class {
		return x.scopes[n-1].name
	}
	return ""
}

// declaration records the declaration one logical line starts with.
func (x *pyExtractor) declaration(i int) (string, string, bool) {
	keyword, j := x.declWords(i)
	if keyword == "" {
		return "", "", false
	}
	name := x.tokens[j].text
	x.skip[j] = true
	kind, recv := wordFunc, ""
	if keyword == wordClass {
		kind = wordType
	} else if recv = x.receiver(); recv != "" {
		kind = wordMethod
	}
	x.buf.addDecl(name, kind, recv, x.tokens[j].line, !strings.HasPrefix(name, "_"))
	return kind, name, true
}

// declWords reads the keyword of one logical line and the index of its declared
// name, or "" when the line declares nothing.
func (x *pyExtractor) declWords(i int) (string, int) {
	keyword, j := x.tokens[i].text, i+1
	if keyword == wordAsync && x.text(j) == wordDef {
		keyword, j = wordDef, j+1
	}
	if keyword != wordDef && keyword != wordClass {
		return "", 0
	}
	if x.kind(j) != pyIdent || pyReserved[x.text(j)] {
		return "", 0
	}
	return keyword, j
}

// imports records every `import` and `from … import` statement.
func (x *pyExtractor) imports() {
	for i, t := range x.tokens {
		if t.kind != pyIdent || !t.logical {
			continue
		}
		switch t.text {
		case wordImport:
			x.plainImport(i + 1)
		case wordFrom:
			x.fromImport(i + 1)
		default:
		}
	}
}

// plainImport records one `import a.b as c` statement and its bindings.
func (x *pyExtractor) plainImport(i int) {
	for i < len(x.tokens) {
		module, name, next, ok := x.importBinding(i)
		if !ok {
			return
		}
		x.buf.addImport(module, name)
		if x.text(next) != "," {
			return
		}
		i = next + 1
	}
}

// importBinding reads one `a.b as c` clause item.
func (x *pyExtractor) importBinding(i int) (string, string, int, bool) {
	if x.kind(i) != pyIdent || pyReserved[x.text(i)] || x.tokens[i].logical {
		return "", "", i, false
	}
	module, next := x.dottedName(i)
	if module == "" {
		return "", "", i, false
	}
	name, _, _ := strings.Cut(module, punctDot)
	if x.text(next) == wordAs && x.kind(next+1) == pyIdent {
		return module, x.text(next + 1), next + 2, true
	}
	return module, name, next, true
}

// fromImport records one `from … import …` statement and its bindings.
func (x *pyExtractor) fromImport(i int) {
	module, next := x.relativeName(i)
	if module == "" || next >= len(x.tokens) || x.tokens[next].text != wordImport {
		return
	}
	names, _ := x.importedNames(next + 1)
	if len(names) == 0 {
		x.buf.addImport(module, "")
		return
	}
	for _, name := range names {
		x.buf.addImport(module, name)
	}
}

// dottedName reads one dotted module name and returns the index after it.
func (x *pyExtractor) dottedName(i int) (string, int) {
	var parts []string
	for i < len(x.tokens) {
		if x.tokens[i].kind != pyIdent || pyReserved[x.tokens[i].text] {
			break
		}
		parts = append(parts, x.tokens[i].text)
		i++
		if i >= len(x.tokens) || x.tokens[i].text != punctDot {
			break
		}
		i++
	}
	return strings.Join(parts, punctDot), i
}

// relativeName reads one `from` module: leading dots then a dotted name.
func (x *pyExtractor) relativeName(i int) (string, int) {
	dots := 0
	for i < len(x.tokens) && x.tokens[i].text == punctDot {
		dots++
		i++
	}
	rest, next := x.dottedName(i)
	if rest == "" && dots == 0 {
		return "", i
	}
	module := strings.Repeat(punctDot, dots) + rest
	if module == "" {
		module = punctDot
	}
	return module, next
}

// importedNames reads the names of one `import` clause, following a
// parenthesized one across lines.
func (x *pyExtractor) importedNames(i int) ([]string, int) {
	if x.text(i) == punctParen {
		if end := x.pair(i); end > 0 {
			return x.nameList(i+1, end), end
		}
	}
	return x.nameList(i, len(x.tokens)), i
}

// nameList reads a comma-separated name list, stopping at a new statement.
func (x *pyExtractor) nameList(i, end int) []string {
	var names []string
	for i < end {
		switch {
		case x.tokens[i].logical && len(names) > 0:
			return names
		case x.listSeparator(i):
			i++
		case x.kind(i) != pyIdent && x.text(i) != "*":
			return names
		default:
			names, i = x.nameStep(names, i, end)
		}
	}
	return names
}

// listSeparator reports whether one token separates two imported names.
func (x *pyExtractor) listSeparator(i int) bool {
	return x.text(i) == "," || x.text(i) == punctParen
}

// nameStep appends one imported name, taking its `as` alias when it has one.
func (x *pyExtractor) nameStep(names []string, i, end int) ([]string, int) {
	if i+2 < end && x.text(i+1) == wordAs {
		return append(names, x.text(i+2)), i + pyAliasWidth
	}
	return append(names, x.text(i)), i + 1
}

// pair returns the index closing the bracket at open, or -1.
func (x *pyExtractor) pair(open int) int {
	depth := 0
	for i := open; i < len(x.tokens); i++ {
		step := 0
		if x.kind(i) == pyPunct {
			step = bracketStep(x.text(i))
		}
		if step == 0 {
			continue
		}
		depth += step
		if depth == 0 {
			return i
		}
	}
	return -1
}

// calls records every call and attribute site.
func (x *pyExtractor) calls() {
	for i := range x.tokens {
		if x.callCallee(i) {
			t := x.tokens[i]
			x.buf.addCall(t.text, x.callKind(i), t.line, t.col)
		}
	}
}

// callCallee reports whether one token is a callee worth recording.
func (x *pyExtractor) callCallee(i int) bool {
	return x.kind(i) == pyIdent && !pyReserved[x.text(i)] && !x.skip[i] && x.text(i+1) == punctParen
}

// callKind classifies one callee as an attribute site or a call.
func (x *pyExtractor) callKind(i int) string {
	if x.text(i-1) == punctDot {
		return KindMethod
	}
	return KindCall
}

// text returns one token's text, "" outside the stream.
func (x *pyExtractor) text(i int) string {
	if i < 0 || i >= len(x.tokens) {
		return ""
	}
	return x.tokens[i].text
}

// kind returns one token's kind, -1 outside the stream.
func (x *pyExtractor) kind(i int) int {
	if i < 0 || i >= len(x.tokens) {
		return -1
	}
	return x.tokens[i].kind
}
