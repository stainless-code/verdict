package config

import (
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// Legacy knob files the typed configuration supersedes but still honors:
// panel members, the spend threshold, and the byte cap, read from the
// trusted snapshot (base commit for remote runs, the reviewed worktree
// for local runs) below the typed repository layer. Environment wins
// over these files; a malformed budget or byte-cap file refuses naming
// the file, exactly like the shipped knob behavior.
const (
	legacyPanelPath    = ".review/panel"
	legacyBudgetPath   = ".review/budget"
	legacyMaxBytesPath = ".review/max_bytes"
	legacyChunkPath    = ".review/chunk"
)

// LegacyInputs carries the legacy knob-file contents for the resolution
// layer: raw bytes per knob path, nil when the file is absent.
type LegacyInputs struct {
	Panel    []byte
	Budget   []byte
	MaxBytes []byte
}

// legacyLayers parses the present legacy knob files into single-field
// layers, each naming the file that supplied it. Panel members never
// refuse; a malformed budget or byte-cap value refuses with the shipped
// file-named code.
func legacyLayers(files LegacyInputs) ([]layerValue, *core.RefusalError) {
	var out []layerValue
	if len(files.Panel) > 0 {
		if members := legacyPanelMembers(string(files.Panel)); len(members) > 0 {
			out = append(out, layerValue{
				partial: Partial{Panel: members, PanelSet: true},
				layer:   LayerRepo,
				source:  legacyPanelPath,
			})
		}
	}
	if len(files.Budget) > 0 {
		budget, ref := legacyBudget(string(files.Budget))
		if ref != nil {
			return nil, ref
		}
		if budget.BudgetUSD != nil {
			out = append(out, layerValue{
				partial: budget,
				layer:   LayerRepo,
				source:  legacyBudgetPath,
			})
		}
	}
	if len(files.MaxBytes) > 0 {
		maxBytes, ref := legacyMaxBytes(string(files.MaxBytes))
		if ref != nil {
			return nil, ref
		}
		if maxBytes != nil {
			out = append(out, layerValue{
				partial: Partial{MaxBytes: maxBytes},
				layer:   LayerRepo,
				source:  legacyMaxBytesPath,
			})
		}
	}
	return out, nil
}

// legacyPanelMembers scans every usable line: comments and blanks
// skipped, empty reading absent.
func legacyPanelMembers(raw string) []string {
	var out []string
	for line := range strings.Lines(raw) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// legacyFirstLine reads the first usable line of a knob file, reporting
// absent when the file holds none.
func legacyFirstLine(raw string) (string, bool) {
	for line := range strings.Lines(raw) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return trimmed, true
	}
	return "", false
}

// legacyBudget parses the budget knob into a set layer: a leading $ is
// stripped, the file literal rides for display, and a malformed value
// refuses naming the file.
func legacyBudget(raw string) (Partial, *core.RefusalError) {
	line, ok := legacyFirstLine(raw)
	if !ok {
		return Partial{}, nil
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "$"))
	if !isPlainNumber(line) {
		return Partial{}, refuse("bad-budget",
			"budget '%s' from %s is not a plain USD number (want e.g. REVIEW_BUDGET_USD=0.50 or a .review/budget file holding 0.50)", line, legacyBudgetPath)
	}
	budget, err := strconv.ParseFloat(line, floatBits)
	if err != nil || budget <= 0 {
		return Partial{}, refuse("bad-budget", "budget '%s' from %s is not above 0", line, legacyBudgetPath)
	}
	return Partial{BudgetUSD: &budget, BudgetDisplay: line}, nil
}

// legacyMaxBytes parses the byte-cap knob, refusing malformed values
// naming the file.
func legacyMaxBytes(raw string) (*int64, *core.RefusalError) {
	line, ok := legacyFirstLine(raw)
	if !ok {
		return nil, nil
	}
	maxBytes, err := strconv.ParseInt(line, decimalBase, floatBits)
	if err != nil || maxBytes < 1 {
		return nil, refuse("bad-max-bytes", "cap '%s' from %s is not a positive byte count", line, legacyMaxBytesPath)
	}
	return &maxBytes, nil
}
