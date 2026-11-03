package readcommon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// Explicit-usage handoff. The core never discovers usage on its own: the
// operator names one path via VERDICT_USAGE (a file or a directory of
// files) holding records in the schemas/usage.schema.json shape, and the
// budget gate joins them against past cost records by window. Vendor
// parsing lives outside the binary in examples/usage-adapters/.
const (
	usageEnv          = "VERDICT_USAGE"
	codeBadUsage      = "bad-usage"
	maxUsageFileBytes = 1 << 20
	jsonSuffix        = ".json"
	jsonlSuffix       = ".jsonl"
)

// ContractUsage is one validated usage record: the measurer's free-form
// source name, the model (when known), the token counts (when known), the
// session's priced USD (when the measurer priced it — the tool never prices
// tokens itself), and the window it covers.
type ContractUsage struct {
	Started    time.Time
	Ended      time.Time
	USD        *float64
	Source     string
	Model      string
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	HasTokens  bool
}

// usageDoc is the wire shape of one usage record. Unknown keys are ignored
// so newer writers stay readable; mistyped or missing required keys refuse
// at validation instead of degrading silently.
type usageDoc struct {
	SchemaVersion string       `json:"schema_version"`
	Model         string       `json:"model"`
	Tokens        *usageTokens `json:"tokens"`
	USD           *float64     `json:"usd"`
	Source        string       `json:"source"`
	StartedAt     string       `json:"started_at"`
	EndedAt       string       `json:"ended_at"`
}

// usageTokens is the wire shape of one record's token counts. Input and
// output are pointers so a present-but-unpriced tokens object still
// validates; a missing tokens object reads as unknown, never zero.
type usageTokens struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

// ReadContractUsage reads the usage records the operator explicitly handed
// over via VERDICT_USAGE: one path holding our-format files (a single
// .json object or array, or a .jsonl log with one object per line), or a
// directory whose *.json/*.jsonl entries each read the same way. An unset
// variable — or a handed directory holding no usage files yet — reads as no
// measurement, so the gate falls back to the self-reported record. A named
// path that is missing, oversized, or malformed is a typed refusal: the
// operator named it, so a broken handoff must never silently degrade.
func ReadContractUsage(env map[string]string) ([]ContractUsage, error) {
	spec := env[usageEnv]
	if spec == "" {
		return nil, nil
	}
	info, err := os.Stat(spec)
	if err != nil {
		return nil, usageUnreadable(spec, err)
	}
	if !info.IsDir() {
		return readUsageFile(spec)
	}
	return readUsageDir(spec)
}

// usageUnreadable refuses a handed path the tool cannot read: the operator
// named it, so a broken handoff must never silently degrade.
func usageUnreadable(path string, err error) error {
	return &core.RefusalError{Code: codeBadUsage, Message: fmt.Sprintf(
		"VERDICT_USAGE %q is not readable: %v", path, err)}
}

// readUsageDir reads the usage files directly under dir in stable order.
// Non-usage entries are ignored; an empty handed directory reads as no
// measurement.
func readUsageDir(dir string) ([]ContractUsage, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, usageUnreadable(dir, err)
	}
	var out []ContractUsage
	for _, name := range usageNames(entries) {
		records, err := readUsageFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, records...)
	}
	return out, nil
}

// usageNames lists the usage files directly under a read directory in
// stable order, ignoring subdirectories and non-usage entries.
func usageNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (!strings.HasSuffix(name, jsonSuffix) && !strings.HasSuffix(name, jsonlSuffix)) {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// readUsageFile reads one handed usage file: a .json single object or
// array, or a .jsonl log with one object per line.
func readUsageFile(path string) ([]ContractUsage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, usageUnreadable(path, err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxUsageFileBytes {
		return nil, &core.RefusalError{Code: codeBadUsage, Message: fmt.Sprintf(
			"VERDICT_USAGE %q is not a readable usage file (want a regular file under %d bytes)", path, maxUsageFileBytes)}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, usageUnreadable(path, err)
	}
	if strings.HasSuffix(path, jsonlSuffix) {
		return decodeUsageLines(raw, path)
	}
	return decodeUsageJSON(raw, path)
}

// decodeUsageJSON decodes one .json usage file: a single record object or
// an array of them. Trailing data after the document refuses.
func decodeUsageJSON(raw []byte, path string) ([]ContractUsage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, usageRefusal(path, 0, "file is empty")
	}
	if trimmed[0] == '[' {
		var docs []usageDoc
		if err := decodeUsageValue(trimmed, path, &docs); err != nil {
			return nil, err
		}
		return validateUsageDocs(docs, path)
	}
	var doc usageDoc
	if err := decodeUsageValue(trimmed, path, &doc); err != nil {
		return nil, err
	}
	return validateUsageDocs([]usageDoc{doc}, path)
}

// decodeUsageLines decodes one .jsonl usage log: one record object per
// non-blank line.
func decodeUsageLines(raw []byte, path string) ([]ContractUsage, error) {
	var docs []usageDoc
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var doc usageDoc
		if err := decodeUsageValue(line, path, &doc); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, usageRefusal(path, 0, "file holds no usage records")
	}
	return validateUsageDocs(docs, path)
}

// decodeUsageValue decodes one JSON value strictly (no trailing data),
// reporting mistyped fields as a usage refusal.
func decodeUsageValue(raw []byte, path string, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(value); err != nil {
		return usageRefusal(path, 0, fmt.Sprintf("not usage JSON: %v", err))
	}
	if decoder.More() {
		return usageRefusal(path, 0, "trailing data after the usage document")
	}
	return nil
}

// validateUsageDocs validates every decoded record in file order.
func validateUsageDocs(docs []usageDoc, path string) ([]ContractUsage, error) {
	out := make([]ContractUsage, 0, len(docs))
	for i := range docs {
		record, err := docs[i].validated(path, i+1)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

// validated checks one decoded record against the usage contract: the
// schema stamp, a non-empty source, a parseable window that does not run
// backwards, and non-negative numbers. Tokens without a priced total stay
// valid — they read as no measurement, never a measured claim.
func (d *usageDoc) validated(path string, index int) (ContractUsage, error) {
	if d.SchemaVersion != "1" {
		return ContractUsage{}, usageRefusal(path, index, "schema_version must be \"1\"")
	}
	if d.Source == "" {
		return ContractUsage{}, usageRefusal(path, index, "source is required")
	}
	started, ended, err := parseUsageWindow(d, path, index)
	if err != nil {
		return ContractUsage{}, err
	}
	if err := checkPrice(d, path, index); err != nil {
		return ContractUsage{}, err
	}
	tokens, hasTokens, err := countedUsage(d, path, index)
	if err != nil {
		return ContractUsage{}, err
	}
	var record ContractUsage
	record.Source, record.Model = d.Source, d.Model
	record.Started, record.Ended = started, ended
	if d.USD != nil {
		usd := *d.USD
		record.USD = &usd
	}
	record.Input, record.Output = tokens.Input, tokens.Output
	record.CacheRead, record.CacheWrite = tokens.CacheRead, tokens.CacheWrite
	record.HasTokens = hasTokens
	return record, nil
}

// parseUsageWindow parses one record's window: two RFC 3339 stamps that do
// not run backwards.
func parseUsageWindow(d *usageDoc, path string, index int) (time.Time, time.Time, error) {
	started, err := time.Parse(time.RFC3339Nano, d.StartedAt)
	if err != nil {
		return time.Time{}, time.Time{}, usageRefusal(path, index, fmt.Sprintf("started_at %q is not RFC 3339", d.StartedAt))
	}
	ended, err := time.Parse(time.RFC3339Nano, d.EndedAt)
	if err != nil {
		return time.Time{}, time.Time{}, usageRefusal(path, index, fmt.Sprintf("ended_at %q is not RFC 3339", d.EndedAt))
	}
	if ended.Before(started) {
		return time.Time{}, time.Time{}, usageRefusal(path, index, "ended_at runs before started_at")
	}
	return started, ended, nil
}

// checkPrice refuses a negative priced total.
func checkPrice(d *usageDoc, path string, index int) error {
	if d.USD != nil && *d.USD < 0 {
		return usageRefusal(path, index, "usd must not be negative")
	}
	return nil
}

// countedUsage validates one record's tokens object, if present.
func countedUsage(d *usageDoc, path string, index int) (ContractUsage, bool, error) {
	if d.Tokens == nil {
		return ContractUsage{}, false, nil
	}
	tokens, err := d.Tokens.validated(path, index)
	if err != nil {
		return ContractUsage{}, false, err
	}
	return tokens, true, nil
}

// validated checks one decoded tokens object: input and output are
// required when tokens are present, every count non-negative.
func (t *usageTokens) validated(path string, index int) (ContractUsage, error) {
	if t.Input == nil || t.Output == nil {
		return ContractUsage{}, usageRefusal(path, index, "tokens wants input and output numbers")
	}
	if err := nonNegativeCounts(path, index, t.Input, t.Output, t.CacheRead, t.CacheWrite); err != nil {
		return ContractUsage{}, err
	}
	var record ContractUsage
	record.Input, record.Output = *t.Input, *t.Output
	if t.CacheRead != nil {
		record.CacheRead = *t.CacheRead
	}
	if t.CacheWrite != nil {
		record.CacheWrite = *t.CacheWrite
	}
	return record, nil
}

// nonNegativeCounts refuses when any present token count runs negative.
func nonNegativeCounts(path string, index int, counts ...*float64) error {
	for _, count := range counts {
		if count != nil && *count < 0 {
			return usageRefusal(path, index, "token counts must not be negative")
		}
	}
	return nil
}

// usageRefusal builds the typed refusal for a malformed handoff: the file
// plus the 1-based record index (0 for whole-file failures) and the cause.
func usageRefusal(path string, index int, cause string) error {
	where := path
	if index > 0 {
		where = fmt.Sprintf("%s record %d", path, index)
	}
	return &core.RefusalError{Code: codeBadUsage, Message: fmt.Sprintf(
		"VERDICT_USAGE %s: %s", where, cause)}
}

// MatchContractUsage returns the measured session whose window brackets at,
// nearest end first (a cost record is written at the end of a run, so the
// session that contains it is the run that produced it). No containing
// session reads as no measurement.
func MatchContractUsage(usage []ContractUsage, at time.Time) *ContractUsage {
	var best *ContractUsage
	for i := range usage {
		record := &usage[i]
		if !record.contains(at) {
			continue
		}
		if best == nil || record.closer(at, best) {
			best = record
		}
	}
	return best
}

// contains reports whether at falls inside the session window.
func (u *ContractUsage) contains(at time.Time) bool {
	if u.Started.IsZero() || u.Ended.IsZero() {
		return false
	}
	return !at.Before(u.Started) && !at.After(u.Ended)
}

// closer reports whether u should win a containment tie against best: the
// session that ends nearest at wins, then the later start.
func (u *ContractUsage) closer(at time.Time, best *ContractUsage) bool {
	gap, bestGap := u.Ended.Sub(at), best.Ended.Sub(at)
	if gap != bestGap {
		return gap < bestGap
	}
	return u.Started.After(best.Started)
}
