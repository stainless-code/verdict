package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func mustParse(t *testing.T, give string) *core.Config {
	t.Helper()
	cfg, ref := Parse([]byte(give))
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	return cfg
}

func flagPanel(models ...string) Partial {
	return Partial{Panel: models, PanelSet: true}
}

func resolveMust(t *testing.T, in *Inputs) *Resolved {
	t.Helper()
	res, ref := Resolve(in)
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	return res
}

// precedenceFixture builds the flag/env/repo/personal cascade input.
func precedenceFixture(t *testing.T) *Inputs {
	t.Helper()
	cfg := mustParse(t, `{"schema_version":"1","defaults":{"panel":["repo-model"],"max_bytes":100}}`)
	maxBytes := int64(50)
	personal := &PersonalSettings{Review: Partial{
		Panel: []string{"personal-model"}, PanelSet: true, MaxBytes: &maxBytes,
	}}
	env, _, ref := ParseEnv(map[string]string{"REVIEW_PANEL": "env-model"})
	if ref != nil {
		t.Fatalf("ParseEnv: %v", ref)
	}
	return &Inputs{Flags: flagPanel("flag-model"), Env: env, Repo: cfg, Personal: personal}
}

// panelShadowed collects the losing panel sources in sorted order.
func panelShadowed(res *Resolved) []string {
	var shadowed []string
	for _, s := range res.Defaults.Shadowed {
		if s.Key == "panel" {
			shadowed = append(shadowed, s.Layer+"/"+s.Source)
		}
	}
	slices.Sort(shadowed)
	return shadowed
}

// TestResolveDefaultsPrecedence pins the cascade: flags beat environment,
// which beats repository, personal, and built-ins.
func TestResolveDefaultsPrecedence(t *testing.T) {
	res := resolveMust(t, precedenceFixture(t))
	if got := res.Defaults.Values.Panel; !slices.Equal(got, []string{"flag-model"}) {
		t.Fatalf("panel = %v", got)
	}
	if origin := res.Defaults.Origins["panel"]; origin.Layer != LayerFlag {
		t.Fatalf("panel origin = %+v", origin)
	}
	if res.Defaults.Values.MaxBytes != 100 {
		t.Fatalf("max_bytes = %d, want repo 100", res.Defaults.Values.MaxBytes)
	}
	if res.Defaults.Values.KBMinJudged != BuiltinKBMinJudged {
		t.Fatalf("kb min_judged = %d, want built-in %d", res.Defaults.Values.KBMinJudged, BuiltinKBMinJudged)
	}
}

// TestResolveShadowed pins the shadowing record: every losing panel value
// is reported with its layer and source.
func TestResolveShadowed(t *testing.T) {
	res := resolveMust(t, precedenceFixture(t))
	want := []string{"env/REVIEW_PANEL", "personal/operator settings", "repo/.verdict/config.json"}
	if got := panelShadowed(res); !slices.Equal(got, want) {
		t.Fatalf("panel shadowed = %v", got)
	}
}

// TestResolveNestedMerge pins per-field merging of nested objects:
// repository and personal each supply one kb_policy sub-field.
func TestResolveNestedMerge(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1","defaults":{"kb_policy":{"min_judged":3}}}`)
	rate := 0.7
	personal := &PersonalSettings{Review: Partial{KBMinJudged: nil, KBMaxFPRate: &rate, KBPolicySet: true}}
	res, ref := Resolve(&Inputs{Repo: cfg, Personal: personal})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if res.Defaults.Values.KBMinJudged != 3 {
		t.Fatalf("min_judged = %d", res.Defaults.Values.KBMinJudged)
	}
	if res.Defaults.Values.KBMaxFPRate != 0.7 {
		t.Fatalf("max_fp_rate = %v", res.Defaults.Values.KBMaxFPRate)
	}
	if res.Defaults.Origins["kb_policy.min_judged"].Layer != LayerRepo {
		t.Fatalf("min_judged origin = %+v", res.Defaults.Origins["kb_policy.min_judged"])
	}
	if res.Defaults.Origins["kb_policy.max_fp_rate"].Layer != LayerPersonal {
		t.Fatalf("max_fp_rate origin = %+v", res.Defaults.Origins["kb_policy.max_fp_rate"])
	}
}

// TestResolveArraysReplace pins whole-array replacement: the winning
// panel replaces instead of merging.
func TestResolveArraysReplace(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1","defaults":{"panel":["a","b"]}}`)
	personal := &PersonalSettings{Review: Partial{Panel: []string{"c"}, PanelSet: true}}
	res, ref := Resolve(&Inputs{Repo: cfg, Personal: personal})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if got := res.Defaults.Values.Panel; !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("panel = %v", got)
	}
}

// TestResolveNoInit pins the no-init rule: without base configuration the
// run follows built-in defaults with pending status and never refuses.
func TestResolveNoInit(t *testing.T) {
	res, ref := Resolve(&Inputs{})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if res.Defaults.Values.Effort != BuiltinEffort || res.Defaults.Values.MaxBytes != BuiltinMaxBytes {
		t.Fatalf("defaults = %+v", res.Defaults.Values)
	}
	if res.Plan.Status != PendingStatus || len(res.Plan.Groups) != 0 {
		t.Fatalf("plan = %+v", res.Plan)
	}
}

// TestResolveRequirementOnlyScope pins the friction derivation: a
// requirement-only scope adds another check, and requirements union it
// into the planned checks instead of refusing.
func TestResolveRequirementOnlyScope(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1",
		"requirements":{"block_clauses":[{"name":"root-security","threshold":"important","categories":["security"],"paths":["**"]}],
			"require_checks":["no-open-above-block","head-current"]},
		"scopes":[{"name":"payments-strict","include":["payments/**"],"requirements":{"require_checks":["no-live-threads"]}}],
		"profiles":{"standard-review":{"depth":"standard","checks":["no-open-above-block","head-current"],"max_lenses":2}},
		"default_profile":"standard-review"}`)
	res, ref := Resolve(&Inputs{Repo: cfg, BaseSHA: strings.Repeat("a", 40), Files: []FileInput{{Path: "payments/api.go"}}})
	if ref != nil {
		t.Fatalf("Resolve refused the auto-union: %v", ref)
	}
	if len(res.Plan.Groups) != 1 {
		t.Fatalf("groups = %+v", res.Plan.Groups)
	}
	group := res.Plan.Groups[0]
	for _, want := range []string{"no-open-above-block", "head-current", "no-live-threads"} {
		if !slices.Contains(group.Checks, want) {
			t.Fatalf("planned checks = %v, missing %q", group.Checks, want)
		}
	}
	if group.Profile != "standard-review" || group.Depth != "standard" {
		t.Fatalf("group = %+v", group)
	}
}

// TestResolvePerGroupValidation pins the clause half of the rule: a group
// with the findings check but no applicable clause refuses
// bad-requirement naming the group.
func TestResolvePerGroupValidation(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1",
		"requirements":{"block_clauses":[{"name":"elsewhere","threshold":"critical","paths":["b/**"]}],
			"require_checks":["no-open-above-block"]},
		"profiles":{"standard-review":{"depth":"standard","checks":["no-open-above-block"],"max_lenses":2}},
		"default_profile":"standard-review"}`)
	_, ref := Resolve(&Inputs{Repo: cfg, Files: []FileInput{{Path: "a.go"}}})
	requireRefusal(t, ref, CodeBadRequirement, "plan group 1")
}

// TestResolvePerSourceClauses pins conjunction across sources with
// categories intact: the root security clause and the scope logic clause
// stay separate predicates on their own paths, and files outside the
// scope see only the root clause.
func TestResolvePerSourceClauses(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1",
		"requirements":{"block_clauses":[{"name":"root-security","threshold":"important","categories":["security"],"paths":["**"]}]},
		"scopes":[{"name":"payments-strict","include":["payments/**"],
			"requirements":{"block_clauses":[{"name":"payments-logic","threshold":"critical","categories":["logic"],"paths":["payments/**"]}]}}]}`)
	res, ref := Resolve(&Inputs{Repo: cfg, Files: []FileInput{{Path: "payments/api.go"}, {Path: "api/server.go"}}})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if len(res.Files) != 2 {
		t.Fatalf("files = %+v", res.Files)
	}
	if len(res.Files[0].Requirements.Clauses) != 2 {
		t.Fatalf("payments clauses = %+v", res.Files[0].Requirements.Clauses)
	}
	if res.Files[0].Requirements.Clauses[1].Clause.Categories[0] != "logic" {
		t.Fatalf("scope clause = %+v", res.Files[0].Requirements.Clauses[1])
	}
	if len(res.Files[1].Requirements.Clauses) != 1 || res.Files[1].Requirements.Clauses[0].Clause.Name != "root-security" {
		t.Fatalf("server clauses = %+v", res.Files[1].Requirements.Clauses)
	}
}

// TestResolveRename pins old-path matching: a rename applies the clauses
// of both the old and the new path, with provenance retained.
func TestResolveRename(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1",
		"requirements":{"block_clauses":[{"name":"root-any","threshold":"informational","paths":["**"]}]},
		"scopes":[{"name":"legacy","include":["old/**"],"requirements":{"block_clauses":[{"name":"legacy-rule","threshold":"critical","paths":["old/**"]}]}}]}`)
	file := FileInput{Path: "new/x.go", OldPath: "old/x.go"}
	res, ref := Resolve(&Inputs{Repo: cfg, BaseSHA: strings.Repeat("b", 40), Files: []FileInput{file}})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	clauses := res.Files[0].Requirements.Clauses
	if len(clauses) != 2 {
		t.Fatalf("clauses = %+v", clauses)
	}
	if !slices.Contains(clauses[1].Sources, "scope legacy (old path old/x.go)") {
		t.Fatalf("legacy sources = %v", clauses[1].Sources)
	}
	if !slices.Contains(res.Files[0].Scopes, "legacy") {
		t.Fatalf("scopes = %v", res.Files[0].Scopes)
	}
}

// TestResolveBindOR pins require_bind accumulation: any applicable true
// requires binding, while false adds nothing and never contradicts true.
func TestResolveBindOR(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1",
		"requirements":{"require_bind":false},
		"scopes":[{"name":"strict","include":["payments/**"],"requirements":{"require_bind":true}}]}`)
	res, ref := Resolve(&Inputs{Repo: cfg, Files: []FileInput{{Path: "payments/a.go"}, {Path: "other.go"}}})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if !res.Files[0].Requirements.RequireBind {
		t.Fatalf("payments bind = %+v", res.Files[0].Requirements)
	}
	if !slices.Contains(res.Files[0].Requirements.BindSources, "scope strict") {
		t.Fatalf("bind sources = %v", res.Files[0].Requirements.BindSources)
	}
	if res.Files[1].Requirements.RequireBind {
		t.Fatalf("unscoped bind must stay false: %+v", res.Files[1].Requirements)
	}
}

// spendFixture builds the repo-guard plus personal-limit input.
func spendFixture(t *testing.T) *Inputs {
	t.Helper()
	cfg := mustParse(t, `{"schema_version":"1","defaults":{"budget_usd":5.0}}`)
	limit := 3.0
	personal := &PersonalSettings{
		Spend:   PersonalSpend{BudgetUSD: &limit, RequireKnownEstimate: true, EnforceCap: true},
		Privacy: PersonalPrivacy{Exclusions: []string{"hr/**"}},
	}
	return &Inputs{Repo: cfg, Personal: personal}
}

// TestResolveSpend pins the constraint split: the repository budget feeds
// the estimate guard while the personal limit constrains beside it, and
// the repository can never raise that limit.
func TestResolveSpend(t *testing.T) {
	res := resolveMust(t, spendFixture(t))
	if res.Spend.GuardUSD == nil || *res.Spend.GuardUSD != 5.0 {
		t.Fatalf("guard = %+v", res.Spend)
	}
	if res.Spend.PersonalLimit == nil || *res.Spend.PersonalLimit != 3.0 {
		t.Fatalf("limit = %+v", res.Spend)
	}
	if !res.Spend.RequireKnownEstimate || !res.Spend.EnforceCap {
		t.Fatalf("spend = %+v", res.Spend)
	}
}

// TestResolvePrivacy pins the exclusion constraint: matching paths are
// withheld from model-facing exports without touching raw identity.
func TestResolvePrivacy(t *testing.T) {
	res := resolveMust(t, spendFixture(t))
	if !res.Privacy.Excluded("hr/salaries.md") || res.Privacy.Excluded("api/server.go") {
		t.Fatalf("privacy = %+v", res.Privacy)
	}
	if got := res.Privacy.FilterPaths([]string{"hr/salaries.md", "api/server.go"}); !slices.Equal(got, []string{"api/server.go"}) {
		t.Fatalf("filtered = %v", got)
	}
}

// scopeDefaultsFixture builds the vendored plus requirement-only input.
func scopeDefaultsFixture(t *testing.T) *Resolved {
	t.Helper()
	cfg := mustParse(t, `{"schema_version":"1",
		"defaults":{"display_min_severity":"important","chunk":false},
		"scopes":[
			{"name":"vendored","include":["vendor/**"],"defaults":{"display_min_severity":"critical"}},
			{"name":"req-only","include":["vendor/**"],"requirements":{"require_bind":true}}]}`)
	return resolveMust(t, &Inputs{Repo: cfg, Files: []FileInput{{Path: "vendor/lib.go"}, {Path: "api.go"}}})
}

// TestResolveScopeDefaults pins per-path refinement: the last matching
// scope wins the fields it provides while inherited per-field defaults
// survive, with origins naming the winning scope.
func TestResolveScopeDefaults(t *testing.T) {
	res := scopeDefaultsFixture(t)
	vendored := res.Files[0].Defaults
	if vendored.Values.DisplayMinSeverity == nil || *vendored.Values.DisplayMinSeverity != "critical" {
		t.Fatalf("vendored display = %+v", vendored.Values)
	}
	if vendored.Origins["display_min_severity"] != (FieldOrigin{Layer: LayerScope, Source: "scope vendored"}) {
		t.Fatalf("vendored origin = %+v", vendored.Origins["display_min_severity"])
	}
	if vendored.Values.Chunk {
		t.Fatalf("inherited chunk flipped: %+v", vendored.Values)
	}
	if got := res.Files[1].Defaults.Values.DisplayMinSeverity; got == nil || *got != "important" {
		t.Fatalf("unscoped display = %v", got)
	}
}

// TestResolveRequirementOnlyScopeDefaults pins that requirement-only
// scopes leave defaults alone while still accumulating requirements.
func TestResolveRequirementOnlyScopeDefaults(t *testing.T) {
	res := scopeDefaultsFixture(t)
	if !res.Files[0].Requirements.RequireBind || res.Files[1].Requirements.RequireBind {
		t.Fatalf("bind = %v / %v", res.Files[0].Requirements.RequireBind, res.Files[1].Requirements.RequireBind)
	}
}
