package pack

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core"
)

// checkRequest wires one pack check over the shipped builtin layer.
func checkRequest(t *testing.T, root, path string) *CheckRequest {
	t.Helper()
	return &CheckRequest{
		Runtime:      &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}, PackFS: skillfs.FS},
		RepoRoot:     root,
		ConfigSource: ConfigSourceNone,
		Path:         path,
	}
}

// TestRunCheckResolvesGoStack proves one path resolves to the shipped packs,
// the lenses offered for it, and the rules whose globs match it, all in the
// merge's own order.
func TestRunCheckResolvesGoStack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	resp, ref := RunCheck(t.Context(), checkRequest(t, root, "./internal/store/store.go"))
	if ref != nil {
		t.Fatalf("RunCheck: %s: %s", ref.Code, ref.Message)
	}
	if resp.Data.Path != "internal/store/store.go" {
		t.Errorf("path = %q, want the cleaned repository path", resp.Data.Path)
	}
	if resp.Data.Config != ConfigSourceNone {
		t.Errorf("config = %q, want %q", resp.Data.Config, ConfigSourceNone)
	}
	requirePackNames(t, resp.Data.Packs)
	requireCheckLenses(t, resp.Data.Lenses)
	requireCheckRules(t, resp.Data.Rules)
	human := strings.Join(resp.Human, "\n")
	for _, want := range []string{
		"path     internal/store/store.go",
		"config   none",
		"packs:",
		"  builtin  verdict/go@1.0.0  source=builtin:go  sha256:",
		"  go  pack=verdict/go  layer=builtin  sha256:",
		"  go-shared-state                   severity=critical  pack=verdict/go  layer=builtin  globs=**/*.go",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("human rows lack %q:\n%s", want, human)
		}
	}
}

// requirePackNames pins the loaded layer order.
func requirePackNames(t *testing.T, packs []CheckPack) {
	t.Helper()
	names := make([]string, 0, len(packs))
	for i := range packs {
		names = append(names, packs[i].Name)
	}
	if !slices.Equal(names, shippedLanguagePacks) {
		t.Errorf("packs = %v, want %v", names, shippedLanguagePacks)
	}
}

// requireCheckLenses pins the lens ids a Go path offers and the go row.
func requireCheckLenses(t *testing.T, lenses []CheckLens) {
	t.Helper()
	ids := make([]string, 0, len(lenses))
	for i := range lenses {
		ids = append(ids, lenses[i].ID)
	}
	if want := []string{"api-contract", "go", "perf", "security", "test"}; !slices.Equal(ids, want) {
		t.Errorf("lenses = %v, want %v", ids, want)
	}
	if got := lenses[1]; got.Pack != "verdict/go" || got.Layer != "builtin" || len(got.Hash) != hash8Len {
		t.Errorf("go lens row = %+v", got)
	}
}

// requireCheckRules pins the rule ids a Go path matches and the go row.
func requireCheckRules(t *testing.T, rules []CheckRule) {
	t.Helper()
	ids := make([]string, 0, len(rules))
	for i := range rules {
		ids = append(ids, rules[i].ID)
	}
	want := []string{
		"go-context-lifecycle", "go-error-loss", "go-resource-release", "go-shared-state",
		"sec-broken-authz", "sec-injection", "sec-ssrf", "sec-unsafe-deserialization",
	}
	if !slices.Equal(ids, want) {
		t.Errorf("rules = %v, want %v", ids, want)
	}
	if got := rules[0]; got.Severity != "critical" || got.Pack != "verdict/go" ||
		!slices.Equal(got.Globs, []string{"**/*.go"}) {
		t.Errorf("go rule row = %+v", got)
	}
}

// TestRunCheckAppliesBaseToggles proves the trusted-base composition reaches
// the resolution: the disabled pack is absent, the applied toggle is reported,
// and the surviving packs are unchanged.
func TestRunCheckAppliesBaseToggles(t *testing.T) {
	t.Parallel()
	req := checkRequest(t, t.TempDir(), "internal/store/store.go")
	req.Toggles = Toggles{DisablePacks: []string{"verdict/go"}}
	req.ConfigSource = "base@7fadf718835a"
	resp, ref := RunCheck(t.Context(), req)
	if ref != nil {
		t.Fatalf("RunCheck: %s: %s", ref.Code, ref.Message)
	}
	requireToggledOff(t, resp.Data)
	if !slices.Contains(resp.Human, "disabled pack:verdict/go") {
		t.Errorf("human rows lack the disabled line:\n%s", strings.Join(resp.Human, "\n"))
	}
}

// requireToggledOff pins the resolution after the base disabled one pack.
func requireToggledOff(t *testing.T, data *CheckData) {
	t.Helper()
	if findPack(data.Packs, "verdict/go") != nil {
		t.Fatal("the disabled builtin pack still resolved")
	}
	if !slices.Equal(data.Disabled, []string{"pack:verdict/go"}) {
		t.Errorf("disabled = %v", data.Disabled)
	}
	if data.Config != "base@7fadf718835a" {
		t.Errorf("config = %q", data.Config)
	}
	for i := range data.Rules {
		if strings.HasPrefix(data.Rules[i].ID, "go-") {
			t.Errorf("disabled pack's rule %s still applies", data.Rules[i].ID)
		}
	}
}

// TestRunCheckReportsOverrideWinner proves a repo pack that replaces a builtin
// pack wins the id and the row names what it shadowed.
func TestRunCheckReportsOverrideWinner(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePackFile(t, root, ".verdict/packs/repo/pack.json",
		`{"schema_version":"1","name":"acme/repo","version":"0.1.0","replace":["verdict/go"],`+
			`"severity":{"important":"important"},"lenses":[{"id":"go"}],`+
			`"rules":[{"id":"go-shared-state","severity":"important","globs":["**/*.go"]}]}`)
	writePackFile(t, root, ".verdict/packs/repo/lenses/go.md", "repo go body\n")
	writePackFile(t, root, ".verdict/packs/repo/rules/go-shared-state.md", "repo rule body\n")
	resp, ref := RunCheck(t.Context(), checkRequest(t, root, "main.go"))
	if ref != nil {
		t.Fatalf("RunCheck: %s: %s", ref.Code, ref.Message)
	}
	requireRepoWins(t, resp.Data)
}

// requireRepoWins pins the three rows a declared override changes: the lens,
// the rule, and the pack row's replace list.
func requireRepoWins(t *testing.T, data *CheckData) {
	t.Helper()
	lens := findLens(data.Lenses, "go")
	if lens == nil || lens.Pack != "acme/repo" || lens.Layer != "repo" ||
		!slices.Equal(lens.Replaced, []string{"verdict/go"}) {
		t.Fatalf("go lens = %+v, want the repo pack's row shadowing verdict/go", lens)
	}
	rule := findRule(data.Rules, "go-shared-state")
	if rule == nil || rule.Pack != "acme/repo" || !slices.Equal(rule.Replaced, []string{"verdict/go"}) {
		t.Fatalf("go-shared-state rule = %+v, want the repo pack's row", rule)
	}
	if packed := findPack(data.Packs, "acme/repo"); packed == nil ||
		!slices.Equal(packed.Replace, []string{"verdict/go"}) {
		t.Fatalf("repo pack row = %+v, want its replace list", packed)
	}
}

// findLens returns one lens row by id.
func findLens(rows []CheckLens, id string) *CheckLens {
	for i := range rows {
		if rows[i].ID == id {
			return &rows[i]
		}
	}
	return nil
}

// findRule returns one rule row by id.
func findRule(rows []CheckRule, id string) *CheckRule {
	for i := range rows {
		if rows[i].ID == id {
			return &rows[i]
		}
	}
	return nil
}

// findPack returns one pack row by name.
func findPack(rows []CheckPack, name string) *CheckPack {
	for i := range rows {
		if rows[i].Name == name {
			return &rows[i]
		}
	}
	return nil
}

// TestRunCheckRefusesUnsafePaths proves a path that is not a repository member
// refuses bad-path before any pack is read, and that a control character (a NUL
// included) is refused rather than reaching the glob matcher or a log line.
func TestRunCheckRefusesUnsafePaths(t *testing.T) {
	t.Parallel()
	refused := []string{
		"", "   ", "/etc/passwd", "../outside.go", "a/../../b.go", `src\main.go`,
		"C:/repo/x.go", ".", "main.go\x00evil", "a\tb.go", "main.go\x7f",
	}
	for _, path := range refused {
		_, ref := RunCheck(t.Context(), checkRequest(t, t.TempDir(), path))
		if ref == nil || ref.Code != CodeBadPath {
			t.Errorf("path %q = %v, want bad-path", path, ref)
		}
	}
	// The same run still accepts the printable spelling: refusing control
	// characters must not narrow the paths check resolves.
	if _, ref := RunCheck(t.Context(), checkRequest(t, t.TempDir(), "cmd/main.go")); ref != nil {
		t.Errorf("printable path refused: %s: %s", ref.Code, ref.Message)
	}
}

// TestRunCheckReportsNoMatch proves an unmatched path reports the two empty
// sections instead of printing nothing: with the builtin layer merged off and
// no repo pack, the path matches neither a lens nor a rule.
func TestRunCheckReportsNoMatch(t *testing.T) {
	t.Parallel()
	req := checkRequest(t, t.TempDir(), "notes.txt")
	off := false
	req.Toggles = Toggles{MergeBuiltin: &off}
	resp, ref := RunCheck(t.Context(), req)
	if ref != nil {
		t.Fatalf("RunCheck: %s: %s", ref.Code, ref.Message)
	}
	if len(resp.Data.Lenses) != 0 || len(resp.Data.Rules) != 0 {
		t.Errorf("lenses = %v rules = %v, want none", resp.Data.Lenses, resp.Data.Rules)
	}
	human := strings.Join(resp.Human, "\n")
	for _, want := range []string{
		"  no lens is offered for this path",
		"  no rule's globs match this path",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("human rows lack %q:\n%s", want, human)
		}
	}
}

// TestCheckDataWireShape proves the wire profile carries every published key,
// with empty arrays rather than nulls.
func TestCheckDataWireShape(t *testing.T) {
	t.Parallel()
	empty := (&CheckData{Path: "a.go", Config: ConfigSourceNone}).Wire()
	for _, key := range []string{"path", "config", "disabled", "packs", "lenses", "rules"} {
		if _, ok := empty.Get(key); !ok {
			t.Errorf("wire profile lacks %q", key)
		}
	}
}

// writePackFile writes one fixture file under root, creating parents.
func writePackFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
