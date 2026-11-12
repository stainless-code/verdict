package core

// Config is the typed repository configuration: the versioned
// `.verdict/config.json` shape the strict parser reads and the pure resolver
// consumes. These types are the single source of truth for the published
// `config.schema.json` (aliased from internal/schemagen/model.go); structural
// validation comes from the generated schema and semantic validation from the
// hand-written parser in internal/config, which agree on every structural
// rule. Semantic-only rules (duplicate keys, cross-section references, glob
// portability, per-group clause coverage) stay runtime-specific.
//
// Field order is the published property order. Every optional key carries
// `omitempty` plus `nonnull:"true"`: absent reads as missing, while a
// present null refuses exactly like the parser. Closed `closed:true` $defs
// keep fixed-shape objects exact.
type Config struct { //nolint:govet // field order is the published property order
	SchemaRef      string                   `json:"$schema,omitempty"         nonnull:"true"`
	SchemaVersion  string                   `const:"1"                        json:"schema_version"`
	Defaults       *ConfigDefaults          `json:"defaults,omitempty"        nonnull:"true"`
	Requirements   *ConfigRequirements      `json:"requirements,omitempty"    nonnull:"true"`
	Scopes         []ConfigScope            `json:"scopes,omitempty"`
	Profiles       map[string]ConfigProfile `json:"profiles,omitempty"`
	DefaultProfile string                   `json:"default_profile,omitempty" nonnull:"true"`
	Index          *ConfigIndex             `json:"index,omitempty"           nonnull:"true"`
}

// ConfigIndex holds the local code index caps; absent keys keep the built-in
// defaults.
type ConfigIndex struct {
	Files       *int   `json:"files,omitempty"         min:"1" nonnull:"true"`
	FileBytes   *int64 `json:"file_bytes,omitempty"    min:"1" nonnull:"true"`
	TreeBytes   *int64 `json:"tree_bytes,omitempty"    min:"1" nonnull:"true"`
	ShardsBytes *int64 `json:"shards_bytes,omitempty"  min:"1" nonnull:"true"`
	KeepTrees   *int   `json:"keep_trees,omitempty"    min:"1" nonnull:"true"`
	RefsPerFile *int   `json:"refs_per_file,omitempty" min:"1" nonnull:"true"`
}

// ConfigDefaults holds the ordinary review defaults: the flag, environment,
// repository, personal, built-in cascade resolves each field in that order.
// Nested objects merge per field; arrays replace.
type ConfigDefaults struct { //nolint:govet // field order is the published property order
	Panel              []string        `json:"panel,omitempty"                  minItems:"1"                          nonnull:"true"`
	BudgetUSD          *float64        `exclusiveMin:"0"                        json:"budget_usd,omitempty"           nonnull:"true"`
	MaxBytes           *int64          `json:"max_bytes,omitempty"              min:"1"                               nonnull:"true"`
	Chunk              *bool           `json:"chunk,omitempty"                  nonnull:"true"`
	KBPolicy           *ConfigKBPolicy `json:"kb_policy,omitempty"              nonnull:"true"`
	Effort             *string         `enum:"cheap|standard|deep"              json:"effort,omitempty"               nonnull:"true"`
	DisplayMinSeverity *string         `enum:"critical|important|informational" json:"display_min_severity,omitempty" nonnull:"true"`
	PostMinSeverity    *string         `enum:"critical|important|informational" json:"post_min_severity,omitempty"    nonnull:"true"`
}

// ConfigKBPolicy carries the knowledge-base retirement quorum and ceiling.
type ConfigKBPolicy struct {
	MinJudged *int     `json:"min_judged,omitempty"  min:"1" nonnull:"true"`
	MaxFPRate *float64 `json:"max_fp_rate,omitempty" max:"1" min:"0"        nonnull:"true"`
}

// ConfigRequirements holds the conjunctive review requirements: every
// applicable clause must hold for compliance, and named prepublication
// evaluations accumulate across matching scopes.
type ConfigRequirements struct { //nolint:govet // field order is the published property order
	BlockClauses  []BlockClause `json:"block_clauses,omitempty"                                minItems:"1"                    nonnull:"true"`                //nolint:revive // tags encode the published order and cannot wrap
	RequireChecks []string      `items_enum:"no-open-above-block|head-current|no-live-threads" json:"require_checks,omitempty" minItems:"1"   nonnull:"true"` //nolint:revive // tags encode exact enums and cannot wrap
	RequireBind   *bool         `json:"require_bind,omitempty"                                 nonnull:"true"`
}

// BlockClause is one per-source, per-path blocking predicate: an open
// finding on one of its paths, in one of its categories, at or above its
// threshold rank fails compliance. Absent categories mean every category;
// absent paths mean every path.
type BlockClause struct {
	Name       string   `json:"name"`
	Threshold  string   `enum:"critical|important|informational"                                              json:"threshold"`                                          //nolint:revive // tags encode exact enums and cannot wrap
	Categories []string `items_enum:"logic|edge-case|security|performance|accidental-commit|style|docs|other" json:"categories,omitempty" minItems:"1"   nonnull:"true"` //nolint:revive // tags encode exact enums and cannot wrap
	Paths      []string `json:"paths,omitempty"                                                               minItems:"1"                nonnull:"true"`                //nolint:revive // tags encode the published order and cannot wrap
}

// ConfigScope is one path-scoped entry: the include and exclude globs that
// select repository-relative paths, plus resolution inputs. A scope carries
// at most one of a named profile or inline defaults (naming both refuses),
// while inline requirements combine freely with either.
type ConfigScope struct { //nolint:govet // field order is the published property order
	Name         string              `json:"name"`
	Include      []string            `json:"include"                minItems:"1"   nonnull:"true"`
	Exclude      []string            `json:"exclude,omitempty"      nonnull:"true"`
	Profile      *string             `json:"profile,omitempty"      nonnull:"true"`
	Defaults     *ConfigDefaults     `json:"defaults,omitempty"     nonnull:"true"`
	Requirements *ConfigRequirements `json:"requirements,omitempty" nonnull:"true"`
}

// ConfigProfile is one named work-profile description: provider-neutral
// depth, the checks the group must evaluate, and the lens budget. Advisory
// `effort` stays a plain defaults key; depth, cost tier, and provider
// reasoning settings are distinct axes the later consumer slices own.
type ConfigProfile struct { //nolint:govet // field order is the published property order
	Depth     *string  `enum:"standard|deep"                                          json:"depth,omitempty"  nonnull:"true"`
	Checks    []string `items_enum:"no-open-above-block|head-current|no-live-threads" json:"checks,omitempty" minItems:"1"   nonnull:"true"`
	MaxLenses *int     `json:"max_lenses,omitempty"                                   min:"0"                 nonnull:"true"`
}
