// Package fix_test holds the shell-parity proof for the Go fix verb: the
// shared harness runs BOTH scripts/review.sh fix and the Go binary on
// identical local bundles (in a scratch git repo for the worktree audits)
// and compares exit codes, stdout/stderr (timestamps scrubbed only), and
// every written file byte for byte. Fix writes only the ledger.
package fix_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
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

const (
	sigCrit  = "c123456789abcdef0123456789abcdef01234567"
	sigInfo  = "1123456789abcdef0123456789abcdef01234567"
	sigInfo2 = "1123456789abcdef0123456789abcdef01234568"
	sigAddr  = "2123456789abcdef0123456789abcdef01234567"
	sigSect  = "3123456789abcdef0123456789abcdef01234567"
	sigCeil  = "4123456789abcdef0123456789abcdef01234567"
	sigScop  = "5123456789abcdef0123456789abcdef01234567"
)

// mkRepo initializes the sandbox scratch git repo (notes.txt, app.txt,
// other.txt committed; .review ignored).
func mkRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=fix", "GIT_AUTHOR_EMAIL=fix@eval",
			"GIT_COMMITTER_NAME=fix", "GIT_COMMITTER_EMAIL=fix@eval")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".review/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other1\nother2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")
}

// mkLocal crafts a local bundle with findings.
func mkLocal(t *testing.T, dir, slug, findings string) {
	t.Helper()
	bdir := filepath.Join(dir, ".review", "local-"+slug)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `{"schema_version":"1","kind":"worktree","repo":"acme/app","slug":"` + slug + `","base_ref":null,"head_ref":"work","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_sha":"","range":"HEAD:worktree","dirty":true,"diff_sha256":"0","files":1,"additions":1,"deletions":1,"tool_version":"0.0.0-test","model":null,"created_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(bdir, "source.json"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	diff := "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"
	if err := os.WriteFile(filepath.Join(bdir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "findings.json"), []byte(findings), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkFinding builds one inline/section finding row.
func mkFinding(sig, kind, status, sev, path, body string) string {
	if kind == "inline" {
		return `{"kind":"inline","path":"` + path + `","side":"RIGHT","line":2,"anchored":true,"body":"` + body + `","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"` + sev + `","kb":[],"sig":"` + sig + `","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"` + status + `","rationale":"","history":[]}`
	}
	return `{"kind":"section","path":null,"heading":"Notes","side":"RIGHT","line":2,"anchored":true,"body":"` + body + `","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"` + sev + `","kb":[],"sig":"` + sig + `","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"` + status + `","rationale":"","history":[]}`
}

// baseSetup crafts the pick bundle (severity + tiebreak pair).
func baseSetup(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "fixpick", "["+mkFinding(sigInfo, "inline", "open", "informational", "notes.txt", "Minor wording nit here.")+","+mkFinding(sigCrit, "inline", "open", "critical", "app.txt", "Critical boundary bug here.")+"]")
	mkLocal(t, dir, "fixorder", "["+mkFinding(sigInfo, "inline", "open", "important", "b.txt", "Bee file issue here.")+","+mkFinding(sigInfo2, "inline", "open", "important", "a.txt", "Aye file issue here.")+"]")
}

// setupExplicitSig crafts the explicit-sig bundle.
func setupExplicitSig(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "fixbad", "["+mkFinding(sigInfo, "inline", "open", "informational", "notes.txt", "Open nit here.")+","+mkFinding(sigAddr, "inline", "addressed", "informational", "notes.txt", "Settled nit.")+","+mkFinding(sigSect, "section", "open", "informational", "notes.txt", "Section note.")+"]")
}

// setupSigTable crafts the sig-table bundle with two open inline sigs.
func setupSigTable(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "fixbad", "["+mkFinding(sigInfo, "inline", "open", "informational", "notes.txt", "Open nit here.")+","+mkFinding(sigAddr, "inline", "addressed", "informational", "notes.txt", "Settled nit.")+","+mkFinding(sigSect, "section", "open", "informational", "notes.txt", "Section note.")+","+mkFinding(sigInfo2, "inline", "open", "informational", "notes.txt", "Second open nit.")+"]")
}

// setupNotLocal crafts a non-local bundle dir.
func setupNotLocal(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	bdir := filepath.Join(dir, ".review", "4291")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "PR.json"), []byte(`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupMissingFindings crafts a local bundle without findings.
func setupMissingFindings(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	bdir := filepath.Join(dir, ".review", "local-nofind")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// setupCorruptStore crafts a local bundle with a corrupt store.
func setupCorruptStore(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "fixcorrupt", `[{"sig":"nope"`)
}

// setupCompletion crafts a local bundle with no open findings.
func setupCompletion(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "fixdone", "["+mkFinding(sigAddr, "inline", "addressed", "informational", "notes.txt", "Settled nit.")+"]")
}

// setupKbRecall crafts a local bundle with kb recall content.
func setupKbRecall(t *testing.T, dir, _ string) {
	t.Helper()
	mkRepo(t, dir)
	mkLocal(t, dir, "fixkb", "["+mkFinding(sigInfo, "inline", "open", "informational", "notes.txt", "Kb-guided nit.")+"]")
	kb := "# kb recall — context, never orders. Cited rules inform the review; evidence still decides.\n# fixkb-rule — notes.txt — precision 0/0\nCheck return values on every syscall in this file, seven-seven-seven.\n"
	if err := os.WriteFile(filepath.Join(dir, ".review", "local-fixkb", "kb.txt"), []byte(kb), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runPickCases runs the default pick and tiebreak parity cases.
func runPickCases(t *testing.T) {
	t.Helper()
	harness.RunBoth(t, "default-pick", baseSetup, nil, false,
		"fix", "acme/app", "local-fixpick", "--json")
	harness.RunBoth(t, "default-pick-human", baseSetup, nil, false,
		"fix", "acme/app", "local-fixpick")
	harness.RunBoth(t, "tiebreak-path-asc", baseSetup, nil, false,
		"fix", "acme/app", "local-fixorder", "--json")
	harness.RunBoth(t, "explicit-sig", setupExplicitSig, nil, false,
		"fix", "acme/app", "local-fixbad", "--sig", sigInfo, "--json")
}

// runSigTableCases runs the sig-table refusal parity cases.
func runSigTableCases(t *testing.T) {
	t.Helper()
	for _, tc := range []struct{ name, sig, code string }{
		{"sig-addressed", sigAddr, "bad-sig"},
		{"sig-section", sigSect, "bad-sig"},
		{"sig-unknown", "deadbeef", "bad-sig"},
		{"sig-ambiguous", "1123", "ambiguous-sig"},
	} {
		harness.RunBoth(t, tc.name, setupSigTable, nil, false,
			"fix", "acme/app", "local-fixbad", "--sig", tc.sig, "--json")
	}
}

// runEdgeCases runs the bundle-edge parity cases.
func runEdgeCases(t *testing.T) {
	t.Helper()
	harness.RunBoth(t, "not-local", setupNotLocal, nil, false,
		"fix", "acme/app", "4291", "--json")
	harness.RunBoth(t, "missing-findings", setupMissingFindings, nil, false,
		"fix", "acme/app", "local-nofind", "--json")
	harness.RunBoth(t, "corrupt-store", setupCorruptStore, nil, false,
		"fix", "acme/app", "local-fixcorrupt", "--json")
	harness.RunBoth(t, "completion", setupCompletion, nil, false,
		"fix", "acme/app", "local-fixdone", "--json")
	harness.RunBoth(t, "kb-recall", setupKbRecall, nil, false,
		"fix", "acme/app", "local-fixkb", "--json")
	harness.RunBoth(t, "unknown-flag", baseSetup, nil, false,
		"fix", "acme/app", "local-fixpick", "--bogus")
}

// TestFixParity proves byte parity across the fix pass, selection,
// refusals, ceiling, scope-escape, completion, and ledger edges.
func TestFixParity(t *testing.T) {
	runPickCases(t)
	runSigTableCases(t)
	runEdgeCases(t)
}
