package panelcore

// PlanDocument is verification/plan.json, the bounded invoker dispatch
// contract.
type PlanDocument struct { //nolint:govet // schema property order follows the plan document

	SchemaVersion string     `const:"1"           json:"schema_version"`
	HeadSHA       string     `json:"head_sha"`
	GeneratedAt   string     `json:"generated_at"`
	PanelSource   string     `json:"panel_source"`
	Verifiers     []Verifier `json:"verifiers"`
	Findings      []Finding  `json:"findings"`
	SecondLook    []string   `json:"second_look"`
	Dispatch      string     `json:"dispatch"`
	Totals        Totals     `json:"totals"`
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
