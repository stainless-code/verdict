// Package learn_test holds the golden contract for the Go learn verb: the
// shared harness runs the Go binary on hermetic .review/kb.jsonl stores and
// pins exit codes, stdout/stderr, and every written file against checked-in
// goldens (go-port 08: the bash comparison retired with the shell).
package learn_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
)

var harness *parity.Harness

// TestMain builds the Go binary once and runs the parity suite.
func TestMain(m *testing.M) {
	os.Exit(parity.RunMain(m, &harness))
}

// kbSeed is the two-row store the read cases share: one judged rule and one
// unjudged rule (precision 3/4 and n/a).
const kbSeed = `{"id":"await-err","rule":"Check error paths on every await","scope":"src/*.ts","provenance":"manual","rigor":"walked","hits":3,"false_hits":1,"judged":4,"status":"active"}
{"id":"no-secrets","rule":"Never log secrets in error paths","scope":"src/*.go","provenance":"manual","rigor":"pointed","hits":0,"false_hits":0,"judged":0,"status":"active"}
`

// kbRetired is a store holding one retired rule with history.
const kbRetired = `{"id":"old-rule","rule":"An outdated rule kept for the record","scope":"*","provenance":"manual","rigor":"walked","hits":1,"false_hits":0,"judged":1,"status":"retired","retiredAt":"2026-01-01T00:00:00Z","retireReason":"the rule stopped matching how this repo writes code","history":[{"at":"2026-01-01T00:00:00Z","event":"retired","actor":"human","note":"the rule stopped matching how this repo writes code"}]}
`

// writeKB plants a store at dir/.review/kb.jsonl.
func writeKB(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".review", "kb.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedStatus is the populated status fixture.
func seedStatus(t *testing.T, dir, _ string) { t.Helper(); writeKB(t, dir, kbSeed) }

// seedRetired is the retired-rule fixture (restore target).
func seedRetired(t *testing.T, dir, _ string) { t.Helper(); writeKB(t, dir, kbRetired) }

// seedActive is the active-rule fixture (retire target).
func seedActive(t *testing.T, dir, _ string) { t.Helper(); writeKB(t, dir, kbSeed) }

// seedCorrupt is the unreadable-store fixture.
func seedCorrupt(t *testing.T, dir, _ string) {
	t.Helper()
	writeKB(t, dir, `{"id":"broken"`+"\n")
}

// emptyDir is the no-store fixture.
func emptyDir(t *testing.T, _ string, _ string) { t.Helper() }

// TestLearnParity proves byte parity across the append, status, retire, and
// restore modes plus every typed refusal.
func TestLearnParity(t *testing.T) {
	t.Run("append", testLearnAppend)
	t.Run("status", testLearnStatus)
	t.Run("veto", testLearnVeto)
	t.Run("refusals", testLearnRefusals)
}

// testLearnAppend covers the --rule append in both render modes.
func testLearnAppend(t *testing.T) {
	t.Helper()
	parity.Golden(t, harness, "append-human", emptyDir, nil,
		"learn", "--rule", "Never log secrets in error paths", "--scope", "src/*.ts")
	parity.Golden(t, harness, "append-json", emptyDir, nil,
		"learn", "--rule", "Never log secrets in error paths", "--scope", "src/*.ts",
		"--id", "no-secret-logs", "--provenance", "manual", "--rigor", "walked", "--json")
}

// testLearnStatus covers the --status listing and precision render.
func testLearnStatus(t *testing.T) {
	t.Helper()
	parity.Golden(t, harness, "status-human", seedStatus, nil, "learn", "--status")
	parity.Golden(t, harness, "status-json", seedStatus, nil, "learn", "--status", "--json")
	parity.Golden(t, harness, "status-empty", emptyDir, nil, "learn", "--status")
}

// testLearnVeto covers the retire/restore tombstones in both modes.
func testLearnVeto(t *testing.T) {
	t.Helper()
	const reason = "the rule stopped matching how this repo writes code"
	parity.Golden(t, harness, "retire-json", seedActive, nil,
		"learn", "--retire", "await-err", "--reason", reason, "--json")
	parity.Golden(t, harness, "retire-human", seedActive, nil,
		"learn", "--retire", "await-err", "--reason", reason)
	parity.Golden(t, harness, "restore-json", seedRetired, nil,
		"learn", "--restore", "old-rule", "--reason", reason, "--json")
	parity.Golden(t, harness, "restore-human", seedRetired, nil,
		"learn", "--restore", "old-rule", "--reason", reason)
}

// testLearnRefusals covers the mode, flag, store, and rule refusals.
func testLearnRefusals(t *testing.T) {
	t.Helper()
	const reason = "the rule stopped matching how this repo writes code"
	parity.Golden(t, harness, "need-one-mode", emptyDir, nil, "learn")
	parity.Golden(t, harness, "scope-needs-rule", emptyDir, nil,
		"learn", "--scope", "src/*.ts")
	parity.Golden(t, harness, "reason-needs-veto", emptyDir, nil,
		"learn", "--reason", reason)
	parity.Golden(t, harness, "bad-rigor", emptyDir, nil,
		"learn", "--rule", "A rule", "--scope", "*", "--rigor", "bogus")
	parity.Golden(t, harness, "bad-id", emptyDir, nil,
		"learn", "--rule", "A rule", "--scope", "*", "--id", "Bad_ID")
	parity.Golden(t, harness, "already-learned", seedActive, nil,
		"learn", "--rule", "Check error paths on every await", "--scope", "src/*.ts")
	parity.Golden(t, harness, "id-taken", seedActive, nil,
		"learn", "--rule", "A brand new rule", "--scope", "*", "--id", "await-err")
	parity.Golden(t, harness, "unknown-rule", emptyDir, nil,
		"learn", "--retire", "nope", "--reason", reason)
	parity.Golden(t, harness, "already-retired", seedRetired, nil,
		"learn", "--retire", "old-rule", "--reason", reason)
	parity.Golden(t, harness, "not-retired", seedActive, nil,
		"learn", "--restore", "await-err", "--reason", reason)
	parity.Golden(t, harness, "bad-retire-reason", seedActive, nil,
		"learn", "--retire", "await-err", "--reason", "too short")
	parity.Golden(t, harness, "corrupt-store", seedCorrupt, nil, "learn", "--status", "--json")
	parity.Golden(t, harness, "unknown-flag", emptyDir, nil, "learn", "--bogus")
}
