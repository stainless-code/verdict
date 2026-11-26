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

// progressMark opens every progress line verdict writes for a person.
const progressMark = "» "

// TestRefusalMessagesFoldOutsideValues holds the one choke point: every
// refusal message in the tree (core.RefusalError, cli.UsageError, the
// loop's Refusal) is a constant or a core.Msgf call, every core.Msgf call
// and every printf wrapper that forwards to it gets a constant format, and
// every `»` progress line that carries a value is formatted by one of them.
// So no value from outside verdict reaches an `error:` line, or a progress
// line, without its line breaks folded. A [fmt.Sprintf], a concatenation
// with a variable, or a bare variable would let a path or a host reply
// carrying a newline forge a second `error:` line.
func TestRefusalMessagesFoldOutsideValues(t *testing.T) {
	t.Parallel()
	sites, violations := scanRefusalMessages(t, moduleRoot(t))
	if sites < 600 {
		t.Fatalf("scan found %d refusal message sites; the guard would pass vacuously", sites)
	}
	for _, v := range violations {
		t.Errorf("%s: build the text with core.Msgf (core.Trusted for verdict's own multi-line text)", v)
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
	writeFixture(t, root, "internal/cli/cli.go", `package cli
type UsageError struct{ Message string }
`)
	writeFixture(t, root, "internal/core/loop/loop.go", `package loopcore
type Refusal struct{ Code, Message string }
func bad(v string) *Refusal { return &Refusal{Message: v} }
`)
	writeFixture(t, root, "internal/verb/verb.go", `package verb

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/cli"
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

func refusef(code, format string, args ...any) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: core.Msgf(format, args...)}
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
		refusef("k", "wrapped %s", v),
		refusef("l", v),
		{Code: "m", Message: core.Msgf("wrapped %w", err)},
		{Code: "n", Message: core.Msgf("%*d", 3, 4)},
	}
}

func usage(v string) *cli.UsageError { return &cli.UsageError{Message: "unknown flag " + v} }

func progress(w io.Writer, v string) {
	fmt.Fprint(w, core.Msgf("» folded %s\n", v))
	fmt.Fprintln(w, "» constant")
	fmt.Fprintf(w, "» raw %s\n", v)
	fmt.Println("» joined " + v)
}
`)
	sites, violations := scanRefusalMessages(t, root)
	want := []string{
		"internal/core/loop/loop.go:3 Message: v",
		"internal/verb/verb.go:28 core.Msgf with a non-constant format: core.Msgf(format, v)",
		"internal/verb/verb.go:32 ref.Message = v",
		"internal/verb/verb.go:33 ref.Message += fmt.Sprintf(\" %s\", v)",
		"internal/verb/verb.go:34 ref.Message assigned from a multi-value call",
		"internal/verb/verb.go:36 Message: fmt.Sprintf(\"sprintf %s\", v)",
		"internal/verb/verb.go:37 Message: \"concat \" + v",
		"internal/verb/verb.go:38 Message: v",
		"internal/verb/verb.go:39 Message: err.Error()",
		"internal/verb/verb.go:40 core.Msgf with a non-constant format: core.Msgf(v)",
		"internal/verb/verb.go:41 positional RefusalError literal",
		"internal/verb/verb.go:43 refusef with a non-constant format: refusef(\"l\", v)",
		"internal/verb/verb.go:44 core.Msgf format uses %w or a * width or precision: \"wrapped %w\"",
		"internal/verb/verb.go:45 core.Msgf format uses %w or a * width or precision: \"%*d\"",
		"internal/verb/verb.go:49 Message: \"unknown flag \" + v",
		"internal/verb/verb.go:54 progress line formatted outside core.Msgf: fmt.Fprintf(w, \"» raw %s\\n\", v)",
		"internal/verb/verb.go:55 progress line concatenates a value: \"» joined \" + v",
	}
	if !slices.Equal(violations, want) {
		t.Fatalf("violations:\n%s\nwant:\n%s", strings.Join(violations, "\n"), strings.Join(want, "\n"))
	}
	if sites != 19 {
		t.Fatalf("sites = %d, want 19", sites)
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

// constDef is one declared constant: its value expression and the file it
// was declared in, whose imports resolve a qualified name inside it.
type constDef struct {
	expr  ast.Expr
	scope fileScope
}

// messageScan carries what the check needs across every package, keyed by
// import path: each package's constants, and its printf wrappers that
// forward to core.Msgf with the index of their format parameter.
type messageScan struct {
	fset       *token.FileSet
	consts     map[string]map[string]constDef
	wrappers   map[string]map[string]int
	root       string
	violations []string
	sites      int
}

// scanRefusalMessages checks every refusal message and folded call under
// root and returns how many message sites it checked and the violations.
func scanRefusalMessages(t *testing.T, root string) (int, []string) {
	t.Helper()
	scan := &messageScan{
		fset: token.NewFileSet(), root: root,
		consts: map[string]map[string]constDef{}, wrappers: map[string]map[string]int{},
	}
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
// path, and records each package's constants and wrappers on the way.
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
		scope := fileScope{pkg: pkg, imports: importNames(file)}
		s.collectConsts(scope, file)
		s.collectWrappers(scope, file)
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
func (s *messageScan) collectConsts(scope fileScope, file *ast.File) {
	if s.consts[scope.pkg] == nil {
		s.consts[scope.pkg] = map[string]constDef{}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			return true
		}
		for _, spec := range decl.Specs {
			s.addConsts(scope, spec)
		}
		return false
	})
}

func (s *messageScan) addConsts(scope fileScope, spec ast.Spec) {
	value, ok := spec.(*ast.ValueSpec)
	if !ok {
		return
	}
	for i, name := range value.Names {
		def := constDef{scope: scope}
		if i < len(value.Values) {
			def.expr = value.Values[i]
		}
		s.consts[scope.pkg][name.Name] = def
	}
}

// printfShape is a function's printf parameters: the string parameter just
// before a final `...any`, by index and name, and the variadic's name.
type printfShape struct {
	format, args string
	index        int
}

// shapeOf reports fn's printf shape, if it has one.
func shapeOf(fn *ast.FuncType) (printfShape, bool) {
	var names []string
	for _, field := range fn.Params.List {
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	fields := fn.Params.List
	if len(names) < 2 || len(fields) == 0 {
		return printfShape{}, false
	}
	if _, variadic := fields[len(fields)-1].Type.(*ast.Ellipsis); !variadic {
		return printfShape{}, false
	}
	n := len(names)
	return printfShape{format: names[n-2], args: names[n-1], index: n - 2}, true
}

// collectWrappers records each function that forwards its own printf
// parameters to core.Msgf: its callers owe it a constant format.
func (s *messageScan) collectWrappers(scope fileScope, file *ast.File) {
	if s.wrappers[scope.pkg] == nil {
		s.wrappers[scope.pkg] = map[string]int{}
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			s.addWrapper(scope, fn)
		}
	}
}

func (s *messageScan) addWrapper(scope fileScope, fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	if shape, ok := shapeOf(fn.Type); ok && s.forwardsToMsgf(scope, fn.Body, shape) {
		s.wrappers[scope.pkg][fn.Name.Name] = shape.index
	}
}

func (s *messageScan) forwardsToMsgf(scope fileScope, body *ast.BlockStmt, shape printfShape) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && s.isMsgf(scope, call.Fun) && forwards(call, shape) {
			found = true
		}
		return !found
	})
	return found
}

// forwards reports whether call passes shape's `format, args...` through as
// it came.
func forwards(call *ast.CallExpr, shape printfShape) bool {
	if !call.Ellipsis.IsValid() || len(call.Args) != 2 {
		return false
	}
	format, ok1 := call.Args[0].(*ast.Ident)
	args, ok2 := call.Args[1].(*ast.Ident)
	return ok1 && ok2 && format.Name == shape.format && args.Name == shape.args
}

// fileScope is one file's view: its import names and its package.
type fileScope struct {
	imports map[string]string
	pkg     string
}

// funcScope is a file's view inside one function: the printf parameters a
// wrapper may forward.
type funcScope struct {
	fileScope
	shape    printfShape
	hasShape bool
}

func (s *messageScan) checkFile(pkg string, file *ast.File) {
	scope := fileScope{pkg: pkg, imports: importNames(file)}
	for _, decl := range file.Decls {
		inner := funcScope{fileScope: scope}
		if fn, ok := decl.(*ast.FuncDecl); ok {
			inner.shape, inner.hasShape = shapeOf(fn.Type)
		}
		ast.Inspect(decl, func(n ast.Node) bool { return s.checkNode(inner, n) })
	}
}

// checkNode checks one node and reports whether to descend into it: a `»`
// concatenation is judged whole, so its parts are not judged again.
func (s *messageScan) checkNode(scope funcScope, n ast.Node) bool {
	switch n := n.(type) {
	case *ast.CompositeLit:
		s.checkComposite(scope.fileScope, n)
	case *ast.AssignStmt:
		s.checkAssign(scope.fileScope, n)
	case *ast.CallExpr:
		s.checkCall(scope, n)
	case *ast.BinaryExpr:
		return !s.checkProgressConcat(scope.fileScope, n)
	default:
	}
	return true
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

// refusalTypes are the refusal-like types whose Message a person reads on an
// `error:` or usage line, by type name and the packages that declare or
// alias it. The public client's own pkg/verdict.RefusalError decodes an
// envelope and is not one verdict raises.
var refusalTypes = map[string]map[string]bool{
	"RefusalError": {corePath: true, modulePath + "internal/cli": true},
	"UsageError":   {modulePath + "internal/cli": true},
	"Refusal":      {modulePath + "internal/core/loop": true},
}

// isRefusalType matches a refusal type by name inside its own package, and
// qualified by an import of it elsewhere, optionally behind a pointer.
func isRefusalType(scope fileScope, typ ast.Expr) bool {
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	switch typ := typ.(type) {
	case *ast.Ident:
		return refusalTypes[typ.Name][scope.pkg]
	case *ast.SelectorExpr:
		pkg, ok := typ.X.(*ast.Ident)
		return ok && refusalTypes[typ.Sel.Name][scope.imports[pkg.Name]]
	default:
		return false
	}
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
// constant, or a core.Msgf call (whose format [messageScan.checkCall]
// holds to a constant).
func (s *messageScan) folded(scope fileScope, expr ast.Expr) bool {
	if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok && s.isMsgf(scope, call.Fun) {
		return true
	}
	return s.constant(scope, expr)
}

// checkCall holds a core.Msgf call, and a call to a wrapper that forwards to
// it, to a constant format: go vet checks the verbs of a constant format,
// and a variable one would be a value folded nowhere. Inside a wrapper the
// format may be the wrapper's own, forwarded whole. A `»` progress line
// passed to any other formatter is flagged.
func (s *messageScan) checkCall(scope funcScope, call *ast.CallExpr) {
	name, index, folds := s.foldingCall(scope.fileScope, call.Fun)
	if !folds {
		s.checkProgressCall(scope.fileScope, call)
		return
	}
	if index >= len(call.Args) {
		return
	}
	format := call.Args[index]
	switch {
	case scope.hasShape && forwards(call, scope.shape):
	case !s.constant(scope.fileScope, format):
		s.flag(call, name+" with a non-constant format: "+s.text(call))
	case badVerbs(s.stringValue(scope.fileScope, format)):
		s.flag(call, name+" format uses %w or a * width or precision: "+s.text(format))
	}
}

// foldingCall resolves fun to core.Msgf or a wrapper of it, with the name a
// violation quotes and the format argument's index.
func (s *messageScan) foldingCall(scope fileScope, fun ast.Expr) (string, int, bool) {
	if s.isMsgf(scope, fun) {
		return "core.Msgf", 0, true
	}
	switch fun := fun.(type) {
	case *ast.Ident:
		index, ok := s.wrappers[scope.pkg][fun.Name]
		return fun.Name, index, ok
	case *ast.SelectorExpr:
		pkg := scope.pkg
		if x, ok := fun.X.(*ast.Ident); ok && scope.imports[x.Name] != "" {
			pkg = scope.imports[x.Name]
		}
		index, ok := s.wrappers[pkg][fun.Sel.Name]
		return fun.Sel.Name, index, ok
	default:
		return "", 0, false
	}
}

// badVerbs reports a verb Msgf cannot fold: %w (fmt.Sprintf has no error to
// wrap) and a `*` width or precision (fmt reads it from an int argument,
// which Msgf has wrapped).
func badVerbs(format string) bool {
	parts := strings.Split(strings.ReplaceAll(format, "%%", ""), "%")
	for _, part := range parts[1:] {
		if flags, verb := verbAt(part); verb == 'w' || strings.Contains(flags, "*") {
			return true
		}
	}
	return false
}

// verbAt splits the text after a `%` into its flags, width, and precision,
// and the verb that ends them (0 at the end of the format).
func verbAt(s string) (string, byte) {
	end := strings.IndexFunc(s, func(r rune) bool { return !strings.ContainsRune("+-# 0123456789.[]*", r) })
	if end < 0 {
		return s, 0
	}
	return s[:end], s[end]
}

// checkProgressCall flags a `»` progress format with a verb handed to any
// formatter but core.Msgf or a wrapper of it.
func (s *messageScan) checkProgressCall(scope fileScope, call *ast.CallExpr) {
	for _, arg := range call.Args {
		value := s.stringValue(scope, arg)
		if strings.HasPrefix(value, progressMark) && hasVerb(value) {
			s.flag(call, "progress line formatted outside core.Msgf: "+s.text(call))
			return
		}
	}
}

// checkProgressConcat flags a `»` progress line built by concatenating a
// value, and reports whether expr was such a line.
func (s *messageScan) checkProgressConcat(scope fileScope, expr *ast.BinaryExpr) bool {
	if expr.Op != token.ADD || !strings.HasPrefix(s.stringValue(scope, leftmost(expr)), progressMark) {
		return false
	}
	if !s.constant(scope, expr) {
		s.flag(expr, "progress line concatenates a value: "+s.text(expr))
	}
	return true
}

func leftmost(expr ast.Expr) ast.Expr {
	for {
		bin, ok := ast.Unparen(expr).(*ast.BinaryExpr)
		if !ok {
			return expr
		}
		expr = bin.X
	}
}

// hasVerb reports whether format holds a verb, not only `%%`.
func hasVerb(format string) bool {
	return strings.Contains(strings.ReplaceAll(format, "%%", ""), "%")
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
		_, ok := s.consts[scope.pkg][expr.Name]
		return ok
	case *ast.SelectorExpr:
		pkg, ok := expr.X.(*ast.Ident)
		if !ok {
			return false
		}
		_, ok = s.consts[scope.imports[pkg.Name]][expr.Sel.Name]
		return ok
	case *ast.BinaryExpr:
		return expr.Op == token.ADD && s.constant(scope, expr.X) && s.constant(scope, expr.Y)
	default:
		return false
	}
}

// stringValue is the value of a constant string expression, or "" when it is
// not one this scan can read.
func (s *messageScan) stringValue(scope fileScope, expr ast.Expr) string {
	switch expr := ast.Unparen(expr).(type) {
	case *ast.BasicLit:
		return literalValue(expr)
	case *ast.Ident:
		return s.constValue(scope.pkg, expr.Name)
	case *ast.SelectorExpr:
		pkg, ok := expr.X.(*ast.Ident)
		if !ok {
			return ""
		}
		return s.constValue(scope.imports[pkg.Name], expr.Sel.Name)
	case *ast.BinaryExpr:
		if expr.Op != token.ADD {
			return ""
		}
		return s.stringValue(scope, expr.X) + s.stringValue(scope, expr.Y)
	default:
		return ""
	}
}

// literalValue is a string literal's value, or "" for any other literal.
func literalValue(lit *ast.BasicLit) string {
	value, err := strconv.Unquote(lit.Value)
	if lit.Kind != token.STRING || err != nil {
		return ""
	}
	return value
}

func (s *messageScan) constValue(pkg, name string) string {
	def, ok := s.consts[pkg][name]
	if !ok || def.expr == nil {
		return ""
	}
	return s.stringValue(def.scope, def.expr)
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
