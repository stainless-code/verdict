package brief

import (
	"fmt"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	examplePath = "file.go"
	exampleLine = 10
)

// Issue is one brief validation finding with the JSONPath suffix relative to
// the brief object, the published problem code and level, and the legacy
// warning text callers pinned before structured review-input problems.
type Issue struct {
	Path    string
	Code    string
	Level   string
	Message string
	Fix     string
	Example any
	Warning string
}

// Problem renders the issue as a published review-input problem.
func (i *Issue) Problem() core.Problem {
	return core.Problem{
		Path:    i.Path,
		Code:    i.Code,
		Level:   i.Level,
		Message: i.Message,
		Fix:     i.Fix,
		Example: i.Example,
	}
}

// Check validates one raw brief and returns every issue in document order.
// It is the structured form of Validate: record-bound callers surface each
// dropped or refused part as a review-input problem instead of only a warning.
func Check(raw any, rows []hunks.Hunk) (*core.Brief, []Issue) {
	m, keys, ok := objectOf(raw)
	if !ok {
		return nil, []Issue{{
			Code:    core.ProblemCodeWrongType,
			Level:   core.ProblemLevelError,
			Message: "brief must be an object",
			Fix:     "use {outcome, sites, approach, questions}",
			Example: map[string]any{"outcome": "State what the fix must achieve."},
			Warning: "brief is not an object — dropped",
		}}
	}
	outcome, issues := checkOutcome(m)
	sites, siteIssues := checkSites(m[keySites], rows)
	approach, approachIssues := checkApproach(m[keyApproach])
	questions, questionIssues := checkQuestions(m[keyQuestions])
	issues = append(issues, siteIssues...)
	issues = append(issues, approachIssues...)
	issues = append(issues, questionIssues...)
	issues = append(issues, unknownIssues(keys)...)
	if outcome == "" {
		return nil, issues
	}
	return &core.Brief{
		Sites:     sites,
		Outcome:   outcome,
		Approach:  approach,
		Questions: questions,
	}, issues
}

func checkOutcome(m map[string]any) (string, []Issue) {
	raw, has := m[keyOutcome]
	text, ok := raw.(string)
	if !ok {
		code, msg, warn := core.ProblemCodeWrongType, "brief outcome must be a string", "brief outcome is not a string — dropped the brief"
		if !has || jq.Falsey(raw) {
			code, msg, warn = core.ProblemCodeMissingField, "brief outcome is missing", "brief outcome is missing — dropped the brief"
		}
		return "", []Issue{{
			Path: ".outcome", Code: code, Level: core.ProblemLevelError, Message: msg,
			Fix: "write one sentence naming what the fix must achieve", Warning: warn,
		}}
	}
	if n := utf8.RuneCountInString(text); n < minOutcome || n > maxOutcome {
		return "", []Issue{{
			Path: ".outcome", Code: core.ProblemCodeWrongType, Level: core.ProblemLevelError,
			Message: fmt.Sprintf("brief outcome is %d chars (want %d-%d)", n, minOutcome, maxOutcome),
			Fix:     fmt.Sprintf("write one sentence of %d-%d chars naming what the fix must achieve", minOutcome, maxOutcome),
			Warning: fmt.Sprintf("brief outcome is %d chars (want %d-%d) — dropped the brief", n, minOutcome, maxOutcome),
		}}
	}
	return text, nil
}

func checkSites(v any, rows []hunks.Hunk) ([]core.BriefSite, []Issue) {
	if jq.Falsey(v) {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, []Issue{{
			Path: ".sites", Code: core.ProblemCodeWrongType, Level: core.ProblemLevelError,
			Message: "brief sites must be an array", Fix: "use an array of {path, line} objects",
			Example: []any{map[string]any{"path": examplePath, "line": exampleLine}},
			Warning: "brief sites is not an array — dropped",
		}}
	}
	out := make([]core.BriefSite, 0, len(items))
	seen := map[string]bool{}
	var issues []Issue
	for i, item := range items {
		site, issue, keep := checkSite(i, item, rows)
		if !keep {
			issues = append(issues, issue)
			continue
		}
		if key := siteLocator(site); !seen[key] {
			seen[key] = true
			out = append(out, site)
		}
	}
	return out, issues
}

//nolint:revive // the site gates read as one block
func checkSite(i int, item any, rows []hunks.Hunk) (core.BriefSite, Issue, bool) {
	base := fmt.Sprintf(".sites[%d]", i)
	if i >= maxSites {
		return core.BriefSite{}, Issue{
			Path: base, Code: core.ProblemCodeOutOfRange, Level: core.ProblemLevelError,
			Message: fmt.Sprintf("brief site %d is past the %d-site cap", i+1, maxSites),
			Fix:     fmt.Sprintf("use at most %d sites", maxSites),
			Warning: fmt.Sprintf("brief site %d is past the %d-site cap — dropped", i+1, maxSites),
		}, false
	}
	m, _, ok := objectOf(item)
	if !ok {
		return core.BriefSite{}, Issue{
			Path: base, Code: core.ProblemCodeWrongType, Level: core.ProblemLevelError,
			Message: "brief site must be an object", Fix: "use {path, line}",
			Example: map[string]any{"path": examplePath, "line": exampleLine},
			Warning: "brief site is not an object — dropped",
		}, false
	}
	path, ok := m[keyPath].(string)
	if !ok || path == "" {
		code, msg := core.ProblemCodeWrongType, "brief site path must be a non-empty string"
		if _, has := m[keyPath]; !has || jq.Falsey(m[keyPath]) {
			code, msg = core.ProblemCodeMissingField, "brief site has no path"
		}
		return core.BriefSite{}, Issue{
			Path: base + ".path", Code: code, Level: core.ProblemLevelError, Message: msg,
			Fix: "use a changed file path", Example: examplePath,
			Warning: "brief site has no path — dropped",
		}, false
	}
	line, issue, ok := checkSiteLine(m, base, path)
	if !ok {
		return core.BriefSite{}, issue, false
	}
	site := core.BriefSite{Path: path, Line: line}
	if rows != nil && !inHunks(rows, path, line) {
		return core.BriefSite{}, Issue{
			Path: base + ".path", Code: core.ProblemCodePathNotInDiff, Level: core.ProblemLevelWarning,
			Message: fmt.Sprintf("brief site %s is not a changed file with hunks", siteLocator(site)),
			Fix:     "use a changed file and a line inside one of its hunks",
			Warning: fmt.Sprintf("brief site %s is not a changed file with hunks — dropped", siteLocator(site)),
		}, false
	}
	return site, Issue{}, true
}

func checkSiteLine(m map[string]any, base, path string) (*int, Issue, bool) {
	raw, ok := m[keyLine]
	if !ok || jq.Falsey(raw) {
		return nil, Issue{}, true
	}
	n, ok := jq.IntOf(raw)
	if !ok {
		return nil, Issue{
			Path: base + ".line", Code: core.ProblemCodeWrongType, Level: core.ProblemLevelError,
			Message: fmt.Sprintf("brief site %s has a non-integer line", path),
			Fix:     "use an integer line number inside a hunk", Example: exampleLine,
			Warning: fmt.Sprintf("brief site %s has a non-integer line — dropped", path),
		}, false
	}
	return &n, Issue{}, true
}

func checkApproach(v any) (string, []Issue) {
	if jq.Falsey(v) {
		return "", nil
	}
	text, ok := v.(string)
	if !ok {
		return "", []Issue{{
			Path: ".approach", Code: core.ProblemCodeWrongType, Level: core.ProblemLevelError,
			Message: "brief approach must be a string", Fix: "use a string approach or leave it out",
			Example: "Track delivery ids in a bounded LRU.",
			Warning: "brief approach is not a string — dropped",
		}}
	}
	if text == "" {
		return "", nil
	}
	if n := utf8.RuneCountInString(text); n < minApproach || n > maxApproach {
		return "", []Issue{{
			Path: ".approach", Code: core.ProblemCodeOutOfRange, Level: core.ProblemLevelError,
			Message: fmt.Sprintf("brief approach is %d chars (want %d-%d)", n, minApproach, maxApproach),
			Fix:     fmt.Sprintf("write %d-%d chars or leave the approach out", minApproach, maxApproach),
			Warning: fmt.Sprintf("brief approach is %d chars (want %d-%d) — dropped", n, minApproach, maxApproach),
		}}
	}
	return text, nil
}

//nolint:revive // the question gates read as one block
func checkQuestions(v any) ([]string, []Issue) {
	if jq.Falsey(v) {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, []Issue{{
			Path: ".questions", Code: core.ProblemCodeWrongType, Level: core.ProblemLevelError,
			Message: "brief questions must be an array", Fix: "use an array of question strings",
			Example: []string{"Should ...?"}, Warning: "brief questions is not an array — dropped",
		}}
	}
	out := make([]string, 0, len(items))
	var issues []Issue
	for i, item := range items {
		base := fmt.Sprintf(".questions[%d]", i)
		if i >= maxQuestions {
			issues = append(issues, Issue{
				Path: base, Code: core.ProblemCodeOutOfRange, Level: core.ProblemLevelError,
				Message: fmt.Sprintf("brief question %d is past the %d-question cap", i+1, maxQuestions),
				Fix:     fmt.Sprintf("use at most %d questions", maxQuestions),
				Warning: fmt.Sprintf("brief question %d is past the %d-question cap — dropped", i+1, maxQuestions),
			})
			continue
		}
		text, ok := item.(string)
		if !ok {
			issues = append(issues, Issue{
				Path: base, Code: core.ProblemCodeWrongType, Level: core.ProblemLevelError,
				Message: fmt.Sprintf("brief question %d must be a string", i+1),
				Fix:     "use a question string", Example: "Should ...?",
				Warning: fmt.Sprintf("brief question %d is not a string — dropped", i+1),
			})
			continue
		}
		if n := utf8.RuneCountInString(text); n < minQuestion || n > maxQuestion {
			issues = append(issues, Issue{
				Path: base, Code: core.ProblemCodeOutOfRange, Level: core.ProblemLevelError,
				Message: fmt.Sprintf("brief question %d is %d chars (want %d-%d)", i+1, n, minQuestion, maxQuestion),
				Fix:     fmt.Sprintf("write %d-%d chars", minQuestion, maxQuestion),
				Warning: fmt.Sprintf("brief question %d is %d chars (want %d-%d) — dropped", i+1, n, minQuestion, maxQuestion),
			})
			continue
		}
		out = append(out, text)
	}
	return out, issues
}

func unknownIssues(keys []string) []Issue {
	var issues []Issue
	for _, key := range keys {
		switch key {
		case keySites, keyOutcome, keyApproach, keyQuestions:
		default:
			issues = append(issues, Issue{
				Path: "." + key, Code: core.ProblemCodeUnknownField, Level: core.ProblemLevelWarning,
				Message: fmt.Sprintf("unknown brief field %q", key),
				Fix:     "remove it or rename it to a known brief key",
				Warning: fmt.Sprintf("brief.%s is not a known brief key — dropped", key),
			})
		}
	}
	return issues
}
