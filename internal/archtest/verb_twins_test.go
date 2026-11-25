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
// the others: the frozen usage synopsis, the verb table (which also supplies
// the "did you mean" hint, the arity gate, and the handlers), the verbs
// dispatch serves before the table, and the shell completion list. It is the
// twin check for a new verb that lands on one surface only.
func TestVerbRegistriesAgree(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	usage := cliVerbs(t, root)
	rows := tableRows(t, root)
	table := tableNames(rows)
	served := servedBeforeTable(t, root)
	completion := completionVerbs(t, root)
	checkUnique(t, "cli verb", usage)
	checkUnique(t, "table verb", table)
	checkUnique(t, "served verb", served)
	checkUnique(t, "completion verb", completion)
	checkIncludes(t, "table", table, "cli verb", usage)
	checkIncludes(t, "usage synopsis", usage, "table verb", table)
	checkIncludes(t, "completion", completion, "cli verb", without(usage, names(hiddenVerbs)))
	checkIncludes(t, "usage synopsis", usage, "completion verb", completion)
	checkIncludes(t, "dispatch", served, "handlerless table verb", without(handlerless(rows), names(hiddenVerbs)))
	checkIncludes(t, "handlerless table verbs", handlerless(rows), "served verb", served)
	for _, verb := range names(hiddenVerbs) {
		if !slices.Contains(handlerless(rows), verb) {
			t.Errorf("hidden verb %q must be a table row with no handler: run() serves it before the table", verb)
		}
		if slices.Contains(served, verb) || slices.Contains(completion, verb) {
			t.Errorf("hidden verb %q must stay out of dispatch's served verbs and completion", verb)
		}
	}
}

// TestVerbTableMatchesSurfaces keeps the verb table, the usage synopsis, and
// the MCP tool registry one list: the table names exactly the synopsis verbs,
// and every table verb outside the documented MCP omissions is an MCP tool,
// while every MCP tool outside the query surface is a table verb. A verb
// added to the table alone, or to a surface alone, fails here.
func TestVerbTableMatchesSurfaces(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	table := tableNames(tableRows(t, root))
	usage := cliVerbs(t, root)
	tools := without(mcpToolNames(t, root), names(mcpQuerySurface))
	checkIncludes(t, "table", table, "usage verb", usage)
	checkIncludes(t, "usage synopsis", usage, "table verb", table)
	checkIncludes(t, "mcp tools", tools, "table verb outside the mcp omissions", without(table, names(mcpOmittedVerbs)))
	checkIncludes(t, "table", table, "mcp tool", tools)
	checkIncludes(t, "table", table, "mcp omission", names(mcpOmittedVerbs))
}

// tableRow is one verbTable row: the verb and whether it carries a handler.
type tableRow struct {
	name   string
	hasRun bool
}

// tableRows reads the verb table's rows from the source: each is a keyed
// {Name, Arity, Run} literal, and a nil Run marks a verb served before the
// table.
func tableRows(t *testing.T, root string) []tableRow {
	t.Helper()
	dir := filepath.Join(root, "cmd", "verdict")
	consts := dirStringConsts(t, dir)
	var rows []tableRow
	for _, decl := range parseGo(t, filepath.Join(dir, "verbs.go")).Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			rows = append(rows, tableSpecRows(t, spec, consts)...)
		}
	}
	if len(rows) == 0 {
		t.Fatal("verbTable declares no verbs; the verb registry did not parse")
	}
	return rows
}

func tableSpecRows(t *testing.T, spec ast.Spec, consts map[string]string) []tableRow {
	t.Helper()
	value, ok := spec.(*ast.ValueSpec)
	if !ok || len(value.Names) != 1 || len(value.Values) != 1 || value.Names[0].Name != "verbTable" {
		return nil
	}
	lit, ok := value.Values[0].(*ast.CompositeLit)
	if !ok {
		t.Fatal("verbTable is not a composite literal")
	}
	rows := make([]tableRow, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		rows = append(rows, tableRowOf(t, elt, consts))
	}
	return rows
}

// tableRowOf reads one row's Name and whether its Run is anything but nil.
func tableRowOf(t *testing.T, elt ast.Expr, consts map[string]string) tableRow {
	t.Helper()
	row, ok := elt.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("verbTable row %T is not a composite literal", elt)
	}
	var out tableRow
	for _, field := range row.Elts {
		key, value := rowField(t, field)
		switch key {
		case "Name":
			out.name = verbLiteral(t, value, consts)
		case "Run":
			handler, isIdent := value.(*ast.Ident)
			out.hasRun = !isIdent || handler.Name != "nil"
		}
	}
	return out
}

// rowField splits one keyed field of a verbTable row.
func rowField(t *testing.T, field ast.Expr) (string, ast.Expr) {
	t.Helper()
	kv, ok := field.(*ast.KeyValueExpr)
	if !ok {
		t.Fatalf("verbTable row field %T is not keyed", field)
	}
	key, ok := kv.Key.(*ast.Ident)
	if !ok {
		t.Fatalf("verbTable row key %T is not a field name", kv.Key)
	}
	return key.Name, kv.Value
}

func tableNames(rows []tableRow) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.name
	}
	return out
}

func handlerless(rows []tableRow) []string {
	var out []string
	for _, row := range rows {
		if !row.hasRun {
			out = append(out, row.name)
		}
	}
	return out
}

// servedBeforeTable reads the verbs dispatch serves before the table: the
// case labels of its switch.
func servedBeforeTable(t *testing.T, root string) []string {
	t.Helper()
	dir := filepath.Join(root, "cmd", "verdict")
	verbs := switchCaseVerbs(t, filepath.Join(dir, "main.go"), "dispatch", dirStringConsts(t, dir))
	if len(verbs) == 0 {
		t.Fatal("dispatch serves no verbs before the table; the registry did not parse")
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
