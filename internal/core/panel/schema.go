package panelcore

// PlanDocument is verification/plan.json, the bounded invoker dispatch
// contract.
type PlanDocument struct { //nolint:govet // schema property order follows the plan document
	ExecutionID  string            `json:"execution_id,omitempty"`
	DiffSHA256   string            `json:"diff_sha256,omitempty"`
	ReviewSHA256 string            `json:"review_sha256,omitempty"`
	Sources      []ExecutionSource `json:"sources,omitempty"       maxItems:"100"`

	Checks        []DirectedCheck `json:"checks,omitempty"`
	SchemaVersion string          `enum:"1|2"              json:"schema_version"`
	HeadSHA       string          `json:"head_sha"`
	GeneratedAt   string          `json:"generated_at"     optional:"true"`
	PanelSource   string          `json:"panel_source"     optional:"true"`
	Verifiers     []Verifier      `json:"verifiers"        optional:"true"`
	Findings      []Finding       `json:"findings"         optional:"true"`
	SecondLook    []string        `json:"second_look"      optional:"true"`
	Dispatch      string          `json:"dispatch"         optional:"true"`
	Totals        Totals          `json:"totals"           optional:"true"`
}

// Verifier is one roster member after the synthesizer.
type Verifier struct {
	Provider *string `json:"provider"`
	Model    string  `json:"model"`
}

// Finding is the inline anchor handed to independent verifiers.
type Finding struct {
	Sig      string `json:"sig"`
	Path     string `json:"path"`
	Line     any    `json:"line"     type:"integer|string|null"`
	Severity any    `json:"severity" type:"string|null"`
	Kind     string `const:"inline"  json:"kind"`
}

// Totals bounds the work an invoker dispatches.
type Totals struct {
	Findings   int `json:"findings"   max:"100" min:"0"`
	Verifiers  int `json:"verifiers"  max:"4"   min:"1"`
	Dispatches int `json:"dispatches" max:"400" min:"0"`
}

// DirectedCheck is assigned by the coordinator before a checker sees a claim.
type DirectedCheck struct {
	Sig     string `json:"sig"`
	Origin  string `enum:"finder-1|finder-2" json:"origin"`
	Checker string `enum:"finder-1|finder-2" json:"checker"`
}

// ExecutionSource preserves exact finder origins for one union signature.
type ExecutionSource struct {
	Sig     string         `json:"sig"`
	Origins []FinderOrigin `json:"origins" maxItems:"2" minItems:"1"`
}

// FinderOrigin binds a source index to the original finder claim.
type FinderOrigin struct {
	Finding map[string]any `json:"finding"`
	Finder  string         `enum:"finder-1|finder-2" json:"finder"`
	Index   int            `json:"index"             min:"0"`
}
