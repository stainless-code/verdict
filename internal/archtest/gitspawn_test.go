package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// gitSpawnSourceDirs are the module's Go source trees; the root's own Go
// files are scanned beside them.
var gitSpawnSourceDirs = []string{"bench", "cmd", "internal", "pkg"}

// gitHelperDir is the one package that may name the git binary in an argv:
// every verdict-internal git call is built by gitcmd.Argv, which makes it
// hook-free, fsmonitor-free, and textconv-free and names the repository.
const gitHelperDir = "internal/gitcmd"

// gitNamingCalls are the calls whose "git" argument names the tool in text —
// a missing-tool check, a failure line — and spawns nothing.
var gitNamingCalls = []string{"ExitFailure", "checkTool", "LookPath"}

// gitLabelConsts are the constants whose "git" value is a label, never an
// argv element.
var gitLabelConsts = map[string]string{
	"fetchedViaGit": "the demo's source label for a locally read diff",
}

// TestGitSpawnsGoThroughTheHelper fails when non-test code outside
// internal/gitcmd writes a "git" literal anywhere it could become an argv: a
// slice element, an exec argument, a constant or variable, a call argument
// other than a tool-naming one. A new git call therefore cannot skip the
// shared helper, and with it the hook, filter, and fsmonitor lockdown on PR
// content.
func TestGitSpawnsGoThroughTheHelper(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	var bad []string
	for _, path := range gitSpawnSources(t, root) {
		bad = append(bad, unroutedGitLiterals(t, root, path)...)
	}
	if len(bad) > 0 {
		t.Errorf("git spawned outside %s (build the argv with gitcmd.Argv and the env with gitcmd.Env):\n%s",
			gitHelperDir, strings.Join(bad, "\n"))
	}
}

// TestGitSpawnGuardFires proves the guard flags a hand-built git argv, a
// constant that would carry one, and an exec of git, and lets a tool-naming
// call and a map key through.
func TestGitSpawnGuardFires(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "internal", "fixture", "spawn.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package fixture

import "os/exec"

const bin = "git"

func spawn(r interface{ ExitFailure(string) error }) {
	_ = []string{"git", "status"}
	_ = exec.Command("git", "status")
	_ = r.ExitFailure("git")
	_ = map[string]string{"git": "tool"}
}
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got := unroutedGitLiterals(t, root, path)
	want := []string{
		"internal/fixture/spawn.go:5",
		"internal/fixture/spawn.go:8",
		"internal/fixture/spawn.go:9",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("flagged %q, want %q", got, want)
	}
}

// gitSpawnSources lists every non-test Go file in the module's source trees,
// outside testdata and the helper package.
func gitSpawnSources(t *testing.T, root string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range gitSpawnSourceDirs {
		if err := filepath.WalkDir(filepath.Join(root, dir), collectGoSources(root, &paths)); err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return slices.DeleteFunc(paths, func(p string) bool {
		return !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go")
	})
}

// collectGoSources appends every file the walk visits to paths, skipping
// testdata and the helper package.
func collectGoSources(root string, paths *[]string) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && (d.Name() == "testdata" || filepath.ToSlash(rel(root, path)) == gitHelperDir):
			return fs.SkipDir
		case !d.IsDir():
			*paths = append(*paths, path)
		}
		return nil
	}
}

// unroutedGitLiterals reports, as module-relative file:line, every "git"
// literal in one file that is not a tool-naming call argument, a map key, or
// an allowed label constant.
func unroutedGitLiterals(t *testing.T, root, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var bad []string
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if lit, ok := n.(*ast.BasicLit); ok && isGitLiteral(lit) && !allowedGitUse(lit, stack[len(stack)-1]) {
			bad = append(bad, filepath.ToSlash(rel(root, path))+":"+strconv.Itoa(fset.Position(lit.Pos()).Line))
		}
		stack = append(stack, n)
		return true
	})
	return bad
}

// isGitLiteral reports a string literal whose value is exactly git.
func isGitLiteral(lit *ast.BasicLit) bool {
	if lit.Kind != token.STRING {
		return false
	}
	v, err := strconv.Unquote(lit.Value)
	return err == nil && v == "git"
}

// allowedGitUse reports whether parent uses the literal as a name, not an
// argv element.
func allowedGitUse(lit *ast.BasicLit, parent ast.Node) bool {
	switch p := parent.(type) {
	case *ast.CallExpr:
		sel, ok := p.Fun.(*ast.SelectorExpr)
		return ok && slices.Contains(gitNamingCalls, sel.Sel.Name)
	case *ast.KeyValueExpr:
		return p.Key == lit
	case *ast.ValueSpec:
		return len(p.Names) == 1 && gitLabelConsts[p.Names[0].Name] != ""
	default:
		return false
	}
}

// rel is path relative to root, or path itself when it is not under root.
func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return r
}
