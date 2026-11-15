package recordcore

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/validation"
)

// ValidationResult is what the shared merge read and applied: the exact-file
// hash (nil when the pass was skipped), the per-verdict tally, the policy the
// gate used, and the findings below their floor. post refuses on Violations
// in gate mode; record only warns, so the store keeps its receipt.
type ValidationResult struct { //nolint:govet // field order is the published property order
	Hash       *string
	Counts     core.ValidationCounts
	Floors     core.RigorFloors
	Mode       string
	Violations []validation.Violation
}

// applyValidation ingests the optional validation.json and the trusted-base
// rigor policy, applies the verdicts and floors to the merged entries, and
// returns the receipt both verbs store. Running it in the shared merge keeps
// findings.json and post.json describing the same pass.
func (st *state) applyValidation(in *MergeParams) (*ValidationResult, *core.RefusalError) {
	policy, ref := st.rigorPolicy()
	if ref != nil {
		return nil, ref
	}
	if policy, ref = policy.WithRequireRigor(in.RequireRigor); ref != nil {
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

// rigorPolicy reads the policy from the trusted-base configuration the D1
// snapshot carries: a head edit never moves the gate, and an absent file
// keeps the built-in floor. A malformed base configuration refuses through
// the one strict parser, never a second dialect.
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

// applyVerdicts indexes the artifact by sig and applies each verdict to its
// open finding, returning the tally. A result for a terminal finding warns
// and is ignored: the validator judges candidates, never the store's history.
func (st *state) applyVerdicts(file *core.ValidationFile, mode string) core.ValidationCounts {
	var counts core.ValidationCounts
	index := make(map[string]core.ValidationResult, len(file.Results))
	for _, result := range file.Results {
		index[result.Sig] = result
	}
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
		st.applyValidationVerdict(m, result, mode, &counts)
	}
	for _, result := range file.Results {
		if _, open := index[result.Sig]; open {
			st.warn(fmt.Sprintf("validation result for %s ignored: no open finding carries it",
				readcommon.ShortSHA(result.Sig)))
		}
	}
	return counts
}

// applyVerdict applies one judgement: a downgrade moves the severity exactly
// one rank with a history event, a rejection withholds the finding in gate
// mode (the D2 receipt, status untouched) and only warns in advisory mode.
func (st *state) applyValidationVerdict(m *jq.Obj, result core.ValidationResult, mode string, counts *core.ValidationCounts) {
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

// applyFloors applies the policy: advisory mode only names what a gate would
// do, because a downgrade moves a stored severity and can change whether a
// trusted requirement blocks. Gate mode downgrades the critical findings whose
// rung still clears the important floor and returns the rest for post to
// refuse; record warns them, since it publishes nothing.
func (st *state) applyFloors(policy validation.Policy) []validation.Violation {
	downgrades, violations := validation.Floors(st.entries, policy)
	if policy.Mode != core.ValidationGate {
		for _, v := range append(downgrades, violations...) {
			st.warn(fmt.Sprintf("rigor %s below %s floor %s on %s (advisory)%s",
				v.Rigor, v.Severity, v.Floor, readcommon.ShortSHA(v.Sig), downgradeNote(v, downgrades)))
		}
		return nil
	}
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
	if st.cmd == "post" {
		return violations
	}
	for _, v := range violations {
		st.warn(fmt.Sprintf("rigor %s below %s floor %s on %s",
			v.Rigor, v.Severity, v.Floor, readcommon.ShortSHA(v.Sig)))
	}
	return nil
}

// downgradeNote marks an advisory line whose finding a gate would downgrade.
func downgradeNote(v validation.Violation, downgrades []validation.Violation) string {
	for _, d := range downgrades {
		if d.Sig == v.Sig {
			return "; a gate would downgrade it to important"
		}
	}
	return ""
}

// openCount counts the findings a skipped validation pass would have judged.
func openCount(entries []any) int {
	n := 0
	for _, entry := range entries {
		if suppressionString(entryObj(entry), fieldStatus) == statusOpen && readcommon.WithheldSource(entry) == "" {
			n++
		}
	}
	return n
}

// entryObj lifts one entry to its object, or nil.
func entryObj(entry any) *jq.Obj {
	m, _ := entry.(*jq.Obj)
	return m
}
