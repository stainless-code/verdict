package config

import (
	"github.com/stainless-code/verdict/internal/core"
)

// defaultsValues renders one scope's effective values with their origins, so
// the root defaults and every file speak one key/value/layer/source vocabulary.
func defaultsValues(d *ResolvedDefaults) []core.ConfigValue {
	get := func(key string) FieldOrigin { return d.Origins[key] }
	values := d.Values
	return []core.ConfigValue{
		{Key: keyPanel, Value: values.Panel, Layer: get(keyPanel).Layer, Source: get(keyPanel).Source},
		{Key: keyBudgetUSD, Value: values.BudgetUSD, Layer: get(keyBudgetUSD).Layer, Source: get(keyBudgetUSD).Source},
		{Key: keyMaxBytes, Value: values.MaxBytes, Layer: get(keyMaxBytes).Layer, Source: get(keyMaxBytes).Source},
		{Key: keyChunk, Value: values.Chunk, Layer: get(keyChunk).Layer, Source: get(keyChunk).Source},
		{Key: keyKBMinJudged, Value: values.KBMinJudged, Layer: get(keyKBMinJudged).Layer, Source: get(keyKBMinJudged).Source},
		{Key: keyKBMaxFPRate, Value: values.KBMaxFPRate, Layer: get(keyKBMaxFPRate).Layer, Source: get(keyKBMaxFPRate).Source},
		{Key: keyEffort, Value: values.Effort, Layer: get(keyEffort).Layer, Source: get(keyEffort).Source},
		{Key: keyDisplayFloor, Value: values.DisplayMinSeverity, Layer: get(keyDisplayFloor).Layer, Source: get(keyDisplayFloor).Source},
		{Key: keyPostFloor, Value: values.PostMinSeverity, Layer: get(keyPostFloor).Layer, Source: get(keyPostFloor).Source},
	}
}

// shadowValues renders captured losing values as reported values, keeping the
// layer and source of the value that did not win.
func shadowValues(shadowed []ShadowedValue) []core.ConfigValue {
	out := make([]core.ConfigValue, 0, len(shadowed))
	for _, s := range shadowed {
		out = append(out, core.ConfigValue{Key: s.Key, Value: s.Value, Layer: s.Layer, Source: s.Source})
	}
	return out
}

// resolvedFiles renders the per-file configuration from the capture: the
// contributing scopes in match order plus that path's own effective and
// shadowed values. Nothing here re-reads configuration or the environment.
func resolvedFiles(resolved *Resolved) []core.ConfigFile {
	out := make([]core.ConfigFile, 0, len(resolved.Files))
	for i := range resolved.Files {
		file := &resolved.Files[i]
		out = append(out, core.ConfigFile{
			Path:     file.Path,
			OldPath:  file.OldPath,
			Scopes:   file.Scopes,
			Values:   defaultsValues(&file.Defaults),
			Shadowed: shadowValues(file.Defaults.Shadowed),
		})
	}
	return out
}
