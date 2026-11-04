package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// refusalSkipDirs hold no Go emission sites: build output, dependencies, and
// fixtures. A testdata fixture stays free to name a fake code without
// registering it.
var refusalSkipDirs = map[string]bool{
	".git":         true,
	".pi":          true,
	".scratch":     true,
	"apps":         true,
	"bin":          true,
	"dist":         true,
	"evals":        true,
	"examples":     true,
	"node_modules": true,
	"testdata":     true,
	"vendor":       true,
}

// tsUnionLiteral matches one quoted code in the generated TypeScript union.
var tsUnionLiteral = regexp.MustCompile(`"([^"\\]*)"`)

// TestRefusalCodesMatchRegistry proves the refusal codes the tree emits and
// the codes the registry publishes are the same set, in both directions. The
// two sides come from different places on purpose: the registry is the shipped
// Go list cmd/tsgen renders from, while this test re-derives the set by walking
// every non-test source file for emission sites.
func TestRefusalCodesMatchRegistry(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	emitted := scanRefusalCodes(t, root)
	if len(emitted) == 0 {
		t.Fatal("scan found no refusal emission sites; the guard would pass vacuously")
	}
	registered := core.RefusalCodes()
	checkRegistryShape(t, registered)
	for _, code := range slices.Sorted(maps.Keys(emitted)) {
		if !slices.Contains(registered, code) {
			t.Errorf("refusal code %q is emitted at %s but missing from core.RefusalCodes()",
				code, strings.Join(emitted[code], ", "))
		}
	}
	// The other direction: a registered code no emission site carries is dead
	// weight in the published client union.
	checkIncludes(t, "tree's emission sites", slices.Sorted(maps.Keys(emitted)), "registered refusal code", registered)
}

// TestRefusalCodeUnionMatchesRegistry proves the generated TypeScript union is
// exactly the registry, so a TS consumer that switches exhaustively on
// `RefusalCode` cannot be missing a code the binary emits. It reads the
// checked-in generated file, which is the artifact a consumer compiles
// against, rather than the generator's output.
func TestRefusalCodeUnionMatchesRegistry(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	path := filepath.Join(root, "clients", "ts", "src", "refusal-codes.gen.ts")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	union := tsUnionCodes(string(raw))
	if len(union) == 0 {
		t.Fatal("generated union carries no codes; the guard would pass vacuously")
	}
	registered := core.RefusalCodes()
	checkSubset(t, "generated RefusalCode", union, "registered refusal code", registered)
	checkIncludes(t, "generated RefusalCode union", union, "registered refusal code", registered)
}

// TestHintTableCodesAreRegistered proves every exact code the hint table
// answers is a refusal code the binary can emit: a hint keyed on a misspelled
// code would never fire, and the operator would lose the remediation line.
func TestHintTableCodesAreRegistered(t *testing.T) {
	t.Parallel()
	hinted := core.ExactRefusalCodes()
	if len(hinted) == 0 {
		t.Fatal("ExactRefusalCodes returned nothing; the guard would pass vacuously")
	}
	checkSubset(t, "hint table code", hinted, "registered refusal code", core.RefusalCodes())
}

// tsUnionCodes extracts the quoted codes from a generated union source.
func tsUnionCodes(src string) []string {
	var codes []string
	for _, match := range tsUnionLiteral.FindAllStringSubmatch(src, -1) {
		codes = append(codes, match[1])
	}
	return codes
}

// checkRegistryShape proves the registry is sorted and free of duplicates, so
// the generated union is sorted and each code appears once.
func checkRegistryShape(t *testing.T, codes []string) {
	t.Helper()
	if len(codes) == 0 {
		t.Fatal("core.RefusalCodes() returned nothing")
	}
	if !slices.IsSorted(codes) {
		t.Error("core.RefusalCodes() is not sorted")
	}
	if compact := slices.Compact(slices.Clone(codes)); !slices.Equal(compact, codes) {
		t.Error("core.RefusalCodes() repeats a code")
	}
}

// scanRefusalCodes returns every refusal code sighted at an emission site,
// keyed by code, with each `file:line` it was seen at.
func scanRefusalCodes(t *testing.T, root string) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	return mergePackageCodes(fset, parsePackages(t, fset, root))
}

// mergePackageCodes folds every package's emission sites into one map.
func mergePackageCodes(fset *token.FileSet, packages map[string][]*ast.File) map[string][]string {
	sighted := map[string][]string{}
	for _, dir := range slices.Sorted(maps.Keys(packages)) {
		for code, at := range refusalCodesInPackage(fset, packages[dir]) {
			sighted[code] = append(sighted[code], at...)
		}
	}
	return sighted
}

// parsePackages parses every emission source under root, grouped by directory,
// which is the Go package boundary.
func parsePackages(t *testing.T, fset *token.FileSet, root string) map[string][]*ast.File {
	t.Helper()
	files, err := walkFiles(root)
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	packages := map[string][]*ast.File{}
	for _, path := range files {
		if isEmissionSource(path) {
			dir := filepath.Dir(path)
			packages[dir] = append(packages[dir], parseFile(t, fset, path))
		}
	}
	return packages
}

// parseFile parses one source file with the shared file set.
func parseFile(t *testing.T, fset *token.FileSet, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

// walkFiles lists every file under root, skipping the trees no emission site
// lives in.
func walkFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && refusalSkipDirs[entry.Name()] {
			return fs.SkipDir
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

// isEmissionSource reports whether path is a non-test Go source file.
func isEmissionSource(path string) bool {
	return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
}

// refusalCodesInPackage reports the refusal codes one package's sources carry
// at an emission site.
func refusalCodesInPackage(fset *token.FileSet, files []*ast.File) map[string][]string {
	consts := packageStringConsts(files)
	sighted := map[string][]string{}
	record := func(expr ast.Expr) {
		code, ok := refusalCodeOf(expr, consts)
		if !ok {
			return
		}
		sighted[code] = append(sighted[code], fset.Position(expr.Pos()).String())
	}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			recordEmission(n, record)
			return true
		})
	}
	return sighted
}

// recordEmission reports the expression an emission site names. An emission
// site is a `Code:` field, the first argument of a `refuse*` call, or an
// assigned `.Code` field.
func recordEmission(n ast.Node, record func(ast.Expr)) {
	switch node := n.(type) {
	case *ast.KeyValueExpr:
		if isCodeField(node.Key) {
			record(node.Value)
		}
	case *ast.CallExpr:
		if isRefuseCall(node) {
			record(node.Args[0])
		}
	case *ast.AssignStmt:
		recordCodeAssignments(node, record)
	default:
		// Every other node is not an emission site.
	}
}

// isCodeField reports whether key is the `Code` field of a struct literal.
func isCodeField(key ast.Expr) bool {
	ident, ok := key.(*ast.Ident)
	return ok && ident.Name == "Code"
}

// isRefuseCall reports whether call is a `refuse*` helper invoked with a code.
func isRefuseCall(call *ast.CallExpr) bool {
	return len(call.Args) > 0 && strings.HasPrefix(calleeName(call.Fun), "refuse")
}

// recordCodeAssignments reports the right-hand side of every `x.Code = ...`
// assignment.
func recordCodeAssignments(assign *ast.AssignStmt, record func(ast.Expr)) {
	for i, lhs := range assign.Lhs {
		selector, ok := lhs.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Code" || i >= len(assign.Rhs) {
			continue
		}
		record(assign.Rhs[i])
	}
}

// packageStringConsts maps every string constant or variable declared in the
// package to its value, so an emission site can name a code instead of
// repeating its literal.
func packageStringConsts(files []*ast.File) map[string]string {
	consts := map[string]string{}
	for _, file := range files {
		for _, spec := range constSpecs(file) {
			maps.Copy(consts, stringExprPairs(spec))
		}
	}
	return consts
}

// constSpecs returns the const and var value specs of one file.
func constSpecs(file *ast.File) []*ast.ValueSpec {
	var specs []*ast.ValueSpec
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok {
			return true
		}
		if isConstOrVar(decl.Tok) {
			specs = append(specs, valueSpecs(decl)...)
		}
		return false
	})
	return specs
}

// isConstOrVar reports whether a declaration kind can declare a string value.
func isConstOrVar(tok token.Token) bool {
	return tok == token.CONST || tok == token.VAR
}

// valueSpecs returns the value specs one declaration carries.
func valueSpecs(decl *ast.GenDecl) []*ast.ValueSpec {
	specs := make([]*ast.ValueSpec, 0, len(decl.Specs))
	for _, spec := range decl.Specs {
		if value, ok := spec.(*ast.ValueSpec); ok {
			specs = append(specs, value)
		}
	}
	return specs
}

// stringExprPairs maps the names a spec declares to their string literal
// values; a spec with a non-literal value contributes nothing.
func stringExprPairs(spec *ast.ValueSpec) map[string]string {
	if len(spec.Names) != len(spec.Values) {
		return nil
	}
	pairs := make(map[string]string, len(spec.Names))
	for i, name := range spec.Names {
		if value, ok := stringLiteral(spec.Values[i]); ok {
			pairs[name.Name] = value
		}
	}
	return pairs
}

// stringLiteral returns the unquoted value of a string literal expression.
func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// refusalCodeOf resolves one emission expression to a refusal code. Anything
// that is not a string literal or a package string constant is not a code: a
// JSON-RPC `Code: codeParse` is an integer, and `Code: err.Code` is a value the
// guard cannot see.
func refusalCodeOf(expr ast.Expr, consts map[string]string) (string, bool) {
	if value, ok := stringLiteral(expr); ok {
		return value, true
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	code, ok := consts[ident.Name]
	return code, ok
}

// calleeName returns the trailing name of a call's callee: "refuse" for both
// `refuse(...)` and `state.refuse(...)`.
func calleeName(fun ast.Expr) string {
	switch fn := fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	default:
		return ""
	}
}

// TestRefusalCodeScanForms pins the extraction rules against the shapes real
// emission sites take and the decoys that must stay out of the set, so the
// guard's blind spots are the documented ones rather than accidental.
func TestRefusalCodeScanForms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		want    []string
		sources []string
	}{
		{
			name:    "literal code field",
			sources: []string{"package p\n\nfunc f() error { return &RefusalError{Code: \"bad-usage\", Message: \"m\"} }"},
			want:    []string{"bad-usage"},
		},
		{
			name: "constant code field",
			sources: []string{
				"package p\n\nconst codeBadFlags = \"bad-flags\"\n",
				"package p\n\nfunc f() error { return &RefusalError{Code: codeBadFlags} }\n",
			},
			want: []string{"bad-flags"},
		},
		{
			name: "literal refuse call",
			sources: []string{
				"package p\n\nfunc (s *state) refuse(c string) error { return nil }\n\n" +
					"func f() error { return (&state{}).refuse(\"canceled\") }",
			},
			want: []string{"canceled"},
		},
		{
			name: "constant refuse call",
			sources: []string{
				"package p\n\nconst codeNeedsConfirm = \"needs-confirm\"\n",
				"package p\n\nfunc (s *state) refuse(c string) error { return nil }\n\n" +
					"func f() error { return (&state{}).refuse(codeNeedsConfirm) }\n",
			},
			want: []string{"needs-confirm"},
		},
		{
			name:    "assigned code field",
			sources: []string{"package p\n\ntype refusal struct{ Code string }\n\nfunc f(e *refusal) { e.Code = \"scope-escape\" }"},
			want:    []string{"scope-escape"},
		},
		{
			name: "json-rpc integer code is not a refusal code",
			sources: []string{
				"package p\n\nconst codeParse = -32700\n\ntype rpcError struct{ Code int }\n\n" +
					"func f() any { return rpcError{Code: codeParse} }",
			},
			want: nil,
		},
		{
			name:    "path constant is not a refusal code",
			sources: []string{"package p\n\nconst codesRelPath = \"clients/ts/src/refusal-codes.gen.ts\"\n"},
			want:    nil,
		},
		{
			name:    "forwarded code field is invisible",
			sources: []string{"package p\n\ntype refusal struct{ Code string }\n\nfunc f(dst, src *refusal) { dst.Code = src.Code }"},
			want:    nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := slices.Sorted(maps.Keys(refusalCodesInPackage(parsePackage(t, tc.sources))))
			if !slices.Equal(got, tc.want) {
				t.Errorf("refusalCodesInPackage = %v; want %v", got, tc.want)
			}
		})
	}
}

// parsePackage parses fixture sources as one package; the file names differ so
// cross-file constant resolution is exercised.
func parsePackage(t *testing.T, sources []string) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(sources))
	for i, src := range sources {
		file, err := parser.ParseFile(fset, "p"+strconv.Itoa(i)+".go", src, 0)
		if err != nil {
			t.Fatalf("parse fixture %d: %v", i, err)
		}
		files = append(files, file)
	}
	return fset, files
}
