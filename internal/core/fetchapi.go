package core

import (
	"github.com/stainless-code/verdict/internal/jq"
)

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
	Receipt         *Truncation `json:"-" receipt:"true"`
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
	Preview         *PreviewData
	PromptDump      *string `json:"-"`
}

// Wire renders the data object in wire order: estimate/budget travel
// together when a budget is set, cap and chunk follow only when armed, and
// the prompt dump lands last.
func (d *FetchData) Wire() *jq.Obj {
	// A preview writes no bundle, so its data is the preview payload alone:
	// every other key on this profile describes artifacts that do not exist.
	if o, ok := d.previewOnly(); ok {
		return o
	}
	return d.bundleWire()
}

// bundleWire renders the bundle profile's keys in published order.
func (d *FetchData) bundleWire() *jq.Obj {
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
	return d.Receipt.Apply(o)
}

// previewOnly reports the preview payload as the whole data object; a run that
// wrote a bundle reports false.
func (d *FetchData) previewOnly() (*jq.Obj, bool) {
	if d.Preview == nil {
		return nil, false
	}
	return jq.NewObj("preview", d.Preview.Wire()), true
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
