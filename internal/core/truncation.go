package core

import (
	"reflect"

	"github.com/stainless-code/verdict/internal/jq"
)

// Truncation is a receipt for a bounded result. Counts use the producer's
// documented unit; Total is unknown when the producer stopped scanning.
type Truncation struct { //nolint:govet // field order is the published receipt order
	Truncated        bool   `json:"truncated"`
	TruncationReason string `enum:"work_budget|response_cap|time_budget|scan_cap|chunk_cap" json:"truncation_reason"`
	Returned         int    `json:"returned"                                                min:"0"`
	Total            *int   `json:"total"                                                   min:"0"`
	NextOffset       *int   `json:"next_offset"                                             min:"0"`
}

// Apply appends the receipt to data. A nil receipt leaves data byte-identical.
func (t *Truncation) Apply(data *jq.Obj) *jq.Obj {
	if t == nil {
		return data
	}
	receipt := structObj(reflect.ValueOf(t).Elem())
	for _, key := range receipt.Keys {
		value, _ := receipt.Get(key)
		data.Set(key, value)
	}
	return data
}

// ResultCut reports a capped list. A nil total means the scan stopped before
// counting the full domain; next is nil for producers without offset support.
func ResultCut(reason string, returned int, total, next *int) *Truncation {
	return &Truncation{Truncated: true, TruncationReason: reason, Returned: returned, Total: total, NextOffset: next}
}

// ScanCut reports an incomplete scan with an unknown total.
func ScanCut(cut bool, returned int) *Truncation {
	if !cut {
		return nil
	}
	return ResultCut("scan_cap", returned, nil, nil)
}

// ForResult carries source incompleteness into a different result domain.
// Its total and continuation are unknown in the new domain. A nil or complete
// source receipt stays nil.
func (t *Truncation) ForResult(returned int) *Truncation {
	if t == nil || !t.Truncated {
		return nil
	}
	return ResultCut(t.TruncationReason, returned, nil, nil)
}
