// Package readycore is the ready verb's domain service: the readiness
// advisory over live PR state (checks, threads, draft/conflict,
// description). Advisory runs exit 0; --check gates for hooks and CI. All
// gh reads go through the injected commander; the probe stderr stages
// beside the bundle as an owner-only sidecar. internal/ready owns argv
// parsing, progress routing, envelope rendering, and exit mapping.
package readycore

import (
	"context"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request carries the resolved ready invocation: the shared read runtime
// plus the --check gate flag.
type Request struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	Check    bool
	Warnings []string
}

// Response carries the completed advisory: the envelope fields, the gate
// outcome, and the typed data profile. Human lines stream live through the
// runtime progress observer, so the response carries no rendered lines.
type Response struct { //nolint:govet // field grouping documents the response surface
	Repo       string
	Pr         string
	At         string
	Next       string
	Warnings   []string
	Data       *Data
	Ready      bool
	GateFailed bool
}

// Data is the ready verb's --json data profile. Field order is the published
// jq construction order.
type Data struct { //nolint:govet // field order is the published property order
	Ready            bool
	Recommendation   string
	Blockers         []Blocker
	State            string
	Draft            bool
	Mergeable        string
	MergeState       string
	CI               CICounts
	ThreadsOpen      int
	DescriptionChars int
}

// Blocker is one readiness blocker with its recovery action.
type Blocker struct {
	Kind   string
	Detail string
	Action string
}

// CICounts is the live CI check tally.
type CICounts struct {
	Failing int
	Total   int
}

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	blockers := make([]any, 0, len(d.Blockers))
	for _, b := range d.Blockers {
		blockers = append(blockers, jq.NewObj(
			"kind", b.Kind,
			"detail", b.Detail,
			"action", b.Action,
		))
	}
	return jq.NewObj(
		"ready", d.Ready,
		"recommendation", d.Recommendation,
		"blockers", blockers,
		"state", d.State,
		"draft", d.Draft,
		"mergeable", d.Mergeable,
		"mergeStateStatus", d.MergeState,
		"ci", jq.NewObj("failing", d.CI.Failing, "total", d.CI.Total),
		"threads_open", d.ThreadsOpen,
		"description_chars", d.DescriptionChars,
	)
}

type state struct {
	req          *Request
	policy       *config.ReadCompliance
	warnings     []string
	requiredGaps []string
	live         map[string]any
	pr           string
	rec          string
	mergeable    string
	mergeState   string
	prState      string
	repo         string
	blockers     []Blocker
	threadsOpen  int
	bodyLen      int
	ciTotal      int
	ciFail       int
	ready        bool
	draft        bool
}

const recWait = "wait"

const hashSep = "#"

const mergeUnknown = "UNKNOWN"

const newline = "\n"

const (
	commentOpen  = "<!--"
	commentClose = "-->"
)

// Run executes the readiness advisory over live PR state.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{
		req:      req,
		repo:     req.Repo,
		pr:       req.Pr,
		warnings: append([]string{}, req.Warnings...),
	}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: codeCanceled, Message: "canceled before ready completed"}
	}
	if ref := loadState(ctx, st); ref != nil {
		return nil, ref
	}
	return st.finish(), nil
}

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

func (st *state) human(line string) {
	if st.req.Runtime != nil {
		st.req.Human(line)
	}
}

func (st *state) diag(line string) {
	if st.req.Runtime != nil {
		st.req.Diag(line)
	}
}

func (st *state) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

func (st *state) commander() core.Commander {
	var commander core.Commander
	if rt := st.runtime(); rt != nil {
		commander = rt.Commander
	}
	return commander
}

func (st *state) runtime() *core.Runtime {
	return st.req.Runtime
}

func (st *state) finish() *Response {
	st.emitHuman()
	next := "nudge the author instead of reviewing"
	switch st.rec {
	case "review":
		next = "fetch + review"
	case recWait:
		next = "hold the review — re-run ready later"
	}
	return &Response{
		Repo:       st.repo,
		Pr:         st.pr,
		At:         st.req.TimeUTC(),
		Next:       next,
		Warnings:   st.warnings,
		Data:       st.data(),
		Ready:      st.ready,
		GateFailed: st.req.Check && !st.ready,
	}
}

func (st *state) data() *Data {
	return &Data{
		Ready:            st.ready,
		Recommendation:   st.rec,
		Blockers:         st.blockers,
		State:            st.prState,
		Draft:            st.draft,
		Mergeable:        st.mergeable,
		MergeState:       st.mergeState,
		CI:               CICounts{Failing: st.ciFail, Total: st.ciTotal},
		ThreadsOpen:      st.threadsOpen,
		DescriptionChars: st.bodyLen,
	}
}

func loadState(ctx context.Context, st *state) *core.RefusalError {
	dir, ref := readcommon.ResolveBundleDir(st.req.RepoRootPath(), st.pr)
	if ref != nil {
		return st.withWarnings(ref)
	}
	if isLocalDir(dir) {
		return st.withWarnings(&core.RefusalError{
			Code:    "no-pr-target",
			Message: "ready needs a PR bundle (fetch owner/repo N first); local bundles have no live checks to advise on",
		})
	}
	host, ref := st.readyHost(dir)
	if ref != nil {
		return ref
	}
	if !isRegular(dir + "/PR.json") {
		return st.withWarnings(&core.RefusalError{
			Code:    "missing-bundle",
			Message: "missing " + dir + "/PR.json (run fetch " + st.repo + " " + st.pr + " first)",
		})
	}
	if ref := host.live(ctx, st, dir); ref != nil {
		return ref
	}
	host.checks(ctx, st)
	if ref := host.threads(ctx, st); ref != nil {
		return ref
	}
	loadPolicyCompliance(st, dir)
	computeReadiness(st)
	return nil
}

const providerGitHub = "github"

func isLocalDir(dir string) bool {
	return readcommon.IsLocalBundleDir(dir)
}

func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func githubLive(ctx context.Context, st *state, dir string) *core.RefusalError {
	stdout, stderr, ok := coreexec.Gh(ctx, st.commander(), "pr", "view", st.pr, "--repo", st.repo,
		"--json", "isDraft,mergeable,mergeStateStatus,body,state,headRefOid")
	writeErrFile(st, dir+"/.ready.err", stderr)
	if !ok {
		return st.withWarnings(&core.RefusalError{
			Code:    waitReadCode,
			Message: "could not read live PR state for " + st.repo + hashSep + st.pr + ": " + firstLine(stderr),
		})
	}
	doc, derr := jq.Decode([]byte(stdout))
	if derr != nil {
		st.live = map[string]any{}
	} else if m, ok := jq.AsMap(doc); ok {
		st.live = m
	} else {
		st.live = map[string]any{}
	}
	applyLive(st, dir)
	return nil
}

// applyLive folds the canonical live map into the advisory: the stale
// warning, the draft/mergeable/state reads, and the unknown-mergeability
// warning. Both hosts decode onto the same keys first, so this tail runs
// once for either provider.
func applyLive(st *state, dir string) {
	fetched := readcommon.PrField(dir, "head_sha")
	liveHead := liveString(st.live, "headRefOid")
	if fetched != "" && liveHead != "" && fetched != liveHead {
		st.warn("bundle stale (fetched " + readcommon.ShortSHA(fetched) + ", live " +
			readcommon.ShortSHA(liveHead) + ") — re-fetch before review, advice reads live")
	}
	st.draft = liveString(st.live, "isDraft") == "true"
	st.mergeable = liveOr(st.live, "mergeable", mergeUnknown)
	st.mergeState = liveOr(st.live, "mergeStateStatus", mergeUnknown)
	st.prState = liveOr(st.live, "state", mergeUnknown)
	st.bodyLen = bodyLength(liveBody(st.live))
	if st.mergeable == mergeUnknown && st.mergeState == mergeUnknown {
		st.warn("mergeability UNKNOWN (host still computing?) — conflict treated as absent, re-run ready if this persists")
	}
}

const probeStagePerm = 0o600

func writeErrFile(st *state, path, stderr string) {
	if err := atomic.WriteFileMode(path, []byte(stderr), probeStagePerm); err != nil {
		st.diag("ready: could not stage probe stderr in " + path + ": " + err.Error())
	}
}

func firstLine(s string) string {
	if len(s) > errHeadBytes {
		s = s[:errHeadBytes]
	}
	return strings.ReplaceAll(s, newline, " ")
}

const errHeadBytes = 200

func liveString(live map[string]any, key string) string {
	v, ok := live[key]
	if !ok || v == nil {
		return ""
	}
	return jq.R(v)
}

func liveOr(live map[string]any, key, dflt string) string {
	v, ok := live[key]
	if !ok || v == nil {
		return dflt
	}
	return jq.R(v)
}

func liveBody(live map[string]any) string {
	v, ok := live["body"]
	if !ok || v == nil {
		return ""
	}
	return jq.R(v)
}

func bodyLength(body string) int {
	var out strings.Builder
	inComment := false
	for line := range strings.SplitSeq(body, newline) {
		out.WriteString(stripComments(line, &inComment))
		out.WriteByte('\n')
	}
	return len(squeezeSpace(out.String()))
}

func stripComments(line string, inComment *bool) string {
	var kept strings.Builder
	rest := line
	for len(rest) > 0 {
		if *inComment {
			rest = skipCommentRest(rest, inComment)
			continue
		}
		rest = takePlainSegment(rest, inComment, &kept)
	}
	return kept.String()
}

func skipCommentRest(rest string, inComment *bool) string {
	if _, after, ok := strings.Cut(rest, commentClose); ok {
		*inComment = false
		return after
	}
	return ""
}

func takePlainSegment(rest string, inComment *bool, kept *strings.Builder) string {
	if before, after, ok := strings.Cut(rest, commentOpen); ok {
		kept.WriteString(before)
		*inComment = true
		return after
	}
	kept.WriteString(rest)
	return ""
}

func squeezeSpace(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	inRun := false
	for i := range len(s) {
		if isSpaceByte(s[i]) {
			if !inRun {
				out.WriteByte(' ')
				inRun = true
			}
			continue
		}
		out.WriteByte(s[i])
		inRun = false
	}
	return out.String()
}

func isSpaceByte(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

func githubChecks(ctx context.Context, st *state) {
	stdout, _, ok := coreexec.Gh(ctx, st.commander(), "pr", "checks", st.pr, "--repo", st.repo,
		"--json", "bucket,name,state")
	raw := "[]"
	if ok {
		raw = stdout
	}
	list := checkElements(raw)
	failNames := []string{}
	anomaly := false
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			anomaly = true
			break
		}
		failNames = countFailBucket(st, m, failNames)
	}
	if anomaly {
		st.ciFail = 0
		st.ciTotal = 0
		return
	}
	st.ciTotal = len(list)
	appendCIFailing(st, failNames)
}

// appendCIFailing records the failing-check blocker once the tally is
// final. Both hosts render the same kind, detail shape, and action.
func appendCIFailing(st *state, failNames []string) {
	if st.ciFail > 0 {
		st.blockers = append(st.blockers, Blocker{
			Kind:   "ci-failing",
			Detail: strconv.Itoa(st.ciFail) + " failing check(s): " + strings.Join(failNames, ", "),
			Action: "fix CI, then re-run ready",
		})
	}
}

func countFailBucket(st *state, m map[string]any, failNames []string) []string {
	bucket, ok := m["bucket"].(string)
	if !ok || bucket != "fail" {
		return failNames
	}
	st.ciFail++
	return append(failNames, checkName(m))
}

func checkElements(raw string) []any {
	doc, err := jq.Decode([]byte(raw))
	if err != nil {
		return []any{}
	}
	switch t := doc.(type) {
	case []any:
		return t
	case map[string]any:
		vals := make([]any, 0, len(t))
		for _, k := range sortedKeys(t) {
			vals = append(vals, t[k])
		}
		return vals
	case nil:
		return []any{}
	default:
		return []any{}
	}
}

func checkName(m map[string]any) string {
	v, ok := m["name"]
	if !ok || v == nil {
		return "null"
	}
	return jq.R(v)
}

func githubThreads(ctx context.Context, st *state) *core.RefusalError {
	owner, name := readcommon.SplitOwner(st.repo)
	nodes, _, err := readcommon.Threads(ctx, st.commander(), owner, name, st.pr, false)
	if err != nil {
		return st.withWarnings(&core.RefusalError{
			Code:    codeThreadsFailed,
			Message: "could not read live review threads for " + st.repo + hashSep + st.pr,
		})
	}
	applyThreadCounts(st, readcommon.OpenThreadCount(nodes), readcommon.MalformedThreadCount(nodes))
	return nil
}

// applyThreadCounts folds an open/malformed thread tally into the
// advisory: the fail-closed warning and the open-threads blocker. Both
// hosts count first, so this tail runs once for either provider.
func applyThreadCounts(st *state, open, malformed int) {
	if malformed > 0 {
		st.warn(strconv.Itoa(malformed) + " live thread node(s) carry no readable resolution state " +
			"— counted as unresolved (fail-closed)")
	}
	st.threadsOpen = open
	if st.threadsOpen > 0 {
		st.blockers = append(st.blockers, Blocker{
			Kind:   "threads-open",
			Detail: strconv.Itoa(st.threadsOpen) + " unresolved thread(s)",
			Action: "address or resolve open threads, then re-run ready",
		})
	}
}

// loadPolicyCompliance evaluates captured requirements for the saved head:
// blockers fail readiness, pending or unverified work stays unknown, and an
// absent typed policy invents no blocker for legacy reads. It never appends
// an outcome or executes a command.
func loadPolicyCompliance(st *state, dir string) {
	rs, ref := config.VerifyRunState(dir)
	if ref != nil {
		st.policy = &config.ReadCompliance{HasPolicy: true, Unverified: true}
		st.warn("compliance unknown: " + ref.Message)
		return
	}
	if rs == nil || rs.Resolved == nil {
		return
	}
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		st.policy = &config.ReadCompliance{HasPolicy: true, Unverified: true}
		st.warn("compliance unknown: findings store is unreadable")
		return
	}
	comp := config.EvaluateReadCompliance(rs, readcommon.PrField(dir, "head_sha"), policyFindings(entries))
	if comp.HasPolicy {
		st.policy = &comp
		st.requiredGaps = requiredProfileGaps(st, dir)
	}
}

// requiredProfileGaps reads captured profile work readiness must reflect: a
// deep group without a current validation receipt, and a capped group without
// usable lens attestation. Reads never execute and never append outcomes.
func requiredProfileGaps(st *state, dir string) []string {
	if st.policy == nil || !st.policy.HasPolicy {
		return nil
	}
	rs, ref := config.VerifyRunState(dir)
	if ref != nil || rs == nil || rs.Resolved == nil {
		return nil
	}
	head := readcommon.PrField(dir, "head_sha")
	var gaps []string
	for _, gap := range verifycore.DeepGaps(rs, dir, head, st.req.RepoRootPath()) {
		gaps = append(gaps, "required work: "+gap)
	}
	usage := config.ReadLensUsage(rs, dir)
	for _, violation := range usage.Violations() {
		gaps = append(gaps, "required lens evidence: "+violation.Detail)
	}
	return gaps
}

// policyFindings adapts stored entries to the compliance view.
func policyFindings(entries []any) []config.FindingView {
	out := make([]config.FindingView, 0, len(entries))
	for _, entry := range entries {
		out = append(out, config.FindingView{
			Path:     jq.StrOf(readcommon.FieldOf(entry, "path")),
			Category: jq.StrOf(readcommon.FieldOf(entry, "category")),
			Severity: jq.StrOf(readcommon.FieldOf(entry, "severity")),
			Status:   jq.StrOf(readcommon.FieldOf(entry, "status")),
		})
	}
	return out
}

func computeReadiness(st *state) {
	appendComplianceBlocker(st)
	appendDraftBlocker(st)
	appendConflictBlocker(st)
	appendDescriptionBlocker(st)
	st.ready = true
	st.rec = "review"
	if st.prState != stateOpen {
		st.ready = false
		st.rec = recWait
		return
	}
	if len(st.blockers) > 0 {
		st.ready = false
		if st.draft {
			st.rec = recWait
		} else {
			st.rec = "nudge-author"
		}
	}
}

// appendComplianceBlocker makes the captured policy part of readiness: a
// blocking finding, pending required check, or unverified capture is a
// blocker, never a pass.
func appendComplianceBlocker(st *state) {
	comp := st.policy
	if comp == nil || !comp.HasPolicy {
		return
	}
	switch {
	case len(comp.Blockers) > 0:
		first := comp.Blockers[0]
		st.blockers = append(st.blockers, Blocker{
			Kind:   "compliance",
			Detail: strconv.Itoa(len(comp.Blockers)) + " blocking finding(s), first clause " + first.Clause,
			Action: "address or triage the blocking findings, then re-run ready",
		})
	case len(comp.Conflicts) > 0:
		first := comp.Conflicts[0]
		st.blockers = append(st.blockers, Blocker{
			Kind:   "compliance-conflict",
			Detail: "required review withholds " + first.Path + " (rule " + first.Rule + ")",
			Action: "narrow or endorse the exclusion explicitly, or review the material without disclosure",
		})
	case comp.Unverified:
		st.blockers = append(st.blockers, Blocker{
			Kind:   "compliance-unknown",
			Detail: "captured provenance unavailable",
			Action: "re-fetch the bundle so captured requirements verify",
		})
	case len(st.requiredGaps) > 0:
		st.blockers = append(st.blockers, Blocker{
			Kind:   "profile-work",
			Detail: st.requiredGaps[0],
			Action: "complete the required profile work for this capture, then re-run ready",
		})
	case len(comp.Pending) > 0:
		first := comp.Pending[0]
		st.blockers = append(st.blockers, Blocker{
			Kind:   "compliance-pending",
			Detail: strconv.Itoa(len(comp.Pending)) + " required check(s) pending (first: " + first.Check + ")",
			Action: "complete the required checks for the saved head, then re-run ready",
		})
	}
}

func appendDraftBlocker(st *state) {
	if st.draft {
		st.blockers = append(st.blockers, Blocker{
			Kind:   "draft",
			Detail: "PR is still a draft",
			Action: "mark ready for review when done, then re-run ready",
		})
	}
}

func appendConflictBlocker(st *state) {
	if st.mergeable == "CONFLICTING" || st.mergeState == "DIRTY" {
		st.blockers = append(st.blockers, Blocker{
			Kind:   "conflict",
			Detail: "mergeable=" + st.mergeable + " mergeStateStatus=" + st.mergeState,
			Action: "rebase onto base and resolve conflicts, then re-run ready",
		})
	}
}

func appendDescriptionBlocker(st *state) {
	if st.bodyLen < descriptionThin {
		st.blockers = append(st.blockers, Blocker{
			Kind:   "description-thin",
			Detail: "description ~" + strconv.Itoa(st.bodyLen) + " chars (<50)",
			Action: "expand the description (what/why/testing), then re-run ready",
		})
	}
}

const descriptionThin = 50

func (st *state) emitHuman() {
	if st.ready {
		st.human("ready: yes — " + st.repo + hashSep + st.pr + " looks reviewable " +
			"(CI " + strconv.Itoa(st.ciTotal) + " checks, 0 failing; " +
			strconv.Itoa(st.threadsOpen) + " open threads; mergeable " + st.mergeable +
			"; body " + strconv.Itoa(st.bodyLen) + " chars)")
		st.human("recommendation: review — fetch + review")
		return
	}
	if st.prState != stateOpen {
		st.human("ready: no — " + st.repo + hashSep + st.pr + " is " + st.prState + ", nothing to review")
	} else {
		st.human("ready: no — " + st.repo + hashSep + st.pr + " has " +
			strconv.Itoa(len(st.blockers)) + " blocker(s):")
		for _, b := range st.blockers {
			st.human("  - [" + b.Kind + "] " + b.Detail + " — action: " + b.Action)
		}
	}
	switch st.rec {
	case recWait:
		st.human("recommendation: wait — hold the review, re-run ready later")
	default:
		st.human("recommendation: nudge-author — nudge the author instead of reviewing")
	}
}

func sortedKeys(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}
