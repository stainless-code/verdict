package readgolden_test

import (
	"fmt"
	"testing"
)

// TestGateGolden pins the gate verb's whole surface: the per-dimension lines
// and the verdict tally, the exit table (0 pass, 1 failed or unknown, 2 usage,
// 3 tool error), and the one-envelope JSON shape. Every leg runs --live skip so
// the suite stays network-dead; the postures are pinned beside the service.
// The pass and fail bundles carry the stored coverage receipt a posted review
// leaves behind, so the coverage dimension reads a complete attestation and the
// goldens pin its pass line rather than a --coverage warn skip.
func TestGateGolden(t *testing.T) {
	t.Parallel()

	pass := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5201")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5201/findings.json": findingsDoc(
				finding(sigInfo, "open", "informational", "said", "inline")),
			".verdict/state/5201/post.json": coveredPostDoc(headA, 31),
		})
	}
	goArgs := splitRepo("gate", "5201", "--max-severity", "critical", "--live", "skip")
	goldenRead(t, "gate/pass-human", pass, nil, nil, goArgs...)

	goArgs = splitRepo("gate", "5201", "--max-severity", "critical", "--live", "skip", "--json")
	goldenRead(t, "gate/pass-json", pass, nil, nil, goArgs...)

	fail := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5201")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5201/findings.json": findingsDoc(
				finding(sigCrit, "open", "critical", "walked", "inline")),
			".verdict/state/5201/post.json": coveredPostDoc(headA, 32),
		})
	}
	goArgs = splitRepo("gate", "5201", "--max-severity", "important", "--live", "skip")
	goldenRead(t, "gate/fail-human", fail, nil, nil, goArgs...)

	goArgs = splitRepo("gate", "5201", "--max-severity", "important", "--live", "skip", "--json")
	goldenRead(t, "gate/fail-json", fail, nil, nil, goArgs...)

	stale := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5202")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5202/findings.json": findingsDoc(
				finding(sigInfo, "open", "informational", "said", "inline")),
			".verdict/state/5202/post.json": postDoc(headC, 7),
		})
	}
	goArgs = splitRepo("gate", "5202", "--max-severity", "critical", "--require-head", "--live", "skip", "--json")
	goldenRead(t, "gate/stale-head-json", stale, nil, nil, goArgs...)

	goArgs = splitRepo("gate", "5202", "--require-head", "--live", "skip")
	goldenRead(t, "gate/stale-head-human", stale, nil, nil, goArgs...)

	corrupt := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5203")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5203/findings.json": `[42]`,
		})
	}
	goArgs = splitRepo("gate", "5203", "--max-severity", "critical", "--live", "skip", "--json")
	goldenRead(t, "gate/tool-error-json", corrupt, nil, nil, goArgs...)

	// A bare gate invocation is a usage error before any read: one line on
	// stderr, no envelope, exit 2.
	goArgs = splitRepo("gate", "5203")
	goldenRead(t, "gate/usage-bare", corrupt, nil, nil, goArgs...)
}

// coveredPostDoc is the stored post a gated PR carries once its review attested
// the whole diff: the head plus the measured receipt the coverage dimension
// reads.
func coveredPostDoc(head string, id int) string {
	return fmt.Sprintf(`{"schema_version":"1","review":{"id":%d},`+
		`"head":%q,"posted_at":"2026-01-01T00:00:00Z","confidence":null,"security_summary":null,`+
		`"coverage":{"percent":100,"attested":24,"total":24,"files":2,"exempt":0,"gaps":0}}`, id, head)
}
