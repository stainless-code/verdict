package recordcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// CheckResult is CheckReview's output: every problem in document order, the
// overflow past the 50-problem cap, and the summary a clean dry run reports.
type CheckResult struct {
	Problems  []Problem         `json:"problems"`
	Truncated int               `json:"problems_truncated"`
	Summary   core.CheckSummary `json:"summary"`
}

// Problem is one CheckReview finding.
type Problem = core.Problem

const (
	problemCap           = 50
	problemRoot          = "$"
	problemApproved      = "approved"
	problemRationale     = "rationale"
	problemPathSuffix    = ".path"
	exampleFile          = "file.go"
	exampleLine          = 10
	exampleEllipsis      = "..."
	exampleImportant     = "important"
	exampleWontFix       = "wont-fix"
	exampleFalsePositive = "false-positive"
	exampleConfidence    = 3
)

var (
	//nolint:goconst // published schema literals
	knownReviewKeys = []string{
		"schema_version", fieldBody, problemApproved, "comments",
		"resolved", "triage", "sections", "chunks", "confidence", "security_summary",
	}
	//nolint:goconst // schema keys are published literals
	knownCommentKeys = []string{
		"path", "line", "start_line", "side", "body",
		"suggestion", "brief", "rigor", fieldSeverity, "in-bounds", "kb",
	}
	knownBriefKeys   = []string{"sites", fieldOutcome, "approach", "questions"}
	knownSiteKeys    = []string{"path", "line"}
	knownTriageKeys  = []string{fieldSig, fieldStatus, problemRationale}
	knownSectionKeys = []string{"heading", fieldSeverity}
	reviewKeys       = []string{"critical", exampleImportant, "informational"}
	rigorKeys        = []string{"said", "pointed", "walked", "ran", "reproduced"}
	triageStatuses   = []string{"open", "addressed", exampleWontFix, exampleFalsePositive, "deferred"}
)

// CheckReview validates a review.json against a unified diff. It is pure: it
// reads no store and writes nothing, and it collects every problem instead of
// stopping at the first.
func CheckReview(review []byte, diff string) CheckResult {
	c := &checker{
		res:   CheckResult{Problems: []Problem{}},
		hunks: hunks.Parse(diff),
	}
	if problem := invalidJSON(review); problem != nil {
		c.add(*problem)
		return *c.result()
	}
	doc, err := jq.DecodeOrdered(review)
	if err != nil {
		c.invalidJSON(err)
		return *c.result()
	}
	obj, ok := doc.(*jq.Obj)
	if !ok {
		c.add(c.problem(problemRoot, core.ProblemCodeWrongType, core.ProblemLevelError,
			"review.json must hold a JSON object",
			"write one JSON object with body and comments[]", reviewObjectExample()))
		return *c.result()
	}
	c.checkReview(obj)
	return *c.result()
}

//nolint:govet // the fields group one check pass
type checker struct {
	res      CheckResult
	hunks    []hunks.Hunk
	comments int
	sections int
	moved    int
}

func (c *checker) result() *CheckResult {
	c.res.Summary = core.CheckSummary{
		Comments:           c.comments,
		Sections:           c.sections,
		AnchorsMovedToBody: c.moved,
	}
	if c.res.Problems == nil {
		c.res.Problems = []Problem{}
	}
	return &c.res
}

//nolint:gocritic // Problem is copied once into the result
func (c *checker) add(p Problem) {
	if len(c.res.Problems) >= problemCap {
		c.res.Truncated++
		return
	}
	c.res.Problems = append(c.res.Problems, p)
}

func (c *checker) problem(path, code, level, message, fix string, example ...any) Problem {
	p := Problem{Path: path, Code: code, Level: level, Message: message, Fix: fix}
	if len(example) > 0 {
		p.Example = example[0]
	}
	return p
}

func (c *checker) wrongType(path, message, fix string, example ...any) {
	c.add(c.problem(path, core.ProblemCodeWrongType, core.ProblemLevelError, message, fix, example...))
}

func (c *checker) missing(path, message, fix string, example ...any) {
	c.add(c.problem(path, core.ProblemCodeMissingField, core.ProblemLevelError, message, fix, example...))
}

func (c *checker) badEnum(path, message, fix string, example any) {
	c.add(c.problem(path, core.ProblemCodeBadEnum, core.ProblemLevelError, message, fix, example))
}

func (c *checker) unknown(path, key string, known []string) {
	c.add(c.problem(path+"."+key, core.ProblemCodeUnknownField, core.ProblemLevelWarning,
		fmt.Sprintf("unknown field %q", key),
		fmt.Sprintf("remove it or rename it to the nearest known key %q", nearest(key, known))))
}

// checkReview walks the top-level object in document order.
//
//nolint:cyclop // one branch per published review key
func (c *checker) checkReview(obj *jq.Obj) {
	for _, key := range obj.Keys {
		v, _ := obj.Get(key)
		switch key {
		case "schema_version":
			c.checkSchemaVersion(v)
		case "body":
			c.checkString("$."+fieldBody, v, true, "write the full review text")
		case problemApproved:
			if _, ok := v.(bool); !ok {
				c.wrongType("$."+problemApproved, "approved must be a boolean", "use true or false", false)
			}
		case "comments":
			c.checkComments(v)
		case "resolved":
			c.checkStringArray("$.resolved", v, "list finding sigs as strings")
		case "triage":
			c.checkTriage(v)
		case "sections":
			c.checkSections(v)
		case "confidence":
			c.checkConfidence(v)
		case "security_summary":
			c.checkSecuritySummary(v)
		case "chunks":
			c.checkStringArray("$.chunks", v, "list manifest chunk names as strings")
		default:
			c.unknown("$", key, knownReviewKeys)
		}
	}
	if _, ok := obj.Get(fieldBody); !ok {
		c.missing("$."+fieldBody, "missing body", "write the full review text", exampleEllipsis)
	}
}

func (c *checker) checkSchemaVersion(v any) {
	s, ok := v.(string)
	if !ok {
		c.wrongType("$.schema_version", "schema_version must be the string \"1\"", "write \"schema_version\": \"1\"", "1")
		return
	}
	if s != schemaVersion1 {
		c.badEnum("$.schema_version", fmt.Sprintf("unknown schema_version %q", s),
			"write the only supported version", schemaVersion1)
	}
}

func (c *checker) checkString(path string, v any, required bool, fix string) {
	if v == nil && !required {
		return
	}
	if _, ok := v.(string); !ok {
		if required && v == nil {
			c.missing(path, "missing required string", fix, "...")
			return
		}
		c.wrongType(path, "value must be a string", fix, exampleEllipsis)
	}
}

func (c *checker) checkStringArray(path string, v any, fix string) {
	if v == nil || v == false {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType(path, "value must be an array of strings", fix, []string{exampleEllipsis})
		return
	}
	for i, item := range items {
		if _, ok := item.(string); !ok {
			c.wrongType(fmt.Sprintf("%s[%d]", path, i), "array entry must be a string", fix, exampleEllipsis)
		}
	}
}

func (c *checker) checkComments(v any) {
	if v == nil || v == false {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType("$.comments", "comments must be an array",
			"write an array of {path, line, body} objects", []any{commentExample()})
		return
	}
	c.comments = len(items)
	for i, item := range items {
		c.checkComment(i, item)
	}
}

//nolint:gocognit,cyclop,revive // one branch per published comment key
func (c *checker) checkComment(i int, item any) {
	base := fmt.Sprintf("$.comments[%d]", i)
	m, ok := item.(*jq.Obj)
	if !ok {
		c.wrongType(base, "comment must be an object",
			"write one {path, line, body, side} object per finding", commentExample())
		return
	}
	pathValue, hasPath := m.Get(fieldPath)
	path, pathOK := pathValue.(string)
	for _, key := range m.Keys {
		v, _ := m.Get(key)
		switch key {
		case fieldPath:
			if !pathOK {
				c.wrongType(base+problemPathSuffix, "path must be a string", "use a changed file path", exampleFile)
			}
		case fieldLine, fieldStartLine:
			c.checkAnchorValue(base+"."+key, key, v)
		case "side":
			c.checkString(base+".side", v, false, "use RIGHT or LEFT")
		case fieldBody:
			c.checkString(base+".body", v, true, "write the finding text")
		case fieldSuggestion:
			if v != nil {
				c.checkString(base+".suggestion", v, false, "use a string code suggestion or null")
			}
		case fieldBrief:
			c.checkBrief(base, v)
		case fieldRigor:
			c.checkRigor(base, v)
		case fieldSeverity:
			c.checkSeverity(base+".severity", v)
		case "in-bounds":
			if _, ok := v.(bool); !ok {
				c.wrongType(base+".in-bounds", "in-bounds must be a boolean", "use true or false", true)
			}
		case fieldKB:
			c.checkStringArray(base+".kb", v, "list knowledge-base rule ids as strings")
		default:
			c.unknown(base, key, knownCommentKeys)
		}
	}
	if !hasPath {
		c.missing(base+problemPathSuffix, "missing path", "add the changed file path", exampleFile)
	}
	if _, hasBody := m.Get(fieldBody); !hasBody {
		c.missing(base+".body", "missing body", "add the finding text", exampleEllipsis)
	}
	if _, hasRigor := m.Get(fieldRigor); !hasRigor {
		c.checkRigor(base, nil)
	}
	c.checkAnchor(base, m, path, pathOK)
}

func (c *checker) checkAnchorValue(path, key string, v any) {
	if v == nil || v == false {
		if v == false {
			c.wrongType(path, key+" must be an integer or numeric string", "use a line number inside a hunk", exampleLine)
		}
		return
	}
	switch t := v.(type) {
	case string:
		return
	case json.Number:
		if strings.ContainsAny(t.String(), ".eE") {
			c.wrongType(path, key+" must be an integer or numeric string", "use a whole line number", exampleLine)
		}
	default:
		c.wrongType(path, "anchor must be a string or integer", "use a line number inside a hunk", exampleLine)
	}
}

func (c *checker) checkAnchor(base string, m *jq.Obj, path string, pathOK bool) {
	if !pathOK {
		return
	}
	key := stripDiffPrefix(path)
	if !hunks.Indexed(c.hunks, key) {
		c.add(c.problem(base+problemPathSuffix, core.ProblemCodePathNotInDiff, core.ProblemLevelError,
			fmt.Sprintf("path %q is not in the fetched diff", path),
			"anchor the comment on a changed file or move it into the body"))
		return
	}
	lo, hi, side := anchorRange(m)
	if lo < 0 {
		return
	}
	want := "NEW"
	if side == "LEFT" {
		want = "OLD"
	}
	if hunks.Contains(c.hunks, path, want, lo, hi) || hunks.Contains(c.hunks, stripDiffPrefix(path), want, lo, hi) {
		return
	}
	c.moved++
	c.add(c.problem(base+".line", core.ProblemCodeAnchorOutsideHunk, core.ProblemLevelWarning,
		fmt.Sprintf("anchor %d-%d (%s) is outside every reviewed hunk; record moves it to the body", lo, hi, side),
		"move the comment to the body or set line/start_line inside a reviewed hunk"))
}

func (c *checker) checkRigor(base string, v any) {
	if v == nil || v == false {
		c.add(c.problem(base+".rigor", core.ProblemCodeRigorUnset, core.ProblemLevelWarning,
			"rigor is unset", "set rigor to said|pointed|walked|ran|reproduced", "walked"))
		return
	}
	s, ok := v.(string)
	if !ok {
		c.wrongType(base+".rigor", "rigor must be a string", "use said|pointed|walked|ran|reproduced", "walked")
		return
	}
	if !slices.Contains(rigorKeys, s) {
		c.badEnum(base+".rigor", fmt.Sprintf("unknown rigor %q", s),
			"use said|pointed|walked|ran|reproduced", "walked")
	}
}

func (c *checker) checkSeverity(path string, v any) {
	if v == nil || v == false {
		if v == false {
			c.wrongType(path, "severity must be a string or null", "use critical|important|informational", "important")
		}
		return
	}
	s, ok := v.(string)
	if !ok {
		c.wrongType(path, "severity must be a string or null", "use critical|important|informational", "important")
		return
	}
	if !slices.Contains(reviewKeys, s) {
		c.badEnum(path, fmt.Sprintf("unknown severity %q", s),
			"use critical|important|informational", "important")
	}
}

//nolint:revive // one branch per published brief key
func (c *checker) checkBrief(base string, v any) {
	bpath := base + ".brief"
	if v == nil || v == false {
		return
	}
	m, ok := v.(*jq.Obj)
	if !ok {
		c.wrongType(bpath, "brief must be an object", "use {outcome, sites, approach, questions}",
			map[string]any{"outcome": "State what the fix must achieve."})
		return
	}
	for _, key := range m.Keys {
		v, _ := m.Get(key)
		switch key {
		case fieldOutcome:
			c.checkString(bpath+"."+fieldOutcome, v, true, "write one sentence naming what the fix must achieve")
		case "approach":
			if v != nil {
				c.checkString(bpath+".approach", v, false, "use a string approach")
			}
		case "questions":
			c.checkQuestions(bpath+".questions", v)
		case "sites":
			c.checkSites(bpath+".sites", v)
		default:
			c.unknown(bpath, key, knownBriefKeys)
		}
	}
	if _, ok := m.Get(fieldOutcome); !ok {
		c.missing(bpath+"."+fieldOutcome, "missing brief outcome",
			"add one sentence naming what the fix must achieve", "Reject duplicate deliveries before anything is enqueued.")
	}
}

func (c *checker) checkQuestions(path string, v any) {
	if v == nil || v == false {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType(path, "questions must be an array of strings", "use an array of question strings", []string{"Should ...?"})
		return
	}
	for i, item := range items {
		if _, ok := item.(string); !ok {
			c.wrongType(fmt.Sprintf("%s[%d]", path, i), "question must be a string", "use a question string", "Should ...?")
		}
	}
}

func (c *checker) checkSites(path string, v any) {
	if v == nil || v == false {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType(path, "sites must be an array", "use an array of {path, line} objects", []any{siteExample()})
		return
	}
	for i, item := range items {
		c.checkSite(fmt.Sprintf("%s[%d]", path, i), item)
	}
}

func (c *checker) checkSite(base string, v any) {
	m, ok := v.(*jq.Obj)
	if !ok {
		c.wrongType(base, "site must be an object", "use {path, line}", siteExample())
		return
	}
	pathValue, hasPath := m.Get(fieldPath)
	if _, ok := pathValue.(string); !ok && hasPath {
		c.wrongType(base+problemPathSuffix, "site path must be a string", "use a changed file path", exampleFile)
	}
	for _, key := range m.Keys {
		v, _ := m.Get(key)
		switch key {
		case fieldPath:
		case fieldLine:
			c.checkAnchorValue(base+".line", "line", v)
		default:
			c.unknown(base, key, knownSiteKeys)
		}
	}
	if !hasPath {
		c.missing(base+problemPathSuffix, "missing site path", "add the changed file path", exampleFile)
	}
}

func (c *checker) checkTriage(v any) {
	if v == nil || v == false {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType("$.triage", "triage must be an array",
			"write an array of {sig, status, rationale}", []any{map[string]any{
				fieldSig:         exampleEllipsis,
				fieldStatus:      exampleWontFix,
				problemRationale: exampleEllipsis,
			}})
		return
	}
	for i, item := range items {
		c.checkTriageClaim(i, item)
	}
}

//nolint:revive // one branch per published triage key
func (c *checker) checkTriageClaim(i int, item any) {
	base := fmt.Sprintf("$.triage[%d]", i)
	m, ok := item.(*jq.Obj)
	if !ok {
		c.wrongType(base, "triage entry must be an object", "use {sig, status, rationale}",
			map[string]any{"sig": "...", "status": "wont-fix", "rationale": "..."})
		return
	}
	for _, key := range m.Keys {
		v, _ := m.Get(key)
		switch key {
		case fieldSig:
			c.checkString(base+".sig", v, true, "use a finding sig")
		case fieldStatus:
			c.checkTriageStatus(base+".status", v)
		case problemRationale:
			if v != nil {
				c.checkString(base+"."+problemRationale, v, false, "use a rationale string")
			}
		default:
			c.unknown(base, key, knownTriageKeys)
		}
	}
	if _, ok := m.Get(fieldSig); !ok {
		c.missing(base+".sig", "missing triage sig", "add the finding sig", "...")
	}
	if _, ok := m.Get(fieldStatus); !ok {
		c.missing(base+".status", "missing triage status", "add open|addressed|wont-fix|false-positive|deferred", "wont-fix")
	}
}

func (c *checker) checkTriageStatus(path string, v any) {
	s, ok := v.(string)
	if !ok {
		c.wrongType(path, "triage status must be a string", "use open|addressed|wont-fix|false-positive|deferred", exampleWontFix)
		return
	}
	if !slices.Contains(triageStatuses, s) {
		c.badEnum(path, fmt.Sprintf("unknown triage status %q", s),
			"use open|addressed|wont-fix|false-positive|deferred", exampleWontFix)
	}
}

func (c *checker) checkSections(v any) {
	if v == nil || v == false {
		return
	}
	items, ok := v.([]any)
	if !ok {
		c.wrongType("$.sections", "sections must be an array",
			"write an array of {heading, severity}", []any{sectionExample()})
		return
	}
	c.sections = len(items)
	for i, item := range items {
		c.checkSection(i, item)
	}
}

func (c *checker) checkSection(i int, item any) {
	base := fmt.Sprintf("$.sections[%d]", i)
	m, ok := item.(*jq.Obj)
	if !ok {
		c.wrongType(base, "section must be an object", "use {heading, severity}", sectionExample())
		return
	}
	for _, key := range m.Keys {
		v, _ := m.Get(key)
		switch key {
		case fieldHeading:
			c.checkString(base+".heading", v, true, "use the exact ### heading string")
		case fieldSeverity:
			c.checkSeverity(base+".severity", v)
		default:
			c.unknown(base, key, knownSectionKeys)
		}
	}
	if _, ok := m.Get(fieldHeading); !ok {
		c.missing(base+".heading", "missing heading", "use the exact ### heading string", "### Security")
	}
}

func (c *checker) checkConfidence(v any) {
	if v == nil {
		return
	}
	n, ok := v.(json.Number)
	if !ok || strings.ContainsAny(n.String(), ".eE") {
		c.wrongType("$.confidence", "confidence must be an integer 1-5", "use an integer 1-5", exampleConfidence)
	}
}

func (c *checker) checkSecuritySummary(v any) {
	if v == nil {
		return
	}
	if _, ok := v.(string); !ok {
		c.wrongType("$.security_summary", "security_summary must be a string", "use a short paragraph", "...")
	}
}

func invalidJSON(review []byte) *Problem {
	var v any
	err := json.Unmarshal(review, &v)
	if err == nil {
		return nil
	}
	line, col := jsonLineCol(review, err)
	p := Problem{
		Path:    problemRoot,
		Code:    core.ProblemCodeInvalidJSON,
		Level:   core.ProblemLevelError,
		Message: fmt.Sprintf("invalid JSON at %d:%d: %s", line, col, jsonErrorText(err)),
		Fix:     "emit only the JSON object, no code fence, no prose",
		Example: reviewObjectExample(),
	}
	return &p
}

func (c *checker) invalidJSON(err error) {
	line, col := jsonLineCol(nil, err)
	c.add(c.problem(problemRoot, core.ProblemCodeInvalidJSON, core.ProblemLevelError,
		fmt.Sprintf("invalid JSON at %d:%d: %s", line, col, jsonErrorText(err)),
		"emit only the JSON object, no code fence, no prose", reviewObjectExample()))
}

func jsonErrorText(err error) string {
	if syntax, ok := errors.AsType[*json.SyntaxError](err); ok {
		return syntax.Error()
	}
	return err.Error()
}

func jsonLineCol(raw []byte, err error) (int, int) {
	syntax, ok := errors.AsType[*json.SyntaxError](err)
	if !ok || syntax.Offset <= 0 {
		return 1, 1
	}
	offset := int(syntax.Offset)
	offset = min(offset, len(raw))
	line, col := 1, 1
	for i := range offset {
		if raw[i] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}

func reviewObjectExample() map[string]any {
	return map[string]any{
		"schema_version": "1",
		fieldBody:        "> ✅ Review text.",
		problemApproved:  false,
		"comments":       []any{},
	}
}

func commentExample() map[string]any {
	return map[string]any{"path": exampleFile, "line": exampleLine, "side": "RIGHT", "body": exampleEllipsis}
}

func siteExample() map[string]any { return map[string]any{"path": exampleFile, "line": exampleLine} }

func sectionExample() map[string]any {
	return map[string]any{"heading": "### Security", "severity": exampleImportant}
}

func nearest(s string, keys []string) string {
	best, bestD := "", -1
	for _, key := range keys {
		d := editDistance(s, key)
		if bestD < 0 || d < bestD {
			best, bestD = key, d
		}
	}
	return best
}

//nolint:revive // bounded edit distance
func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
