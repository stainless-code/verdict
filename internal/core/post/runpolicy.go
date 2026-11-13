package postcore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Codes the typed run policy adds: compliance and bind refusals no flag
// can bypass, because they enforce trusted repository requirements
// rather than advisory guards.
const (
	// CodeRequirementBlocked refuses a binding approval whose stored findings
	// violate an applicable block clause.
	CodeRequirementBlocked = "requirement-blocked"
	// CodeBindRequired refuses a binding approval the effective
	// require_bind demands without --bind.
	CodeBindRequired = "bind-required"
	// CodeRequirementUnmet refuses a binding approval whose selected
	// typed check has no passing evidence for the reviewed head.
	CodeRequirementUnmet = "requirement-unmet"
)

// runPolicy is the typed run bundle loaded for one post: the fetch-time
// resolution, the root requirements, and the appended check outcomes.
// Nil when the bundle predates typed policy (legacy runs degrade to no
// repository requirements instead of refusing, with the gap warned).
type runPolicy struct {
	state *config.RunState
}

// loadRunPolicy verifies the persisted run bundle through the typed
// integrity check: a deleted or tampered bundle refuses controls-stale
// instead of silently degrading to no policy, and a bundle that
// predates typed evidence warns and proceeds without requirements. A
// pre-integrity bundle still enforces its stored policy but reports
// unavailable provenance instead of verified evidence.
func (st *state) loadRunPolicy() *core.RefusalError {
	rs, ref := config.VerifyRunState(st.dir)
	if ref != nil {
		return ref
	}
	if rs == nil {
		st.warn("run bundle has no typed evidence (pre-typed bundle?) — proceeding without repository requirements")
		return nil
	}
	if !rs.Verified() {
		st.warn("typed evidence provenance unavailable (pre-integrity bundle) — stored policy enforced without verified evidence")
	}
	st.policy = &runPolicy{state: rs}
	return nil
}

// complianceGate records compliance over the full findings for every post.
// Block clauses refuse only positive binding approvals; comments and negative
// verdicts preserve the failed observations without requiring a bypass.
func (st *state) complianceGate() *core.RefusalError {
	if st.policy == nil || st.policy.state.Resolved == nil {
		return nil
	}
	rs := st.policy.state
	blocked := config.CheckCompliance(rs.Resolved, rs.Root.Clauses, policyFindings(st.merged.Entries))
	st.recordCompliance(rs, blocked)
	if len(blocked) == 0 || st.flags.Approve != approveTrue || !st.flags.Bind || st.flags.RequestChanges {
		return nil
	}
	first := blocked[0]
	return &core.RefusalError{Code: CodeRequirementBlocked, Message: fmt.Sprintf(
		msgRequirementBlocked, findingLabel(first.Finding), first.Clause,
		strings.Join(first.Sources, ", "), blockingGroup(&first))}
}

// blockingGroup describes the group a blocker belongs to: the group the
// finding's file resolved into, or the root fallback for a path outside
// every group.
func blockingGroup(blocker *config.BlockingFinding) string {
	if blocker.GroupKnown {
		return groupDesc(blocker.GroupLabel, blocker.Group)
	}
	return "no plan group (root clauses)"
}

// policyFindings adapts the stored merge entries to the compliance
// view: only entries carrying the finding shape participate, and the
// gate reads status exactly as stored.
func policyFindings(entries []any) []config.FindingView {
	var out []config.FindingView
	for _, entry := range entries {
		m, ok := entry.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		path, _ := m.Get("path")
		category, _ := m.Get("category")
		severity, _ := m.Get("severity")
		status, _ := m.Get("status")
		out = append(out, config.FindingView{
			Path:     jq.StrOf(path),
			Category: jq.StrOf(category),
			Severity: jq.StrOf(severity),
			Status:   jq.StrOf(status),
		})
	}
	return out
}

// findingLabel names a blocking finding for the refusal.
func findingLabel(finding config.FindingView) string {
	if finding.Path != "" {
		return finding.Path + " [" + finding.Category + "/" + finding.Severity + "]"
	}
	return "[" + finding.Category + "/" + finding.Severity + "]"
}

// checkEvidence is one verdict a post actually gathered for one selected
// check, with the detail an operator reads.
type checkEvidence struct {
	check  string
	detail string
	passed bool
}

// groupDesc names a scope by its explanation label and its identity, so a
// diagnostic never confuses two groups that share a label.
func groupDesc(label, id string) string {
	return fmt.Sprintf("group %q (id %s)", label, id)
}

// recordCompliance appends the no-open-above-block verdict for every scope
// carrying the check: a scope with a blocker records the failure, and a
// blocker outside every group fails every scope, because a root clause
// applies wherever no group's files do.
func (st *state) recordCompliance(rs *config.RunState, blocked []config.BlockingFinding) {
	failed, rootFailure := blockedGroups(blocked)
	conflicted, conflictRoot := conflictGroups(config.PrivacyConflicts(rs))
	for _, scope := range config.CheckScopes(rs.Resolved.Plan, &rs.Root) {
		if !slices.Contains(scope.Checks, config.CheckNoOpenAboveBlock) {
			continue
		}
		st.recordCheck(rs, scope, complianceFact(scope.ID, failed, rootFailure, conflicted, conflictRoot))
	}
}

// complianceFact states the ephemeral no-open verdict for one scope: a block
// violation or privacy conflict fails it, and the detail names the cause.
func complianceFact(id string, failed map[string]bool, rootFailure bool,
	conflicted map[string]bool, conflictRoot bool,
) checkEvidence {
	fact := checkEvidence{
		check:  config.CheckNoOpenAboveBlock,
		passed: !failed[id] && !rootFailure && !conflicted[id] && !conflictRoot,
	}
	if fact.passed {
		return fact
	}
	fact.detail = "block clause violation in stored findings"
	if conflicted[id] || conflictRoot {
		fact.detail = "required review withholds an excluded path (privacy conflict)"
	}
	return fact
}

// conflictGroups collects the scope ids a privacy conflict makes unreviewable,
// and whether any conflict sits outside every group (a root-clause conflict).
func conflictGroups(conflicts []config.PrivacyConflict) (map[string]bool, bool) {
	failed := map[string]bool{}
	root := false
	for i := range conflicts {
		if conflicts[i].Group == "" {
			root = true
			continue
		}
		failed[conflicts[i].Group] = true
	}
	return failed, root
}

// privacyConflictGate refuses a binding positive approval whose required
// review is withheld by the captured personal exclusions. The conflict names
// the path, group, and exclusion rule only; --force, consent, and any other
// bypass are never consulted, while comments and negative recording stay
// available to report the conflict.
func (st *state) privacyConflictGate() *core.RefusalError {
	if st.policy == nil || st.policy.state.Resolved == nil {
		return nil
	}
	conflicts := config.PrivacyConflicts(st.policy.state)
	if len(conflicts) == 0 {
		return nil
	}
	first := conflicts[0]
	where := "the whole review"
	if first.Group != "" {
		where = groupDesc(first.GroupLabel, first.Group)
	}
	return &core.RefusalError{Code: CodeRequirementUnmet, Message: fmt.Sprintf(
		"required review for %s in %s withholds that path by exclusion rule %q — compliance is unmet and conflicted; "+
			"adjust the review scope or requirements, then re-fetch (comments and non-approving runs can record the conflict), "+
			"and never disclose the withheld bytes", first.Path, where, first.Rule)}
}

// blockedGroups collects the group ids a blocker set fails, and whether any
// blocker sat outside every group (a root-clause failure).
func blockedGroups(blocked []config.BlockingFinding) (map[string]bool, bool) {
	failed := map[string]bool{}
	rootFailure := false
	for i := range blocked {
		if !blocked[i].GroupKnown {
			rootFailure = true
			continue
		}
		failed[blocked[i].Group] = true
	}
	return failed, rootFailure
}

// recordCheck appends one observation for one scope and keeps the in-memory
// ledger in step with the persisted one. A failed append leaves the check
// pending, so a binding approval can never claim evidence the bundle did not
// record; a dry run keeps its verdict in memory only.
func (st *state) recordCheck(rs *config.RunState, scope config.CheckScope, fact checkEvidence) {
	if scope.ID == "" {
		return
	}
	obs := config.CheckObservation{
		Check: fact.check, Group: scope.ID, Head: st.commitID,
		At: st.now, Detail: fact.detail, Passed: fact.passed,
	}
	if st.flags.DryRun {
		rs.Outcomes = append(rs.Outcomes, obs)
		return
	}
	if !config.OutcomeChanged(rs, &obs) {
		return
	}
	if ok, serr := config.AppendOutcome(st.dir, &obs); !ok {
		st.warnOutcome(serr)
		return
	}
	rs.Outcomes = append(rs.Outcomes, obs)
}

// recordChecks appends one observation per scope selecting fact's check.
func (st *state) recordChecks(rs *config.RunState, fact checkEvidence) {
	for _, scope := range config.CheckScopes(rs.Resolved.Plan, &rs.Root) {
		if slices.Contains(scope.Checks, fact.check) {
			st.recordCheck(rs, scope, fact)
		}
	}
}

// requiresCheck reports whether any scope of the run selects check.
func (st *state) requiresCheck(check string) bool {
	if st.policy == nil || st.policy.state.Resolved == nil {
		return false
	}
	rs := st.policy.state
	for _, scope := range config.CheckScopes(rs.Resolved.Plan, &rs.Root) {
		if slices.Contains(scope.Checks, check) {
			return true
		}
	}
	return false
}

// unmetScopes lists the scopes selecting check whose recorded verdict for
// head is not a pass: no observation, a failed one, or one recorded for a
// different group or head all read unmet.
func unmetScopes(rs *config.RunState, check, head string) []config.ScopeStatus {
	scopes := config.CheckScopes(rs.Resolved.Plan, &rs.Root)
	var out []config.ScopeStatus
	for i, status := range config.ObserveScopes(scopes, rs.Outcomes, head) {
		if slices.Contains(scopes[i].Checks, check) && !status.Checks[check] {
			out = append(out, status)
		}
	}
	return out
}

// latestDetail reads the newest recorded detail for one scope, check, and
// head, so a refusal explains the verdict it refused on.
func latestDetail(rs *config.RunState, group, check, head string) string {
	for _, obs := range slices.Backward(rs.Outcomes) {
		if obs.Check == check && obs.Group == group && obs.Head == head {
			return obs.Detail
		}
	}
	return "no evidence recorded"
}

// unmetRefusal refuses a binding approval whose selected check has no passing
// evidence, naming the first unmet scope, its recorded detail, and the step
// that would produce the evidence. Generic --force is never consulted.
func (st *state) unmetRefusal(check, hint string) *core.RefusalError {
	rs := st.policy.state
	unmet := unmetScopes(rs, check, st.commitID)
	if len(unmet) == 0 {
		return nil
	}
	first := unmet[0]
	return &core.RefusalError{Code: CodeRequirementUnmet, Message: fmt.Sprintf(
		msgRequirementUnmet, check, groupDesc(first.Label, first.ID),
		readcommon.ShortSHA(st.commitID), latestDetail(rs, first.ID, check, st.commitID), hint)}
}

// headCurrent is the drift read's verdict: the live host head equals the
// fetched head. An unreadable lookup and a headless bundle are not passes.
func (st *state) headCurrent() bool {
	return st.fetchedSHA != "" && st.liveSHA != "" && st.liveSHA == st.fetchedSHA
}

// headDetail explains the drift verdict for the outcome ledger.
func (st *state) headDetail() string {
	switch {
	case st.fetchedSHA == "":
		return "the bundle carries no fetched head"
	case st.liveSHA == "":
		return "live head lookup failed"
	case st.liveSHA == st.fetchedSHA:
		return "live head " + readcommon.ShortSHA(st.liveSHA) + " matches the fetched head"
	default:
		return "live head " + readcommon.ShortSHA(st.liveSHA) + " moved past the fetched head " + readcommon.ShortSHA(st.fetchedSHA)
	}
}

// recordHeadFact appends this run's drift verdict for every scope selecting
// head-current: the fact is the run's own live comparison, so a comment run
// preserves it and a binding approval is decided on it.
func (st *state) recordHeadFact() {
	if st.policy == nil || st.policy.state.Resolved == nil {
		return
	}
	st.recordChecks(st.policy.state, checkEvidence{
		check: config.CheckHeadCurrent, passed: st.headCurrent(), detail: st.headDetail(),
	})
}

// headGate refuses a binding approval whose scopes select head-current unless
// the fetched head is the live host head. A completed-post receipt is never
// this evidence, and --force never applies.
func (st *state) headGate() *core.RefusalError {
	if !st.requiresCheck(config.CheckHeadCurrent) {
		return nil
	}
	return st.unmetRefusal(config.CheckHeadCurrent, msgHintHead)
}

// warnOutcome records an outcome-append failure as a warning: the publish
// it observes already decided, so the append never refuses. Absent run
// state (nil error) warns once per run instead.
func (st *state) warnOutcome(serr error) {
	if serr != nil {
		st.warn(fmt.Sprintf("check outcome not recorded: %v", serr))
		return
	}
	st.warn("run bundle absent — check outcome not recorded")
}

// deepValidationGate refuses a binding positive approval while any deep
// plan group lacks a tool-produced validation receipt for this exact run,
// group, and head. Deep means "standard review plus a recorded validation
// pass", so a plan label, a cost tier, or another group's receipt never
// satisfies it; --force and consent are never consulted.
func (st *state) deepValidationGate() *core.RefusalError {
	if st.policy == nil || st.policy.state.Resolved == nil {
		return nil
	}
	rs := st.policy.state
	root, ref := st.certifiedRoot()
	if ref != nil {
		return ref
	}
	return st.deepGroupsGap(rs, root)
}

// certifiedRoot resolves the reviewed root a deep receipt must bind, refusing
// when the root itself cannot be certified.
func (st *state) certifiedRoot() (string, *core.RefusalError) {
	path := ""
	if st.req != nil {
		path = st.req.RepoRootPath()
	}
	root, ref := verifycore.CanonicalRoot(path)
	if ref != nil {
		return "", &core.RefusalError{
			Code:    CodeRequirementUnmet,
			Message: "deep validation cannot certify the reviewed root: " + ref.Message,
		}
	}
	return root, nil
}

// deepGroupsGap refuses the first deep group whose receipt does not certify
// this run, head, and root.
func (st *state) deepGroupsGap(rs *config.RunState, root string) *core.RefusalError {
	for i := range rs.Resolved.Plan.Groups {
		group := &rs.Resolved.Plan.Groups[i]
		if group.Depth != profileDepthDeep {
			continue
		}
		if ref := st.deepGroupGap(rs, root, group); ref != nil {
			return ref
		}
	}
	return nil
}

// deepGroupGap refuses one deep group whose receipt is missing, unreadable,
// or does not bind this run, head, root, and inputs.
func (st *state) deepGroupGap(rs *config.RunState, root string, group *config.PlanGroup) *core.RefusalError {
	receipt, found, ref := verifycore.ReadValidationReceipt(st.dir, group.ID)
	name := groupDesc(group.Label, group.ID)
	var why string
	switch {
	case ref != nil:
		why = ref.Message
	case !found:
		why = "no validation receipt exists — run verify --group " + group.ID + " --command <cmd> first"
	case !verifycore.ReceiptCurrent(&receipt, rs.Name, rs.InputsSHA256, group.ID, st.fetchedSHA) || receipt.RepoRoot != root:
		why = "its validation receipt is stale (run, group, head, or root moved) — " +
			"re-run verify --group " + group.ID + " at the reviewed head"
	default:
		return nil
	}
	return &core.RefusalError{Code: CodeRequirementUnmet, Message: fmt.Sprintf(msgDeepValidation, name, why)}
}

// lensApprovalGate refuses a binding positive approval whose capped plan
// groups have no usable lens attestation or recorded more unique
// dispatches than the cap. Usage is agent self-report: this gate proves
// only that the report covers the capped groups, never that a lens ran.
// Comments and non-approving runs stay available for reporting failures.
func (st *state) lensApprovalGate() *core.RefusalError {
	if st.policy == nil || st.policy.state.Resolved == nil {
		return nil
	}
	usage := config.ReadLensUsage(st.policy.state, st.dir)
	violations := usage.Violations()
	if len(violations) == 0 {
		return nil
	}
	first := violations[0]
	return &core.RefusalError{Code: CodeRequirementUnmet, Message: fmt.Sprintf(
		msgLensGate, first.Detail, msgLensFix)}
}

// bindRequiredGate enforces the effective require_bind: a binding
// approval (approve or request-changes) without --bind refuses when any
// applicable requirement set demands binding. Legacy runs without typed
// policy keep the downgrade warning. --force cannot bypass: binding is
// a trusted requirement, and the refusal never records forced[].
func (st *state) bindRequiredGate() *core.RefusalError {
	if st.policy == nil {
		return nil
	}
	if !st.policy.state.Root.RequireBind {
		return nil
	}
	wantBind := st.flags.Approve == approveTrue || st.flags.RequestChanges
	if wantBind && !st.flags.Bind {
		return &core.RefusalError{Code: CodeBindRequired, Message: msgRequireBind}
	}
	return nil
}

// floorPayload returns the findings the published payload carries under
// the run policy floor: the effective post_min_severity withholds
// below-floor findings from the payload while every gate keeps reading
// the full stored set. Unset floor publishes everything; unknown
// severities are never withheld, so the filter cannot silently drop
// what the vocabulary does not know.
func (st *state) floorPayload() ([]any, string, []core.FloorScope) {
	rs := postRunState(st)
	floor := ""
	if rs != nil && rs.Resolved != nil {
		if f := rs.Resolved.Defaults.Values.PostMinSeverity; f != nil {
			floor = *f
		}
	}
	kept := make([]any, 0, len(st.merged.Entries))
	withheld := make([]core.FloorScope, 0, len(st.merged.Entries))
	for _, entry := range readcommon.VisibleFindings(st.merged.Entries) {
		if !config.BelowPostFloor(rs, entry) {
			kept = append(kept, entry)
			continue
		}
		withheld = append(withheld, scopedFloorOf(rs, entry, floor))
	}
	return kept, floor, withheld
}

// scopedFloors keeps the distinct path floors that withheld findings, sorted
// by path, so the receipt lists each one once.
func scopedFloors(withheld []core.FloorScope) []core.FloorScope {
	seen := make(map[core.FloorScope]bool, len(withheld))
	out := make([]core.FloorScope, 0, len(withheld))
	for _, sc := range withheld {
		if sc.Path == "" || seen[sc] {
			continue
		}
		seen[sc] = true
		out = append(out, sc)
	}
	slices.SortFunc(out, func(a, b core.FloorScope) int {
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

// scopedFloorOf records the floor that withheld one entry when it is a
// path-scoped value rather than the global baseline the receipt already
// states. A finding with no floor of its own carries none.
func scopedFloorOf(rs *config.RunState, entry any, baseline string) core.FloorScope {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return core.FloorScope{}
	}
	path, _ := m.Get("path")
	name := jq.StrOf(path)
	if name == "" {
		return core.FloorScope{}
	}
	floor := config.PostFloorFor(rs, name)
	if floor == "" || floor == baseline {
		return core.FloorScope{}
	}
	return core.FloorScope{Path: name, Floor: floor}
}

// postRunState reads the typed run state the post policy capture carries.
func postRunState(st *state) *config.RunState {
	if st.policy == nil {
		return nil
	}
	return st.policy.state
}

// observeLiveThreads appends the no-live-threads verdict: zero unresolved
// live threads passes; any unresolved thread, an unreadable read included,
// fails. The stored-open comparison is the shipped legacy guard and never
// this typed check.
func (st *state) observeLiveThreads(count int, readable bool, reason string) {
	fact := checkEvidence{check: config.CheckNoLiveThreads, passed: readable && count == 0}
	switch {
	case !readable:
		fact.detail = "live thread read failed: " + reason
	case count == 0:
		fact.detail = "no unresolved live threads"
	default:
		fact.detail = fmt.Sprintf("%d unresolved live thread(s)", count)
	}
	st.recordChecks(st.policy.state, fact)
}
