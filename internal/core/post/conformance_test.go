package postcore

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Provider conformance (STA-297): one table of hosts, and for every host three
// sources that must agree — the capability table in internal/core/hostcaps.go
// (what `fetch` advertises), the submit path driven by a fake Commander (what
// `post` actually does), and docs/manual.md (what the operator is promised). A
// host that advertises false while doing the thing, or true while doing
// nothing, fails here with the host and the disagreement named.
//
// The rows below declare only the wire contract (the exact argv each write
// uses) and the documented degradations. The capability booleans are read from
// core.CapabilitiesFor at run time, so a row can never re-declare what the
// table says and drift from it.

// The published capability columns, spelled as data.capabilities names them.
const (
	conformancePost             = "post"
	conformanceInlineAnchors    = "inline_anchors"
	conformanceApprove          = "approve"
	conformanceRequestChanges   = "request_changes"
	conformanceThreadResolution = "thread_resolution"
)

// conformanceColumns is the matrix's column order: the submit capabilities the
// manual documents per host, then the gap every host shares today.
var conformanceColumns = []string{
	conformancePost, conformanceInlineAnchors,
	conformanceApprove, conformanceRequestChanges, conformanceThreadResolution,
}

// conformanceSubmitColumns are the columns docs/manual.md documents in its
// per-host submit row. thread_resolution is absent on every host and the
// manual explains it once, in the section prose, so it has no per-host row.
var conformanceSubmitColumns = conformanceColumns[:4]

const (
	conformanceHead      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	conformanceBase      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	conformancePath      = "notes.txt"
	conformanceLine      = 2
	conformanceStartLine = 1
)

// conformanceGitHubReview is the exact argv GitHub's one atomic review POST
// uses. The literal is the contract: a rename in the submit path fails here
// instead of following the helper that produced it.
const conformanceGitHubReview = "gh\x00api\x00repos/acme/app/pulls/7/reviews\x00--method\x00POST\x00--input\x00-\x00--jq\x00" + postJQFilter

// conformanceGitHubLogin is the authorship read the suite scripts to a
// teammate, so a binding verdict is not downgraded for being self-authored.
const conformanceGitHubLogin = "gh\x00api\x00user\x00--jq\x00.login"

const conformanceArgvSep = "\x00"

// conformanceCommentScenario names the plain-comment scenario, the one whose
// body payload every host must carry identically.
const conformanceCommentScenario = "comment"

// conformanceReview is the review every row posts: one inline finding with a
// multi-line anchor, so a host that flattens anchors has one to flatten and a
// host that carries them has one to keep.
const conformanceReview = `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,` +
	`"comments":[{"path":"` + conformancePath + `","line":2,"start_line":1,"side":"RIGHT",` +
	`"body":"Consider clarifying this line.","rigor":"pointed","severity":"informational"}]}`

// hostConformance is one host's row: the exact argv each write it can fire
// uses, and the degradations docs/manual.md promises for what it cannot do.
type hostConformance struct {
	provider string
	// gaps is the phrase docs/manual.md's reason-code row and the operator
	// message must share, keyed by a submit capability the host lacks.
	gaps map[string]string
	// partial is the word docs/manual.md's submit row and the runtime warning
	// must share, keyed by a capability the host carries but loses part of.
	partial map[string]string
	// noSubmitDoc is the phrase docs/manual.md's §Providers Gaps cell promises
	// a fetch-only host's refusal stands in for.
	noSubmitDoc string
	// bodyArgv carries the summary body. Empty means the host has no submit
	// path at all, so post refuses before any call.
	bodyArgv string
	// anchorArgv carries one anchored finding. Empty means anchors ride
	// bodyArgv inside its payload.
	anchorArgv string
	// approveArgv records an approval. Empty means the approval rides bodyArgv
	// as that payload's own verdict event.
	approveArgv string
}

// conformanceRows is the matrix: the two hosts with a submit path, and one
// fetch-only host whose whole row must refuse.
func conformanceRows() []*hostConformance {
	return []*hostConformance{
		{
			provider: providerGitHub,
			bodyArgv: conformanceGitHubReview,
		},
		{
			provider:    providerGitLab,
			bodyArgv:    gitlabNotesArgv,
			anchorArgv:  gitlabDiscussionsArgv,
			approveArgv: gitlabApproveArgv,
			gaps:        map[string]string{conformanceRequestChanges: "no request-changes review event"},
			partial:     map[string]string{conformanceInlineAnchors: "multi-line"},
		},
		{
			provider:    "bitbucket",
			noSubmitDoc: "no grouped submit",
		},
	}
}

// TestProviderConformance is the suite: every host answers the three
// questions, and all three answers must agree.
func TestProviderConformance(t *testing.T) {
	docs := conformanceManual(t)
	for _, row := range conformanceRows() {
		t.Run(row.provider, func(t *testing.T) {
			t.Parallel()
			conformanceCheckHost(t, docs, row)
		})
	}
}

// conformanceCheckHost runs every scenario against one host and asserts what
// the run did, what the table advertises, and what the manual promises agree.
func conformanceCheckHost(t *testing.T, docs conformanceDocs, row *hostConformance) {
	t.Helper()
	caps := core.CapabilitiesFor(row.provider)
	counts := writeCounts{}
	warnings := []string{}
	for _, scn := range conformanceScenarios() {
		run := runConformance(t, row, scn)
		counts = counts.add(row.observe(t, run))
		warnings = append(warnings, conformanceEnvelopeWarnings(run)...)
		conformanceCheckScenario(t, row, caps, scn, run)
		if scn.name == conformanceCommentScenario {
			conformanceCheckBody(t, row, caps, run)
			conformanceCheckAnchors(t, row, caps, run)
		}
	}
	sent := strings.Join(warnings, "\n")
	conformanceCheckMatrix(t, row, caps, counts, sent, docs)
	conformanceCheckSubmitRow(t, row, caps, docs)
	conformanceCheckPartial(t, row, caps, sent, docs)
	if row.bodyArgv == "" {
		conformanceCheckNoSubmitDoc(t, row, docs)
	}
	conformanceCheckVerdictSum(t, row, caps, counts)
}

// conformanceScenario is one review the suite asks a host to post: a plain
// comment, a bound request-changes, or a bound approval. A capability only
// fires under the scenario that asks for it.
type conformanceScenario struct {
	name    string
	approve string
	changes bool
	bind    bool
	force   bool
	dryRun  bool
}

// conformanceScenarios are the three posts the matrix observes.
func conformanceScenarios() []conformanceScenario {
	return []conformanceScenario{
		{name: conformanceCommentScenario, approve: approveFalse},
		{name: "request-changes", approve: approveFalse, changes: true, bind: true},
		{name: "approve", approve: approveTrue, bind: true, force: true},
	}
}

// conformanceRun is everything one scripted post made observable.
type conformanceRun struct {
	response *core.PostResponse
	refusal  *core.RefusalError
	root     string
	calls    [][]string
	stdin    []string
}

// conformanceEnvelopeWarnings returns the warnings the envelope carries, which
// is where an agent reads a degradation: the refusal's list when the run
// refused, the response's when it posted.
func conformanceEnvelopeWarnings(run *conformanceRun) []string {
	if run.refusal != nil {
		return run.refusal.Warnings
	}
	if run.response != nil {
		return run.response.Warnings
	}
	return nil
}

// runConformance posts one scenario against one host with a fake Commander and
// returns the calls, the payloads, and the envelope inputs.
func runConformance(t *testing.T, row *hostConformance, scn conformanceScenario) *conformanceRun {
	t.Helper()
	root := conformanceBundle(t, row.provider)
	cmdr := conformanceCommander(t, row)
	var warnings, human []string
	flags := core.PostFlags{
		Approve: scn.approve, RequestChanges: scn.changes, Bind: scn.bind,
		Force: scn.force, DryRun: scn.dryRun,
		ReviewFile: filepath.Join(conformanceBundleDir(root), "review.json"),
	}
	req := postTestReq(cmdr, flags, &warnings, &human)
	req.RepoRoot = root
	resp, ref := Run(context.Background(), req)
	return &conformanceRun{calls: cmdr.calls, stdin: cmdr.stdin, root: root, response: resp, refusal: ref}
}

// conformanceBundleDir is the bundle a conformance root holds.
func conformanceBundleDir(root string) string {
	return filepath.Join(root, ".verdict", "state", "7")
}

// conformanceBundle writes the reviewed bundle every row posts from and
// returns its root.
func conformanceBundle(t *testing.T, provider string) string {
	t.Helper()
	root := t.TempDir()
	dir := conformanceBundleDir(root)
	head, base := conformanceHead, conformanceBase
	pr := `{"state":"OPEN","headRefOid":"` + head + `","baseRefOid":"` + base + `","author":{"login":"alice"}}`
	if provider != providerGitHub {
		head, base = gitlabHead, gitlabBase
		pr = `{"provider":"` + provider + `","number":7,"state":"open","author":"alice","head_sha":"` + head +
			`","base_sha":"` + base + `"}`
	}
	for name, body := range map[string]string{
		"PR.json": pr, "full.diff": gitlabTestDiff, "review.json": conformanceReview,
	} {
		if err := writeTestFile(filepath.Join(dir, name), body); err != nil {
			t.Fatal(err)
		}
	}
	seedPostControls(t, dir, base)
	return root
}

// conformanceCommander scripts the reads and writes one host may make. A host
// with no submit path gets an empty script, so any call it makes fails as an
// unexpected argv instead of passing silently.
func conformanceCommander(t *testing.T, row *hostConformance) *scriptCommander {
	t.Helper()
	switch row.provider {
	case providerGitHub:
		cmdr := postCommander(t)
		cmdr.out[conformanceGitHubLogin] = scriptResult{stdout: "teammate\n"}
		return cmdr
	case providerGitLab:
		return gitlabCommander(t)
	default:
		return &scriptCommander{t: t, out: map[string]scriptResult{}}
	}
}

// conformanceTool is the CLI a host's submit path spawns; every other argv on
// that row is a disagreement. It reads the host seam rather than a table, so a
// host whose seam names another tool fails here.
func conformanceTool(provider string) string {
	writer, _ := hostFor(provider)
	return writer.tool()
}

// writeCounts is what the submit path actually did, counted per capability
// column rather than per call: one call can carry a body, an anchor, and a
// verdict at once.
type writeCounts struct {
	post    int
	anchors int
	approve int
	changes int
	resolve int
}

func (c writeCounts) add(o writeCounts) writeCounts {
	return writeCounts{
		post: c.post + o.post, anchors: c.anchors + o.anchors, approve: c.approve + o.approve,
		changes: c.changes + o.changes, resolve: c.resolve + o.resolve,
	}
}

// observe counts the writes one run fired, from the calls and their payloads —
// never from the table.
func (row *hostConformance) observe(t *testing.T, run *conformanceRun) writeCounts {
	t.Helper()
	var counts writeCounts
	for i, call := range run.calls {
		counts.resolve += conformanceResolveWrite(call)
		counts = counts.add(row.writeCount(t, run, i, call))
	}
	return counts
}

// writeCount counts what one call wrote for this host's shape.
func (row *hostConformance) writeCount(t *testing.T, run *conformanceRun, i int, call []string) writeCounts {
	t.Helper()
	if row.bodyArgv == "" {
		// No submit path: any call at all is the disagreement.
		return writeCounts{post: 1}
	}
	key := strings.Join(call, conformanceArgvSep)
	switch {
	case key == row.bodyArgv:
		return row.bodyWrite(t, run.stdin[i])
	case row.anchorArgv != "" && key == row.anchorArgv:
		return writeCounts{anchors: 1}
	case row.approveArgv != "" && key == row.approveArgv:
		return writeCounts{approve: 1}
	}
	return writeCounts{}
}

// bodyWrite counts what the summary-body payload carried. A verdict counts as
// a host write only where the host takes it that way: on an atomic host the
// review payload's event is the write, while a replay host's payload event is
// verdict's own record of what it asked for.
func (row *hostConformance) bodyWrite(t *testing.T, stdin string) writeCounts {
	t.Helper()
	counts := writeCounts{post: 1}
	payload := conformancePayload(t, stdin)
	if row.anchorArgv == "" {
		counts.anchors = len(jq.Elements(payload["comments"]))
	}
	if row.verbInPayload() {
		switch payload["event"] {
		case eventApprove:
			counts.approve = 1
		case eventRequestChanges:
			counts.changes = 1
		}
	}
	return counts
}

// conformanceResolveWrite reports whether one call is a thread-resolve
// mutation: GitHub's resolveReviewThread mutation, or GitLab's discussion
// resolve endpoint. verdict ships neither, which is why thread_resolution reads
// false on every host.
func conformanceResolveWrite(call []string) int {
	joined := strings.Join(call, " ")
	if strings.Contains(joined, "resolveReviewThread") || strings.Contains(joined, "/resolve") {
		return 1
	}
	return 0
}

// conformancePayload decodes one posted stdin body.
func conformancePayload(t *testing.T, raw string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("posted body is not one JSON object: %v\n%s", err, raw)
	}
	return doc
}

// conformanceMap reads one decoded JSON object, reporting nil for anything
// else.
func conformanceMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// conformanceCheckScenario checks one run against the post capability: a host
// with no submit path refuses before any call, and a host that posts only ever
// spawns its own CLI.
func conformanceCheckScenario(t *testing.T, row *hostConformance, caps core.HostCapabilities, scn conformanceScenario, run *conformanceRun) {
	t.Helper()
	if !caps.Post {
		conformanceCheckRefusal(t, row, scn, run)
		return
	}
	if run.refusal != nil {
		t.Fatalf("%s: %s scenario refused: %s: %s", row.provider, scn.name, run.refusal.Code, run.refusal.Message)
	}
	for _, call := range run.calls {
		if call[0] != conformanceTool(row.provider) {
			t.Errorf("%s: %s scenario spawned %q, want %s only",
				row.provider, scn.name, strings.Join(call, " "), conformanceTool(row.provider))
		}
	}
}

// conformanceCheckRefusal proves a fetch-only host refuses the post before any
// call, and that the refusal names the host and the gap instead of an empty
// impossibility.
func conformanceCheckRefusal(t *testing.T, row *hostConformance, scn conformanceScenario, run *conformanceRun) {
	t.Helper()
	if run.refusal == nil {
		t.Fatalf("%s: %s scenario posted on a host with no submit path (%q)", row.provider, scn.name, run.calls)
	}
	if run.refusal.Code != "provider-unimplemented" {
		t.Errorf("%s: refusal code = %q, want provider-unimplemented", row.provider, run.refusal.Code)
	}
	if !strings.Contains(run.refusal.Message, row.provider) || !strings.Contains(run.refusal.Message, "not implemented") {
		t.Errorf("%s: refusal %q does not name the host and the gap", row.provider, run.refusal.Message)
	}
	if len(run.calls) != 0 {
		t.Errorf("%s: %s scenario fired %d call(s) before refusing: %q", row.provider, scn.name, len(run.calls), run.calls)
	}
}

// conformanceCheckMatrix asserts the three sources agree, column by column.
func conformanceCheckMatrix(t *testing.T, row *hostConformance, caps core.HostCapabilities, counts writeCounts, sent string, docs conformanceDocs) {
	t.Helper()
	for _, column := range conformanceColumns {
		conformanceCheckColumn(t, row, caps, counts, column, sent, docs)
	}
}

// conformanceCheckColumn checks one capability column: the table's boolean
// against the writes the submit path fired, then the documented gap.
func conformanceCheckColumn(t *testing.T, row *hostConformance, caps core.HostCapabilities, counts writeCounts, column, sent string, docs conformanceDocs) {
	t.Helper()
	advertised := advertisedCapability(t, caps, column)
	fired := capabilityWrites(t, counts, column)
	switch {
	case advertised && fired == 0:
		t.Errorf("%s advertises %s=true but the submit path never fired the write that carries it (%s)",
			row.provider, column, row.carriedBy(column))
	case !advertised && fired > 0:
		t.Errorf("%s advertises %s=false but the submit path fired %d write(s) for it (%s) — the table lies",
			row.provider, column, fired, row.carriedBy(column))
	}
	if slices.Contains(conformanceSubmitColumns, column) {
		conformanceCheckGap(t, row, caps, column, sent, docs)
	}
}

// conformanceCheckGap checks one submit column's documentation.
func conformanceCheckGap(t *testing.T, row *hostConformance, caps core.HostCapabilities, column, sent string, docs conformanceDocs) {
	t.Helper()
	if advertisedCapability(t, caps, column) {
		if phrase, promised := row.gaps[column]; promised {
			t.Errorf("%s advertises %s=true but its row still documents the gap %q — the docs and the table disagree",
				row.provider, column, phrase)
		}
		return
	}
	conformanceCheckMissing(t, row, caps, column, sent, docs)
}

// conformanceCheckMissing checks one absent submit capability: the manual's
// reason-code row and the operator message must share one phrase. A fetch-only
// host has one documented refusal for its whole row, so its columns only have
// to share one cause; conformanceCheckNoSubmitDoc checks that promise.
func conformanceCheckMissing(t *testing.T, row *hostConformance, caps core.HostCapabilities, column, sent string, docs conformanceDocs) {
	t.Helper()
	if row.bodyArgv == "" {
		if got, want := reasonFor(caps, column), reasonFor(caps, conformancePost); got != want {
			t.Errorf("%s: %s reason = %q, want the row's one cause %q", row.provider, column, got, want)
		}
		return
	}
	phrase, promised := row.gaps[column]
	if !promised {
		t.Errorf("%s advertises %s=false and the submit path agrees, but docs/manual.md promises nothing for it on this host",
			row.provider, column)
		return
	}
	conformanceCheckPhrase(t, row, caps, column, phrase, sent, docs)
}

// conformanceCheckPhrase checks the documented phrase, both ends of it: the
// manual's reason-code meaning and the message the operator reads.
func conformanceCheckPhrase(t *testing.T, row *hostConformance, caps core.HostCapabilities, column, phrase, sent string, docs conformanceDocs) {
	t.Helper()
	reason := reasonFor(caps, column)
	documented, ok := docs.codes[reason]
	if !ok {
		t.Errorf("%s: %s reason %q is not a published code", row.provider, column, reason)
		return
	}
	if !strings.Contains(documented, phrase) {
		t.Errorf("%s: docs/manual.md %q row reads %q, which does not promise %q", row.provider, reason, documented, phrase)
	}
	if !strings.Contains(sent, phrase) {
		t.Errorf("%s: advertises %s=false but no message told the operator %q (warnings: %q)", row.provider, column, phrase, sent)
	}
}

// conformanceCheckSubmitRow checks the manual's per-host submit row against the
// table.
func conformanceCheckSubmitRow(t *testing.T, row *hostConformance, caps core.HostCapabilities, docs conformanceDocs) {
	t.Helper()
	if row.bodyArgv == "" {
		return
	}
	cell, ok := docs.submit[row.provider]
	if !ok {
		t.Errorf("docs/manual.md lost the submit row for %s", row.provider)
		return
	}
	conformanceCheckSubmitCell(t, row, cell, missingSubmitColumns(t, caps))
}

// conformanceCheckSubmitCell checks one host's Gaps cell: every submit
// capability the host lacks is named, and a host that carries all four keeps
// the cell empty.
func conformanceCheckSubmitCell(t *testing.T, row *hostConformance, cell string, missing []string) {
	t.Helper()
	if len(missing) == 0 {
		if strings.TrimSpace(cell) != "—" {
			t.Errorf("%s carries all four submit capabilities but its submit row claims gaps: %q", row.provider, cell)
		}
		return
	}
	for _, column := range missing {
		if !strings.Contains(cell, column) {
			t.Errorf("%s advertises %s=false but its submit row does not name it: %q", row.provider, column, cell)
		}
	}
}

// conformanceCheckNoSubmitDoc checks a fetch-only host's documented refusal:
// the manual's §Providers row names the gap the refusal stands in for.
func conformanceCheckNoSubmitDoc(t *testing.T, row *hostConformance, docs conformanceDocs) {
	t.Helper()
	cell, ok := docs.providers[row.provider]
	if !ok {
		t.Errorf("docs/manual.md lost the §Providers row for %s", row.provider)
		return
	}
	if !strings.Contains(cell, row.noSubmitDoc) {
		t.Errorf("%s: docs/manual.md §Providers Gaps cell reads %q, which does not promise %q",
			row.provider, cell, row.noSubmitDoc)
	}
}

// conformanceCheckPartial checks a documented partial loss: a capability the
// host carries, but not whole.
func conformanceCheckPartial(t *testing.T, row *hostConformance, caps core.HostCapabilities, sent string, docs conformanceDocs) {
	t.Helper()
	for _, column := range conformanceSubmitColumns {
		if concept, documented := row.partial[column]; documented {
			conformanceCheckPartialColumn(t, row, caps, column, concept, sent, docs.submit[row.provider])
		}
	}
}

// conformanceCheckPartialColumn checks one documented partial loss: the
// manual's submit row and the runtime message must name the same limitation,
// and the table must still say the host carries the capability.
func conformanceCheckPartialColumn(t *testing.T, row *hostConformance, caps core.HostCapabilities, column, concept, sent, cell string) {
	t.Helper()
	if !advertisedCapability(t, caps, column) {
		t.Errorf("%s documents a partial %s loss, but the table says the host does not carry it at all",
			row.provider, column)
		return
	}
	if !strings.Contains(cell, concept) {
		t.Errorf("%s: docs/manual.md submit row reads %q, which does not promise the %s loss", row.provider, cell, concept)
	}
	if !strings.Contains(sent, concept) {
		t.Errorf("%s: the manual promises a %s loss but no message named it (warnings: %q)", row.provider, concept, sent)
	}
}

// conformanceCheckVerdictSum proves review_verdicts is the derived sum of the
// two split flags in both directions: what the table derives, and what the
// submit path can actually write.
func conformanceCheckVerdictSum(t *testing.T, row *hostConformance, caps core.HostCapabilities, counts writeCounts) {
	t.Helper()
	if want := caps.Approve && caps.RequestChanges; caps.ReviewVerdicts != want {
		t.Errorf("%s: review_verdicts = %v, want approve && request_changes = %v",
			row.provider, caps.ReviewVerdicts, want)
	}
	if wrote := counts.approve > 0 && counts.changes > 0; caps.ReviewVerdicts != wrote {
		t.Errorf("%s: review_verdicts = %v but the submit path wrote approve=%d request-changes=%d",
			row.provider, caps.ReviewVerdicts, counts.approve, counts.changes)
	}
}

// conformanceCheckBody proves the body write carried the review: the review
// prose with its hidden repeat marker, the reviewed commit where the host takes
// one in the payload, and no anchor on a host that cannot carry one.
func conformanceCheckBody(t *testing.T, row *hostConformance, caps core.HostCapabilities, run *conformanceRun) {
	t.Helper()
	if !caps.Post {
		return
	}
	payloads := row.bodyPayloads(t, run)
	if len(payloads) != 1 {
		t.Fatalf("%s: body writes = %d, want the review's one", row.provider, len(payloads))
	}
	body := jq.StrOf(payloads[0]["body"])
	for _, want := range []string{"Looks good", "<!-- verdict-review payload="} {
		conformanceCheckContains(t, row.provider, "the posted body", body, want)
	}
	if row.verbInPayload() {
		if got := jq.StrOf(payloads[0]["commit_id"]); got != row.head() {
			t.Errorf("%s: posted commit_id = %q, want the reviewed head %s", row.provider, got, row.head())
		}
	}
}

// bodyPayloads decodes the summary-body writes one run sent.
func (row *hostConformance) bodyPayloads(t *testing.T, run *conformanceRun) []map[string]any {
	t.Helper()
	var out []map[string]any
	for i, call := range run.calls {
		if row.bodyArgv != "" && strings.Join(call, conformanceArgvSep) == row.bodyArgv {
			out = append(out, conformancePayload(t, run.stdin[i]))
		}
	}
	return out
}

// verbInPayload reports whether the host takes its verdicts in the body
// payload's own event field, which is what an atomic review does.
func (row *hostConformance) verbInPayload() bool {
	return row.approveArgv == ""
}

// head is the commit the row reviews.
func (row *hostConformance) head() string {
	if row.provider == providerGitHub {
		return conformanceHead
	}
	return gitlabHead
}

// anchoredFinding is one finding a run sent as an anchored comment, in the
// shape the host's own API names it.
type anchoredFinding struct {
	path  string
	head  string
	line  int
	start int
}

// conformanceCheckAnchors proves the anchor write carries the reviewed finding,
// and that a host whose row documents flattening shows the loss in the position
// it writes instead of silently sending it anyway.
func conformanceCheckAnchors(t *testing.T, row *hostConformance, caps core.HostCapabilities, run *conformanceRun) {
	t.Helper()
	findings := row.anchoredFindings(t, run)
	if !caps.InlineAnchors {
		if len(findings) > 0 {
			t.Errorf("%s advertises inline_anchors=false but sent %d anchored finding(s)", row.provider, len(findings))
		}
		return
	}
	if len(findings) != 1 {
		t.Fatalf("%s: anchored findings = %d, want the review's one finding", row.provider, len(findings))
	}
	conformanceCheckAnchor(t, row, findings[0])
}

// conformanceCheckAnchor checks one anchored finding against the review.
func conformanceCheckAnchor(t *testing.T, row *hostConformance, got anchoredFinding) {
	t.Helper()
	if got.path != conformancePath || got.line != conformanceLine {
		t.Errorf("%s: anchored at %s:%d, want %s:%d", row.provider, got.path, got.line, conformancePath, conformanceLine)
	}
	if row.anchorArgv != "" && got.head != row.head() {
		t.Errorf("%s: position head_sha = %q, want the reviewed head %s", row.provider, got.head, row.head())
	}
	_, flattened := row.partial[conformanceInlineAnchors]
	switch {
	case flattened && got.start != 0:
		t.Errorf("%s: the anchor kept start_line=%d, but its row documents flattening it", row.provider, got.start)
	case !flattened && got.start != conformanceStartLine:
		t.Errorf("%s: anchor start_line = %d, want the review's multi-line anchor kept (%d)",
			row.provider, got.start, conformanceStartLine)
	}
}

// anchoredFindings decodes the anchored findings one run sent: review comments
// on an atomic host, discussion positions on a replay host.
func (row *hostConformance) anchoredFindings(t *testing.T, run *conformanceRun) []anchoredFinding {
	t.Helper()
	var out []anchoredFinding
	for i, call := range run.calls {
		key := strings.Join(call, conformanceArgvSep)
		switch {
		case row.verbInPayload() && row.bodyArgv != "" && key == row.bodyArgv:
			out = append(out, conformanceReviewComments(t, run.stdin[i])...)
		case row.anchorArgv != "" && key == row.anchorArgv:
			out = append(out, conformanceDiscussionAnchor(t, run.stdin[i]))
		}
	}
	return out
}

// conformanceReviewComments reads the comments one atomic review carried.
func conformanceReviewComments(t *testing.T, stdin string) []anchoredFinding {
	t.Helper()
	var out []anchoredFinding
	for _, c := range jq.Elements(conformancePayload(t, stdin)["comments"]) {
		comment := conformanceMap(c)
		out = append(out, anchoredFinding{
			path: jq.StrOf(comment["path"]), line: conformanceInt(comment["line"]), start: conformanceInt(comment["start_line"]),
		})
	}
	return out
}

// conformanceDiscussionAnchor reads the one anchor a discussion position
// carries.
func conformanceDiscussionAnchor(t *testing.T, stdin string) anchoredFinding {
	t.Helper()
	pos := conformanceMap(conformancePayload(t, stdin)["position"])
	return anchoredFinding{
		path: jq.StrOf(pos["new_path"]), head: jq.StrOf(pos["head_sha"]),
		line: conformanceInt(pos["new_line"]), start: conformanceInt(pos["start_line"]),
	}
}

// conformanceInt reads one integer field, reporting 0 when it is missing.
func conformanceInt(v any) int {
	if n, ok := jq.IntOf(v); ok {
		return n
	}
	return 0
}

// advertisedCapability reads one column of the capability table. The switch is
// the published column list: a column added to data.capabilities without a case
// here fails the suite instead of passing silently.
func advertisedCapability(t *testing.T, caps core.HostCapabilities, column string) bool {
	t.Helper()
	switch column {
	case "fetch":
		return caps.Fetch
	case conformancePost:
		return caps.Post
	case conformanceInlineAnchors:
		return caps.InlineAnchors
	case conformanceApprove:
		return caps.Approve
	case conformanceRequestChanges:
		return caps.RequestChanges
	case conformanceThreadResolution:
		return caps.ThreadResolution
	default:
		t.Fatalf("unknown capability column %q", column)
		return false
	}
}

// capabilityWrites reads one column of the observed counts.
func capabilityWrites(t *testing.T, counts writeCounts, column string) int {
	t.Helper()
	switch column {
	case conformancePost:
		return counts.post
	case conformanceInlineAnchors:
		return counts.anchors
	case conformanceApprove:
		return counts.approve
	case conformanceRequestChanges:
		return counts.changes
	case conformanceThreadResolution:
		return counts.resolve
	default:
		t.Fatalf("unknown capability column %q", column)
		return 0
	}
}

// missingSubmitColumns lists the submit capabilities the table says this host
// cannot carry.
func missingSubmitColumns(t *testing.T, caps core.HostCapabilities) []string {
	t.Helper()
	var out []string
	for _, column := range conformanceSubmitColumns {
		if !advertisedCapability(t, caps, column) {
			out = append(out, column)
		}
	}
	return out
}

// reasonFor returns the published reason the table carries for one absent
// capability.
func reasonFor(caps core.HostCapabilities, column string) string {
	for _, gap := range caps.Unsupported() {
		if gap.Capability == column {
			return gap.Reason
		}
	}
	return ""
}

// conformanceHints is the per-column seam hint a mismatch reports.
type conformanceHints struct {
	post    string
	anchors string
	approve string
	changes string
}

// carriedBy names the call a capability would ride on this host, so a failure
// points at the seam instead of only the flag.
func (row *hostConformance) carriedBy(column string) string {
	hints := row.hints()
	switch column {
	case conformancePost:
		return hints.post
	case conformanceInlineAnchors:
		return hints.anchors
	case conformanceApprove:
		return hints.approve
	case conformanceRequestChanges:
		return hints.changes
	default:
		return "verdict ships no resolve mutation on any host"
	}
}

// hints builds the per-column failure hints once.
func (row *hostConformance) hints() conformanceHints {
	body := conformanceArgvText(row.bodyArgv)
	anchor := conformanceArgvText(row.anchorArgv)
	approve := conformanceArgvText(row.approveArgv)
	return conformanceHints{
		post:    conformanceEither(body == "", "this host has no submit path", "the "+body+" call"),
		anchors: conformanceEither(anchor != "", "the "+anchor+" call", "the "+body+" payload's comments"),
		approve: conformanceEither(approve != "", "the "+approve+" call", "the "+body+" payload's APPROVE event"),
		changes: conformanceEither(approve != "", "this host has no review-event write to carry it",
			"the "+body+" payload's REQUEST_CHANGES event"),
	}
}

// conformanceEither picks one of two strings.
func conformanceEither(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

// conformanceArgvText renders one argv for a failure message.
func conformanceArgvText(argv string) string {
	return strings.ReplaceAll(argv, conformanceArgvSep, " ")
}

// TestConformanceReasonCodes proves the table's reasons are the published set:
// every absent capability carries one, every code the table emits is
// documented, and every documented code is emitted by a host or named as
// reserved with the host that would emit it.
func TestConformanceReasonCodes(t *testing.T) {
	t.Parallel()
	docs := conformanceManual(t)
	emitted := conformanceEmittedReasons(t, docs)
	for _, code := range slices.Sorted(maps.Keys(docs.codes)) {
		conformanceCheckCode(t, code, emitted[code])
	}
	conformanceCheckThreadProse(t, docs)
}

// conformanceCheckCode checks one published code: a host emits it, or it is
// reserved and proven reachable.
func conformanceCheckCode(t *testing.T, code string, emitSites []string) {
	t.Helper()
	if len(emitSites) > 0 {
		return
	}
	reserved, ok := reservedReasons[code]
	if !ok {
		t.Errorf("docs/manual.md publishes %q but no host emits it and it is not reserved", code)
		return
	}
	conformanceCheckReserved(t, code, reserved)
}

// reservedCode is a published code no shipped host emits, with the synthetic
// host that proves the code stays reachable.
type reservedCode struct {
	capability string
	caps       core.HostCapabilities
}

// reservedReasons names the codes no shipped host emits: every posting host
// carries an approval write and inline anchors today. A code here is reserved,
// not dead — the first body-only host emits it through submitReason.
var reservedReasons = map[string]reservedCode{
	"no-approve-verdict": {
		caps:       core.HostCapabilities{Provider: "body-only", Fetch: true, Post: true, InlineAnchors: true},
		capability: conformanceApprove,
	},
	"no-inline-anchors": {
		caps:       core.HostCapabilities{Provider: "anchors-only", Fetch: true, Post: true, Approve: true, RequestChanges: true},
		capability: conformanceInlineAnchors,
	},
}

// conformanceCheckReserved proves a reserved code is reachable: the synthetic
// host reports it for the capability the code explains.
func conformanceCheckReserved(t *testing.T, code string, reserved reservedCode) {
	t.Helper()
	for _, gap := range reserved.caps.Unsupported() {
		if gap.Reason == code && gap.Capability == reserved.capability {
			return
		}
	}
	t.Errorf("reserved code %q is not reachable: %s reports %v", code, reserved.caps.Provider, reserved.caps.Unsupported())
}

// conformanceEmittedReasons collects every reason the table reports for the
// suite's hosts and for an unknown provider, keyed by code.
func conformanceEmittedReasons(t *testing.T, docs conformanceDocs) map[string][]string {
	t.Helper()
	emitted := map[string][]string{}
	for _, provider := range append(conformanceProviders(), "codeberg") {
		conformanceEmitHost(t, docs, provider, emitted)
	}
	return emitted
}

// conformanceEmitHost records the reasons one provider's row reports and checks
// each sits beside a false flag and a published code.
func conformanceEmitHost(t *testing.T, docs conformanceDocs, provider string, emitted map[string][]string) {
	t.Helper()
	caps := core.CapabilitiesFor(provider)
	for _, gap := range caps.Unsupported() {
		if advertisedCapability(t, caps, gap.Capability) {
			t.Errorf("%s: reason %q sits beside a capability that reads true", provider, gap.Reason)
		}
		if _, ok := docs.codes[gap.Reason]; !ok {
			t.Errorf("%s: %s reason %q is not a code docs/manual.md publishes", provider, gap.Capability, gap.Reason)
		}
		emitted[gap.Reason] = append(emitted[gap.Reason], provider+"/"+gap.Capability)
	}
}

// conformanceCheckThreadProse checks the one promise the manual makes for every
// host instead of per host: thread resolution is absent everywhere, so the
// section prose (not a submit row) has to say so.
func conformanceCheckThreadProse(t *testing.T, docs conformanceDocs) {
	t.Helper()
	for _, row := range conformanceRows() {
		if core.CapabilitiesFor(row.provider).ThreadResolution {
			return
		}
	}
	if !strings.Contains(docs.prose, "false on every host") {
		t.Error("docs/manual.md no longer says thread_resolution is false on every host, which is the whole promise for it")
	}
}

// conformanceProviders lists the hosts the matrix covers.
func conformanceProviders() []string {
	providers := make([]string, 0, len(conformanceRows()))
	for _, row := range conformanceRows() {
		providers = append(providers, row.provider)
	}
	return providers
}

// conformanceDocs is the documentation side of the matrix: the published reason
// codes, the §Providers Gaps cell and the submit-table Gaps cell per host, and
// the section prose.
type conformanceDocs struct {
	codes     map[string]string
	providers map[string]string
	submit    map[string]string
	prose     string
}

// add files one table row under the table its column count names.
func (d conformanceDocs) add(cells []string) {
	name := strings.Trim(cells[0], "`")
	switch len(cells) {
	case 2:
		d.codes[name] = cells[1]
	case 3:
		d.submit[name] = cells[2]
	case 4:
		d.providers[name] = cells[3]
	}
}

// conformanceManual reads docs/manual.md and slices the provider contract the
// suite checks. It reads the real file rather than a fixture: the manual is the
// operator contract, and a passing test that stopped reading it would certify
// nothing.
func conformanceManual(t *testing.T) conformanceDocs {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(conformanceModuleRoot(t), "docs", "manual.md"))
	if err != nil {
		t.Fatalf("read docs/manual.md: %v", err)
	}
	prose, ok := conformanceSection(string(raw), "## Providers (non-GitHub hosts)")
	if !ok {
		t.Fatal("docs/manual.md lost the Providers section")
	}
	docs := conformanceDocs{
		prose: prose, codes: map[string]string{}, providers: map[string]string{}, submit: map[string]string{},
	}
	for _, cells := range conformanceTableRows(prose) {
		docs.add(cells)
	}
	return docs
}

// conformanceSection returns the body under one exact heading, up to the next
// second-rank heading: the provider section owns its paragraph tables and its
// `### Submit` subsection.
func conformanceSection(manual, heading string) (string, bool) {
	lines := strings.Split(manual, "\n")
	start := slices.Index(lines, heading)
	if start < 0 {
		return "", false
	}
	end := start + 1
	for end < len(lines) && !strings.HasPrefix(lines[end], "## ") {
		end++
	}
	return strings.Join(lines[start+1:end], "\n"), true
}

// conformanceTableRows returns the data rows of every pipe table in s: cells
// trimmed, header and separator rows dropped by the backticked-name rule.
func conformanceTableRows(s string) [][]string {
	var rows [][]string
	for line := range strings.SplitSeq(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if !conformanceBackticked(cells[0]) {
			continue
		}
		rows = append(rows, cells)
	}
	return rows
}

// conformanceBackticked reports whether a table's first cell is a backticked
// name, which is how the manual marks a data row (a host or a reason code).
func conformanceBackticked(cell string) bool {
	return len(cell) > 2 && strings.HasPrefix(cell, "`") && strings.HasSuffix(cell, "`")
}

// conformanceModuleRoot walks up from the test's working directory to the
// checkout root, so the suite reads the real manual wherever `go test` runs.
func conformanceModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, serr := os.Stat(filepath.Join(dir, "go.mod")); serr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test working directory")
		}
		dir = parent
	}
}

// TestConformanceDryRunBytes proves a degradation reaches the bytes a
// `post --dry-run --json` prints: the requested event and the documented gap
// ride the envelope the CLI renders, and no write fires. A host that carries
// the verdict must not print another host's degradation sentence.
func TestConformanceDryRunBytes(t *testing.T) {
	for _, row := range conformanceRows() {
		t.Run(row.provider, func(t *testing.T) {
			t.Parallel()
			conformanceCheckDryRun(t, row)
		})
	}
}

// conformanceCheckDryRun posts the request-changes scenario as a dry run and
// reads the bytes it would print.
func conformanceCheckDryRun(t *testing.T, row *hostConformance) {
	t.Helper()
	caps := core.CapabilitiesFor(row.provider)
	scn := conformanceScenario{name: "request-changes", approve: approveFalse, changes: true, bind: true, dryRun: true}
	run := runConformance(t, row, scn)
	conformanceCheckScenario(t, row, caps, scn, run)
	if counts := row.observe(t, run); counts != (writeCounts{}) {
		t.Errorf("%s: dry run wrote %+v, want nothing", row.provider, counts)
	}
	if !caps.Post {
		return
	}
	conformanceCheckDryRunBytes(t, row, caps, run)
}

// conformanceCheckDryRunBytes asserts the degradation rides the printed bytes.
func conformanceCheckDryRunBytes(t *testing.T, row *hostConformance, caps core.HostCapabilities, run *conformanceRun) {
	t.Helper()
	bytes := conformanceEnvelopeBytes(t, run.response)
	for _, want := range []string{`"dry_run": true`, `"event": "` + eventRequestChanges + `"`} {
		conformanceCheckContains(t, row.provider, "--dry-run bytes", bytes, want)
	}
	for _, phrase := range conformanceGapPhrases() {
		want := !caps.RequestChanges && row.gaps[conformanceRequestChanges] == phrase
		if got := strings.Contains(bytes, phrase); got != want {
			t.Errorf("%s: --dry-run bytes carry the degradation %q = %v, want %v:\n%s",
				row.provider, phrase, got, want, bytes)
		}
	}
}

// conformanceCheckContains fails when got misses want.
func conformanceCheckContains(t *testing.T, provider, surface, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("%s: %s miss %s:\n%s", provider, surface, want, got)
	}
}

// TestConformanceReceiptRecord proves a degraded post is visible in the record
// it leaves: post.json carries the state the host accepted, while the envelope
// carries the event verdict asked for, so the pair shows the loss — and the
// receipt's withheld entry names the capability and the published reason code,
// so the loss does not depend on an envelope a later reader no longer has.
func TestConformanceReceiptRecord(t *testing.T) {
	t.Parallel()
	row := conformanceRow(t, providerGitLab)
	run := runConformance(t, row, conformanceScenario{name: "request-changes", approve: approveFalse, changes: true, bind: true})
	if run.refusal != nil {
		t.Fatalf("Run refusal: %s: %s", run.refusal.Code, run.refusal.Message)
	}
	if got := run.response.Data.Event; got != eventRequestChanges {
		t.Errorf("envelope event = %q, want %s (verdict's own record of the request)", got, eventRequestChanges)
	}
	raw, receipt := conformanceReceipt(t, run.root)
	review := conformanceMap(receipt["review"])
	if got := jq.StrOf(review["state"]); got != "commented" {
		t.Errorf("receipt review.state = %q, want the state the host accepted (commented)", got)
	}
	for _, want := range []string{conformanceRequestChanges, "no-request-changes-event"} {
		conformanceCheckContains(t, row.provider, "post.json", raw, want)
	}
}

// TestConformanceReceiptWithheld separates the two receipts a GitLab post can
// leave. A bound request-changes the host cannot carry records the withheld
// capability, its published reason code, and the event the run asked for beside
// review.state=commented — where a plain comment leaves review.state=commented
// with no withheld key at all. Without the field the two receipts differ
// nowhere a later reader looks; an undegraded post carries no noise.
func TestConformanceReceiptWithheld(t *testing.T) {
	t.Parallel()
	row := conformanceRow(t, providerGitLab)
	degraded := runConformance(t, row, conformanceScenario{name: "request-changes", approve: approveFalse, changes: true, bind: true})
	if degraded.refusal != nil {
		t.Fatalf("Run refusal: %s: %s", degraded.refusal.Code, degraded.refusal.Message)
	}
	_, degradedReceipt := conformanceReceipt(t, degraded.root)
	conformanceCheckWithheld(t, degradedReceipt, eventRequestChanges)
	if got := conformanceMap(degradedReceipt["review"])["state"]; jq.StrOf(got) != "commented" {
		t.Fatalf("degraded review.state = %v, want the state the host accepted (commented)", got)
	}

	plain := runConformance(t, row, conformanceScenario{name: conformanceCommentScenario, approve: approveFalse})
	if plain.refusal != nil {
		t.Fatalf("Run refusal: %s: %s", plain.refusal.Code, plain.refusal.Message)
	}
	raw, plainReceipt := conformanceReceipt(t, plain.root)
	if _, present := plainReceipt["withheld"]; present {
		t.Errorf("%s: a plain comment records withheld: %s", row.provider, raw)
	}
	if got := conformanceMap(plainReceipt["review"])["state"]; jq.StrOf(got) != "commented" {
		t.Fatalf("plain review.state = %v, want commented", got)
	}
}

// conformanceCheckWithheld asserts one withheld entry: the capability row the
// table publishes, its reason code, and the binding event the run asked for.
func conformanceCheckWithheld(t *testing.T, receipt map[string]any, requested string) {
	t.Helper()
	entries := jq.Elements(receipt["withheld"])
	if len(entries) != 1 {
		t.Fatalf("withheld = %v, want the one capability this host withheld", receipt["withheld"])
	}
	entry := conformanceMap(entries[0])
	for _, field := range []struct {
		key  string
		want string
	}{
		{key: "capability", want: conformanceRequestChanges},
		{key: "reason", want: "no-request-changes-event"},
		{key: "requested_event", want: requested},
	} {
		if got := jq.StrOf(entry[field.key]); got != field.want {
			t.Errorf("withheld %s = %q, want %q", field.key, got, field.want)
		}
	}
}

// conformanceReceipt reads a posted bundle's receipt, raw and decoded.
func conformanceReceipt(t *testing.T, root string) (string, map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(conformanceBundleDir(root), "post.json"))
	if err != nil {
		t.Fatalf("read post.json: %v", err)
	}
	var receipt map[string]any
	if uerr := json.Unmarshal(raw, &receipt); uerr != nil {
		t.Fatalf("post.json is not one JSON object: %v", uerr)
	}
	return string(raw), receipt
}

// conformanceRow returns one row by provider.
func conformanceRow(t *testing.T, provider string) *hostConformance {
	t.Helper()
	for _, row := range conformanceRows() {
		if row.provider == provider {
			return row
		}
	}
	t.Fatalf("no conformance row for %s", provider)
	return nil
}

// conformanceGapPhrases lists every degradation phrase a row documents, in a
// deterministic order.
func conformanceGapPhrases() []string {
	var phrases []string
	for _, row := range conformanceRows() {
		for _, phrase := range row.gaps {
			if !slices.Contains(phrases, phrase) {
				phrases = append(phrases, phrase)
			}
		}
	}
	slices.Sort(phrases)
	return phrases
}

// conformanceEnvelopeBytes renders the envelope exactly as the CLI prints it
// for `post --json`: the frozen key order over the ordered data object.
func conformanceEnvelopeBytes(t *testing.T, resp *core.PostResponse) string {
	t.Helper()
	return jq.PrettyOrdered(RenderPostEnvelope(resp))
}
