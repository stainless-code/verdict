package config

import (
	"slices"

	"github.com/stainless-code/verdict/internal/core"
)

// Check identifiers are the requirements-contract strings owned by
// resolve.go (`CheckNoOpenAboveBlock`, `CheckHeadCurrent`,
// `CheckNoLiveThreads`). A group lists the checks it must evaluate; the
// plan carries them pending until a verb appends an observation.

// SeverityRank orders severities for threshold comparison: critical 3,
// important 2, informational 1, anything else 0 (never blocks, never
// withholds).
func SeverityRank(severity string) int {
	return severityRank(severity)
}

// severity ranks order findings against clause thresholds.
const (
	rankInformational = 1
	rankImportant     = 2
	rankCritical      = 3
)

// severityRank orders severities for threshold comparison: a finding at
// or above the clause threshold blocks.
func severityRank(severity string) int {
	switch severity {
	case "critical":
		return rankCritical
	case "important":
		return rankImportant
	case "informational":
		return rankInformational
	default:
		return 0
	}
}

// AtOrAboveThreshold reports whether a finding severity meets a clause
// threshold rank. Unknown severities never block: the record checker
// owns the severity vocabulary, and compliance must not invent ranks.
func AtOrAboveThreshold(severity, threshold string) bool {
	rank := severityRank(severity)
	if rank == 0 {
		return false
	}
	return rank >= severityRank(threshold)
}

// FindingView is the minimal finding shape compliance evaluates: the
// repository-relative path, the category, the severity, and the status.
// Verbs adapt their stored entries to this view so compliance never
// imports a verb package.
type FindingView struct {
	Path     string
	Category string
	Severity string
	Status   string
}

// Open reports whether the finding still blocks: only open findings
// count; deferred findings already carry an explicit deferral decision.
func (f FindingView) Open() bool { return f.Status == "open" }

// BlockingFinding pairs one open finding with the clause that blocks on
// it, naming the group the finding's file resolved into. Group is the
// stable group identity; GroupLabel is the explanation label beside it.
type BlockingFinding struct { //nolint:govet // field grouping documents the blocker
	Finding    FindingView
	Clause     string
	Group      string
	GroupLabel string
	GroupKnown bool
	Sources    []string
}

// CheckCompliance evaluates every finding against the clauses applicable
// to its path: an open finding on a clause path, in a clause category
// (or any category when the clause names none), at or above the clause
// threshold rank blocks. Findings whose file matches no per-file
// resolution fall back to the root clauses. The returned blockers name
// the clause, the group, and every source behind the conjunction.
func CheckCompliance(res *Resolved, root []ClauseProvenance, findings []FindingView) []BlockingFinding {
	if res == nil {
		return nil
	}
	var blocked []BlockingFinding
	for _, finding := range findings {
		if !finding.Open() {
			continue
		}
		blocked = append(blocked, findingBlockers(res, root, finding)...)
	}
	return blocked
}

// findingBlockers names every applicable clause one open finding violates.
func findingBlockers(res *Resolved, root []ClauseProvenance, finding FindingView) []BlockingFinding {
	clauses, group := applicableClauses(res, root, finding.Path)
	var blocked []BlockingFinding
	for i := range clauses {
		provenance := &clauses[i]
		if !clauseMatches(&provenance.Clause, finding) {
			continue
		}
		blocked = append(blocked, blockingFinding(finding, provenance, group))
	}
	return blocked
}

// blockingFinding builds one blocker, carrying the group identity when the
// finding's file resolved into a plan group.
func blockingFinding(finding FindingView, provenance *ClauseProvenance, group *PlanGroup) BlockingFinding {
	blocker := BlockingFinding{
		Finding: finding,
		Clause:  provenance.Clause.Name,
		Sources: append([]string{}, provenance.Sources...),
	}
	if group != nil {
		blocker.Group, blocker.GroupLabel, blocker.GroupKnown = group.ID, group.Label, true
	}
	return blocker
}

// applicableClauses returns the clauses for the file resolution matching
// path (old paths match too, so renames never evade) plus the plan group
// holding that file's NEW path, so a pre-rename finding still resolves to
// the group the renamed file lives in. Findings matching no resolution
// fall back to the root clauses with no group.
func applicableClauses(res *Resolved, root []ClauseProvenance, path string) ([]ClauseProvenance, *PlanGroup) {
	for i := range res.Files {
		file := &res.Files[i]
		if file.Path == path || (file.OldPath != "" && file.OldPath == path) {
			return file.Requirements.Clauses, groupForPath(res.Plan, file.Path)
		}
	}
	return root, nil
}

// groupForPath returns the plan group holding path, nil when the file
// resolved outside every group.
func groupForPath(plan Plan, path string) *PlanGroup {
	for i := range plan.Groups {
		if slices.Contains(plan.Groups[i].Files, path) {
			return &plan.Groups[i]
		}
	}
	return nil
}

// clauseMatches reports whether one clause blocks on one finding: the
// finding path must fall under a clause path (or the clause names no
// paths), the category must match (or the clause names none), and the
// severity must meet the threshold rank.
func clauseMatches(clause *core.BlockClause, finding FindingView) bool {
	if len(clause.Paths) > 0 && !pathCovered(clause.Paths, finding.Path) {
		return false
	}
	if len(clause.Categories) > 0 && !slices.Contains(clause.Categories, finding.Category) {
		return false
	}
	return AtOrAboveThreshold(finding.Severity, clause.Threshold)
}

// pathCovered reports whether any configured clause glob selects the
// finding path. Only the configured glob is a pattern: the finding path is
// data, so a path that happens to carry `*` bytes never matches a glob it
// would not otherwise match.
func pathCovered(globs []string, path string) bool {
	for _, glob := range globs {
		if Match(glob, path) {
			return true
		}
	}
	return false
}

// CheckObservation is one observed check execution appended by a verb: the
// check, the stable group identity it ran for, the fetched head it ran
// against, whether it passed, and the detail the verb saw. Observations
// never rewrite captured inputs, and only an observation naming the same
// group and head satisfies a requirement: the run bundle it lives in is
// its run identity.
type CheckObservation struct {
	Check   string   `json:"check"`
	Group   string   `json:"group"`
	Head    string   `json:"head"`
	At      string   `json:"at"`
	Detail  string   `json:"detail"`
	Sources []string `json:"sources"`
	Passed  bool     `json:"passed"`
}

// RootScopeID names the whole-run requirement scope: root require_checks no
// plan group selects, which an empty plan would otherwise leave vacuously
// met. Group ids are digests, so the literal never collides with one.
const RootScopeID = "root"

// CheckScope is one identity a selected check belongs to: a plan group, or
// the whole-run root scope.
type CheckScope struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Checks []string `json:"checks"`
}

// CheckScopes returns every scope a binding action must satisfy: each plan
// group with its selected checks, plus the whole-run root scope for root
// requirements no group selects, so an empty plan cannot leave a root check
// vacuously satisfied.
func CheckScopes(plan Plan, root *RequirementSet) []CheckScope {
	scopes := PlanScopes(plan)
	if root == nil {
		return scopes
	}
	uncovered := uncoveredChecks(scopes, root.Checks)
	if len(uncovered) > 0 {
		scopes = append(scopes, CheckScope{ID: RootScopeID, Label: "root requirements", Checks: uncovered})
	}
	return scopes
}

// uncoveredChecks returns the root checks no scope selects, in their
// declared order and without duplicates.
func uncoveredChecks(scopes []CheckScope, rootChecks []string) []string {
	var out []string
	for _, check := range rootChecks {
		if !scopeSelects(scopes, check) && !slices.Contains(out, check) {
			out = append(out, check)
		}
	}
	return out
}

// scopeSelects reports whether any scope selects check.
func scopeSelects(scopes []CheckScope, check string) bool {
	for i := range scopes {
		if slices.Contains(scopes[i].Checks, check) {
			return true
		}
	}
	return false
}

// PlanScopes returns the file-group scopes of one plan without the
// whole-run root scope.
func PlanScopes(plan Plan) []CheckScope {
	scopes := make([]CheckScope, 0, len(plan.Groups))
	for i := range plan.Groups {
		group := &plan.Groups[i]
		scopes = append(scopes, CheckScope{ID: group.ID, Label: group.Label, Checks: slices.Clone(group.Checks)})
	}
	return scopes
}

// ScopeStatus is the observed execution state of one check scope.
type ScopeStatus struct {
	Checks map[string]bool `json:"checks"`
	ID     string          `json:"id"`
	Label  string          `json:"label"`
}

// ObserveScopes folds observations into per-scope check states: every
// selected check starts pending (absent means unevaluated), and only an
// observation naming that exact scope id, that exact check, and the run's
// fetched head records a verdict. An empty or unknown group matches no
// scope, and a record from another group, run, or head does not match.
func ObserveScopes(scopes []CheckScope, observations []CheckObservation, head string) []ScopeStatus {
	out := make([]ScopeStatus, 0, len(scopes))
	for i := range scopes {
		out = append(out, scopeStatus(&scopes[i], observations, head))
	}
	return out
}

// scopeStatus folds one scope: a scope carrying no identity is unevaluable,
// so no observation, not even one recorded with an empty group, satisfies it.
func scopeStatus(scope *CheckScope, observations []CheckObservation, head string) ScopeStatus {
	status := ScopeStatus{ID: scope.ID, Label: scope.Label, Checks: map[string]bool{}}
	for _, check := range scope.Checks {
		status.Checks[check] = scope.ID != "" && observedPass(observations, scope.ID, check, head)
	}
	return status
}

// observedPass reports the newest verdict for one scope, check, and head.
func observedPass(observations []CheckObservation, group, check, head string) bool {
	passed := false
	for i := range observations {
		if observations[i].Group == group && observations[i].Check == check && observations[i].Head == head {
			passed = observations[i].Passed
		}
	}
	return passed
}

// ObservePlan folds observations for the plan's file groups alone.
func ObservePlan(plan Plan, observations []CheckObservation, head string) []ScopeStatus {
	return ObserveScopes(PlanScopes(plan), observations, head)
}

// OutcomeChanged reports whether an observation would move the ledger:
// false when the latest observation for the same check, group, and head
// already records the same verdict, so repeated identical runs append
// nothing. A different head is a different execution and always appends.
func OutcomeChanged(rs *RunState, obs *CheckObservation) bool {
	if rs == nil {
		return true
	}
	for _, prior := range slices.Backward(rs.Outcomes) {
		if prior.Check == obs.Check && prior.Group == obs.Group && prior.Head == obs.Head {
			return prior.Passed != obs.Passed
		}
	}
	return true
}

// PendingChecks lists the selected checks with no passing observation for
// head, each naming its scope in deterministic order.
func PendingChecks(scopes []CheckScope, observations []CheckObservation, head string) []CheckObservation {
	var out []CheckObservation
	for _, status := range ObserveScopes(scopes, observations, head) {
		out = append(out, pendingOf(status, head)...)
	}
	return out
}

// pendingOf lists one scope's unpassed checks in sorted order.
func pendingOf(status ScopeStatus, head string) []CheckObservation {
	checks := make([]string, 0, len(status.Checks))
	for check := range status.Checks {
		checks = append(checks, check)
	}
	slices.Sort(checks)
	var out []CheckObservation
	for _, check := range checks {
		if !status.Checks[check] {
			out = append(out, CheckObservation{Check: check, Group: status.ID, Head: head})
		}
	}
	return out
}
