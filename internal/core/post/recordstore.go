package postcore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/stainless-code/verdict/internal/core"
)

// storedRecord is the part of record.json post reads: the decision record
// stored, and the inputs it read, so post publishes that decision instead of
// a second opinion and can prove nothing moved between the two verbs.
type storedRecord struct {
	Approval     *core.ApprovalReceipt
	Head         string
	DiffSHA256   string
	ReviewSHA256 string
}

// recordFile is the completed-record artifact record writes beside the bundle.
const recordFile = "/record.json"

// loadStoredRecord reads the bundle's record.json. ok is false when the
// bundle carries no record, or a record written before the approval receipt
// existed: an unrecorded review decides from post's own merge, and a record
// with no decision is not a decision to republish.
func loadStoredRecord(dir string) (storedRecord, bool) {
	raw, err := os.ReadFile(dir + recordFile)
	if err != nil {
		return storedRecord{}, false
	}
	var receipt struct {
		Head         string          `json:"head"`
		DiffSHA256   string          `json:"diff_sha256"`
		ReviewSHA256 string          `json:"review_sha256"`
		Approval     json.RawMessage `json:"approval"`
	}
	if json.Unmarshal(raw, &receipt) != nil || len(receipt.Approval) == 0 {
		return storedRecord{}, false
	}
	out := storedRecord{
		Head:         receipt.Head,
		DiffSHA256:   receipt.DiffSHA256,
		ReviewSHA256: receipt.ReviewSHA256,
	}
	if json.Unmarshal(receipt.Approval, &out.Approval) != nil || out.Approval == nil {
		return storedRecord{}, false
	}
	return out, true
}

// adoptStoredApproval publishes record's decision. The record is the one verb
// that decides, so post reads its receipt rather than keeping the second
// opinion its own merge computed; a record whose review, diff, or head moved
// since it was written refuses instead of silently recomputing over a review
// the operator never recorded.
func (st *state) adoptStoredApproval() *core.RefusalError {
	record, ok := loadStoredRecord(st.dir)
	if !ok {
		return nil
	}
	if record.Head != st.commitID || record.DiffSHA256 != fileSHA256(st.dir+"/full.diff") ||
		record.ReviewSHA256 != fileSHA256(st.flags.ReviewFile) {
		return &core.RefusalError{Code: "record-stale", Message: msgRecordStale}
	}
	st.merged.Approval = record.Approval
	return nil
}

// fileSHA256 hashes one artifact's bytes, the same way record stamps the
// review and diff it read.
func fileSHA256(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
