package core

// CostRecord is one agent self-reported spend record (or one element of the
// array a re-reviewed bundle writes), read from a bundle's cost.json and
// published as schemas/cost.schema.json.
type CostRecord struct {
	SchemaVersion string       `const:"2"                     json:"schema_version,omitempty"`
	Effort        string       `json:"effort,omitempty"`
	Models        []ModelSpend `json:"models,omitempty"`
	EstUSD        *float64     `json:"est_usd,omitempty"`
	WallSeconds   *float64     `json:"wall_seconds,omitempty"`
	DiffBytes     *float64     `json:"diff_bytes,omitempty"`
	Lenses        []LensUsage  `json:"lenses,omitempty"`
	// Run and InputsSHA256 bind a lens attestation to the exact captured run
	// it belongs to, copied verbatim from run.json. They are optional so a
	// billing-only record stays readable, but a record without the current
	// binding cannot certify work for this capture: it is unknown, never
	// zero, and never a pass. Attestation stays agent self-report; this
	// binding only stops stale artifacts from certifying a newer capture.
	Run          string `json:"run,omitempty"`
	InputsSHA256 string `json:"inputs_sha256,omitempty"`
	// GroupCosts carries the agent's own per-group spend claim, keyed by the
	// stable plan group id and the effort tier it ran at. It is
	// agent-reported, never tool-observed: an absent est_usd leaves that
	// group unknown rather than free.
	GroupCosts []GroupCost `json:"group_costs,omitempty"`
}

// GroupCost is one group's agent-reported spend inside a cost record.
type GroupCost struct {
	EstUSD *float64 `json:"est_usd,omitempty"`
	Group  string   `json:"group"`
	Effort string   `json:"effort"`
}

// LensUsage is one agent-reported group entry: the stable plan group
// identity and the labels dispatched for it. Labels is required, so an
// empty list is an explicit zero attestation for that group while an
// omitted group stays unknown. The record is attestation, never
// tool-observed proof.
type LensUsage struct {
	Group  string   `json:"group"`
	Labels []string `json:"labels"`
}

// ModelSpend is one model's token record inside a cost record.
type ModelSpend struct { //nolint:govet // field order is the published property order
	Name                     string   `json:"name"`
	InputTokens              float64  `json:"input_tokens,omitempty"`
	OutputTokens             float64  `json:"output_tokens,omitempty"`
	CacheReadInputTokens     *float64 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *float64 `json:"cache_creation_input_tokens,omitempty"`
}
