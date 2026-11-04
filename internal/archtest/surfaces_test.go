package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// cliOnlyVerbs are the CLI verbs no other surface exposes, and why. The
// distribution lifecycle stays CLI-only on purpose (pkg/verdict/doc.go), and
// `mcp` is the transport entry point itself: an MCP tool or SDK method named
// `mcp` would recurse instead of reviewing.
var cliOnlyVerbs = map[string]string{
	"mcp":       "the MCP transport entry point itself",
	"skill":     "distribution surface (pkg/verdict/doc.go)",
	"uninstall": "distribution surface (pkg/verdict/doc.go)",
	"upgrade":   "distribution surface (pkg/verdict/doc.go)",
}

// sdkGaps are review verbs one public surface is still missing. Closing a gap
// is an API decision (a new method and its types), so the guard records it
// instead of hiding it: the entry must name the finding tracking it, and it
// fails as stale the moment the method exists.
var sdkGaps = map[string]string{
	"threads": "A2-parity-05: pkg/verdict exposes no Threads method (OPEN)",
}

// tsMethod matches one interface method declaration in the TS client, with an
// optional generic parameter list.
var tsMethod = regexp.MustCompile(`^ {2}([a-z][A-Za-z0-9]*)(<[^>]*>)?\(`)

// tsRunEscapeHatch is the client's generic "any verb" method: it is not a verb,
// and it keeps a verb the client lacks reachable anyway.
const tsRunEscapeHatch = "run"

// surfaceVerbs is one read of the four public verb surfaces: the CLI synopsis,
// the MCP tool registry, the Go facade, and the TypeScript client.
type surfaceVerbs struct {
	cli []string
	mcp []string
	sdk []string
	ts  []string
}

// readSurfaceVerbs reads each surface from where that surface declares it, so
// the guarantees compare separate sources rather than a list against itself.
func readSurfaceVerbs(t *testing.T) surfaceVerbs {
	t.Helper()
	root := moduleRoot(t)
	return surfaceVerbs{
		cli: cliVerbs(t, root),
		mcp: mcpToolNames(t, root),
		sdk: sdkVerbs(t, root),
		ts:  tsClientVerbs(t, root),
	}
}

// TestSurfaceParityCLI proves the CLI synopsis is well formed and that every
// verb it offers reaches MCP or is allowlisted as CLI-only.
func TestSurfaceParityCLI(t *testing.T) {
	t.Parallel()
	verbs := readSurfaceVerbs(t)
	checkUnique(t, "cli verb", verbs.cli)
	if len(verbs.cli) < 10 {
		t.Errorf("cli synopsis lists %d verbs; the usage line did not parse", len(verbs.cli))
	}
	checkSubset(t, "mcp tool", verbs.mcp, "cli verb", verbs.cli)
	checkIncludes(t, "mcp surface", verbs.mcp, "cli verb", without(verbs.cli, names(cliOnlyVerbs)))
}

// TestSurfaceParitySDK proves every Go facade method wraps a CLI verb, and that
// a CLI verb reaches the facade unless it is allowlisted as CLI-only or listed
// as a gap.
func TestSurfaceParitySDK(t *testing.T) {
	t.Parallel()
	verbs := readSurfaceVerbs(t)
	checkUnique(t, "sdk method", verbs.sdk)
	checkSubset(t, "sdk method", verbs.sdk, "cli verb", verbs.cli)
	want := without(verbs.cli, names(cliOnlyVerbs), names(sdkGaps))
	checkIncludes(t, "pkg/verdict surface", verbs.sdk, "cli verb", want)
}

// TestSurfaceParityTS proves the TypeScript client exposes exactly the verbs
// the Go facade does, so the two clients of the same wire contract cannot
// drift.
func TestSurfaceParityTS(t *testing.T) {
	t.Parallel()
	verbs := readSurfaceVerbs(t)
	checkUnique(t, "ts method", verbs.ts)
	checkSubset(t, "ts method", verbs.ts, "sdk method", verbs.sdk)
	checkIncludes(t, "TypeScript client", verbs.ts, "sdk method", verbs.sdk)
}

// TestSurfaceParityAllowlistsStayHonest proves the two allowlists name real
// verbs and go stale loudly: a CLI-only verb another surface exposes, or a gap
// the facade has closed, fails here instead of quietly widening the check.
func TestSurfaceParityAllowlistsStayHonest(t *testing.T) {
	t.Parallel()
	verbs := readSurfaceVerbs(t)
	checkSubset(t, "cli-only verb", names(cliOnlyVerbs), "cli verb", verbs.cli)
	checkSubset(t, "sdk gap", names(sdkGaps), "cli verb", verbs.cli)
	for _, verb := range names(cliOnlyVerbs) {
		if slices.Contains(verbs.sdk, verb) || slices.Contains(verbs.mcp, verb) {
			t.Errorf("verb %q is allowlisted as cli-only but another surface exposes it", verb)
		}
	}
	for _, verb := range names(sdkGaps) {
		if slices.Contains(verbs.sdk, verb) {
			t.Errorf("sdk gap %q is stale: pkg/verdict now exposes it", verb)
		}
	}
}

// cliVerbs reads the verb list from the frozen usage synopsis in
// cmd/verdict/usage.go.
func cliVerbs(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "cmd", "verdict", "usage.go")
	line, ok := strings.CutPrefix(functionStrings(t, path, "usageText"), "usage: verdict ")
	if !ok {
		t.Fatalf("%s: usage synopsis does not start with \"usage: verdict \"", path)
	}
	verbs, ok := strings.CutSuffix(firstLine(line), " [opts]")
	if !ok {
		t.Fatalf("%s: usage synopsis does not end with \" [opts]\"", path)
	}
	return strings.Split(verbs, "|")
}

// firstLine returns the first line of s.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// functionStrings returns the string literals one function builds, in source
// order and concatenated, which is how the usage block and its wrapped lines
// read.
func functionStrings(t *testing.T, path, name string) string {
	t.Helper()
	var out strings.Builder
	ast.Inspect(functionDecl(t, path, name).Body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok {
			if value, ok := stringLiteral(lit); ok {
				out.WriteString(value)
			}
		}
		return true
	})
	return out.String()
}

// functionDecl parses path and returns the named function declaration.
func functionDecl(t *testing.T, path, name string) *ast.FuncDecl {
	t.Helper()
	file := parseGo(t, path)
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("%s: no func %s", path, name)
	return nil
}

// parseGo parses one Go source file; a file that does not parse fails the
// guard instead of shrinking the set it reports on.
func parseGo(t *testing.T, path string) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

// mcpToolNames reads the tool names the MCP registry declares.
func mcpToolNames(t *testing.T, root string) []string {
	t.Helper()
	var names []string
	file := parseGo(t, filepath.Join(root, "internal", "mcp", "tools.go"))
	ast.Inspect(file, func(n ast.Node) bool {
		if name, ok := toolName(n); ok {
			names = append(names, name)
		}
		return true
	})
	if len(names) == 0 {
		t.Fatal("internal/mcp/tools.go declares no tool names")
	}
	return names
}

// toolName reports the string literal a `name:` field carries.
func toolName(n ast.Node) (string, bool) {
	field, ok := n.(*ast.KeyValueExpr)
	if !ok || !isNameField(field.Key) {
		return "", false
	}
	return stringLiteral(field.Value)
}

// isNameField reports whether key is the `name` field of a tool spec.
func isNameField(key ast.Expr) bool {
	ident, ok := key.(*ast.Ident)
	return ok && ident.Name == "name"
}

// sdkVerbs reads the Go facade's verb methods: exported methods on *Client that
// take a context first, which is the facade's method convention.
func sdkVerbs(t *testing.T, root string) []string {
	t.Helper()
	var verbs []string
	for _, decl := range parseGo(t, filepath.Join(root, "pkg", "verdict", "verbs.go")).Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && isVerbMethod(fn) {
			verbs = append(verbs, kebabCase(fn.Name.Name))
		}
	}
	if len(verbs) == 0 {
		t.Fatal("pkg/verdict/verbs.go declares no verb methods")
	}
	return verbs
}

// isVerbMethod reports whether fn is a *Client method whose first parameter is
// a [context.Context].
func isVerbMethod(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 || !isClientReceiver(fn.Recv.List[0].Type) {
		return false
	}
	params := fn.Type.Params.List
	if len(params) == 0 || len(params[0].Names) == 0 {
		return false
	}
	return isContextType(params[0].Type)
}

// isClientReceiver reports whether a method receiver is *Client.
func isClientReceiver(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == "Client"
}

// isContextType reports whether expr is [context.Context].
func isContextType(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "context" && selector.Sel.Name == "Context"
}

// tsClientVerbs reads the verb methods the VerdictClient interface declares,
// minus the generic escape hatch.
func tsClientVerbs(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "clients", "ts", "src", "client.ts")
	var verbs []string
	for _, line := range interfaceBody(t, path, "export interface VerdictClient {") {
		if name, ok := tsVerbMethod(line); ok {
			verbs = append(verbs, kebabCase(name))
		}
	}
	if len(verbs) == 0 {
		t.Fatalf("%s: VerdictClient declares no verb methods", path)
	}
	return verbs
}

// interfaceBody returns the lines between the interface header line and its
// closing brace.
func interfaceBody(t *testing.T, path, header string) []string {
	t.Helper()
	lines := readLines(t, path)
	start := slices.Index(lines, header)
	if start < 0 {
		t.Fatalf("%s: no %q interface", path, header)
	}
	for i, line := range lines[start+1:] {
		if line == "}" {
			return lines[start+1 : start+1+i]
		}
	}
	t.Fatalf("%s: interface %q is unterminated", path, header)
	return nil
}

// tsVerbMethod reports the method name a declaration line carries, skipping
// the generic escape hatch and every non-method field.
func tsVerbMethod(line string) (string, bool) {
	match := tsMethod.FindStringSubmatch(line)
	if match == nil || match[1] == tsRunEscapeHatch {
		return "", false
	}
	return match[1], true
}

// readLines reads path and splits it into lines.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(raw), "\n")
}

// kebabCase renders an exported Go or camelCase TS name as the CLI verb it
// wraps: Report -> report, FetchLocal -> fetch-local.
func kebabCase(name string) string {
	var out strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				out.WriteByte('-')
			}
			r = unicode.ToLower(r)
		}
		out.WriteRune(r)
	}
	return out.String()
}

// names returns the allowlist keys in a stable order.
func names(set map[string]string) []string {
	return slices.Sorted(maps.Keys(set))
}

// without returns the entries of have that none of the drop lists carry.
func without(have []string, drops ...[]string) []string {
	var kept []string
	for _, name := range have {
		if !slices.ContainsFunc(drops, func(drop []string) bool { return slices.Contains(drop, name) }) {
			kept = append(kept, name)
		}
	}
	return kept
}

// checkIncludes fails for every name in want that have does not carry, naming
// the surface that lacks it.
func checkIncludes(t *testing.T, haveName string, have []string, wantKind string, want []string) {
	t.Helper()
	for _, name := range want {
		if !slices.Contains(have, name) {
			t.Errorf("%s %q is missing from the %s", wantKind, name, haveName)
		}
	}
}

// checkSubset fails for every name in have that want does not carry.
func checkSubset(t *testing.T, haveKind string, have []string, wantKind string, want []string) {
	t.Helper()
	if len(want) == 0 {
		t.Fatalf("no %s to compare against; the guard would pass vacuously", wantKind)
	}
	for _, name := range have {
		if !slices.Contains(want, name) {
			t.Errorf("%s %q is not a %s", haveKind, name, wantKind)
		}
	}
}

// checkUnique fails on a repeated name, which would make the subset checks
// read as coverage they do not have.
func checkUnique(t *testing.T, kind string, list []string) {
	t.Helper()
	seen := make(map[string]bool, len(list))
	for _, name := range list {
		if seen[name] {
			t.Errorf("%s %q is declared more than once", kind, name)
			continue
		}
		seen[name] = true
	}
}
