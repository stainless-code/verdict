package readcommon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/spend"
)

func costExecution(dir string) string {
	value := phaseCostObject(filepath.Join(dir, "run.json"))
	return jq.StrOf(FieldOf(FieldOf(value, "execution"), "id"))
}

func phaseCostObject(path string) any {
	raw, err := jq.CappedFile(path, jq.MaxCostBytes)
	if err != nil {
		return nil
	}
	value, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil
	}
	return value
}

func phaseCostTotal(dir, execution string) CostResult {
	phases := observedCostPhases(dir, execution)
	prices, valid := phasePrices(dir, execution)
	if valid {
		valid = assignPhasePrices(phases, prices)
	}
	if !valid {
		phases = observedCostPhases(dir, execution)
		prices = map[string]phasePrice{}
	}
	projection, _ := spend.Project(execution, phases)
	byTier := jq.NewObj()
	for _, tier := range []string{tierCheap, tierStandard, tierDeep} {
		rows := tierPhases(phases, prices, tier)
		byTier.Set(tier, phaseTokenObject(rows))
	}
	cost := phaseTokenObject(phases)
	cost.Set("runs_with_cost", len(prices))
	cost.Set("cached_input_tokens", nil)
	var total any
	if projection.Total != nil {
		total = *projection.Total
	}
	cost.Set(keyEstUSD, total)
	cost.Set("by_tier", byTier)
	result := CostResult{Cost: cost, Phased: true}
	if !valid {
		result.Warns = []string{"phase costs unbound or repeated — spend remains unknown"}
	}
	return result
}

type phasePrice struct {
	phase spend.Phase
	tier  string
}

func observedCostPhases(dir, execution string) []spend.Phase {
	var phases []spend.Phase
	for _, finder := range []string{"finder-1", "finder-2"} {
		phases = append(phases, observedFinderPhases(dir, execution, finder, "discovery")...)
		phases = append(phases, observedFinderPhases(dir, execution, finder, "cross")...)
	}
	return phases
}

func observedFinderPhases(dir, execution, finder, phase string) []spend.Phase {
	namespace := costNamespace(finder, phase)
	launcher := phaseCostObject(filepath.Join(dir, namespace, "launcher.json"))
	// A cross launch that left no evidence is the zero-cross case; one whose
	// evidence exists but cannot be read is a launch no price can be bound to.
	if launcher == nil && phase == "cross" && !launchEvidenceExists(dir, namespace) {
		return nil
	}
	attempts := phaseAttemptCount(dir, namespace, launcher)
	var phases []spend.Phase
	for attempt := range attempts {
		kind := phase
		if phase == "discovery" && attempt > 0 {
			kind = "repair"
		}
		phases = append(phases, spend.Phase{
			ExecutionID: execution, Finder: finder, Phase: kind,
			LaunchID: fmt.Sprintf("%s-%s-%d", finder, phase, attempt+1),
		})
	}
	return phases
}

// launchEvidenceExists reports whether a launch left any evidence in
// namespace, even evidence no price can be read from: a manifest, or
// the rounds directory a launch writes its first output into. A lookup
// this cannot answer is evidence too, so unreadable spend stays unknown
// rather than reading as zero.
func launchEvidenceExists(dir, namespace string) bool {
	nsDir := filepath.Join(dir, namespace)
	info, err := os.Lstat(nsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil || !info.IsDir() {
		return true
	}
	for _, name := range []string{"launcher.json", ".run.rounds"} {
		if _, err := os.Lstat(filepath.Join(nsDir, name)); !errors.Is(err, fs.ErrNotExist) {
			return true
		}
	}
	return false
}

func phasePrices(dir, execution string) (map[string]phasePrice, bool) {
	prices := map[string]phasePrice{}

	for _, path := range phaseCostPaths(dir) {
		records, present, parsed := readCostFile(path)
		if !present {
			continue
		}
		if !parsed {
			return prices, false
		}
		if !collectCostRecords(prices, records, execution) {
			return prices, false
		}
	}
	return prices, true
}

func collectPhasePrices(prices map[string]phasePrice, record any, execution string) bool {
	object, ok := record.(map[string]any)
	if !ok {
		return false
	}
	raw, present := object["phases"]
	if !present {
		return true
	}
	if object["execution_id"] != execution {
		return false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	phases, code := spend.DecodePhases(data, execution)
	if code != "" {
		return false
	}
	return insertPhasePrices(prices, phases, CostTier(record))
}

func insertPhasePrices(prices map[string]phasePrice, phases []spend.Phase, tier string) bool {
	for _, phase := range phases {
		if _, exists := prices[phase.LaunchID]; exists {
			return false
		}
		prices[phase.LaunchID] = phasePrice{phase: phase, tier: tier}
	}
	return true
}

func assignPhasePrices(phases []spend.Phase, prices map[string]phasePrice) bool {
	assigned := 0
	for i, phase := range phases {
		if price, ok := prices[phase.LaunchID]; ok {
			if price.phase.Finder != phase.Finder || price.phase.Phase != phase.Phase {
				return false
			}
			phases[i] = price.phase
			assigned++
		}
	}
	return assigned == len(prices)
}

func tierPhases(phases []spend.Phase, prices map[string]phasePrice, tier string) []spend.Phase {
	var rows []spend.Phase
	for _, phase := range phases {
		phaseTier := tierStandard
		if price, ok := prices[phase.LaunchID]; ok {
			phaseTier = price.tier
		}
		if phaseTier == tier {
			rows = append(rows, phase)
		}
	}
	return rows
}

func phaseTokenObject(phases []spend.Phase) *jq.Obj {
	if len(phases) == 0 {
		return jq.NewObj(keyRuns, 0, keyInputTokens, 0, keyOutputTokens, 0, keyTotalTokens, 0)
	}
	projection, _ := spend.Project(phases[0].ExecutionID, phases)
	var in, out, total any
	if projection.InputTokens != nil {
		in = *projection.InputTokens
	}
	if projection.OutputTokens != nil {
		out = *projection.OutputTokens
	}
	if canSumPhaseTokens(projection) {
		total = *projection.InputTokens + *projection.OutputTokens
	}
	return jq.NewObj(keyRuns, len(phases), keyInputTokens, in, keyOutputTokens, out, keyTotalTokens, total)
}

// phaseAttemptCount counts the rounds this phase launched. NN.out is the one
// file every round writes before its verdict, so a round that stopped before
// saving a review still counts, and a launcher's attempt records stay a lower
// bound.
func phaseAttemptCount(dir, namespace string, launcher any) int {
	count := max(len(jq.Elements(FieldOf(launcher, "attempts"))), 1)
	files, err := os.ReadDir(filepath.Join(dir, namespace, ".run.rounds"))
	if err != nil {
		return count
	}
	rounds := 0
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".out") {
			rounds++
		}
	}
	return max(count, rounds)
}

func costNamespace(finder, phase string) string {
	index := "1"
	if finder == "finder-2" {
		index = "2"
	}
	namespace := "x-finder-" + index
	if phase == "cross" {
		namespace = "x-cross-" + index
	}
	return namespace
}

func collectCostRecords(prices map[string]phasePrice, records []any, execution string) bool {
	for _, record := range records {
		if !collectPhasePrices(prices, record, execution) {
			return false
		}
	}
	return true
}

func canSumPhaseTokens(projection spend.Projection) bool {
	if projection.InputTokens == nil || projection.OutputTokens == nil {
		return false
	}
	return *projection.InputTokens <= math.MaxInt64-*projection.OutputTokens
}

func phaseCostPaths(dir string) []string {
	paths := []string{filepath.Join(dir, CostFile)}
	for _, name := range []string{"x-finder-1", "x-finder-2", "x-cross-1", "x-cross-2"} {
		paths = append(paths, filepath.Join(dir, name, CostFile))
	}
	return paths
}
