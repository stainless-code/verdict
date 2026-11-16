package core

// SelectedFile is a bundle's reaction-scoped fix selection: the finding sigs
// whose posted root review comment carries a human 👍. The file exists only
// when at least one sig was selected — absence is "no claim", never an empty
// scope.
type SelectedFile struct {
	SchemaVersion string   `enum:"1"           json:"schema_version"`
	Repo          string   `json:"repo"`
	Pr            string   `json:"pr"`
	HeadSHA       string   `json:"head_sha"`
	SelectedAt    string   `json:"selected_at"`
	Sigs          []string `json:"sigs"`
}
