package config

import (
	"slices"
	"strconv"

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

// severityRank orders severities for threshold comparison: a finding at
// or above the clause threshold blocks.
func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 3
	case "important":
		return 2
	case "informational":
		return 1
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
// it, naming the group the finding's file resolved into.
type BlockingFinding struct {
	Finding FindingView
	Clause  string
	Group   string
	Sources []string
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
		clauses, group := applicableClauses(res, root, finding.Path)
		for i := range clauses {
			provenance := &clauses[i]
			if !clauseMatches(&provenance.Clause, finding) {
				continue
			}
			blocked = append(blocked, BlockingFinding{
				Finding: finding,
				Clause:  provenance.Clause.Name,
				Group:   group,
				Sources: append([]string{}, provenance.Sources...),
			})
		}
	}
	return blocked
}

// applicableClauses returns the clauses for the file resolution matching
// path (old paths match too, so renames never evade), else the root
// clauses with no group.
func applicableClauses(res *Resolved, root []ClauseProvenance, path string) ([]ClauseProvenance, string) {
	for i := range res.Files {
		file := &res.Files[i]
		if file.Path == path || (file.OldPath != "" && file.OldPath == path) {
			return file.Requirements.Clauses, groupName(res.Plan, path)
		}
	}
	return root, ""
}

// groupName names the plan group holding path, empty when the file
// resolved outside every group.
func groupName(plan Plan, path string) string {
	for i, group := range plan.Groups {
		for _, member := range group.Files {
			if member == path {
				if group.Profile != "" {
					return group.Profile
				}
				return "group " + strconv.Itoa(i+1)
			}
		}
	}
	return ""
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

// pathCovered reports whether any clause glob selects the finding path.
// Both directions match (either glob may carry the trailing /**), and
// both the new and old rename paths were already considered when the
// clauses were collected.
func pathCovered(globs []string, path string) bool {
	for _, glob := range globs {
		if Match(glob, path) || Match(path, glob) {
			return true
		}
	}
	return false
}

// CheckObservation is one observed check execution appended by a verb:
// the check, the group it ran for, whether it passed, and the detail
// the verb saw. Observations never rewrite captured inputs.
type CheckObservation struct {
	Check   string   `json:"check"`
	Group   string   `json:"group"`
	At      string   `json:"at"`
	Detail  string   `json:"detail"`
	Sources []string `json:"sources"`
	Passed  bool     `json:"passed"`
}

// GroupStatus is the observed execution state of one plan group.
type GroupStatus struct {
	Checks map[string]bool `json:"checks"`
	Group  string          `json:"group"`
}

// ObservePlan folds observations into per-group check states: every
// planned check starts pending (absent), and each observation records
// the latest verdict for its check. Unknown checks never satisfy a
// planned one; the writer owns the check vocabulary.
func ObservePlan(plan Plan, observations []CheckObservation) []GroupStatus {
	out := make([]GroupStatus, 0, len(plan.Groups))
	for i, group := range plan.Groups {
		name := group.Profile
		if name == "" {
			name = "group " + strconv.Itoa(i+1)
		}
		status := GroupStatus{Group: name, Checks: map[string]bool{}}
		for _, want := range group.Checks {
			status.Checks[want] = false
			for _, obs := range observations {
				if obs.Check == want && (obs.Group == "" || obs.Group == name) {
					status.Checks[want] = obs.Passed
				}
			}
		}
		out = append(out, status)
	}
	return out
}

// OutcomeChanged reports whether an observation would move the ledger:
// false when the latest matching check-and-group outcome already
// records the same verdict, so repeated identical runs append nothing.
// Verbs consult it before appending to keep the outcome ledger a
// change log, not a repetition log.
func OutcomeChanged(rs *RunState, obs *CheckObservation) bool {
	if rs == nil {
		return true
	}
	for _, prior := range slices.Backward(rs.Outcomes) {
		if prior.Check == obs.Check && prior.Group == obs.Group {
			return prior.Passed != obs.Passed
		}
	}
	return true
}

// PendingChecks lists the planned checks with no passing observation:
// the work still due before the group is fully observed. Each entry
// names its group; a passing observation satisfies only its group.
func PendingChecks(plan Plan, observations []CheckObservation) []CheckObservation {
	status := ObservePlan(plan, observations)
	var out []CheckObservation
	for _, group := range status {
		for check, passed := range group.Checks {
			if !passed {
				out = append(out, CheckObservation{Check: check, Group: group.Group})
			}
		}
	}
	return out
}
