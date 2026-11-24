package initcore

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/jq"
)

// pinned records which presets are documented to write a roster. A preset
// missing here fails TestEveryPresetIsClassified, so a new one cannot skip
// the validator below by being forgotten.
var pinned = map[string]bool{"chill": false, "standard": true, "strict": true}

func writtenConfig(t *testing.T, name string) (panel []string, budget float64) {
	t.Helper()
	var cfg struct {
		Defaults struct {
			Panel     []string `json:"panel"`
			BudgetUSD float64  `json:"budget_usd"`
		} `json:"defaults"`
	}
	if err := json.Unmarshal([]byte(presetFiles(presets[name])[configName]), &cfg); err != nil {
		t.Fatalf("%s: init wrote invalid config.json: %v", name, err)
	}
	return cfg.Defaults.Panel, cfg.Defaults.BudgetUSD
}

func TestEveryPresetIsClassified(t *testing.T) {
	t.Parallel()
	names := PresetValues()
	got := make([]string, 0, len(pinned))
	for name := range pinned {
		got = append(got, name)
	}
	slices.Sort(got)
	if !slices.Equal(names, got) {
		t.Fatalf("presets %v and the pinned table %v differ; classify the new preset", names, got)
	}
}

// TestPresetRostersPassPanelValidation feeds each written roster to the
// planner `verdict panel` runs, in the one-slug-per-line form fetch pins into
// the bundle, so init cannot scaffold a panel the verb then refuses.
func TestPresetRostersPassPanelValidation(t *testing.T) {
	t.Parallel()
	for _, name := range PresetValues() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkPresetRoster(t, name)
		})
	}
}

func checkPresetRoster(t *testing.T, name string) {
	t.Helper()
	roster, _ := writtenConfig(t, name)
	if got := len(roster) > 0; got != pinned[name] {
		t.Fatalf("%s wrote roster %v; documented as pinned=%v", name, roster, pinned[name])
	}
	if len(roster) > 0 {
		requirePanelAccepts(t, name, roster)
	}
}

func requirePanelAccepts(t *testing.T, name string, roster []string) {
	t.Helper()
	_, _, _, err := panelcore.Plan("bundle", "head", "now", strings.Join(roster, "\n")+"\n", "prompt", jq.NewObj())
	if err != nil {
		t.Fatalf("panel refuses the %s roster %v: %v", name, roster, err)
	}
}

// TestPresetBudgetGrowsWithStrictness pins the order: a deeper panel costs
// more per review, so its guard cannot be the tightest.
func TestPresetBudgetGrowsWithStrictness(t *testing.T) {
	t.Parallel()
	var got []string
	last := -1.0
	for _, name := range []string{"chill", "standard", "strict"} {
		_, budget := writtenConfig(t, name)
		if budget <= last {
			t.Errorf("%s budget %v does not exceed the previous preset's %v", name, budget, last)
		}
		last = budget
		got = append(got, strconv.FormatFloat(budget, 'f', 2, 64))
	}
	if want := "0.10 0.50 2.00"; strings.Join(got, " ") != want {
		t.Errorf("budgets = %v; want %s", got, want)
	}
}
