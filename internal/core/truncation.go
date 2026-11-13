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
