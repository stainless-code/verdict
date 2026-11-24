package fixcore

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/core/recipe"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestComboFaultRecipe pins the --recipe flag combinations: the empty-name
// witness, the two refusals, and the batch flags it composes with.
func TestComboFaultRecipe(t *testing.T) {
	tests := []struct {
		name string
		req  *Request
		want string
	}{
		{"recipe alone is a batch", &Request{Recipe: "quick"}, ""},
		{"recipe with floor", &Request{Recipe: "quick", MinSeverity: "critical"}, ""},
		{"recipe with verify", &Request{Recipe: "quick", VerifyCmd: "go test ./..."}, ""},
		{"recipe with paths", &Request{Recipe: "quick", Paths: []string{"src/**"}}, ""},
		{"recipe with all", &Request{Recipe: "quick", All: true}, ""},
		{"recipe with selected", &Request{Recipe: "quick", Selected: true}, ""},
		{
			"explicitly empty recipe refuses", &Request{RecipeSet: true},
			"fix --recipe needs a name (an empty value would run an ordinary pass): pass --recipe <name>",
		},
		{
			"recipe with sig refuses", &Request{Recipe: "quick", Sig: "deadbeef"},
			"fix takes --sig or --recipe, not both (--recipe selects the findings a named recipe owns)",
		},
		{
			"recipe with test refuses", &Request{Recipe: "quick", Test: "deadbeef"},
			"fix takes --test or --recipe, not both (--recipe selects the findings a named recipe owns)",
		},
		{
			"test with recipe refuses", &Request{Test: "deadbeef", Recipe: "quick"},
			"fix takes --test or --recipe, not both (--recipe selects the findings a named recipe owns)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.req.comboFault(); got != tc.want {
				t.Errorf("comboFault = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestPlanLensIDs proves the selector set reads the lenses stage only.
func TestPlanLensIDs(t *testing.T) {
	plan := &recipe.Plan{Stages: []recipe.Stage{
		{Kind: recipe.StageLenses, Lenses: []recipe.LensUse{{ID: "perf"}, {ID: "security"}}},
		{Kind: recipe.StageSynthesize},
	}}
	if got := planLensIDs(plan); len(got) != 2 || got[0] != "perf" || got[1] != "security" {
		t.Fatalf("planLensIDs = %v; want [perf security]", got)
	}
	if got := planLensIDs(&recipe.Plan{Stages: []recipe.Stage{{Kind: recipe.StageSynthesize}}}); len(got) != 0 {
		t.Fatalf("planLensIDs = %v; want no lenses without a lenses stage", got)
	}
}

var recipeCmdr = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}

// recipeRepo builds a git repository whose base commits a recipes.json that
// declares `quick` over the perf lens, then rewrites the worktree file to a
// different recipe and commits it. It chdirs into the repository and
// returns its root and base commit.
func recipeRepo(t *testing.T, declared, prepared string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	gitFix(t, dir, "init", "-q")
	writeFixFile(t, dir, ".verdict/recipes.json", declared)
	gitFix(t, dir, "add", "-f", "-A")
	gitFix(t, dir, "commit", "-qm", "base")
	base := strings.TrimSpace(gitFix(t, dir, "rev-parse", "HEAD"))
	writeFixFile(t, dir, ".verdict/recipes.json", prepared)
	gitFix(t, dir, "add", "-f", "-A")
	gitFix(t, dir, "commit", "-qm", "head", "--allow-empty")
	return dir, base
}

func gitFix(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_NAME":     "fix",
		"GIT_AUTHOR_EMAIL":    "fix@example.invalid",
		"GIT_COMMITTER_NAME":  "fix",
		"GIT_COMMITTER_EMAIL": "fix@example.invalid",
	})
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func writeFixFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recipeFinding renders one store entry with optional lens provenance.
func recipeFinding(sig, path, lens string) string {
	key := ""
	if lens != "" {
		key = `"lens":"` + lens + `",`
	}
	return `{"kind":"inline","path":"` + path + `","side":"RIGHT","line":2,` + key +
		`"anchored":true,"body":"Body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":"pointed","severity":"important","kb":[],"sig":"` + sig + `",` +
		`"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[]}`
}

// recipeBundle builds a recipe repo, writes the bundle files, and resolves
// the control snapshot. lenses and selected are optional: an empty string
// writes no file.
func recipeBundle(t *testing.T, declared, prepared, lenses, findings, selected string) string {
	t.Helper()
	root, base := recipeRepo(t, declared, prepared)
	bundleDir := filepath.Join(root, ".verdict", "state", "local-fix")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if lenses != "" {
		writeFixFile(t, root, ".verdict/state/local-fix/lenses.txt", lenses)
	}
	writeFixFile(t, root, ".verdict/state/local-fix/findings.json", findings)
	if selected != "" {
		writeFixFile(t, root, ".verdict/state/local-fix/selected.json", selected)
	}
	snapshotRecipes(t, bundleDir, base)
	return root
}

// snapshotRecipes resolves the bundle's control snapshot from the base.
func snapshotRecipes(t *testing.T, bundleDir, base string) {
	t.Helper()
	if _, ref := controls.Resolve(t.Context(), recipeCmdr, &controls.Request{BundleDir: bundleDir, BaseSHA: base}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
}

func recipeRequest(t *testing.T, root, name string, human *strings.Builder) *Request {
	t.Helper()
	rr := core.ReadRequest{
		Runtime:  &core.Runtime{Commander: recipeCmdr},
		Repo:     "acme/app",
		Pr:       "local-fix",
		RepoRoot: root,
	}
	return &Request{
		ReadRequest: rr,
		Recipe:      name,
		RecipeSet:   true,
		VerifyCmd:   "go test ./...",
		Human:       human,
	}
}

// TestBatchModeComposesSelectedRecipe proves the re-run text carries both
// flags for a composite --selected --recipe run instead of widening it.
func TestBatchModeComposesSelectedRecipe(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  *Request
		want string
	}{
		{"selected recipe", &Request{Selected: true, Recipe: "quick"}, "--selected --recipe quick"},
		{"all recipe", &Request{All: true, Recipe: "quick"}, "--all --recipe quick"},
		{"recipe only", &Request{Recipe: "quick"}, "--recipe quick"},
		{"selected only", &Request{Selected: true}, "--selected"},
		{"all only", &Request{All: true}, "--all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (&state{req: tc.req}).batchMode(); got != tc.want {
				t.Errorf("batchMode = %q; want %q", got, tc.want)
			}
		})
	}
}

// ptrValue reads an optional string, empty when absent.
func ptrValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func batchSigs(t *testing.T, resp *Response) []string {
	t.Helper()
	var out []string
	for _, row := range resp.Data.Batch {
		out = append(out, row.Sig)
	}
	return out
}

// TestRunRecipeSelectsSnapshotLenses proves the selection reads the base
// snapshot's recipe, not the worktree's: the head commit rewrote recipes.json
// to a recipe over an unrelated lens, yet fix still selects by the base lens.
func TestRunRecipeSelectsSnapshotLenses(t *testing.T) {
	root, base := recipeRepo(t,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`,
		`{"schema_version":"1","recipes":{"quick":{"version":"2.0.0","lenses":[{"id":"evil"}],"synthesize":{}}}}`)
	bundleDir := filepath.Join(root, ".verdict", "state", "local-fix")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixFile(t, root, ".verdict/state/local-fix/lenses.txt",
		"# lens perf — sha256:abc\nperf body\n# end lens perf\n# lens security — sha256:def\nsecurity body\n# end lens security\n")
	writeFixFile(t, root, ".verdict/state/local-fix/findings.json", "["+
		recipeFinding("perf-a", "perf.go", "perf")+","+
		recipeFinding("sec-a", "sec.go", "security")+","+
		recipeFinding("nolen", "plain.go", "")+"]")
	snapshotRecipes(t, bundleDir, base)

	var human strings.Builder
	resp, ref := Run(t.Context(), recipeRequest(t, root, "quick", &human))
	if ref != nil {
		t.Fatalf("Run: %+v", ref)
	}
	if got := batchSigs(t, resp); len(got) != 1 || got[0] != "perf-a" {
		t.Fatalf("batch sigs = %v; want [perf-a] from the base snapshot", got)
	}
	if resp.Data.Recipe == nil || *resp.Data.Recipe != "quick" {
		t.Fatalf("data.recipe = %v; want quick", resp.Data.Recipe)
	}
	if got, _ := resp.Data.Wire().Get("recipe"); got != "quick" {
		t.Fatalf("wire recipe = %v; want quick", got)
	}
}

// TestRunRecipeZeroLensesReportsEmpty proves the fail-closed guard: a recipe
// resolving to no lenses selects nothing and names the recipe, instead of
// widening the batch to every open finding.
func TestRunRecipeZeroLensesReportsEmpty(t *testing.T) {
	root, base := recipeRepo(t,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","synthesize":{}}}}`,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","synthesize":{}}}}`)
	bundleDir := filepath.Join(root, ".verdict", "state", "local-fix")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixFile(t, root, ".verdict/state/local-fix/findings.json",
		"["+recipeFinding("perf-a", "perf.go", "perf")+"]")
	snapshotRecipes(t, bundleDir, base)

	var human strings.Builder
	resp, ref := Run(t.Context(), recipeRequest(t, root, "quick", &human))
	if ref != nil {
		t.Fatalf("Run: %+v", ref)
	}
	if len(resp.Data.Batch) != 0 {
		t.Fatalf("batch = %v; want zero rows for a lens-less recipe", resp.Data.Batch)
	}
	if !strings.Contains(human.String(), "fix --recipe quick: the recipe declares no lenses") {
		t.Fatalf("human output = %q; want the empty recipe scope named", human.String())
	}
	if resp.Data.Recipe == nil || *resp.Data.Recipe != "quick" {
		t.Fatalf("data.recipe = %v; want quick", resp.Data.Recipe)
	}
	if resp.Next != "pick a recipe that declares lenses or a different batch scope" {
		t.Fatalf("data.next = %q; want the lens-less hint, not a severity hint", resp.Next)
	}
}

// TestRunRecipeUnknownRefuses proves a missing name surfaces the loader's
// typed refusal rather than an empty batch.
func TestRunRecipeUnknownRefuses(t *testing.T) {
	root, base := recipeRepo(t,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`)
	bundleDir := filepath.Join(root, ".verdict", "state", "local-fix")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixFile(t, root, ".verdict/state/local-fix/findings.json",
		"["+recipeFinding("perf-a", "perf.go", "perf")+"]")
	snapshotRecipes(t, bundleDir, base)

	var human strings.Builder
	_, ref := Run(t.Context(), recipeRequest(t, root, "nope", &human))
	if ref == nil || ref.Code != recipe.CodeUnknown {
		t.Fatalf("ref = %+v; want %s", ref, recipe.CodeUnknown)
	}
}

// TestRunRecipeSelectedIntersection proves the composite --selected --recipe
// run selects the intersection and keeps both flags in the re-run text.
func TestRunRecipeSelectedIntersection(t *testing.T) {
	findings := "[" + recipeFinding("perf-a", "perf.go", "perf") + "," +
		recipeFinding("sec-a", "sec.go", "security") + "]"
	root := recipeBundle(t,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`,
		"# lens perf — sha256:abc\nperf body\n# end lens perf\n# lens security — sha256:def\nsecurity body\n# end lens security\n",
		findings,
		`{"schema_version":"1","repo":"acme/app","pr":"1",`+
			`"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"selected_at":"2026-01-01T00:00:00Z","sigs":["perf-a","sec-a"]}`+"\n")

	var human strings.Builder
	req := recipeRequest(t, root, "quick", &human)
	req.Selected = true
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("Run: %+v", ref)
	}
	if got := batchSigs(t, resp); !slices.Equal(got, []string{"perf-a"}) {
		t.Fatalf("batch sigs = %v; want [perf-a] from the intersection", got)
	}
	if ptrValue(resp.Data.Recipe) != "quick" {
		t.Fatalf("data.recipe = %v; want quick", resp.Data.Recipe)
	}
	if ptrValue(resp.Data.Selection) == "" {
		t.Fatalf("data.selection = %v; want the selected.json path", resp.Data.Selection)
	}
	if !strings.Contains(human.String(), "--selected --recipe quick") {
		t.Fatalf("human output = %q; want the re-run text to carry both flags", human.String())
	}
	if !strings.Contains(resp.Next, "--selected --recipe quick") {
		t.Fatalf("data.next = %q; want both flags", resp.Next)
	}
}

// TestRunRecipeSelectedEmptyIntersection proves a composite run whose
// selection matches no lens reports both scopes and preserves data.recipe.
func TestRunRecipeSelectedEmptyIntersection(t *testing.T) {
	root := recipeBundle(t,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`,
		"# lens perf — sha256:abc\nperf body\n# end lens perf\n",
		"["+recipeFinding("sec-a", "sec.go", "security")+"]",
		`{"schema_version":"1","repo":"acme/app","pr":"1",`+
			`"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"selected_at":"2026-01-01T00:00:00Z","sigs":["sec-a"]}`+"\n")

	var human strings.Builder
	req := recipeRequest(t, root, "quick", &human)
	req.Selected = true
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("Run: %+v; want a completion naming both scopes, not a refusal", ref)
	}
	if len(resp.Data.Batch) != 0 {
		t.Fatalf("batch = %v; want zero rows for an empty intersection", resp.Data.Batch)
	}
	if ptrValue(resp.Data.Recipe) != "quick" {
		t.Fatalf("data.recipe = %v; want quick", resp.Data.Recipe)
	}
	if ptrValue(resp.Data.Selection) == "" {
		t.Fatalf("data.selection = %v; want the selected.json path", resp.Data.Selection)
	}
	if !strings.Contains(human.String(), "fix --selected --recipe quick") {
		t.Fatalf("human output = %q; want both flags named", human.String())
	}
	if !strings.Contains(human.String(), "under lenses perf") {
		t.Fatalf("human output = %q; want the lens set named", human.String())
	}
	if !strings.Contains(human.String(), "selected.json") {
		t.Fatalf("human output = %q; want the selection named", human.String())
	}
}

// TestRunRecipeForwardsPlanWarnings proves a lens the bundle does not load
// surfaces as a warning instead of vanishing.
func TestRunRecipeForwardsPlanWarnings(t *testing.T) {
	root, base := recipeRepo(t,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`)
	bundleDir := filepath.Join(root, ".verdict", "state", "local-fix")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixFile(t, root, ".verdict/state/local-fix/lenses.txt",
		"# lens security — sha256:def\nsecurity body\n# end lens security\n")
	writeFixFile(t, root, ".verdict/state/local-fix/findings.json",
		"["+recipeFinding("nolen", "plain.go", "")+"]")
	snapshotRecipes(t, bundleDir, base)

	var human strings.Builder
	resp, ref := Run(t.Context(), recipeRequest(t, root, "quick", &human))
	if ref != nil {
		t.Fatalf("Run: %+v", ref)
	}
	found := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, `"perf"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v; want one naming the lens the bundle does not load", resp.Warnings)
	}
}

// TestRunRecipeNoMatchNamesPathsScope proves the empty recipe message names
// the --paths scope alongside the lens set.
func TestRunRecipeNoMatchNamesPathsScope(t *testing.T) {
	root, base := recipeRepo(t,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`)
	bundleDir := filepath.Join(root, ".verdict", "state", "local-fix")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixFile(t, root, ".verdict/state/local-fix/lenses.txt",
		"# lens perf — sha256:abc\nperf body\n# end lens perf\n")
	writeFixFile(t, root, ".verdict/state/local-fix/findings.json",
		"["+recipeFinding("nolen", "plain.go", "")+"]")
	snapshotRecipes(t, bundleDir, base)

	var human strings.Builder
	req := recipeRequest(t, root, "quick", &human)
	req.Paths = []string{"docs/**"}
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("Run: %+v", ref)
	}
	if len(resp.Data.Batch) != 0 {
		t.Fatalf("batch = %v; want zero rows", resp.Data.Batch)
	}
	if !strings.Contains(human.String(), "under lenses perf under docs/**") {
		t.Fatalf("human output = %q; want the paths scope named", human.String())
	}
}

// TestRunRecipeNoMatchNamesLensSet proves a recipe whose lenses match no
// finding reports the scope instead of silently finishing.
func TestRunRecipeNoMatchNamesLensSet(t *testing.T) {
	root, base := recipeRepo(t,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`,
		`{"schema_version":"1","recipes":{"quick":{"version":"1.0.0","lenses":[{"id":"perf"}],"synthesize":{}}}}`)
	bundleDir := filepath.Join(root, ".verdict", "state", "local-fix")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixFile(t, root, ".verdict/state/local-fix/lenses.txt",
		"# lens perf — sha256:abc\nperf body\n# end lens perf\n")
	writeFixFile(t, root, ".verdict/state/local-fix/findings.json",
		"["+recipeFinding("nolen", "plain.go", "")+"]")
	snapshotRecipes(t, bundleDir, base)

	var human strings.Builder
	resp, ref := Run(t.Context(), recipeRequest(t, root, "quick", &human))
	if ref != nil {
		t.Fatalf("Run: %+v", ref)
	}
	if len(resp.Data.Batch) != 0 {
		t.Fatalf("batch = %v; want zero rows when no finding carries the lens", resp.Data.Batch)
	}
	if !strings.Contains(human.String(), "fix --recipe quick: no open inline finding at or above important under lenses perf") {
		t.Fatalf("human output = %q; want the recipe and lens set named", human.String())
	}
}
