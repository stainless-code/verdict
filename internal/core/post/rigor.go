package postcore

import (
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/validation"
)

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

func (st *state) validationData() *core.ValidationData {
	receipt := st.validationReceipt()
	return &core.ValidationData{
		Hash: receipt.Hash, Counts: receipt.Counts, Mode: receipt.Mode, Floors: receipt.Floors,
	}
}

func (st *state) validationReceipt() *recordcore.ValidationResult {
	if st.merged.Validation != nil {
		return st.merged.Validation
	}
	return &recordcore.ValidationResult{Mode: core.ValidationWarn, Floors: validation.PolicyFromConfig(nil).Floors}
}
