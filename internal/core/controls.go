package core

// ControlsFile is the bundle's controls.json: the review controls resolved
// for one bundle, verified by post/record before any control read.
//
//nolint:govet // field order is the published property order
type ControlsFile struct {
	SchemaVersion string              `enum:"1"                      json:"schema_version"`
	Policy        string              `enum:"base|head"              json:"policy"`
	BaseSHA       string              `json:"base_sha"`
	SHA           string              `json:"sha"`
	StrictInput   bool                `json:"strict_input,omitempty"`
	Files         []ControlsFileEntry `json:"files"`
}

// ControlsFileEntry is one inventory path: the exact bytes written to the
// snapshot and their hash, or status "missing" when the source lacks it.
type ControlsFileEntry struct { //nolint:govet // field order is the published property order
	Path   string `json:"path"`
	Status string `enum:"used|missing" json:"status"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
