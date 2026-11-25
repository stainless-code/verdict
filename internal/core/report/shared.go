package reportcore

import (
	"slices"
	"strings"

	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// revalidatorActor and addressedEvent are the history record a VERIFIED
// claim leaves on the finding it flips: record's revalidator marks it
// addressed, noting the command whose verify receipt backed the claim.
const (
	revalidatorActor = "revalidator"
	addressedEvent   = "addressed"
)

// withSharedNote names, in the evidence detail, the directories the verify
// receipt behind a VERIFIED claim shared into its snapshot: bytes outside the
// graded tree the passing run may have depended on. It is information, never
// a status change; refusing such receipts is a strict gate's separate choice.
func withSharedNote(st *state, detail string) string {
	receipt, found, problem := verifycore.ReadVerifyReceipt(st.dir)
	if !found || problem != "" || len(receipt.Shared) == 0 || !revalidatedBy(st.entries, receipt.Command) {
		return detail
	}
	return detail + "; receipt used shared directories: " + strings.Join(receipt.Shared, ", ")
}

// revalidatedBy reports whether a VERIFIED claim citing command flipped a
// finding.
func revalidatedBy(entries []any, command string) bool {
	return slices.ContainsFunc(entries, func(entry any) bool {
		history, ok := readcommon.FieldOf(entry, "history").([]any)
		return ok && slices.ContainsFunc(history, func(ev any) bool { return revalidation(ev, command) })
	})
}

// revalidation reports whether one history event is the revalidator marking
// a finding addressed on command's receipt.
func revalidation(ev any, command string) bool {
	return readcommon.FieldOf(ev, "actor") == revalidatorActor && readcommon.FieldOf(ev, "event") == addressedEvent &&
		jq.StrOf(readcommon.FieldOf(ev, "note")) == command
}
