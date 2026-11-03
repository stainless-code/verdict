package core_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCoreSourcesAvoidProcessIO proves core code never touches stdout,
// stderr, [os.Exit], argv, or the environment directly: those are adapter
// concerns.
func TestCoreSourcesAvoidProcessIO(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return checkProcessIO(t, path)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// processIO maps the forbidden os selectors to their reason.
var processIO = map[string]string{
	"Stdout":  "stdout is an adapter stream",
	"Stderr":  "stderr is an adapter stream",
	"Stdin":   "stdin is an adapter stream",
	"Exit":    "only cmd/verdict/main exits via [os.Exit]",
	"Getenv":  "the environment is injected through Runtime.Env",
	"Environ": "the environment is injected through Runtime.Env",
	"Args":    "arguments are parsed by the adapter",
}

// checkProcessIO parses one core source file and fails on process I/O.
//
//nolint:revive // the AST walk reads clearer inline than behind another helper
func checkProcessIO(t *testing.T, path string) error {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "os" {
			return true
		}
		if reason, bad := processIO[sel.Sel.Name]; bad {
			t.Errorf("%s uses os.%s (%s)", path, sel.Sel.Name, reason)
		}
		return true
	})
	return nil
}

// TestCoreSourceFilesExist is a guard against an empty walk silently
// passing the process-I/O test.
func TestCoreSourceFilesExist(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			n++
		}
	}
	if n == 0 {
		t.Fatal("no core source files found")
	}
}
