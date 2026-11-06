package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func requireRefusal(t *testing.T, ref *core.RefusalError, code, fragment string) {
	t.Helper()
	var target *core.RefusalError
	if !errors.As(ref, &target) || target == nil {
		t.Fatalf("want refusal %q, got %v", code, ref)
	}
	if target.Code != code {
		t.Fatalf("code = %q, want %q (%s)", target.Code, code, target.Message)
	}
	if fragment != "" && !strings.Contains(target.Message, fragment) {
		t.Errorf("refusal %q misses fragment %q", target.Message, fragment)
	}
}

// checkParseRefusal parses one input and pins its refusal code and message
// fragment, so table tests stay branch-free.
func checkParseRefusal(t *testing.T, give, code, fragment string) {
	t.Helper()
	_, ref := Parse([]byte(give))
	requireRefusal(t, ref, code, fragment)
}

// TestParseOrder pins the fixed refusal order: one input produces one
// diagnostic, with size first and semantic checks last.
func TestParseOrder(t *testing.T) {
	big := `{"schema_version":"1","defaults":{"panel":["` + strings.Repeat("x", MaxConfigBytes) + `"]}}`
	tests := []struct {
		name     string
		give     string
		code     string
		fragment string
	}{
		{"size before shape", big, CodeTooLarge, "over the 65536-byte cap"},
		{"malformed names offset", `{"schema_version":`, CodeBadConfig, "offset"},
		{"trailing data", `{"schema_version":"1"} {}`, CodeBadConfig, "trailing data"},
		{"duplicate key", `{"schema_version":"1","schema_version":"1"}`, CodeBadConfig, `duplicate key "schema_version"`},
		{"top null", `null`, CodeBadConfig, "$"},
		{"top array", `[]`, CodeBadConfig, "want a JSON object"},
		{"missing version", `{}`, CodeBadConfig, "schema_version"},
		{"null version", `{"schema_version":null}`, CodeBadConfig, "schema_version"},
		{"wrong version", `{"schema_version":"2"}`, CodeBadConfig, `"1"`},
		{"credential before unknown", `{"schema_version":"1","bogus":1,"token":"x"}`, CodeBadCredentials, `"token"`},
		{"credential nested", `{"schema_version":"1","scopes":[{"name":"s","include":["x"],"requirements":{"block_clauses":[{"name":"c","threshold":"critical","secret":"x"}]}}]}`, CodeBadCredentials, `"secret"`},
		{"unknown field", `{"schema_version":"1","bogus":1}`, CodeBadConfig, "$.bogus: unknown field"},
		{"schema ref non-string", `{"schema_version":"1","$schema":7}`, CodeBadConfig, "$schema"},
		{"semantic last", `{"schema_version":"1","token":"x","defaults":{"budget_usd":0}}`, CodeBadCredentials, `"token"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkParseRefusal(t, tc.give, tc.code, tc.fragment)
		})
	}
}

// TestParseOrderShape pins the JSON-shape refusals separately so the
// order table above stays readable.
func TestParseOrderShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		give     string
		fragment string
	}{
		{"empty", ``, "offset"},
		{"blank", `   `, "offset"},
		{"unclosed array", `[`, "offset"},
		{"deep duplicate", `{"schema_version":"1","defaults":{"panel":["a"],"panel":["b"]}}`, `duplicate key "panel"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkParseRefusal(t, tc.give, CodeBadConfig, tc.fragment)
		})
	}
}

// TestParseCredentials pins the exemption: profile names are user-defined
// map keys, so a profile named like a credential still parses, while the
// same key as a fixed-shape field refuses.
func TestParseCredentials(t *testing.T) {
	exempt := `{"schema_version":"1","profiles":{"token":{"depth":"deep"}}}`
	cfg, ref := Parse([]byte(exempt))
	if ref != nil {
		t.Fatalf("profile name exemption refused: %v", ref)
	}
	if _, ok := cfg.Profiles["token"]; !ok {
		t.Fatal("profile \"token\" missing after parse")
	}
	for _, key := range []string{"api_key", "client_secret", "private_key", "auth_token", "session_token", "password", "bearer"} {
		give := `{"schema_version":"1","defaults":{"panel":["m"],"kb_policy":{"min_judged":5}},"` + key + `":"x"}`
		checkParseRefusal(t, give, CodeBadCredentials, `"`+key+`"`)
	}
}

// TestParseValidContract parses the Appendix A shape and spot-checks the
// typed construction: root defaults, root clauses, scope kinds, profiles,
// and the default profile.
func TestParseValidContract(t *testing.T) {
	give := `{"schema_version":"1",
		"defaults":{"panel":["a"],"budget_usd":5.0,"max_bytes":512000,"chunk":false,
			"kb_policy":{"min_judged":5,"max_fp_rate":0.5},"effort":"standard",
			"display_min_severity":"important","post_min_severity":"important"},
		"requirements":{"block_clauses":[
			{"name":"root-security","threshold":"important","categories":["security"],"paths":["**"]},
			{"name":"payments-logic","threshold":"critical","categories":["logic"],"paths":["payments/**"]}],
			"require_checks":["no-open-above-block","head-current"],"require_bind":true},
		"scopes":[
			{"name":"migrations","include":["db/migrations/**"],"profile":"deep-review",
				"requirements":{"block_clauses":[{"name":"migrations-any","threshold":"important","paths":["db/migrations/**"]}]}},
			{"name":"vendored","include":["vendor/**","dist/**"],"exclude":["vendor/keep/**"],"defaults":{"display_min_severity":"critical"}},
			{"name":"payments-strict","include":["payments/**"],"requirements":{"require_checks":["no-live-threads"]}}],
		"profiles":{
			"standard-review":{"depth":"standard","checks":["no-open-above-block","head-current"],"max_lenses":2},
			"deep-review":{"depth":"deep","checks":["no-open-above-block","head-current"],"max_lenses":4}},
		"default_profile":"standard-review"}`
	cfg, ref := Parse([]byte(give))
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	if cfg.SchemaVersion != "1" || len(cfg.Defaults.Panel) != 1 || *cfg.Defaults.BudgetUSD != 5.0 {
		t.Fatalf("defaults = %+v", cfg.Defaults)
	}
	if len(cfg.Requirements.BlockClauses) != 2 || cfg.Requirements.RequireBind == nil || !*cfg.Requirements.RequireBind {
		t.Fatalf("requirements = %+v", cfg.Requirements)
	}
	if len(cfg.Scopes) != 3 || len(cfg.Profiles) != 2 || cfg.DefaultProfile != "standard-review" {
		t.Fatalf("scopes/profiles = %+v %+v", cfg.Scopes, cfg.Profiles)
	}
}

// TestParseSemanticDefaults pins the bad-config half: ordinary default
// problems refuse with the field path named.
func TestParseSemanticDefaults(t *testing.T) {
	for _, tc := range []struct {
		name     string
		give     string
		fragment string
	}{
		{"budget zero", `{"schema_version":"1","defaults":{"budget_usd":0}}`, "budget_usd"},
		{"budget string", `{"schema_version":"1","defaults":{"budget_usd":"5"}}`, "budget_usd"},
		{"max bytes fraction", `{"schema_version":"1","defaults":{"max_bytes":1.5}}`, "max_bytes"},
		{"max bytes zero", `{"schema_version":"1","defaults":{"max_bytes":0}}`, "max_bytes"},
		{"chunk string", `{"schema_version":"1","defaults":{"chunk":"yes"}}`, "chunk"},
		{"kb min judged zero", `{"schema_version":"1","defaults":{"kb_policy":{"min_judged":0}}}`, "min_judged"},
		{"kb rate high", `{"schema_version":"1","defaults":{"kb_policy":{"max_fp_rate":2}}}`, "max_fp_rate"},
		{"effort unknown", `{"schema_version":"1","defaults":{"effort":"turbo"}}`, "effort"},
		{"profile bad check", `{"schema_version":"1","profiles":{"p":{"checks":["nope"]}}}`, "checks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkParseRefusal(t, tc.give, CodeBadConfig, tc.fragment)
		})
	}
}

// TestParseSemanticRequirements pins the bad-requirement half: clause and
// check problems refuse naming the requirement path.
func TestParseSemanticRequirements(t *testing.T) {
	for _, tc := range []struct {
		name     string
		give     string
		fragment string
	}{
		{"empty block clauses", `{"schema_version":"1","requirements":{"block_clauses":[]}}`, "block_clauses"},
		{"clause bad threshold", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"blocker"}]}}`, "threshold"},
		{"clause missing name", `{"schema_version":"1","requirements":{"block_clauses":[{"threshold":"critical"}]}}`, `"name"`},
		{"clause bad category", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"critical","categories":["ux"]}]}}`, "categories"},
		{"clause absolute path", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"critical","paths":["/etc/passwd"]}]}}`, "paths"},
		{"duplicate clause names", `{"schema_version":"1","requirements":{"block_clauses":[{"name":"c","threshold":"critical"},{"name":"c","threshold":"important"}]}}`, `"c"`},
		{"bad check id", `{"schema_version":"1","requirements":{"require_checks":["nope"]}}`, "require_checks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkParseRefusal(t, tc.give, CodeBadRequirement, tc.fragment)
		})
	}
}

// TestParseSemanticScopes pins the bad-scope half: scope shape and
// presence-rule problems refuse naming the scope path.
func TestParseSemanticScopes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		give     string
		fragment string
	}{
		{"empty scope", `{"schema_version":"1","scopes":[{"name":"s","include":["a/**"]}]}`, "one of profile, defaults, or requirements"},
		{"profile plus defaults", `{"schema_version":"1","profiles":{"p":{}},"scopes":[{"name":"s","include":["a/**"],"profile":"p","defaults":{"chunk":true}}]}`, "exclusive"},
		{"dangling scope profile", `{"schema_version":"1","scopes":[{"name":"s","include":["a/**"],"profile":"ghost"}]}`, `"ghost"`},
		{"duplicate scope names", `{"schema_version":"1","scopes":[{"name":"s","include":["a/**"],"requirements":{}},{"name":"s","include":["b/**"],"requirements":{}}]}`, `"s"`},
		{"scope bad glob escape", `{"schema_version":"1","scopes":[{"name":"s","include":["../evil"],"requirements":{}}]}`, "include"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkParseRefusal(t, tc.give, CodeBadScope, tc.fragment)
		})
	}
}

// TestParseSemanticDefaultProfile pins the top-level reference: a
// default profile naming nothing defined is a config problem.
func TestParseSemanticDefaultProfile(t *testing.T) {
	checkParseRefusal(t, `{"schema_version":"1","default_profile":"ghost"}`, CodeBadConfig, "default_profile")
}
