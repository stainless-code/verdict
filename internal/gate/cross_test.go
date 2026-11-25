package gate

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
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
	`"evidence":{"head":"` + gateHead + `","diff_sha256":"0000000000000000000000000000000000000000000000000000000000000000",` +
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

// assertCrossGateFails runs the tier gate and requires exit 1 with every
// wanted line on stdout.
func assertCrossGateFails(t *testing.T, req Request, wants ...string) {
	t.Helper()
	code, out, diag := gateRun(t, []string{"--max-severity", "important", "--live", "skip", "--coverage", "warn"}, req, false)
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d (gate passed an unconfirmed cross check)\nstdout:\n%s\nstderr:\n%s", code, cli.ExitRefused, out, diag)
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
