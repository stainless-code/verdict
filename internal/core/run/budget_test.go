package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// docsRoles marks every fixture row as documentation, which turns the ordinary
// four-file source fixture into the docs-only tree the light tier exists for.
const docsRoles = "## docs\n- a.go\n- old.go\n- notes.txt\n- elsewhere.txt\n"

// sensitiveCapture is a captured run record whose defaults carry one repository
// sure-fire glob. Written as literals so the resolver is graded against what a
// fetch writes, not against a value the test rebuilt.
const sensitiveCapture = `{"schema_version":"1","resolved":{"defaults":{"values":{"sensitive_paths":["**/a.go"]},` +
	`"origins":{"sensitive_paths":{"layer":"repo","source":"base"}}}}}`

// budgetCapture is a captured run record whose defaults ask for the classifying
// depth, exactly as fetch stores a repository's defaults.budget.
const budgetCapture = `{"schema_version":"1","resolved":{"defaults":{"values":{"budget":"auto"},` +
	`"origins":{"budget":{"layer":"repo","source":"base"}}}}}`

// documentedFiles is the PR document's file list: the four rows the shared
// fixture diff changes, which is both the gate's file count and the row order
// every other topology surface reads.
const documentedFiles = `"files":[{"path":"a.go"},{"path":"old.go"},{"path":"notes.txt"},{"path":"elsewhere.txt"}]`

// documentedHead is the two-finder fixture's PR document: the head the plan
// stamps plus the file list.
const documentedHead = `{"headRefOid":"head",` + documentedFiles + `}`

// docsOnlyBundle builds the fixture and marks its rows documentation.
func docsOnlyBundle(t *testing.T) (root, dir string) {
	t.Helper()
	root = fixtureRoot(t)
	dir = bundleDir(root)
	writeUnder(t, dir, "roles.txt", docsRoles)
	writeUnder(t, dir, "PR.json", `{"number":42,"state":"OPEN",`+documentedFiles+`}`)
	return root, dir
}

// sizedCoordinatorFixture builds the two-finder fixture with file rows in its
// PR document, so the classifier reads four files instead of the zero a
// row-less PR document reports.
func sizedCoordinatorFixture(t *testing.T) (*core.RunRequest, *phaseCommander) {
	t.Helper()
	req, commander := coordinatorFixture(t)
	writeUnder(t, commander.dir, "PR.json", documentedHead)
	return req, commander
}

// launcherObject reads a bundle's launcher.json as a wire object.
func launcherObject(t *testing.T, dir string) *jq.Obj {
	t.Helper()
	return readPhaseObject(t, filepath.Join(dir, launcherFile))
}

// budgetOf reads the recorded budget object out of one launcher.json or
// --json data profile.
func budgetOf(t *testing.T, obj *jq.Obj) *jq.Obj {
	t.Helper()
	budget, ok := readcommon.FieldOf(obj, "budget").(*jq.Obj)
	if !ok {
		t.Fatalf("no budget object in %s", obj.MarshalCompact())
	}
	return budget
}

// dataBudget reads the recorded decision out of one --json data profile.
func dataBudget(t *testing.T, data *core.RunData) *jq.Obj {
	t.Helper()
	wire := core.Wire(data.Budget)
	obj, ok := wire.(*jq.Obj)
	if !ok {
		t.Fatalf("data.budget = %T; want an object", wire)
	}
	return obj
}

// assertBudget pins one recorded decision against literals.
func assertBudget(t *testing.T, budget *jq.Obj, want map[string]any) {
	t.Helper()
	for key, expected := range want {
		got := readcommon.FieldOf(budget, key)
		switch wantTyped := expected.(type) {
		case []string:
			assertBudgetStrings(t, key, got, wantTyped)
		case int:
			assertBudgetInt(t, key, got, wantTyped)
		default:
			assertBudgetScalar(t, key, got, wantTyped)
		}
	}
}

// assertBudgetStrings grades one recorded reason list in order. A non-array
// wire value — null included — fails here instead of passing silently as an
// empty list, which is what an empty want would otherwise accept.
func assertBudgetStrings(t *testing.T, key string, got any, want []string) {
	t.Helper()
	if _, ok := got.([]any); !ok {
		t.Errorf("budget.%s = %v; want a JSON array", key, got)
		return
	}
	var values []string
	for _, item := range jq.Elements(got) {
		values = append(values, jq.StrOf(item))
	}
	if !slices.Equal(values, want) {
		t.Errorf("budget.%s = %q; want %q", key, values, want)
	}
}

// assertBudgetInt grades one recorded count.
func assertBudgetInt(t *testing.T, key string, got any, want int) {
	t.Helper()
	if n, ok := intOf(got); !ok || n != want {
		t.Errorf("budget.%s = %v; want %d", key, got, want)
	}
}

// assertBudgetScalar grades one recorded string.
func assertBudgetScalar(t *testing.T, key string, got, want any) {
	t.Helper()
	text, ok := want.(string)
	if !ok {
		t.Fatalf("budget.%s expectation %v is not a string literal", key, want)
	}
	if jq.StrOf(got) != text {
		t.Errorf("budget.%s = %v; want %q", key, got, want)
	}
}

// TestBudgetAutoLightensDocsOnly proves a docs-only tree launches one finder,
// records the light decision beside the launch, and names the rule that decided
// it.
func TestBudgetAutoLightensDocsOnly(t *testing.T) {
	t.Parallel()
	root, dir := docsOnlyBundle(t)
	commander := &scripted{write: func() { writeReview(t, dir) }}
	req := request(root, commander)
	req.FindersRaw = ""
	req.BudgetRaw = "auto"
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if commander.calls != 1 || response.Data.Execution != nil {
		t.Fatalf("launches = %d, execution = %+v; want the single-finder shape", commander.calls, response.Data.Execution)
	}
	assertBudget(t, dataBudget(t, response.Data), map[string]any{
		"tier": "light", "source": "flag", "requested": "auto", "files": 4,
		"diff_bytes": len(fixtureDiff), "finders": 1, "cross_check": "off",
		"reasons": []string{"docs-or-test-only"},
	})
	assertBudget(t, budgetOf(t, launcherObject(t, dir)), map[string]any{
		"tier": "light", "finders": 1, "cross_check": "off", "source": "flag",
		"reasons": []string{"docs-or-test-only"},
	})
}

// renamedHead is the fixture PR document with one row renamed out of a
// sensitive tree: the host records the old path as `previous_filename`, which
// the bundle reader must carry into the classifier. The four rows and the hunk
// receipts of `fixtureDiff` are untouched, so only the rename signal differs
// from an ordinary fixture run.
const renamedHead = `{"number":42,"state":"OPEN","headRefOid":"head","files":[` +
	`{"path":"a.go","previous_filename":"auth/token.go"},` +
	`{"path":"old.go"},{"path":"notes.txt"},{"path":"elsewhere.txt"}]}`

// renamedDiff is a diff-only bundle: one ordinary section and one pure rename
// with no hunks, so the rename section owes no receipt and the fallback reader
// must take the old path from the rename headers.
const renamedDiff = `diff --git a/src/app.go b/src/app.go
--- a/src/app.go
+++ b/src/app.go
@@ -1 +1 @@
-old
+new
diff --git a/auth/token.go b/lib/token.go
similarity index 95%
rename from auth/token.go
rename to lib/token.go
`

// combinedHead is a PR document as `gh pr view --json files` writes it: rows
// only, and no `previous_filename` on the renamed row. The rename source exists
// solely in the bundled diff, which is what a real GitHub bundle looks like.
const combinedHead = `{"number":42,"state":"OPEN","headRefOid":"head","files":[` +
	`{"path":"a.go"},{"path":"old.go"},{"path":"notes.txt"},` +
	`{"path":"elsewhere.txt"},{"path":"lib/token.go"}]}`

// renameSection appends one hunks-free rename to the shared fixture diff, so
// the receipts of `fixtureDiff` still cover every hunk and only the rename
// signal is new.
const renameSection = "diff --git a/auth/token.go b/lib/token.go\n" +
	"similarity index 95%\nrename from auth/token.go\nrename to lib/token.go\n"

// TestBudgetRenameCombinesPRJSONAndDiff proves the two bundle readers agree: a
// GitHub bundle carries both `PR.json` rows (no `previous_filename`) and a
// `full.diff` that names the rename source, and preferring the PR rows must not
// discard the path the diff measured.
func TestBudgetRenameCombinesPRJSONAndDiff(t *testing.T) {
	t.Parallel()
	req, commander := sizedCoordinatorFixture(t)
	writeUnder(t, commander.dir, "PR.json", combinedHead)
	writeUnder(t, commander.dir, "full.diff", fixtureDiff+renameSection)
	req.FindersRaw = ""
	req.BudgetRaw = "auto"
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
	assertBudget(t, dataBudget(t, response.Data), map[string]any{
		"tier": "deep", "requested": "auto", "files": 5, "finders": 2,
		"reasons": []string{"sensitive-path: **/auth/**"},
	})
}

// TestBudgetRenameFromPRJSON proves a rename the host recorded still selects
// deep: the row's own path is harmless, and only its previous path matches the
// built-in auth glob.
func TestBudgetRenameFromPRJSON(t *testing.T) {
	t.Parallel()
	req, commander := sizedCoordinatorFixture(t)
	writeUnder(t, commander.dir, "PR.json", renamedHead)
	req.FindersRaw = ""
	req.BudgetRaw = "auto"
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
	assertBudget(t, dataBudget(t, response.Data), map[string]any{
		"tier": "deep", "requested": "auto", "files": 4, "finders": 2,
		"reasons": []string{"sensitive-path: **/auth/**"},
	})
}

// TestBudgetRenameFromDiff proves the diff fallback carries the same signal
// when PR.json is absent: a `rename from` section renames the file out of the
// auth tree, and the classifier still sees the path it came from.
func TestBudgetRenameFromDiff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeUnder(t, dir, "full.diff", renamedDiff)
	st := &state{req: &core.RunRequest{BudgetRaw: "auto"}}
	recorded, ref := st.resolveBudget(dir, nil)
	if ref != nil {
		t.Fatal(ref)
	}
	if recorded.Tier != "deep" || recorded.Files != 2 || recorded.DiffBytes != len(renamedDiff) {
		t.Fatalf("budget = %+v; want deep over 2 files and %d bytes", recorded, len(renamedDiff))
	}
	if want := []string{"sensitive-path: **/auth/**"}; !slices.Equal(recorded.Reasons, want) {
		t.Fatalf("reasons = %q; want %q", recorded.Reasons, want)
	}
}

// intOf reads a wire integer: structObj emits Go ints as int64, which jq.IntOf
// only accepts as a number literal.
func intOf(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return jq.IntOf(v)
	}
}

// TestBudgetAutoEscalatesSensitivePath proves a configured sensitive glob buys
// the coordinator's two-finder pass and records both the escalation and the
// finder budget as the source of the count.
func TestBudgetAutoEscalatesSensitivePath(t *testing.T) {
	t.Parallel()
	req, commander := sizedCoordinatorFixture(t)
	writeCapture(t, commander.dir, sensitiveCapture)
	req.FindersRaw = ""
	req.BudgetRaw = "auto"
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
	assertBudget(t, dataBudget(t, response.Data), map[string]any{
		"tier": "deep", "requested": "auto", "finders": 2, "cross_check": "opposite-finder",
		"reasons": []string{"sensitive-path: **/a.go"},
	})
	assertBudget(t, budgetOf(t, launcherObject(t, commander.dir)), map[string]any{
		"tier": "deep", "finders": 2, "reasons": []string{"sensitive-path: **/a.go"},
	})
	capture := readPhaseObject(t, filepath.Join(commander.dir, "run.json"))
	if got := readcommon.FieldOf(capture, "finder_source"); got != "budget" {
		t.Fatalf("finder_source = %v; want budget", got)
	}
}

// TestBudgetConfigDefault proves a captured defaults.budget decides with
// source config when no flag asked for a depth.
func TestBudgetConfigDefault(t *testing.T) {
	t.Parallel()
	root, dir := docsOnlyBundle(t)
	writeCapture(t, dir, budgetCapture)
	commander := &scripted{write: func() { writeReview(t, dir) }}
	req := request(root, commander)
	req.FindersRaw = ""
	req.BudgetRaw = ""
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if commander.calls != 1 {
		t.Fatalf("launches = %d; want 1", commander.calls)
	}
	assertBudget(t, dataBudget(t, response.Data), map[string]any{
		"tier": "light", "source": "config", "requested": "auto", "finders": 1,
		"reasons": []string{"docs-or-test-only"},
	})
}

// TestFinderFlagOutranksBudgetFlag proves the precedence chain's first rank:
// --finders wins, and the record says which lever won and what it launched.
func TestFinderFlagOutranksBudgetFlag(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	commander := &scripted{write: func() { writeReview(t, dir) }}
	req := request(root, commander)
	req.BudgetRaw = "deep"
	response, ref := Run(t.Context(), req) // request() sets FindersRaw = "1"
	if ref != nil {
		t.Fatal(ref)
	}
	if commander.calls != 1 || response.Data.Execution != nil {
		t.Fatalf("launches = %d; want --finders 1 to win", commander.calls)
	}
	assertBudget(t, dataBudget(t, response.Data), map[string]any{
		"tier": "deep", "requested": "deep", "finders": 1, "cross_check": "off",
		"overridden_by": "finders", "reasons": []string{},
	})
}

// TestFinderFlagSkipsCapturedBudget proves the first rank decides without
// classifying at all: an explicit --finders never consults the captured
// defaults.budget, so the run records no budget object to override.
func TestFinderFlagSkipsCapturedBudget(t *testing.T) {
	t.Parallel()
	req, commander := sizedCoordinatorFixture(t)
	writeCapture(t, commander.dir, budgetCapture)
	req.FindersRaw = "2"
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
	if response.Data.Budget != nil {
		t.Fatalf("data.budget = %+v; want absent", response.Data.Budget)
	}
	if _, present := launcherObject(t, commander.dir).Get("budget"); present {
		t.Fatalf("an explicit --finders recorded a budget it never consulted: %s",
			launcherObject(t, commander.dir).MarshalCompact())
	}
}

// TestBudgetFlagOutranksCapturedFinders proves a requested depth replaces a
// captured finder count: a repository-wide finders: 1 speed setting can never
// single-finder a diff the classifier escalated.
func TestBudgetFlagOutranksCapturedFinders(t *testing.T) {
	t.Parallel()
	req, commander := sizedCoordinatorFixture(t)
	writeCapture(t, commander.dir,
		`{"schema_version":"1","resolved":{"defaults":{"values":{"finders":1},"origins":{"finders":{"layer":"repo","source":"base"}}}}}`)
	req.FindersRaw = ""
	req.BudgetRaw = "standard"
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	assertPhaseGroups(t, commander.launched(),
		[]string{"x-finder-1", "x-finder-2"}, []string{"x-cross-1", "x-cross-2"})
	assertBudget(t, dataBudget(t, response.Data), map[string]any{
		"tier": "standard", "source": "flag", "requested": "standard", "finders": 2,
	})
}

// TestBudgetRenderingRefusesTwoFinders proves a reconciled deep or standard
// tier trips the existing --print/--stage refusal instead of inventing a second
// one, and does it before anything reaches disk.
func TestBudgetRenderingRefusesTwoFinders(t *testing.T) {
	t.Parallel()
	req, commander := sizedCoordinatorFixture(t)
	writeCapture(t, commander.dir, sensitiveCapture)
	req.BudgetRaw = "auto"
	req.FindersRaw = ""
	req.Print = true
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != "bad-flags" || !strings.Contains(ref.Message, "--finders 1") {
		t.Fatalf("refusal = %v", ref)
	}
	if launched := commander.launched(); len(launched) != 0 {
		t.Fatalf("render launched %v", launched)
	}
	if _, err := os.Stat(filepath.Join(commander.dir, launcherFile)); !os.IsNotExist(err) {
		t.Fatalf("render wrote the launcher record: %v", err)
	}
}

// TestBudgetInvalidValuesRefuse proves both the flag value and a captured value
// refuse typed before any launch, naming the whole vocabulary.
func TestBudgetInvalidValuesRefuse(t *testing.T) {
	t.Parallel()
	t.Run("flag", assertInvalidBudgetFlag)
	t.Run("captured", assertInvalidCapturedBudget)
}

// assertInvalidBudgetFlag grades an unknown --budget: the exact message and no
// spawned process.
func assertInvalidBudgetFlag(t *testing.T) {
	t.Helper()
	root := fixtureRoot(t)
	commander := &scripted{write: func() {}}
	req := request(root, commander)
	req.BudgetRaw = "fast"
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != "bad-flags" || ref.Message != "--budget must be auto|light|standard|deep" {
		t.Fatalf("refusal = %v", ref)
	}
	if commander.calls != 0 {
		t.Fatalf("an invalid --budget launched %d processes", commander.calls)
	}
}

// assertInvalidCapturedBudget grades an unknown captured defaults.budget the
// same way, with no flag in play.
func assertInvalidCapturedBudget(t *testing.T) {
	t.Helper()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	writeCapture(t, dir,
		`{"schema_version":"1","resolved":{"defaults":{"values":{"budget":"fast"}}}}`)
	commander := &scripted{write: func() { writeReview(t, dir) }}
	req := request(root, commander)
	req.FindersRaw = ""
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != "bad-flags" || !strings.Contains(ref.Message, "captured budget must be") {
		t.Fatalf("refusal = %v", ref)
	}
	if commander.calls != 0 {
		t.Fatalf("an invalid captured budget launched %d processes", commander.calls)
	}
}

// TestDrainNeverReachesTheClassifier proves drain keeps its one-finder launch
// even when a depth is requested: a drain never classifies, so it records no
// budget at all.
func TestDrainNeverReachesTheClassifier(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	commander := &scripted{write: func() {
		writeUnder(t, dir, drainFile, `{"schema_version":"1"}`)
	}}
	req := request(root, commander)
	req.FindersRaw = ""
	req.BudgetRaw = "deep"
	response, ref := Drain(t.Context(), req, "digest")
	if ref != nil {
		t.Fatal(ref)
	}
	if commander.calls != 1 || response.Data.Budget != nil {
		t.Fatalf("launches = %d, budget = %+v; want one finder and no budget", commander.calls, response.Data.Budget)
	}
	manifest := launcherObject(t, dir)
	if _, present := manifest.Get("budget"); present {
		t.Fatalf("drain recorded a budget: %s", manifest.MarshalCompact())
	}
}

// TestNoBudgetKeepsArtifactsUnchanged proves an ordinary run writes no budget
// key and carries no budget in --json, the byte-identical claim the omitempty
// fields exist for.
func TestNoBudgetKeepsArtifactsUnchanged(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	commander := &scripted{write: func() { writeReview(t, dir) }}
	response, ref := Run(t.Context(), request(root, commander))
	if ref != nil {
		t.Fatal(ref)
	}
	if response.Data.Budget != nil {
		t.Fatalf("data.budget = %+v; want absent", response.Data.Budget)
	}
	manifest := launcherObject(t, dir)
	if _, present := manifest.Get("budget"); present {
		t.Fatalf("launcher.json grew a budget without one being asked for: %s", manifest.MarshalCompact())
	}
}

// lineRecorder keeps the human lines a service streams.
type lineRecorder struct {
	lines []string
}

func (r *lineRecorder) Human(line string) { r.lines = append(r.lines, line) }
func (r *lineRecorder) Warn(string)       {}
func (r *lineRecorder) Diag(string)       {}

// TestBudgetHumanLineNamesTheDecision proves the operator sees the tier, the
// measurements, and every reason before the launch costs anything.
func TestBudgetHumanLineNamesTheDecision(t *testing.T) {
	req, commander := sizedCoordinatorFixture(t)
	writeCapture(t, commander.dir, sensitiveCapture)
	recorder := &lineRecorder{}
	req.Progress = recorder
	req.FindersRaw = ""
	req.BudgetRaw = "auto"
	if _, ref := Run(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	const want = "review budget: deep (4 files, 960 bytes) — sensitive-path: **/a.go"
	if !slices.Contains(recorder.lines, want) {
		t.Fatalf("human lines = %q; want %q among them", recorder.lines, want)
	}
}
