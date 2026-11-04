package core

// CorpusCase is one benchmark corpus manifest: a real or seeded PR bundle
// with the ground truth its review output is graded against. The checked-in
// cases live under bench/corpus/<id>/case.json; a run bundle copies the
// manifest to .review/corpus-*/corpus.json so calibrate and trend can join
// offline. Published as schemas/corpus.schema.json.
type CorpusCase struct {
	SchemaVersion string           `const:"1"                       json:"schema_version"`
	ID            string           `json:"id"`
	Kind          string           `enum:"seeded|real"              json:"kind"`
	Language      string           `json:"language,omitempty"`
	LineTolerance *int             `json:"line_tolerance,omitempty" min:"0"`
	Provenance    CorpusProvenance `json:"provenance"`
	Bugs          []CorpusBug      `json:"bugs"`
}

// CorpusProvenance records where a case came from: the injected defect for
// seeded cases, or the repository, PR, and later bug-fix commit for real
// ones.
type CorpusProvenance struct {
	Kind      string `enum:"seeded|real"          json:"kind"`
	Injected  string `json:"injected,omitempty"`
	Repo      string `json:"repo,omitempty"`
	PR        *int   `json:"pr,omitempty"`
	FixCommit string `json:"fix_commit,omitempty"`
	Source    string `json:"source,omitempty"`
	Note      string `json:"note,omitempty"`
}

// CorpusBug is one known defect a review is expected to report.
type CorpusBug struct { //nolint:govet // field order is the published property order
	Path     string `json:"path"`
	Line     int    `json:"line"                             min:"1"`
	Kind     string `json:"kind"`
	Severity string `enum:"critical|important|informational" json:"severity"`
	Note     string `json:"note,omitempty"`
}
