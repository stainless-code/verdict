package panelcore

// PlanDocument is verification/plan.json, the bounded invoker dispatch contract.
type PlanDocument struct {
	SchemaVersion string     `json:"schema_version" const:"1"`
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
	Model    string  `json:"model"`
	Provider *string `json:"provider"`
}

// Finding is the inline anchor handed to independent verifiers.
type Finding struct {
	Sig      string `json:"sig"`
	Path     string `json:"path"`
	Line     any    `json:"line" type:"integer|string|null"`
	Severity any    `json:"severity" type:"string|null"`
	Kind     string `json:"kind" const:"inline"`
}

// Totals bounds the work an invoker dispatches.
type Totals struct {
	Findings   int `json:"findings" min:"0" max:"100"`
	Verifiers  int `json:"verifiers" min:"1" max:"4"`
	Dispatches int `json:"dispatches" min:"0" max:"400"`
}
