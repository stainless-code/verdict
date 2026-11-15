package core

// AuthorFile is a bundle's advisory author fingerprint; absent means unknown.
type AuthorFile struct {
	SchemaVersion string           `enum:"1"                   json:"schema_version"`
	Agent         string           `enum:"claude|codex|cursor" json:"agent"`
	Model         *string          `json:"model"`
	Confidence    string           `enum:"high|medium"         json:"confidence"`
	Evidence      []AuthorEvidence `json:"evidence"`
}

// AuthorEvidence is one matched signal.
type AuthorEvidence struct {
	Field  string `enum:"title|body|head_ref|commits" json:"field"`
	Signal string `json:"signal"`
}
