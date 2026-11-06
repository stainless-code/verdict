package postcore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Codes the typed run policy adds: compliance and bind refusals no flag
// can bypass, because they enforce trusted repository requirements
// rather than advisory guards.
const (
	// CodeRequirementBlocked refuses a publish whose stored findings
	// violate an applicable block clause.
	CodeRequirementBlocked = "requirement-blocked"
	// CodeBindRequired refuses a binding approval the effective
	// require_bind demands without --bind.
	CodeBindRequired = "bind-required"
)

// runPolicy is the typed run bundle loaded for one post: the fetch-time
// resolution, the root requirements, and the appended check outcomes.
// Nil when the bundle predates typed policy (legacy runs degrade to no
// repository requirements instead of refusing).
type runPolicy struct {
	state *config.RunState
}

// loadRunPolicy reads the persisted run bundle beside the legacy diff
// bundle: absent reads nil policy, present parses strictly. The state
// root anchors to the working directory like the diff bundle itself,
// never to the tool checkout root.
func (st *state) loadRunPolicy() *core.RefusalError {
	rs, found, serr := config.ReadRunState("", st.req.Pr)
	if serr != nil {
		if ref := serr.Refusal(); ref != nil {
			return ref
		}
		return &core.RefusalError{Code: codePostFailed, Message: fmt.Sprintf("post failed: could not read run state: %v", serr)}
	}
	if !found {
		return nil
	}
	st.policy = &runPolicy{state: rs}
	return nil
}

// complianceGate enforces the stored block clauses against the full
// stored findings: an open finding on a clause path, in a clause
// category, at or above the clause threshold rank refuses. Deferred,
// addressed, and terminal findings never block; unknown severities
// never block. --force cannot bypass a trusted requirement: the gate
// never consults the force flag, and the refusal never records a
// forced[] entry.
func (st *state) complianceGate() *core.RefusalError {
	if st.policy == nil || st.policy.state.Resolved == nil {
		return nil
	}
	rs := st.policy.state
	blocked := config.CheckCompliance(rs.Resolved, rs.Root.Clauses, policyFindings(st.merged.Entries))
	st.observeCompliance(rs, blocked)
	if len(blocked) == 0 {
		return nil
	}
	first := blocked[0]
	return &core.RefusalError{Code: CodeRequirementBlocked, Message: fmt.Sprintf(
		"finding %s (%s/%s) violates block clause %q from %s — address it, narrow the clause, or re-fetch without the policy",
		findingLabel(first.Finding), first.Clause, strings.Join(first.Sources, ", "), first.Group, findingDetail(first.Finding))}
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

// findingDetail renders the group the finding resolved into.
func findingDetail(finding config.FindingView) string {
	if finding.Path != "" {
		return "group of " + finding.Path
	}
	return "the run"
}

// observeCompliance appends one no-open-above-block observation per
// planned group carrying the check: groups with a blocker record the
// failure, groups without record the pass. Observations never rewrite
// captured inputs; an append failure warns instead of refusing the
// publish it observes.
func (st *state) observeCompliance(rs *config.RunState, blocked []config.BlockingFinding) {
	failed := map[string]bool{}
	for _, blocker := range blocked {
		if blocker.Group != "" {
			failed[blocker.Group] = true
		}
	}
	for i := range rs.Resolved.Plan.Groups {
		st.observeGroup(rs, &rs.Resolved.Plan.Groups[i], failed)
	}
}

// observeGroup records the no-open-above-block verdict for one planned
// group carrying the check, skipping groups without it.
func (st *state) observeGroup(rs *config.RunState, group *config.PlanGroup, failed map[string]bool) {
	if group.Profile == "" || !slices.Contains(group.Checks, config.CheckNoOpenAboveBlock) {
		return
	}
	obs := config.CheckObservation{
		Check:  config.CheckNoOpenAboveBlock,
		Group:  group.Profile,
		Passed: !failed[group.Profile],
		At:     st.now,
	}
	if failed[group.Profile] {
		obs.Detail = "block clause violation in stored findings"
	}
	if !config.OutcomeChanged(rs, &obs) {
		return
	}
	if ok, serr := config.AppendOutcome("", st.req.Pr, &obs); !ok {
		st.warnOutcome(serr)
	}
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
		return &core.RefusalError{Code: CodeBindRequired, Message: "require_bind is set by the run policy — pass --bind to bind this approval " +
			"(binding downgrades never satisfy a required bind, and --force cannot bypass it)"}
	}
	return nil
}

// floorPayload returns the findings the published payload carries under
// the run policy floor: the effective post_min_severity withholds
// below-floor findings from the payload while every gate keeps reading
// the full stored set. Unset floor publishes everything; unknown
// severities are never withheld, so the filter cannot silently drop
// what the vocabulary does not know.
func (st *state) floorPayload() ([]any, string, int) {
	floor := ""
	if st.policy != nil && st.policy.state.Resolved != nil {
		if f := st.policy.state.Resolved.Defaults.Values.PostMinSeverity; f != nil {
			floor = *f
		}
	}
	if floor == "" {
		return st.merged.Entries, "", 0
	}
	kept := make([]any, 0, len(st.merged.Entries))
	excluded := 0
	for _, entry := range st.merged.Entries {
		if belowFloor(entry, floor) {
			excluded++
			continue
		}
		kept = append(kept, entry)
	}
	return kept, floor, excluded
}

// belowFloor reports whether one stored entry sits below the payload
// floor: a legible severity under the floor rank withholds it, while a
// missing or unknown severity keeps it visible.
func belowFloor(entry any, floor string) bool {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	severity, _ := m.Get("severity")
	rank := config.SeverityRank(jq.StrOf(severity))
	if rank == 0 {
		return false
	}
	return rank < config.SeverityRank(floor)
}

// observeLiveThreads appends the no-live-threads observation after the
// recheck gate runs: a refused recheck records the failure, a passed
// one records the clean threads. Downgraded runs (unreachable recheck)
// record nothing — no evidence exists either way.
func (st *state) observeLiveThreads(liveOpen, storedOpen int, downgraded bool) {
	if st.policy == nil || downgraded {
		return
	}
	obs := config.CheckObservation{
		Check:  config.CheckNoLiveThreads,
		Passed: liveOpen <= storedOpen,
		At:     st.now,
		Detail: fmt.Sprintf("live open %d vs stored open %d", liveOpen, storedOpen),
	}
	if st.policy != nil && !config.OutcomeChanged(st.policy.state, &obs) {
		return
	}
	if ok, serr := config.AppendOutcome("", st.req.Pr, &obs); !ok {
		st.warnOutcome(serr)
	}
}
