package readgolden_test

import (
	"testing"
)

// handoffBriefed is the critical fixture finding with a stored brief, the cited
// KB rule, and a code suggestion: the shape the projection carries whole.
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

// handoffVerification records the rerun command for the critical sig, the
// claim a fix pass would write.
func handoffVerification() string {
	return `[{"sig":"` + sigCrit + `","verdict":"VERIFIED","verifyCmd":"go test ./...",` +
		`"resultHead":"` + headA + `"}]`
}

// handoffMixed builds a local bundle with three open inline findings: one
// critical with a brief and a recorded verify command, one important with
// neither, and one informational under the default floor.
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

// handoffBelowFloor builds a bundle whose only open finding sits under the
// default floor, so the projection refuses and names it.
func handoffBelowFloor(t *testing.T, dir, _ string) {
	t.Helper()
	mkLocalBundle(t, dir, "local-x")
	writeFiles(t, dir, map[string]string{
		".verdict/state/local-x/findings.json": findingsDoc(
			finding(sigInfo, "open", "informational", "said", "inline"),
		),
	})
}

// handoffArgs builds the repo+pr argv the projection takes.
func handoffArgs(flags ...string) []string {
	return append([]string{"handoff", "acme/app", "local-x"}, flags...)
}

// TestHandoffGolden pins the projection: the markdown handoff, the same rows
// as JSON, the scope and floor flags, and the two refusals (nothing at the
// floor, and a usage error). Pure read: every leg carries the no-write proof.
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
}
