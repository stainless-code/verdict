package indexcore

import (
	"strings"
	"testing"
)

// tsDeclSource is one TypeScript file carrying every declaration form the
// scanner records, written one line per element.
var tsDeclSource = strings.Join([]string{
	`// leading comment`,
	`import { helper, other as renamed } from './util';`,
	`import * as ns from './ns';`,
	`import def from './def';`,
	`const cjs = require('./cjs');`,
	`import './side';`,
	`export * from './re';`,
	`const dyn = () => import('./dyn');`,
	``,
	`export interface Shape { area(): number }`,
	`export type Alias = Shape | null;`,
	`export enum Color { Red, Green }`,
	`const arrow = (x: number) => x + 1;`,
	`export default class Widget extends Base {`,
	`  #secret = 1;`,
	`  constructor(public name: string) { super(name); }`,
	`  static make(): Widget { return new Widget('x'); }`,
	`  get area2(): number { return helper(this.name); }`,
	`  #privateMethod() { return this.#secret; }`,
	`  handler = () => renamed(1);`,
	`}`,
	`function plain(a: number) {`,
	`  const re = /ab+c/gi;`,
	"  const t = `total ${helper(a)} and ${`nested ${renamed(2)}`}`;",
	`  const q = a / 2 / 3;`,
	`  if (a) /x/.test('y');`,
	`  return ns.thing(a) + def(a) + cjs(a) + plain(a);`,
	`}`,
	`const el = <div className="k">{helper(el)}text note()</div>;`,
}, "\n")

// TestScanTypeScriptDeclarations proves every declaration form, its kind, its
// class, and its exported bit, plus every module edge the file writes.
func TestScanTypeScriptDeclarations(t *testing.T) {
	s := scanFile(t, langTypeScript, tsDeclSource)
	assertDecls(t, s.Decls, []decl{
		{Name: "cjs", Kind: "const", Line: 5},
		{Name: "dyn", Kind: "func", Line: 8},
		{Name: "Shape", Kind: "type", Line: 10, Exported: true},
		{Name: "Alias", Kind: "type", Line: 11, Exported: true},
		{Name: "Color", Kind: "type", Line: 12, Exported: true},
		{Name: "arrow", Kind: "func", Line: 13},
		{Name: "Widget", Kind: "type", Line: 14, Exported: true},
		{Name: "constructor", Kind: "method", Recv: "Widget", Line: 16, Exported: true},
		{Name: "make", Kind: "method", Recv: "Widget", Line: 17, Exported: true},
		{Name: "area2", Kind: "method", Recv: "Widget", Line: 18, Exported: true},
		{Name: "#privateMethod", Kind: "method", Recv: "Widget", Line: 19},
		{Name: "handler", Kind: "method", Recv: "Widget", Line: 20, Exported: true},
		{Name: "plain", Kind: "func", Line: 22},
		{Name: "el", Kind: "const", Line: 29},
	})
	assertImports(t, s.Imports, []importEntry{
		{Path: "./util", Name: "helper"},
		{Path: "./util", Name: "renamed"},
		{Path: "./ns", Name: "ns"},
		{Path: "./def", Name: "def"},
		{Path: "./cjs", Name: "cjs"},
		{Path: "./side"},
		{Path: "./re"},
		{Path: "./dyn"},
	})
}

// TestScanTypeScriptCallSites proves the call and attribute sites of the same
// file, with the position each one carries.
func TestScanTypeScriptCallSites(t *testing.T) {
	s := scanFile(t, langTypeScript, tsDeclSource)
	assertRefs(t, s.Refs, []ref{
		{Name: "require", Kind: KindCall, Line: 5, Col: 13},
		{Name: "Widget", Kind: KindCall, Line: 17, Col: 38},
		{Name: "helper", Kind: KindCall, Line: 18, Col: 32},
		{Name: "renamed", Kind: KindCall, Line: 20, Col: 19},
		{Name: "helper", Kind: KindCall, Line: 24, Col: 22},
		{Name: "renamed", Kind: KindCall, Line: 24, Col: 49},
		{Name: "test", Kind: KindMethod, Line: 26, Col: 14, Selector: true},
		{Name: "thing", Kind: KindMethod, Line: 27, Col: 13, Selector: true},
		{Name: "def", Kind: KindCall, Line: 27, Col: 24},
		{Name: "cjs", Kind: KindCall, Line: 27, Col: 33},
		{Name: "plain", Kind: KindCall, Line: 27, Col: 42},
		{Name: "helper", Kind: KindCall, Line: 29, Col: 32},
	})
}

// TestScanTypeScriptTemplateNesting proves a template inside an interpolation
// is still tokenized as code, so a call three levels deep is a call site.
func TestScanTypeScriptTemplateNesting(t *testing.T) {
	src := strings.Join([]string{
		"const t = `a ${outer(1)} ${`b ${middle(2)} ${`c ${deep(3)}`}`}`;",
		"const o = `x ${pick({k: 1}, also(2))} y`;",
		"next(4);",
	}, "\n")
	s := scanFile(t, langTypeScript, src)
	assertRefs(t, s.Refs, []ref{
		{Name: "outer", Kind: KindCall, Line: 1, Col: 16},
		{Name: "middle", Kind: KindCall, Line: 1, Col: 33},
		{Name: "deep", Kind: KindCall, Line: 1, Col: 51},
		{Name: "pick", Kind: KindCall, Line: 2, Col: 16},
		{Name: "also", Kind: KindCall, Line: 2, Col: 29},
		{Name: "next", Kind: KindCall, Line: 3, Col: 1},
	})
}

// TestScanTypeScriptRegexVersusDivision proves a slash after a value divides,
// a slash after a control paren starts a regex, and neither desynchronizes the
// tokens that follow.
func TestScanTypeScriptRegexVersusDivision(t *testing.T) {
	src := strings.Join([]string{
		`const q = total / count;`,
		`helper(q);`,
		`if (ok) /x/.test(q);`,
		`const flags = /[/]\//.test(q) ? a : b;`,
		`const half = q / 2 / 3;`,
		`const ratio = 3 / share(1) / 2;`,
		`const scale = box(7) / two() / 3;`,
		`last(half, ratio, scale);`,
	}, "\n")
	s := scanFile(t, langTypeScript, src)
	assertRefs(t, s.Refs, []ref{
		{Name: "helper", Kind: KindCall, Line: 2, Col: 1},
		{Name: "test", Kind: KindMethod, Line: 3, Col: 13, Selector: true},
		{Name: "test", Kind: KindMethod, Line: 4, Col: 23, Selector: true},
		{Name: "share", Kind: KindCall, Line: 6, Col: 19},
		{Name: "box", Kind: KindCall, Line: 7, Col: 15},
		{Name: "two", Kind: KindCall, Line: 7, Col: 24},
		{Name: "last", Kind: KindCall, Line: 8, Col: 1},
	})
}

// TestScanTypeScriptJSX proves markup text is never read as code while the
// expressions inside it still are.
func TestScanTypeScriptJSX(t *testing.T) {
	src := strings.Join([]string{
		`const el = <div className="k" onClick={() => handler(1)}>text note() {inner(2)}</div>;`,
		`const f = <T,>(x: T) => helper(x);`,
		`const y = <Foo>bar;`,
		`after();`,
	}, "\n")
	s := scanFile(t, langTypeScript, src)
	assertRefs(t, s.Refs, []ref{
		{Name: "handler", Kind: KindCall, Line: 1, Col: 46},
		{Name: "inner", Kind: KindCall, Line: 1, Col: 71},
		{Name: "helper", Kind: KindCall, Line: 2, Col: 25},
		{Name: "after", Kind: KindCall, Line: 4, Col: 1},
	})
}

// TestScanTypeScriptTypeBodies proves a signature inside an interface, type
// literal, or enum is never read as a call site.
func TestScanTypeScriptTypeBodies(t *testing.T) {
	src := strings.Join([]string{
		`interface Shape { area(): number; draw(): void }`,
		`type Fn = { call(x: number): void };`,
		`enum Color { Red, Green }`,
		`real();`,
	}, "\n")
	s := scanFile(t, langTypeScript, src)
	assertRefs(t, s.Refs, []ref{{Name: "real", Kind: KindCall, Line: 4, Col: 1}})
}

// TestScanTypeScriptPrivateAndOptionalChain proves a `#private` name survives
// as one token and optional chaining still names the callee.
func TestScanTypeScriptPrivateAndOptionalChain(t *testing.T) {
	src := strings.Join([]string{
		`class Store {`,
		`  #load(k) { return this.#get(k)?.value; }`,
		`  #get(k) { return k; }`,
		`}`,
		`const v = store?.load?.(1);`,
	}, "\n")
	s := scanFile(t, langTypeScript, src)
	assertDecls(t, s.Decls, []decl{
		{Name: "Store", Kind: "type", Line: 1},
		{Name: "#load", Kind: "method", Recv: "Store", Line: 2},
		{Name: "#get", Kind: "method", Recv: "Store", Line: 3},
		{Name: "v", Kind: "const", Line: 5},
	})
	assertRefs(t, s.Refs, []ref{
		{Name: "#get", Kind: KindMethod, Line: 2, Col: 26, Selector: true},
		{Name: "load", Kind: KindMethod, Line: 5, Col: 18, Selector: true},
	})
}

// TestScanTypeScriptObjectMethodIsACallSite pins the one shape a scanner
// cannot tell from a call: an object literal's method shorthand has the same
// lexical form.
func TestScanTypeScriptObjectMethodIsACallSite(t *testing.T) {
	s := scanFile(t, langTypeScript, "const o = { m() { return inner(); } };\n")
	assertRefs(t, s.Refs, []ref{
		{Name: "m", Kind: KindCall, Line: 1, Col: 13},
		{Name: "inner", Kind: KindCall, Line: 1, Col: 26},
	})
}

// TestScanTypeScriptRefCap proves the per-file cap truncates and says so.
func TestScanTypeScriptRefCap(t *testing.T) {
	s := parseShard("blob", []byte("a(); b(); c();\n"), langTypeScript, 2)
	if len(s.Refs) != 2 || !s.RefsTruncated {
		t.Fatalf("refs = %+v truncated %v; want 2 refs, truncated", s.Refs, s.RefsTruncated)
	}
	if s.Language != string(langTypeScript) {
		t.Errorf("language = %q; want %q", s.Language, langTypeScript)
	}
}

// FuzzScanTypeScript proves the scanner survives any bytes, records only
// positions inside the file, and terminates.
func FuzzScanTypeScript(f *testing.F) {
	f.Add(tsDeclSource)
	f.Add("const a = `x${`y${z(1)}`}`; // tail\n")
	f.Add("const el = <div a={b(c)}>t(1)</div>;")
	f.Add("if (a) /[/]\\(/.test(b) / 2;")
	f.Add("class A { #b() { return this.#b(); } }")
	f.Fuzz(func(t *testing.T, src string) {
		s := parseShard("blob", []byte(src), langTypeScript, 64)
		assertScannedPositions(t, &s, src)
	})
}
