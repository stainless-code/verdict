package readgolden_test

import (
	"testing"
)

// TestGateGolden pins the gate verb's whole surface: the per-dimension lines
// and the verdict tally, the exit table (0 pass, 1 failed or unknown, 2 usage,
// 3 tool error), and the one-envelope JSON shape. Every leg runs --live skip so
// the suite stays network-dead; the postures are pinned beside the service.
func TestGateGolden(t *testing.T) {
	t.Parallel()

	pass := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5201")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5201/findings.json": findingsDoc(
				finding(sigInfo, "open", "informational", "said", "inline")),
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
