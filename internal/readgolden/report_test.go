package readgolden_test

import (
	"strings"
	"testing"
)

// costTop renders a top-level cost.json with the cache-split mix.
func costTop() string {
	return `{"schema_version":"1","effort":"standard",` +
		`"models":[{"name":"m","input_tokens":100,"output_tokens":50,` +
		`"cache_read_input_tokens":30,"cache_creation_input_tokens":10},` +
		`{"name":"n","input_tokens":20,"output_tokens":5}],"est_usd":0.05}`
}

// costChunk renders one per-chunk cost record.
func costChunk() string {
	return `{"schema_version":"1","effort":"cheap",` +
		`"models":[{"name":"m","input_tokens":1000,"output_tokens":200}],"est_usd":1.5}`
}

// budgetDoc renders a budget.json receipts array: the consented receipt
// fetch writes, beside the measured spend receipt a two-finder run settles.
func budgetDoc() string {
	return `[{"at":"2026-01-01T00:00:00Z","projected_usd":9.5,` +
		`"projected_tier":"standard","budget_usd":10,"consent":"--consent-over-budget"},` +
		`{"at":"2026-01-02T00:00:00Z","execution_id":"exec-A","scope":"invocation",` +
		`"invocation_id":"exec-A@1","projected_usd":1.30604299999999994,"priced_by":"measured"}]`
}

// kitchenSink layers panel, patch, post, cost (top + chunk), and budget
// onto a PR bundle for the maximal report surface.
func kitchenSink(t *testing.T, dir, pr string) {
	t.Helper()
	mkPRBundle(t, dir, pr)
	writeFiles(t, dir, map[string]string{
		".verdict/state/" + pr + "/findings.json":            mixedFindings(),
		".verdict/state/" + pr + "/panel":                    "alice\nbob\n",
		".verdict/state/" + pr + "/patch.json":               `{"sig":"patch1","note":"hello"}`,
		".verdict/state/" + pr + "/post.json":                postDoc(headA, 99),
		".verdict/state/" + pr + "/cost.json":                costTop(),
		".verdict/state/" + pr + "/budget.json":              budgetDoc(),
		".verdict/state/" + pr + "/chunks/manifest.json":     `{"chunks":[{"name":"chunk-a"}]}`,
		".verdict/state/" + pr + "/chunks/chunk-a.cost.json": costChunk(),
	})
}

// validationPass renders a validation.json judging three of the four open
// findings: one downgrade, one rejection, and one confirmation whose reason
// carries a newline and a terminal escape the table must flatten.
func validationPass() string {
	return `{"schema_version":"1","head_sha":"` + headA + `","results":[` +
		`{"sig":"` + sigCrit + `","verdict":"DOWNGRADED","from_severity":"critical","to_severity":"important",` +
		`"reason":"The hunk only reaches this path under a test flag."},` +
		`{"sig":"` + sigImp + `","verdict":"REJECTED","reason":"The cited line is outside the diff."},` +
		`{"sig":"` + sigInfo + `","verdict":"CONFIRMED","reason":"Reachable from the cited hunk.\n\u001b[31mNo   change.\u001b[0m"}]}`
}

// validationReceipt renders the post.json a gate-mode post wrote after it
// applied a pass, so the report reads the stored tally instead of recounting.
func validationReceipt() string {
	return `{"schema_version":"1","head":"` + headA + `","posted_at":"2026-01-01T00:00:00Z",` +
		`"validation_hash":"` + strings.Repeat("9f", 32) + `",` +
		`"validation_counts":{"confirmed":1,"rejected":1,"downgraded":1,"unvalidated":1},"validation_mode":"gate",` +
		`"rigor_floors":{"critical":"walked","important":"pointed","informational":"said"}}`
}

// TestReportGolden pins the report verb across the human
// and machine surfaces, the filter flags, the cost rollup (top, chunk,
// cache splits, receipts), the requested output artifacts, and the gate
// flags (tier, freshness, live-thread skip/unreachable). Readers write
// nothing: every leg carries the no-write proof.
func TestReportGolden(t *testing.T) {
	t.Parallel()

	mixed := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json": mixedFindings(),
		})
	}

	goArgs := splitRepo("report", "5101")
	goldenRead(t, "report/human", mixed, nil, nil, goArgs...)

	goArgs = splitRepo("report", "5101", "--json")
	goldenRead(t, "report/json", mixed, nil, nil, goArgs...)

	goArgs = splitRepo("report", "5101", "--status", "open")
	goldenRead(t, "report/status-open", mixed, nil, nil, goArgs...)

	goArgs = splitRepo("report", "5101", "--kind", "section", "--json")
	goldenRead(t, "report/kind-section-json", mixed, nil, nil, goArgs...)

	cost := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json": mixedFindings(),
			".verdict/state/5101/cost.json":     costTop(),
			".verdict/state/5101/budget.json":   budgetDoc(),
		})
	}
	goArgs = splitRepo("report", "5101", "--cost")
	goldenRead(t, "report/cost-human", cost, nil, nil, goArgs...)

	goArgs = splitRepo("report", "5101", "--cost", "--json")
	goldenRead(t, "report/cost-json", cost, nil, nil, goArgs...)

	broken := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json": mixedFindings(),
			".verdict/state/5101/cost.json":     `{broken`,
		})
	}
	goArgs = splitRepo("report", "5101", "--cost", "--json")
	goldenRead(t, "report/cost-unreadable", broken, nil, nil, goArgs...)

	chunked := func(t *testing.T, dir, _ string) {
		t.Helper()
		kitchenSink(t, dir, "5101")
	}
	goArgs = splitRepo("report", "5101",
		"--cost", "--output", "out.md", "--sarif", "out.sarif", "--json")
	goldenRead(t, "report/kitchen-sink", chunked, nil,
		allowKeys("C/out.md", "C/out.sarif"), goArgs...)

	judged := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json":   mixedFindings(),
			".verdict/state/5101/validation.json": validationPass(),
		})
	}
	goArgs = splitRepo("report", "5101", "--validation")
	goldenRead(t, "report/validation-human", judged, nil, nil, goArgs...)

	receipted := func(t *testing.T, dir, _ string) {
		t.Helper()
		judged(t, dir, "")
		writeFiles(t, dir, map[string]string{".verdict/state/5101/post.json": validationReceipt()})
	}
	goArgs = splitRepo("report", "5101", "--validation")
	goldenRead(t, "report/validation-receipt-human", receipted, nil, nil, goArgs...)

	dirty := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json": findingsDoc(
				finding(sigCrit, "open", "critical", "walked", "inline")),
		})
	}
	goArgs = splitRepo("gate", "5101", "--max-severity", "critical", "--coverage", "warn")
	goldenRead(t, "gate/check-critical-dirty", dirty, nil, nil, goArgs...)

	// A lone below-tier open finding still passes --max-severity critical (the
	// comparison key is the whole-store open count, and the tier gate is
	// quiet).
	infoOnly := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5102")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5102/findings.json": findingsDoc(
				finding(sigInfo, "open", "informational", "said", "inline")),
			".verdict/state/5102/post.json":   postDoc(headA, 7),
			".verdict/state/5102/record.json": evidenceRecordDoc(),
		})
	}
	goArgs = splitRepo("gate", "5102",
		"--max-severity", "critical", "--require-head", "--coverage", "warn")
	goldenRead(t, "gate/check-critical-pass", infoOnly, nil, nil, goArgs...)

	stale := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5102")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5102/findings.json": findingsDoc(
				finding(sigInfo, "open", "informational", "said", "inline")),
			".verdict/state/5102/post.json": postDoc(headC, 7),
		})
	}
	goArgs = splitRepo("gate", "5102", "--require-head", "--coverage", "warn")
	goldenRead(t, "gate/completeness-stale", stale, nil, nil, goArgs...)

	noPost := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5102")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5102/findings.json": findingsDoc(
				finding(sigInfo, "open", "informational", "said", "inline")),
		})
	}
	goArgs = splitRepo("gate", "5102",
		"--require-head", "--coverage", "warn", "--json")
	goldenRead(t, "gate/completeness-missing", noPost, nil, nil, goArgs...)

	// Live-thread legs run against the network-dead harness stub: skip
	// warns neutral, strict fails closed, auto stays neutral.
	goArgs = splitRepo("gate", "5102",
		"--max-severity", "critical", "--coverage", "warn", "--live", "skip")
	goldenRead(t, "gate/live-skip", infoOnly, nil, nil, goArgs...)

	goArgs = splitRepo("gate", "5102",
		"--max-severity", "critical", "--coverage", "warn", "--live", "strict")
	goldenRead(t, "gate/live-strict-unreachable", infoOnly, nil, nil, goArgs...)

	goArgs = splitRepo("gate", "5102", "--max-severity", "critical", "--coverage", "warn")
	goldenRead(t, "gate/live-auto-unreachable", infoOnly, nil, nil, goArgs...)

	local := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkLocalBundle(t, dir, "local-work")
		writeFiles(t, dir, map[string]string{
			".verdict/state/local-work/findings.json": findingsDoc(
				finding(sigInfo, "open", "informational", "said", "inline")),
		})
	}
	goArgs = splitRepo("report", "local-work")
	goldenRead(t, "report/local-human", local, nil, nil, goArgs...)

	goArgs = splitRepo("gate", "local-work",
		"--max-severity", "critical", "--coverage", "warn", "--json")
	goldenRead(t, "gate/local-check-json", local, nil, nil, goArgs...)
}

// corruptDocs maps refusal-leg names to findings.json payloads (the empty
// marker means the store file is missing entirely).
func corruptDocs() map[string]string {
	numericSig := `{"kind":"inline","path":"notes.txt","side":"RIGHT",` +
		`"line":2,"anchored":true,"body":"Usable body here with enough substance.",` +
		`"suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical",` +
		`"sig":42,"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa",` +
		`"status":"open","rationale":"","history":[]}`
	numericBody := `{"kind":"inline","path":"notes.txt","side":"RIGHT",` +
		`"line":2,"anchored":true,"body":42,` +
		`"suggestion":null,"inBounds":true,"rigor":"walked","severity":"critical",` +
		`"sig":"` + sigCrit + `","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa",` +
		`"status":"open","rationale":"","history":[]}`
	return map[string]string{
		"numeric-sig":   `[` + numericSig + `]`,
		"numeric-body":  `[` + numericBody + `]`,
		"non-object":    `[42]`,
		"non-array":     `{"sig":"x"}`,
		"null-store":    `null`,
		"missing-store": "",
	}
}

// TestReportRefusals proves the corrupt-store typed refusals match
// byte-exact: exit codes, envelope codes, and the untouched store.
func TestReportRefusals(t *testing.T) {
	t.Parallel()
	for name, doc := range corruptDocs() {
		setup := func(t *testing.T, dir, _ string) {
			t.Helper()
			mkPRBundle(t, dir, "5101")
			if doc != "" {
				writeFiles(t, dir, map[string]string{
					".verdict/state/5101/findings.json": doc,
				})
			}
		}
		goArgs := splitRepo("report", "5101", "--json")
		goldenRead(t, "report/refuse-"+name, setup, nil, nil, goArgs...)
	}

	patch := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json": mixedFindings(),
			".verdict/state/5101/patch.json":    `{broken`,
		})
	}
	goArgs := splitRepo("report", "5101", "--json")
	goldenRead(t, "report/refuse-patch", patch, nil, nil, goArgs...)

	// The human refusal surface matches too, not just the envelope.
	human := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".verdict/state/5101/findings.json": `[42]`,
		})
	}
	goArgs = splitRepo("report", "5101")
	goldenRead(t, "report/refuse-human", human, nil, nil, goArgs...)
}
