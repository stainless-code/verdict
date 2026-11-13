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
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/sarif"
)

const (
	// MaxSARIFBytes bounds untrusted SARIF input at 8 MiB.
	MaxSARIFBytes = 8 << 20

	sarifVersion210 = "2.1.0"
	codeBadSARIF    = "bad-sarif"
)

type sarifDocument struct {
	Schema  *string    `json:"$schema"`
	Version any        `json:"version"`
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
	Name  string      `json:"name"`
	Rules []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID                   string                  `json:"id"`
	DefaultConfiguration *sarifRuleConfiguration `json:"defaultConfiguration"`
	Properties           map[string]any          `json:"properties"`
}

type sarifRuleConfiguration struct {
	Level string `json:"level"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	RuleIndex           *int              `json:"ruleIndex"`
	Level               string            `json:"level"`
	Message             *sarifMessage     `json:"message"`
	Locations           []sarifLocation   `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Fingerprints        map[string]string `json:"fingerprints"`
	Properties          map[string]any    `json:"properties"`
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
	StartLine   int `json:"startLine"`
	EndLine     int `json:"endLine"`
	StartColumn int `json:"startColumn"`
}

type parsedResult struct {
	Tool        string
	RuleID      string
	Severity    string
	Path        string
	StartLine   int
	EndLine     int
	Body        string
	Fingerprint string
	VerdictSig  string
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
	seenTools := map[string]bool{}
	seenWarnings := map[string]bool{}
	for i := range doc.Runs {
		tool, results, warns, ref := parseRun(i, &doc.Runs[i])
		if ref != nil {
			return nil, ref
		}
		if !seenTools[tool] {
			seenTools[tool] = true
			out.Tools = append(out.Tools, tool)
		}
		for _, w := range warns {
			if !seenWarnings[w] {
				seenWarnings[w] = true
				out.Warnings = append(out.Warnings, w)
			}
		}
		out.Results = append(out.Results, results...)
	}
	slices.Sort(out.Tools)
	return out, nil
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

func parseRun(idx int, run *sarifRun) (string, []parsedResult, []string, *core.RefusalError) {
	if run.Tool == nil || run.Tool.Driver == nil {
		return "", nil, nil, badSARIF(fmt.Sprintf("SARIF runs[%d] must contain tool.driver.name", idx))
	}
	tool := strings.TrimSpace(run.Tool.Driver.Name)
	if tool == "" {
		return "", nil, nil, badSARIF(fmt.Sprintf("SARIF runs[%d].tool.driver.name must not be empty", idx))
	}
	rawResults, ref := decodeRunResults(idx, run.Results)
	if ref != nil {
		return "", nil, nil, ref
	}
	rules := run.Tool.Driver.Rules
	out := make([]parsedResult, 0, len(rawResults))
	var warns []string
	for j := range rawResults {
		res, warn, ref := parseResultItem(idx, j, tool, rules, &rawResults[j])
		if ref != nil {
			return "", nil, nil, ref
		}
		if warn != "" {
			warns = append(warns, warn)
		}
		out = append(out, res)
	}
	return tool, out, warns, nil
}

func decodeRunResults(idx int, raw *json.RawMessage) ([]sarifResult, *core.RefusalError) {
	if raw == nil {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(*raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
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

func parseResultItem(runIdx, resIdx int, tool string, rules []sarifRule, raw *sarifResult) (parsedResult, string, *core.RefusalError) {
	rule, ruleID, ref := resolveRule(runIdx, resIdx, rules, raw)
	if ref != nil {
		return parsedResult{}, "", ref
	}
	body, ref := extractMessage(runIdx, resIdx, raw.Message)
	if ref != nil {
		return parsedResult{}, "", ref
	}
	cleanPath, startLine, endLine, startCol, ref := extractLocation(runIdx, resIdx, raw.Locations)
	if ref != nil {
		return parsedResult{}, "", ref
	}
	level, secSev := effectiveSeverityInputs(raw, rule)
	tier, warn := sarif.TierForResult(level, secSev)
	verdictSig, fp := deriveFingerprint(tool, ruleID, cleanPath, startLine, startCol, body, raw.PartialFingerprints, raw.Fingerprints)
	return parsedResult{
		Tool:        tool,
		RuleID:      ruleID,
		Severity:    tier,
		Path:        cleanPath,
		StartLine:   startLine,
		EndLine:     endLine,
		Body:        body,
		Fingerprint: fp,
		VerdictSig:  verdictSig,
	}, warn, nil
}

func resolveRule(runIdx, resIdx int, rules []sarifRule, raw *sarifResult) (*sarifRule, string, *core.RefusalError) {
	ruleID := strings.TrimSpace(raw.RuleID)
	var rule *sarifRule
	if raw.RuleIndex != nil {
		ri := *raw.RuleIndex
		if ri < 0 || ri >= len(rules) {
			return nil, "", badSARIF(fmt.Sprintf("SARIF runs[%d].results[%d].ruleIndex %d out of range", runIdx, resIdx, ri))
		}
		rule = &rules[ri]
		if ruleID == "" {
			ruleID = strings.TrimSpace(rule.ID)
		}
	} else if ruleID != "" {
		for i := range rules {
			if strings.TrimSpace(rules[i].ID) == ruleID {
				rule = &rules[i]
				break
			}
		}
	}
	return rule, ruleID, nil
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
	return recordcore.NeutralizeMentions(text), nil
}

func extractLocation(runIdx, resIdx int, locs []sarifLocation) (cleanPath string, startLine, endLine, startCol int, ref *core.RefusalError) {
	if len(locs) == 0 || locs[0].PhysicalLocation == nil {
		return "", 0, 0, 0, nil
	}
	pl := locs[0].PhysicalLocation
	if pl.ArtifactLocation != nil && strings.TrimSpace(pl.ArtifactLocation.URI) != "" {
		cleaned, err := sarif.CleanURI(pl.ArtifactLocation.URI)
		if err != nil {
			return "", 0, 0, 0, badSARIF(fmt.Sprintf("SARIF runs[%d].results[%d]: %v", runIdx, resIdx, err))
		}
		cleanPath = cleaned
	}
	if pl.Region != nil {
		if pl.Region.StartLine < 0 || pl.Region.EndLine < 0 || pl.Region.StartColumn < 0 {
			return "", 0, 0, 0, badSARIF(fmt.Sprintf("SARIF runs[%d].results[%d].region line/column must be non-negative", runIdx, resIdx))
		}
		startLine = pl.Region.StartLine
		endLine = pl.Region.EndLine
		if endLine < startLine {
			endLine = startLine
		}
		startCol = pl.Region.StartColumn
	}
	return cleanPath, startLine, endLine, startCol, nil
}

func effectiveSeverityInputs(raw *sarifResult, rule *sarifRule) (level string, secSev any) {
	level = strings.TrimSpace(raw.Level)
	if level == "" && rule != nil && rule.DefaultConfiguration != nil {
		level = strings.TrimSpace(rule.DefaultConfiguration.Level)
	}
	if raw.Properties != nil {
		if v, ok := raw.Properties["security-severity"]; ok && v != nil {
			secSev = v
		}
	}
	if secSev == nil && rule != nil && rule.Properties != nil {
		if v, ok := rule.Properties["security-severity"]; ok && v != nil {
			secSev = v
		}
	}
	return level, secSev
}

func deriveFingerprint(tool, ruleID, cleanPath string, startLine, startCol int, body string, partial, full map[string]string) (verdictSig, fp string) {
	if s := strings.TrimSpace(partial["verdict/sig"]); s != "" {
		verdictSig = s
	}
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
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	if len(keys) == 1 {
		return strings.TrimSpace(m[keys[0]])
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strings.TrimSpace(m[k]))
	}
	return jq.StrOf(strings.Join(parts, ";"))
}
