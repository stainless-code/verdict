package indexcore

import (
	"path"
	"slices"
	"strings"
)

// language names one source language the index scans. The value is published
// in the tree document and on every caller row.
type language string

// The languages the index scans.
const (
	langGo         language = "go"
	langTypeScript language = "typescript"
	langJavaScript language = "javascript"
	langPython     language = "python"
)

// family is the scanner a language is read by. JavaScript and TypeScript are
// one grammar family with two published names.
func (l language) family() language {
	if l == langJavaScript {
		return langTypeScript
	}
	return l
}

// sameFamily reports whether two files answer to one scanner's grammar, and so
// can name each other's declarations. Go, Python, and the JavaScript family are
// three separate worlds.
func (l language) sameFamily(other language) bool { return l.family() == other.family() }

// The words the index publishes as declaration kinds and compares as language
// keywords, and the punctuators the scanners switch on. A kind and its keyword
// spell the same word, so one constant serves both.
const (
	wordAs        = "as"
	wordAsync     = "async"
	wordClass     = "class"
	wordConst     = "const"
	wordDefault   = "default"
	wordDef       = "def"
	wordDelete    = "delete"
	wordElse      = "else"
	wordEnum      = "enum"
	wordExport    = "export"
	wordFor       = "for"
	wordFrom      = "from"
	wordFunc      = "func"
	wordFunction  = "function"
	wordImport    = "import"
	wordInterface = "interface"
	wordMethod    = "method"
	wordNew       = "new"
	wordReturn    = "return"
	wordType      = "type"
	wordTypeof    = "typeof"
	wordVar       = "var"
	wordWhile     = "while"
	wordWith      = "with"
)

// The punctuators the scanners compare.
const (
	punctArrow      = "=>"
	punctAssign     = "="
	punctBrace      = "{"
	punctBraceEnd   = "}"
	punctBracket    = "["
	punctBracketEnd = "]"
	punctDot        = "."
	punctOptional   = "?."
	punctParen      = "("
	punctParenEnd   = ")"
	punctSemi       = ";"
)

// bracketStep is one bracket's contribution to a depth count: +1 for an
// opening bracket, -1 for a closing one, 0 for anything else.
func bracketStep(text string) int {
	switch text {
	case punctParen, punctBracket, punctBrace:
		return 1
	case punctParenEnd, punctBracketEnd, punctBraceEnd:
		return -1
	default:
		return 0
	}
}

// sourceLanguages maps each indexed extension to its language. Every
// extension absent here is not indexed at all.
var sourceLanguages = map[string]language{
	".go":  langGo,
	".ts":  langTypeScript,
	".tsx": langTypeScript,
	".mts": langTypeScript,
	".cts": langTypeScript,
	".js":  langJavaScript,
	".jsx": langJavaScript,
	".mjs": langJavaScript,
	".cjs": langJavaScript,
	".py":  langPython,
	".pyi": langPython,
}

// indexedLanguage returns the language one repository path is scanned as, or
// "" when the index does not scan it.
func indexedLanguage(p string) language { return sourceLanguages[path.Ext(p)] }

// fileLanguage reads one tree file's language. A tree document written before
// the field existed omits it, and every file it holds is Go: such a store is
// readable rather than silently misrouted through the scanned-language path.
func fileLanguage(f *treeFile) language {
	if f.Language == "" {
		return langGo
	}
	return language(f.Language)
}

// scanShard parses one blob with its language's lexical scanner. A scanner
// never fails: unknown syntax is skipped, not recorded as a parse error.
func scanShard(blob string, src []byte, lang language, maxRefs int) shard {
	out := newShard(blob, lang, maxRefs)
	buf := newFileBuf(maxRefs)
	if lang.family() == langPython {
		scanPython(src, &buf)
	} else {
		scanTypeScript(src, &buf)
	}

	out.Imports, out.Decls = buf.imports, buf.decls
	out.Refs, out.RefsTruncated = buf.refs.refs, buf.refs.truncated
	return out
}

// fileBuf accumulates one scanned file's imports, declarations, and
// references under the per-file reference cap.
type fileBuf struct {
	imports []importEntry
	decls   []decl
	refs    refBuffer
}

// newFileBuf starts one file's buffer.
func newFileBuf(maxRefs int) fileBuf { return fileBuf{refs: newRefBuffer(maxRefs)} }

// addImport records one import binding, once.
func (b *fileBuf) addImport(module, name string) {
	entry := importEntry{Path: module, Name: name}
	if module == "" || slices.Contains(b.imports, entry) {
		return
	}
	b.imports = append(b.imports, entry)
}

// addDecl records one declaration.
func (b *fileBuf) addDecl(name, kind, recv string, line int, exported bool) {
	if name != "" {
		b.decls = append(b.decls, decl{Name: name, Kind: kind, Recv: recv, Line: line, Exported: exported})
	}
}

// addCall records one call or attribute site. A scanned language has no object
// identity to resolve, so a site carries no qualifier.
func (b *fileBuf) addCall(name, kind string, line, col int) {
	if name != "" {
		b.refs.add(ref{Name: name, Kind: kind, Line: line, Col: col, Selector: kind == KindMethod})
	}
}

// testPath reports whether one indexed path is a test file in its language.
func testPath(lang language, p string) bool {
	base := path.Base(p)
	switch lang.family() {
	case langGo:
		return strings.HasSuffix(p, testSuffix)
	case langTypeScript:
		return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || hasDirSegment(p, "__tests__")
	case langPython:
		return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") ||
			base == "conftest.py" || hasDirSegment(p, "tests") || hasDirSegment(p, "test")
	default:
		return false
	}
}

// hasDirSegment reports whether one name appears as a whole directory segment
// of a slash path.
func hasDirSegment(p, name string) bool {
	for dir := range strings.SplitSeq(path.Dir(p), "/") {
		if dir == name {
			return true
		}
	}
	return false
}
