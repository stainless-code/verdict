// Package corpustest writes the on-disk corpus and replay bundles the
// corpus-facing entry points grade. It is a fixture package for tests in other
// packages, not a production dependency.
package corpustest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The shared fixture is a real race case. Its bug line is added by the diff,
// and the finding names only the built-in phrase the spelled diff carries.
const (
	CaseID    = "race-filter"
	RunID     = "race-run"
	BugPath   = "pool.go"
	BugLine   = 45
	Finding   = "The goroutine closes the conn after the pool takes it back."
	CleanBody = "This is a race between the pool and the borrower."
)

const (
	dirMode  = 0o755
	fileMode = 0o600
)

const manifest = `{
  "schema_version": "1",
  "id": "race-filter",
  "kind": "real",
  "language": "go",
  "provenance": {
    "kind": "real",
    "repo": "urfave/cli",
    "pr": 2319,
    "base_sha": "1111111111111111111111111111111111111111",
    "head_sha": "2222222222222222222222222222222222222222",
    "fix_commit": "3333333333333333333333333333333333333333"
  },
  "bugs": [
    {"path": "pool.go", "line": 45, "kind": "race", "severity": "important"}
  ]
}
`

func findings(body string) string {
	return fmt.Sprintf(`[
  {
    "kind": "inline",
    "path": "pool.go",
    "side": "RIGHT",
    "line": 45,
    "anchored": true,
    "body": %q,
    "suggestion": null,
    "rigor": "walked",
    "severity": "important",
    "sig": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "status": "open"
  }
]
`, body)
}

const sampling = `{"run_id":"race-run","k":1,"repeats":%d,"sample":%d,"severity_weights":{"critical":25,"important":5,"informational":1}}`

// SpelledDiff adds the bug line and carries the race phrase in a comment,
// the shape a real race-fix diff has.
const SpelledDiff = `diff --git a/pool.go b/pool.go
--- a/pool.go
+++ b/pool.go
@@ -44,2 +44,3 @@ func borrow() *Conn {
+	// the goroutine closes the conn after the pool takes it back
+	c := pool.get()
 return c
`

// CleanDiff adds the same line without naming any race phrase.
const CleanDiff = `diff --git a/pool.go b/pool.go
--- a/pool.go
+++ b/pool.go
@@ -44,2 +44,3 @@ func borrow() *Conn {
+	c := pool.get()
 return c
`

// Bundle writes a complete corpus bundle: manifest, reviewed diff, findings,
// and the sampling record that keeps sibling bundles in one cohort.
func Bundle(t *testing.T, dir, diff, body string) {
	t.Helper()
	write(t, dir, "corpus.json", manifest)
	write(t, dir, "full.diff", diff)
	write(t, dir, "findings.json", findings(body))
	Sample(t, dir, 1, 1)
}

// Sample rewrites a bundle's sampling record as one sample of a repeats run.
func Sample(t *testing.T, dir string, sample, repeats int) {
	t.Helper()
	write(t, dir, "corpus_run.json", fmt.Sprintf(sampling, repeats, sample))
}

// Review writes the replay-bundle part of a corpus bundle: findings and the
// reviewed diff, with no manifest (backtest score builds its case in memory).
func Review(t *testing.T, dir, diff, body string) {
	t.Helper()
	write(t, dir, "full.diff", diff)
	write(t, dir, "findings.json", findings(body))
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, dirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), fileMode); err != nil {
		t.Fatal(err)
	}
}
