package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// corePath is the package that owns RefusalError and Msgf.
const corePath = modulePath + "internal/core"

// TestRefusalMessagesFoldOutsideValues holds the one choke point: every
// RefusalError message in the tree is a constant or a core.Msgf call, so no
// value from outside verdict reaches an `error:` line without its line
// breaks folded. A [fmt.Sprintf], a concatenation with a variable, or a bare
// variable would let a path or a host reply carrying a newline forge a
// second `error:` line.
func TestRefusalMessagesFoldOutsideValues(t *testing.T) {
	t.Parallel()
	sites, violations := scanRefusalMessages(t, moduleRoot(t))
	if sites < 500 {
		t.Fatalf("scan found %d refusal message sites; the guard would pass vacuously", sites)
	}
	for _, v := range violations {
		t.Errorf("%s: build the refusal message with core.Msgf (core.Trusted for verdict's own multi-line text)", v)
	}
}

// TestRefusalMessageGuardFires proves the scan flags each unfolded shape and
// passes each folded one, on a fixture tree laid out like the module.
func TestRefusalMessageGuardFires(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixture(t, root, "internal/core/core.go", `package core
type RefusalError struct{ Code, Message string }
type Trusted string
func Msgf(format string, args ...any) string { return format }
const MsgShared = "shared %s"
func ok(v string) *RefusalError { return &RefusalError{Message: Msgf("own %s", v)} }
`)
	writeFixture(t, root, "internal/verb/verb.go", `package verb

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
)

const msgLocal = "local"

func good(v string, ref *core.RefusalError) []*core.RefusalError {
	ref.Message += " (" + msgLocal + ")"
	ref.Message = ref.Message[:4]
	return []*core.RefusalError{
		{Code: "a", Message: "literal"},
		{Code: "b", Message: msgLocal + " and " + core.MsgShared},
		{Code: "c", Message: core.Msgf(core.MsgShared, v)},
		{Code: "d", Message: core.Msgf("%s", core.Trusted(ref.Message))},
	}
}

func refusef(format string, args ...any) *core.RefusalError {
	return &core.RefusalError{Message: core.Msgf(format, args...)}
}

func notWrapper(format, v string) *core.RefusalError {
	return &core.RefusalError{Message: core.Msgf(format, v)}
}

func bad(v string, err error, ref *core.RefusalError) []*core.RefusalError {
	ref.Message = v
	ref.Message += fmt.Sprintf(" %s", v)
	ref.Message, _ = pair(v)
	return []*core.RefusalError{
		{Code: "e", Message: fmt.Sprintf("sprintf %s", v)},
		{Code: "f", Message: "concat " + v},
		{Code: "g", Message: v},
		{Code: "h", Message: err.Error()},
		{Code: "i", Message: core.Msgf(v)},
		{"j", "positional"},
	}
}
`)
	sites, violations := scanRefusalMessages(t, root)
	want := []string{
		"internal/verb/verb.go:27 Message: core.Msgf(format, v)",
		"internal/verb/verb.go:31 ref.Message = v",
		"internal/verb/verb.go:32 ref.Message += fmt.Sprintf(\" %s\", v)",
		"internal/verb/verb.go:33 ref.Message assigned from a multi-value call",
		"internal/verb/verb.go:35 Message: fmt.Sprintf(\"sprintf %s\", v)",
		"internal/verb/verb.go:36 Message: \"concat \" + v",
		"internal/verb/verb.go:37 Message: v",
		"internal/verb/verb.go:38 Message: err.Error()",
		"internal/verb/verb.go:39 Message: core.Msgf(v)",
		"internal/verb/verb.go:40 positional RefusalError literal",
	}
	if !slices.Equal(violations, want) {
		t.Fatalf("violations:\n%s\nwant:\n%s", strings.Join(violations, "\n"), strings.Join(want, "\n"))
	}
	if sites != 15 {
		t.Fatalf("sites = %d, want 15", sites)
	}
}

func writeFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// messageScan carries what the check needs across every package: each
// package's constant names keyed by import path.
type messageScan struct {
	fset       *token.FileSet
	consts     map[string]map[string]bool
	root       string
	violations []string
	sites      int
}

// scanRefusalMessages checks every RefusalError message under root and
// returns how many sites it checked and the ones that are not folded.
func scanRefusalMessages(t *testing.T, root string) (int, []string) {
	t.Helper()
	scan := &messageScan{fset: token.NewFileSet(), root: root, consts: map[string]map[string]bool{}}
	parsed := scan.parseTree(t)
	for _, pkg := range slices.Sorted(maps.Keys(parsed)) {
		for _, file := range parsed[pkg] {
			scan.checkFile(pkg, file)
		}
	}
	slices.Sort(scan.violations)
	return scan.sites, scan.violations
}

// parseTree parses every non-test source under the root, grouped by import
// path, and records each package's constants on the way.
func (s *messageScan) parseTree(t *testing.T) map[string][]*ast.File {
	t.Helper()
	files, err := walkFiles(s.root)
	if err != nil {
		t.Fatalf("walk %s: %v", s.root, err)
	}
	parsed := map[string][]*ast.File{}
	for _, path := range slices.DeleteFunc(files, func(p string) bool { return !isEmissionSource(p) }) {
		file, err := parser.ParseFile(s.fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		pkg := s.importPath(filepath.Dir(path))
		parsed[pkg] = append(parsed[pkg], file)
		s.collectConsts(pkg, file)
	}
	return parsed
}

func (s *messageScan) importPath(dir string) string {
	rel, err := filepath.Rel(s.root, dir)
	if err != nil || rel == "." {
		return strings.TrimSuffix(modulePath, "/")
	}
	return modulePath + filepath.ToSlash(rel)
}

// collectConsts records every constant a file declares, at any scope: a
// name the check accepts is one the compiler holds fixed.
func (s *messageScan) collectConsts(pkg string, file *ast.File) {
	if s.consts[pkg] == nil {
		s.consts[pkg] = map[string]bool{}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			return true
		}
		for _, spec := range decl.Specs {
			s.addConstNames(pkg, spec)
		}
		return false
	})
}

func (s *messageScan) addConstNames(pkg string, spec ast.Spec) {
	if value, ok := spec.(*ast.ValueSpec); ok {
		for _, name := range value.Names {
			s.consts[pkg][name.Name] = true
		}
	}
}

// fileScope is one file's view: its import names and its package.
type fileScope struct {
	imports map[string]string
	pkg     string
}

func (s *messageScan) checkFile(pkg string, file *ast.File) {
	scope := fileScope{pkg: pkg, imports: importNames(file)}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			s.checkComposite(scope, n)
		case *ast.AssignStmt:
			s.checkAssign(scope, n)
		default:
		}
		return true
	})
}

// checkComposite checks a refusal literal, and the refusal elements of a
// slice, array, or map literal, which drop the type name
// (`[]*core.RefusalError{{Code: …}}`).
func (s *messageScan) checkComposite(scope fileScope, lit *ast.CompositeLit) {
	if isRefusalType(scope, lit.Type) {
		s.checkLiteral(scope, lit)
	}
	if !isRefusalType(scope, elementType(lit.Type)) {
		return
	}
	for _, elt := range lit.Elts {
		if inner := elidedLiteral(elt); inner != nil {
			s.checkLiteral(scope, inner)
		}
	}
}

func importNames(file *ast.File) map[string]string {
	names := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		names[name] = path
	}
	return names
}

// refusalPackages declare the type: core owns it and cli aliases it. The
// public client's own pkg/verdict.RefusalError decodes an envelope and is
// not a refusal verdict raises.
var refusalPackages = map[string]bool{corePath: true, modulePath + "internal/cli": true}

// isRefusalType matches `RefusalError` inside core or cli, and
// `core.RefusalError` or `cli.RefusalError` elsewhere, optionally behind a
// pointer.
func isRefusalType(scope fileScope, typ ast.Expr) bool {
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	switch typ := typ.(type) {
	case *ast.Ident:
		return typ.Name == "RefusalError" && refusalPackages[scope.pkg]
	case *ast.SelectorExpr:
		pkg, ok := typ.X.(*ast.Ident)
		return ok && typ.Sel.Name == "RefusalError" && refusalPackages[scope.imports[pkg.Name]]
	default:
		return false
	}
}

// elementType is the element type of a slice, array, or map type, or nil.
func elementType(typ ast.Expr) ast.Expr {
	switch typ := typ.(type) {
	case *ast.ArrayType:
		return typ.Elt
	case *ast.MapType:
		return typ.Value
	default:
		return nil
	}
}

// elidedLiteral is the typeless literal an element holds, past a map key
// and an `&`, or nil.
func elidedLiteral(elt ast.Expr) *ast.CompositeLit {
	if kv, ok := elt.(*ast.KeyValueExpr); ok {
		elt = kv.Value
	}
	if addr, ok := elt.(*ast.UnaryExpr); ok && addr.Op == token.AND {
		elt = addr.X
	}
	if inner, ok := elt.(*ast.CompositeLit); ok && inner.Type == nil {
		return inner
	}
	return nil
}

func (s *messageScan) checkLiteral(scope fileScope, lit *ast.CompositeLit) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			s.flag(lit, "positional RefusalError literal")
			return
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Message" {
			s.checkMessage(scope, kv.Value, kv, "Message: ")
		}
	}
}

// checkMessage counts one message site and flags it unless it is folded;
// label prefixes the expression in the violation.
func (s *messageScan) checkMessage(scope fileScope, expr ast.Expr, at ast.Node, label string) {
	s.sites++
	if !s.folded(scope, expr) {
		s.flag(at, label+s.text(expr))
	}
}

// checkAssign holds `x.Message = …` and `x.Message += …` to the literal's
// rule. The AST cannot tell a refusal from another struct with a Message
// field, so the rule covers them all; truncating a message in place keeps
// what it held and passes. A Message among a call's several results has no
// expression to check, so it is flagged.
func (s *messageScan) checkAssign(scope fileScope, assign *ast.AssignStmt) {
	if assign.Tok != token.ASSIGN && assign.Tok != token.ADD_ASSIGN {
		return
	}
	for i, lhs := range assign.Lhs {
		sel, ok := lhs.(*ast.SelectorExpr)
		switch {
		case !ok || sel.Sel.Name != "Message":
		case len(assign.Lhs) != len(assign.Rhs):
			s.flag(assign, s.text(sel)+" assigned from a multi-value call")
		case !s.truncates(sel, assign.Rhs[i]):
			s.checkMessage(scope, assign.Rhs[i], assign, s.text(sel)+" "+assign.Tok.String()+" ")
		}
	}
}

// truncates reports whether rhs slices the field it is assigned to.
func (s *messageScan) truncates(field *ast.SelectorExpr, rhs ast.Expr) bool {
	slice, ok := rhs.(*ast.SliceExpr)
	return ok && s.text(slice.X) == s.text(field)
}

// folded reports whether expr is a message built at the choke point: a
// constant, or a core.Msgf call whose format is a constant or is the
// `format, args...` a printf-style wrapper forwards, whose callers go vet
// then checks as it checks Msgf's.
func (s *messageScan) folded(scope fileScope, expr ast.Expr) bool {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok || !s.isMsgf(scope, call.Fun) {
		return s.constant(scope, expr)
	}
	if len(call.Args) == 0 {
		return false
	}
	if id, ok := call.Args[0].(*ast.Ident); ok && id.Name == "format" && call.Ellipsis.IsValid() {
		return true
	}
	return s.constant(scope, call.Args[0])
}

func (s *messageScan) isMsgf(scope fileScope, fun ast.Expr) bool {
	switch fun := fun.(type) {
	case *ast.Ident:
		return scope.pkg == corePath && fun.Name == "Msgf"
	case *ast.SelectorExpr:
		pkg, ok := fun.X.(*ast.Ident)
		return ok && scope.imports[pkg.Name] == corePath && fun.Sel.Name == "Msgf"
	default:
		return false
	}
}

// constant reports whether expr is a string the compiler holds fixed: a
// literal, a declared constant, or a sum of them.
func (s *messageScan) constant(scope fileScope, expr ast.Expr) bool {
	switch expr := ast.Unparen(expr).(type) {
	case *ast.BasicLit:
		return expr.Kind == token.STRING
	case *ast.Ident:
		return s.consts[scope.pkg][expr.Name]
	case *ast.SelectorExpr:
		pkg, ok := expr.X.(*ast.Ident)
		return ok && s.consts[scope.imports[pkg.Name]][expr.Sel.Name]
	case *ast.BinaryExpr:
		return expr.Op == token.ADD && s.constant(scope, expr.X) && s.constant(scope, expr.Y)
	default:
		return false
	}
}

func (s *messageScan) flag(node ast.Node, what string) {
	pos := s.fset.Position(node.Pos())
	rel, err := filepath.Rel(s.root, pos.Filename)
	if err != nil {
		rel = pos.Filename
	}
	s.violations = append(s.violations, filepath.ToSlash(rel)+":"+strconv.Itoa(pos.Line)+" "+what)
}

func (s *messageScan) text(expr ast.Expr) string { return types.ExprString(expr) }
