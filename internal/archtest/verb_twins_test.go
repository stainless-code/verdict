package archtest

import (
	"go/ast"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// hiddenVerbs are served before command parsing and omitted from completion on
// purpose, with the reason.
var hiddenVerbs = map[string]string{
	"mcp": "the stdio transport: run() serves it before splitCommand, and completion hides it",
}

// TestVerbRegistriesAgree fails when a verb reaches one CLI registry and not
// the others: the frozen usage synopsis, the "did you mean" hint, the dispatch
// tables, and the shell completion list. It is the twin check for a new verb
// that lands on one surface only.
func TestVerbRegistriesAgree(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	usage := cliVerbs(t, root)
	hint := hintVerbs(t, root)
	dispatch := dispatchVerbs(t, root)
	completion := completionVerbs(t, root)
	checkUnique(t, "cli verb", usage)
	checkUnique(t, "hint verb", hint)
	checkUnique(t, "dispatch verb", dispatch)
	checkUnique(t, "completion verb", completion)
	checkIncludes(t, "hint", hint, "cli verb", usage)
	checkIncludes(t, "usage synopsis", usage, "hint verb", hint)
	checkIncludes(t, "dispatch", dispatch, "cli verb", without(usage, names(hiddenVerbs)))
	checkIncludes(t, "usage synopsis", usage, "dispatch verb", dispatch)
	checkIncludes(t, "completion", completion, "cli verb", without(usage, names(hiddenVerbs)))
	checkIncludes(t, "usage synopsis", usage, "completion verb", completion)
	for _, verb := range names(hiddenVerbs) {
		if !slices.Contains(usage, verb) {
			t.Errorf("hidden verb %q is not in the usage synopsis", verb)
		}
		if slices.Contains(dispatch, verb) || slices.Contains(completion, verb) {
			t.Errorf("hidden verb %q must stay out of the dispatch tables and completion", verb)
		}
	}
}

func hintVerbs(t *testing.T, root string) []string {
	t.Helper()
	consts := dirStringConsts(t, filepath.Join(root, "cmd", "verdict"))
	hint, ok := consts["verbsHint"]
	if !ok {
		t.Fatal("cmd/verdict declares no verbsHint constant; the hint registry moved")
	}
	return strings.Split(hint, "|")
}

func dispatchVerbs(t *testing.T, root string) []string {
	t.Helper()
	dir := filepath.Join(root, "cmd", "verdict")
	path := filepath.Join(dir, "main.go")
	consts := dirStringConsts(t, dir)
	verbs := readVerbMapKeys(t, path, consts)
	for _, name := range []string{"dispatch", "dispatchMutating", "dispatchFetch", "dispatchLocal", "dispatchStoreVerbs"} {
		verbs = append(verbs, switchCaseVerbs(t, path, name, consts)...)
	}
	if len(verbs) == 0 {
		t.Fatal("cmd/verdict dispatches no verbs; the dispatch registry did not parse")
	}
	return verbs
}

func readVerbMapKeys(t *testing.T, path string, consts map[string]string) []string {
	t.Helper()
	var verbs []string
	for _, decl := range parseGo(t, path).Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			verbs = append(verbs, readVerbSpec(t, spec, consts)...)
		}
	}
	if len(verbs) == 0 {
		t.Fatal("readVerbs declares no verbs; the read registry did not parse")
	}
	return verbs
}

func readVerbSpec(t *testing.T, spec ast.Spec, consts map[string]string) []string {
	t.Helper()
	value, ok := spec.(*ast.ValueSpec)
	if !ok || len(value.Names) != 1 || len(value.Values) != 1 || value.Names[0].Name != "readVerbs" {
		return nil
	}
	lit, ok := value.Values[0].(*ast.CompositeLit)
	if !ok {
		t.Fatal("readVerbs is not a composite literal")
	}
	var verbs []string
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			verbs = append(verbs, verbLiteral(t, kv.Key, consts))
		}
	}
	return verbs
}

func switchCaseVerbs(t *testing.T, path, name string, consts map[string]string) []string {
	t.Helper()
	var verbs []string
	ast.Inspect(functionDecl(t, path, name).Body, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			verbs = append(verbs, verbLiteral(t, expr, consts))
		}
		return true
	})
	return verbs
}

func verbLiteral(t *testing.T, expr ast.Expr, consts map[string]string) string {
	t.Helper()
	verb, ok := stringExpr(t, expr, consts)
	if !ok {
		t.Fatalf("verb expression %T is neither a string literal nor a known constant", expr)
	}
	return verb
}

func completionVerbs(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "scripts", "verdict-complete.sh")
	lines := readLines(t, path)
	start := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, "_verdict_verbs()") })
	if start < 0 {
		t.Fatalf("%s: no _verdict_verbs function", path)
	}
	const prefix = "printf '%s\\n' "
	for _, line := range lines[start+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "}" {
			break
		}
		if verbs, ok := strings.CutPrefix(trimmed, prefix); ok {
			return strings.Fields(verbs)
		}
	}
	t.Fatalf("%s: _verdict_verbs prints no verb list", path)
	return nil
}

func dirStringConsts(t *testing.T, dir string) map[string]string {
	t.Helper()
	consts := make(map[string]string)
	for _, name := range goFileNames(t, dir) {
		maps.Copy(consts, fileStringConsts(t, parseGo(t, filepath.Join(dir, name)), consts))
	}
	if len(consts) == 0 {
		t.Fatalf("%s declares no string constants", dir)
	}
	return consts
}

func fileStringConsts(t *testing.T, file *ast.File, consts map[string]string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, decl := range file.Decls {
		maps.Copy(out, declStringConsts(t, decl, consts))
	}
	return out
}

func declStringConsts(t *testing.T, decl ast.Decl, consts map[string]string) map[string]string {
	t.Helper()
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.CONST {
		return nil
	}
	out := make(map[string]string)
	for _, spec := range gen.Specs {
		if name, text, ok := stringConst(t, spec, consts); ok {
			out[name] = text
		}
	}
	return out
}

func stringConst(t *testing.T, spec ast.Spec, consts map[string]string) (string, string, bool) {
	t.Helper()
	value, ok := spec.(*ast.ValueSpec)
	if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
		return "", "", false
	}
	text, ok := stringExpr(t, value.Values[0], consts)
	if !ok {
		return "", "", false
	}
	return value.Names[0].Name, text, true
}

// stringExpr evaluates a string literal, a known constant, or a concatenation
// of either.
func stringExpr(t *testing.T, expr ast.Expr, consts map[string]string) (string, bool) {
	t.Helper()
	switch e := expr.(type) {
	case *ast.BasicLit:
		return unquoted(t, e)
	case *ast.Ident:
		value, ok := consts[e.Name]
		return value, ok
	case *ast.BinaryExpr:
		return concatenated(t, e, consts)
	default:
		return "", false
	}
}

func unquoted(t *testing.T, lit *ast.BasicLit) (string, bool) {
	t.Helper()
	if lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		t.Fatalf("unquote %s: %v", lit.Value, err)
	}
	return value, true
}

func concatenated(t *testing.T, expr *ast.BinaryExpr, consts map[string]string) (string, bool) {
	t.Helper()
	if expr.Op != token.ADD {
		return "", false
	}
	left, leftOK := stringExpr(t, expr.X, consts)
	right, rightOK := stringExpr(t, expr.Y, consts)
	return left + right, leftOK && rightOK
}
