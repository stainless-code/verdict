package recordcore

import (
	"fmt"
	"maps"
	"slices"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
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

func (st *state) applyValidation() *core.RefusalError {
	policy, ref := st.rigorPolicy()
	if ref != nil {
		return ref
	}
	if ref = policy.WithRequireRigor(st.requireRigor); ref != nil {
		return ref
	}
	file, hash, ref := validation.Load(st.dir, st.headFull, st.entries)
	if ref != nil {
		return ref
	}
	out := &ValidationResult{Mode: policy.Mode, Floors: policy.Floors}
	st.judged = map[string]core.ValidationResult{}
	if file != nil {
		st.warnPromptDrift(file)
		out.Hash = &hash
		if out.Counts, ref = st.applyVerdicts(file, policy.Mode); ref != nil {
			return ref
		}
	}
	st.policy = policy
	st.validation = out
	return nil
}

func (st *state) warnPromptDrift(file *core.ValidationFile) {
	if drift := validation.PromptDrift(file); drift != "" {
		st.warn(drift)
	}
}

func (st *state) sealFloors() {
	if st.validation == nil {
		return
	}
	st.validation.Violations = st.applyFloors(st.policy)
}

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

func (st *state) applyVerdicts(file *core.ValidationFile, mode string) (core.ValidationCounts, *core.RefusalError) {
	var counts core.ValidationCounts
	index := indexResults(file.Results)
	st.judged = make(map[string]core.ValidationResult, len(index))
	maps.Copy(st.judged, index)
	for _, entry := range st.entries {
		m := validation.CandidateObj(entry)
		if m == nil {
			continue
		}
		result, validated := index[suppressionString(m, fieldSig)]
		if !validated {
			continue
		}
		delete(index, result.Sig)
		if ref := st.applyValidationVerdict(m, &result, mode, &counts); ref != nil {
			return counts, ref
		}
	}
	st.warnUnmatched(index)
	return counts, nil
}

func indexResults(results []core.ValidationResult) map[string]core.ValidationResult {
	index := make(map[string]core.ValidationResult, len(results))
	for _, result := range results {
		index[result.Sig] = result
	}
	return index
}

func (st *state) warnUnmatched(index map[string]core.ValidationResult) {
	for _, sig := range slices.Sorted(maps.Keys(index)) {
		st.warn(fmt.Sprintf("validation result for %s ignored: no open finding carries it",
			readcommon.ShortSHA(sig)))
	}
}

func (st *state) applyValidationVerdict(
	m *jq.Obj, result *core.ValidationResult, mode string, counts *core.ValidationCounts,
) *core.RefusalError {
	short := readcommon.ShortSHA(result.Sig)
	switch result.Verdict {
	case core.ValidationConfirmed:
		counts.Confirmed++
	case core.ValidationDowngraded:
		if ref := downgradeRefusal(m, result); ref != nil {
			return ref
		}
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
			return nil
		}
		st.stampSuppression(m, jq.NewObj(sourceKey, "validation", reasonKey, result.Reason))
	}
	return nil
}

func downgradeRefusal(m *jq.Obj, result *core.ValidationResult) *core.RefusalError {
	current := suppressionString(m, fieldSeverity)
	if current == result.FromSeverity {
		return nil
	}
	return &core.RefusalError{Code: validation.CodeBad, Message: fmt.Sprintf(
		"validation.json DOWNGRADED for %s names from_severity %s, but the finding is %s",
		readcommon.ShortSHA(result.Sig), result.FromSeverity, current)}
}

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

func (st *state) warnAdvisoryFloors(downgrades, violations []validation.Violation) {
	for _, v := range append(downgrades, violations...) {
		st.warn(fmt.Sprintf("rigor %s below %s floor %s on %s (advisory)%s",
			v.Rigor, v.Severity, v.Floor, readcommon.ShortSHA(v.Sig), downgradeNote(v, downgrades)))
	}
}

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

func downgradeNote(v validation.Violation, downgrades []validation.Violation) string {
	for _, d := range downgrades {
		if d.Sig == v.Sig {
			return "; a gate would downgrade it to important"
		}
	}
	return ""
}

func (st *state) settleValidation() {
	if st.validation == nil {
		return
	}
	n := 0
	for _, entry := range st.entries {
		if validation.Candidate(entry) && !isJudged(st.judged, entry) {
			n++
		}
	}
	st.validation.Counts.Unvalidated = n
}

func isJudged(judged map[string]core.ValidationResult, entry any) bool {
	_, named := judged[jq.StrOf(readcommon.FieldOf(entry, fieldSig))]
	return named
}
