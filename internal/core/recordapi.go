package core

// RecordFlags is a placeholder for the record verb's resolved options. The
// adapter parses argv and the service receives values only; record takes no
// value flags today (only --json, which the adapter consumes).
type RecordFlags struct{}

// RecordRequest is the record service invocation: the read runtime and
// positionals, the render configuration, and the warnings the adapter
// already accumulated (prefs).
type RecordRequest struct {
	ReadRequest
	Flags     RecordFlags
	Effective Effective
	Warnings  []string
	DryRun    bool `json:"dry_run"`
	Strict    bool `json:"-"`
}

// RecordResponse carries the record verb's typed --json data profile, the
// warnings, and the envelope's next step. Human lines stream live through
// the runtime progress observer (they interleave with warnings on the
// diagnostic stream), so the response carries no rendered lines.
type RecordResponse struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Data     *RecordData
	Warnings []string
	Next     string
}
