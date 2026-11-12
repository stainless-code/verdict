package indexcore

import (
	"slices"
	"strings"
)

// Token kinds a scanned expression can carry. The kind decides only whether a
// following `/` continues a value or starts a regex literal.
const (
	tsIdent = iota
	tsPunct
	tsValue
)

// The nesting and backtrack budget of one file: templates inside templates,
// JSX scans, and the rewinds an abandoned guess costs.
const (
	tsMaxDepth   = 32
	tsMaxRewinds = 64
	tsMaxHeader  = 400
)

// tsNonASCII is the first byte an identifier may hold outside ASCII.
const tsNonASCII = 0x80

// tsNewSelector is the token distance from `new` to the name of a selector
// constructor, as in `new a.b()`.
const tsNewSelector = 3

// tsDecoratorHead is the token distance from a decorator's `(` back to its `@`,
// as in `@Get(…)`: the `@`, the name, and then the paren.
const tsDecoratorHead = 3

// tsToken is one lexical token of a TypeScript or JavaScript source file.
type tsToken struct {
	text string
	kind int
	line int
	col  int
}

// tsReserved are the words that can never name a callee or a recorded
// declaration.
var tsReserved = map[string]bool{
	"break": true, "case": true, "catch": true, wordClass: true, wordConst: true,
	"continue": true, "debugger": true, "default": true, wordDelete: true,
	"do": true, wordElse: true, wordEnum: true, wordExport: true, "extends": true,
	"false": true, "finally": true, wordFor: true, wordFunction: true, "if": true,
	wordImport: true, "in": true, "instanceof": true, "let": true, wordNew: true,
	"null": true, wordReturn: true, "super": true, "switch": true, "this": true,
	"throw": true, "try": true, wordTypeof: true, wordVar: true,
	"void": true, wordWhile: true, wordWith: true,
}

// tsRegexAfter are the words a regex literal may follow.
var tsRegexAfter = map[string]bool{
	wordReturn: true, wordTypeof: true, "instanceof": true, "in": true, "of": true,
	wordNew: true, wordDelete: true, "void": true, "case": true, "do": true,
	wordElse: true, "yield": true, "await": true, "throw": true,
}

// tsControlParens are the keywords whose closing paren can be followed by a
// regex literal rather than by a division.
var tsControlParens = map[string]bool{
	"if": true, wordFor: true, wordWhile: true, "switch": true, "catch": true, wordWith: true,
}

// tsContextual are the words that cannot end a class header, so a `{` after
// one opens a type literal instead of a body.
var tsContextual = map[string]bool{
	"extends": true, "implements": true, "keyof": true, wordTypeof: true,
	wordAs: true, "is": true, "in": true, "of": true, wordNew: true,
}

// tsDeclPrefixes are the words that turn a variable keyword into a
// declaration of something else.
var tsDeclPrefixes = map[string]bool{
	wordEnum: true, wordType: true, wordInterface: true, wordClass: true,
	wordFunction: true, wordAsync: true, wordDeclare: true, wordAbstract: true,
	"namespace": true, "module": true,
}

// tsMemberModifiers are the words a class member statement can start after.
var tsMemberModifiers = map[string]bool{
	"static": true, wordAsync: true, "get": true, "set": true, "readonly": true,
	"public": true, "private": true, "protected": true, "override": true,
	wordDeclare: true, wordAbstract: true, "accessor": true,
}

// tsPuncts are the punctuators the scanner joins, longest form first.
var tsPuncts = []string{
	">>>=", "...", "===", "!==", "**=", "<<=", ">>=", ">>>", "&&=", "||=", "??=",
	"=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "++", "--", "+=", "-=",
	"*=", "/=", "%=", "&=", "|=", "^=", "**", "<<", ">>",
}

// tsLexer tokenizes one TypeScript or JavaScript source file.
type tsLexer struct {
	src       []byte
	tokens    []tsToken
	parens    []bool
	prev      tsToken
	pos       int
	line      int
	lineStart int
	depth     int
	rewinds   int
	hasPrev   bool
	lastParen bool
}

// scanTypeScript extracts one TypeScript or JavaScript file.
func scanTypeScript(src []byte, buf *fileBuf) {
	l := &tsLexer{src: src, line: 1}
	for l.pos < len(src) {
		l.lex()
	}
	tsExtract(l.tokens, buf)
}

// lex reads the next token, or skips trivia.
func (l *tsLexer) lex() {
	if !l.skipSpace() && l.pos < len(l.src) {
		l.lexToken()
	}
}

// lexToken reads the one token at the cursor.
func (l *tsLexer) lexToken() {
	line, col := l.line, l.pos-l.lineStart+1
	switch c := l.src[l.pos]; {
	case c == '`':
		l.lexTemplate(line, col)
	case c == '\'' || c == '"':
		l.lexString(c, line, col)
	case c == '/':
		l.lexSlash(line, col)
	case isDigit(c):
		l.lexNumber(line, col)
	case c == '#':
		l.lexPrivate(line, col)
	case isIdentStart(c):
		l.lexIdent(line, col)
	case l.lexJSX():
	default:
		l.lexPunct(line, col)
	}
}

// lexSlash reads a regex literal where one may start, and a punctuator where
// the slash divides instead.
func (l *tsLexer) lexSlash(line, col int) {
	if !l.regexAllowed() || !l.lexRegex(line, col) {
		l.lexPunct(line, col)
	}
}

// skipSpace consumes whitespace and comments, reporting whether it consumed
// anything.
func (l *tsLexer) skipSpace() bool {
	start := l.pos
	for l.pos < len(l.src) {
		if !l.skipTrivia() {
			break
		}
	}
	return l.pos != start
}

// skipTrivia consumes one whitespace byte or one comment, reporting whether it
// consumed anything.
func (l *tsLexer) skipTrivia() bool {
	switch c := l.src[l.pos]; {
	case c == ' ' || c == '\t' || c == '\r' || c == '\n':
		l.advance(1)
	case c == '/' && l.next(1) == '/':
		l.skipLineComment()
	case c == '/' && l.next(1) == '*':
		l.skipBlockComment()
	default:
		return false
	}
	return true
}

// skipLineComment consumes one `//` comment.
func (l *tsLexer) skipLineComment() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.advance(1)
	}
}

// skipBlockComment consumes one `/* */` comment, or the rest of the file when
// it never closes.
func (l *tsLexer) skipBlockComment() {
	l.advance(2)
	for l.pos < len(l.src) {
		if l.src[l.pos] == '*' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/' {
			l.advance(2)
			return
		}
		l.advance(1)
	}
}

// advance moves the cursor forward and keeps the line accounting current.
func (l *tsLexer) advance(n int) {
	end := min(l.pos+n, len(l.src))
	for i := l.pos; i < end; i++ {
		if l.src[i] == '\n' {
			l.line++
			l.lineStart = i + 1
		}
	}
	l.pos = end
}

// emit appends one token and tracks the regex context it leaves behind.
func (l *tsLexer) emit(kind int, text string, line, col int) {
	if kind == tsPunct {
		l.parenStep(text)
	}
	t := tsToken{text: text, kind: kind, line: line, col: col}
	l.tokens = append(l.tokens, t)
	l.prev, l.hasPrev = t, true
}

// parenStep keeps the open-paren stack, so a `)` can say whether it closed a
// control clause.
func (l *tsLexer) parenStep(text string) {
	switch text {
	case punctParen:
		l.parens = append(l.parens, l.hasPrev && l.prev.kind == tsIdent && tsControlParens[l.prev.text])
	case punctParenEnd:
		n := len(l.parens)
		l.lastParen = n > 0 && l.parens[n-1]
		if n > 0 {
			l.parens = l.parens[:n-1]
		}
	}
}

// lexString consumes one quoted string. A line break inside it ends the token,
// so an unterminated literal can never swallow the rest of the file.
func (l *tsLexer) lexString(quote byte, line, col int) {
	start := l.pos
	l.advance(1)
	for l.pos < len(l.src) {
		switch l.src[l.pos] {
		case '\\':
			l.advance(2)
		case quote:
			l.advance(1)
			l.emit(tsValue, l.literal(start), line, col)
			return
		case '\n':
			l.emit(tsValue, l.literal(start), line, col)
			return
		default:
			l.advance(1)
		}
	}
	l.emit(tsValue, l.literal(start), line, col)
}

// next returns the byte one offset ahead, 0 past the end.
func (l *tsLexer) next(n int) byte {
	if l.pos+n >= len(l.src) {
		return 0
	}
	return l.src[l.pos+n]
}

// literal returns the source text one literal token spans.
func (l *tsLexer) literal(start int) string { return string(l.src[start:l.pos]) }

// lexTemplate consumes one template literal, tokenizing each `${ … }`
// interpolation as code so a call inside one stays a call site.
func (l *tsLexer) lexTemplate(line, col int) {
	l.advance(1)
	for l.pos < len(l.src) {
		switch c := l.src[l.pos]; {
		case c == '\\':
			l.advance(2)
		case c == '`':
			l.advance(1)
			l.emit(tsValue, "", line, col)
			return
		case c == '$' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '{':
			l.advance(2)
			l.emit(tsValue, "", line, col)
			l.lexInterpolation()
		default:
			l.advance(1)
		}
	}
	l.emit(tsValue, "", line, col)
}

// lexInterpolation tokenizes one `{ … }` body up to its closing brace.
func (l *tsLexer) lexInterpolation() {
	if l.depth >= tsMaxDepth {
		return
	}
	l.depth++
	budget := len(l.src)
	for l.pos < len(l.src) && budget > 0 {
		budget--
		if l.skipSpace() {
			continue
		}
		switch l.src[l.pos] {
		case '{':
			l.advance(1)
			l.lexInterpolation()
			continue
		case '}':
			l.advance(1)
			l.depth--
			return
		default:
		}
		l.lex()
	}
	l.depth--
}

// lexRegex consumes one regex literal, reporting false when the line ends
// first and the slash was a division after all.
func (l *tsLexer) lexRegex(line, col int) bool {
	inClass := false
	for i := l.pos + 1; i < len(l.src); i++ {
		switch c := l.src[i]; {
		case c == '\n':
			return false
		case c == '\\':
			i++
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			l.regexEnd(i, line, col)
			return true
		}
	}
	return false
}

// regexEnd consumes one closed regex literal and the flags after it.
func (l *tsLexer) regexEnd(end, line, col int) {
	start := l.pos
	i := end + 1
	for i < len(l.src) && isIdentPart(l.src[i]) {
		i++
	}
	l.advance(i - l.pos)
	l.emit(tsValue, l.literal(start), line, col)
}

// regexAllowed reports whether a `/` at the cursor starts a regex literal
// rather than a division.
func (l *tsLexer) regexAllowed() bool {
	if !l.hasPrev {
		return true
	}
	switch l.prev.kind {
	case tsValue:
		return false
	case tsPunct:
		switch l.prev.text {
		case punctParenEnd, "]", "++", "--":
			return l.prev.text == punctParenEnd && l.lastParen
		default:
			return true
		}
	default:
		return tsRegexAfter[l.prev.text]
	}
}

// lexNumber consumes one numeric literal.
func (l *tsLexer) lexNumber(line, col int) {
	start := l.pos
	for l.pos < len(l.src) && (isIdentPart(l.src[l.pos]) || l.src[l.pos] == '.') {
		l.advance(1)
	}
	l.emit(tsValue, l.literal(start), line, col)
}

// lexPrivate consumes one `#name` private member.
func (l *tsLexer) lexPrivate(line, col int) {
	start := l.pos
	l.advance(1)
	if l.pos >= len(l.src) || !isIdentStart(l.src[l.pos]) {
		l.emit(tsPunct, "#", line, col)
		return
	}
	for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
		l.advance(1)
	}
	l.emit(tsIdent, string(l.src[start:l.pos]), line, col)
}

// lexIdent consumes one identifier or keyword.
func (l *tsLexer) lexIdent(line, col int) {
	start := l.pos
	for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
		l.advance(1)
	}
	l.emit(tsIdent, string(l.src[start:l.pos]), line, col)
}

// lexPunct consumes one punctuator.
func (l *tsLexer) lexPunct(line, col int) {
	text := l.punctText()
	l.advance(len(text))
	l.emit(tsPunct, text, line, col)
}

// punctText returns the punctuator at the cursor, longest form first.
func (l *tsLexer) punctText() string {
	rest := l.src[l.pos:]
	for _, form := range tsPuncts {
		if len(form) <= len(rest) && string(rest[:len(form)]) == form {
			return form
		}
	}
	return string(rest[:1])
}

// isIdentStart reports a byte that can start an identifier.
func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || isAlpha(c) || c >= tsNonASCII
}

// isIdentPart reports a byte that can continue an identifier.
func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) }

// isAlpha reports an ASCII letter.
func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// isDigit reports an ASCII digit.
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// tsMark is one speculative scan's position, kept so an abandoned guess can be
// rewound without re-lexing what it consumed.
type tsMark struct {
	prev      tsToken
	pos       int
	line      int
	lineStart int
	tokens    int
	parens    int
	hasPrev   bool
	lastParen bool
}

// mark records the lexer's position.
func (l *tsLexer) mark() *tsMark {
	return &tsMark{
		pos: l.pos, line: l.line, lineStart: l.lineStart, tokens: len(l.tokens),
		parens: len(l.parens), prev: l.prev, hasPrev: l.hasPrev, lastParen: l.lastParen,
	}
}

// rewind restores a recorded position and drops the tokens it produced.
func (l *tsLexer) rewind(m *tsMark) {
	l.pos, l.line, l.lineStart = m.pos, m.line, m.lineStart
	l.tokens, l.parens = l.tokens[:m.tokens], l.parens[:m.parens]
	l.prev, l.hasPrev, l.lastParen = m.prev, m.hasPrev, m.lastParen
}

// lexJSX speculatively consumes one JSX element: tags are scanned, `{ … }`
// expressions are tokenized, and the text between them is skipped as markup. A
// guess that does not close is rewound and the `<` becomes a punctuator.
func (l *tsLexer) lexJSX() bool {
	if l.src[l.pos] != '<' || l.rewinds >= tsMaxRewinds || l.depth >= tsMaxDepth || !l.jsxStart() {
		return false
	}
	m := l.mark()
	if !l.jsxElement() {
		l.rewind(m)
		l.rewinds++
		return false
	}
	return true
}

// jsxStart reports whether a `<` at the cursor can open an element: it must
// follow a token that allows an expression, and name a tag or a fragment.
func (l *tsLexer) jsxStart() bool {
	if l.pos+1 >= len(l.src) {
		return false
	}
	next := l.src[l.pos+1]
	if next != '>' && next != '/' && !isIdentStart(next) {
		return false
	}
	if !l.hasPrev {
		return true
	}
	if l.prev.kind == tsPunct {
		return l.prev.text != punctParenEnd && l.prev.text != "]"
	}
	return tsRegexAfter[l.prev.text]
}

// jsxElement consumes one element or fragment.
func (l *tsLexer) jsxElement() bool {
	l.advance(1)
	name := l.jsxName()
	if name == "" {
		return l.jsxFragment()
	}
	if !l.jsxAttributes() {
		return false
	}
	if l.eat("/>") {
		return true
	}
	if !l.eat(">") {
		return false
	}
	if l.jsxArrow() {
		return false
	}
	return l.jsxChildren(name)
}

// jsxFragment consumes one `<>…</>` fragment.
func (l *tsLexer) jsxFragment() bool {
	return l.eat(">") && l.jsxChildren("")
}

// jsxArrow reports whether the `>` just consumed ends a type parameter list
// rather than a tag, which is how a generic arrow function looks.
func (l *tsLexer) jsxArrow() bool {
	for i := l.pos; i < len(l.src); i++ {
		switch l.src[i] {
		case ' ', '\t', '\r', '\n':
		case '(':
			return true
		default:
			return false
		}
	}
	return false
}

// jsxName consumes one tag name.
func (l *tsLexer) jsxName() string {
	start := l.pos
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if isIdentPart(c) || c == '.' || c == ':' || c == '-' {
			l.advance(1)
			continue
		}
		break
	}
	return string(l.src[start:l.pos])
}

// jsxAttributes consumes one tag's attributes up to its closing `>` or `/>`.
func (l *tsLexer) jsxAttributes() bool {
	for l.pos < len(l.src) {
		switch c := l.src[l.pos]; {
		case c == '>':
			return true
		case c == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '>':
			return true
		case c == '"' || c == '\'':
			l.jsxAttrString(c)
		case c == '{':
			l.advance(1)
			l.lexInterpolation()
		case c == '<':
			return false
		default:
			l.advance(1)
		}
	}
	return false
}

// jsxAttrString consumes one JSX attribute string, which carries no escapes.
func (l *tsLexer) jsxAttrString(quote byte) {
	l.advance(1)
	for l.pos < len(l.src) && l.src[l.pos] != quote && l.src[l.pos] != '\n' {
		l.advance(1)
	}
	if l.pos < len(l.src) && l.src[l.pos] == quote {
		l.advance(1)
	}
}

// jsxChildren consumes one element's children up to its closing tag.
func (l *tsLexer) jsxChildren(name string) bool {
	for l.pos < len(l.src) {
		switch l.src[l.pos] {
		case '{':
			l.advance(1)
			l.lexInterpolation()
		case '<':
			ok, closed := l.jsxChild(name)
			if !ok || closed {
				return ok
			}
		default:
			l.advance(1)
		}
	}
	return false
}

// jsxChild consumes one child element, reporting whether it parsed and whether
// it closed the parent.
func (l *tsLexer) jsxChild(name string) (bool, bool) {
	if l.jsxClose() {
		return l.jsxClosing(name), true
	}
	return l.jsxElement(), false
}

// jsxClose advances past a `</` and reports it.
func (l *tsLexer) jsxClose() bool {
	if l.src[l.pos] != '<' || l.pos+1 >= len(l.src) || l.src[l.pos+1] != '/' {
		return false
	}
	l.advance(2)
	return true
}

// jsxClosing consumes one closing tag and matches it against the open one.
func (l *tsLexer) jsxClosing(name string) bool {
	closing := l.jsxName()
	for l.pos < len(l.src) {
		switch l.src[l.pos] {
		case '>':
			l.advance(1)
			return closing == name
		case ' ', '\t', '\r', '\n':
			l.advance(1)
		default:
			return false
		}
	}
	return false
}

// eat consumes one literal prefix, reporting whether it was there.
func (l *tsLexer) eat(prefix string) bool {
	if len(l.src)-l.pos < len(prefix) || string(l.src[l.pos:l.pos+len(prefix)]) != prefix {
		return false
	}
	l.advance(len(prefix))
	return true
}

// tsExtract records the declarations, imports, and call sites of one token
// stream.
func tsExtract(tokens []tsToken, buf *fileBuf) {
	x := &tsExtractor{
		tokens: tokens, buf: buf, skip: map[int]bool{},
		match: tsPairs(tokens), depth: tsBraceDepths(tokens),
	}
	x.blocks = tsBlocks(x.depth, tokens)
	x.inside, x.outer = tsBlockIndex(x.blocks, len(tokens))
	x.imports()
	x.declarations()
	x.calls()
}

// tsExtractor walks one token stream.
type tsExtractor struct {
	skip   map[int]bool
	buf    *fileBuf
	tokens []tsToken
	match  []int
	depth  []int
	blocks []tsBlock
	inside []int
	outer  []int
}

// tsBlock is one declaration body: a class, interface, enum, or type literal.
// Only a class body holds methods, and no block at all holds call sites.
type tsBlock struct {
	name  string
	kind  int
	open  int
	close int
	depth int
}

// tsPairs pairs every bracket with its partner, -1 where unmatched.
func tsPairs(tokens []tsToken) []int {
	match := make([]int, len(tokens))
	for i := range match {
		match[i] = -1
	}
	var open []int
	for i, t := range tokens {
		if t.kind == tsPunct {
			open = pairToken(t.text, i, open, match)
		}
	}
	return match
}

// pairToken pairs one bracket with its partner on the open stack.
func pairToken(text string, i int, open []int, match []int) []int {
	step := bracketStep(text)
	switch {
	case step > 0:
		return append(open, i)
	case step == 0 || len(open) == 0:
		return open
	default:
	}
	j := open[len(open)-1]
	match[i], match[j] = j, i
	return open[:len(open)-1]
}

// tsBraceDepths returns the open-brace count in force at each token.
func tsBraceDepths(tokens []tsToken) []int {
	depth := make([]int, len(tokens))
	n := 0
	for i, t := range tokens {
		if t.kind == tsPunct && t.text == punctBraceEnd && n > 0 {
			n--
		}
		depth[i] = n
		if t.kind == tsPunct && t.text == punctBrace {
			n++
		}
	}
	return depth
}

// The declaration bodies the scanner tracks.
const (
	blockClass = iota
	blockInterface
	blockType
)

// tsBlockNest is the declaration nesting the block sweep's stack starts with.
const tsBlockNest = 8

// tsBlockKinds maps a declaration keyword to the body it opens.
var tsBlockKinds = map[string]int{
	wordClass: blockClass, wordInterface: blockInterface, wordEnum: blockInterface, wordType: blockType,
}

// tsBlocks finds every declaration body and the name it belongs to.
func tsBlocks(depth []int, tokens []tsToken) []tsBlock {
	var blocks []tsBlock
	for i, t := range tokens {
		if t.kind != tsIdent || !tsBlockStart(tokens, i) {
			continue
		}
		name := tokens[i+1].text
		open := tsBlockBody(tokens, i+2)
		if open < 0 {
			continue
		}
		blocks = append(blocks, tsBlock{
			kind: tsBlockKinds[t.text], name: name, open: open,
			close: tsPairClose(tokens, open), depth: depth[open],
		})
	}
	return blocks
}

// tsBlockStart reports whether the token at i opens a declaration body.
func tsBlockStart(tokens []tsToken, i int) bool {
	if _, ok := tsBlockKinds[tokens[i].text]; !ok {
		return false
	}
	if i > 0 && tokens[i-1].text == punctDot {
		return false
	}
	if i+1 >= len(tokens) || tokens[i+1].kind != tsIdent || tsReserved[tokens[i+1].text] {
		return false
	}
	if tokens[i].text == wordType && i > 0 && !tsTypeAliasPrefix(tokens[i-1]) {
		return false
	}
	return true
}

// tsTypeAliasPrefix reports whether the token before a `type` lets it open an
// alias. The word is contextual, so `const type = …`, `as type`, and `x.type`
// name something else; a punctuator, `export`, or `declare` opens one.
func tsTypeAliasPrefix(prev tsToken) bool {
	return prev.kind == tsPunct || prev.text == wordExport || prev.text == wordDeclare
}

// tsBlockBody finds the `{` that opens a declaration body, starting after the
// declared name. A `{` after `extends` or another contextual word opens a type
// literal of its own, not the body.
func tsBlockBody(tokens []tsToken, from int) int {
	for j := from; j < len(tokens) && j < from+tsMaxHeader; j++ {
		if tsHeaderEnds(tokens, j) {
			return -1
		}
		if tsBodyOpen(tokens, j) {
			return j
		}
	}
	return -1
}

// tsHeaderEnds reports whether one token ends a declaration header unopened.
func tsHeaderEnds(tokens []tsToken, j int) bool {
	return tokens[j].kind == tsPunct && tokens[j].text == punctSemi
}

// tsBodyOpen reports whether the brace at j opens a declaration body.
func tsBodyOpen(tokens []tsToken, j int) bool {
	if tokens[j].kind != tsPunct || tokens[j].text != punctBrace {
		return false
	}
	prev := tokens[j-1]
	return prev.text == punctAssign || prev.kind == tsIdent && !tsContextual[prev.text]
}

// tsPairClose returns the index of the brace closing one opening token.
func tsPairClose(tokens []tsToken, open int) int {
	n := 0
	for i := open; i < len(tokens); i++ {
		if tokens[i].kind != tsPunct || tokens[i].text != punctBrace && tokens[i].text != punctBraceEnd {
			continue
		}
		n += bracketStep(tokens[i].text)
		if n == 0 {
			return i
		}
	}
	return len(tokens)
}

// tsBlockIndex answers both block questions once per token: inside[i] is the
// innermost declaration body holding token i and outer[b] the body holding
// block b, both by index, -1 for none. One sweep over the token stream, so a
// file's cost is its token count rather than tokens times blocks.
func tsBlockIndex(blocks []tsBlock, tokens int) (inside, outer []int) {
	inside, outer = negIndex(tokens), negIndex(len(blocks))
	s := &blockStack{blocks: blocks, outer: outer, open: make([]int, 0, tsBlockNest)}
	for i := range inside {
		s.enter(i)
		s.leave(i)
		inside[i] = s.top()
	}
	return inside, outer
}

// blockStack tracks the declaration bodies open at one token index. Bodies nest
// by brace, so they open and close on a stack.
type blockStack struct {
	blocks []tsBlock
	outer  []int
	open   []int
	next   int
}

// enter opens every body that began before token i, recording its holder.
func (s *blockStack) enter(i int) {
	for s.next < len(s.blocks) && s.blocks[s.next].open < i {
		s.outer[s.next] = s.top()
		s.open = append(s.open, s.next)
		s.next++
	}
}

// leave closes every body that ended at or before token i.
func (s *blockStack) leave(i int) {
	for len(s.open) > 0 && s.blocks[s.open[len(s.open)-1]].close <= i {
		s.open = s.open[:len(s.open)-1]
	}
}

// top returns the innermost open body index, -1 when none is open.
func (s *blockStack) top() int {
	if len(s.open) == 0 {
		return -1
	}
	return s.open[len(s.open)-1]
}

// negIndex returns n indexes set to -1.
func negIndex(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = -1
	}
	return out
}

// enclosingBlock returns the innermost declaration body holding one token
// index.
func (x *tsExtractor) enclosingBlock(i int) *tsBlock {
	if b := x.inside[i]; b >= 0 {
		return &x.blocks[b]
	}
	return nil
}

// classMember returns the class one token index is a direct member of, when the
// member statement starts there.
func (x *tsExtractor) classMember(i int) *tsBlock {
	block := x.enclosingBlock(i)
	if block == nil || block.kind != blockClass || x.depth[i] != block.depth+1 || !x.memberStart(i) {
		return nil
	}
	return block
}

// memberStart reports whether one token begins a class member statement rather
// than sitting inside a member's initializer: after the previous member's
// closing token, after a modifier, after a decorator, or on a new line once the
// previous member's value is complete.
func (x *tsExtractor) memberStart(i int) bool {
	switch prev := x.prevText(i); prev {
	case punctBrace, punctBraceEnd, punctSemi, "*":
		return true
	default:
		return tsMemberModifiers[prev] || x.afterDecorator(i) || x.afterMemberValue(i)
	}
}

// afterDecorator reports whether the token before i closes a decorator's
// argument list, as in `@Get(':id') findOne() {…}`.
func (x *tsExtractor) afterDecorator(i int) bool {
	if i < tsDecoratorHead || x.text(i-1) != punctParenEnd {
		return false
	}
	open := x.match[i-1]
	return open > 1 && x.tokens[open-1].kind == tsIdent && x.tokens[open-2].text == punctAt
}

// afterMemberValue reports whether the previous member's value ended on an
// earlier line, so a member name on this line starts the next one: a class
// written without semicolons, as in `count = 0` followed by `inc() {…}`.
func (x *tsExtractor) afterMemberValue(i int) bool {
	if i == 0 || x.tokens[i].line <= x.tokens[i-1].line {
		return false
	}
	return x.memberValueEnd(i - 1)
}

// memberValueEnd reports whether one token can end a class member's value.
// Keywords are left out: `return` or `this` before a name on the next line is
// far more likely an unfinished expression than a member boundary.
func (x *tsExtractor) memberValueEnd(j int) bool {
	t := x.tokens[j]
	switch t.kind {
	case tsValue:
		return true
	case tsIdent:
		return !tsReserved[t.text]
	default:
		return t.text == punctParenEnd || t.text == punctBracketEnd ||
			t.text == punctBraceEnd || t.text == "++" || t.text == "--"
	}
}

// imports records every ES and CommonJS module edge.
func (x *tsExtractor) imports() {
	for i, t := range x.tokens {
		if t.kind != tsIdent {
			continue
		}
		switch t.text {
		case "require":
			x.requireEdge(i)
		case wordImport:
			x.esEdge(i + 1)
		case wordExport:
			x.reExportEdge(i + 1)
		default:
		}
	}
}

// esEdge records one `import …` clause: a side-effect module, a dynamic
// import, or a binding set.
func (x *tsExtractor) esEdge(i int) {
	if x.requireAssign(i) {
		return
	}
	if i < len(x.tokens) && x.tokens[i].text == wordType && i+1 < len(x.tokens) && x.tokens[i+1].kind != tsValue {
		i++
	}
	if i >= len(x.tokens) {
		return
	}
	switch {
	case x.tokens[i].text == punctParen:
		x.buf.addImport(x.specifier(i+1), "")
	case x.tokens[i].kind == tsValue:
		x.buf.addImport(x.specifier(i), "")
	default:
		names, module := x.bindingNames(i)
		x.addBindings(module, names)
	}
}

// reExportEdge records one `export … from` module edge. A re-exported name is
// not a binding in this file, so only the module lands.
func (x *tsExtractor) reExportEdge(i int) {
	if _, module := x.bindingNames(i); module != "" {
		x.buf.addImport(module, "")
	}
}

// requireAssign reports whether one import clause is the `x = require(…)`
// form, whose edge the require call itself records.
func (x *tsExtractor) requireAssign(i int) bool {
	return i+2 < len(x.tokens) && x.tokens[i].kind == tsIdent && x.tokens[i+1].text == punctAssign &&
		x.tokens[i+2].text == "require"
}

// requireEdge records one CommonJS `require(…)` edge and its binding.
func (x *tsExtractor) requireEdge(i int) {
	if i+2 >= len(x.tokens) || x.tokens[i+1].text != punctParen || x.tokens[i+2].kind != tsValue {
		return
	}
	x.buf.addImport(x.specifier(i+2), x.requireBinding(i))
}

// requireBinding returns the name a `require` call binds.
func (x *tsExtractor) requireBinding(i int) string {
	if i >= 2 && x.tokens[i-1].text == punctAssign && x.tokens[i-2].kind == tsIdent {
		return x.tokens[i-2].text
	}
	return ""
}

// bindingNames reads one import clause up to its `from` specifier.
func (x *tsExtractor) bindingNames(start int) ([]string, string) {
	var names []string
	for i := start; i < len(x.tokens) && i < start+tsMaxHeader; i++ {
		if x.text(i) == wordFrom {
			return names, x.specifier(i + 1)
		}
		if x.clauseEnds(i) {
			return names, ""
		}
		names, i = x.bindingStep(names, i)
	}
	return names, ""
}

// clauseEnds reports whether one token closes an import clause unbound.
func (x *tsExtractor) clauseEnds(i int) bool {
	switch x.text(i) {
	case punctSemi, wordImport, wordExport:
		return true
	default:
		return false
	}
}

// bindingStep reads one clause item, returning the names it added and the
// index the clause continues at.
func (x *tsExtractor) bindingStep(names []string, i int) ([]string, int) {
	t := x.tokens[i]
	if t.text == punctBrace && x.match[i] > 0 {
		return append(names, x.namedBindings(i+1, x.match[i])...), x.match[i]
	}
	if t.text == "*" && x.text(i+1) == wordAs {
		return append(names, x.text(i+2)), i + 2
	}
	if x.bindable(i) {
		return append(names, t.text), i
	}
	return names, i
}

// bindable reports whether one token names a local binding.
func (x *tsExtractor) bindable(i int) bool {
	t := x.tokens[i]
	return t.kind == tsIdent && !tsReserved[t.text] && t.text != wordType && t.text != wordAs
}

// namedBindings reads one `{ a, b as c }` clause.
func (x *tsExtractor) namedBindings(from, end int) []string {
	var names []string
	for j := from; j < end; j++ {
		t := x.tokens[j]
		if t.kind != tsIdent || tsReserved[t.text] || t.text == wordType {
			continue
		}
		if j+2 < end && x.text(j+1) == wordAs && x.tokens[j+2].kind == tsIdent {
			names = append(names, x.tokens[j+2].text)
			j += 2
			continue
		}
		names = append(names, t.text)
	}
	return names
}

// specifier returns the string token at i as a module specifier, without its
// quotes.
func (x *tsExtractor) specifier(i int) string {
	if i < len(x.tokens) && x.tokens[i].kind == tsValue {
		return strings.Trim(x.tokens[i].text, `'"`)
	}
	return ""
}

// addBindings records one module edge per bound name, or one bare edge when
// the clause binds nothing.
func (x *tsExtractor) addBindings(module string, names []string) {
	if module == "" {
		return
	}
	if len(names) == 0 {
		x.buf.addImport(module, "")
		return
	}
	for _, name := range names {
		x.buf.addImport(module, name)
	}
}

// declarations records top-level declarations and class members.
func (x *tsExtractor) declarations() {
	for i, t := range x.tokens {
		if t.kind != tsIdent {
			continue
		}
		if class := x.classMember(i); class != nil {
			x.member(i, class)
			continue
		}
		if x.depth[i] != 0 {
			continue
		}
		x.declaration(i)
	}
}

// declaration records the top-level declaration a keyword at i introduces.
func (x *tsExtractor) declaration(i int) {
	name, kind, ok := tsDecl(x.tokens, i)
	if !ok {
		return
	}
	x.skip[name] = true
	if kind != wordFunc && kind != wordMethod && x.arrowBinding(name+1) {
		kind = wordFunc
	}
	x.buf.addDecl(x.tokens[name].text, kind, "", x.tokens[name].line, x.exported(i))
}

// tsDecl reads the declaration a keyword at i introduces: the index of its
// name and its kind.
func tsDecl(tokens []tsToken, i int) (int, string, bool) {
	switch tokens[i].text {
	case "function":
		return tsNamed(tokens, i, wordFunc)
	case wordConst:
		return tsNamed(tokens, i, wordConst)
	case "let", wordVar:
		return tsNamed(tokens, i, wordVar)
	case wordClass, "enum":
		return tsNamed(tokens, i, wordType)
	case wordInterface:
		return tsHeader(tokens, i, wordType, punctBrace, "<", "extends")
	case wordType:
		return tsHeader(tokens, i, wordType, punctAssign, "<", "extends")
	default:
		return 0, "", false
	}
}

// tsNamed reads the name one declaration keyword introduces.
func tsNamed(tokens []tsToken, i int, kind string) (int, string, bool) {
	j := i + 1
	if j < len(tokens) && tokens[j].kind == tsPunct && tokens[j].text == "*" {
		j++
	}
	if j >= len(tokens) || tokens[j].kind != tsIdent || tsReserved[tokens[j].text] || tsDeclPrefixes[tokens[j].text] {
		return 0, "", false
	}
	return j, kind, true
}

// tsHeader reads the name of a declaration whose body must follow it.
func tsHeader(tokens []tsToken, i int, kind string, body ...string) (int, string, bool) {
	j, kind, ok := tsNamed(tokens, i, kind)
	if !ok || j+1 >= len(tokens) {
		return 0, "", false
	}
	if slices.Contains(body, tokens[j+1].text) {
		return j, kind, true
	}
	return 0, "", false
}

// arrowBinding reports whether the initializer after the token at i binds a
// function rather than a value.
func (x *tsExtractor) arrowBinding(i int) bool {
	if x.text(i) != punctAssign {
		return false
	}
	for j := i + 1; j < len(x.tokens) && j < i+tsMaxHeader; j++ {
		switch x.text(j) {
		case wordFunction, punctArrow:
			return true
		case punctSemi, punctBraceEnd:
			return false
		}
	}
	return false
}

// member records one class member method, written either as a method or as an
// arrow-valued property.
func (x *tsExtractor) member(i int, class *tsBlock) {
	if class.name == "" || tsReserved[x.tokens[i].text] {
		return
	}
	if prev := x.prevText(i); prev == punctDot || prev == "?." || prev == wordNew {
		return
	}
	if i+1 >= len(x.tokens) {
		return
	}
	if x.tokens[i+1].text != punctParen && !x.arrowBinding(i+1) {
		return
	}
	x.skip[i] = true
	x.buf.addDecl(x.tokens[i].text, wordMethod, class.name, x.tokens[i].line, !strings.HasPrefix(x.tokens[i].text, "#"))
}

// exported reports whether the declaration at i carries an `export`, walking
// back over the modifiers that can sit between the two, as in
// `export default async function`.
func (x *tsExtractor) exported(i int) bool {
	j := i - 1
	for j >= 0 && tsExportModifier(x.text(j)) {
		j--
	}
	return x.text(j) == wordExport
}

// tsExportModifier reports whether one word can stand between `export` and the
// declaration keyword it exports.
func tsExportModifier(word string) bool {
	switch word {
	case wordAsync, wordDefault, wordDeclare, wordAbstract:
		return true
	default:
		return false
	}
}

// calls records every call and attribute site.
func (x *tsExtractor) calls() {
	for i := range x.tokens {
		if x.callCallee(i) {
			t := x.tokens[i]
			x.buf.addCall(t.text, x.callKind(i), x.callQualifier(i), t.line, t.col)
		}
	}
}

// callCallee reports whether one token is a callee worth recording.
func (x *tsExtractor) callCallee(i int) bool {
	t := x.tokens[i]
	return t.kind == tsIdent && !tsReserved[t.text] && !x.skip[i] && x.callParen(i) && !x.inTypeBlock(i)
}

// callKind classifies one callee: a method reached through `.`, and a call
// everywhere else, including a constructor reached through `new`.
func (x *tsExtractor) callKind(i int) string {
	prev := x.prevText(i)
	if (prev == punctDot || prev == punctOptional) && !x.newCall(i) {
		return KindMethod
	}
	return KindCall
}

// callQualifier returns the token immediately before a dotted call's dot.
func (x *tsExtractor) callQualifier(i int) string {
	prev := x.prevText(i)
	if i < 2 || (prev != punctDot && prev != punctOptional) || x.tokens[i-2].kind != tsIdent {
		return ""
	}
	start := i - 2
	for start >= 2 && (x.text(start-1) == punctDot || x.text(start-1) == punctOptional) && x.tokens[start-2].kind == tsIdent {
		start -= 2
	}
	return x.text(start)
}

// callParen reports whether the token after i opens a call's arguments, with
// optional chaining allowed in between.
func (x *tsExtractor) callParen(i int) bool {
	next := i + 1
	if next < len(x.tokens) && x.tokens[next].text == "?." {
		next++
	}
	return next < len(x.tokens) && x.tokens[next].text == punctParen
}

// inTypeBlock reports whether one token sits in a declaration that holds types
// rather than executable code, where a `name(` is a signature, not a call.
func (x *tsExtractor) inTypeBlock(i int) bool {
	for b := x.inside[i]; b >= 0; b = x.outer[b] {
		if x.blocks[b].kind != blockClass {
			return true
		}
	}
	return false
}

// text returns one token's text, "" outside the stream.
func (x *tsExtractor) text(i int) string {
	if i < 0 || i >= len(x.tokens) {
		return ""
	}
	return x.tokens[i].text
}

// prevText returns the text before one token index, "" at the start.
func (x *tsExtractor) prevText(i int) string {
	if i == 0 {
		return ""
	}
	return x.tokens[i-1].text
}

// newCall reports whether the call at i is the constructor of a `new`
// expression, including one reached through a selector.
func (x *tsExtractor) newCall(i int) bool {
	if x.prevText(i) == wordNew {
		return true
	}
	return i >= tsNewSelector && x.prevText(i) == punctDot && x.tokens[i-tsNewSelector].text == wordNew
}
