package fetchcore

import (
	"io"

	"github.com/stainless-code/verdict/internal/core"
)

// Flags carries the resolved fetch options: the adapter parses argv
// and the service receives values only.
type Flags struct {
	BaseSHA  string
	Provider string `enum:"github|gitlab|gitcafe|forgejo|bitbucket|azure"`
	// Index is the operator's explicit index hand-off (a ref or a directory);
	// empty means no impact section is written.
	Index             string
	Context           []string
	Spec              []string
	Issue             []string
	Ignore            []string
	NoDefaultExcludes bool
	// Preview computes the diff file list, applies every exclusion, and
	// resolves the review budget without writing a bundle or launching an
	// agent.
	Preview       bool
	BudgetConsent bool
	ShowPrompts   bool
	ChunkFlag     bool
	HeadControls  bool
	HasBase       bool `json:"-"`
	HasProvider   bool `json:"-"`
	// PublicRead reads a public GitHub pull request over the anonymous REST
	// API (curl) instead of gh. Adapter-injected: no CLI flag, no MCP field,
	// no schema; verdict demo is its only caller.
	PublicRead bool `json:"-"`
	// NoRepoLayer records that the run has no checkout it may trust, so the
	// repository control and configuration layers are absent by construction.
	// Adapter-injected like PublicRead; the snapshot it writes is honest about
	// it (`policy: base`, empty inventory).
	NoRepoLayer bool `json:"-"`
}

// Request is the fetch service invocation: the read runtime and
// positionals, the resolved options, the render configuration, the JSON-mode
// flag, the warnings the adapter already accumulated (prefs), and the human
// and diagnostic streams the adapter routes (the prompt dump owns stdout
// under --show-prompts, so the service never touches [os.Stdout] itself).
// SettingsPath is the operator settings file the personal layer reads
// (empty reads no personal layer).
type Request struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	Flags Flags
	// SettingsPath is adapter-injected (HOME/XDG_CONFIG_HOME), never a
	// transport argument: MCP/JSON callers cannot set operator paths.
	SettingsPath string `json:"-"`
	Effective    core.Effective
	JSONMode     bool
	Warnings     []string
	Human        io.Writer
	Diag         io.Writer
}

// Response carries the fetch outcome: the envelope identity, the typed
// data, and the accumulated warnings. Human lines stream live to the
// request's Human writer (they interleave with warnings on the diagnostic
// stream), so the response carries no rendered lines; the prompt dump rides
// in Data.PromptDump for the adapter to own on stdout (human mode) or in
// the envelope (JSON mode).
type Response struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Data     *core.FetchData
	Warnings []string
	Next     string
}

// LocalFlags carries the resolved fetch-local options.
type LocalFlags struct {
	RangeSpec string
	BaseRef   string
	StashN    string
	// RepoLayerRoot is the checkout the run's base-commit reads run in when the
	// bundle is written somewhere else (verdict demo keeps every artifact in a
	// disposable temp dir, but the base controls and policy still come from the
	// operator's checkout). Empty reads RepoRoot, the shipped posture.
	RepoLayerRoot string `json:"-"`
	// Index is the operator's explicit index hand-off, exactly as fetch takes
	// it; empty means no impact section is written.
	Index   string
	Context []string
	Spec    []string
	Ignore  []string
	Paths   []string
	// Scan selects files rather than a git change: each operand is a file or
	// directory inside the repository, and the bundle reviews their contents as
	// added files. It needs no diff and is mutually exclusive with every diff
	// source.
	Scan              []string
	Staged            bool
	HasStash          bool `json:"-"`
	Worktree          bool
	BudgetConsent     bool
	ShowPrompts       bool
	ChunkFlag         bool
	HeadControls      bool
	NoDefaultExcludes bool
	// Preview computes the diff file list, applies every exclusion, and
	// resolves the review budget without writing a bundle or launching an
	// agent.
	Preview bool
}

// LocalRequest is the fetch-local service invocation. SettingsPath
// carries the operator settings file like the fetch request.
type LocalRequest struct { //nolint:govet // field grouping documents the request surface
	core.ReadRequest
	Flags LocalFlags
	// SettingsPath is adapter-injected like the fetch request.
	SettingsPath string `json:"-"`
	Effective    core.Effective
	JSONMode     bool
	Warnings     []string
	Human        io.Writer
	Diag         io.Writer
}

// Presences lists the presence bits JSON transports derive for fetch.
func (f *Flags) Presences() []core.Presence {
	return []core.Presence{
		{Name: "HasBase", Value: f.BaseSHA, Flag: &f.HasBase},
		{Name: "HasProvider", Value: f.Provider, Flag: &f.HasProvider},
	}
}

// Presences lists the presence bits JSON transports derive for fetch-local.
func (f *LocalFlags) Presences() []core.Presence {
	return []core.Presence{
		{Name: "HasStash", Value: f.StashN, Flag: &f.HasStash},
	}
}
