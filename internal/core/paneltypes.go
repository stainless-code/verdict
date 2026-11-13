package core

// PanelVerification is independent model evidence, separate from executable proof claims.
type PanelVerification struct {
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
	Verdict  string `json:"verdict" enum:"confirmed|refuted|insufficient"`
	Evidence string `json:"evidence" minLength:"10" maxLength:"500"`
	At       string `json:"at,omitempty"`
}

// VerificationSummary counts derived confidence without voting or withholding findings.
type VerificationSummary struct {
	Verifiers int `json:"verifiers" min:"0"`
	Confirmed int `json:"confirmed" min:"0"`
	Contested int `json:"contested" min:"0"`
	Single    int `json:"single" min:"0"`
}
