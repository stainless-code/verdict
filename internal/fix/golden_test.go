// Package fix_test holds the golden contract for the Go fix verb: the
// shared harness runs the Go binary on hermetic local bundles inside a
// scratch git worktree and pins exit codes, stdout/stderr, the ledger, and
// every written file against checked-in goldens.
package fix_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/repro"
	"github.com/stainless-code/verdict/internal/testenv"
)

var harness *golden.Harness

// TestMain builds the Go binary once and runs the golden suite.
func TestMain(m *testing.M) {
	os.Exit(golden.RunMain(m, &harness))
}

// Finding sigs shared by the fix fixtures.
const (
	sigCritical = "c123456789abcdef0123456789abcdef01234567"
	sigInfo     = "1123456789abcdef0123456789abcdef01234567"
	sigInfo2    = "1123456789abcdef0123456789abcdef01234568"
	sigDone     = "2123456789abcdef0123456789abcdef01234567"
	sigCeiling  = "4123456789abcdef0123456789abcdef01234567"
	sigScope    = "5123456789abcdef0123456789abcdef01234567"
	sigBatch    = "6123456789abcdef0123456789abcdef01234567"
)

// findingJSON renders one valid store entry.
func findingJSON(sig, status, severity, path, body string) string {
	return `{"kind":"inline","path":"` + path + `",` +
		`"heading":null,"side":"RIGHT","line":2,"anchored":true,` +
		`"body":"` + body + `","suggestion":null,"inBounds":true,` +
		`"rigor":"pointed","severity":"` + severity + `","kb":[],` +
		`"sig":"` + sig + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"` + status + `",` +
		`"rationale":"","history":[]}`
}

// mkRepo initializes the scratch git worktree the fix audits read.
func mkRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = testenv.Base(map[string]string{
			"GIT_AUTHOR_NAME":     "fix",
			"GIT_AUTHOR_EMAIL":    "fix@eval",
			"GIT_COMMITTER_NAME":  "fix",
			"GIT_COMMITTER_EMAIL": "fix@eval",
		})
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	for name, content := range map[string]string{
		"notes.txt": "line1\nline2\nline3\n",
		"app.txt":   "alpha\nbeta\ngamma\n",
		"other.txt": "other1\nother2\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-qm", "init")
}

// mkLocal writes a local bundle with the given findings array.
func mkLocal(t *testing.T, dir, slug, findings string) {
	t.Helper()
	bdir := filepath.Join(dir, ".verdict", "state", "local-"+slug)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "findings.json"), []byte(findings), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedLedger writes a fix.json ledger for the fixture.
func seedLedger(t *testing.T, dir, slug, doc string) {
	t.Helper()
	bdir := filepath.Join(dir, ".verdict", "state", "local-"+slug)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "fix.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// briefJSON is the stored machine brief used by the fix brief fixture.
const briefJSON = `"brief":{"sites":[{"path":"app.txt","line":2},` +
	`{"path":"notes.txt"}],"outcome":"Reject duplicate deliveries before anything` +
	` is enqueued.","approach":"Track delivery ids in a bounded LRU checked at the` +
	` top of the handler.","questions":["Should the dedupe window be 24 hours or` +
	` 7 days?"]},`

// setupBrief is the stored-brief fixture: the fix pass prints the brief block
// between the anchors and the instructions.
func setupBrief(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	entry := strings.Replace(findingJSON(sigCritical, "open", "critical",
		"app.txt", "Critical boundary bug here."), `"suggestion":null,`, `"suggestion":null,`+briefJSON, 1)
	mkLocal(t, dir, "brief", "["+entry+"]")
}

// setupPass is the two-finding default-pick fixture.
func setupPass(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "pass", "["+
		findingJSON(sigInfo, "open", "informational", "notes.txt", "Minor wording nit here.")+","+
		findingJSON(sigCritical, "open", "critical", "app.txt", "Critical boundary bug here.")+"]")
}

// setupOrder is the equal-severity path tiebreak fixture.
func setupOrder(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "order", "["+
		findingJSON(sigInfo, "open", "important", "b.txt", "Bee file issue here.")+","+
		findingJSON(sigInfo2, "open", "important", "a.txt", "Aye file issue here.")+"]")
}

// setupBadSig is the addressed-finding refusal fixture.
func setupBadSig(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "bad", "["+
		findingJSON(sigInfo, "open", "informational", "notes.txt", "Open nit here.")+","+
		findingJSON(sigDone, "addressed", "informational", "notes.txt", "Settled nit.")+"]")
}

// setupDone is the terminal-completion fixture.
func setupDone(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "done", "["+
		findingJSON(sigDone, "addressed", "informational", "notes.txt", "Settled nit.")+"]")
}

// setupCeiling is the 3-pass ceiling fixture.
func setupCeiling(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "ceil", "["+
		findingJSON(sigCeiling, "open", "important", "notes.txt", "Stubborn bug here.")+"]")
	seedLedger(t, dir, "ceil", `{"sigs":{"`+sigCeiling+`":{"passes":3}}}`+"\n")
}

// setupScope is the scope-escape fixture: a pass is on record and a tracked
// file outside the anchor changed after the baseline.
func setupScope(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "scope", "["+
		findingJSON(sigScope, "open", "important", "notes.txt", "Scoped bug here.")+"]")
	seedLedger(t, dir, "scope", `{"sigs":{"`+sigScope+`":{"passes":1,`+
		`"last_at":"2026-01-01T00:00:00Z","baseline":["app.txt","notes.txt"]}}}`+"\n")
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other1\ntouched\nother2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const batchVerifyCmd = "go test ./..."

func setupBatch(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "batch", "["+
		findingJSON(sigInfo, "open", "informational", "notes.txt", "Minor wording nit here.")+","+
		findingJSON(sigBatch, "open", "important", "other.txt", "Important scoping bug here.")+","+
		findingJSON(sigCritical, "open", "critical", "app.txt", "Critical boundary bug here.")+"]")
}

func setupBatchStopped(t *testing.T, dir, _ string) {
	t.Helper()
	setupBatch(t, dir, "")
	seedLedger(t, dir, "batch", `{"sigs":{"`+sigBatch+`":{"passes":3}}}`+"\n")
}

func setupBatchPatch(t *testing.T, dir, _ string) {
	t.Helper()
	setupBatch(t, dir, "")
	patch := `{"sig":"` + sigCritical + `","verifyCmd":"go vet ./..."}` + "\n"
	bdir := filepath.Join(dir, ".verdict", "state", "local-batch")
	if err := os.WriteFile(filepath.Join(bdir, "patch.json"), []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupBatchBelowFloor(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "low", "["+
		findingJSON(sigInfo, "open", "informational", "notes.txt", "Minor wording nit here.")+","+
		findingJSON(sigInfo2, "open", "informational", "app.txt", "Another nit here.")+"]")
}

// findingLensJSON renders one valid store entry carrying lens provenance.
func findingLensJSON(sig, severity, path, body, lens string) string {
	return `{"kind":"inline","path":"` + path + `",` +
		`"heading":null,"side":"RIGHT","line":2,"anchored":true,` +
		`"body":"` + body + `","suggestion":null,"inBounds":true,` +
		`"rigor":"pointed","severity":"` + severity + `","kb":[],` +
		`"lens":"` + lens + `",` +
		`"sig":"` + sig + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[]}`
}

// setupRecipe is the lens-provocation fixture: the bundle loads two lenses
// and three findings carry one each — one perf, one security, one without.
func setupRecipe(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	lenses := "# lens perf — sha256:abc\nperf body\n# end lens perf\n" +
		"# lens security — sha256:def\nsecurity body\n# end lens security\n"
	bdir := filepath.Join(dir, ".verdict", "state", "local-recipe")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "lenses.txt"), []byte(lenses), 0o644); err != nil {
		t.Fatal(err)
	}
	mkLocal(t, dir, "recipe", "["+
		findingLensJSON(sigBatch, "important", "other.txt", "Important perf bug here.", "perf")+","+
		findingLensJSON(sigInfo, "important", "app.txt", "Important security bug here.", "security")+","+
		findingJSON(sigCritical, "open", "critical", "notes.txt", "Critical unprovenanced bug.")+"]")
}

// setupRecipeEmpty is the zero-lens fixture: no bundle lenses and no
// committed recipes anywhere, so the built-in default resolves lens-less.
func setupRecipeEmpty(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "emptylens", "["+
		findingLensJSON(sigBatch, "important", "other.txt", "Important perf bug here.", "perf")+"]")
}

// setupRecipeSelected is the composite fixture: the reaction selection
// names two sigs, one inside the recipe lens set and one outside it.
func setupRecipeSelected(t *testing.T, dir, _ string) {
	t.Helper()
	setupRecipe(t, dir, "")
	seedSelected(t, dir, "local-recipe", sigBatch, sigCritical)
}

// setupRecipeSelectedEmpty is the composite empty-intersection fixture: the
// selection names only the unprovenanced finding, which sits outside every
// lens the bundle loads.
func setupRecipeSelectedEmpty(t *testing.T, dir, _ string) {
	t.Helper()
	setupRecipe(t, dir, "")
	seedSelected(t, dir, "local-recipe", sigCritical)
}

const patchedAnchorContent = "alpha\nbeta fixed\ngamma\n"

func setupPatch(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "patched", "["+
		findingJSON(sigBatch, "open", "important", "app.txt", "Important scoping bug here.")+"]")
	seedLedger(t, dir, "patched", `{"sigs":{"`+sigBatch+`":{"passes":1,`+
		`"last_at":"2026-01-01T00:00:00Z","baseline":["app.txt","notes.txt","other.txt"]}}}`+"\n")
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte(patchedAnchorContent), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupPatchSecret(t *testing.T, dir, _ string) {
	t.Helper()
	setupPatch(t, dir, "")
	leak := "alpha\ntoken=ghp_0123456789abcdefghijklmnopqrstuvwxyz\ngamma\n"
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte(leak), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupBatchLedger(t *testing.T, dir, _ string) {
	t.Helper()
	setupBatch(t, dir, "")
	seedLedger(t, dir, "batch", "{\n  \"sigs\": {\n    \""+sigBatch+"\": {\"passes\": 2}\n  }\n}\n")
}

func setupPatchStale(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "stale", "["+
		findingJSON(sigBatch, "open", "important", "app.txt", "Important scoping bug here.")+"]")
	stale := "diff --git a/app.txt b/app.txt\n--- a/app.txt\n+++ b/app.txt\n@@ -1,3 +1,3 @@\n alpha\n-beta\n+beta stale\n gamma\n"
	bdir := filepath.Join(dir, ".verdict", "state", "local-stale")
	if err := os.WriteFile(filepath.Join(bdir, "fix.patch"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupSelected is the reaction-scoped batch fixture: the fetch-written
// selection names two of three open inline findings.
func setupSelected(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "sel", "["+
		findingJSON(sigInfo, "open", "informational", "notes.txt", "Minor wording nit here.")+","+
		findingJSON(sigCritical, "open", "critical", "app.txt", "Critical boundary bug here.")+","+
		findingJSON(sigBatch, "open", "important", "other.txt", "Important scoping bug here.")+"]")
	seedSelected(t, dir, "local-sel", sigInfo, sigBatch)
}

// setupSelectedAddressed is the selection whose sigs are all settled.
func setupSelectedAddressed(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "sold", "["+
		findingJSON(sigInfo, "addressed", "informational", "notes.txt", "Settled nit here.")+"]")
	seedSelected(t, dir, "local-sold", sigInfo)
}

// setupSelectedCorrupt is the present-but-unreadable selection fixture.
func setupSelectedCorrupt(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "sbad", "["+
		findingJSON(sigInfo, "open", "informational", "notes.txt", "Minor nit here.")+"]")
	writeSelected(t, dir, "local-sbad", `{"sigs":"nope"}`)
}

// setupSelectedEmpty is the selection that names no sig at all: the empty list
// must refuse, because Select reads an empty wanted set as "no filter".
func setupSelectedEmpty(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "snone", "["+
		findingJSON(sigInfo, "open", "informational", "notes.txt", "Minor nit here.")+"]")
	writeSelected(t, dir, "local-snone", `{"sigs":[]}`)
}

// setupSelectedPR is the PR-bundle fixture: --selected is the batch that
// reads a bundle fetch wrote, so it runs here and lands through post.
func setupSelectedPR(t *testing.T, dir, _ string) {
	t.Helper()
	setupPRBundle(t, dir, "")
	seedSelected(t, dir, "4291", sigInfo)
}

// seedSelected writes the selection a GitHub fetch records.
func seedSelected(t *testing.T, dir, bundle string, sigs ...string) {
	t.Helper()
	rows := make([]string, 0, len(sigs))
	for _, sig := range sigs {
		rows = append(rows, `"`+sig+`"`)
	}
	writeSelected(t, dir, bundle, `{"schema_version":"1","repo":"acme/app","pr":"1",`+
		`"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
		`"selected_at":"2026-01-01T00:00:00Z","sigs":[`+strings.Join(rows, ",")+`]}`)
}

func writeSelected(t *testing.T, dir, bundle, doc string) {
	t.Helper()
	bdir := filepath.Join(dir, ".verdict", "state", bundle)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "selected.json"), []byte(doc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupPRBundle is the PR-bundle fixture: a bundle fetch wrote, with findings
// but no PR.json, so a default fix pass runs on it and lands through post.
func setupPRBundle(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	bdir := filepath.Join(dir, ".verdict", "state", "4291")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "findings.json"), []byte("["+
		findingJSON(sigInfo, "open", "informational", "notes.txt", "PR finding.")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupCorpus is the corpus-bundle fixture: fix refuses benchmark bundles.
func setupCorpus(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	bdir := filepath.Join(dir, ".verdict", "state", "corpus-x")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "findings.json"), []byte("["+
		findingJSON(sigInfo, "open", "informational", "notes.txt", "Corpus finding.")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupMissing is the missing-findings fixture.
func setupMissing(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, ".verdict", "state", "local-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// seedReproBundle captures a real artifact for sig in the local-<slug> bundle
// and stores a reproduced finding whose receipt names it.
func seedReproBundle(t *testing.T, dir, slug, sig string, artifact []byte) {
	t.Helper()
	seedReproBundleCmd(t, dir, slug, sig, artifact, "python3 {artifact}")
}

// seedReproBundleCmd is seedReproBundle with the capture command spelled by the
// fixture, so the receipt-rewrite legs can vary the reference.
func seedReproBundleCmd(t *testing.T, dir, slug, sig string, artifact []byte, command string) {
	t.Helper()
	bdir := localBundleDir(dir, slug)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "probe.py"), artifact, 0o644); err != nil {
		t.Fatal(err)
	}
	receipt, err := repro.Capture(bdir, sig, "probe.py", command, false)
	if err != nil {
		t.Fatal(err)
	}
	receiptJSON, err := json.Marshal(receiptDoc{
		Path: receipt.Path, Command: receipt.Command, Redacted: receipt.Redacted,
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := strings.Replace(findingJSON(sig, "open", "important", "app.txt", "Reproduced bug here."),
		`"rigor":"pointed"`, `"rigor":"reproduced"`, 1)
	entry = entry[:len(entry)-1] + `,"reproduction":` + string(receiptJSON) + `}`
	if err := os.WriteFile(filepath.Join(bdir, "findings.json"), []byte("["+entry+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// receiptDoc is the reproduction receipt as the store spells it, marshaled
// rather than concatenated so a Windows bundle path escapes its backslashes
// instead of breaking the JSON.
type receiptDoc struct {
	Path     string `json:"path"`
	Command  string `json:"command"`
	Redacted bool   `json:"redacted"`
}

// setupTestPromote is the promotion fixture: one reproduced inline finding
// with a retained artifact.
func setupTestPromote(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	seedReproBundle(t, dir, "test", sigBatch, []byte("assert False\n"))
}

// setupTestCommit dirties an unrelated tracked file so the commit fixture
// proves only the promoted file is staged and committed.
func setupTestCommit(t *testing.T, dir, _ string) {
	t.Helper()
	setupTestPromote(t, dir, "")
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other1\ndirty\nother2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupTestDotSlash spells the receipt reference with a leading ./ so the
// promotion golden proves that spelling rewrites to the promoted path.
func setupTestDotSlash(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	seedReproBundleCmd(t, dir, "test", sigBatch, []byte("assert False\n"), "sh ./{artifact}")
}

// localBundleDir is the bundle seedReproBundle captures into.
func localBundleDir(dir, slug string) string {
	return filepath.Join(dir, ".verdict", "state", "local-"+slug)
}

// setupTestAbsoluteCmd spells the receipt reference as an absolute path into
// the bundle, which promotion leaves alone: only the relative spelling moves.
func setupTestAbsoluteCmd(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	const slug = "test"
	seedReproBundleCmd(t, dir, slug, sigBatch, []byte("assert False\n"),
		"sh "+filepath.ToSlash(localBundleDir(dir, slug))+"/{artifact}")
}

// setupTestConflict pre-places different bytes at the promoted destination.
func setupTestConflict(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	seedReproBundle(t, dir, "test", sigBatch, []byte("assert False\n"))
	// The artifact hash names the destination; pre-place different bytes there.
	preplaceConflict(t, dir)
}

// preplaceConflict copies each retained artifact name to the promoted
// directory with different bytes, skipping command receipts.
func preplaceConflict(t *testing.T, dir string) {
	t.Helper()
	seeded := filepath.Join(dir, ".verdict", "state", "local-test", "tests")
	entries, err := os.ReadDir(seeded)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".command.json") {
			continue
		}
		preplaceEntry(t, dir, e.Name())
	}
}

// preplaceEntry writes one artifact name with different bytes at the
// promoted destination.
func preplaceEntry(t *testing.T, dir, name string) {
	t.Helper()
	destDir := filepath.Join(dir, ".verdict", "tests")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, name), []byte("different bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupTestUnreproduced stores an open inline finding at rigor=ran with no
// reproduction receipt.
func setupTestUnreproduced(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	entry := strings.Replace(findingJSON(sigInfo, "open", "important", "app.txt", "Unreproduced bug here."),
		`"rigor":"pointed"`, `"rigor":"ran"`, 1)
	mkLocal(t, dir, "test", "["+entry+"]")
}

// TestFixGolden proves byte parity across the default pick, explicit sigs,
// the completion and ledger gate exits, and every typed refusal.
func TestFixGolden(t *testing.T) {
	t.Run("pick", testFixPick)
	t.Run("refusals", testFixRefusals)
	t.Run("gates", testFixGates)
	t.Run("brief", testFixBrief)
	t.Run("batch", testFixBatch)
	t.Run("patch", testFixPatch)
	t.Run("selected", testFixSelected)
	t.Run("promotion", testFixPromotion)
	t.Run("recipe", testFixRecipe)
}

// testFixRecipe pins the --recipe batch surface: selection by stored lens,
// the empty-scope report for a lens-less recipe, and the flag refusals.
func testFixRecipe(t *testing.T) {
	t.Helper()
	golden.Pin(t, harness, "recipe-json", setupRecipe, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "default", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "recipe-human", setupRecipe, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "default", "--verify-cmd", batchVerifyCmd)
	golden.Pin(t, harness, "recipe-paths", setupRecipe, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "default", "--verify-cmd", batchVerifyCmd,
		"--paths", "other.txt", "--json")
	golden.Pin(t, harness, "recipe-empty", setupRecipeEmpty, nil,
		"fix", "acme/app", "local-emptylens", "--recipe", "default", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "recipe-paths-empty", setupRecipe, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "default", "--verify-cmd", batchVerifyCmd,
		"--paths", "docs/**", "--json")
	golden.Pin(t, harness, "recipe-selected-json", setupRecipeSelected, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "default", "--selected", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "recipe-selected-human", setupRecipeSelected, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "default", "--selected", "--verify-cmd", batchVerifyCmd)
	golden.Pin(t, harness, "recipe-selected-empty-json", setupRecipeSelectedEmpty, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "default", "--selected", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "recipe-selected-empty-human", setupRecipeSelectedEmpty, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "default", "--selected", "--verify-cmd", batchVerifyCmd)
	golden.Pin(t, harness, "recipe-unknown", setupRecipe, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "nope", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "recipe-with-sig", setupRecipe, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "default", "--sig", sigInfo, "--json")
	golden.Pin(t, harness, "recipe-with-test", setupRecipe, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "default", "--test", sigInfo, "--json")
	golden.Pin(t, harness, "recipe-empty-name", setupRecipe, nil,
		"fix", "acme/app", "local-recipe", "--recipe", "", "--json")
}

// testFixPromotion pins the --test promotion surface: the promote and commit
// writes, the different-bytes refusal, the unreproduced refusal, and the
// flag-combination refusals.
func testFixPromotion(t *testing.T) {
	t.Helper()
	golden.Pin(t, harness, "test-json", setupTestPromote, nil,
		"fix", "acme/app", "local-test", "--test", sigBatch, "--json")
	golden.Pin(t, harness, "test-human", setupTestPromote, nil,
		"fix", "acme/app", "local-test", "--test", sigBatch)
	golden.Pin(t, harness, "test-commit", setupTestCommit, nil,
		"fix", "acme/app", "local-test", "--test", sigBatch, "--commit", "--json")
	golden.Pin(t, harness, "test-conflict", setupTestConflict, nil,
		"fix", "acme/app", "local-test", "--test", sigBatch, "--json")
	golden.Pin(t, harness, "test-unreproduced", setupTestUnreproduced, nil,
		"fix", "acme/app", "local-test", "--test", sigInfo, "--json")
	golden.Pin(t, harness, "test-all-conflict", setupTestPromote, nil,
		"fix", "acme/app", "local-test", "--test", sigBatch, "--all", "--json")
	golden.Pin(t, harness, "test-commit-without-test", setupTestPromote, nil,
		"fix", "acme/app", "local-test", "--commit", "--json")
	golden.Pin(t, harness, "test-empty", setupTestPromote, nil,
		"fix", "acme/app", "local-test", "--test", "", "--json")
	golden.Pin(t, harness, "test-dot-slash", setupTestDotSlash, nil,
		"fix", "acme/app", "local-test", "--test", sigBatch, "--json")
	golden.Pin(t, harness, "test-absolute", setupTestAbsoluteCmd, nil,
		"fix", "acme/app", "local-test", "--test", sigBatch)
}

func testFixPatch(t *testing.T) {
	t.Helper()
	golden.Pin(t, harness, "patch-json", setupPatch, nil,
		"fix", "acme/app", "local-patched", "--json")
	golden.Pin(t, harness, "patch-human", setupPatch, nil,
		"fix", "acme/app", "local-patched")
	golden.Pin(t, harness, "patch-secret", setupPatchSecret, nil,
		"fix", "acme/app", "local-patched", "--json")
	golden.Pin(t, harness, "patch-stale", setupPatchStale, nil,
		"fix", "acme/app", "local-stale", "--json")
	golden.Pin(t, harness, "patch-batch", setupPatch, nil,
		"fix", "acme/app", "local-patched", "--all", "--verify-cmd", batchVerifyCmd, "--json")
}

func testFixBatch(t *testing.T) {
	t.Helper()
	golden.Pin(t, harness, "batch-json", setupBatch, nil,
		"fix", "acme/app", "local-batch", "--all", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "batch-human", setupBatch, nil,
		"fix", "acme/app", "local-batch", "--all", "--verify-cmd", batchVerifyCmd)
	golden.Pin(t, harness, "batch-floor", setupBatch, nil,
		"fix", "acme/app", "local-batch", "--all", "--min-severity", "critical",
		"--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "batch-stopped", setupBatchStopped, nil,
		"fix", "acme/app", "local-batch", "--all", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "batch-patch-cmd", setupBatchPatch, nil,
		"fix", "acme/app", "local-batch", "--all", "--min-severity", "critical", "--json")
	golden.Pin(t, harness, "batch-no-verify", setupBatch, nil,
		"fix", "acme/app", "local-batch", "--all", "--json")
	golden.Pin(t, harness, "batch-empty", setupBatchBelowFloor, nil,
		"fix", "acme/app", "local-low", "--all", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "batch-complete", setupDone, nil,
		"fix", "acme/app", "local-done", "--all", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "batch-bad-floor", setupBatch, nil,
		"fix", "acme/app", "local-batch", "--all", "--min-severity", "high",
		"--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "batch-bad-floor-empty", setupDone, nil,
		"fix", "acme/app", "local-done", "--all", "--min-severity", "high",
		"--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "batch-conflict", setupBatch, nil,
		"fix", "acme/app", "local-batch", "--all", "--sig", sigInfo)
	golden.Pin(t, harness, "batch-no-verify-ledger", setupBatchLedger, nil,
		"fix", "acme/app", "local-batch", "--all", "--json")
	golden.Pin(t, harness, "batch-paths", setupBatch, nil,
		"fix", "acme/app", "local-batch", "--all", "--verify-cmd", batchVerifyCmd,
		"--paths", "other.txt", "--json")
	golden.Pin(t, harness, "batch-paths-glob", setupBatch, nil,
		"fix", "acme/app", "local-batch", "--all", "--verify-cmd", batchVerifyCmd,
		"--paths", "app.*", "--paths", "other.txt")
	golden.Pin(t, harness, "batch-paths-empty", setupBatch, nil,
		"fix", "acme/app", "local-batch", "--all", "--verify-cmd", batchVerifyCmd,
		"--paths", "docs/**", "--json")
	golden.Pin(t, harness, "batch-paths-without-all", setupBatch, nil,
		"fix", "acme/app", "local-batch", "--paths", "src/**")
}

// testFixPick covers the default pick, tiebreak, and completion.
func testFixPick(t *testing.T) {
	t.Helper()
	golden.Pin(t, harness, "pass-json", setupPass, nil,
		"fix", "acme/app", "local-pass", "--json")
	golden.Pin(t, harness, "pass-human", setupPass, nil,
		"fix", "acme/app", "local-pass")
	golden.Pin(t, harness, "explicit-sig-json", setupPass, nil,
		"fix", "acme/app", "local-pass", "--sig", sigInfo, "--json")
	golden.Pin(t, harness, "tiebreak-json", setupOrder, nil,
		"fix", "acme/app", "local-order", "--json")
	golden.Pin(t, harness, "complete-json", setupDone, nil,
		"fix", "acme/app", "local-done", "--json")
	golden.Pin(t, harness, "complete-human", setupDone, nil,
		"fix", "acme/app", "local-done")
}

// testFixBrief pins the human and json shapes of a stored brief.
func testFixBrief(t *testing.T) {
	t.Helper()
	golden.Pin(t, harness, "brief-human", setupBrief, nil,
		"fix", "acme/app", "local-brief")
	golden.Pin(t, harness, "brief-json", setupBrief, nil,
		"fix", "acme/app", "local-brief", "--json")
}

// testFixRefusals covers the sig and bundle refusals plus usage.
func testFixRefusals(t *testing.T) {
	t.Helper()
	golden.Pin(t, harness, "bad-sig-status", setupBadSig, nil,
		"fix", "acme/app", "local-bad", "--sig", sigDone, "--json")
	golden.Pin(t, harness, "bad-sig-unknown", setupBadSig, nil,
		"fix", "acme/app", "local-bad", "--sig", "deadbeef", "--json")
	golden.Pin(t, harness, "pr-bundle-json", setupPRBundle, nil,
		"fix", "acme/app", "4291", "--json")
	golden.Pin(t, harness, "pr-bundle-human", setupPRBundle, nil,
		"fix", "acme/app", "4291")
	golden.Pin(t, harness, "corpus-bundle", setupCorpus, nil,
		"fix", "acme/app", "corpus-x", "--json")
	golden.Pin(t, harness, "missing-findings", setupMissing, nil,
		"fix", "acme/app", "local-empty", "--json")
	golden.Pin(t, harness, "unknown-flag", setupPass, nil,
		"fix", "acme/app", "local-pass", "--bogus")
}

// testFixSelected pins the reaction-scoped batch: fetch's selected.json is the
// scope, the output says so, and every way it can fail to name work refuses.
func testFixSelected(t *testing.T) {
	t.Helper()
	golden.Pin(t, harness, "selected-json", setupSelected, nil,
		"fix", "acme/app", "local-sel", "--selected", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "selected-human", setupSelected, nil,
		"fix", "acme/app", "local-sel", "--selected", "--verify-cmd", batchVerifyCmd)
	golden.Pin(t, harness, "selected-pr", setupSelectedPR, nil,
		"fix", "acme/app", "4291", "--selected", "--verify-cmd", batchVerifyCmd)
	golden.Pin(t, harness, "selected-missing", setupBatch, nil,
		"fix", "acme/app", "local-batch", "--selected", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "selected-addressed", setupSelectedAddressed, nil,
		"fix", "acme/app", "local-sold", "--selected", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "selected-corrupt", setupSelectedCorrupt, nil,
		"fix", "acme/app", "local-sbad", "--selected", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "selected-empty", setupSelectedEmpty, nil,
		"fix", "acme/app", "local-snone", "--selected", "--verify-cmd", batchVerifyCmd, "--json")
	golden.Pin(t, harness, "selected-conflict", setupSelected, nil,
		"fix", "acme/app", "local-sel", "--selected", "--sig", sigInfo)
	golden.Pin(t, harness, "selected-and-all", setupSelected, nil,
		"fix", "acme/app", "local-sel", "--selected", "--all", "--verify-cmd", batchVerifyCmd)
	golden.Pin(t, harness, "selected-paths", setupSelected, nil,
		"fix", "acme/app", "local-sel", "--selected", "--verify-cmd", batchVerifyCmd,
		"--paths", "notes.txt", "--json")
}

// testFixGates covers the ceiling and scope-escape gate exits.
func testFixGates(t *testing.T) {
	t.Helper()
	golden.Pin(t, harness, "ceiling", setupCeiling, nil,
		"fix", "acme/app", "local-ceil", "--json")
	golden.Pin(t, harness, "scope-escape", setupScope, nil,
		"fix", "acme/app", "local-scope", "--json")
}
