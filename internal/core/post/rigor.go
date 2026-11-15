package postcore

import (
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/validation"
)

// rigorGate enforces the evidence floor the trusted base and --require-rigor
// set: gate mode refuses every posting finding below its tier, and --force
// records the sigs it published anyway instead of skipping silently.
func (st *state) rigorGate() *core.RefusalError {
	receipt := st.merged.Validation
	if receipt == nil || receipt.Mode != core.ValidationGate || len(receipt.Violations) == 0 {
		return nil
	}
	ref := validation.BelowFloor(receipt.Violations)
	sigs := make([]string, 0, len(receipt.Violations))
	details := make([]string, 0, len(receipt.Violations))
	for _, violation := range receipt.Violations {
		sigs = append(sigs, readcommon.ShortSHA(violation.Sig))
		details = append(details, violation.Detail())
	}
	if !st.flags.Force {
		return ref
	}
	st.forced = append(st.forced, core.ForcedPost{Reason: ref.Code, Sigs: sigs})
	st.warn("forced past " + ref.Code + ": " + strings.Join(details, ", "))
	return nil
}

// validationData is the envelope's view of the pass: the same hash, tally,
// mode, and floors the receipt stores.
func (st *state) validationData() *core.ValidationData {
	receipt := st.validationReceipt()
	return &core.ValidationData{
		Hash: receipt.Hash, Counts: receipt.Counts, Mode: receipt.Mode, Floors: receipt.Floors,
	}
}

// validationReceipt is the pass the merge applied; a state built without one
// reads the built-in policy, so a receipt never claims a gate that did not run.
func (st *state) validationReceipt() *recordcore.ValidationResult {
	if st.merged.Validation != nil {
		return st.merged.Validation
	}
	return &recordcore.ValidationResult{Mode: core.ValidationWarn, Floors: validation.PolicyFromConfig(nil).Floors}
}
