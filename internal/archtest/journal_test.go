package archtest

import (
	"go/ast"
	"maps"
	"path/filepath"
	"slices"
	"testing"

	journalcore "github.com/stainless-code/verdict/internal/core/journal"
)

// TestJournalSingleHookInCLI enforces DESIGN §1.5 assertion 1: cmd/verdict has
// exactly one call to journal.Guard inside dispatch, and every verb dispatcher
// (readVerbs, dispatchMutating, dispatchStoreVerbs, dispatchLocal,
// dispatchFetch) is reachable only through dispatchVerb inside Guard's closure.
func TestJournalSingleHookInCLI(t *testing.T) {
	t.Parallel()
	mainPath := filepath.Join(moduleRoot(t), "cmd", "verdict", "main.go")
	file := parseGo(t, mainPath)
	guardCalls := findGuardCalls(file)
	if len(guardCalls) != 1 {
		t.Fatalf("cmd/verdict/main.go has %d journal.Guard calls, want exactly 1", len(guardCalls))
	}
	dispatchFn := findFuncInFile(t, file, "dispatch")
	if len(findGuardCalls(dispatchFn.Body)) != 1 {
		t.Fatal("journal.Guard call is not inside dispatch")
	}
	assertDirectDispatchersNotInDispatch(t, dispatchFn.Body)
	assertDispatchVerbOnlyInsideGuardClosure(t, file, guardCalls[0])
}

func findFuncInFile(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func findGuardCalls(node ast.Node) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "Guard" {
			calls = append(calls, call)
		}
		return true
	})
	return calls
}

func assertDirectDispatchersNotInDispatch(t *testing.T, body *ast.BlockStmt) {
	t.Helper()
	forbidden := map[string]bool{
		"readVerbs":          true,
		"dispatchMutating":   true,
		"dispatchStoreVerbs": true,
		"dispatchLocal":      true,
		"dispatchFetch":      true,
	}
	ast.Inspect(body, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if ok && forbidden[ident.Name] {
			t.Errorf("dispatch references %s directly outside dispatchVerb", ident.Name)
		}
		return true
	})
}

func assertDispatchVerbOnlyInsideGuardClosure(t *testing.T, file *ast.File, guardCall *ast.CallExpr) {
	t.Helper()
	if len(guardCall.Args) != 2 {
		t.Fatalf("journal.Guard has %d args, want 2", len(guardCall.Args))
	}
	closure, ok := guardCall.Args[1].(*ast.FuncLit)
	if !ok {
		t.Fatalf("journal.Guard arg 2 is %T, want func literal closure", guardCall.Args[1])
	}
	totalCalls := countIdentCalls(file, "dispatchVerb")
	closureCalls := countIdentCalls(closure.Body, "dispatchVerb")
	if totalCalls != 1 || closureCalls != 1 {
		t.Fatalf("dispatchVerb calls total=%d inside Guard closure=%d, want 1 and 1", totalCalls, closureCalls)
	}
}

func countIdentCalls(node ast.Node, name string) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == name {
			count++
		}
		return true
	})
	return count
}

// TestJournalVerbTableIsTotal enforces DESIGN §1.5 assertion 2: every verb in
// usageText()'s synopsis has one row in journal.VerbTable with a non-empty
// reason, and no stale row remains in VerbTable.
func TestJournalVerbTableIsTotal(t *testing.T) {
	t.Parallel()
	usage := cliVerbs(t, moduleRoot(t))
	assertAllUsageVerbsClassified(t, usage)
	for _, verb := range slices.Sorted(maps.Keys(journalcore.VerbTable)) {
		if !slices.Contains(usage, verb) {
			t.Errorf("journal.VerbTable carries unknown/stale verb %q not in usageText()", verb)
		}
	}
}

func assertAllUsageVerbsClassified(t *testing.T, usage []string) {
	t.Helper()
	for _, verb := range usage {
		pol, ok := journalcore.VerbTable[verb]
		if !ok {
			t.Errorf("CLI verb %q is missing from journal.VerbTable", verb)
		} else if pol.Reason == "" {
			t.Errorf("journal.VerbTable[%q] has an empty Reason", verb)
		}
	}
}

// TestJournalWritingSetIsLiteral enforces DESIGN §1.5 assertion 4: growing or
// shrinking the set of verbs that write to journal.jsonl is a deliberate,
// visible diff against a literal list.
func TestJournalWritingSetIsLiteral(t *testing.T) {
	t.Parallel()
	var got []string
	for verb, pol := range journalcore.VerbTable {
		if pol.Journals {
			got = append(got, verb)
		}
	}
	slices.Sort(got)
	want := []string{
		"adopt",
		"calibrate",
		"drain",
		"fetch",
		"fetch-local",
		"fix",
		"ingest",
		"post",
		"record",
		"report",
		"run",
		"threads",
		"triage",
		"verify",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("journaling verb set = %v, want %v", got, want)
	}
}

type parsedMCPTool struct {
	name      string
	mutates   string
	hasBundle bool
}

// TestJournalMCPRegistryParity enforces DESIGN §1.5 assertion 3: every tool in
// internal/mcp/tools.go with mutates==localWrites and Repo+Pr required maps to
// Journals: true in journal.VerbTable, and every noLocalWrites tool maps to
// Journals: false. It also pins that internal/mcp/server.go routes tool
// invocations through journal.Guard.
func TestJournalMCPRegistryParity(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	tools := parseMCPToolSpecs(t, filepath.Join(root, "internal", "mcp", "tools.go"))
	if len(tools) < 10 {
		t.Fatalf("parsed only %d MCP tools; tools.go shape changed", len(tools))
	}
	for _, tool := range tools {
		assertMCPToolJournalParity(t, tool)
	}
	journalFile := parseGo(t, filepath.Join(root, "internal", "mcp", "journal.go"))
	if len(findGuardCalls(journalFile)) != 1 {
		t.Fatalf("internal/mcp/journal.go has %d journal.Guard calls, want 1", len(findGuardCalls(journalFile)))
	}
	serverFile := parseGo(t, filepath.Join(root, "internal", "mcp", "server.go"))
	callFn := findFuncInFile(t, serverFile, "call")
	if countMethodCalls(callFn.Body, "invokeJournaled") != 1 {
		t.Fatal("internal/mcp/server.go (*Server).call does not route through invokeJournaled")
	}
}

// checkQueryToolJournalParity pins the dashboard query tools' journal
// posture: they are noLocalWrites tools with no VerbTable row, so
// journal.Guard never writes for them. A row that appears must keep the
// non-journaling posture.
func checkQueryToolJournalParity(t *testing.T, tool parsedMCPTool, pol journalcore.VerbPolicy, ok bool) {
	t.Helper()
	if ok && pol.Journals {
		t.Errorf("MCP query tool %q (noLocalWrites) has VerbTable.Journals=true, want false", tool.name)
	}
}

func assertMCPToolJournalParity(t *testing.T, tool parsedMCPTool) {
	t.Helper()
	verb := journalcore.CanonicalVerb(tool.name)
	pol, ok := journalcore.VerbTable[verb]
	if _, queryTool := mcpQuerySurface[tool.name]; queryTool {
		checkQueryToolJournalParity(t, tool, pol, ok)
		return
	}
	if !ok {
		t.Errorf("MCP tool %q (verb %q) missing from journal.VerbTable", tool.name, verb)
		return
	}
	switch tool.mutates {
	case "localWrites":
		if tool.hasBundle != pol.Journals {
			t.Errorf("MCP tool %q (localWrites, hasBundle=%v) has VerbTable.Journals=%v",
				tool.name, tool.hasBundle, pol.Journals)
		}
	case "noLocalWrites":
		if pol.Journals {
			t.Errorf("MCP tool %q (noLocalWrites) has VerbTable.Journals=true, want false", tool.name)
		}
	default:
		t.Errorf("MCP tool %q has unrecognized mutates=%q", tool.name, tool.mutates)
	}
}

func parseMCPToolSpecs(t *testing.T, path string) []parsedMCPTool {
	t.Helper()
	file := parseGo(t, path)
	var tools []parsedMCPTool
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isIdentType(lit.Type, "toolSpec") {
			return true
		}
		tools = append(tools, extractMCPToolSpec(t, lit))
		return false
	})
	return tools
}

func extractMCPToolSpec(t *testing.T, lit *ast.CompositeLit) parsedMCPTool {
	t.Helper()
	var spec parsedMCPTool
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		applyToolSpecField(t, &spec, key.Name, kv.Value)
	}
	return spec
}

func applyToolSpecField(t *testing.T, spec *parsedMCPTool, field string, val ast.Expr) {
	t.Helper()
	switch field {
	case "name":
		if s, ok := stringLiteral(val); ok {
			spec.name = s
		}
	case "required":
		spec.hasBundle = requiredCarriesBundle(val)
	case "meta":
		spec.mutates = extractMutatesClass(val)
	}
}

func requiredCarriesBundle(expr ast.Expr) bool {
	if isIdentType(expr, "requiredRepoPr") {
		return true
	}
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return false
	}
	return hasIdentElement(lit.Elts, "argRepo") && hasIdentElement(lit.Elts, "argPr")
}

func hasIdentElement(elts []ast.Expr, name string) bool {
	for _, elt := range elts {
		if isIdentType(elt, name) {
			return true
		}
	}
	return false
}

func extractMutatesClass(expr ast.Expr) string {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return ""
	}
	for _, elt := range lit.Elts {
		if val, ok := keyIdentValue(elt, "mutates"); ok {
			return val
		}
	}
	return ""
}

func keyIdentValue(elt ast.Expr, field string) (string, bool) {
	kv, ok := elt.(*ast.KeyValueExpr)
	if !ok || !isIdentType(kv.Key, field) {
		return "", false
	}
	val, ok := kv.Value.(*ast.Ident)
	if !ok {
		return "", false
	}
	return val.Name, true
}

func isIdentType(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

func countMethodCalls(node ast.Node, method string) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == method {
			count++
		}
		return true
	})
	return count
}
