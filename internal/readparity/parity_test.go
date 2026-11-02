// Package readparity_test holds the golden contract for the Go read
// verbs (report, next, show, ready, trend, calibrate, doctor, check-tree).
// The shared harness runs the Go binary on hermetic fixtures and pins exit
// codes, stdout/stderr, and every written file against checked-in goldens
// (go-port 08: the bash comparison retired with the shell). Readers are
// read-only by contract, so each leg additionally proves NO file is written
// or changed — except the explicitly requested artifacts
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

// goldenRead runs the Go binary on hermetic fixtures, pins exit codes,
// streams, and every written file against the checked-in golden, and proves
// the read-only contract: nothing outside allowedWrites changed.
// setup writes the bundle into dir (fixtures must be deterministic).
// extraEnv overlays variables.
func goldenRead(
	t *testing.T, name string,
	setup func(t *testing.T, dir, home string),
	extraEnv map[string]string, allowedWrites map[string]bool,
	args ...string,
) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Helper()
		t.Parallel()
		dir := t.TempDir()
		home := filepath.Join(t.TempDir(), "home")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		setup(t, dir, home)
		before := snapshotBoth(t, dir, home)
		out := harness.Run(t, dir, home, extraEnv, args...)
		parity.Check(t, name, parity.Render(out, []string{dir, home}))
		assertNoWrites(t, before, out.Files, allowedWrites, "go")
	})
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

// splitRepo builds argv for verbs taking repo+pr positionals. Every leg
// reviews acme/app; the helper hardcodes the repo so the legs read as the
// invocation they pin.
func splitRepo(cmd, pr string, flags ...string) []string {
	return append([]string{cmd, "acme/app", pr}, flags...)
}

// splitRepoOnly builds argv for trend (repo but no pr).
func splitRepoOnly(flags ...string) []string {
	return append([]string{"trend", "acme/app"}, flags...)
}

// splitBare builds argv for position-less verbs (doctor, check-tree).
func splitBare(cmd string, flags ...string) []string {
	return append([]string{cmd}, flags...)
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
