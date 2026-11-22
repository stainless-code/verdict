// Package ingestcore implements `verdict ingest`: validating and mapping
// untrusted SARIF 2.1.0 documents into the shared findings.json store.
package ingestcore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/sarif"
	"github.com/stainless-code/verdict/internal/untrusted"
)

const (
	// MaxSARIFBytes bounds untrusted SARIF input at 8 MiB.
	MaxSARIFBytes = 8 << 20

	sarifVersion210 = "2.1.0"
	codeBadSARIF    = "bad-sarif"

	// SourceSARIF names the one ingest source format this build reads. The
	// flag is `--from sarif` so a caller names a format, never a vendor.
	SourceSARIF = "sarif"
)

// Skip reasons published on the ingest envelope's skipped[] rows: one honest
// label per result that could not be anchored, so nothing is dropped silently
// and no finding is stored at line 0.
const (
	SkipNoLocation      = "no-location"
	SkipInvalidLine     = "invalid-line"
	SkipPathEscapesRepo = "path-escapes-repo"
	SkipOutOfDiff       = "out-of-diff"
	SkipAmbiguousPath   = "ambiguous-path"
	SkipSnippetMismatch = "snippet-mismatch"
)

type sarifDocument struct {
	Version any        `json:"version"`
	Schema  *string    `json:"$schema"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    *sarifTool       `json:"tool"`
	Results *json.RawMessage `json:"results"`
}

type sarifTool struct {
	Driver *sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	Rules   []sarifRule `json:"rules"`
}

type sarifRule struct {
	DefaultConfiguration *sarifRuleConfiguration `json:"defaultConfiguration"`
	Properties           map[string]any          `json:"properties"`
	ID                   string                  `json:"id"`
}

type sarifRuleConfiguration struct {
	Level string `json:"level"`
}

type sarifResult struct {
	RuleIndex           *int              `json:"ruleIndex"`
	Message             *sarifMessage     `json:"message"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Fingerprints        map[string]string `json:"fingerprints"`
	Properties          map[string]any    `json:"properties"`
	RuleID              string            `json:"ruleId"`
	Level               string            `json:"level"`
	Locations           []sarifLocation   `json:"locations"`
}

type sarifMessage struct {
	Text     string `json:"text"`
	Markdown string `json:"markdown"`
}

type sarifLocation struct {
	PhysicalLocation *sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation *sarifArtifactLocation `json:"artifactLocation"`
	Region           *sarifRegion           `json:"region"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	Snippet     *sarifSnippet `json:"snippet"`
	StartLine   int           `json:"startLine"`
	EndLine     int           `json:"endLine"`
	StartColumn int           `json:"startColumn"`
}

// sarifSnippet is the region's reported source text. It is evidence to check
// against the pinned diff, never text to trust: a result whose snippet
// disagrees with the diff line it names is skipped, not re-pointed.
type sarifSnippet struct {
	Text string `json:"text"`
}

type parsedResult struct {
	Tool        string
	RuleID      string
	Severity    string
	Path        string
	Body        string
	Snippet     string
	SkipReason  string
	Fingerprint string
	VerdictSig  string
	StartLine   int
	EndLine     int
	StartColumn int
}

type parseOutput struct {
	Results  []parsedResult
	Tools    []string
	Warnings []string
}

func badSARIF(msg string) *core.RefusalError {
	return &core.RefusalError{Code: codeBadSARIF, Message: msg}
}

// parseSARIF decodes, validates, and normalizes an untrusted SARIF 2.1.0 byte
// slice without consulting the filesystem or executing any URI or rule text.
func parseSARIF(raw []byte) (*parseOutput, *core.RefusalError) {
	if !utf8.Valid(raw) {
		return nil, badSARIF("SARIF input is not valid UTF-8")
	}
	doc, ref := decodeSARIFDoc(raw)
	if ref != nil {
		return nil, ref
	}
	out := &parseOutput{}
	var drivers []string
	seenWarnings := map[string]bool{}
	for i := range doc.Runs {
		if ref := appendRunResults(i, &doc.Runs[i], out, &drivers, seenWarnings); ref != nil {
			return nil, ref
		}
	}
	out.Tools = ingestedTools(out.Results, drivers)
	return out, nil
}

// ingestedTools names the analyzers whose results landed: the effective tool of
// each result, so a `report --sarif` round trip reports the tool its provenance
// restored (`semgrep`) instead of the driver it arrived under (`verdict`). A
// document with no results still names its drivers, so an empty scan says who
// ran.
func ingestedTools(results []parsedResult, drivers []string) []string {
	seen := make(map[string]bool, len(results)+len(drivers))
	names := make([]string, 0, len(results))
	for i := range results {
		names = appendTool(names, seen, results[i].Tool)
	}
	if len(names) == 0 {
		for _, driver := range drivers {
			names = appendTool(names, seen, driver)
		}
	}
	slices.Sort(names)
	return names
}

// appendTool adds one tool name once, ignoring an empty one.
func appendTool(names []string, seen map[string]bool, name string) []string {
	if name == "" || seen[name] {
		return names
	}
	seen[name] = true
	return append(names, name)
}

func appendRunResults(
	idx int, run *sarifRun, out *parseOutput, drivers *[]string, seenWarnings map[string]bool,
) *core.RefusalError {
	tool, results, warns, ref := parseRun(idx, run)
	if ref != nil {
		return ref
	}
	*drivers = append(*drivers, tool)
	for _, w := range warns {
		if !seenWarnings[w] {
			seenWarnings[w] = true
			out.Warnings = append(out.Warnings, w)
		}
	}
	out.Results = append(out.Results, results...)
	return nil
}

func decodeSARIFDoc(raw []byte) (*sarifDocument, *core.RefusalError) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, badSARIF("SARIF input must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var doc sarifDocument
	if err := dec.Decode(&doc); err != nil {
		return nil, badSARIF(fmt.Sprintf("invalid SARIF JSON: %v", err))
	}
	if err := rejectTrailingTokens(dec); err != nil {
		return nil, err
	}
	ver, ok := doc.Version.(string)
	if !ok || ver != sarifVersion210 {
		return nil, badSARIF(fmt.Sprintf("SARIF version must be %q (got %v)", sarifVersion210, doc.Version))
	}
	if doc.Runs == nil {
		return nil, badSARIF("SARIF document must contain a runs array")
	}
	return &doc, nil
}

func rejectTrailingTokens(dec *json.Decoder) *core.RefusalError {
	var extra any
	err := dec.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return badSARIF("SARIF input has unexpected trailing data after the root object")
}

func parseRun(idx int, run *sarifRun) (tool string, results []parsedResult, warns []string, ref *core.RefusalError) {
	tool, ref = extractToolName(idx, run)
	if ref != nil {
		return "", nil, nil, ref
	}
	rawResults, ref := decodeRunResults(idx, run.Results)
	if ref != nil {
		return "", nil, nil, ref
	}
	rules := run.Tool.Driver.Rules
	results = make([]parsedResult, 0, len(rawResults))
	for j := range rawResults {
		res, warn, ref := parseResultItem(idx, j, tool, rules, &rawResults[j])
		if ref != nil {
			return "", nil, nil, ref
		}
		if warn != "" {
			warns = append(warns, warn)
		}
		results = append(results, res)
	}
	return tool, results, warns, nil
}

func extractToolName(idx int, run *sarifRun) (string, *core.RefusalError) {
	if run.Tool == nil || run.Tool.Driver == nil {
		return "", badSARIF(fmt.Sprintf("SARIF runs[%d] must contain tool.driver.name", idx))
	}
	tool := strings.TrimSpace(run.Tool.Driver.Name)
	if tool == "" {
		return "", badSARIF(fmt.Sprintf("SARIF runs[%d].tool.driver.name must not be empty", idx))
	}
	return tool, nil
}

func decodeRunResults(idx int, raw *json.RawMessage) ([]sarifResult, *core.RefusalError) {
	trimmed, skip := trimResultsRaw(raw)
	if skip {
		return nil, nil
	}
	if trimmed[0] != '[' {
		return nil, badSARIF(fmt.Sprintf("SARIF runs[%d].results must be an array", idx))
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var results []sarifResult
	if err := dec.Decode(&results); err != nil {
		return nil, badSARIF(fmt.Sprintf("invalid SARIF runs[%d].results: %v", idx, err))
	}
	for j, r := range results {
		if r.Message == nil {
			return nil, badSARIF(fmt.Sprintf("SARIF runs[%d].results[%d] must contain message", idx, j))
		}
	}
	return results, nil
}

func trimResultsRaw(raw *json.RawMessage) ([]byte, bool) {
	if raw == nil {
		return nil, true
	}
	trimmed := bytes.TrimSpace(*raw)
	return trimmed, len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func parseResultItem(runIdx, resIdx int, tool string, rules []sarifRule, raw *sarifResult) (parsedResult, string, *core.RefusalError) {
	rule, ruleID, ref := resolveRule(runIdx, resIdx, rules, raw)
	if ref != nil {
		return parsedResult{}, "", ref
	}
	tool, ruleID = provenanceLabels(tool, ruleID, raw.Properties)
	body, ref := extractMessage(runIdx, resIdx, raw.Message)
	if ref != nil {
		return parsedResult{}, "", ref
	}
	cleanPath, startLine, endLine, startCol, snippet, skipReason := extractLocation(raw.Locations)
	level, secSev := effectiveSeverityInputs(raw, rule)
	tier, warn := sarif.TierForResult(level, secSev)
	verdictSig, fp := deriveFingerprint(tool, ruleID, cleanPath, startLine, startCol, body, raw.PartialFingerprints, raw.Fingerprints)
	if reported := propertyString(raw.Properties, "fingerprint"); reported != "" {
		fp = reported
	}
	return parsedResult{
		Tool:        tool,
		RuleID:      ruleID,
		Severity:    tier,
		Path:        cleanPath,
		Body:        body,
		Snippet:     snippet,
		SkipReason:  skipReason,
		Fingerprint: fp,
		VerdictSig:  verdictSig,
		StartLine:   startLine,
		EndLine:     endLine,
		StartColumn: startCol,
	}, warn, nil
}

func resolveRule(runIdx, resIdx int, rules []sarifRule, raw *sarifResult) (*sarifRule, string, *core.RefusalError) {
	ruleID := strings.TrimSpace(raw.RuleID)
	if raw.RuleIndex != nil {
		ri := *raw.RuleIndex
		if ri < 0 || ri >= len(rules) {
			return nil, "", badSARIF(fmt.Sprintf("SARIF runs[%d].results[%d].ruleIndex %d out of range", runIdx, resIdx, ri))
		}
		rule := &rules[ri]
		if ruleID == "" {
			ruleID = strings.TrimSpace(rule.ID)
		}
		return rule, ruleID, nil
	}
	return findRuleByID(rules, ruleID), ruleID, nil
}

func findRuleByID(rules []sarifRule, ruleID string) *sarifRule {
	if ruleID == "" {
		return nil
	}
	for i := range rules {
		if strings.TrimSpace(rules[i].ID) == ruleID {
			return &rules[i]
		}
	}
	return nil
}

func extractMessage(runIdx, resIdx int, msg *sarifMessage) (string, *core.RefusalError) {
	if msg == nil {
		return "", badSARIF(fmt.Sprintf("SARIF runs[%d].results[%d] missing message", runIdx, resIdx))
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Markdown)
	}
	if text == "" {
		return "", badSARIF(fmt.Sprintf("SARIF runs[%d].results[%d].message.text must not be empty", runIdx, resIdx))
	}
	return untrusted.NeutralizeMentions(text), nil
}

// extractLocation reads the first location as a candidate anchor: the
// repo-relative path, the line range, and the reported snippet. A location the
// bundle cannot anchor is not fatal — a bad URI in one result must not refuse a
// whole scan — so the caller receives a typed skip reason instead of a
// refusal: no location, an unusable line, or a URI that escapes the repository.
// Nothing here touches the filesystem, opens a URI, or trusts a scheme.
func extractLocation(
	locs []sarifLocation,
) (cleanPath string, startLine, endLine, startCol int, snippet, skipReason string) {
	pl := firstPhysicalLocation(locs)
	if pl == nil {
		return "", 0, 0, 0, "", SkipNoLocation
	}
	cleanPath, skipReason = cleanLocationPath(pl.ArtifactLocation)
	startLine, endLine, startCol = regionBounds(pl.Region)
	snippet = regionSnippet(pl.Region)
	if skipReason == "" && unusableRegion(pl.Region, startLine, startCol) {
		skipReason = SkipInvalidLine
	}
	return cleanPath, startLine, endLine, startCol, snippet, skipReason
}

// firstPhysicalLocation returns the first location's physical location, the
// only one SARIF orders as primary.
func firstPhysicalLocation(locs []sarifLocation) *sarifPhysicalLocation {
	if len(locs) == 0 {
		return nil
	}
	return locs[0].PhysicalLocation
}

// cleanLocationPath normalizes the artifact URI. A refused URI keeps its raw
// text so the skip row names what the scanner reported.
func cleanLocationPath(loc *sarifArtifactLocation) (string, string) {
	if loc == nil || strings.TrimSpace(loc.URI) == "" {
		return "", SkipNoLocation
	}
	cleaned, err := sarif.CleanURI(loc.URI)
	if err != nil {
		return loc.URI, SkipPathEscapesRepo
	}
	return cleaned, ""
}

// regionSnippet reads the reported source text, trimmed; it is evidence to
// check against the diff, never text to trust.
func regionSnippet(reg *sarifRegion) string {
	if reg == nil || reg.Snippet == nil {
		return ""
	}
	return strings.TrimSpace(reg.Snippet.Text)
}

// unusableRegion reports a region no finding can point at: absent, a
// non-positive start line, a negative column, or a negative end line.
func unusableRegion(reg *sarifRegion, startLine, startCol int) bool {
	return reg == nil || startLine <= 0 || startCol < 0 || reg.EndLine < 0
}

// regionBounds reads a region's line span, clamping an absent or inverted end
// line to the start line. It reports raw negatives; the caller grades them.
func regionBounds(reg *sarifRegion) (startLine, endLine, startCol int) {
	if reg == nil {
		return 0, 0, 0
	}
	endLine = max(reg.EndLine, reg.StartLine)
	return reg.StartLine, endLine, reg.StartColumn
}

// provenanceLabels reads the labels verdict's own `report --sarif` export
// writes under properties, so a round-tripped finding keeps the tool and rule
// that reported it instead of being relabeled as verdict's. Every other
// scanner's properties are read for these exact keys only; they carry
// provenance, never path or line authority.
func provenanceLabels(tool, ruleID string, props map[string]any) (string, string) {
	if v := propertyString(props, keyTool); v != "" {
		tool = v
	}
	if v := propertyString(props, "rule_id"); v != "" {
		ruleID = v
	}
	return tool, ruleID
}

func propertyString(props map[string]any, key string) string {
	v, ok := props[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func effectiveSeverityInputs(raw *sarifResult, rule *sarifRule) (level string, secSev any) {
	level = strings.TrimSpace(raw.Level)
	if level == "" && rule != nil && rule.DefaultConfiguration != nil {
		level = strings.TrimSpace(rule.DefaultConfiguration.Level)
	}
	secSev = lookupSecSeverity(raw.Properties)
	if secSev == nil && rule != nil {
		secSev = lookupSecSeverity(rule.Properties)
	}
	return level, secSev
}

func lookupSecSeverity(props map[string]any) any {
	if props == nil {
		return nil
	}
	if v, ok := props["security-severity"]; ok && v != nil {
		return v
	}
	return nil
}

func deriveFingerprint(
	tool, ruleID, cleanPath string, startLine, startCol int,
	body string, partial, full map[string]string,
) (verdictSig, fp string) {
	verdictSig = strings.TrimSpace(partial["verdict/sig"])
	if canon := canonicalFP(partial); canon != "" {
		return verdictSig, canon
	}
	if canon := canonicalFP(full); canon != "" {
		return verdictSig, canon
	}
	var seed string
	if cleanPath != "" {
		seed = strings.Join([]string{
			tool, ruleID, cleanPath, strconv.Itoa(startLine), strconv.Itoa(startCol),
		}, "\x00")
	} else {
		seed = strings.Join([]string{tool, ruleID, body}, "\x00")
	}
	sum := sha256.Sum256([]byte(seed))
	return verdictSig, "sha256:" + hex.EncodeToString(sum[:])
}

func canonicalFP(m map[string]string) string {
	keys := nonEmptyMapKeys(m)
	if len(keys) == 0 {
		return ""
	}
	slices.Sort(keys)
	if len(keys) == 1 {
		return strings.TrimSpace(m[keys[0]])
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strings.TrimSpace(m[k]))
	}
	return jq.StrOf(strings.Join(parts, ";"))
}

func nonEmptyMapKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			keys = append(keys, k)
		}
	}
	return keys
}
