package core

// Calibration is the persisted live outcome audit for one bundle.
type Calibration struct { //nolint:govet // field order follows the published receipt

	SchemaVersion string               `enum:"1"          json:"schema_version"`
	Bundle        string               `json:"bundle"`
	AsOf          string               `json:"as_of"`
	ReviewID      any                  `json:"review_id"`
	Head          map[string]any       `json:"head"`
	PR            map[string]any       `json:"pr"`
	Precision     CalibrationPrecision `json:"precision"`
	Outcomes      []map[string]any     `json:"outcomes"`
	Provenance    map[string]any       `json:"provenance"`
}

// PrecisionCell holds outcome counts and a nullable resolution rate.
type PrecisionCell struct { //nolint:govet // field order follows the published cell

	Fixed           int      `json:"fixed"            min:"0"`
	ResolvedUnfixed int      `json:"resolved_unfixed" min:"0"`
	Open            int      `json:"open"             min:"0"`
	Unknown         int      `json:"unknown"          min:"0"`
	Judged          int      `json:"judged"           min:"0"`
	ResolutionRate  *float64 `json:"resolution_rate"`
}

// CalibrationPrecision publishes the live join's denominators and audit splits.
type CalibrationPrecision struct { //nolint:govet // field order follows the published precision block

	Fixed           int                       `json:"fixed"                  min:"0"`
	ResolvedUnfixed int                       `json:"resolved_unfixed"       min:"0"`
	Open            int                       `json:"open"                   min:"0"`
	Unknown         int                       `json:"unknown"                min:"0"`
	Judged          int                       `json:"judged"                 min:"0"`
	ResolutionRate  *float64                  `json:"resolution_rate"`
	SampleSize      int                       `json:"sample_size"            min:"0"`
	Judge           string                    `enum:"human|agent|mixed|none" json:"judge"`
	AsOf            string                    `json:"as_of"`
	ByJudge         map[string]int            `json:"by_judge"`
	ByTier          map[string]map[string]int `json:"by_tier"`
	BySeverity      map[string]PrecisionCell  `json:"by_severity"`
	ByRule          map[string]PrecisionCell  `json:"by_rule"`
	ByCategory      map[string]PrecisionCell  `json:"by_category"`
}
