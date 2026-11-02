// Package readparity_test holds the shell-parity proof for the Go read
// verbs (report, next, show, ready, trend, calibrate, doctor, check-tree).
// The shared harness runs BOTH scripts/review.sh and the Go binary on
// identical fixtures and compares exit codes, stdout/stderr (timestamps
// scrubbed only), and every written file byte for byte. Readers are
// read-only by contract, so each leg additionally proves NO file is written
// or changed on either side — except the explicitly requested artifacts
// (--output/--sarif targets, calibrate's kb-grades.jsonl, ready's probe
// err file), which still compare byte-exact across the two sides. Gate
// flags (--check tiers, freshness, live-thread skip/unreachable), --json
// envelopes, and the corrupt-store typed refusals all have legs. Every leg
// runs parallel with per-case temp dirs.
package readparity_test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
)

var harness *parity.Harness

// TestMain builds the Go binary once and runs the read-parity suite.
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

// tsRe scrubs timestamps; everything else compares byte-exact.
var tsRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`)

// scrub normalizes timestamps and nothing else.
func scrub(s string) string {
	return tsRe.ReplaceAllString(s, "0000-00-00T00:00:00Z")
}

// scrubRoots normalizes the two sandbox roots and homes to placeholders.
// The four roots are pairwise distinct temp dirs; replacement order puts
// the longer (home) paths first so nested prefixes cannot shadow.
func scrubRoots(s string, roots [4]string) string {
	ordered := append([]string{}, roots[:]...)
	slices.SortFunc(ordered, func(a, b string) int { return len(b) - len(a) })
	for _, r := range ordered {
		if r == "" {
			continue
		}
		s = strings.ReplaceAll(s, r, "ROOT")
	}
	return s
}

// isGitPath skips .git internals (hashes and timestamps, never graded;
// the evals grade worktree files, not the object store).
func isGitPath(p string) bool {
	return slices.Contains(strings.Split(filepath.ToSlash(p), "/"), ".git")
}

// snapshotTree reads every file under root (except .git internals),
// keyed by a tag plus the relative path. It runs BEFORE the invocation
// so the read-only legs can prove nothing was written or changed.
func snapshotTree(t *testing.T, tag, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, snapshotVisit(tag, root, out))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// snapshotVisit returns the Walk callback recording file bytes into out.
// Unreadable entries abort the snapshot: silently skipping them would
// hide real writes.
func snapshotVisit(tag, root string, out map[string]string) filepath.WalkFunc {
	return func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() || isGitPath(p) {
			return nil
		}
		return snapshotFile(tag, root, out, p)
	}
}

// snapshotFile records one file's bytes into out.
func snapshotFile(tag, root string, out map[string]string, p string) error {
	raw, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return err
	}
	out[tag+"/"+rel] = string(raw)
	return nil
}

// mergeMaps unions two file maps.
func mergeMaps(a, b map[string]string) map[string]string {
	maps.Copy(a, b)
	return a
}

// snapshotBoth snapshots the sandbox and home trees with the harness tags.
func snapshotBoth(t *testing.T, dir, home string) map[string]string {
	t.Helper()
	return mergeMaps(snapshotTree(t, "C", dir), snapshotTree(t, "H", home))
}

// runReadBoth runs bash and Go on identical fixtures and compares
// everything: exit codes, streams, and every written file (R6), plus the
// read-only proof that neither side wrote or changed any file outside
// allowedWrites (requested artifacts that still compare byte-exact).
// setup writes the bundle into dir (called once per side, so fixtures
// must be deterministic). extraEnv overlays variables on both sides.
// bashArgs are the script argv; goArgs are the binary argv (the binary
// always receives cmd+repo+pr+flags, with empty positionals for
// position-less verbs — see splitRepo/splitRepoOnly/splitBare).
func runReadBoth(
	t *testing.T, name string,
	setup func(t *testing.T, dir, home string),
	extraEnv map[string]string, allowedWrites map[string]bool,
	bashArgs, goArgs []string,
) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Helper()
		t.Parallel()
		dirA := t.TempDir()
		homeA := filepath.Join(t.TempDir(), "home")
		dirB := t.TempDir()
		homeB := filepath.Join(t.TempDir(), "home")
		for _, h := range []string{homeA, homeB} {
			if err := os.MkdirAll(h, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		setup(t, dirA, homeA)
		setup(t, dirB, homeB)
		beforeA := snapshotBoth(t, dirA, homeA)
		beforeB := snapshotBoth(t, dirB, homeB)
		roots := [4]string{dirA, homeA, dirB, homeB}
		a := harness.Run(t, false, dirA, homeA, extraEnv, bashArgs...)
		b := harness.Run(t, true, dirB, homeB, extraEnv, goArgs...)
		compareReadOutputs(t, a, b, roots)
		assertNoWrites(t, beforeA, a.Files, allowedWrites, "bash")
		assertNoWrites(t, beforeB, b.Files, allowedWrites, "go")
	})
}

// compareReadOutputs compares exit codes, captured streams, and every
// written file of the two sides.
func compareReadOutputs(t *testing.T, a, b parity.Outputs, roots [4]string) {
	t.Helper()
	compareReadStreams(t, a, b, roots)
	compareReadFiles(t, a, b, roots)
}

// compareReadStreams compares exit codes and captured streams.
func compareReadStreams(t *testing.T, a, b parity.Outputs, roots [4]string) {
	t.Helper()
	if a.Status != b.Status {
		t.Errorf("exit: bash=%d go=%d\nbash stderr:\n%s\ngo stderr:\n%s",
			a.Status, b.Status, scrubRoots(a.Stderr, roots), scrubRoots(b.Stderr, roots))
	}
	as, bs := scrub(scrubRoots(a.Stdout, roots)), scrub(scrubRoots(b.Stdout, roots))
	if as != bs {
		t.Errorf("stdout differs:\n--- bash ---\n%s\n--- go ---\n%s", a.Stdout, b.Stdout)
	}
	ae, be := scrub(scrubRoots(a.Stderr, roots)), scrub(scrubRoots(b.Stderr, roots))
	if ae != be {
		t.Errorf("stderr differs:\n--- bash ---\n%s\n--- go ---\n%s", a.Stderr, b.Stderr)
	}
}

// compareReadFiles compares every written file byte for byte.
func compareReadFiles(t *testing.T, a, b parity.Outputs, roots [4]string) {
	t.Helper()
	seen := map[string]bool{}
	for k, va := range a.Files {
		seen[k] = true
		vb, ok := b.Files[k]
		if !ok {
			t.Errorf("file presence differs: %s bash=present go=absent", k)
			continue
		}
		compareReadFile(t, k, va, vb, roots)
	}
	for k := range b.Files {
		if !seen[k] {
			t.Errorf("file presence differs: %s bash=absent go=present", k)
		}
	}
}

// compareReadFile compares one file present on both sides.
func compareReadFile(t *testing.T, k, va, vb string, roots [4]string) {
	t.Helper()
	fa, fb := scrub(scrubRoots(va, roots)), scrub(scrubRoots(vb, roots))
	if fa != fb {
		t.Errorf("file differs: %s\n--- bash ---\n%s\n--- go ---\n%s", k, va, vb)
	}
}

// assertNoWrites proves the read-only contract: every file present after
// the run matches the pre-run snapshot, except explicitly requested
// artifacts (which compareReadOutputs already byte-compares across
// sides). Deletions fail too — readers remove nothing.
func assertNoWrites(
	t *testing.T, before, after map[string]string,
	allowedWrites map[string]bool, side string,
) {
	t.Helper()
	for k, va := range after {
		// Requested artifacts are byte-compared across sides by
		// compareReadOutputs; they are exempt from the no-change
		// proof on both sides.
		if allowedWrites[k] {
			continue
		}
		checkKeptFile(t, k, va, before, side)
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			t.Errorf("%s deleted file: %s", side, k)
		}
	}
}

// checkKeptFile proves one pre-existing file survived the run unchanged.
func checkKeptFile(t *testing.T, k, va string, before map[string]string, side string) {
	t.Helper()
	vb, ok := before[k]
	if !ok {
		t.Errorf("%s wrote unexpected file: %s", side, k)
		return
	}
	if scrub(va) != scrub(vb) {
		t.Errorf("%s changed file: %s", side, k)
	}
}

// splitRepo builds argv for verbs taking repo+pr positionals: identical
// on both sides. Every leg reviews acme/app; the helper hardcodes the
// repo so the legs read as the dispatch they pin.
func splitRepo(cmd, pr string, flags ...string) ([]string, []string) {
	bash := append([]string{cmd, "acme/app", pr}, flags...)
	goArgs := append([]string{cmd, "acme/app", pr}, flags...)
	return bash, goArgs
}

// splitRepoOnly builds argv for trend (repo but no pr): the binary
// receives the empty pr positional the shell dispatch would pass.
func splitRepoOnly(flags ...string) ([]string, []string) {
	bash := append([]string{"trend", "acme/app"}, flags...)
	goArgs := append([]string{"trend", "acme/app", ""}, flags...)
	return bash, goArgs
}

// splitBare builds argv for position-less verbs (doctor, check-tree):
// the binary receives the empty positionals the shell dispatch passes.
func splitBare(cmd string, flags ...string) ([]string, []string) {
	bash := append([]string{cmd}, flags...)
	goArgs := append([]string{cmd, "", ""}, flags...)
	return bash, goArgs
}

// allowKeys builds the allowed-writes set for requested artifacts.
func allowKeys(keys ...string) map[string]bool {
	out := map[string]bool{}
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// Heads and sigs shared by the fixtures (40-hex findings sigs).
const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	headC = "cccccccccccccccccccccccccccccccccccccccc"

	sigCrit  = "c111111111111111111111111111111111111111"
	sigImp   = "b222222222222222222222222222222222222222"
	sigInfo  = "a333333333333333333333333333333333333333"
	sigAddr  = "d444444444444444444444444444444444444444"
	sigDef   = "e555555555555555555555555555555555555555"
	sigSect  = "f666666666666666666666666666666666666666"
	sigShowA = "a111111111111111111111111111111111111111"
	sigShowB = "a111111122222222222222222222222222222222"
)

// finding renders one inline findings entry.
func finding(sig, status, severity, rigor, kind string) string {
	return fmt.Sprintf(`{"kind":%q,"path":"notes.txt","side":"RIGHT","line":2,`+
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,`+
		`"inBounds":true,"rigor":%q,"severity":%q,"sig":%q,"sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":%q,"rationale":"","history":[]}`,
		kind, rigor, severity, sig, status)
}

// findingsDoc wraps entries in a findings array.
func findingsDoc(entries ...string) string {
	return "[" + strings.Join(entries, ",") + "]"
}

// mixedFindings is the canonical mixed store: whole-store open counting,
// tier gates, kind/status filters, and the ranked order all read it.
func mixedFindings() string {
	return findingsDoc(
		finding(sigCrit, "open", "critical", "walked", "inline"),
		finding(sigImp, "open", "important", "pointed", "inline"),
		finding(sigInfo, "open", "informational", "said", "inline"),
		finding(sigAddr, "addressed", "important", "walked", "inline"),
		finding(sigDef, "deferred", "critical", "reproduced", "inline"),
		finding(sigSect, "open", "informational", "pointed", "section"),
	)
}

// prDoc renders a minimal GitHub-shaped PR.json against the fixed base.
func prDoc(head string) string {
	return fmt.Sprintf(`{"state":"OPEN","headRefOid":%q,"baseRefOid":%q,"author":{"login":"teammate"}}`,
		head, headB)
}

// sourceDoc renders a local bundle source.json.
func sourceDoc(head string) string {
	return fmt.Sprintf(
		`{"schema_version":"1","kind":"range","repo":"acme/app","slug":"work",`+
			`"head_ref":"v2","base_sha":%q,"head_sha":%q,"dirty":false}`,
		headB, head)
}

// fullDiff is the one-hunk fixture diff.
const fullDiff = `diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
`

// postDoc renders a completed-post record for head with review id.
func postDoc(head string, id int) string {
	return fmt.Sprintf(`{"schema_version":"1","review":{"id":%d},`+
		`"head":%q,"posted_at":"2026-01-01T00:00:00Z","confidence":null,"security_summary":null}`,
		id, head)
}

// writeFiles writes relpath->content fixtures under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// mkPRBundle writes a PR bundle shell (PR.json + diff); findings and
// extras layer on per leg.
func mkPRBundle(t *testing.T, dir, pr string) {
	t.Helper()
	writeFiles(t, dir, map[string]string{
		".review/" + pr + "/PR.json":   prDoc(headA),
		".review/" + pr + "/full.diff": fullDiff,
	})
}

// mkLocalBundle writes a local bundle shell.
func mkLocalBundle(t *testing.T, dir, slug string) {
	t.Helper()
	writeFiles(t, dir, map[string]string{
		".review/" + slug + "/source.json": sourceDoc(headA),
		".review/" + slug + "/full.diff":   fullDiff,
	})
}
