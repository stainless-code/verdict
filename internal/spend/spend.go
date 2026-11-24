// Package spend keeps unknown phase prices distinct from free work.
package spend

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"

	"github.com/stainless-code/verdict/internal/jq"
)

const (
	badPhaseUsage    = "bad-phase-usage"
	decimalPrecision = 10000
	numberBits       = 64
	maxCrossAttempts = 2
)

// Phase binds explicit usage to one launch, rather than a model name.
type Phase struct {
	USD          *float64 `json:"usd"                    min:"0"`
	InputTokens  *int64   `json:"input_tokens"           min:"0"`
	OutputTokens *int64   `json:"output_tokens"          min:"0"`
	ExecutionID  string   `json:"execution_id"           minLength:"1"`
	LaunchID     string   `json:"launch_id"              minLength:"1"`
	Finder       string   `enum:"finder-1|finder-2"      json:"finder"`
	Phase        string   `enum:"discovery|repair|cross" json:"phase"`
}

// Projection retains the known lower bound when a required phase is unpriced.
type Projection struct {
	InputTokens   *int64   `json:"input_tokens"`
	OutputTokens  *int64   `json:"output_tokens"`
	Total         *float64 `json:"total_usd"`
	KnownSubtotal *float64 `json:"known_subtotal_usd"`
	Unknown       []string `json:"unknown_phases"`
}

// Project rejects ambiguous attribution before adding any phase twice.
func Project(execution string, phases []Phase) (Projection, string) {
	state := phaseSum{result: Projection{Unknown: []string{}}, sum: new(big.Rat), seen: make(map[string]bool)}
	for i := range phases {
		if !state.add(execution, &phases[i]) {
			return state.result, badPhaseUsage
		}
	}
	amount := ratFloat(state.sum)
	if math.IsInf(amount, 0) {
		return state.result, badPhaseUsage
	}
	if len(state.seen) > len(state.result.Unknown) {
		state.result.KnownSubtotal = new(amount)
	}
	if len(phases) > 0 && len(state.result.Unknown) == 0 {
		state.result.Total = new(amount)
	}
	state.result.InputTokens = sumTokens(phases, func(p *Phase) *int64 { return p.InputTokens })
	state.result.OutputTokens = sumTokens(phases, func(p *Phase) *int64 { return p.OutputTokens })
	return state.result, ""
}

type phaseSum struct {
	sum    *big.Rat
	seen   map[string]bool
	result Projection
}

func (s *phaseSum) add(execution string, phase *Phase) bool {
	if !validPhase(execution, phase) || s.seen[phase.LaunchID] {
		return false
	}
	s.seen[phase.LaunchID] = true
	if phase.USD == nil {
		s.result.Unknown = append(s.result.Unknown, phase.LaunchID)
		return true
	}
	amount := decimal(*phase.USD)
	if amount == nil {
		return false
	}
	s.sum.Add(s.sum, amount)
	return true
}

func validPhase(execution string, phase *Phase) bool {
	return execution != "" && phase.ExecutionID == execution && phase.LaunchID != "" &&
		(phase.Finder == "finder-1" || phase.Finder == "finder-2") && knownPhase(phase.Phase) && nonnegativeTokens(phase)
}

func knownPhase(phase string) bool {
	return phase == "discovery" || phase == "repair" || phase == "cross"
}

func nonnegativeTokens(phase *Phase) bool {
	return (phase.InputTokens == nil || *phase.InputTokens >= 0) && (phase.OutputTokens == nil || *phase.OutputTokens >= 0)
}

func ratFloat(value *big.Rat) float64 { result, _ := value.Float64(); return result }

func decimal(value float64) *big.Rat {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	result, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, numberBits))
	if !ok {
		return nil
	}
	return result
}

// Policy has invocation-only consent; captured settings cannot grant it.
type Policy struct {
	Budget       string
	Personal     string
	MonthlyCap   string
	RequireKnown bool
	EnforceCap   bool
}

// Authorization names the existing refusal vocabulary without rendering it.
type Authorization struct {
	Code    string
	Receipt bool
}

// Authorize checks known lower bounds even when the full price is unknown.
func Authorize(projection Projection, policy Policy, consent bool) Authorization {
	if policy.EnforceCap {
		return Authorization{Code: "unsupported-cap"}
	}
	code := refusalCode(projection, policy)
	if code == "" {
		return Authorization{}
	}
	if consent {
		return Authorization{Receipt: true}
	}
	return Authorization{Code: code}
}

func refusalCode(projection Projection, policy Policy) string {
	amount := projection.Total
	if amount == nil {
		amount = projection.KnownSubtotal
	}
	if code := knownRefusal(amount, policy); code != "" {
		return code
	}

	if projection.Total == nil && policy.RequireKnown {
		return "budget-unknown"
	}
	return ""
}

func knownRefusal(amount *float64, policy Policy) string {
	if amount != nil {
		value := strconv.FormatFloat(*amount, 'f', -1, numberBits)
		if Exceeds(value, policy.Personal) {
			return "spend-capped"
		}
		if Exceeds(value, policy.Budget) {
			return "budget-exceeded"
		}
	}
	return ""
}

// Exceeds compares the decimal literals the operator actually provided.
func Exceeds(projected, limit string) bool {
	p, okP := new(big.Rat).SetString(projected)
	l, okL := new(big.Rat).SetString(limit)
	return okP && okL && p.Sign() >= 0 && l.Sign() >= 0 && p.Cmp(l) > 0
}

// MeanUSD preserves fetch's historical four-decimal projection.
func MeanUSD(sum float64, count int) (float64, string) {
	if count == 0 {
		return -1, ""
	}
	mean := sum / float64(count)
	rounded := math.Round(mean*decimalPrecision) / decimalPrecision
	return mean, jq.Compact(rounded)
}

func sumTokens(phases []Phase, selectTokens func(*Phase) *int64) *int64 {
	if len(phases) == 0 {
		return nil
	}
	var sum int64
	for i := range phases {
		value := selectTokens(&phases[i])
		if value == nil || sum > math.MaxInt64-*value {
			return nil
		}
		sum += *value
	}
	return &sum
}

// DecodePhases certifies phase attribution together with the cost record.
func DecodePhases(raw []byte, execution string) ([]Phase, string) {
	var wire []struct {
		Phase
		Input  json.RawMessage `json:"input_tokens"`
		Output json.RawMessage `json:"output_tokens"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return nil, badPhaseUsage
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, badPhaseUsage
	}
	phases := make([]Phase, len(wire))
	for i := range wire {
		item := &wire[i]
		input, inputOK := decodeTokens(item.Input)
		output, outputOK := decodeTokens(item.Output)
		if !inputOK || !outputOK {
			return nil, badPhaseUsage
		}
		item.InputTokens, item.OutputTokens = input, output
		phases[i] = item.Phase
	}
	if _, code := Project(execution, phases); code != "" {
		return nil, code
	}
	return phases, ""
}

func decodeTokens(raw json.RawMessage) (*int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, true
	}
	if raw[0] != '-' && (raw[0] < '0' || raw[0] > '9') {
		return nil, false
	}
	value, ok := new(big.Rat).SetString(string(raw))
	if !ok || value.Sign() < 0 || !value.IsInt() || !value.Num().IsInt64() {
		return nil, false
	}
	return new(value.Num().Int64()), true
}

// Expected prices no work implicitly, including bounded repair attempts.
func Expected(execution string, rounds int) []Phase {
	phases := make([]Phase, 0)
	for _, finder := range []string{"finder-1", "finder-2"} {
		phases = append(phases, expectedFinder(execution, finder, rounds)...)
	}
	return phases
}

func expectedFinder(execution, finder string, rounds int) []Phase {
	phases := make([]Phase, 0)
	for attempt := 1; attempt <= rounds; attempt++ {
		phase := "discovery"
		if attempt > 1 {
			phase = "repair"
		}
		phases = append(phases, Phase{
			ExecutionID: execution, Finder: finder, Phase: phase,
			LaunchID: fmt.Sprintf("%s-discovery-%d", finder, attempt),
		})
	}
	for attempt := 1; attempt <= min(rounds, maxCrossAttempts); attempt++ {
		phases = append(phases, Phase{
			ExecutionID: execution, Finder: finder, Phase: "cross",
			LaunchID: fmt.Sprintf("%s-cross-%d", finder, attempt),
		})
	}
	return phases
}

// CompletePlan leaves every unpriced required attempt unknown.
func CompletePlan(execution string, rounds int, prices []Phase) ([]Phase, string) {
	if _, code := Project(execution, prices); code != "" {
		return nil, code
	}
	expected := Expected(execution, rounds)
	indexes := make(map[string]int, len(expected))
	for i := range expected {
		indexes[expected[i].LaunchID] = i
	}
	for i := range prices {
		if !assignPrice(expected, indexes, &prices[i]) {
			return nil, badPhaseUsage
		}
	}

	return expected, ""
}

func assignPrice(expected []Phase, indexes map[string]int, price *Phase) bool {
	index, ok := indexes[price.LaunchID]
	if !ok || expected[index].Finder != price.Finder || expected[index].Phase != price.Phase {
		return false
	}
	expected[index] = *price
	return true
}

// ValidLimit refuses malformed guard values rather than disabling them.
func ValidLimit(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := strconv.ParseFloat(value, numberBits)
	return err == nil && decimal(parsed) != nil
}

// RecordProjection prevents an aggregate claim from hiding unpriced phases.
func RecordProjection(record map[string]any) (Projection, bool, bool) {
	raw, present := record["phases"]
	if !present {
		return Projection{}, false, true
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return Projection{}, true, false
	}
	execution, ok := record["execution_id"].(string)
	if !ok {
		return Projection{}, true, false
	}
	phases, code := DecodePhases(data, execution)
	if code != "" {
		return Projection{}, true, false
	}
	projection, code := Project(execution, phases)
	return projection, true, code == ""
}
