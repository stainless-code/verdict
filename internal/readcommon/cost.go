package readcommon

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// Cost rollup warning lines, byte-identical to the shell.
const (
	warnCostUnreadable   = "cost.json unreadable — spend unrecorded for this rollup"
	warnChunkUnreadable  = "chunk cost %s unreadable — spend unrecorded for this chunk"
	warnBudgetUnreadable = "budget.json unreadable — receipts unlisted for this rollup"
)

// CostResult carries the --cost rollup: the envelope-ready cost object (nil
// when the flag is absent), the receipts array (nil unless a budget file
// parsed), and warnings in shell order.
type CostResult struct {
	Cost     *jq.Obj
	Receipts []any
	Warns    []string
}

// CostRollup ports the report --cost rollup: the top-level cost.json plus
// every per-chunk <name>.cost.json merged through one rollup filter, then
// the budget.json receipts join. Absent files contribute zeros silently;
// unparseable files warn and contribute zeros — neither ever fails the run.
func CostRollup(dir string) CostResult {
	res := CostResult{}
	top, topPresent, topParsed := readCostFile(dir + "/cost.json")
	chunkFiles := chunkCostFiles(dir)
	if len(chunkFiles) == 0 {
		res.Cost = rollupTop(top, topPresent, topParsed, &res)
		attachReceipts(dir, &res)
		return res
	}
	if topPresent {
		if c, ok := rollupOne(top, topParsed); ok {
			res.Cost = c
		} else {
			res.Warns = append(res.Warns, warnCostUnreadable)
			res.Cost = costZeros()
		}
	} else {
		res.Cost = costZeros()
	}
	merged := append([]any{}, top...)
	for _, f := range chunkFiles {
		recs, present, parsed := readCostFile(f)
		if !present {
			continue
		}
		if !parsed {
			res.Warns = append(res.Warns, chunkWarn(f))
			continue
		}
		merged = append(merged, recs...)
	}
	if c := rollupRecords(merged); c != nil {
		res.Cost = c
	} else {
		// R1: the shell's merged rollup died on shape-invalid merged
		// streams (a chunk record the filter rejects); it now warns and
		// keeps zeros like every other cost failure.
		res.Warns = append(res.Warns, warnCostUnreadable)
		res.Cost = costZeros()
	}
	attachReceipts(dir, &res)
	return res
}

// rollupTop rolls the top-level file when no chunk files exist.
func rollupTop(top []any, present, parsed bool, res *CostResult) *jq.Obj {
	if !present {
		return costZeros()
	}
	if c, ok := rollupOne(top, parsed); ok {
		return c
	}
	res.Warns = append(res.Warns, warnCostUnreadable)
	return costZeros()
}

// rollupOne rolls parsed records, reporting whether the shape held.
func rollupOne(recs []any, parsed bool) (*jq.Obj, bool) {
	if !parsed {
		return nil, false
	}
	c := rollupRecords(recs)
	if c == nil {
		return nil, false
	}
	return c, true
}

// chunkWarn renders the per-chunk unreadable warning.
func chunkWarn(path string) string {
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
	}
	return "chunk cost " + base + " unreadable — spend unrecorded for this chunk"
}

// readCostFile reads one cost file: records plus its present/parsed
// tri-state. A non-regular path reads absent (the shell's [[ -f ]] gate);
// invalid JSON reads present-but-unparseable. Records that fail shape
// validation survive the read and fail at rollup time, mirroring the
// shell's two-stage filter.
func readCostFile(path string) (recs []any, present, parsed bool) {
	if !isRegular(path) {
		return nil, false, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, true, false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil, true, false
	}
	if arr, ok := doc.([]any); ok {
		return arr, true, true
	}
	return []any{doc}, true, true
}

// chunkCostFiles ports the per-chunk file discovery: manifest names first,
// then the on-disk chunk-*.cost.json glob, sorted and deduplicated.
func chunkCostFiles(dir string) []string {
	chunksDir := dir + "/chunks"
	if !isDir(chunksDir) {
		return nil
	}
	var files []string
	for _, name := range manifestChunkNames(chunksDir + "/manifest.json") {
		files = append(files, chunksDir+"/"+name+".cost.json")
	}
	entries, err := os.ReadDir(chunksDir)
	if err != nil {
		return dedupeSorted(files)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "chunk-") || !strings.HasSuffix(name, ".cost.json") {
			continue
		}
		files = append(files, chunksDir+"/"+name)
	}
	return dedupeSorted(files)
}

// manifestChunkNames reads the manifest's chunk names ("" when the manifest
// is absent or unreadable, mirroring `|| true`).
func manifestChunkNames(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil
	}
	list, ok := m["chunks"].([]any)
	if !ok {
		return nil
	}
	var names []string
	for _, c := range list {
		cm, ok := c.(map[string]any)
		if !ok {
			return nil
		}
		name, ok := chunkName(cm["name"])
		if !ok {
			return nil
		}
		if name == "" || strings.Contains(name, "/") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// chunkName renders a manifest chunk name: strings verbatim, numbers
// canonically and true as "true" (the shell interpolates scalars into the
// path), null/false read empty and skip, containers invalidate the whole
// manifest like the shell's jq error.
func chunkName(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", true
	case bool:
		if t {
			return "true", true
		}
		return "", true
	case string:
		return t, true
	default:
		if _, ok := NumVal(v); ok {
			return Tostring(v), true
		}
		return "", false
	}
}

// dedupeSorted sorts and deduplicates file paths, mirroring `sort -u`.
func dedupeSorted(files []string) []string {
	slices.Sort(files)
	return slices.Compact(files)
}

// rollupRecords applies the shared cost rollup filter to merged records.
// Nil marks a shape failure (a non-object record or model, mirroring the
// shell filter's jq error); callers warn and substitute zeros.
func rollupRecords(records []any) *jq.Obj {
	type tierAcc struct {
		runs          int
		input, output float64
	}
	tiers := []string{"cheap", "standard", "deep"}
	acc := map[string]*tierAcc{}
	for _, t := range tiers {
		acc[t] = &tierAcc{}
	}
	runs, withCost := 0, 0
	var input, output, cached, estSum float64
	estN := 0
	for _, rec := range records {
		runs++
		rm, ok := costRecordMap(rec)
		if !ok {
			return nil
		}
		tier := recordTier(rm)
		a := acc[tier]
		a.runs++
		models, ok := costModels(rm)
		if !ok {
			return nil
		}
		if len(models) > 0 {
			withCost++
		}
		for _, mm := range models {
			in, out := modelTokens(mm)
			a.input += in
			a.output += out
			input += in
			output += out
			cached += modelCached(mm)
		}
		if est, ok := recordEstimate(rm); ok {
			estSum += est
			estN++
		}
	}
	var estUSD any
	if estN > 0 {
		estUSD = round4(estSum)
	}
	byTier := jq.NewObj()
	for _, t := range tiers {
		a := acc[t]
		byTier.Set(t, jq.NewObj(
			"runs", a.runs,
			"input_tokens", a.input,
			"output_tokens", a.output,
			"total_tokens", a.input+a.output,
		))
	}
	return jq.NewObj(
		"runs", runs,
		"runs_with_cost", withCost,
		"input_tokens", input,
		"output_tokens", output,
		"total_tokens", input+output,
		"cached_input_tokens", cached,
		"est_usd", estUSD,
		"by_tier", byTier,
	)
}

// costRecordMap validates one cost record: null and objects read; anything
// else fails the rollup like the shell's jq error.
func costRecordMap(rec any) (map[string]any, bool) {
	if rec == nil {
		return map[string]any{}, true
	}
	m, ok := rec.(map[string]any)
	if !ok {
		return nil, false
	}
	return m, true
}

// costModels validates one record's models list: absent reads empty.
func costModels(rm map[string]any) ([]any, bool) {
	raw, ok := rm["models"]
	if !ok || raw == nil {
		return nil, true
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	for _, mm := range list {
		if mm == nil {
			continue
		}
		if _, ok := mm.(map[string]any); !ok {
			return nil, false
		}
	}
	return list, true
}

// modelTokens sums one model's numeric input/output tokens.
func modelTokens(mm any) (in, out float64) {
	m, ok := mm.(map[string]any)
	if !ok {
		return 0, 0
	}
	if f, ok := NumVal(m["input_tokens"]); ok {
		in = f
	}
	if f, ok := NumVal(m["output_tokens"]); ok {
		out = f
	}
	return in, out
}

// modelCached sums one model's numeric cache-split tokens (subsets of the
// input tokens, rolled into the top-level cached scalar).
func modelCached(mm any) float64 {
	m, ok := mm.(map[string]any)
	if !ok {
		return 0
	}
	var cached float64
	if f, ok := NumVal(m["cache_read_input_tokens"]); ok {
		cached += f
	}
	if f, ok := NumVal(m["cache_creation_input_tokens"]); ok {
		cached += f
	}
	return cached
}

// recordTier reads one record's effort tier.
func recordTier(rm map[string]any) string {
	eff, _ := rm["effort"]
	return EffortTier(eff)
}

// recordEstimate reads one record's numeric est_usd.
func recordEstimate(rm map[string]any) (float64, bool) {
	est, ok := rm["est_usd"]
	if !ok {
		return 0, false
	}
	return NumVal(est)
}

// round4 ports jq's (add * 10000 | round) / 10000.
func round4(f float64) float64 {
	return math.Round(f*roundScale) / roundScale
}

// roundScale is the est_usd rounding factor.
const roundScale = 10000

// costZeros ports the zero rollup for absent or unreadable cost stores.
func costZeros() *jq.Obj {
	byTier := jq.NewObj()
	for _, t := range []string{"cheap", "standard", "deep"} {
		byTier.Set(t, jq.NewObj(
			"runs", 0,
			"input_tokens", 0,
			"output_tokens", 0,
			"total_tokens", 0,
		))
	}
	return jq.NewObj(
		"runs", 0,
		"runs_with_cost", 0,
		"input_tokens", 0,
		"output_tokens", 0,
		"total_tokens", 0,
		"cached_input_tokens", 0,
		"est_usd", nil,
		"by_tier", byTier,
	)
}

// attachReceipts ports the budget.json receipts join: an array (or a
// single record wrapped in one) attaches as receipts; an unreadable file
// warns and leaves receipts null.
func attachReceipts(dir string, res *CostResult) {
	path := filepath.Join(dir, "budget.json")
	if !isRegular(path) {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		res.Warns = append(res.Warns, warnBudgetUnreadable)
		return
	}
	if arr, ok := doc.([]any); ok {
		res.Receipts = arr
	} else {
		res.Receipts = []any{doc}
	}
	res.Cost.Set("receipts", receiptsValue(res.Receipts))
}

// receiptsValue converts receipts to the envelope value.
func receiptsValue(receipts []any) any {
	out := make([]any, 0, len(receipts))
	return append(out, receipts...)
}
