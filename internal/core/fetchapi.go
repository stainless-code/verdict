package core

import (
	"io"

	"github.com/stainless-code/verdict/internal/jq"
)

// FetchFlags carries the resolved fetch options: the adapter parses argv
// and the service receives values only.
type FetchFlags struct {
	BaseSHA  string
	Provider string `enum:"github|gitlab|gitcafe|forgejo|bitbucket|azure"`
	// Index is the operator's explicit index hand-off (a ref or a directory);
	// empty means no impact section is written.
	Index             string
	Context           []string
	Ignore            []string
	NoDefaultExcludes bool
	BudgetConsent     bool
	ShowPrompts       bool
	ChunkFlag         bool
	HeadControls      bool
	HasBase           bool `json:"-"`
	HasProvider       bool `json:"-"`
}

// Presences lists the presence bits JSON transports derive for fetch.
func (f *FetchFlags) Presences() []Presence {
	return []Presence{
		{Name: "HasBase", Value: f.BaseSHA, Flag: &f.HasBase},
		{Name: "HasProvider", Value: f.Provider, Flag: &f.HasProvider},
	}
}

// FetchLocalFlags carries the resolved fetch-local options.
type FetchLocalFlags struct {
	RangeSpec string
	BaseRef   string
	StashN    string
	// Index is the operator's explicit index hand-off, exactly as fetch takes
	// it; empty means no impact section is written.
	Index             string
	Context           []string
	Ignore            []string
	Paths             []string
	Staged            bool
	HasStash          bool `json:"-"`
	Worktree          bool
	BudgetConsent     bool
	ShowPrompts       bool
	ChunkFlag         bool
	HeadControls      bool
	NoDefaultExcludes bool
}

// Presences lists the presence bits JSON transports derive for fetch-local.
func (f *FetchLocalFlags) Presences() []Presence {
	return []Presence{
		{Name: "HasStash", Value: f.StashN, Flag: &f.HasStash},
	}
}

// FetchRequest is the fetch service invocation: the read runtime and
// positionals, the resolved options, the render configuration, the JSON-mode
// flag, the warnings the adapter already accumulated (prefs), and the human
// and diagnostic streams the adapter routes (the prompt dump owns stdout
// under --show-prompts, so the service never touches [os.Stdout] itself).
// SettingsPath is the operator settings file the personal layer reads
// (empty reads no personal layer).
type FetchRequest struct { //nolint:govet // field grouping documents the request surface
	ReadRequest
	Flags FetchFlags
	// SettingsPath is adapter-injected (HOME/XDG_CONFIG_HOME), never a
	// transport argument: MCP/JSON callers cannot set operator paths.
	SettingsPath string `json:"-"`
	Effective    Effective
	JSONMode     bool
	Warnings     []string
	Human        io.Writer
	Diag         io.Writer
}

// FetchLocalRequest is the fetch-local service invocation. SettingsPath
// carries the operator settings file like the fetch request.
type FetchLocalRequest struct { //nolint:govet // field grouping documents the request surface
	ReadRequest
	Flags FetchLocalFlags
	// SettingsPath is adapter-injected like the fetch request.
	SettingsPath string `json:"-"`
	Effective    Effective
	JSONMode     bool
	Warnings     []string
	Human        io.Writer
	Diag         io.Writer
}

// FetchResponse carries the fetch outcome: the envelope identity, the typed
// data, and the accumulated warnings. Human lines stream live to the
// request's Human writer (they interleave with warnings on the diagnostic
// stream), so the response carries no rendered lines; the prompt dump rides
// in Data.PromptDump for the adapter to own on stdout (human mode) or in
// the envelope (JSON mode).
type FetchResponse struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Data     *FetchData
	Warnings []string
	Next     string
}

// FetchPanel is the fetch envelope's panel summary: the pinned source plus
// the member list.
type FetchPanel struct {
	Source  *string
	Members []string
}

// Wire renders the panel summary in wire order.
func (p FetchPanel) Wire() *jq.Obj {
	members := make([]any, 0, len(p.Members))
	for _, m := range p.Members {
		members = append(members, m)
	}
	var source any
	if p.Source != nil {
		source = *p.Source
	}
	return jq.NewObj("source", source, "members", members)
}

// FetchData is the fetch/fetch-local verb's --json data profile. The shape
// is open (fetch has no published data schema; only the envelope is
// schema-bound), but the scalars are typed and the conditional summaries
// (estimate, budget, cap, chunk, artifacts, prompt dump) ride as raw
// ordered passthrough. Field order is the published order:
// the local verb emits kind second and no incremental_base, while fetch
// emits incremental_base last among the fixed keys.
type FetchData struct { //nolint:govet // field order is the published property order
	Sources         []string
	Local           bool `json:"-"`
	Dir             string
	Kind            string `json:"-"`
	HeadSHA         string
	BaseSHA         string
	Files           any
	Additions       any
	Deletions       any
	Hunks           int
	Omitted         []any
	RawDiffBytes    *int64
	DiffBytes       int64
	Panel           FetchPanel
	Capabilities    *jq.Obj `json:"-"`
	Controls        *jq.Obj `json:"-"`
	Artifacts       *jq.Obj `json:"-"`
	IncrementalBase *string `json:"-"`
	Estimate        any     `json:"-"`
	Budget          any     `json:"-"`
	Cap             any     `json:"-"`
	Chunk           any     `json:"-"`
	PromptDump      *string `json:"-"`
}

// Wire renders the data object in wire order: estimate/budget travel
// together when a budget is set, cap and chunk follow only when armed, and
// the prompt dump lands last.
func (d *FetchData) Wire() *jq.Obj {
	o := jq.NewObj("dir", d.Dir)
	if d.Local {
		o.Set("kind", d.Kind)
	}
	o.Set("head_sha", d.HeadSHA)
	o.Set("base_sha", d.BaseSHA)
	o.Set("files", d.Files)
	o.Set("additions", d.Additions)
	o.Set("deletions", d.Deletions)
	o.Set("hunks", d.Hunks)
	omitted := d.Omitted
	if omitted == nil {
		omitted = []any{}
	}
	o.Set("omitted", omitted)
	o.Set("diff_bytes", d.DiffBytes)
	d.wireRawDiff(o)
	o.Set("panel", d.Panel.Wire())
	if d.Capabilities != nil {
		o.Set("capabilities", d.Capabilities)
	}
	if d.Controls != nil {
		o.Set("controls", d.Controls)
	}
	o.Set("artifacts", d.Artifacts)
	if len(d.Sources) > 0 {
		sources := make([]any, 0, len(d.Sources))
		for _, source := range d.Sources {
			sources = append(sources, source)
		}
		o.Set("sources", sources)
	}
	d.wireTail(o)
	return o
}

// wireCosts renders the cost summary pair: the estimate whenever one exists (a
// grouped run has one with no ordinary budget) and the ordinary budget only
// when the run resolved one.
func (d *FetchData) wireCosts(o *jq.Obj) {
	if d.Estimate != nil {
		o.Set("estimate", d.Estimate)
	}
	if d.Budget != nil {
		o.Set("budget", d.Budget)
	}
}

// wireTail renders the conditional tail: the incremental base on the fetch
// verb only, the estimate whenever one exists (a grouped run has one without an
// ordinary budget) and the budget only when a budget set one, the cap and chunk
// summaries only when armed, and the prompt dump last when staged.
func (d *FetchData) wireTail(o *jq.Obj) {
	if !d.Local {
		var base any
		if d.IncrementalBase != nil {
			base = *d.IncrementalBase
		}
		o.Set("incremental_base", base)
	}
	d.wireCosts(o)
	if d.Cap != nil {
		o.Set("cap", d.Cap)
	}
	if d.Chunk != nil {
		o.Set("chunk", d.Chunk)
	}
	if d.PromptDump != nil {
		o.Set("prompt_dump", *d.PromptDump)
	}
}

func (d *FetchData) wireRawDiff(o *jq.Obj) {
	if d.RawDiffBytes != nil {
		o.Set("raw_diff_bytes", *d.RawDiffBytes)
	}
}
