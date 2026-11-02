package readparity_test

import (
	"fmt"
	"testing"
)

// calibrateStubScript answers the live join deterministically: threads
// with comments (one resolved, one open), PR state with a parametrized
// live head ($CAL_LIVE), and a compare file list ($CAL_COMPARE=fail takes
// the compare down). Anything else fails like the network-dead stub.
const calibrateStubScript = `#!/usr/bin/env bash
echo "stub-gh $*" >> "${EVAL_STUBLOG:-/dev/null}"
if [[ "$*" == *"api graphql"* ]]; then
  printf '%s' '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
    {"isResolved":true,"comments":{"nodes":[{"body":"Fixed body here with enough substance.","path":"notes.txt"}]}},
    {"isResolved":false,"comments":{"nodes":[{"body":"Open body here with enough substance.","path":"notes.txt"}]}}
  ]}}}}}'
  exit 0
fi
case "$*" in
  *"pr view"*)
    printf '%s' "{\"headRefOid\":\"${CAL_LIVE:-cccccccccccccccccccccccccccccccccccccccc}\",\"state\":\"OPEN\",\"mergedAt\":null}"
    exit 0;;
  *"api repos/"*)
    if [ "${CAL_COMPARE:-ok}" = "fail" ]; then echo "stub-gh: compare down" >&2; exit 1; fi
    if [ "${CAL_COMPARE:-ok}" = "garbage" ]; then printf '%s' 'not json'; exit 0; fi
    printf '%s' '["notes.txt"]'
    exit 0;;
esac
echo "stub-gh: no match ($*)" >&2
exit 1
`

// findingBody renders one inline entry with a custom body and kb cites.
func findingBody(sig, status, severity, body, kb string) string {
	if kb == "" {
		kb = "[]"
	}
	return fmt.Sprintf(`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,`+
		`"anchored":true,"body":%q,"suggestion":null,`+
		`"inBounds":true,"rigor":"walked","severity":%q,"sig":%q,"sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":%q,"rationale":"",`+
		`"history":[],"kb":%s}`,
		body, severity, sig, status, kb)
}

// calibrateLive posts a completed-post receipt with provenance fields and
// findings whose bodies settle each join outcome: fixed (resolved +
// changed), open (unresolved), unknown (unmatched addressed), and
// body-section (section kind).
func calibrateLive(t *testing.T, dir string) {
	t.Helper()
	mkPRBundle(t, dir, "5101")
	writeFiles(t, dir, map[string]string{
		".review/5101/findings.json": findingsDoc(
			findingBody(sigCrit, "open", "critical", "Fixed body here with enough substance.", `["r-a1"]`),
			findingBody(sigImp, "open", "important", "Open body here with enough substance.", `[]`),
			findingBody(sigAddr, "addressed", "important", "Unseen body here with enough substance.", `["r-a2"]`),
			`{"kind":"section","path":null,"side":null,"line":null,`+
				`"anchored":false,"body":"Section body here with enough substance.",`+
				`"suggestion":null,"inBounds":false,"rigor":"pointed","severity":"informational",`+
				`"sig":"`+sigSect+`","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa",`+
				`"status":"open","rationale":"","history":[]}`,
		),
		".review/5101/post.json": `{"schema_version":"1","review":{"id":424242},` +
			`"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","posted_at":"2026-10-31T00:00:00Z",` +
			`"confidence":null,"security_summary":null,` +
			`"tool_version":"0.0.1-test","prompts_hash":"abc123def456","model":"m"}`,
	})
}

// TestCalibrateLiveParity proves the live join matches bash across the
// stale/current heads, the downed compare, and the bundle-gate refusals
// (missing post, no review id, dead threads read, local bundles,
// non-GitHub providers, corrupt stores). All legs prove no writes.
func TestCalibrateLiveParity(t *testing.T) {
	t.Parallel()

	stub := func(extra map[string]string) map[string]string {
		return writeStub(t, calibrateStubScript, extra)
	}

	stale := func(t *testing.T, dir, _ string) {
		t.Helper()
		calibrateLive(t, dir)
	}
	live := stub(map[string]string{"CAL_LIVE": headC})
	bash, goArgs := splitRepo("calibrate", "5101")
	runReadBoth(t, "calibrate/live-stale-human", stale, live, nil, bash, goArgs)

	bash, goArgs = splitRepo("calibrate", "5101", "--json")
	runReadBoth(t, "calibrate/live-stale-json", stale, live, nil, bash, goArgs)

	liveCurrent := stub(map[string]string{"CAL_LIVE": headA})
	bash, goArgs = splitRepo("calibrate", "5101", "--json")
	runReadBoth(t, "calibrate/live-current-json", stale, liveCurrent, nil, bash, goArgs)

	downed := stub(map[string]string{"CAL_LIVE": headC, "CAL_COMPARE": "fail"})
	bash, goArgs = splitRepo("calibrate", "5101", "--json")
	runReadBoth(t, "calibrate/live-compare-down", stale, downed, nil, bash, goArgs)

	garbage := stub(map[string]string{"CAL_LIVE": headC, "CAL_COMPARE": "garbage"})
	bash, goArgs = splitRepo("calibrate", "5101", "--json")
	runReadBoth(t, "calibrate/live-compare-garbage", stale, garbage, nil, bash, goArgs)

	noPost := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": mixedFindings(),
		})
	}
	bash, goArgs = splitRepo("calibrate", "5101", "--json")
	runReadBoth(t, "calibrate/missing-post", noPost, nil, nil, bash, goArgs)

	noID := func(t *testing.T, dir, _ string) {
		t.Helper()
		calibrateLive(t, dir)
		writeFiles(t, dir, map[string]string{
			".review/5101/post.json": `{"schema_version":"1","review":{},` +
				`"head":"` + headA + `","posted_at":"2026-10-31T00:00:00Z"}`,
		})
	}
	bash, goArgs = splitRepo("calibrate", "5101", "--json")
	runReadBoth(t, "calibrate/no-review-id", noID, nil, nil, bash, goArgs)

	// Dead threads read: the stub fails every gh call, so the join
	// refuses threads-failed before touching PR state.
	bash, goArgs = splitRepo("calibrate", "5101", "--json")
	runReadBoth(t, "calibrate/threads-failed", stale, nil, nil, bash, goArgs)

	local := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkLocalBundle(t, dir, "local-work")
		writeFiles(t, dir, map[string]string{
			".review/local-work/findings.json": mixedFindings(),
		})
	}
	bash, goArgs = splitRepo("calibrate", "local-work", "--json")
	runReadBoth(t, "calibrate/local-refused", local, nil, nil, bash, goArgs)

	gitlab := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/PR.json": `{"provider":"gitlab","number":5101,` +
				`"head_sha":"` + headA + `"}`,
			".review/5101/findings.json": mixedFindings(),
			".review/5101/post.json":     postDoc(headA, 99),
		})
	}
	bash, goArgs = splitRepo("calibrate", "5101", "--json")
	runReadBoth(t, "calibrate/provider-refused", gitlab, nil, nil, bash, goArgs)

	corrupt := func(t *testing.T, dir, _ string) {
		t.Helper()
		calibrateLive(t, dir)
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": `[` + numericSigEntry() + `]`,
		})
	}
	bash, goArgs = splitRepo("calibrate", "5101", "--json")
	runReadBoth(t, "calibrate/corrupt-store", corrupt, nil, nil, bash, goArgs)
}

// probeFindings renders probe findings with kb cites for grading.
func probeFindings() string {
	return findingsDoc(
		findingBody(sigCrit, "addressed", "critical", "Fixed body here with enough substance.", `["r-a1"]`),
		findingBody(sigImp, "open", "important", "Open body here with enough substance.", `[]`),
		findingBody(sigAddr, "false-positive", "important", "Unseen body here with enough substance.", `["r-a2"]`),
	)
}

// TestCalibrateProbeParity proves the offline probe matches bash: the
// grade write (byte-exact kb-grades.jsonl on both sides), sticky priors,
// agent judgments, and the judgment refusals.
func TestCalibrateProbeParity(t *testing.T) {
	t.Parallel()

	probe := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": probeFindings(),
		})
	}
	grades := allowKeys("C/.review/5101/kb-grades.jsonl")

	bash, goArgs := splitRepo("calibrate", "5101", "--probe")
	runReadBoth(t, "calibrate/probe-human", probe, nil, grades, bash, goArgs)

	bash, goArgs = splitRepo("calibrate", "5101", "--probe", "--json")
	runReadBoth(t, "calibrate/probe-json", probe, nil, grades, bash, goArgs)

	judged := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": probeFindings(),
			// A sticky prior for a sig still present merges in.
			".review/5101/kb-grades.jsonl": `{"at":"2026-01-01T00:00:00Z",` +
				`"bundle":"5101","sig":"` + sigCrit + `","verdict":"unknown",` +
				`"rule":null,"helpful":null,"agent_verdict":"real","agent_helpful":"true",` +
				`"agreement":null,"grader":"calibrate-probe"}` + "\n",
		})
	}
	bash, goArgs = splitRepo("calibrate", "5101",
		"--probe", "--judge", sigImp+":false:false", "--json")
	runReadBoth(t, "calibrate/probe-judge", judged, nil, grades, bash, goArgs)

	bad := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPRBundle(t, dir, "5101")
		writeFiles(t, dir, map[string]string{
			".review/5101/findings.json": probeFindings(),
		})
	}
	bash, goArgs = splitRepo("calibrate", "5101",
		"--probe", "--judge", "bogus", "--json")
	runReadBoth(t, "calibrate/probe-bad-judge", bad, nil, nil, bash, goArgs)

	bash, goArgs = splitRepo("calibrate", "5101",
		"--judge", sigImp+":false:false")
	runReadBoth(t, "calibrate/judge-needs-probe", bad, nil, nil, bash, goArgs)
}
