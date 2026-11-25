package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
// internal/gitcmd spells git anywhere it could become an argv: a "git"
// literal as a slice element, an exec argument, a constant or variable, or a
// call argument other than a tool-naming one; a constant concatenation that
// spells "git"; or a sh/bash argv whose literal script runs git. A new git
// call therefore cannot skip the shared helper, and with it the hook,
// filter, and fsmonitor lockdown on PR content, by any of those spellings.
// It is a syntactic guard: a name computed at run time is out of its reach,
// and review covers that.
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
// constant that would carry one, an exec of git, a concatenated spelling,
// and a shell script running git, and lets a tool-naming call, a map key,
// and a shell script without git through.
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
	_ = exec.Command("g"+"it", "status")
	_ = exec.Command("sh", "-c", "cd x && git status")
	_ = []string{"/bin/bash", "-c", "git push"}
	_ = exec.Command("sh", "-c", "digit status")
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
		"internal/fixture/spawn.go:12",
		"internal/fixture/spawn.go:13",
		"internal/fixture/spawn.go:14",
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

// unroutedGitLiterals reports, as module-relative file:line, every git
// spelling in one file the guard flags: a "git" literal that is not a
// tool-naming call argument, a map key, or an allowed label constant; a
// constant concatenation that spells "git"; and a call or slice that runs a
// shell on a literal script naming git.
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
		var parent ast.Node
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		if spellsGit(n, parent) {
			bad = append(bad, filepath.ToSlash(rel(root, path))+":"+strconv.Itoa(fset.Position(n.Pos()).Line))
		}
		stack = append(stack, n)
		return true
	})
	return bad
}

// spellsGit reports a node the guard flags, given its parent.
func spellsGit(n, parent ast.Node) bool {
	switch n := n.(type) {
	case *ast.BasicLit:
		v, ok := stringValue(n)
		return ok && v == "git" && !allowedGitUse(n, parent)
	case *ast.BinaryExpr:
		v, ok := stringValue(n)
		return ok && v == "git"
	case *ast.CallExpr:
		return shellRunsGit(n.Args)
	case *ast.CompositeLit:
		return shellRunsGit(n.Elts)
	default:
		return false
	}
}

// stringValue is the constant string an expression spells: a string literal
// or a `+` of them. ok is false for anything else.
func stringValue(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(e.Value)
		return v, err == nil
	case *ast.BinaryExpr:
		left, lok := stringValue(e.X)
		right, rok := stringValue(e.Y)
		return left + right, e.Op == token.ADD && lok && rok
	default:
		return "", false
	}
}

// shellScriptGit matches git run as a command inside a shell script.
var shellScriptGit = regexp.MustCompile(`(^|[\s;&|(])git\s`)

// shellRunsGit reports an argv that runs sh or bash with a literal script
// running git.
func shellRunsGit(elts []ast.Expr) bool {
	shell, script := false, false
	for _, e := range elts {
		v, ok := stringValue(e)
		if !ok {
			continue
		}
		name := v[strings.LastIndexByte(v, '/')+1:]
		shell = shell || name == "sh" || name == "bash"
		script = script || shellScriptGit.MatchString(v)
	}
	return shell && script
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
