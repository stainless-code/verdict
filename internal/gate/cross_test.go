package gate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
)

// writeCapture writes the bundle's typed run capture and the controls a typed
// capture requires, so the report reads the capture instead of refusing it.
func writeCapture(t *testing.T, rs *config.RunState) {
	t.Helper()
	const store = ".verdict/state/5101"
	rs.BaseSHA, rs.Trusted = strings.Repeat("b", 40), true
	if err := config.WriteRunState(store, rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: store, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
}

// crossFinding is one open important finding the two-finder panel stamped
// pending: its quote verified, its assigned checker never confirmed it.
func crossFinding(cross string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,` +
		`"body":"Gate fixture body.","suggestion":null,"inBounds":true,"rigor":"walked",` +
		`"severity":"important","sig":"` + gateSigImp + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[],` +
		`"verification":{"quote":"verified"` + cross + `}}`
}

const pendingCross = `,"cross":{"execution_id":"execution-A","status":"pending",` +
	`"origins":["finder-1"],"expected":["finder-2"],"checks":[]}`

// twoFinderRecord is the stored record a two-finder run left: an evidence
// receipt for the head and the finder summary the panel attached.
const twoFinderRecord = `{"schema_version":"1","status":"pr","head":"` + gateHead + `",` +
	`"verification":{"finders":{"completed":2,"confirmed":0,"contested":0,"pending":1}},` +
	`"evidence":{"head":"` + gateHead + `","diff_sha256":"` + zeroDigest + `",` +
	`"receipts":{"total":0,"covered":0,"exempt":0,"complete":true},` +
	`"spec":{"total":0,"covered":0,"min":80,"met":true},"insufficient":false}}`

// TestGateFailsUnconfirmedCrossWithoutLiveExecution is the false pass the
// gate shipped: an open finding at the tier whose cross check the captured
// execution left unconfirmed passed `gate` once no live execution artifact
// remained, because the findings dimension excludes it as unconfirmed and the
// evidence dimension asked about it only while the execution was active.
func TestGateFailsUnconfirmedCrossWithoutLiveExecution(t *testing.T) {
	const (
		wantEvidence = "gate: evidence     fail  a finding within the requested tier lacks a confirmed cross check"
		wantFindings = "gate: findings     fail  1 open finding(s) at-or-above important"
	)
	tests := []struct {
		run    *config.RunState
		name   string
		cross  string
		record string
		want   string
	}{
		// Nothing states a capture: the bundle is manual, so the stale stamp
		// is dropped and the finding gates as the manual finding it is.
		{name: "nil", cross: pendingCross, want: wantFindings},
		// The run's artifacts are gone; the stored receipt still states two
		// completed finders, so the requirement outlives them.
		{name: "finished", cross: pendingCross, record: twoFinderRecord, want: wantEvidence},
		// The same finished run with the stamp stripped from the store: the
		// receipt keeps the requirement, so it cannot read as a manual finding.
		{name: "finished-stripped", record: twoFinderRecord, want: wantEvidence},
		// A re-fetch reset run.json after the two-finder execution retired.
		{name: "refetched", cross: pendingCross, record: twoFinderRecord, run: &config.RunState{}, want: wantEvidence},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertCrossGateFails(t, crossBundle(t, tc.cross, tc.record, tc.run), tc.want)
		})
	}
}

// crossBundle seeds one cross-stamped finding, then overlays the stored record
// and the typed run capture a case names.
func crossBundle(t *testing.T, cross, record string, run *config.RunState) Request {
	t.Helper()
	req := gateBundle(t, "["+crossFinding(cross)+"]")
	if record != "" {
		writeGateFile(t, ".verdict/state/5101/record.json", record)
	}
	if run != nil {
		writeCapture(t, run)
	}
	return req
}

var tierArgs = []string{"--max-severity", "important", "--live", "skip", "--coverage", "warn"}

// assertCrossGateFails runs the tier gate and requires exit 1 with every
// wanted line on stdout.
func assertCrossGateFails(t *testing.T, req Request, wants ...string) {
	t.Helper()
	assertGateExit(t, req, tierArgs, cli.ExitRefused, wants...)
}

// assertGateExit runs the gate with args and requires the exit code and every
// wanted line on stdout.
func assertGateExit(t *testing.T, req Request, args []string, want int, wants ...string) {
	t.Helper()
	code, out, diag := gateRun(t, args, req, false)
	if code != want {
		t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, want, out, diag)
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
}

// TestGateManualCaptureDropsStoredCross proves a one-finder capture keeps
// quote-only eligibility: the same finding, carrying an automatic stamp a
// retired execution left, gates as a manual finding on the findings dimension.
func TestGateManualCaptureDropsStoredCross(t *testing.T) {
	confirmed := `,"cross":{"execution_id":"execution-A","status":"confirmed","origins":["finder-1"],` +
		`"expected":["finder-2"],"checks":[{"checker":"finder-2","verdict":"confirmed","quote_status":"verified"}]}`
	for name, cross := range map[string]string{"pending": pendingCross, "confirmed": confirmed} {
		t.Run(name, func(t *testing.T) {
			assertCrossGateFails(t, crossBundle(t, cross, "", &config.RunState{Finders: 1}),
				"gate: findings     fail  1 open finding(s) at-or-above important",
				"gate: evidence     pass")
		})
	}
}

// unanchoredLogic is an open important logic finding with no inline anchor and
// a pending cross stamp. Read as a manual finding, a category gate never counts
// it (no anchor), so only the cross requirement can stop the gate on it.
const unanchoredLogic = `{"kind":"general","path":"notes.txt","anchored":false,` +
	`"body":"Gate fixture body.","suggestion":null,"inBounds":true,"rigor":"walked",` +
	`"category":"logic","severity":"important","sig":"` + gateSigImp + `","sig_version":"1",` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[],` +
	`"verification":{"quote":"verified"` + pendingCross + `}}`

// TestGateCategoryKeepsCapturedCrossRequirement pins the two ways a recorded
// two-finder capture of this head was read as manual, which let a category
// gate pass over a finding whose cross check never confirmed.
func TestGateCategoryKeepsCapturedCrossRequirement(t *testing.T) {
	const partialRecord = `{"schema_version":"1","status":"pr","head":"` + gateHead + `",` +
		`"verification":{"finders":{"completed":1,"confirmed":0,"contested":0,"pending":1}},` +
		`"evidence":{"head":"` + gateHead + `","diff_sha256":"` + zeroDigest + `",` +
		`"receipts":{"total":0,"covered":0,"exempt":0,"complete":true},` +
		`"spec":{"total":0,"covered":0,"min":80,"met":true},"insufficient":false}}`
	oneFinderDefault := &config.RunState{Resolved: &config.Resolved{
		Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{Finders: 1}},
	}}
	tests := []struct {
		run    *config.RunState
		name   string
		record string
	}{
		// A configured one-finder default is policy for future runs; the
		// receipt for this head says two finders reviewed it.
		{name: "config-default", record: twoFinderRecord, run: oneFinderDefault},
		// One finder failed: the summary still states a two-finder capture.
		{name: "partial-run", record: partialRecord},
	}
	args := []string{"--max-severity", "important", "--category", "logic", "--live", "skip", "--coverage", "warn"}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := gateBundle(t, "["+unanchoredLogic+"]")
			writeGateFile(t, ".verdict/state/5101/record.json", tc.record)
			if tc.run != nil {
				writeCapture(t, tc.run)
			}
			assertGateExit(t, req, args, cli.ExitRefused,
				"gate: evidence     fail  a finding within the requested tier lacks a confirmed cross check")
		})
	}
}

const zeroDigest = "0000000000000000000000000000000000000000000000000000000000000000"

// TestGatePassesCleanReviewOverStaleCaptures pins the false fails: a clean
// review of this head passes although an earlier capture left evidence of two
// finders behind, whether a receipt bound to an earlier head or the execution
// references a retired run left beside an explicit one-finder capture.
func TestGatePassesCleanReviewOverStaleCaptures(t *testing.T) {
	t.Run("earlier-head-post", func(t *testing.T) {
		req := gateBundle(t, "[]")
		raw, err := json.Marshal(&core.Post{
			SchemaVersion: "1", Review: map[string]any{}, PostedAt: "2026-11-02T12:00:00Z",
			Head:         strings.Repeat("c", 40),
			Verification: &core.PanelSummary{Finders: &core.FinderSummary{Completed: 2, Confirmed: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		writeGateFile(t, ".verdict/state/5101/post.json", string(raw))
		assertGateExit(t, req, tierArgs, cli.ExitOK, "gate: evidence     pass", "gate: PASS")
	})
	t.Run("one-finder-over-stale-references", func(t *testing.T) {
		req := gateBundle(t, "[]")
		writeCapture(t, &config.RunState{Finders: 1})
		writeGateFile(t, ".verdict/state/5101/review.json",
			`{"schema_version":"2","findings":[],"execution":{"id":"stale-execution","finders":2}}`)
		writeGateFile(t, ".verdict/state/5101/verification/verdicts.json", `{"execution_id":"stale-execution"}`)
		assertGateExit(t, req, tierArgs, cli.ExitOK, "gate: evidence     pass", "gate: PASS")
	})
}
