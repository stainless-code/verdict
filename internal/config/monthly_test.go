package config

import (
	"strings"
	"testing"
)

func TestMonthlyBudgetPrecedenceStricterWins(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1","defaults":{"monthly_budget_usd":100}}`)
	personal := &PersonalSettings{Spend: PersonalSpend{MonthlyBudgetUSD: usd(50)}}
	resolved, ref := Resolve(&Inputs{Repo: cfg, Personal: personal})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if resolved.Spend.MonthlyBudgetUSD == nil || *resolved.Spend.MonthlyBudgetUSD != 50 {
		t.Fatalf("effective = %v, want stricter 50", resolved.Spend.MonthlyBudgetUSD)
	}
	if resolved.Spend.MonthlyRepoCap == nil || *resolved.Spend.MonthlyRepoCap != 100 {
		t.Fatalf("repo cap = %v, want 100", resolved.Spend.MonthlyRepoCap)
	}
	if resolved.Spend.MonthlyPersonalCap == nil || *resolved.Spend.MonthlyPersonalCap != 50 {
		t.Fatalf("personal cap = %v, want 50", resolved.Spend.MonthlyPersonalCap)
	}
}

func TestMonthlyBudgetRepoWhenNoPersonal(t *testing.T) {
	cfg := mustParse(t, `{"schema_version":"1","defaults":{"monthly_budget_usd":75}}`)
	resolved, ref := Resolve(&Inputs{Repo: cfg})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if resolved.Spend.MonthlyBudgetUSD == nil || *resolved.Spend.MonthlyBudgetUSD != 75 {
		t.Fatalf("effective = %v, want 75", resolved.Spend.MonthlyBudgetUSD)
	}
}

func TestMonthlyBudgetPersonalWhenNoRepo(t *testing.T) {
	personal := &PersonalSettings{Spend: PersonalSpend{MonthlyBudgetUSD: usd(42)}}
	resolved, ref := Resolve(&Inputs{Personal: personal})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if resolved.Spend.MonthlyBudgetUSD == nil || *resolved.Spend.MonthlyBudgetUSD != 42 {
		t.Fatalf("effective = %v, want 42", resolved.Spend.MonthlyBudgetUSD)
	}
}

func TestMonthlyBudgetInvalidRefuses(t *testing.T) {
	cases := []string{
		`{"schema_version":"1","defaults":{"monthly_budget_usd":0}}`,
		`{"schema_version":"1","defaults":{"monthly_budget_usd":-1}}`,
		`{"schema_version":"1","defaults":{"monthly_budget_usd":"5"}}`,
		`{"schema_version":"1","defaults":{"monthly_budget_usd":null}}`,
	}
	for _, doc := range cases {
		if _, ref := Parse([]byte(doc)); ref == nil || ref.Code != CodeBadConfig {
			t.Fatalf("Parse(%s) = %v, want bad-config", doc, ref)
		}
	}
	// Personal side as well.
	for _, doc := range []string{
		`{"spend":{"monthly_budget_usd":0}}`,
		`{"spend":{"monthly_budget_usd":-5}}`,
		`{"spend":{"monthly_budget_usd":"x"}}`,
	} {
		if _, ref := ParsePersonal([]byte(doc)); ref == nil {
			t.Fatalf("ParsePersonal(%s) passed, want refusal", doc)
		}
	}
}

func TestMonthlyBudgetValidParses(t *testing.T) {
	cfg, ref := Parse([]byte(`{"schema_version":"1","defaults":{"monthly_budget_usd":12.34}}`))
	if ref != nil {
		t.Fatalf("Parse valid: %v", ref)
	}
	if cfg.Defaults.MonthlyBudgetUSD == nil || *cfg.Defaults.MonthlyBudgetUSD != 12.34 {
		t.Fatalf("parsed = %v, want 12.34", cfg.Defaults.MonthlyBudgetUSD)
	}
	personal, ref := ParsePersonal([]byte(`{"spend":{"monthly_budget_usd":99.9}}`))
	if ref != nil {
		t.Fatalf("ParsePersonal valid: %v", ref)
	}
	if personal.Spend.MonthlyBudgetUSD == nil || *personal.Spend.MonthlyBudgetUSD != 99.9 {
		t.Fatalf("personal = %v, want 99.9", personal.Spend.MonthlyBudgetUSD)
	}
}

func TestMonthlyBudgetScopeRefuses(t *testing.T) {
	doc := `{"schema_version":"1","scopes":[{"name":"api","include":["*.go"],"defaults":{"monthly_budget_usd":10}}]}`
	_, ref := Parse([]byte(doc))
	if ref == nil || ref.Code != CodeBadConfig {
		t.Fatalf("scope monthly_budget_usd Parse = %v, want bad-config", ref)
	}
	if !strings.Contains(ref.Message, "monthly_budget_usd") || !strings.Contains(ref.Message, "$.scopes[0].defaults.monthly_budget_usd") {
		t.Fatalf("refusal = %q, want path $.scopes[0].defaults.monthly_budget_usd naming repo-wide default", ref.Message)
	}
}
