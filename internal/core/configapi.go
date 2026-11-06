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

// ConfigRequirement is one captured requirement with its fetch-time
// status and contributing sources.
type ConfigRequirement struct {
	Key       string   `json:"key"`
	Threshold *string  `json:"threshold,omitempty"`
	Status    string   `json:"status"`
	Sources   []string `json:"sources,omitempty"`
}

// ConfigPlanGroup is one execution group: the profile, depth, lens
// budget, required checks, and file list the plan surface carries to
// the agent.
type ConfigPlanGroup struct {
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

// ConfigCheck is one observed check execution: the check, its group,
// and the latest dated verdict.
type ConfigCheck struct { //nolint:govet // field order is the published property order
	Check  string `json:"check"`
	Group  string `json:"group"`
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
}

// Wire renders the data object in wire order.
func (d *ConfigData) Wire() *jq.Obj {
	o := jq.NewObj("mode", d.Mode, "trusted", d.Trusted, "source", d.Source)
	values := make([]any, 0, len(d.Values))
	for _, v := range d.Values {
		values = append(values, jq.NewObj("key", v.Key, "value", v.Value, "layer", v.Layer, "source", v.Source))
	}
	o.Set("values", values)
	requirements := make([]any, 0, len(d.Requirements))
	for _, r := range d.Requirements {
		var threshold any
		if r.Threshold != nil {
			threshold = *r.Threshold
		}
		o := jq.NewObj("key", r.Key, "threshold", threshold, "status", r.Status)
		if len(r.Sources) > 0 {
			sources := make([]any, 0, len(r.Sources))
			for _, s := range r.Sources {
				sources = append(sources, s)
			}
			o.Set("sources", sources)
		}
		requirements = append(requirements, o)
	}
	o.Set("requirements", requirements)
	plan := make([]any, 0, len(d.Plan))
	for _, g := range d.Plan {
		plan = append(plan, wirePlanGroup(g))
	}
	o.Set("plan", plan)
	o.Set("plan_status", d.PlanStatus)
	o.Set("spend", wireSpend(d.Spend))
	privacy := make([]any, 0, len(d.Privacy))
	for _, p := range d.Privacy {
		privacy = append(privacy, p)
	}
	o.Set("privacy", privacy)
	if d.Checks != nil {
		checks := make([]any, 0, len(d.Checks))
		for _, c := range d.Checks {
			checks = append(checks, jq.NewObj("check", c.Check, "group", c.Group,
				"passed", c.Passed, "at", c.At, "detail", c.Detail))
		}
		o.Set("checks", checks)
	}
	return o
}

func wirePlanGroup(g ConfigPlanGroup) *jq.Obj {
	o := jq.NewObj("profile", g.Profile, "depth", g.Depth)
	if len(g.Checks) > 0 {
		checks := make([]any, 0, len(g.Checks))
		for _, c := range g.Checks {
			checks = append(checks, c)
		}
		o.Set("checks", checks)
	}
	if g.MaxLenses != nil {
		o.Set("max_lenses", *g.MaxLenses)
	}
	if len(g.Files) > 0 {
		files := make([]any, 0, len(g.Files))
		for _, f := range g.Files {
			files = append(files, f)
		}
		o.Set("files", files)
	}
	if len(g.Scopes) > 0 {
		scopes := make([]any, 0, len(g.Scopes))
		for _, s := range g.Scopes {
			scopes = append(scopes, s)
		}
		o.Set("scopes", scopes)
	}
	return o
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
