package config

import "github.com/stainless-code/verdict/internal/core"

func buildSeverityWeights(raw any, path string) (*core.ConfigSeverityWeights, *core.RefusalError) {
	obj, ref := asObject(raw, path)
	if ref != nil {
		return nil, ref
	}
	out := &core.ConfigSeverityWeights{}
	fields := []struct {
		dst  **int
		name string
	}{
		{name: severityEnum[0], dst: &out.Critical},
		{name: severityEnum[1], dst: &out.Important},
		{name: severityEnum[2], dst: &out.Informational},
	}
	for _, f := range fields {
		value, ref := presentValue(obj, f.name, path, weightValue)
		if ref != nil {
			return nil, ref
		}
		*f.dst = value
	}
	return out, nil
}

// SeverityWeights resolves repository overrides over the benchmark defaults.
func SeverityWeights(cfg *core.Config) core.SeverityWeights {
	w := core.SeverityWeights{Critical: defaultCriticalWeight, Important: defaultImportantWeight, Informational: 1}
	if cfg == nil || cfg.SeverityWeights == nil {
		return w
	}
	c := cfg.SeverityWeights
	if c.Critical != nil {
		w.Critical = *c.Critical
	}
	if c.Important != nil {
		w.Important = *c.Important
	}
	if c.Informational != nil {
		w.Informational = *c.Informational
	}
	return w
}

const (
	defaultCriticalWeight  = 25
	defaultImportantWeight = 5
	maxSeverityWeight      = 1000000
)

func weightValue(v any, at string) (int, *core.RefusalError) {
	n, ref := boundedCount(v, at, 1)
	if ref != nil {
		return 0, ref
	}
	if n > maxSeverityWeight {
		return 0, refuse(CodeBadConfig, "%s: weight exceeds 1000000", at)
	}
	return n, nil
}
