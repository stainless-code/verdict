package core

// The trusted validator's three judgements; a verdict never upgrades.
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

// ValidationResult is one validator judgement on one candidate finding; a
// DOWNGRADED verdict requires the severity pair, and no other verdict may
// carry one.
type ValidationResult struct {
	Sig          string   `json:"sig"`
	Verdict      string   `enum:"CONFIRMED|REJECTED|DOWNGRADED"           json:"verdict"`
	Reason       string   `json:"reason"                                  maxLength:"500"                minLength:"10"`
	FromSeverity string   `enum:"critical|important|informational"        json:"from_severity,omitempty"`
	ToSeverity   string   `enum:"critical|important|informational"        json:"to_severity,omitempty"`
	Checks       []string `items_enum:"hedge|repro|anchor|rigor|severity" json:"checks,omitempty"`
}

// ValidationValidator names who ran the pass; every field is optional.
type ValidationValidator struct {
	Model        string `json:"model,omitempty"`
	PromptSHA256 string `json:"prompt_sha256,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
}

// ValidationFile is the bundle's validation.json.
type ValidationFile struct {
	SchemaVersion string               `const:"1"       json:"schema_version"`
	HeadSHA       string               `json:"head_sha" minLength:"1"`
	Validator     *ValidationValidator `anyOf:"null"    json:"validator,omitempty" optional:"true"`
	Results       []ValidationResult   `json:"results"`
}

// ValidationCounts is the receipt's per-verdict tally; Unvalidated counts the
// open findings no result named.
type ValidationCounts struct {
	Confirmed   int `json:"confirmed"   min:"0"`
	Rejected    int `json:"rejected"    min:"0"`
	Downgraded  int `json:"downgraded"  min:"0"`
	Unvalidated int `json:"unvalidated" min:"0"`
}

// RigorFloors is the effective evidence floor per severity tier.
type RigorFloors struct {
	Critical      string `enum:"said|pointed|walked|ran|reproduced" json:"critical"`
	Important     string `enum:"said|pointed|walked|ran|reproduced" json:"important"`
	Informational string `enum:"said|pointed|walked|ran|reproduced" json:"informational"`
}
