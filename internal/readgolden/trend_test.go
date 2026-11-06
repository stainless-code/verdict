package readgolden_test

import (
	"testing"
)

// trendMixed builds three bundles: a dated post bundle with mixed
// findings, a record-dated bundle with cost models and a recall manifest,
// and an undated local bundle.
func trendMixed(t *testing.T, dir string) {
	t.Helper()
	writeFiles(t, dir, map[string]string{
		".verdict/state/5101/PR.json":       prDoc(headA),
		".verdict/state/5101/full.diff":     fullDiff,
		".verdict/state/5101/findings.json": mixedFindings(),
		".verdict/state/5101/post.json":     postDoc(headA, 99),

		".verdict/state/5102/PR.json":   prDoc(headC),
		".verdict/state/5102/full.diff": fullDiff,
		".verdict/state/5102/findings.json": findingsDoc(
			finding(sigImp, "addressed", "important", "walked", "inline"),
			finding(sigInfo, "open", "informational", "said", "inline"),
		),
		".verdict/state/5102/record.json": `{"recorded_at":"2026-02-01T00:00:00Z"}`,
		".verdict/state/5102/cost.json": `{"schema_version":"1","effort":"deep",` +
			`"models":[{"name":"m","input_tokens":10,"output_tokens":5}],"est_usd":0.01}`,
		".verdict/state/5102/kb-recall.json": `[{"id":"r-a1","score":1,"hits":2,"judged":2}]`,

		".verdict/state/local-x/source.json": sourceDoc(headA),
		".verdict/state/local-x/full.diff":   fullDiff,
		".verdict/state/local-x/findings.json": findingsDoc(
			finding(sigCrit, "open", "critical", "walked", "inline")),
		".verdict/state/local-x/record.json": `{"recorded_at":"2026-03-01T00:00:00Z"}`,

		".verdict/kb-joins.jsonl": `{"at":"2026-10-31T00:00:01Z","sig":"` + sigImp + `",` +
			`"rule":"r-a1","outcome":"addressed","actor":"human","verb":"triage",` +
			`"bundle":"5102","review_id":null}` + "\n",
	})
}

// trendCorrupt adds an unreadable bundle to the mixed set: trend warns
// and skips it, rolling up the readable ones.
func trendCorrupt(t *testing.T, dir string) {
	t.Helper()
	trendMixed(t, dir)
	writeFiles(t, dir, map[string]string{
		".verdict/state/5109/PR.json":       prDoc(headA),
		".verdict/state/5109/full.diff":     fullDiff,
		".verdict/state/5109/findings.json": `[` + numericSigEntry() + `]`,
	})
}

// numericSigEntry renders a shape-guard-violating entry (numeric sig).
func numericSigEntry() string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT",` +
		`"line":2,"anchored":true,"body":"Usable body here with enough substance.",` +
		`"suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical",` +
		`"sig":7,"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa",` +
		`"status":"open","rationale":"","history":[]}`
}

// TestTrendGolden pins the trend verb: the empty store,
// the dated rollup (periods, tiers, provenance), the overlap audit, the
// corrupt-bundle skip, and the unreadable-joins warn. Pure read: every
// leg carries the no-write proof.
func TestTrendGolden(t *testing.T) {
	t.Parallel()

	empty := func(t *testing.T, _, _ string) {
		t.Helper()
	}
	goArgs := splitRepoOnly("--json")
	goldenRead(t, "trend/empty-json", empty, nil, nil, goArgs...)

	goArgs = splitRepoOnly()
	goldenRead(t, "trend/empty-human", empty, nil, nil, goArgs...)

	mixed := func(t *testing.T, dir, _ string) {
		t.Helper()
		trendMixed(t, dir)
	}
	goArgs = splitRepoOnly("--json")
	goldenRead(t, "trend/mixed-json", mixed, nil, nil, goArgs...)

	goArgs = splitRepoOnly()
	goldenRead(t, "trend/mixed-human", mixed, nil, nil, goArgs...)

	goArgs = splitRepoOnly("--overlap", "--json")
	goldenRead(t, "trend/overlap-json", mixed, nil, nil, goArgs...)

	goArgs = splitRepoOnly("--overlap")
	goldenRead(t, "trend/overlap-human", mixed, nil, nil, goArgs...)

	corrupt := func(t *testing.T, dir, _ string) {
		t.Helper()
		trendCorrupt(t, dir)
	}
	goArgs = splitRepoOnly("--json")
	goldenRead(t, "trend/corrupt-skip-json", corrupt, nil, nil, goArgs...)

	goArgs = splitRepoOnly("--overlap")
	goldenRead(t, "trend/corrupt-skip-overlap", corrupt, nil, nil, goArgs...)

	brokenJoins := func(t *testing.T, dir, _ string) {
		t.Helper()
		trendMixed(t, dir)
		writeFiles(t, dir, map[string]string{
			".verdict/kb-joins.jsonl": `{broken`,
		})
	}
	goArgs = splitRepoOnly("--overlap", "--json")
	goldenRead(t, "trend/joins-unreadable", brokenJoins, nil, nil, goArgs...)
}
