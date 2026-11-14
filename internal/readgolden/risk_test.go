package readgolden_test

import (
	"testing"
)

// riskLowRecord is a claimed-low store with attested coverage: no findings, so
// the queue keeps the claim and reports low.
func riskLowRecord(head string) string {
	return `{"head":"` + head + `",` +
		`"risk":{"level":"low","rationale":"One note line, fully covered.",` +
		`"assessed_head":"` + head + `"},"coverage":{"gaps":0}}`
}

// riskMergedDoc is a PR record the host already merged, so the default filter
// skips it.
func riskMergedDoc() string {
	return `{"state":"MERGED","headRefOid":"` + headA + `","baseRefOid":"` + headB + `"}`
}

// riskMixed builds the queue's four shapes: a PR with mixed findings, a
// claimed-low clean PR, an unreviewed local bundle, and a merged PR the
// default filter skips.
func riskMixed(t *testing.T, dir, _ string) {
	t.Helper()
	mkPRBundle(t, dir, "5101")
	writeFiles(t, dir, map[string]string{
		".verdict/state/5101/findings.json": mixedFindings(),
		".verdict/state/5102/PR.json":       prDoc(headC),
		".verdict/state/5102/full.diff":     fullDiff,
		".verdict/state/5102/findings.json": "[]",
		".verdict/state/5102/record.json":   riskLowRecord(headC),
		".verdict/state/5109/PR.json":       riskMergedDoc(),
		".verdict/state/5109/full.diff":     fullDiff,
		".verdict/state/5109/findings.json": mixedFindings(),
	})
	mkLocalBundle(t, dir, "local-x")
}

// riskArgs builds the repo-only argv the queue takes: one repo and its flags.
func riskArgs(flags ...string) []string {
	return append([]string{"risk", "acme/app"}, flags...)
}

// TestRiskGolden pins the triage queue: the empty store, the ranked mixed
// store with a skipped merged bundle, --status all, the row cap with its
// receipt, and the usage refusal. Pure read: every leg carries the no-write
// proof.
func TestRiskGolden(t *testing.T) {
	t.Parallel()

	empty := func(t *testing.T, _, _ string) {
		t.Helper()
	}
	goldenRead(t, "risk/empty-human", empty, nil, nil, riskArgs()...)
	goldenRead(t, "risk/empty-json", empty, nil, nil, riskArgs("--json")...)

	goldenRead(t, "risk/mixed-human", riskMixed, nil, nil, riskArgs()...)
	goldenRead(t, "risk/mixed-json", riskMixed, nil, nil, riskArgs("--json")...)

	goldenRead(t, "risk/all-json", riskMixed, nil, nil, riskArgs("--status", "all", "--json")...)

	goldenRead(t, "risk/limit-human", riskMixed, nil, nil, riskArgs("--limit", "2")...)
	goldenRead(t, "risk/limit-json", riskMixed, nil, nil, riskArgs("--limit", "2", "--json")...)

	goldenRead(t, "risk/bad-status", riskMixed, nil, nil, riskArgs("--status", "weird")...)
	goldenRead(t, "risk/bad-limit", riskMixed, nil, nil, riskArgs("--limit", "201")...)
}
