package archtest

import (
	"encoding/json"
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

// mcpOmittedVerbs are CLI verbs with no MCP tool, and why. The distribution
// lifecycle stays CLI-only on purpose (pkg/verdict/doc.go), and `mcp` is the
// transport entry point itself: an MCP tool named `mcp` would recurse instead
// of reviewing. `gate` is omitted because it exists to hand a CI job an exit
// code: an agent calling it as a tool would read a local verdict as authority
// to act, when the merge stays the human's. `run` is omitted because it spawns
// the operator's own agent: an MCP tool that launches a local agent process
// would make every tool call a recursive launcher of unpriced work, which
// is the boundary the run guardrail draws — the operator asks for one
// process, from their own terminal or CI, not from inside a model's turn.
var mcpOmittedVerbs = map[string]string{
	"panel":     "CLI-only invoker dispatch contract (STA-91)",
	"mcp":       "the MCP transport entry point itself",
	"skill":     "distribution surface (pkg/verdict/doc.go)",
	"uninstall": "distribution surface (pkg/verdict/doc.go)",
	"upgrade":   "distribution surface (pkg/verdict/doc.go)",
	"gate":      "a CI exit code, not a tool an agent should act on",
	"run":       "spawns the operator's agent; a tool that launches an agent recurses",
	"backtest":  "mining is free, but replay will spend on model runs: the CLI owns both",
	"serve":     "the MCP surface already exposes the same reads as tools; a tool that binds a listener starts a transport from a transport",
}

// sdkOmittedVerbs are CLI verbs with no Go facade method or TypeScript client
// method, and why: the distribution lifecycle stays CLI-only. `gate` is not
// here — a program honoring an exit code may compose the same verdict — while
// it stays out of the MCP surface above. `run` is here for the same reason
// it is out of MCP: the facade and the TS client drive review data, while a
// process launcher belongs to the operator's CLI (a program that wants it can
// run the same argv the --print output renders).
var sdkOmittedVerbs = map[string]string{
	"panel":     "CLI-only invoker dispatch contract (STA-91)",
	"mcp":       "the MCP transport entry point itself",
	"skill":     "distribution surface (pkg/verdict/doc.go)",
	"uninstall": "distribution surface (pkg/verdict/doc.go)",
	"upgrade":   "distribution surface (pkg/verdict/doc.go)",
	"run":       "a process launcher, not review data: the CLI owns it (see --print)",
	"backtest":  "replay will spend on model runs; the CLI owns the spend boundary",
	"serve":     "its product is HTTP; a method that blocks on a listener is not a client call",
}

// sdkGaps are review verbs one public surface is still missing. Closing a gap
// is an API decision (a new method and its types), so the guard records it
// instead of hiding it: the entry must name the finding tracking it, and it
// fails as stale the moment the method exists. Empty today: every review verb
// reaches the facade.
var sdkGaps = map[string]string{}

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
// verb it offers reaches MCP or is omitted from that surface.
func TestSurfaceParityCLI(t *testing.T) {
	t.Parallel()
	verbs := readSurfaceVerbs(t)
	checkUnique(t, "cli verb", verbs.cli)
	if len(verbs.cli) < 10 {
		t.Errorf("cli synopsis lists %d verbs; the usage line did not parse", len(verbs.cli))
	}
	checkSubset(t, "mcp tool", verbs.mcp, "cli verb", verbs.cli)
	checkIncludes(t, "mcp surface", verbs.mcp, "cli verb", without(verbs.cli, names(mcpOmittedVerbs)))
}

// TestSurfaceParitySDK proves every Go facade method wraps a CLI verb, and that
// a CLI verb reaches the facade unless it is omitted from the SDK or listed as
// a gap.
func TestSurfaceParitySDK(t *testing.T) {
	t.Parallel()
	verbs := readSurfaceVerbs(t)
	checkUnique(t, "sdk method", verbs.sdk)
	checkSubset(t, "sdk method", verbs.sdk, "cli verb", verbs.cli)
	want := without(verbs.cli, names(sdkOmittedVerbs), names(sdkGaps))
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

// TestSurfaceParityAllowlistsStayHonest proves the allowlists name real verbs
// and go stale loudly: a verb omitted from a surface that now exposes it, or a
// gap the facade has closed, fails here instead of quietly widening the check.
func TestSurfaceParityAllowlistsStayHonest(t *testing.T) {
	t.Parallel()
	verbs := readSurfaceVerbs(t)
	checkSubset(t, "mcp-omitted verb", names(mcpOmittedVerbs), "cli verb", verbs.cli)
	checkSubset(t, "sdk-omitted verb", names(sdkOmittedVerbs), "cli verb", verbs.cli)
	checkSubset(t, "sdk gap", names(sdkGaps), "cli verb", verbs.cli)
	checkMcpOmissions(t, &verbs)
	checkSDKOmissions(t, &verbs)
}

// checkMcpOmissions fails an MCP-omitted verb the MCP surface carries anyway.
func checkMcpOmissions(t *testing.T, verbs *surfaceVerbs) {
	t.Helper()
	for _, verb := range names(mcpOmittedVerbs) {
		if slices.Contains(verbs.mcp, verb) {
			t.Errorf("verb %q is omitted from MCP but the MCP surface exposes it", verb)
		}
	}
}

// checkSDKOmissions fails an SDK-omitted verb a client carries anyway, and a
// gap the facade has closed (the gap entry would hide the new method).
func checkSDKOmissions(t *testing.T, verbs *surfaceVerbs) {
	t.Helper()
	for _, verb := range names(sdkOmittedVerbs) {
		if slices.Contains(verbs.sdk, verb) || slices.Contains(verbs.ts, verb) {
			t.Errorf("verb %q is omitted from the SDK but a client exposes it", verb)
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
			names = append(names, strings.ReplaceAll(name, "_", "-"))
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
			if kebabBoundary(name, i) {
				out.WriteByte('-')
			}
			r = unicode.ToLower(r)
		}
		out.WriteRune(r)
	}
	return out.String()
}

// kebabBoundary reports whether the uppercase rune at i starts a new word: it
// follows a non-uppercase rune, or it ends an uppercase run.
func kebabBoundary(name string, i int) bool {
	if i == 0 {
		return false
	}
	if !unicode.IsUpper(rune(name[i-1])) {
		return true
	}
	return i+1 < len(name) && unicode.IsLower(rune(name[i+1]))
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

// TestVerifySandboxReachesEveryTypedSurface pins the opt-in sandbox knob on
// the surfaces that carry typed verify arguments: the CLI parser, the core
// request, the MCP schema snapshot and decoder, and the Go facade. The
// TypeScript client forwards raw argv, so `verify(["--sandbox"])` reaches the
// same parser without a typed field to drift.
func TestVerifySandboxReachesEveryTypedSurface(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	if !structHasBoolField(t, filepath.Join(root, "internal", "core", "verifyapi.go"), "VerifyRequest", "Sandbox") {
		t.Error("core.VerifyRequest lost its Sandbox field")
	}
	if !structHasBoolField(t, filepath.Join(root, "pkg", "verdict", "verbs.go"), "VerifyRequest", "Sandbox") {
		t.Error("pkg/verdict.VerifyRequest lost its Sandbox field")
	}
	cli := readSource(t, filepath.Join(root, "internal", "verify", "verify.go"))
	if !strings.Contains(cli, `case "--sandbox":`) {
		t.Error("the verify adapter no longer parses --sandbox")
	}
	mcp := readSource(t, filepath.Join(root, "internal", "mcp", "requests.go"))
	if !strings.Contains(mcp, `a.boolean("sandbox"`) {
		t.Error("the MCP verify decoder no longer reads sandbox")
	}
	if !mcpSchemaHasBoolean(t, filepath.Join(root, "internal", "mcp", "testdata", "tools_list_default.jsonl"), "verify", "sandbox") {
		t.Error("the published MCP verify schema lost its sandbox boolean")
	}
}

// structHasBoolField reports whether path declares typeName with a bool field.
func structHasBoolField(t *testing.T, path, typeName, field string) bool {
	t.Helper()
	st := declaredStruct(t, path, typeName)
	if st == nil {
		return false
	}
	for _, f := range st.Fields.List {
		if isBoolField(f, field) {
			return true
		}
	}
	return false
}

// declaredStruct returns one named struct declaration, or nil.
func declaredStruct(t *testing.T, path, typeName string) *ast.StructType {
	t.Helper()
	ts := findTypeSpec(t, path, typeName)
	if ts == nil {
		return nil
	}
	st, ok := ts.Type.(*ast.StructType)
	if !ok {
		return nil
	}
	return st
}

// findTypeSpec returns the named type declaration, or nil.
func findTypeSpec(t *testing.T, path, typeName string) *ast.TypeSpec {
	t.Helper()
	var found *ast.TypeSpec
	ast.Inspect(parseGo(t, path), func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if ok && ts.Name.Name == typeName {
			found = ts
			return false
		}
		return true
	})
	return found
}

// isBoolField reports whether f declares one bool field with the given name.
func isBoolField(f *ast.Field, name string) bool {
	if len(f.Names) != 1 || f.Names[0].Name != name {
		return false
	}
	ident, ok := f.Type.(*ast.Ident)
	return ok && ident.Name == "bool"
}

// readSource reads one source file as a string.
func readSource(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestCompletionTableCoversUsageFlags proves every flag a verb's synopsis
// documents reaches that verb's completion row. Nothing compared the two
// surfaces before, so each release added a flag to one of them alone.
func TestCompletionTableCoversUsageFlags(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	documented := usageFlagSets(t, functionStrings(t, filepath.Join(root, "cmd", "verdict", "usage.go"), "usageText"))
	offered := completionFlagSets(t, readSource(t, filepath.Join(root, "scripts", "verdict-complete.sh")))
	if len(documented) < 10 || len(offered) < 10 {
		t.Fatalf("parsed %d usage verbs and %d completion arms; the guard would pass vacuously",
			len(documented), len(offered))
	}
	for _, verb := range slices.Sorted(maps.Keys(documented)) {
		checkCompletionVerb(t, verb, documented[verb], offered[verb])
	}
}

// checkCompletionVerb fails every documented flag the verb's row omits.
func checkCompletionVerb(t *testing.T, verb string, documented, offered map[string]bool) {
	t.Helper()
	for _, flag := range slices.Sorted(maps.Keys(documented)) {
		if !offered[flag] {
			t.Errorf("verb %q documents %s but its completion row does not offer it", verb, flag)
		}
	}
}

// usageFlagSets maps each verb to the flags its synopsis lines carry: the
// header line plus the wrapped continuation lines before the prose starts.
func usageFlagSets(t *testing.T, usage string) map[string]map[string]bool {
	t.Helper()
	lines := strings.Split(usage, "\n")
	known := usageVerbs(lines[0])
	out := make(map[string]map[string]bool)
	for i := 0; i < len(lines); i++ {
		verb, ok := synopsisVerb(lines[i], known)
		if !ok {
			continue
		}
		i = collectSynopsisFlags(lines, i, verb, out)
	}
	return out
}

// usageVerbs reads the verb list from the usage synopsis line.
func usageVerbs(header string) map[string]bool {
	verbs := strings.Split(strings.TrimSuffix(strings.TrimPrefix(header, "usage: verdict "), " [opts]"), "|")
	known := make(map[string]bool, len(verbs))
	for _, verb := range verbs {
		known[verb] = true
	}
	return known
}

// collectSynopsisFlags adds one verb's synopsis flags and returns the index of
// the last line the synopsis consumed.
func collectSynopsisFlags(lines []string, start int, verb string, out map[string]map[string]bool) int {
	if out[verb] == nil {
		out[verb] = make(map[string]bool)
	}
	i := start
	for {
		for _, flag := range usageFlagToken.FindAllString(lines[i], -1) {
			out[verb][flag] = true
		}
		if i+1 >= len(lines) || !synopsisContinuation(lines[i+1]) {
			return i
		}
		i++
	}
}

// synopsisVerb reads the verb a "  <verb> ..." synopsis header names.
func synopsisVerb(line string, known map[string]bool) (string, bool) {
	rest, ok := strings.CutPrefix(line, "  ")
	if !ok || strings.HasPrefix(rest, " ") {
		return "", false
	}
	verb, _, _ := strings.Cut(rest, " ")
	return verb, known[verb]
}

// synopsisContinuation reports whether line continues the synopsis above it:
// a wrapped flag line, never the four-space prose below the synopsis.
func synopsisContinuation(line string) bool {
	if strings.TrimSpace(line) == "" {
		return false
	}
	return len(line)-len(strings.TrimLeft(line, " ")) >= 5 ||
		strings.HasPrefix(line, "[") || strings.HasPrefix(line, "-")
}

// completionFlagSets maps each verb to the flags its _verdict_flags arm offers.
func completionFlagSets(t *testing.T, script string) map[string]map[string]bool {
	t.Helper()
	out := make(map[string]map[string]bool)
	for line := range strings.SplitSeq(completionTable(t, script), "\n") {
		addCompletionArm(line, out)
	}
	return out
}

// completionTable returns the body of the _verdict_flags case arm.
func completionTable(t *testing.T, script string) string {
	t.Helper()
	start := strings.Index(script, "_verdict_flags() {")
	end := strings.Index(script, "_verdict_values() {")
	if start < 0 || end < start {
		t.Fatalf("scripts/verdict-complete.sh: cannot locate the _verdict_flags table")
	}
	return script[start:end]
}

// addCompletionArm records the flags one `verbs) ... ;;` arm offers.
func addCompletionArm(line string, out map[string]map[string]bool) {
	arm := completionArm.FindStringSubmatch(line)
	if arm == nil {
		return
	}
	flags := make(map[string]bool)
	for _, flag := range usageFlagToken.FindAllString(line, -1) {
		flags[flag] = true
	}
	for verb := range strings.SplitSeq(arm[1], "|") {
		out[verb] = flags
	}
}

var (
	usageFlagToken = regexp.MustCompile(`--[a-z0-9][a-z0-9-]*`)
	completionArm  = regexp.MustCompile(`^\s*([a-z][a-z0-9|-]*)\)`)
)

// mcpSchemaHasBoolean reads the checked-in tools/list snapshot and reports
// whether one tool's published input schema carries a boolean property.
func mcpSchemaHasBoolean(t *testing.T, path, tool, property string) bool {
	t.Helper()
	var frame struct {
		Result struct {
			Tools []struct { //nolint:govet // decode-only shape for the checked-in snapshot
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]struct {
						Type string `json:"type"`
					} `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(readSource(t, path)), &frame); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	for _, candidate := range frame.Result.Tools {
		if candidate.Name != tool {
			continue
		}
		prop, ok := candidate.InputSchema.Properties[property]
		return ok && prop.Type == "boolean"
	}
	t.Fatalf("%s: no %q tool", path, tool)
	return false
}
