package core

import (
	"github.com/stainless-code/verdict/internal/jq"
)

// ConfigFlags carries the resolved config options: exactly one of
// explain or preview. The adapter parses argv and the service receives
// values only.
type ConfigFlags struct { //nolint:govet // field order is the public invocation surface
	Explain bool
	Preview bool
	File    string
}

// ConfigRequest is the config service invocation: the read runtime and
// positionals plus the resolved options. The service performs no live
// calls, no exec, and no writes: it reads the run bundle and the
// proposed file only.
type ConfigRequest struct { //nolint:govet // field order is the public invocation surface
	ReadRequest
	Flags        ConfigFlags
	Warnings     []string
	SettingsPath string            `json:"-"`
	Env          map[string]string `json:"-"`
}

// ConfigResponse carries the config outcome: the envelope identity, the
// typed data, and the accumulated warnings.
type ConfigResponse struct { //nolint:govet // field order is the public envelope surface
	Repo     string
	Pr       string
	At       string
	Data     *ConfigData
	Warnings []string
	Next     string
}

// ConfigValue is one effective default with its provenance: the key,
// the value, and the layer and source that supplied it.
type ConfigValue struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Layer  string `json:"layer"`
	Source string `json:"source"`
}

// wireConfigFiles renders the per-file configuration in published order.
func wireConfigFiles(files []ConfigFile) []any {
	out := make([]any, 0, len(files))
	for i := range files {
		file := &files[i]
		o := jq.NewObj("path", file.Path)
		if file.OldPath != "" {
			o.Set("old_path", file.OldPath)
		}
		o.Set("scopes", wireStrings(file.Scopes))
		o.Set("values", wireValues(file.Values))
		o.Set("shadowed", wireValues(file.Shadowed))
		out = append(out, o)
	}
	return out
}

// ConfigRequirement is one captured requirement with its fetch-time
// status and contributing sources.
type ConfigRequirement struct {
	Key       string   `json:"key"`
	Threshold *string  `json:"threshold,omitempty"`
	Status    string   `json:"status"`
	Sources   []string `json:"sources,omitempty"`
}

// ConfigPlanGroup is one execution group: the stable identity, the
// explanation label, the profile, depth, lens budget, required checks, and
// file list the plan surface carries to the agent.
type ConfigPlanGroup struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Profile   string   `json:"profile"`
	Depth     string   `json:"depth"`
	Checks    []string `json:"checks,omitempty"`
	MaxLenses *int     `json:"max_lenses,omitempty"`
	Files     []string `json:"files,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
}

// ConfigSpend separates the two spend evaluations: the ordinary
// estimate guard and the personal authorization limit.
type ConfigSpend struct {
	GuardUSD             *float64 `json:"guard_usd,omitempty"`
	PersonalLimit        *float64 `json:"personal_limit_usd,omitempty"`
	RequireKnownEstimate bool     `json:"require_known_estimate"`
	EnforceCap           bool     `json:"enforce_cap"`
}

// ConfigCheck is one observed check execution: the check, its scope
// identity, the fetched head it ran against, and the latest dated verdict.
type ConfigCheck struct { //nolint:govet // field order is the published property order
	Check  string `json:"check"`
	Group  string `json:"group"`
	Head   string `json:"head"`
	Passed bool   `json:"passed"`
	At     string `json:"at"`
	Detail string `json:"detail,omitempty"`
}

// ConfigData is the config verb's --json data profile: the mode, the
// trust bit, the source, the effective values with origins, the
// requirements, the pending plan, the spend views, the privacy
// exclusions, and the observed check outcomes on explain.
type ConfigData struct { //nolint:govet // field order is the published property order
	Mode         string              `enum:"explain|preview"  json:"mode"`
	Trusted      bool                `json:"trusted"`
	Source       string              `json:"source"`
	Values       []ConfigValue       `json:"values"`
	Requirements []ConfigRequirement `json:"requirements"`
	Plan         []ConfigPlanGroup   `json:"plan"`
	PlanStatus   string              `json:"plan_status"`
	Spend        ConfigSpend         `json:"spend"`
	Privacy      []string            `json:"privacy"`
	Checks       []ConfigCheck       `json:"checks,omitempty"`
	// Shadowed lists the losing layer values the capture recorded for the
	// root defaults, so an override is never invisible.
	Shadowed []ConfigValue `json:"shadowed,omitempty"`
	// Files carries each reviewed path's effective configuration: the scopes
	// that shaped it, its winning values with origins, and the values they
	// overrode.
	Files []ConfigFile `json:"files,omitempty"`
}

// ConfigFile is one reviewed path's effective configuration. Values and
// Shadowed use the same key/value/layer/source shape as the root defaults, so
// a caller reads one vocabulary at both scopes.
type ConfigFile struct {
	Path     string        `json:"path"`
	OldPath  string        `json:"old_path,omitempty"`
	Scopes   []string      `json:"scopes"`
	Values   []ConfigValue `json:"values"`
	Shadowed []ConfigValue `json:"shadowed"`
}

// Wire renders the data object in wire order.
func (d *ConfigData) Wire() *jq.Obj {
	o := jq.NewObj("mode", d.Mode, "trusted", d.Trusted, "source", d.Source)
	o.Set("values", wireValues(d.Values))
	o.Set("requirements", wireRequirements(d.Requirements))
	plan := make([]any, 0, len(d.Plan))
	for i := range d.Plan {
		plan = append(plan, wirePlanGroup(&d.Plan[i]))
	}
	o.Set("plan", plan)
	o.Set("plan_status", d.PlanStatus)
	o.Set("spend", wireSpend(d.Spend))
	o.Set("privacy", wireStrings(d.Privacy))
	if d.Checks != nil {
		o.Set("checks", wireChecks(d.Checks))
	}
	if d.Shadowed != nil {
		o.Set("shadowed", wireValues(d.Shadowed))
	}
	if d.Files != nil {
		o.Set("files", wireConfigFiles(d.Files))
	}
	return o
}

// wireValues renders the effective-value rows in capture order.
func wireValues(values []ConfigValue) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, jq.NewObj("key", v.Key, "value", v.Value, "layer", v.Layer, "source", v.Source))
	}
	return out
}

// wireRequirements renders the requirement rows; sources appear only when the
// capture recorded them.
func wireRequirements(reqs []ConfigRequirement) []any {
	out := make([]any, 0, len(reqs))
	for _, r := range reqs {
		var threshold any
		if r.Threshold != nil {
			threshold = *r.Threshold
		}
		row := jq.NewObj("key", r.Key, "threshold", threshold, "status", r.Status)
		setWireList(row, "sources", r.Sources)
		out = append(out, row)
	}
	return out
}

// wireChecks renders the check observations in execution order.
func wireChecks(checks []ConfigCheck) []any {
	out := make([]any, 0, len(checks))
	for _, c := range checks {
		out = append(out, jq.NewObj("check", c.Check, "group", c.Group, "head", c.Head,
			"passed", c.Passed, "at", c.At, "detail", c.Detail))
	}
	return out
}

// wirePlanGroup renders one plan group: identity first, then the optional
// checks, lens cap, files, and scopes it carries.
func wirePlanGroup(g *ConfigPlanGroup) *jq.Obj {
	o := jq.NewObj("id", g.ID, "label", g.Label, "profile", g.Profile, "depth", g.Depth)
	setWireList(o, "checks", g.Checks)
	if g.MaxLenses != nil {
		o.Set("max_lenses", *g.MaxLenses)
	}
	setWireList(o, "files", g.Files)
	setWireList(o, "scopes", g.Scopes)
	return o
}

// setWireList sets a list key only when the list is non-empty, which keeps an
// absent optional property out of the wire instead of emitting an empty array.
func setWireList[T any](o *jq.Obj, key string, items []T) {
	if len(items) == 0 {
		return
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	o.Set(key, out)
}

// wireStrings renders a string list in order.
func wireStrings(items []string) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func wireSpend(s ConfigSpend) *jq.Obj {
	var guard, limit any
	if s.GuardUSD != nil {
		guard = *s.GuardUSD
	}
	if s.PersonalLimit != nil {
		limit = *s.PersonalLimit
	}
	return jq.NewObj("guard_usd", guard, "personal_limit_usd", limit,
		"require_known_estimate", s.RequireKnownEstimate, "enforce_cap", s.EnforceCap)
}
