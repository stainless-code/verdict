package indexcore

import (
	"strings"
	"testing"
)

// pyDeclSource is one Python file carrying every declaration and import form
// the scanner records, written one line per element.
var pyDeclSource = strings.Join([]string{
	`# leading comment`,
	`import os, sys as system`,
	`import a.b.c as abc`,
	`from . import sibling`,
	`from ..pkg.mod import (alpha, beta as b2)`,
	`from x import *`,
	`from .rel import thing`,
	``,
	`@decorator(arg)`,
	`class Widget(Base):`,
	`    attr = 1`,
	``,
	`    def __init__(self, name):`,
	`        self.name = helper(name)`,
	`        pass`,
	``,
	`    async def run(self):`,
	`        return self.__init__(1) + outer(2)`,
	``,
	`    def _private(self):`,
	`        text = f"value {helper(self.name)} {1 + 2}" + f"{'nested'}"`,
	``,
	`def outer(a):`,
	`    y = (a,`,
	`         3)`,
	`    z = """triple`,
	`quoted""" + '''`,
	`'''`,
	`    return thing(a) * a // 2`,
	``,
	`lam = lambda v: v + 1`,
}, "\n")

// TestScanPythonDeclarations proves `def`, `async def`, and `class` at every
// indent, each method qualified by its class, and each import binding.
func TestScanPythonDeclarations(t *testing.T) {
	s := scanFile(t, langPython, pyDeclSource)
	assertDecls(t, s.Decls, []decl{
		{Name: "Widget", Kind: "type", Line: 10, Exported: true},
		{Name: "__init__", Kind: "method", Recv: "Widget", Line: 13},
		{Name: "run", Kind: "method", Recv: "Widget", Line: 17, Exported: true},
		{Name: "_private", Kind: "method", Recv: "Widget", Line: 20},
		{Name: "outer", Kind: "func", Line: 23, Exported: true},
	})
	assertImports(t, s.Imports, []importEntry{
		{Path: "os", Name: "os"},
		{Path: "sys", Name: "system"},
		{Path: "a.b.c", Name: "abc"},
		{Path: ".", Name: "sibling"},
		{Path: "..pkg.mod", Name: "alpha"},
		{Path: "..pkg.mod", Name: "b2"},
		{Path: "x", Name: "*"},
		{Path: ".rel", Name: "thing"},
	})
}

// TestScanPythonCallSites proves the call and attribute sites of the same file,
// including the two inside its f-strings.
func TestScanPythonCallSites(t *testing.T) {
	s := scanFile(t, langPython, pyDeclSource)
	assertRefs(t, s.Refs, []ref{
		{Name: "decorator", Kind: KindCall, Line: 9, Col: 2},
		{Name: "helper", Kind: KindCall, Line: 14, Col: 21},
		{Name: "__init__", Kind: KindMethod, Qualifier: "self", Line: 18, Col: 21, Selector: true},
		{Name: "outer", Kind: KindCall, Line: 18, Col: 35},
		{Name: "helper", Kind: KindCall, Line: 21, Col: 25},
		{Name: "thing", Kind: KindCall, Line: 29, Col: 12},
	})
}

// TestScanPythonTripleQuotes proves a triple-quoted body and a comment never
// declare or call anything, at top level and inside a function.
func TestScanPythonTripleQuotes(t *testing.T) {
	src := strings.Join([]string{
		`"""`,
		`def fake(): pass`,
		`class Fake: pass`,
		`"""`,
		`# def fake2(): pass`,
		`async def run(x):`,
		`    s = """`,
		`    def fake3(): pass`,
		`    """`,
		`    return helper(x)`,
	}, "\n")
	s := scanFile(t, langPython, src)
	assertDecls(t, s.Decls, []decl{{Name: "run", Kind: "func", Line: 6, Exported: true}})
	assertRefs(t, s.Refs, []ref{{Name: "helper", Kind: KindCall, Line: 10, Col: 12}})
}

// TestScanPythonNestedScopes proves a class in a class, a function in a
// method, and a later method's indent all resolve to the right receiver.
func TestScanPythonNestedScopes(t *testing.T) {
	src := strings.Join([]string{
		`class Outer:`,
		`    class Inner:`,
		`        def deep(self):`,
		`            return inner_helper()`,
		``,
		`    def method(self):`,
		`        def local():`,
		`            return local_helper()`,
		`        return local()`,
		``,
		`def top():`,
		`    return top_helper()`,
	}, "\n")
	s := scanFile(t, langPython, src)
	assertDecls(t, s.Decls, []decl{
		{Name: "Outer", Kind: "type", Line: 1, Exported: true},
		{Name: "Inner", Kind: "type", Line: 2, Exported: true},
		{Name: "deep", Kind: "method", Recv: "Inner", Line: 3, Exported: true},
		{Name: "method", Kind: "method", Recv: "Outer", Line: 6, Exported: true},
		{Name: "local", Kind: "func", Line: 7, Exported: true},
		{Name: "top", Kind: "func", Line: 11, Exported: true},
	})
	assertRefs(t, s.Refs, []ref{
		{Name: "inner_helper", Kind: KindCall, Line: 4, Col: 20},
		{Name: "local_helper", Kind: KindCall, Line: 8, Col: 20},
		{Name: "local", Kind: KindCall, Line: 9, Col: 16},
		{Name: "top_helper", Kind: KindCall, Line: 12, Col: 12},
	})
}

// TestScanPythonContinuations proves a bracketed or backslashed line keeps
// reading one statement, and that the line after it starts a new one.
func TestScanPythonContinuations(t *testing.T) {
	src := strings.Join([]string{
		`total = first + \`,
		`    second`,
		`pairs = {`,
		`    "a": pair(1),`,
		`}`,
		`from pkg import (one,`,
		`                 two as three)`,
		`done()`,
	}, "\n")
	s := scanFile(t, langPython, src)
	assertRefs(t, s.Refs, []ref{
		{Name: "pair", Kind: KindCall, Line: 4, Col: 10},
		{Name: "done", Kind: KindCall, Line: 8, Col: 1},
	})
	assertImports(t, s.Imports, []importEntry{
		{Path: "pkg", Name: "one"},
		{Path: "pkg", Name: "three"},
	})
}

// TestScanPythonRefCap proves the per-file cap truncates and says so.
func TestScanPythonRefCap(t *testing.T) {
	s := parseShard("blob", []byte("a()\nb()\nc()\n"), langPython, 2)
	if len(s.Refs) != 2 || !s.RefsTruncated {
		t.Fatalf("refs = %+v truncated %v; want 2 refs, truncated", s.Refs, s.RefsTruncated)
	}
	if s.Language != string(langPython) {
		t.Errorf("language = %q; want %q", s.Language, langPython)
	}
}

// FuzzScanPython proves the scanner survives any bytes, records only positions
// inside the file, and terminates.
func FuzzScanPython(f *testing.F) {
	f.Add(pyDeclSource)
	f.Add("f\"{a(b)}\"\n")
	f.Add("f\"{{a(b)}}\"\n")
	f.Add("from . import (a,\n b)\n")
	f.Add("class A:\n    def b(self):\n        return c(d)\n")
	f.Fuzz(func(t *testing.T, src string) {
		s := parseShard("blob", []byte(src), langPython, 64)
		assertScannedPositions(t, &s, src)
	})
}

// TestScanPythonFStringEscapedBraces proves `{{` and `}}` are literal text: an
// escaped pair never opens an interpolation, and the pair around an expression
// never swallows it.
func TestScanPythonFStringEscapedBraces(t *testing.T) {
	src := strings.Join([]string{
		`def run():`,
		`    a = f"{{escaped()}}"`,
		`    b = f"{{can't {helper(1)}}}"`,
		`    c = f"""{{"k": {helper(2)}}}"""`,
		`    d = f"""{{it's {helper(3)}}}"""`,
		`    e = f"{{{helper(4)}}}"`,
	}, "\n")
	s := scanFile(t, langPython, src)
	assertRefs(t, s.Refs, []ref{
		{Name: "helper", Kind: KindCall, Line: 3, Col: 20},
		{Name: "helper", Kind: KindCall, Line: 4, Col: 21},
		{Name: "helper", Kind: KindCall, Line: 5, Col: 21},
		{Name: "helper", Kind: KindCall, Line: 6, Col: 14},
	})
}

// TestScanPythonRawStringEscapedQuote proves a backslash still guards the quote
// in a raw string: `r'[\'"]'` is one literal, so the declarations and calls
// after it are read at their own lines.
func TestScanPythonRawStringEscapedQuote(t *testing.T) {
	src := strings.Join([]string{
		`import re`,
		``,
		`PATTERN = re.compile(r'[\'"]')`,
		``,
		``,
		`def later():`,
		`    return called()`,
	}, "\n")
	s := scanFile(t, langPython, src)
	assertDecls(t, s.Decls, []decl{
		{Name: "later", Kind: "func", Line: 6, Exported: true},
	})
	assertRefs(t, s.Refs, []ref{
		{Name: "compile", Kind: "method", Qualifier: "re", Line: 3, Col: 14, Selector: true},
		{Name: "called", Kind: "call", Line: 7, Col: 12},
	})
}
