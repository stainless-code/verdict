package config

import (
	"path/filepath"
	"testing"
)

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

// TestRegistryPathUsesSettingsDirectory pins platform paths and XDG precedence.
func TestRegistryPathUsesSettingsDirectory(t *testing.T) {
	tests := []struct {
		name string
		home string
		xdg  string
		want string
	}{
		{name: "default", home: `/home/alice`, want: `/home/alice/.config/verdict/projects.json`},
		{name: "windows home", home: filepath.FromSlash(`C:/Users/Alice`), want: `C:/Users/Alice/.config/verdict/projects.json`},
		{name: "xdg with space", home: `~`, xdg: filepath.FromSlash(`C:/Shared Config`), want: `C:/Shared Config/verdict/projects.json`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RegistryPath(tt.home, tt.xdg); filepath.ToSlash(got) != tt.want {
				t.Errorf("RegistryPath(%q, %q) = %q, want %q", tt.home, tt.xdg, got, tt.want)
			}
		})
	}
}

// TestParsePersonalPrefsKeys pins the shared-file rule: the CLI prefs keys
// a real operator settings file carries parse but never feed the resolver.
func TestParsePersonalPrefsKeys(t *testing.T) {
	give := `{"layout":{"compact":true},"output":{"format":"json"},"color":{"mode":"auto"},"publish":{"footer":true},
		"review":{"panel":["m"]}}`
	got, ref := ParsePersonal([]byte(give))
	if ref != nil {
		t.Fatalf("ParsePersonal: %v", ref)
	}
	if len(got.Review.Panel) != 1 {
		t.Fatalf("review = %+v, want the typed section parsed beside the prefs keys", got.Review)
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

// TestParsePersonalReachesNestedDefaults pins the recursive walk: a misspelled
// kb_policy member used to be accepted here and silently keep the built-in
// threshold, while the identical typo in .verdict/config.json refused.
func TestParsePersonalReachesNestedDefaults(t *testing.T) {
	checkPersonalRefusal(t, `{"review":{"kb_policy":{"minjudged":3}}}`, CodeBadConfig, "kb_policy")
	checkPersonalRefusal(t, `{"review":{"kb_policy":{"max_fp_rate":"half"}}}`, CodeBadConfig, "max_fp_rate")
	good := `{"review":{"kb_policy":{"min_judged":3,"max_fp_rate":0.3}}}`
	got, ref := ParsePersonal([]byte(good))
	if ref != nil {
		t.Fatalf("ParsePersonal: %v", ref)
	}
	if got.Review.KBMinJudged == nil || *got.Review.KBMinJudged != 3 {
		t.Fatalf("review.kb_policy = %+v", got.Review)
	}
	if got.Review.KBMaxFPRate == nil || *got.Review.KBMaxFPRate != 0.3 {
		t.Fatalf("review.kb_policy rate = %+v", got.Review)
	}
}

// TestParsePersonalSemantics pins the personal value rules: non-empty
// exclusions, portable globs, and a positive limit.
func TestParsePersonalSemantics(t *testing.T) {
	checkPersonalRefusal(t, `{"privacy":{"exclusions":[]}}`, CodeBadConfig, "exclusions")
	checkPersonalRefusal(t, `{"privacy":{"exclusions":["/abs"]}}`, CodeBadConfig, "exclusions")
	checkPersonalRefusal(t, `{"spend":{"budget_usd":0}}`, CodeBadConfig, "budget_usd")
}

// TestParsePersonalPacks pins the explicit org pack list: a string array
// only, no empty entries, and no implicit directory scan.
func TestParsePersonalPacks(t *testing.T) {
	got, ref := ParsePersonal([]byte(`{"packs":["org/security","/abs/team"]}`))
	if ref != nil {
		t.Fatalf("ParsePersonal: %v", ref)
	}
	if len(got.Packs) != 2 || got.Packs[0] != "org/security" || got.Packs[1] != "/abs/team" {
		t.Fatalf("packs = %v, want [org/security /abs/team]", got.Packs)
	}
	checkPersonalRefusal(t, `{"packs":"org"}`, CodeBadConfig, "packs")
	checkPersonalRefusal(t, `{"packs":[""]}`, CodeBadConfig, "packs[0]")
	checkPersonalRefusal(t, `{"packs":[1]}`, CodeBadConfig, "packs[0]")
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
