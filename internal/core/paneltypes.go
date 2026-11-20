package core

// Verification is independent model evidence, separate from executable proof
// claims.
type Verification struct {
	Checker  string `json:"checker,omitempty"`
	Model    string `json:"model,omitempty"                minLength:"1"`
	Provider string `json:"provider,omitempty"`
	Verdict  string `enum:"confirmed|refuted|insufficient" json:"verdict"`
	Evidence string `json:"evidence"                       maxLength:"500" minLength:"10"`
	At       string `json:"at,omitempty"`
}

// PanelSummary counts derived confidence without voting or withholding
// findings.
type PanelSummary struct {
	Finders   *FinderSummary `anyOf:"null"     json:"finders,omitempty"`
	Verifiers int            `json:"verifiers" min:"0"`
	Confirmed int            `json:"confirmed" min:"0"`
	Contested int            `json:"contested" min:"0"`
	Single    int            `json:"single"    min:"0"`
}

// FinderSummary counts completed discovery and directed cross-check outcomes.
type FinderSummary struct {
	Completed int `json:"completed" max:"2" min:"0"`
	Confirmed int `json:"confirmed" min:"0"`
	Contested int `json:"contested" min:"0"`
	Pending   int `json:"pending"   min:"0"`
}
