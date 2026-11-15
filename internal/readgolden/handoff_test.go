package readgolden_test

import (
	"testing"
)

func handoffBriefed() string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"The retry path drops the first attempt.",` +
		`"suggestion":"return retry(ctx, firstAttempt(ctx))",` +
		`"inBounds":true,"rigor":"walked","severity":"critical","kb":["await-err"],` +
		`"sig":"` + sigCrit + `","sig_version":"1",` +
		`"brief":{"sites":[{"path":"notes.txt","line":2}],` +
		`"outcome":"Retry once before surfacing the error to the caller.",` +
		`"approach":"Wrap the first attempt so the retry sees the same context.",` +
		`"questions":["Should the retry reuse the original deadline?"]},` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`
}

func handoffVerification() string {
	return `[{"sig":"` + sigCrit + `","verdict":"VERIFIED","verifyCmd":"go test ./...",` +
		`"resultHead":"` + headA + `"}]`
}

func handoffMixed(t *testing.T, dir, _ string) {
	t.Helper()
	mkLocalBundle(t, dir, "local-x")
	writeFiles(t, dir, map[string]string{
		".verdict/state/local-x/findings.json": findingsDoc(
			handoffBriefed(),
			finding(sigImp, "open", "important", "pointed", "inline"),
			finding(sigInfo, "open", "informational", "said", "inline"),
		),
		".verdict/state/local-x/verification.json": handoffVerification(),
	})
}

func handoffBelowFloor(t *testing.T, dir, _ string) {
	t.Helper()
	mkLocalBundle(t, dir, "local-x")
	writeFiles(t, dir, map[string]string{
		".verdict/state/local-x/findings.json": findingsDoc(
			finding(sigInfo, "open", "informational", "said", "inline"),
		),
	})
}

// envJSON is the env-derived JSON mode a CI shell sets: a formatter or an
// agent turns envelopes on for every verb in the job.
var envJSON = map[string]string{"REVIEW_OUTPUT_FORMAT": "json"}

func handoffArgs(flags ...string) []string {
	return append([]string{"handoff", "acme/app", "local-x"}, flags...)
}

func TestHandoffGolden(t *testing.T) {
	t.Parallel()

	goldenRead(t, "handoff/human", handoffMixed, nil, nil, handoffArgs()...)
	goldenRead(t, "handoff/json", handoffMixed, nil, nil, handoffArgs("--json")...)
	goldenRead(t, "handoff/floor", handoffMixed, nil, nil,
		handoffArgs("--min-severity", "informational")...)
	goldenRead(t, "handoff/paths", handoffMixed, nil, nil, handoffArgs("--paths", "*.go")...)
	goldenRead(t, "handoff/empty", handoffBelowFloor, nil, nil, handoffArgs()...)
	goldenRead(t, "handoff/bad-floor", handoffMixed, nil, nil,
		handoffArgs("--min-severity", "blocker")...)
	goldenRead(t, "handoff/bad-format", handoffMixed, nil, nil, handoffArgs("--format", "text")...)
	goldenRead(t, "handoff/bad-flag", handoffMixed, nil, nil, handoffArgs("--sig", sigCrit)...)
	// The env's JSON mode and an explicit --format md disagree; the flag wins,
	// because flags outrank env everywhere else in the CLI.
	goldenRead(t, "handoff/env-json", handoffMixed, envJSON, nil, handoffArgs()...)
	goldenRead(t, "handoff/env-json-format-md", handoffMixed, envJSON, nil,
		handoffArgs("--format", "md")...)
	// Both spelled out on argv is a contradiction, not a silent winner.
	goldenRead(t, "handoff/json-format-md", handoffMixed, nil, nil,
		handoffArgs("--json", "--format", "md")...)
}
