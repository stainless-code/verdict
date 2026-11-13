package core

// ExcludedPath is one removed raw diff section.
type ExcludedPath struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"`
	RuleID  string `json:"rule_id"`
	Bytes   int    `json:"bytes"              min:"0"`
}

// ExcludedFile records every removed byte and both diff hashes.
type ExcludedFile struct { //nolint:govet // published property order

	SchemaVersion     string         `const:"1"                  json:"schema_version"`
	PatternsSource    string         `json:"patterns_source"`
	Ignore            []string       `json:"ignore"`
	NoDefaultExcludes bool           `json:"no_default_excludes"`
	Excluded          []ExcludedPath `json:"excluded"`
	ExcludedBytes     int            `json:"excluded_bytes"      min:"0"`
	KeptBytes         int            `json:"kept_bytes"          min:"0"`
	RawDiffSHA256     string         `json:"raw_diff_sha256"`
	DiffSHA256        string         `json:"diff_sha256"`
}
