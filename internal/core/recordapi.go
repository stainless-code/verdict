package core

// RecordFlags carries the record verb's resolved options. The adapter
// parses argv and the service receives values only.
//
// CostNames asks for the cost-name repair. CostPanel carries the names to
// apply, in the order the panel ran: the flag's own explicit list, which is
// what a one-shot panel needs. An empty CostPanel with CostNames set means
// the repair reads the panel the bundle pinned, and refuses when there is
// none rather than inventing a name from a slug.
type RecordFlags struct {
	CostPanel []string `json:"-"`
	CostNames bool     `json:"-"`
}

// RecordRequest is the record service invocation: the read runtime and
// positionals, the render configuration, and the warnings the adapter
// already accumulated (prefs).
type RecordRequest struct {
	ReadRequest
	Effective Effective
	Warnings  []string
	Flags     RecordFlags
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
