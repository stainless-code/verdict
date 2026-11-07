package readcommon

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// Cost rollup warning lines, frozen as the wire contract.
const (
	warnCostUnreadable   = "cost.json unreadable — spend unrecorded for this rollup"
	warnChunkUnreadable  = "chunk cost %s unreadable — spend unrecorded for this chunk"
	warnBudgetUnreadable = "budget.json unreadable — receipts unlisted for this rollup"
)

const (
	tierCheap    = "cheap"
	tierStandard = "standard"
	tierDeep     = "deep"
)

const (
	keyRuns         = "runs"
	keyInputTokens  = "input_tokens"
	keyOutputTokens = "output_tokens"
	keyTotalTokens  = "total_tokens"
	keyEstUSD       = "est_usd"
	slash           = "/"
)

// CostResult carries the --cost rollup: the envelope-ready cost object (nil
// when the flag is absent), the receipts array (nil unless a budget file
// parsed), and warnings in shell order.
type CostResult struct {
	Cost     *jq.Obj
	Receipts []any
	Warns    []string
}

// CostRollup owns the report --cost rollup: the top-level cost.json plus
// every per-chunk <name>.cost.json merged through one rollup filter, then
// the budget.json receipts join. Absent files contribute zeros silently;
// unparseable files warn and contribute zeros — neither ever fails the run.
func CostRollup(dir string) CostResult {
	res := CostResult{}
	top, topPresent, topParsed := readCostFile(filepath.Join(dir, CostFile))
	chunkFiles := chunkCostFiles(dir)
	if len(chunkFiles) == 0 {
		res.Cost = rollupTop(top, topPresent, topParsed, &res)
		attachReceipts(dir, &res)
		return res
	}
	merged := append(append([]any{}, top...), mergeChunkRecords(chunkFiles, &res)...)
	if topPresent {
		if _, ok := rollupOne(top, topParsed); !ok {
			res.Warns = append(res.Warns, warnCostUnreadable)
		}
	}
	if c := rollupRecords(merged); c != nil {
		res.Cost = c
	} else {
		res.Warns = append(res.Warns, warnCostUnreadable)
		res.Cost = costZeros()
	}
	attachReceipts(dir, &res)
	return res
}

func mergeChunkRecords(chunkFiles []string, res *CostResult) []any {
	var merged []any
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
	return merged
}

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

func chunkWarn(path string) string {
	base := path
	if _, after, ok := strings.CutLast(path, slash); ok {
		base = after
	}
	return "chunk cost " + base + " unreadable — spend unrecorded for this chunk"
}

// CostFile is the bundle's agent-written spend record, read by the report
// rollup, the trend rollup, and the provenance footer.
const CostFile = "cost.json"

// CostFiles lists every cost record a bundle carries: the bundle record
// first, then each per-chunk record, deduplicated and sorted within the
// chunk group. The rollup and the `record --cost-names` repair read the same
// list, so neither can miss a file the other names.
func CostFiles(dir string) []string {
	return append([]string{dir + "/" + CostFile}, chunkCostFiles(dir)...)
}

// CostRecords reads a bundle's spend record: an
// array passes, a single object reads as one record, and an absent,
// oversized, or unparsable store reads nil (the callers degrade, never fail).
func CostRecords(dir string) []any {
	recs, _, _ := readCostFile(filepath.Join(dir, CostFile))
	return recs
}

// CostTier reads one record's effort tier as cheap, standard, or deep;
// everything absent, falsey, or unknown reads standard.
func CostTier(rec any) string {
	m, ok := rec.(map[string]any)
	if !ok {
		return tierStandard
	}
	return EffortTier(m["effort"])
}

// CostEstimate reads one record's numeric est_usd.
func CostEstimate(rec any) (float64, bool) {
	m, ok := rec.(map[string]any)
	if !ok {
		return 0, false
	}
	est, ok := m["est_usd"]
	if !ok {
		return 0, false
	}
	return NumVal(est)
}

// CostModels reads one record's models list: only an array passes, and an
// absent or null list reads empty. Every non-null element must be an object
// (the shape filter).
//
// An object-shaped list fails the shape gate rather than being read by key
// order: the keys are the only place a model's name existed, so coercing the
// values would drop the identity the writer gave and leave an unattributed
// entry that still priced. A failing record reads as unreadable spend, which
// the rollup names and never fails on.
func CostModels(rec any) ([]any, bool) {
	m, ok := rec.(map[string]any)
	if !ok {
		if rec == nil {
			return nil, true
		}
		return nil, false
	}
	raw, ok := m["models"]
	if !ok || raw == nil {
		return nil, true
	}
	list, ok := raw.([]any)
	if !ok || !allModelMaps(list) {
		return nil, false
	}
	return list, true
}

// readCostFile reads one cost file: records plus its present/parsed
// tri-state. A non-regular path reads absent;
// invalid JSON reads present-but-unparseable. Records that fail shape
// validation survive the read and fail at rollup time (the two-stage
// filter).
func readCostFile(path string) (recs []any, present, parsed bool) {
	if !IsFile(path) {
		return nil, false, false
	}
	raw, err := jq.CappedFile(path, jq.MaxCostBytes)
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

func chunkCostFiles(dir string) []string {
	chunksDir := dir + "/chunks"
	if !IsDir(chunksDir) {
		return nil
	}
	var files []string
	for _, name := range manifestChunkNames(chunksDir + "/manifest.json") {
		files = append(files, chunksDir+"/"+name+".cost.json")
	}
	return dedupeSorted(appendChunkGlob(chunksDir, files))
}

func appendChunkGlob(chunksDir string, files []string) []string {
	entries, err := os.ReadDir(chunksDir)
	if err != nil {
		return files
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
	return files
}

// manifestChunkNames reads the manifest's chunk names ("" when the manifest
// is absent or unreadable, mirroring `|| true`).
func manifestChunkNames(path string) []string {
	list, ok := manifestChunkList(path)
	if !ok {
		return nil
	}
	var names []string
	for _, c := range list {
		name, ok := manifestChunkName(c)
		if !ok {
			return nil
		}
		if name == "" || strings.Contains(name, slash) {
			continue
		}
		names = append(names, name)
	}
	return names
}

func manifestChunkList(path string) ([]any, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil, false
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil, false
	}
	list, ok := m["chunks"].([]any)
	if !ok {
		return nil, false
	}
	return list, true
}

func manifestChunkName(c any) (string, bool) {
	cm, ok := c.(map[string]any)
	if !ok {
		return "", false
	}
	return chunkName(cm["name"])
}

// chunkName renders a manifest chunk name: strings verbatim, numbers
// canonically and true as "true" (scalars interpolate into the path),
// null/false read empty and skip, and containers invalidate the whole
// manifest.
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

func dedupeSorted(files []string) []string {
	slices.Sort(files)
	return slices.Compact(files)
}

type tierAcc struct {
	runs          int
	input, output float64
}

type costSums struct {
	runs, withCost, estN          int
	input, output, cached, estSum float64
}

func rollupRecords(records []any) *jq.Obj {
	tiers := []string{tierCheap, tierStandard, tierDeep}
	acc := map[string]*tierAcc{}
	for _, t := range tiers {
		acc[t] = &tierAcc{}
	}
	var sum costSums
	for _, rec := range records {
		if !foldRecord(rec, acc, &sum) {
			return nil
		}
	}
	var estUSD any
	if sum.estN > 0 {
		estUSD = Round4(sum.estSum)
	}
	byTier := jq.NewObj()
	for _, t := range tiers {
		a := acc[t]
		byTier.Set(t, jq.NewObj(
			keyRuns, a.runs,
			keyInputTokens, a.input,
			keyOutputTokens, a.output,
			keyTotalTokens, a.input+a.output,
		))
	}
	// total_tokens is appended after by_tier (`.total_tokens = ...`
	// past the object construction), so the key lands last: insertion
	// order is the wire order.
	obj := jq.NewObj(
		keyRuns, sum.runs,
		"runs_with_cost", sum.withCost,
		keyInputTokens, sum.input,
		keyOutputTokens, sum.output,
		"cached_input_tokens", sum.cached,
		keyEstUSD, estUSD,
		"by_tier", byTier,
	)
	obj.Set(keyTotalTokens, sum.input+sum.output)
	return obj
}

func foldRecord(rec any, acc map[string]*tierAcc, sum *costSums) bool {
	sum.runs++
	rm, ok := costRecordMap(rec)
	if !ok {
		return false
	}
	a := acc[CostTier(rm)]
	a.runs++
	models, ok := CostModels(rm)
	if !ok {
		return false
	}
	if len(models) > 0 {
		sum.withCost++
	}
	for _, mm := range models {
		in, out := modelTokens(mm)
		a.input += in
		a.output += out
		sum.input += in
		sum.output += out
		sum.cached += modelCached(mm)
	}
	if est, ok := CostEstimate(rm); ok {
		sum.estSum += est
		sum.estN++
	}
	return true
}

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

func allModelMaps(list []any) bool {
	for _, mm := range list {
		if mm == nil {
			continue
		}
		if _, ok := mm.(map[string]any); !ok {
			return false
		}
	}
	return true
}

func modelTokens(mm any) (in, out float64) {
	m, ok := mm.(map[string]any)
	if !ok {
		return 0, 0
	}
	if f, ok := NumVal(m[keyInputTokens]); ok {
		in = f
	}
	if f, ok := NumVal(m[keyOutputTokens]); ok {
		out = f
	}
	return in, out
}

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

// Round4 rounds to four decimals: (add * 10000 | round) / 10000.
func Round4(f float64) float64 {
	return math.Round(f*roundScale) / roundScale
}

const roundScale = 10000

func costZeros() *jq.Obj {
	byTier := jq.NewObj()
	for _, t := range []string{tierCheap, tierStandard, tierDeep} {
		byTier.Set(t, jq.NewObj(
			keyRuns, 0,
			keyInputTokens, 0,
			keyOutputTokens, 0,
			keyTotalTokens, 0,
		))
	}
	return jq.NewObj(
		keyRuns, 0,
		"runs_with_cost", 0,
		keyInputTokens, 0,
		keyOutputTokens, 0,
		keyTotalTokens, 0,
		"cached_input_tokens", 0,
		keyEstUSD, nil,
		"by_tier", byTier,
	)
}

// attachReceipts owns the budget.json receipts join: an array (or a
// single record wrapped in one) attaches as receipts; an unreadable file
// warns and leaves receipts null. Receipts decode ordered: the envelope
// re-emits them in file key order.
func attachReceipts(dir string, res *CostResult) {
	path := filepath.Join(dir, "budget.json")
	if !IsFile(path) {
		return
	}
	raw, err := jq.CappedFile(path, jq.MaxCostBytes)
	if err != nil {
		return
	}
	doc, err := jq.DecodeOrdered(raw)
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

func receiptsValue(receipts []any) any {
	out := make([]any, 0, len(receipts))
	return append(out, receipts...)
}
