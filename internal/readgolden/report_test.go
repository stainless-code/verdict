package readgolden_test

import (
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

// budgetDoc renders a budget.json receipts array.
func budgetDoc() string {
	return `[{"at":"2026-01-01T00:00:00Z","projected_usd":9.5,` +
		`"projected_tier":"standard","budget_usd":10}]`
}

// kitchenSink layers panel, patch, post, cost (top + chunk), and budget
// onto a PR bundle for the maximal report surface.
func kitchenSink(t *testing.T, dir, pr string) {
	t.Helper()
	mkPRBundle(t, dir, pr)
	writeFiles(t, dir, map[string]string{
		".review/" + pr + "/findings.json":            mixedFindings(),
		".review/" + pr + "/panel":                    "alice\nbob\n",
		".review/" + pr + "/patch.json":               `{"sig":"patch1","note":"hello"}`,
		".review/" + pr + "/post.json":                postDoc(headA, 99),
		".review/" + pr + "/cost.json":                costTop(),
		".review/" + pr + "/budget.json":              budgetDoc(),
		".review/" + pr + "/chunks/manifest.json":     `{"chunks":[{"name":"chunk-a"}]}`,
		".review/" + pr + "/chunks/chunk-a.cost.json": costChunk(),
	})
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
			".review/5101/findings.json": mixedFindings(),
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
			".review/5101/findings.json": mixedFindings(),
			".review/5101/cost.json":     costTop(),
			".review/5101/budget.json":   budgetDoc(),
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
			".review/5101/findings.json": mixedFindings(),
			".review/5101/cost.json":     `{broken`,
		})
	}
	goArgs = splitRepo("report", "5101", "--cost", "--json")
	goldenRead(t, "report/cost-unreadable", broken, nil, nil, goArgs...)

	chunked := func(t *testing.T, dir, _ string) {
		t.Helper()
		kitchenSink(t, dir, "5101")
	}
	goArgs = splitRepo("report", "5101",
		"--cost", "--completeness", "--check", "important",
		"--output", "out.md", "--sarif", "out.sarif", "--json")
	goldenRead(t, "report/kitchen-sink", chunked, nil,
		allowKeys("C/out.md", "C/out.sarif"), goArgs...)

	dirty := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": findingsDoc(
				finding(sigCrit, "open", "critical", "walked", "inline")),
		})
	}
	goArgs = splitRepo("report", "5101", "--check", "critical")
	goldenRead(t, "report/check-critical-dirty", dirty, nil, nil, goArgs...)

	// A lone below-tier open finding still passes --check critical (the
	// comparison key is the whole-store open count, and the tier gate is
	// quiet).
	infoOnly := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5102")
		writeFiles(t, dir, map[string]string{
			".review/5102/findings.json": findingsDoc(
				finding(sigInfo, "open", "informational", "said", "inline")),
			".review/5102/post.json": postDoc(headA, 7),
		})
	}
	goArgs = splitRepo("report", "5102",
		"--check", "critical", "--completeness")
	goldenRead(t, "report/check-critical-pass", infoOnly, nil, nil, goArgs...)

	stale := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5102")
		writeFiles(t, dir, map[string]string{
			".review/5102/findings.json": findingsDoc(
				finding(sigInfo, "open", "informational", "said", "inline")),
			".review/5102/post.json": postDoc(headC, 7),
		})
	}
	goArgs = splitRepo("report", "5102", "--completeness")
	goldenRead(t, "report/completeness-stale", stale, nil, nil, goArgs...)

	noPost := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5102")
		writeFiles(t, dir, map[string]string{
			".review/5102/findings.json": findingsDoc(
				finding(sigInfo, "open", "informational", "said", "inline")),
		})
	}
	goArgs = splitRepo("report", "5102",
		"--completeness", "--json")
	goldenRead(t, "report/completeness-missing", noPost, nil, nil, goArgs...)

	// Live-thread legs run against the network-dead harness stub: skip
	// warns neutral, strict fails closed, auto stays neutral.
	goArgs = splitRepo("report", "5102",
		"--check", "critical", "--live", "skip")
	goldenRead(t, "report/live-skip", infoOnly, nil, nil, goArgs...)

	goArgs = splitRepo("report", "5102",
		"--check", "critical", "--live", "strict")
	goldenRead(t, "report/live-strict-unreachable", infoOnly, nil, nil, goArgs...)

	goArgs = splitRepo("report", "5102", "--check", "critical")
	goldenRead(t, "report/live-auto-unreachable", infoOnly, nil, nil, goArgs...)

	local := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkLocalBundle(t, dir, "local-work")
		writeFiles(t, dir, map[string]string{
			".review/local-work/findings.json": findingsDoc(
				finding(sigInfo, "open", "informational", "said", "inline")),
		})
	}
	goArgs = splitRepo("report", "local-work")
	goldenRead(t, "report/local-human", local, nil, nil, goArgs...)

	goArgs = splitRepo("report", "local-work",
		"--check", "critical", "--json")
	goldenRead(t, "report/local-check-json", local, nil, nil, goArgs...)
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
					".review/5101/findings.json": doc,
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
			".review/5101/findings.json": mixedFindings(),
			".review/5101/patch.json":    `{broken`,
		})
	}
	goArgs := splitRepo("report", "5101", "--json")
	goldenRead(t, "report/refuse-patch", patch, nil, nil, goArgs...)

	// The human refusal surface matches too, not just the envelope.
	human := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": `[42]`,
		})
	}
	goArgs = splitRepo("report", "5101")
	goldenRead(t, "report/refuse-human", human, nil, nil, goArgs...)
}
