package recordcore

import "github.com/stainless-code/verdict/internal/core"

// Flags carries the record verb's resolved options. The adapter
// parses argv and the service receives values only.
//
// CostNames asks for the cost-name repair. CostPanel carries the names to
// apply, in the order the panel ran: the flag's own explicit list, which is
// what a one-shot panel needs. An empty CostPanel with CostNames set means
// the repair reads the panel the bundle pinned, and refuses when there is
// none rather than inventing a name from a slug.
type Flags struct {
	CostPanel []string `json:"-"`
	CostNames bool     `json:"-"`
	// RequireCoverage refuses an absent or partial diff-coverage
	// attestation; --force never bypasses it.
	RequireCoverage bool
}

// Request is the record service invocation: the read runtime and
// positionals, the render configuration, and the warnings the adapter
// already accumulated (prefs).
type Request struct {
	core.ReadRequest
	Effective core.Effective
	Warnings  []string
	// RepoLayerRoot is the checkout a local bundle's freshness check reads when
	// the bundle lives somewhere else (verdict demo keeps every artifact in a
	// disposable temp dir). Empty reads RepoRoot, the shipped posture.
	RepoLayerRoot string `json:"-"`
	Flags         Flags
	DryRun        bool `json:"dry_run"`
	Strict        bool `json:"-"`
}

// Response carries the record verb's typed --json data profile, the
// warnings, and the envelope's next step. Human lines stream live through
// the runtime progress observer (they interleave with warnings on the
// diagnostic stream), so the response carries no rendered lines.
type Response struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Data     *core.RecordData
	Body     string `json:"-"`
	Warnings []string
	Next     string
}
