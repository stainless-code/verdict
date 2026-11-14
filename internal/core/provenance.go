package core

import "github.com/stainless-code/verdict/internal/jq"

// ProvenanceReceipt is the persisted provenance.json receipt and the
// `data.receipt` object exposed by `report --provenance --json` and
// `post --provenance --json`.
type ProvenanceReceipt struct {
	SchemaVersion string                   `const:"1"              json:"schema_version"`
	RunID         string                   `json:"run_id"`
	PromptsHash   string                   `json:"prompts_hash"`
	Panel         []string                 `json:"panel,omitempty"`
	Inputs        []ProvenanceInput        `json:"inputs"`
	Truncated     []ProvenanceTruncatedRow `json:"truncated"`
}

// ProvenanceInput records one resolved input, artifact, rule candidate,
// control, config layer, or exclusion summary without copying rule text or
// secret values.
type ProvenanceInput struct { //nolint:govet // field order is the published property order
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	ID            string   `json:"id,omitempty"`
	Status        string   `json:"status"`
	Reason        string   `json:"reason,omitempty"`
	SHA256        string   `json:"sha256,omitempty"`
	Bytes         *int64   `json:"bytes,omitempty"          min:"0"`
	Source        string   `json:"source,omitempty"`
	Layer         string   `json:"layer,omitempty"`
	Members       []string `json:"members,omitempty"`
	Included      *int     `json:"included,omitempty"       min:"0"`
	Omitted       *int     `json:"omitted,omitempty"        min:"0"`
	CharsIncluded *int     `json:"chars_included,omitempty" min:"0"`
	CharsTotal    *int     `json:"chars_total,omitempty"    min:"0"`
	Cap           *int     `json:"cap,omitempty"            min:"1"`
	Excluded      *int     `json:"excluded,omitempty"       min:"0"`
	ExcludedBytes *int64   `json:"excluded_bytes,omitempty" min:"0"`
	KeptBytes     *int64   `json:"kept_bytes,omitempty"     min:"0"`
	Detail        string   `json:"detail,omitempty"`
}

// ProvenanceTruncatedRow records one bounded or omitted input row, reusing
// core.Truncation's fields alongside the provenance-specific counts and cap.
type ProvenanceTruncatedRow struct { //nolint:govet // field order is the published property order
	Input            string `json:"input"`
	ID               string `json:"id,omitempty"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
	Truncated        bool   `json:"truncated"`
	TruncationReason string `enum:"work_budget|response_cap|time_budget|scan_cap|chunk_cap" json:"truncation_reason"`
	Returned         int    `json:"returned"                                                min:"0"`
	Total            *int   `json:"total"                                                   min:"0"`
	NextOffset       *int   `json:"next_offset"                                             min:"0"`
	Included         *int   `json:"included,omitempty"                                      min:"0"`
	Omitted          *int   `json:"omitted,omitempty"                                       min:"0"`
	CharsIncluded    *int   `json:"chars_included,omitempty"                                min:"0"`
	CharsTotal       *int   `json:"chars_total,omitempty"                                   min:"0"`
	Cap              *int   `json:"cap,omitempty"                                           min:"1"`
}

// NewProvenanceTruncatedRow constructs a truncation row carrying the canonical
// core.Truncation fields from ResultCut/ScanCut.
func NewProvenanceTruncatedRow(input, id, status, reason string, cut *Truncation) ProvenanceTruncatedRow {
	row := ProvenanceTruncatedRow{
		Input:  input,
		ID:     id,
		Status: status,
		Reason: reason,
	}
	if cut != nil {
		row.Truncated = cut.Truncated
		row.TruncationReason = cut.TruncationReason
		row.Returned = cut.Returned
		row.Total = cut.Total
		row.NextOffset = cut.NextOffset
	}
	return row
}

// applyProvenanceWire sets data.receipt and data.truncated on wire objects when
// --provenance is requested, and leaves them absent otherwise.
func applyProvenanceWire(o *jq.Obj, enabled bool, receipt *ProvenanceReceipt, truncated []ProvenanceTruncatedRow) {
	if !enabled {
		return
	}
	if receipt == nil {
		receipt = &ProvenanceReceipt{
			SchemaVersion: "1",
			Inputs:        []ProvenanceInput{},
			Truncated:     []ProvenanceTruncatedRow{},
		}
	}
	if receipt.Inputs == nil {
		receipt.Inputs = []ProvenanceInput{}
	}
	if receipt.Truncated == nil {
		receipt.Truncated = []ProvenanceTruncatedRow{}
	}
	if truncated == nil {
		truncated = []ProvenanceTruncatedRow{}
	}
	o.Set("receipt", receipt)
	o.Set("truncated", truncated)
}
