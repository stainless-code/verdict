// Package fix_test holds the golden contract for the Go fix verb: the
// shared harness runs the Go binary on hermetic local bundles inside a
// scratch git worktree and pins exit codes, stdout/stderr, the ledger, and
// every written file against checked-in goldens.
package fix_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/golden"
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

// batchVerifyCmd is the verify command every batch fixture hands the loop.
const batchVerifyCmd = "go test ./..."

// setupBatch is the three-tier batch fixture: one open inline sig per
// severity, so a floor and the default pick can be told apart.
func setupBatch(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "batch", "["+
		findingJSON(sigInfo, "open", "informational", "notes.txt", "Minor wording nit here.")+","+
		findingJSON(sigBatch, "open", "important", "other.txt", "Important scoping bug here.")+","+
		findingJSON(sigCritical, "open", "critical", "app.txt", "Critical boundary bug here.")+"]")
}

// setupBatchStopped is the mixed-outcome fixture: one sig at its ceiling and
// the rest ready, so the batch reports both instead of refusing.
func setupBatchStopped(t *testing.T, dir, _ string) {
	t.Helper()
	setupBatch(t, dir, "")
	seedLedger(t, dir, "batch", `{"sigs":{"`+sigBatch+`":{"passes":3}}}`+"\n")
}

// setupBatchPatch is the patch.json fallback fixture: the bundle's own patch
// descriptor carries the verify command, so --verify-cmd is not needed.
func setupBatchPatch(t *testing.T, dir, _ string) {
	t.Helper()
	setupBatch(t, dir, "")
	patch := `{"sig":"` + sigCritical + `","verifyCmd":"go vet ./..."}` + "\n"
	bdir := filepath.Join(dir, ".verdict", "state", "local-batch")
	if err := os.WriteFile(filepath.Join(bdir, "patch.json"), []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupBatchBelowFloor is the empty-batch fixture: every open finding sits
// below the default floor, so the batch selects nothing.
func setupBatchBelowFloor(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "low", "["+
		findingJSON(sigInfo, "open", "informational", "notes.txt", "Minor wording nit here.")+","+
		findingJSON(sigInfo2, "open", "informational", "app.txt", "Another nit here.")+"]")
}

// patchedAnchorContent is the worktree content a fix patch fixture holds: the
// anchor file patched the way an agent would after pass one.
const patchedAnchorContent = "alpha\nbeta fixed\ngamma\n"

// setupPatch is the patch-output fixture: one pass is on record and the
// anchor file carries the patch that pass asked for.
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

// setupPatchSecret is the same fixture with a credential in the patch: the
// patch output refuses before writing.
func setupPatchSecret(t *testing.T, dir, _ string) {
	t.Helper()
	setupPatch(t, dir, "")
	leak := "alpha\ntoken=ghp_0123456789abcdefghijklmnopqrstuvwxyz\ngamma\n"
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte(leak), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupPatchStale is the clean-worktree fixture: a previous pass left a patch,
// the agent reverted its change, so the stale diff must not survive.
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

// setupNotLocal is the PR-bundle refusal fixture.
func setupNotLocal(t *testing.T, dir, _ string) {
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

// setupMissing is the missing-findings fixture.
func setupMissing(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, ".verdict", "state", "local-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
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
}

// testFixBatch pins the --all batch: the plan for every selected sig, the
// floor, the mixed ceiling outcome, the patch.json fallback, and the typed
// refusals a batch adds.
// testFixPatch pins the patch output: fix.patch beside the ledger, the stale
// patch removed once the worktree is clean, and the secret-shaped refusal that
// writes neither the patch nor a pass.
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
	golden.Pin(t, harness, "not-local", setupNotLocal, nil,
		"fix", "acme/app", "4291", "--json")
	golden.Pin(t, harness, "missing-findings", setupMissing, nil,
		"fix", "acme/app", "local-empty", "--json")
	golden.Pin(t, harness, "unknown-flag", setupPass, nil,
		"fix", "acme/app", "local-pass", "--bogus")
}

// testFixGates covers the ceiling and scope-escape gate exits.
func testFixGates(t *testing.T) {
	t.Helper()
	golden.Pin(t, harness, "ceiling", setupCeiling, nil,
		"fix", "acme/app", "local-ceil", "--json")
	golden.Pin(t, harness, "scope-escape", setupScope, nil,
		"fix", "acme/app", "local-scope", "--json")
}
