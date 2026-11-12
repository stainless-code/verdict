// Package packtest runs offline eval cases embedded in lens packs.
package packtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"unicode"

	"github.com/stainless-code/verdict/internal/core"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
	"github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/hunks"
)

const (
	// CodePackTestCases refuses a pack whose evals/ tree has no cases.
	CodePackTestCases = "pack-test-cases"
	maxTestCases      = 128
	casePathParts     = 3
	passStatus        = "PASS"
	skipStatus        = "SKIP"
	failStatus        = "FAIL"
)

// TestCase is one offline pack eval result.
type TestCase struct {
	Case   string `json:"case"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// TestSummary counts the offline pack eval results.
type TestSummary struct {
	Pass int `json:"pass"`
	Skip int `json:"skip"`
	Fail int `json:"fail"`
}

// TestData is the pack test --json data profile.
type TestData struct {
	Pack    string      `json:"pack"`
	Cases   []TestCase  `json:"cases"`
	Summary TestSummary `json:"summary"`
}

// TestResponse carries the result rows and envelope inputs.
type TestResponse struct {
	At    string
	Next  string
	Data  *TestData
	Human []string
}

type testExpect struct {
	Rules    []string
	NotRules []string
}

// Run evaluates the cases embedded in p without executing pack content.
func Run(ctx context.Context, p *packcore.Pack, set *packcore.Set, at string) (*TestResponse, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before pack test completed"}
	}
	files := p.PayloadFiles()
	names := caseNames(files)
	if len(names) > maxTestCases {
		return nil, &core.RefusalError{
			Code: packcore.CodePackTooLarge,
			Message: fmt.Sprintf("pack %q has more than %d eval cases",
				p.Name, maxTestCases),
		}
	}
	data := testData(p, names)
	if len(data.Cases) == 0 {
		return nil, &core.RefusalError{Code: CodePackTestCases, Message: fmt.Sprintf("pack %q has no eval cases", p.Name)}
	}
	if ref := runCases(ctx, p, set, files, data); ref != nil {
		return nil, ref
	}
	return testResponse(p, data, at), nil
}

func runCases(ctx context.Context, p *packcore.Pack, set *packcore.Set, files []packcore.PayloadFile, data *TestData) *core.RefusalError {
	for i := range data.Cases {
		if err := ctx.Err(); err != nil {
			return &core.RefusalError{Code: "canceled", Message: "canceled while running pack eval cases"}
		}
		result := testCase(p, set, files, &data.Cases[i])
		data.Cases[i] = result
		switch result.Status {
		case passStatus:
			data.Summary.Pass++
		case skipStatus:
			data.Summary.Skip++
		case failStatus:
			data.Summary.Fail++
		}
	}
	return nil
}

func testResponse(p *packcore.Pack, data *TestData, at string) *TestResponse {
	lines := make([]string, 0, len(data.Cases)+1)
	for _, c := range data.Cases {
		line := fmt.Sprintf("%s %s", c.Status, oneLine(c.Case))
		if c.Reason != "" {
			line += " — " + oneLine(c.Reason)
		}
		lines = append(lines, line)
	}
	lines = append(lines, fmt.Sprintf("%s: %d pass, %d skip, %d fail", p.Name,
		data.Summary.Pass, data.Summary.Skip, data.Summary.Fail))
	return &TestResponse{At: at, Next: "fix failing pack eval cases", Data: data, Human: lines}
}

func oneLine(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, value)
}

func caseNames(files []packcore.PayloadFile) []string {
	caseSet := map[string]bool{}
	for i := range files {
		parts := strings.Split(files[i].Path, "/")
		if len(parts) >= casePathParts && parts[0] == "evals" {
			caseSet[parts[1]] = true
		}
	}
	return slices.Sorted(maps.Keys(caseSet))
}

func testData(p *packcore.Pack, names []string) *TestData {
	cases := make([]TestCase, 0, len(names))
	for _, name := range names {
		cases = append(cases, TestCase{Case: name})
	}
	return &TestData{Pack: p.Name, Cases: cases}
}

func testCase(p *packcore.Pack, set *packcore.Set, payload []packcore.PayloadFile, result *TestCase) TestCase {
	name := result.Case
	files := caseFiles(payload, name)
	if missing := missingCaseFile(files); missing != "" {
		return TestCase{Case: name, Status: skipStatus, Reason: "incomplete case: missing " + missing}
	}
	diff := string(files["diff.patch"])
	selected := selectRules(set, diffPaths(diff))
	expect, reason := parseExpect(files["expect.json"])
	if reason != "" {
		return failedCase(name, reason)
	}
	if reason := checkExpectedRules(p.Rules, selected, expect); reason != "" {
		return failedCase(name, reason)
	}
	if missing := missingTool(selected); missing != "" {
		return TestCase{Case: name, Status: skipStatus, Reason: "required tool is absent: " + missing}
	}
	findings, reason := parseFindings(files["findings.json"], diff)
	if reason != "" {
		return failedCase(name, reason)
	}
	if reason := checkFindingCitations(findings, p.Rules, set, expect); reason != "" {
		return failedCase(name, reason)
	}
	return TestCase{Case: name, Status: passStatus}
}

func caseFiles(payload []packcore.PayloadFile, name string) map[string][]byte {
	prefix := "evals/" + name + "/"
	files := map[string][]byte{}
	for i := range payload {
		if relative, ok := strings.CutPrefix(payload[i].Path, prefix); ok {
			files[relative] = payload[i].Body
		}
	}
	return files
}

func missingCaseFile(files map[string][]byte) string {
	for _, name := range []string{"diff.patch", "findings.json", "expect.json"} {
		if _, ok := files[name]; !ok {
			return name
		}
	}
	return ""
}

func selectRules(set *packcore.Set, paths []string) map[string]*packcore.Rule {
	rules := set.RulesFor(paths)
	selected := make(map[string]*packcore.Rule, len(rules))
	for i := range rules {
		selected[rules[i].ID] = &rules[i]
	}
	return selected
}

func checkExpectedRules(rules []packcore.Rule, selected map[string]*packcore.Rule, expect testExpect) string {
	allRules := make(map[string]bool, len(rules))
	for i := range rules {
		allRules[rules[i].ID] = true
	}
	if reason := checkRequiredRules(expect.Rules, allRules, selected); reason != "" {
		return reason
	}
	return checkForbiddenRules(expect.NotRules, allRules, selected)
}

func checkRequiredRules(ids []string, allRules map[string]bool, selected map[string]*packcore.Rule) string {
	for _, id := range ids {
		if !allRules[id] {
			return "unknown rule id " + id
		}
		if selected[id] == nil {
			return "expected rule is not selected: " + id
		}
	}
	return ""
}

func checkForbiddenRules(ids []string, allRules map[string]bool, selected map[string]*packcore.Rule) string {
	for _, id := range ids {
		if !allRules[id] {
			return "unknown rule id " + id
		}
		if selected[id] != nil {
			return "not_rules hit: " + id
		}
	}
	return ""
}

func checkFindingCitations(findings []map[string]any, rules []packcore.Rule, set *packcore.Set, expect testExpect) string {
	allRules := make(map[string]bool, len(rules))
	for i := range rules {
		allRules[rules[i].ID] = true
	}
	cited := map[string]bool{}
	for i := range findings {
		if reason := checkOneFinding(findings[i], i+1, allRules, set, expect, cited); reason != "" {
			return reason
		}
	}
	return missingExpectedCitations(expect.Rules, cited)
}

func checkOneFinding(
	finding map[string]any,
	number int,
	allRules map[string]bool,
	set *packcore.Set,
	expect testExpect,
	cited map[string]bool,
) string {
	pathValue, ok := finding["path"].(string)
	if !ok {
		return fmt.Sprintf("finding %d has an invalid path", number)
	}
	ids, reason := findingRuleIDs(finding, number)
	if reason != "" {
		return reason
	}
	for _, id := range ids {
		if reason := validateCitation(id, number, pathValue, allRules, set, expect.NotRules); reason != "" {
			return reason
		}
		cited[id] = true
	}
	return ""
}

func findingRuleIDs(finding map[string]any, number int) ([]string, string) {
	kb, ok := finding["kb"].([]any)
	if !ok || len(kb) == 0 {
		return nil, fmt.Sprintf("finding %d cites no rule id", number)
	}
	ids := make([]string, len(kb))
	for i := range kb {
		id, ok := kb[i].(string)
		if !ok {
			return nil, fmt.Sprintf("finding %d has an invalid rule id", number)
		}
		ids[i] = id
	}
	return ids, ""
}

func validateCitation(id string, number int, pathValue string, allRules map[string]bool, set *packcore.Set, denied []string) string {
	if !allRules[id] {
		return fmt.Sprintf("finding %d cites unknown rule id %s", number, id)
	}
	if !selectedForPath(set, id, pathValue) {
		return fmt.Sprintf("finding %d cites unselected rule %s for %s", number, id, pathValue)
	}
	if slices.Contains(denied, id) {
		return "not_rules hit: " + id
	}
	return ""
}

func missingExpectedCitations(ids []string, cited map[string]bool) string {
	for _, id := range ids {
		if !cited[id] {
			return "missing finding coverage for rule " + id
		}
	}
	return ""
}

func diffPaths(diff string) []string {
	files := hunks.Files(diff)
	paths := make([]string, 0, len(files))
	for i := range files {
		if files[i].Path != "" && files[i].Path != "/dev/null" {
			paths = append(paths, files[i].Path)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

func parseExpect(raw []byte) (testExpect, string) {
	var expect testExpect
	var wire struct {
		Rules    json.RawMessage `json:"rules"`
		NotRules json.RawMessage `json:"not_rules"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return expect, "invalid expect.json: " + err.Error()
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return expect, "invalid expect.json: trailing JSON value"
	}
	if len(wire.Rules) == 0 || json.Unmarshal(wire.Rules, &expect.Rules) != nil || expect.Rules == nil {
		return expect, "invalid expect.json: rules must be an array"
	}
	if len(wire.NotRules) > 0 && (json.Unmarshal(wire.NotRules, &expect.NotRules) != nil || expect.NotRules == nil) {
		return expect, "invalid expect.json: not_rules must be an array"
	}
	return expect, ""
}

func parseFindings(raw []byte, diff string) ([]map[string]any, string) {
	findings, reason := decodeFindings(raw)
	if reason != "" {
		return nil, reason
	}
	if reason := validateFindings(findings, diff); reason != "" {
		return nil, reason
	}
	return findings, ""
}

func decodeFindings(raw []byte) ([]map[string]any, string) {
	var findings []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&findings); err != nil || findings == nil {
		return nil, "findings.json must be an array of review findings"
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, "findings.json has trailing JSON"
	}
	return findings, ""
}

func validateFindings(findings []map[string]any, diff string) string {
	review, err := json.Marshal(map[string]any{
		"schema_version": "1", "body": "The fixture exercises this pack rule against a changed file.",
		"approved": false, "comments": findings,
	})
	if err != nil {
		return "findings.json cannot be encoded"
	}
	checked := recordcore.CheckReview(review, diff)
	if reason := reviewFindingProblem(checked); reason != "" {
		return reason
	}
	if checked.Summary.AnchorsMovedToBody > 0 {
		return "finding anchor is outside the diff"
	}
	return ""
}

func reviewFindingProblem(checked recordcore.CheckResult) string {
	for _, problem := range checked.Problems {
		return fmt.Sprintf("invalid finding: %s: %s (%s)", problem.Path, problem.Message, problem.Fix)
	}
	return ""
}

func selectedForPath(set *packcore.Set, id, path string) bool {
	rules := set.RulesFor([]string{path})
	for i := range rules {
		if rules[i].ID == id {
			return true
		}
	}
	return false
}

func missingTool(rules map[string]*packcore.Rule) string {
	tools := map[string]bool{}
	for _, rule := range rules {
		for _, name := range rule.Tools {
			tools[name] = true
		}
	}
	names := slices.Sorted(maps.Keys(tools))
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			return name
		}
	}
	return ""
}

func failedCase(name, reason string) TestCase {
	return TestCase{Case: name, Status: failStatus, Reason: reason}
}
