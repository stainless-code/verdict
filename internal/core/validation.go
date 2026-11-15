package core

// The trusted validator's three judgements. A verdict never upgrades a
// finding: CONFIRMED keeps it, REJECTED withholds it in gate mode, and
// DOWNGRADED moves its severity exactly one rank.
const (
	ValidationConfirmed  = "CONFIRMED"
	ValidationRejected   = "REJECTED"
	ValidationDowngraded = "DOWNGRADED"
)

// ValidationModes: advisory is the default, and gate withholds.
const (
	ValidationWarn = "warn"
	ValidationGate = "gate"
)

// ValidationResult is one validator judgement on one candidate finding.
// FromSeverity and ToSeverity are required by DOWNGRADED and refused on any
// other verdict; Reason is stored, never posted as the finding's evidence.
type ValidationResult struct { //nolint:govet // field order is the published property order
	Sig          string   `json:"sig"`
	Verdict      string   `enum:"CONFIRMED|REJECTED|DOWNGRADED"                json:"verdict"`
	Reason       string   `json:"reason"                                       maxLength:"500"                 minLength:"10"`
	FromSeverity string   `enum:"critical|important|informational"            json:"from_severity,omitempty"`
	ToSeverity   string   `enum:"critical|important|informational"            json:"to_severity,omitempty"`
	Checks       []string `items_enum:"hedge|repro|anchor|rigor|severity"      json:"checks,omitempty"`
}

// ValidationValidator names who ran the pass: the invoker's model, the hash
// of the prompt it was handed, and when it started. Every field is optional.
type ValidationValidator struct {
	Model        string `json:"model,omitempty"`
	PromptSHA256 string `json:"prompt_sha256,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
}

// ValidationFile is the bundle's validation.json: the head it reviewed, the
// validator, and one result per candidate finding.
type ValidationFile struct { //nolint:govet // field order is the published property order
	SchemaVersion string               `const:"1"       json:"schema_version"`
	HeadSHA       string               `json:"head_sha" minLength:"1"`
	Validator     *ValidationValidator `anyOf:"null"    json:"validator,omitempty" optional:"true"`
	Results       []ValidationResult   `json:"results"`
}

// ValidationCounts is the receipt's per-verdict tally over the stored
// findings; Unvalidated counts the open findings no result named.
type ValidationCounts struct {
	Confirmed   int `json:"confirmed"   min:"0"`
	Rejected    int `json:"rejected"    min:"0"`
	Downgraded  int `json:"downgraded"  min:"0"`
	Unvalidated int `json:"unvalidated" min:"0"`
}

// RigorFloors is the effective evidence floor per severity tier, weakest
// rung last: a finding below its tier's floor is downgraded or withheld.
type RigorFloors struct { //nolint:govet // field order is the published property order
	Critical      string `enum:"said|pointed|walked|ran|reproduced" json:"critical"`
	Important     string `enum:"said|pointed|walked|ran|reproduced" json:"important"`
	Informational string `enum:"said|pointed|walked|ran|reproduced" json:"informational"`
}
