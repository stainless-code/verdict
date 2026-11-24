package postcore

import "github.com/stainless-code/verdict/internal/core"

// Flags carries the resolved post options: the adapter parses argv and
// the service receives values only.
type Flags struct {
	Approve     string `enum:"true|false"`
	MinSeverity string `enum:"critical|important|informational|off"`
	ReviewFile  string
	// Summary opts into maintaining one separate PR-level summary comment
	// (comment) updated in place across posts, or keeps it disabled (off).
	Summary        string `enum:"off|comment"`
	RequestChanges bool
	Bind           bool
	Force          bool
	DryRun         bool
	// RequireCoverage refuses an absent or partial diff-coverage
	// attestation; --force never bypasses it.
	RequireCoverage bool
	// AllowSecretValues publishes a review whose text carries a
	// secret-shaped value: the pre-submit scan refuses
	// post-secret-shaped without it, and the receipt records what it let
	// through.
	AllowSecretValues bool
	Provenance        bool
}

// Request is the post service invocation: the read runtime and
// positionals, the resolved options, the render configuration, the JSON-mode
// flag, and the warnings the adapter already accumulated (prefs).
type Request struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	Flags Flags
	// RequireRigor names the per-tier evidence floors this invocation
	// demands (`critical=walked`). A named tier also gates: below-floor
	// findings refuse rigor-below-floor unless --force records the bypass.
	RequireRigor []string
	Effective    core.Effective
	JSONMode     bool
	Warnings     []string
	// PinnedContentHash is the content digest a previewed review rendered to
	// (the dry-run's RenderedHash). A real post whose own settled body,
	// comments, and head digest differently refuses post-content-changed
	// before any host call, so the posted bytes are the ones that were
	// verified even if review.json changed after the preview read it. Empty
	// pins nothing: the CLI never sets it.
	PinnedContentHash string
}

// Response carries the post outcome: the envelope identity, the typed
// data, and the accumulated warnings. Human lines stream live through the
// runtime progress observer, so the response carries no rendered lines.
type Response struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Data     *core.PostData
	Warnings []string
	Next     string
}
