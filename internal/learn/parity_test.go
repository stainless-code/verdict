// Package learn_test holds the shell-parity proof for the Go learn verb:
// the shared harness runs BOTH scripts/review.sh learn and the Go binary
// on identical fixtures and compares exit codes, stdout/stderr
// (timestamps scrubbed only), and every written file byte for byte.
package learn_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
)

var harness *parity.Harness

// TestMain builds the Go binary once and runs the parity suite.
func TestMain(m *testing.M) {
	os.Exit(runParityMain(m))
}

// runParityMain builds the harness, runs the suite, and reports the exit
// code; a build failure skips the suite and returns 2.
func runParityMain(m *testing.M) int {
	h, err := parity.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	harness = h
	defer harness.Teardown()
	return m.Run()
}

// seedKB writes a two-rule kb store.
func seedKB(t *testing.T, dir, _ string) {
	t.Helper()
	rows := `{"id":"await-err","rule":"Check error paths on every await","scope":"src/*.ts","provenance":"manual","rigor":"walked","hits":0,"false_hits":0,"judged":0,"status":"active"}` + "\n" +
		`{"id":"docs-ex","rule":"Docs need examples","scope":"docs/*.md","provenance":"manual","rigor":"pointed","hits":0,"false_hits":0,"judged":0,"status":"active"}` + "\n"
	if err := os.MkdirAll(filepath.Join(dir, ".review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".review", "kb.jsonl"), []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
}

// learnBoth runs one learn case with dispatch-faithful args: the shell
// side takes no positionals, the binary side takes the empty repo/pr slots
// the dispatch always passes.
func learnBoth(t *testing.T, name string, setup func(t *testing.T, dir, home string), args ...string) {
	t.Helper()
	bashArgs := append([]string{args[0]}, args[3:]...)
	goArgs := args
	harness.RunBothSplit(t, name, setup, nil, false, bashArgs, goArgs)
}

// TestLearnParityAppend proves byte parity across the append modes and the
// append refusals.
func TestLearnParityAppend(t *testing.T) {
	learnBoth(t, "append", func(t *testing.T, dir, _ string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, ".review"), 0o755); err != nil {
			t.Fatal(err)
		}
	},
		"learn", "", "", "--rule", "Check error paths on every await", "--scope", "src/*.ts", "--provenance", "manual", "--rigor", "walked", "--id", "await-err", "--json")
	learnBoth(t, "append-human", func(t *testing.T, dir, _ string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, ".review"), 0o755); err != nil {
			t.Fatal(err)
		}
	},
		"learn", "", "", "--rule", "Check error paths on every await", "--scope", "src/*.ts")
	learnBoth(t, "append-slugified", func(t *testing.T, dir, _ string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, ".review"), 0o755); err != nil {
			t.Fatal(err)
		}
	},
		"learn", "", "", "--rule", "Check Error Paths On Every Await", "--scope", "src/*.ts", "--json")
	learnBoth(t, "duplicate-rule", seedKB,
		"learn", "", "", "--rule", "Check error paths on every await", "--scope", "src/*.ts", "--json")
	learnBoth(t, "bad-rigor", seedKB,
		"learn", "", "", "--rule", "Another rule text here", "--scope", "src/*.ts", "--rigor", "legendary", "--json")
	learnBoth(t, "bad-id", seedKB,
		"learn", "", "", "--rule", "Yet another rule text", "--scope", "src/*.ts", "--id", "Bad ID!", "--json")
	learnBoth(t, "id-taken", seedKB,
		"learn", "", "", "--rule", "Totally different rule text", "--scope", "src/*.ts", "--id", "await-err", "--json")
	learnBoth(t, "empty-rule", seedKB,
		"learn", "", "", "--rule", "   ", "--scope", "src/*.ts", "--json")
	learnBoth(t, "empty-scope", seedKB,
		"learn", "", "", "--rule", "Some rule text here", "--scope", "   ", "--json")
	learnBoth(t, "scope-blank-element", seedKB,
		"learn", "", "", "--rule", "Some rule text here", "--scope", "a,,b", "--json")
}

// TestLearnParityStatus proves byte parity across the status modes.
func TestLearnParityStatus(t *testing.T) {
	learnBoth(t, "status-empty", func(t *testing.T, dir, _ string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, ".review"), 0o755); err != nil {
			t.Fatal(err)
		}
	},
		"learn", "", "", "--status", "--json")
	learnBoth(t, "status-human", seedKB,
		"learn", "", "", "--status")
	learnBoth(t, "status-json", seedKB,
		"learn", "", "", "--status", "--json")
}

// TestLearnParityVeto proves byte parity across the retire/restore flow
// and the veto edges.
func TestLearnParityVeto(t *testing.T) {
	learnBoth(t, "retire", seedKB,
		"learn", "", "", "--retire", "docs-ex", "--reason", "this rule fires on every docs touch and is pure noise now", "--json")
	learnBoth(t, "retire-human", seedKB,
		"learn", "", "", "--retire", "docs-ex", "--reason", "this rule fires on every docs touch and is pure noise now")
	learnBoth(t, "retire-short-reason", seedKB,
		"learn", "", "", "--retire", "docs-ex", "--reason", "short", "--json")
	learnBoth(t, "retire-unknown", seedKB,
		"learn", "", "", "--retire", "nope-missing", "--reason", "this rule fires on every docs touch and is pure noise now", "--json")
	learnBoth(t, "restore", func(t *testing.T, dir, home string) {
		t.Helper()
		seedKB(t, dir, home)
	},
		"learn", "", "", "--restore", "docs-ex", "--json")
	learnBoth(t, "restore-flow", func(t *testing.T, dir, _ string) {
		t.Helper()
		rows := `{"id":"await-err","rule":"Check error paths on every await","scope":"src/*.ts","provenance":"manual","rigor":"walked","hits":0,"false_hits":0,"judged":0,"status":"active"}` + "\n" +
			`{"id":"docs-ex","rule":"Docs need examples","scope":"docs/*.md","provenance":"manual","rigor":"pointed","hits":0,"false_hits":0,"judged":0,"status":"retired","retiredAt":"2026-01-01T00:00:00Z","retireReason":"this rule fires on every docs touch and is pure noise now","history":[{"at":"2026-01-01T00:00:00Z","event":"retired","actor":"human","note":"this rule fires on every docs touch and is pure noise now"}]}` + "\n"
		if err := os.MkdirAll(filepath.Join(dir, ".review"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".review", "kb.jsonl"), []byte(rows), 0o644); err != nil {
			t.Fatal(err)
		}
	},
		"learn", "", "", "--restore", "docs-ex", "--reason", "rechecked, this docs rule caught a real miss last week", "--json")
}

// TestLearnParityRefusals proves byte parity across the mode refusals and
// the corrupt-store edge.
func TestLearnParityRefusals(t *testing.T) {
	learnBoth(t, "bad-modes", seedKB,
		"learn", "", "", "--rule", "Some rule text here", "--status", "--json")
	learnBoth(t, "scope-without-rule", seedKB,
		"learn", "", "", "--scope", "src/*.ts", "--json")
	learnBoth(t, "corrupt-store", func(t *testing.T, dir, _ string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, ".review"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".review", "kb.jsonl"), []byte(`{"id":`), 0o644); err != nil {
			t.Fatal(err)
		}
	},
		"learn", "", "", "--status", "--json")
	learnBoth(t, "unknown-flag", seedKB,
		"learn", "", "", "--bogus", "--json")
}
