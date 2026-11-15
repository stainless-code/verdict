package recordcore

import (
	"slices"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func authorStamp(dir string) *core.AuthorRef {
	file, ok := readcommon.Author(dir)
	if !ok {
		return nil
	}
	return &core.AuthorRef{Agent: file.Agent, Model: file.Model}
}

func reviewModels(dir string) []string {
	names := []string{}
	for _, rec := range readcommon.CostRecords(dir) {
		models, ok := readcommon.CostModels(rec)
		if !ok {
			continue
		}
		names = append(names, modelNames(models)...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// CostModels allows null entries, so each element still needs AsMap.
func modelNames(models []any) []string {
	var out []string
	for _, m := range models {
		entry, ok := jq.AsMap(m)
		if !ok {
			continue
		}
		if name := jq.StrOf(entry["name"]); name != "" {
			out = append(out, name)
		}
	}
	return out
}
