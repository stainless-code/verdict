package core

// ReportFlags carries the resolved report options: the adapter parses argv
// and the service receives values only.
type ReportFlags struct {
	Status       string
	Kind         string
	Output       string
	Sarif        string
	Check        string
	Live         string
	Cost         bool
	Completeness bool
}

// ReportRequest is the report service invocation: the read runtime and
// positionals, the resolved options, the render configuration, and the
// warnings the adapter already accumulated (prefs).
type ReportRequest struct {
	ReadRequest
	Flags     ReportFlags
	Effective Effective
	Warnings  []string
}

// FileArtifact is one requested output file the adapter must write after a
// successful service call. Bytes are the contract.
type FileArtifact struct {
	Path    string
	Content []byte
}

// ReportResponse carries the report's typed data, warnings, and pure render
// inputs. Human is the human-mode summary (one line per element, no
// trailing newline), Next is the envelope's next field, CheckLines are the
// gate diagnostics written after output, and the artifacts are returned for
// the adapter to write.
type ReportResponse struct { //nolint:govet // field grouping documents the response surface
	Repo       string
	Pr         string
	At         string
	Data       *ReportData
	Warnings   []string
	Next       string
	Human      []string
	CheckLines []string
	GateFailed bool
	Output     *FileArtifact
	Sarif      *FileArtifact
}

// NextRequest is the next service invocation. ReportWarnings seeds the
// internal report run (the shell's re-invocation re-resolved prefs and
// duplicated their warnings, so the adapter passes the replayed list).
type NextRequest struct {
	ReadRequest
	Status         string
	Effective      Effective
	Warnings       []string
	ReportWarnings []string
}

// NextResponse carries the top actionable finding or the unreadable-store
// classification. ReportRefusal is non-nil when the underlying report
// refused; its envelope (verb "report") is what the CLI prints in JSON mode.
type NextResponse struct { //nolint:govet // field grouping documents the response surface
	Repo            string
	Pr              string
	At              string
	Status          string
	Warnings        []string
	Item            *FindingItem
	StoreUnreadable bool
	ReportRefusal   *RefusalError
	Human           string
}

// ShowRequest is the show service invocation.
type ShowRequest struct {
	ReadRequest
	Sig      string
	Warnings []string
}

// ShowResponse carries one full finding record (Raw keeps the store key
// order) and the envelope fields.
type ShowResponse struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Warnings []string
	Item     *FindingItem
}
