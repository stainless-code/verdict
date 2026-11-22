package core

import "github.com/stainless-code/verdict/internal/jq"

// ReportCompare is `report --compare`'s typed data: the same stable finding
// signatures across a baseline bundle and the current target, grouped into the
// four deterministic buckets. The arrays are always present (empty rather than
// null) and sorted by path, line, then sig, so two runs over the same stores
// print the same bytes.
type ReportCompare struct { //nolint:govet // field order is the published property order
	Baseline    CompareBaseline  `json:"baseline"`
	Counts      CompareCounts    `json:"counts"`
	New         []CompareFinding `json:"new"`
	Persisting  []CompareFinding `json:"persisting"`
	Resolved    []CompareFinding `json:"resolved"`
	NotReviewed []CompareFinding `json:"not_reviewed"`
}

// CompareBaseline names the comparison bundle and the run it recorded, so a
// reader can tell which prior run the buckets describe.
type CompareBaseline struct { //nolint:govet // field order is the published property order
	Dir        string  `json:"dir"`
	Name       string  `json:"name"`
	HeadSHA    *string `json:"head_sha"`
	BaseSHA    *string `json:"base_sha"`
	RecordedAt *string `json:"recorded_at"`
	// Findings counts the identity-bearing baseline findings: persisting plus
	// resolved plus not_reviewed equals it for every comparison.
	Findings int `json:"findings" min:"0"`
}

// CompareCounts tallies the four buckets.
type CompareCounts struct {
	New         int `json:"new"          min:"0"`
	Persisting  int `json:"persisting"   min:"0"`
	Resolved    int `json:"resolved"     min:"0"`
	NotReviewed int `json:"not_reviewed" min:"0"`
}

// CompareFinding is one bucket row. Current-side details describe a new or
// persisting finding; baseline-side details describe a resolved or
// not-reviewed one. Reason is the typed rule that placed the row.
type CompareFinding struct {
	Sig      string   `json:"sig"`
	Status   string   `json:"status"`
	Severity *string  `json:"severity"`
	Path     *string  `json:"path"`
	Line     *int     `json:"line"              min:"0"`
	Claim    string   `json:"claim"`
	Reason   string   `json:"reason"`
	Changes  []string `json:"changes,omitempty" optional:"true"`
}

// Wire renders the compare object in the published key order with non-nil
// arrays, so `--json` emits `[]` instead of `null` for an empty bucket.
func (c *ReportCompare) Wire() *jq.Obj {
	o := jq.NewObj(
		"baseline", c.Baseline.wire(),
		"counts", c.Counts.wire(),
	)
	for _, bucket := range []struct {
		key string
		row []CompareFinding
	}{
		{"new", c.New},
		{"persisting", c.Persisting},
		{"resolved", c.Resolved},
		{"not_reviewed", c.NotReviewed},
	} {
		rows := make([]any, 0, len(bucket.row))
		for i := range bucket.row {
			rows = append(rows, bucket.row[i].wire())
		}
		o.Set(bucket.key, rows)
	}
	return o
}

// wire renders the baseline metadata in the published key order.
func (b *CompareBaseline) wire() *jq.Obj {
	var head, base, recorded any
	if b.HeadSHA != nil {
		head = *b.HeadSHA
	}
	if b.BaseSHA != nil {
		base = *b.BaseSHA
	}
	if b.RecordedAt != nil {
		recorded = *b.RecordedAt
	}
	return jq.NewObj(
		"dir", b.Dir,
		"name", b.Name,
		"head_sha", head,
		"base_sha", base,
		"recorded_at", recorded,
		"findings", b.Findings,
	)
}

// wire renders the four tallies in the published key order.
func (c *CompareCounts) wire() *jq.Obj {
	return jq.NewObj(
		"new", c.New,
		"persisting", c.Persisting,
		"resolved", c.Resolved,
		"not_reviewed", c.NotReviewed,
	)
}

// wire renders one bucket row in the published key order.
func (f *CompareFinding) wire() *jq.Obj {
	var severity, path, line, changes any
	if f.Severity != nil {
		severity = *f.Severity
	}
	if f.Path != nil {
		path = *f.Path
	}
	if f.Line != nil {
		line = *f.Line
	}
	if len(f.Changes) > 0 {
		changes = StringValues(f.Changes)
	}
	o := jq.NewObj(
		"sig", f.Sig,
		"status", f.Status,
		"severity", severity,
		"path", path,
		"line", line,
		"claim", f.Claim,
		"reason", f.Reason,
	)
	if changes != nil {
		o.Set("changes", changes)
	}
	return o
}
