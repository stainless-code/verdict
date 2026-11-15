package recordcore

import (
	"fmt"
	"maps"
	"slices"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/validation"
)

// ValidationResult is what the shared merge read and applied: the receipt both
// verbs store, plus the violations post refuses on in gate mode.
type ValidationResult struct {
	Hash       *string
	Floors     core.RigorFloors
	Mode       string
	Violations []validation.Violation
	Counts     core.ValidationCounts
}

// applyValidation applies the optional validation.json and the trusted-base
// policy in the shared merge, so findings.json and post.json describe one pass.
func (st *state) applyValidation(in *MergeParams) (*ValidationResult, *core.RefusalError) {
	policy, ref := st.rigorPolicy()
	if ref != nil {
		return nil, ref
	}
	if ref = policy.WithRequireRigor(in.RequireRigor); ref != nil {
		return nil, ref
	}
	file, hash, ref := validation.Load(st.dir, st.headFull, panelcore.Findings(st.review))
	if ref != nil {
		return nil, ref
	}
	out := &ValidationResult{Mode: policy.Mode, Floors: policy.Floors}
	if file != nil {
		out.Hash = &hash
		out.Counts = st.applyVerdicts(file, policy.Mode)
	} else {
		out.Counts.Unvalidated = openCount(st.entries)
	}
	out.Violations = st.applyFloors(policy)
	return out, nil
}

// rigorPolicy reads the policy from the base snapshot the D1 capture wrote, so
// a head edit cannot move the gate.
func (st *state) rigorPolicy() (validation.Policy, *core.RefusalError) {
	raw, present := st.controls.ReadFile(config.ConfigPath)
	if !present {
		return validation.PolicyFromConfig(nil), nil
	}
	cfg, ref := config.ParseBase(&config.BaseConfig{Raw: raw, Present: true})
	if ref != nil {
		return validation.Policy{}, ref
	}
	return validation.PolicyFromConfig(cfg), nil
}

// applyVerdicts applies each verdict to its open finding and returns the tally.
func (st *state) applyVerdicts(file *core.ValidationFile, mode string) core.ValidationCounts {
	var counts core.ValidationCounts
	index := indexResults(file.Results)
	for _, entry := range st.entries {
		m, ok := entry.(*jq.Obj)
		if !ok || m == nil || suppressionString(m, fieldStatus) != statusOpen {
			continue
		}
		result, validated := index[suppressionString(m, fieldSig)]
		if !validated {
			counts.Unvalidated++
			continue
		}
		delete(index, result.Sig)
		st.applyValidationVerdict(m, &result, mode, &counts)
	}
	st.warnUnmatched(index)
	return counts
}

func indexResults(results []core.ValidationResult) map[string]core.ValidationResult {
	index := make(map[string]core.ValidationResult, len(results))
	for _, result := range results {
		index[result.Sig] = result
	}
	return index
}

// warnUnmatched names every result no open finding carried.
func (st *state) warnUnmatched(index map[string]core.ValidationResult) {
	for _, sig := range slices.Sorted(maps.Keys(index)) {
		st.warn(fmt.Sprintf("validation result for %s ignored: no open finding carries it",
			readcommon.ShortSHA(sig)))
	}
}

// applyValidationVerdict applies one judgement: a downgrade moves the severity
// one rank with a history event, and a rejection withholds only in gate mode.
func (st *state) applyValidationVerdict(m *jq.Obj, result *core.ValidationResult, mode string, counts *core.ValidationCounts) {
	short := readcommon.ShortSHA(result.Sig)
	switch result.Verdict {
	case core.ValidationConfirmed:
		counts.Confirmed++
	case core.ValidationDowngraded:
		m.Set(fieldSeverity, result.ToSeverity)
		m.Set(fieldHistory, appendHistory(m, jq.NewObj(fieldAt, st.now, fieldEvent, "validation",
			fieldActor, "validator", "note", fmt.Sprintf("%s → %s: %s", result.FromSeverity, result.ToSeverity, result.Reason))))
		counts.Downgraded++
		st.warn(fmt.Sprintf("validation downgraded %s: %s → %s", short, result.FromSeverity, result.ToSeverity))
	case core.ValidationRejected:
		counts.Rejected++
		if mode != core.ValidationGate {
			st.warn(fmt.Sprintf("validation rejected %s: %s (advisory — posting anyway; set rigor_policy mode=gate to withhold)",
				short, result.Reason))
			return
		}
		st.stampSuppression(m, jq.NewObj(sourceKey, "validation", reasonKey, result.Reason))
	}
}

// applyFloors applies the policy. Advisory mode only names what a gate would
// do: a downgrade moves a stored severity, which can change whether a trusted
// requirement blocks the post.
func (st *state) applyFloors(policy validation.Policy) []validation.Violation {
	downgrades, violations := validation.Floors(st.entries, policy)
	if policy.Mode != core.ValidationGate {
		st.warnAdvisoryFloors(downgrades, violations)
		return nil
	}
	st.applyFloorDowngrades(downgrades)
	if st.cmd == "post" {
		return violations
	}
	for _, v := range violations {
		st.warn(fmt.Sprintf("rigor %s below %s floor %s on %s",
			v.Rigor, v.Severity, v.Floor, readcommon.ShortSHA(v.Sig)))
	}
	return nil
}

// warnAdvisoryFloors reports what a gate would do, moving nothing.
func (st *state) warnAdvisoryFloors(downgrades, violations []validation.Violation) {
	for _, v := range append(downgrades, violations...) {
		st.warn(fmt.Sprintf("rigor %s below %s floor %s on %s (advisory)%s",
			v.Rigor, v.Severity, v.Floor, readcommon.ShortSHA(v.Sig), downgradeNote(v, downgrades)))
	}
}

// applyFloorDowngrades moves each critical that still clears the important
// floor down to important.
func (st *state) applyFloorDowngrades(downgrades []validation.Violation) {
	for _, v := range downgrades {
		if m := findEntry(st.entries, v.Sig); m != nil {
			m.Set(fieldSeverity, "important")
			m.Set(fieldHistory, appendHistory(m, jq.NewObj(fieldAt, st.now, fieldEvent, "validation",
				fieldActor, "validator", "note", fmt.Sprintf(
					"evidence %s below critical floor %s; downgraded to important", v.Rigor, v.Floor))))
		}
		st.warn(fmt.Sprintf("rigor %s below critical floor %s on %s; downgraded to important",
			v.Rigor, v.Floor, readcommon.ShortSHA(v.Sig)))
	}
}

// downgradeNote marks an advisory line a gate would downgrade.
func downgradeNote(v validation.Violation, downgrades []validation.Violation) string {
	for _, d := range downgrades {
		if d.Sig == v.Sig {
			return "; a gate would downgrade it to important"
		}
	}
	return ""
}

// openCount counts the findings a skipped pass would have judged.
func openCount(entries []any) int {
	n := 0
	for _, entry := range entries {
		if suppressionString(entryObj(entry), fieldStatus) == statusOpen && readcommon.WithheldSource(entry) == "" {
			n++
		}
	}
	return n
}

func entryObj(entry any) *jq.Obj {
	m, ok := entry.(*jq.Obj)
	if !ok {
		return nil
	}
	return m
}
