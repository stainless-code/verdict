package config

import (
	"path/filepath"
	"testing"
)

// checkPersonalRefusal pins one personal-settings refusal.
func checkPersonalRefusal(t *testing.T, give, code, fragment string) {
	t.Helper()
	_, ref := ParsePersonal([]byte(give))
	requireRefusal(t, ref, code, fragment)
}

// TestParsePersonal pins the three personal sections: review defaults
// without budget_usd, the spend authorization limit, and the privacy
// exclusions.
func TestParsePersonal(t *testing.T) {
	give := `{"review":{"panel":["m"],"max_bytes":100,"effort":"deep"},
		"spend":{"budget_usd":3.0,"require_known_estimate":true},
		"privacy":{"exclusions":["hr/**"]}}`
	got, ref := ParsePersonal([]byte(give))
	if ref != nil {
		t.Fatalf("ParsePersonal: %v", ref)
	}
	if len(got.Review.Panel) != 1 || got.Review.BudgetUSD != nil {
		t.Fatalf("review = %+v", got.Review)
	}
	if got.Spend.BudgetUSD == nil || *got.Spend.BudgetUSD != 3.0 || !got.Spend.RequireKnownEstimate {
		t.Fatalf("spend = %+v", got.Spend)
	}
	if len(got.Privacy.Exclusions) != 1 {
		t.Fatalf("privacy = %+v", got.Privacy)
	}
}

// TestParsePersonalMisplacedBudget pins the placement rule: a budget_usd
// inside personal review refuses as misplaced instead of silently
// becoming the authorization limit.
func TestParsePersonalMisplacedBudget(t *testing.T) {
	checkPersonalRefusal(t, `{"review":{"budget_usd":5.0}}`, CodeBadConfig, "misplaced")
}

// TestParsePersonalCredentials pins credentials before unknown fields
// for the personal sections, with no user-map exemptions.
func TestParsePersonalCredentials(t *testing.T) {
	checkPersonalRefusal(t, `{"bogus":1,"api_key":"x"}`, CodeBadCredentials, "api_key")
	checkPersonalRefusal(t, `{"bogus":1}`, CodeBadConfig, "bogus")
}

// TestParsePersonalSemantics pins the personal value rules: non-empty
// exclusions, portable globs, and a positive limit.
func TestParsePersonalSemantics(t *testing.T) {
	checkPersonalRefusal(t, `{"privacy":{"exclusions":[]}}`, CodeBadConfig, "exclusions")
	checkPersonalRefusal(t, `{"privacy":{"exclusions":["/abs"]}}`, CodeBadConfig, "exclusions")
	checkPersonalRefusal(t, `{"spend":{"budget_usd":0}}`, CodeBadConfig, "budget_usd")
}

// TestLoadPersonalAbsent pins the no-create rule: a missing settings file
// reads as an empty personal layer and never writes one.
func TestLoadPersonalAbsent(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "settings.json")
	got, ref := LoadPersonal(missing)
	if ref != nil {
		t.Fatalf("LoadPersonal: %v", ref)
	}
	if got == nil || len(got.Privacy.Exclusions) != 0 || got.Spend.BudgetUSD != nil {
		t.Fatalf("personal = %+v", got)
	}
	entries, err := filepath.Glob(missing)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("LoadPersonal created %v", entries)
	}
}
