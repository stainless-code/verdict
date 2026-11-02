package core

// CostRecord is one agent self-reported spend record (or one element of the
// array a re-reviewed bundle writes), read from a bundle's cost.json and
// published as schemas/cost.schema.json.
type CostRecord struct {
	SchemaVersion string       `const:"1"                     json:"schema_version,omitempty"`
	Effort        string       `json:"effort,omitempty"`
	Models        []ModelSpend `json:"models,omitempty"`
	EstUSD        *float64     `json:"est_usd,omitempty"`
	WallSeconds   *float64     `json:"wall_seconds,omitempty"`
	DiffBytes     *float64     `json:"diff_bytes,omitempty"`
	Lenses        []string     `json:"lenses,omitempty"`
}

// ModelSpend is one model's token record inside a cost record.
type ModelSpend struct { //nolint:govet // field order is the published property order
	Name                     string   `json:"name"`
	InputTokens              float64  `json:"input_tokens,omitempty"`
	OutputTokens             float64  `json:"output_tokens,omitempty"`
	CacheReadInputTokens     *float64 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *float64 `json:"cache_creation_input_tokens,omitempty"`
}
