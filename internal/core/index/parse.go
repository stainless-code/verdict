package indexcore

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strconv"
	"strings"
)

// shard is one content-addressed parse result. It carries no file path: the
// bytes depend on the blob alone, so a rename reuses the shard. RefsPerFile
// records the parse cap the shard was built under: a shard parsed with a
// lower cap holds a truncated reference list, so it is not reusable when the
// cap rises.
type shard struct {
	SchemaVersion string        `json:"schema_version"`
	Blob          string        `json:"blob"`
	Package       string        `json:"package,omitempty"`
	Error         string        `json:"error,omitempty"`
	Imports       []importEntry `json:"imports,omitempty"`
	Decls         []decl        `json:"decls,omitempty"`
	Refs          []ref         `json:"refs,omitempty"`
	RefsPerFile   int           `json:"refs_per_file"`
	RefsTruncated bool          `json:"refs_truncated,omitempty"`
}

// importEntry is one import and the name it is bound to in the file.
type importEntry struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// decl is one package-level declaration.
type decl struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Recv     string `json:"recv,omitempty"`
	Line     int    `json:"line"`
	Exported bool   `json:"exported,omitempty"`
}

// ref is one identifier use that could name a declaration.
type ref struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Qualifier  string `json:"qualifier,omitempty"`
	Line       int    `json:"line"`
	Col        int    `json:"col"`
	Selector   bool   `json:"selector,omitempty"`
	Unresolved bool   `json:"unresolved,omitempty"`
}

// Reference kinds.
const (
	KindCall     = "call"
	KindMethod   = "method"
	KindTypeRef  = "type-ref"
	KindValueRef = "value-ref"
	KindEmbed    = "embed"
	blankName    = "_"
	spaceRune    = 0x20
	delRune      = 0x7f
)

// builtins are the predeclared names an identifier can carry without naming a
// declaration; recording them would bury real rows in noise.
var builtins = map[string]bool{
	"true": true, "false": true, "nil": true, "iota": true,
	"len": true, "cap": true, "make": true, "new": true, "append": true,
	"copy": true, "delete": true, "panic": true, "recover": true,
	"print": true, "println": true, "close": true, "complex": true,
	"real": true, "imag": true, "min": true, "max": true, "clear": true,
	"bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true,
	"int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true, "uint": true, "uint8": true,
	"uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"any": true,
}

// parseShard parses one blob into its shard. A file that does not parse is a
// shard carrying the parser's error: it stays content-addressed and truthful
// instead of disappearing from the coverage count.
func parseShard(blob string, src []byte, maxRefs int) shard {
	out := shard{SchemaVersion: "1", Blob: blob, RefsPerFile: maxRefs}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "indexed.go", src, 0)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	c := &collector{fset: fset, out: &out, aliases: map[string]string{}, skip: map[token.Pos]bool{}, maxRefs: maxRefs}
	c.out.Package = file.Name.Name
	c.skip[file.Name.Pos()] = true
	c.collectImports(file)
	c.collectDecls(file)
	c.collectTypeRefs(file)
	c.walk(file)
	return out
}

// collector walks one parsed file and records declarations and references.
type collector struct {
	fset       *token.FileSet
	out        *shard
	aliases    map[string]string
	pkgObjects map[*ast.Object]bool //nolint:staticcheck // go/parser object resolution is the syntax-only local/package discriminator
	skip       map[token.Pos]bool
	maxRefs    int
}

// collectImports records the file's imports and their bound names.
//
//nolint:revive // one branch per AST node kind keeps the mapping auditable
func (c *collector) collectImports(file *ast.File) {
	for _, imp := range file.Imports {
		ipath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(ipath)
		if imp.Name != nil {
			name = imp.Name.Name
			c.skip[imp.Name.Pos()] = true
			c.skip[imp.Name.End()] = true
		}
		c.out.Imports = append(c.out.Imports, importEntry{Path: ipath, Name: name})
		if name != blankName && name != "." {
			c.aliases[name] = ipath
		}
	}
}

// collectDecls records every package-level declaration.
//
//nolint:revive // one branch per AST node kind keeps the mapping auditable
func (c *collector) collectDecls(file *ast.File) {
	c.pkgObjects = map[*ast.Object]bool{} //nolint:staticcheck // see collector.pkgObjects
	if file.Scope != nil {
		for _, obj := range file.Scope.Objects {
			c.pkgObjects[obj] = true
		}
	}
	for _, d := range file.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			kind := "func"
			recv := ""
			if decl.Recv != nil && len(decl.Recv.List) == 1 {
				kind = "method"
				recv = typeName(decl.Recv.List[0].Type)
			}
			c.recordDecl(decl.Name, kind, recv)
		case *ast.GenDecl:
			c.recordGenDecl(decl)
		default:
		}
	}
}

// recordGenDecl records the names of one var, const, or type group.
//
//nolint:revive // one branch per AST node kind keeps the mapping auditable
func (c *collector) recordGenDecl(decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			c.recordDecl(s.Name, "type", "")
		case *ast.ValueSpec:
			for _, name := range s.Names {
				kind := "var"
				if decl.Tok == token.CONST {
					kind = "const"
				}
				c.recordDecl(name, kind, "")
			}
		default:
		}
	}
}

// recordDecl adds one declaration and marks its name as handled.
func (c *collector) recordDecl(name *ast.Ident, kind, recv string) {
	c.skip[name.Pos()] = true
	c.out.Decls = append(c.out.Decls, decl{
		Name: name.Name, Kind: kind, Recv: recv,
		Line: c.fset.Position(name.Pos()).Line, Exported: ast.IsExported(name.Name),
	})
}

// typeName returns the base name of a receiver type expression.
func typeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.IndexExpr:
		return typeName(t.X)
	case *ast.IndexListExpr:
		return typeName(t.X)
	default:
		return ""
	}
}

// collectTypeRefs records the type references in every declaration signature.
func (c *collector) collectTypeRefs(file *ast.File) {
	for _, d := range file.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			c.collectFuncTypes(decl)
		case *ast.GenDecl:
			c.collectGenTypes(decl)
		default:
		}
	}
}

// collectFuncTypes records one function declaration's type references.
func (c *collector) collectFuncTypes(decl *ast.FuncDecl) {
	if decl.Recv != nil {
		for _, f := range decl.Recv.List {
			c.typeExpr(f.Type, true)
		}
	}
	c.funcType(decl.Type)
}

// collectGenTypes records one var, const, or type group's type references.
func (c *collector) collectGenTypes(decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			c.fieldList(s.TypeParams)
			c.typeExpr(s.Type, true)
		case *ast.ValueSpec:
			c.typeExpr(s.Type, true)
		default:
		}
	}
}

// funcType records a function signature's type references.
func (c *collector) funcType(ft *ast.FuncType) {
	if ft == nil {
		return
	}
	c.fieldList(ft.TypeParams)
	c.fieldList(ft.Params)
	c.fieldList(ft.Results)
}

// fieldList records one parameter, result, or type-parameter list.
func (c *collector) fieldList(list *ast.FieldList) {
	if list == nil {
		return
	}
	for _, f := range list.List {
		for _, name := range f.Names {
			c.skip[name.Pos()] = true
		}
		c.typeExpr(f.Type, true)
	}
}

// typeExpr records type references below one type expression. It is one
// branch per AST node kind, which keeps the mapping auditable.
//
//nolint:gocognit,revive,cyclop // one branch per AST node kind keeps the mapping auditable
func (c *collector) typeExpr(expr ast.Expr, record bool) {
	if expr == nil {
		return
	}
	switch t := expr.(type) {
	case *ast.Ident:
		c.skip[t.Pos()] = true
		if record && t.Name != blankName && !c.isLocal(t) {
			c.record(t.Name, KindTypeRef, t.Pos(), "", false, false)
		}
	case *ast.SelectorExpr:
		c.skip[t.Sel.Pos()] = true
		if x, ok := t.X.(*ast.Ident); ok {
			c.skip[x.Pos()] = true
			if _, ok := c.aliases[x.Name]; ok && !c.isLocal(x) {
				if record {
					c.record(t.Sel.Name, KindTypeRef, t.Sel.Pos(), x.Name, true, false)
				}
				return
			}
		}
		if record {
			c.record(t.Sel.Name, KindTypeRef, t.Sel.Pos(), "", true, true)
		}
	case *ast.ParenExpr:
		c.typeExpr(t.X, record)
	case *ast.StarExpr:
		c.typeExpr(t.X, record)
	case *ast.ArrayType:
		c.typeExpr(t.Elt, record)
	case *ast.MapType:
		c.typeExpr(t.Key, record)
		c.typeExpr(t.Value, record)
	case *ast.ChanType:
		c.typeExpr(t.Value, record)
	case *ast.Ellipsis:
		c.typeExpr(t.Elt, record)
	case *ast.FuncType:
		c.funcType(t)
	case *ast.StructType:
		c.structFields(t.Fields, record)
	case *ast.InterfaceType:
		c.structFields(t.Methods, record)
	case *ast.IndexExpr:
		c.typeExpr(t.X, record)
		c.typeExpr(t.Index, true)
	case *ast.IndexListExpr:
		c.typeExpr(t.X, record)
		for _, index := range t.Indices {
			c.typeExpr(index, true)
		}
	case *ast.UnaryExpr:
		c.typeExpr(t.X, record)
	case *ast.BinaryExpr:
		c.typeExpr(t.X, record)
		c.typeExpr(t.Y, record)
	default:
	}
}

// structFields records struct or interface fields, separating embeds.
//
//nolint:revive // one branch per AST node kind keeps the mapping auditable
func (c *collector) structFields(list *ast.FieldList, record bool) {
	if list == nil {
		return
	}
	for _, f := range list.List {
		for _, name := range f.Names {
			c.skip[name.Pos()] = true
		}
		if len(f.Names) == 0 {
			if record {
				c.embedExpr(f.Type)
			}
			c.typeExpr(f.Type, false)
			continue
		}
		c.typeExpr(f.Type, record)
	}
}

// embedExpr records one embedded type name.
func (c *collector) embedExpr(expr ast.Expr) {
	switch t := expr.(type) {
	case *ast.Ident:
		if c.isLocal(t) {
			return
		}
		c.record(t.Name, KindEmbed, t.Pos(), "", false, false)
	case *ast.SelectorExpr:
		c.embedSelector(t)
	case *ast.StarExpr:
		c.embedExpr(t.X)
	case *ast.IndexExpr:
		c.embedExpr(t.X)
	case *ast.IndexListExpr:
		c.embedExpr(t.X)
	default:
	}
}

// embedSelector records one embedded qualified type through an import edge,
// or an unresolved method-shaped field.
func (c *collector) embedSelector(t *ast.SelectorExpr) {
	if x, ok := t.X.(*ast.Ident); ok {
		if _, ok := c.aliases[x.Name]; ok && !c.isLocal(x) {
			c.record(t.Sel.Name, KindEmbed, t.Sel.Pos(), x.Name, true, false)
			return
		}
	}
	c.record(t.Sel.Name, KindEmbed, t.Sel.Pos(), "", true, true)
}

// walk records calls, selectors, and value references across the file.
//
//nolint:revive // one branch per AST node kind keeps the mapping auditable
func (c *collector) walk(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			c.callee(node.Fun, false)
			c.markArgs(node.Fun)
		case *ast.SelectorExpr:
			c.selector(node)
		case *ast.CompositeLit:
			c.typeExpr(node.Type, true)
		case *ast.TypeAssertExpr:
			c.typeExpr(node.Type, true)
		case *ast.KeyValueExpr:
			if key, ok := node.Key.(*ast.Ident); ok {
				c.skip[key.Pos()] = true
			}
		case *ast.LabeledStmt:
			c.skip[node.Label.Pos()] = true
		case *ast.BranchStmt:
			if node.Label != nil {
				c.skip[node.Label.Pos()] = true
			}
		case *ast.Ident:
			c.ident(node)
		default:
		}
		return true
	})
}

// markArgs marks any type expressions carried inside a callee shape.
func (c *collector) markArgs(expr ast.Expr) {
	switch f := expr.(type) {
	case *ast.IndexExpr:
		c.typeExpr(f.Index, true)
	case *ast.IndexListExpr:
		for _, index := range f.Indices {
			c.typeExpr(index, true)
		}
	default:
	}
}

// callee records one call target.
func (c *collector) callee(expr ast.Expr, generic bool) {
	switch f := expr.(type) {
	case *ast.Ident:
		c.calleeIdent(f, generic)
	case *ast.SelectorExpr:
		c.calleeSelector(f, generic)
	case *ast.IndexExpr:
		c.callee(f.X, true)
	case *ast.IndexListExpr:
		c.callee(f.X, true)
	case *ast.ParenExpr:
		c.callee(f.X, generic)
	case *ast.TypeAssertExpr:
		c.typeExpr(f.Type, true)
	default:
	}
}

// calleeIdent records a bare call target, ignoring builtins and names the
// function scope shadows.
func (c *collector) calleeIdent(f *ast.Ident, generic bool) {
	c.skip[f.Pos()] = true
	if !builtins[f.Name] && f.Name != blankName && !c.isLocal(f) {
		c.record(f.Name, KindCall, f.Pos(), "", false, generic)
	}
}

// calleeSelector records a qualified call target through an import edge, or a
// method call on a value.
func (c *collector) calleeSelector(f *ast.SelectorExpr, generic bool) {
	c.skip[f.Sel.Pos()] = true
	if x, ok := f.X.(*ast.Ident); ok {
		c.skip[x.Pos()] = true
		if _, ok := c.aliases[x.Name]; ok && !c.isLocal(x) {
			c.record(f.Sel.Name, KindCall, f.Sel.Pos(), x.Name, true, generic)
			return
		}
	}
	c.record(f.Sel.Name, KindMethod, f.Sel.Pos(), "", true, true)
}

// selector records one non-call selector use.
func (c *collector) selector(expr *ast.SelectorExpr) {
	if c.skip[expr.Sel.Pos()] {
		return
	}
	c.skip[expr.Sel.Pos()] = true
	if x, ok := expr.X.(*ast.Ident); ok {
		c.skip[x.Pos()] = true
		if _, ok := c.aliases[x.Name]; ok && !c.isLocal(x) {
			c.record(expr.Sel.Name, KindValueRef, expr.Sel.Pos(), x.Name, true, false)
			return
		}
	}
	c.record(expr.Sel.Name, KindValueRef, expr.Sel.Pos(), "", true, true)
}

// ident records one bare identifier value use.
func (c *collector) ident(expr *ast.Ident) {
	if c.skip[expr.Pos()] || expr.Name == blankName || builtins[expr.Name] {
		return
	}
	if c.isLocal(expr) {
		return
	}
	c.record(expr.Name, KindValueRef, expr.Pos(), "", false, false)
}

// isLocal reports whether ident names a function-scope declaration: a local
// variable, a parameter, or a function-local type. The parser resolves those
// objects and leaves package-level names in the file scope, so shadowing is
// visible without type checking.
func (c *collector) isLocal(ident *ast.Ident) bool {
	return ident.Obj != nil && !c.pkgObjects[ident.Obj]
}

// record appends one reference under the per-file cap.
func (c *collector) record(name, kind string, pos token.Pos, qualifier string, selector, unresolved bool) {
	if c.out.RefsTruncated || len(c.out.Refs) >= c.maxRefs {
		c.out.RefsTruncated = true
		return
	}
	position := c.fset.Position(pos)
	c.out.Refs = append(c.out.Refs, ref{
		Name: name, Kind: kind, Qualifier: qualifier, Line: position.Line, Col: position.Column,
		Selector: selector, Unresolved: unresolved,
	})
}

// importPathFor returns the import path bound to qualifier in one shard.
func importPathFor(s *shard, qualifier string) (string, bool) {
	for _, imp := range s.Imports {
		if imp.Name == qualifier {
			return imp.Path, true
		}
	}
	return "", false
}

// quotePath returns a human-safe rendering of a repository path: a path with
// control bytes or fence material is quoted, so one row can never break the
// line-oriented output contract.
func quotePath(p string) string {
	if !strings.ContainsFunc(p, unsafePathRune) {
		return p
	}
	return strconv.Quote(p)
}

// unsafePathRune reports bytes that must not reach the human stream raw.
func unsafePathRune(r rune) bool {
	return r < spaceRune || r == delRune || r == '`' || r == '\\'
}
