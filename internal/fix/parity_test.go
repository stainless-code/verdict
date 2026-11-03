// Package fix_test holds the golden contract for the Go fix verb: the
// shared harness runs the Go binary on hermetic local bundles inside a
// scratch git worktree and pins exit codes, stdout/stderr, the ledger, and
// every written file against checked-in goldens (go-port 08: the bash
// comparison retired with the shell).
package fix_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
	"github.com/stainless-code/verdict/internal/testenv"
)

var harness *parity.Harness

// TestMain builds the Go binary once and runs the parity suite.
func TestMain(m *testing.M) {
	h, err := parity.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	harness = h
	code := m.Run()
	harness.Teardown()
	os.Exit(code)
}

// Finding sigs shared by the fix fixtures.
const (
	sigCritical = "c123456789abcdef0123456789abcdef01234567"
	sigInfo     = "1123456789abcdef0123456789abcdef01234567"
	sigInfo2    = "1123456789abcdef0123456789abcdef01234568"
	sigDone     = "2123456789abcdef0123456789abcdef01234567"
	sigCeiling  = "4123456789abcdef0123456789abcdef01234567"
	sigScope    = "5123456789abcdef0123456789abcdef01234567"
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
	bdir := filepath.Join(dir, ".review", "local-"+slug)
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
	bdir := filepath.Join(dir, ".review", "local-"+slug)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "fix.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
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

// setupNotLocal is the PR-bundle refusal fixture.
func setupNotLocal(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	bdir := filepath.Join(dir, ".review", "4291")
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
	if err := os.MkdirAll(filepath.Join(dir, ".review", "local-empty"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestFixParity proves byte parity across the default pick, explicit sigs,
// the completion and ledger gate exits, and every typed refusal.
func TestFixParity(t *testing.T) {
	t.Run("pick", testFixPick)
	t.Run("refusals", testFixRefusals)
	t.Run("gates", testFixGates)
}

// testFixPick covers the default pick, tiebreak, and completion.
func testFixPick(t *testing.T) {
	t.Helper()
	parity.Golden(t, harness, "pass-json", setupPass, nil,
		"fix", "acme/app", "local-pass", "--json")
	parity.Golden(t, harness, "pass-human", setupPass, nil,
		"fix", "acme/app", "local-pass")
	parity.Golden(t, harness, "explicit-sig-json", setupPass, nil,
		"fix", "acme/app", "local-pass", "--sig", sigInfo, "--json")
	parity.Golden(t, harness, "tiebreak-json", setupOrder, nil,
		"fix", "acme/app", "local-order", "--json")
	parity.Golden(t, harness, "complete-json", setupDone, nil,
		"fix", "acme/app", "local-done", "--json")
	parity.Golden(t, harness, "complete-human", setupDone, nil,
		"fix", "acme/app", "local-done")
}

// testFixRefusals covers the sig and bundle refusals plus usage.
func testFixRefusals(t *testing.T) {
	t.Helper()
	parity.Golden(t, harness, "bad-sig-status", setupBadSig, nil,
		"fix", "acme/app", "local-bad", "--sig", sigDone, "--json")
	parity.Golden(t, harness, "bad-sig-unknown", setupBadSig, nil,
		"fix", "acme/app", "local-bad", "--sig", "deadbeef", "--json")
	parity.Golden(t, harness, "not-local", setupNotLocal, nil,
		"fix", "acme/app", "4291", "--json")
	parity.Golden(t, harness, "missing-findings", setupMissing, nil,
		"fix", "acme/app", "local-empty", "--json")
	parity.Golden(t, harness, "unknown-flag", setupPass, nil,
		"fix", "acme/app", "local-pass", "--bogus")
}

// testFixGates covers the ceiling and scope-escape gate exits.
func testFixGates(t *testing.T) {
	t.Helper()
	parity.Golden(t, harness, "ceiling", setupCeiling, nil,
		"fix", "acme/app", "local-ceil", "--json")
	parity.Golden(t, harness, "scope-escape", setupScope, nil,
		"fix", "acme/app", "local-scope", "--json")
}
