package core

import "github.com/stainless-code/verdict/internal/jq"

// ReportFlags carries the resolved report options: the adapter parses argv
// and the service receives values only.
type ReportFlags struct {
	Status       string `enum:"all|open|addressed|wont-fix|false-positive|deferred"`
	Kind         string `enum:"all|inline|section"`
	Category     string `enum:"logic|edge-case|security|performance|accidental-commit|style|docs|other"`
	Output       string
	Sarif        string
	Check        string `enum:"critical|important|informational"`
	Live         string `enum:"auto|strict|skip"`
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

// NextRequest is the next service invocation: the read runtime and
// positionals, the status filter, the render configuration, and the
// warnings the adapter already accumulated (prefs, carried into the report
// run unchanged).
type NextRequest struct {
	ReadRequest
	Status    string `enum:"all|open|addressed|wont-fix|false-positive|deferred"`
	Effective Effective
	Warnings  []string
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
// order), its read-only repair directive, and the envelope fields.
type ShowResponse struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Warnings []string
	Item     *FindingItem
	Fix      *ShowFix
}

// ShowFix is the per-finding repair directive emitted beside data.item on
// the show --json read path. Every field is read from the finding record or
// the bundle's patch/verification history; nothing here executes or writes.
type ShowFix struct { //nolint:govet // field order is the published property order
	Sig        string      `json:"sig"`
	Status     string      `json:"status"`
	Severity   *string     `enum:"critical|important|informational|null" json:"severity"`
	Files      []string    `json:"files"`
	Locator    string      `json:"locator"`
	Suggestion *string     `json:"suggestion"`
	Brief      *Brief      `anyOf:"null"                                 json:"brief"`
	VerifyHint *VerifyHint `anyOf:"null"                                 json:"verify_hint"`
	Caution    string      `json:"caution"`
}

// VerifyHint is the exact rerun command plus the head bracket recorded for
// this finding by verification.json or patch.json, when either carries one.
type VerifyHint struct { //nolint:govet // field order is the published property order
	VerifyCmd  string  `json:"verifyCmd"`
	ResultHead *string `json:"resultHead"`
}

// Wire renders the fix block in the published key order (sig, status,
// severity, files, locator, suggestion, brief, verify_hint, caution).
func (f *ShowFix) Wire() *jq.Obj {
	o := jq.NewObj(
		"sig", f.Sig,
		"status", f.Status,
		"severity", nullableString(f.Severity),
		"files", stringList(f.Files),
		"locator", f.Locator,
		"suggestion", nullableString(f.Suggestion),
	)
	if f.Brief == nil {
		o.Set("brief", nil)
	} else {
		o.Set("brief", f.Brief.Wire())
	}
	if f.VerifyHint == nil {
		o.Set("verify_hint", nil)
	} else {
		o.Set("verify_hint", f.VerifyHint.Wire())
	}
	o.Set("caution", f.Caution)
	return o
}

// Wire renders the rerun hint in the published key order.
func (h *VerifyHint) Wire() *jq.Obj {
	return jq.NewObj("verifyCmd", h.VerifyCmd, "resultHead", nullableString(h.ResultHead))
}

// nullableString renders a nullable string field.
func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// stringList renders a string slice as a JSON array (an empty list stays
// [] rather than null).
func stringList(values []string) any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
