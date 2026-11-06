package core

// Config is the typed repository configuration: the versioned
// `.verdict/config.json` shape the strict parser reads and the pure resolver
// consumes. These types are the single source of truth for the published
// `config.schema.json` (aliased from internal/schemagen/model.go); structural
// validation comes from the generated schema and semantic validation from the
// hand-written parser in internal/config, never from an unchecked cast.
//
// Field order is the published property order. Every optional key carries
// `omitempty`; optional nested objects additionally carry `anyOf:"null"` so
// the generator widens them the way every other envelope definition does.
type Config struct { //nolint:govet // field order is the published property order
	SchemaRef      string                   `json:"$schema,omitempty"`
	SchemaVersion  string                   `const:"1"                        json:"schema_version"`
	Defaults       *ConfigDefaults          `anyOf:"null"                     json:"defaults,omitempty"`
	Requirements   *ConfigRequirements      `anyOf:"null"                     json:"requirements,omitempty"`
	Scopes         []ConfigScope            `json:"scopes,omitempty"`
	Profiles       map[string]ConfigProfile `json:"profiles,omitempty"`
	DefaultProfile string                   `json:"default_profile,omitempty"`
}

// ConfigDefaults holds the ordinary review defaults: the flag, environment,
// repository, personal, built-in cascade resolves each field in that order.
// Nested objects merge per field; arrays replace.
type ConfigDefaults struct { //nolint:govet // field order is the published property order
	Panel              []string        `json:"panel,omitempty"`
	BudgetUSD          *float64        `json:"budget_usd,omitempty"`
	MaxBytes           *int64          `json:"max_bytes,omitempty"              min:"1"`
	Chunk              *bool           `json:"chunk,omitempty"`
	KBPolicy           *ConfigKBPolicy `anyOf:"null"                            json:"kb_policy,omitempty"`
	Effort             *string         `enum:"cheap|standard|deep"              json:"effort,omitempty"`
	DisplayMinSeverity *string         `enum:"critical|important|informational" json:"display_min_severity,omitempty"`
	PostMinSeverity    *string         `enum:"critical|important|informational" json:"post_min_severity,omitempty"`
}

// ConfigKBPolicy carries the knowledge-base retirement quorum and ceiling.
type ConfigKBPolicy struct {
	MinJudged *int     `json:"min_judged,omitempty"  min:"1"`
	MaxFPRate *float64 `json:"max_fp_rate,omitempty" max:"1" min:"0"`
}

// ConfigRequirements holds the conjunctive review requirements: every
// applicable clause must hold for compliance, and named prepublication
// evaluations accumulate across matching scopes.
type ConfigRequirements struct { //nolint:govet // field order is the published property order
	BlockClauses  []BlockClause `json:"block_clauses,omitempty"`
	RequireChecks []string      `json:"require_checks,omitempty"`
	RequireBind   *bool         `json:"require_bind,omitempty"`
}

// BlockClause is one per-source, per-path blocking predicate: an open
// finding on one of its paths, in one of its categories, at or above its
// threshold rank fails compliance. Absent categories mean every category;
// absent paths mean every path.
type BlockClause struct {
	Name       string   `json:"name"`
	Threshold  string   `enum:"critical|important|informational" json:"threshold"`
	Categories []string `json:"categories,omitempty"`
	Paths      []string `json:"paths,omitempty"`
}

// ConfigScope is one path-scoped entry: the include and exclude globs that
// select repository-relative paths, plus resolution inputs. A scope carries
// at most one of a named profile or inline defaults (naming both refuses),
// while inline requirements combine freely with either.
type ConfigScope struct { //nolint:govet // field order is the published property order
	Name         string              `json:"name"`
	Include      []string            `json:"include"`
	Exclude      []string            `json:"exclude,omitempty"`
	Profile      *string             `json:"profile,omitempty"`
	Defaults     *ConfigDefaults     `anyOf:"null"             json:"defaults,omitempty"`
	Requirements *ConfigRequirements `anyOf:"null"             json:"requirements,omitempty"`
}

// ConfigProfile is one named work-profile description: provider-neutral
// depth, the checks the group must evaluate, and the lens budget. Advisory
// `effort` stays a plain defaults key; depth, cost tier, and provider
// reasoning settings are distinct axes the later consumer slices own.
type ConfigProfile struct { //nolint:govet // field order is the published property order
	Depth     *string  `enum:"standard|deep"        json:"depth,omitempty"`
	Checks    []string `json:"checks,omitempty"`
	MaxLenses *int     `json:"max_lenses,omitempty" min:"0"`
}
