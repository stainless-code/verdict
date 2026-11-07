package config

// ReadCompliance is the read-only compliance evaluation of one captured run:
// the blocking findings, the required checks still pending for the saved head,
// and whether the capture carries verified provenance. A read never appends an
// outcome and never executes a validation command.
type ReadCompliance struct {
	Blockers  []BlockingFinding
	Conflicts []PrivacyConflict
	Pending   []CheckObservation
	// HasPolicy is true when the capture states a repository requirement;
	// an absent policy invents nothing for legacy bundles.
	HasPolicy bool
	// Unverified marks a capture whose provenance cannot be verified; its
	// unmet checks stay unknown, never passing.
	Unverified bool
}

// EvaluateReadCompliance evaluates the captured full finding set against the
// captured requirements. head is the reviewed head saved with the bundle:
// check standing is tied to that saved identity, so an observation from
// another head stays pending. Findings must be the unfiltered stored set, not
// a display-filtered view.
func EvaluateReadCompliance(rs *RunState, head string, findings []FindingView) ReadCompliance {
	if rs == nil || rs.Resolved == nil {
		return ReadCompliance{}
	}
	out := ReadCompliance{
		HasPolicy:  hasCapturedPolicy(rs),
		Unverified: !rs.Verified(),
	}
	out.Blockers = CheckCompliance(rs.Resolved, rs.Root.Clauses, findings)
	out.Conflicts = PrivacyConflicts(rs)
	scopes := CheckScopes(rs.Resolved.Plan, &rs.Root)
	out.Pending = pendingUnknown(scopes, rs, head)
	return out
}

// hasCapturedPolicy reports whether the capture states anything a read must
// evaluate: root or per-file clauses, or any selected check.
func hasCapturedPolicy(rs *RunState) bool {
	if setHasPolicy(&rs.Root) {
		return true
	}
	for i := range rs.Resolved.Files {
		if setHasPolicy(&rs.Resolved.Files[i].Requirements) {
			return true
		}
	}
	for i := range rs.Resolved.Plan.Groups {
		if groupHasPolicy(&rs.Resolved.Plan.Groups[i]) {
			return true
		}
	}
	return false
}

// groupHasPolicy reports whether one plan group states required work: a
// selected check, a deep validation pass, or a lens cap needing attestation.
func groupHasPolicy(group *PlanGroup) bool {
	return len(group.Checks) > 0 || group.Depth == depthDeep || group.MaxLenses != nil
}

// setHasPolicy reports whether one requirement set states anything evaluable.
func setHasPolicy(set *RequirementSet) bool {
	return len(set.Clauses) > 0 || len(set.Checks) > 0
}

// pendingUnknown lists required checks without a current verdict. A
// no-open-above-block check is decided ephemerally from the full findings and
// the privacy conflicts, so it is never pending and never needs a prior post;
// head-current and no-live-threads still require fresh matching evidence.
func pendingUnknown(scopes []CheckScope, rs *RunState, head string) []CheckObservation {
	pending := PendingChecks(scopes, rs.Outcomes, head)
	out := make([]CheckObservation, 0, len(pending))
	for i := range pending {
		if pending[i].Check == CheckNoOpenAboveBlock {
			continue
		}
		out = append(out, pending[i])
	}
	return out
}

// Satisfied reports whether the captured policy is fully satisfied: a policy
// exists, provenance verified, no blocking finding, and no pending check.
// Unknown or pending work never reads as satisfied.
func (c *ReadCompliance) Satisfied() bool {
	return c.HasPolicy && !c.Unverified && len(c.Blockers) == 0 &&
		len(c.Conflicts) == 0 && len(c.Pending) == 0
}
