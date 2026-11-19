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
