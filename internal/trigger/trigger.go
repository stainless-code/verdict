// Package trigger evaluates the closed local trigger predicate set against
// PR facts captured at fetch time. The predicates come from the trusted base
// configuration; the facts come from the bundle's PR.json.
package trigger

import (
	"regexp"
	"slices"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Facts are the PR inputs a trigger predicate can read.
type Facts struct {
	Author     string
	Title      string
	BaseBranch string
	Paths      []string
	Labels     []string
	Draft      bool
	// IncompletePaths marks a captured file list the producer flagged as
	// truncated or paged with an error. Path predicates cannot decide such a
	// PR: skipping the review on partial evidence would be the wrong side to
	// fail on.
	IncompletePaths bool
}

// Evaluate reports whether a PR runs the review and names the predicate that
// decided it: "matched" for a configured match, "no-trigger" when no predicate
// is set, or the failing predicate's key. Lists OR within one predicate and
// AND across predicates.
func Evaluate(pr *Facts, predicates *core.ConfigTrigger) (bool, string) {
	if predicates == nil || empty(predicates) {
		return true, "no-trigger"
	}
	if len(predicates.Authors) != 0 && !slices.Contains(predicates.Authors, pr.Author) {
		return false, "authors"
	}
	if reason := predicateReason(pr, predicates); reason != "" {
		return false, reason
	}
	return true, "matched"
}

func predicateReason(pr *Facts, predicates *core.ConfigTrigger) string {
	paths := withoutExcluded(pr.Paths, predicates.PathsExclude)
	if reason := pathReason(predicates, paths, pr.IncompletePaths); reason != "" {
		return reason
	}
	for _, check := range []struct {
		name string
		ok   bool
	}{
		{"labels", len(predicates.Labels) == 0 || anyContains(predicates.Labels, pr.Labels)},
		{"title_matches", matchTitle(predicates.TitleMatches, pr.Title)},
		{"base_branches", len(predicates.BaseBranches) == 0 || anyGlob(predicates.BaseBranches, pr.BaseBranch)},
		{"draft", predicates.Draft == nil || *predicates.Draft == pr.Draft},
	} {
		if !check.ok {
			return check.name
		}
	}
	return ""
}

func withoutExcluded(paths, excluded []string) []string {
	if len(excluded) == 0 {
		return paths
	}
	kept := make([]string, 0, len(paths))
	for _, path := range paths {
		if !anyGlob(excluded, path) {
			kept = append(kept, path)
		}
	}
	return kept
}

func pathReason(predicates *core.ConfigTrigger, paths []string, incomplete bool) string {
	// With an incomplete file list, a path match proves nothing either way.
	// The caller warns; here the gate leaves the decision to live review.
	if incomplete {
		return ""
	}
	if len(predicates.PathsInclude) != 0 && !anyPath(predicates.PathsInclude, paths) {
		return "paths_include"
	}
	if len(predicates.PathsExclude) != 0 && len(predicates.PathsInclude) == 0 && len(paths) == 0 {
		return "paths_exclude"
	}
	return ""
}

func matchTitle(pattern, title string) bool {
	if pattern == "" {
		return true
	}
	matched, err := regexp.MatchString(pattern, title)
	return err == nil && matched
}

func empty(p *core.ConfigTrigger) bool {
	return len(p.Authors)+len(p.PathsInclude)+len(p.PathsExclude)+len(p.Labels)+
		len(p.BaseBranches)+len(p.TitleMatches) == 0 && p.Draft == nil
}

func anyContains(values, candidates []string) bool {
	for _, candidate := range candidates {
		if slices.Contains(values, candidate) {
			return true
		}
	}
	return false
}

func anyGlob(globs []string, path string) bool {
	for _, pattern := range globs {
		if config.Match(pattern, path) {
			return true
		}
	}
	return false
}

func anyPath(globs, paths []string) bool {
	for _, path := range paths {
		if anyGlob(globs, path) {
			return true
		}
	}
	return false
}

// ReadFacts reads the PR record a fetch produced. Both the raw host shape and
// the normalized bundle shape are accepted: author may be a login object or a
// string, and the draft and base-branch keys carry their provider-neutral
// spellings beside the GitHub ones.
func ReadFacts(dir string) (Facts, error) {
	raw, err := jq.CappedFile(dir+"/PR.json", jq.MaxPRBytes)
	if err != nil {
		return Facts{}, jq.NewErrf("could not read %s/PR.json: %v", dir, err)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return Facts{}, jq.NewErrf("could not decode %s/PR.json: %v", dir, err)
	}
	record, ok := jq.AsMap(doc)
	if !ok {
		return Facts{}, jq.NewErrf("%s/PR.json is not an object", dir)
	}
	return Facts{
		Author:          readAuthor(dir, record),
		Title:           jq.StrOf(record["title"]),
		BaseBranch:      firstString(record["baseRefName"], record["base_ref"]),
		Paths:           stringsFrom(jq.Elements(record["files"]), "path"),
		Labels:          readLabels(record["labels"]),
		Draft:           readDraft(record),
		IncompletePaths: readcommon.SourceTruncation(dir) != nil,
	}, nil
}

func readAuthor(dir string, record map[string]any) string {
	if author := jq.StrOf(record["author"]); author != "" {
		return author
	}
	if m, ok := jq.AsMap(record["author"]); ok {
		if login := jq.StrOf(m["login"]); login != "" {
			return login
		}
	}
	return readcommon.OpenBundle(dir).PrField("author")
}

func firstString(values ...any) string {
	for _, value := range values {
		if str := jq.StrOf(value); str != "" {
			return str
		}
	}
	return ""
}

func readDraft(record map[string]any) bool {
	for _, key := range []string{"isDraft", "draft"} {
		if value, ok := record[key].(bool); ok {
			return value
		}
	}
	return false
}

func stringsFrom(entries []any, key string) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		m, ok := jq.AsMap(entry)
		if !ok {
			continue
		}
		if value := jq.StrOf(m[key]); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func readLabels(raw any) []string {
	var out []string
	for _, entry := range jq.Elements(raw) {
		if name := labelName(entry); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func labelName(entry any) string {
	if value, ok := entry.(string); ok {
		return value
	}
	if m, ok := jq.AsMap(entry); ok {
		return jq.StrOf(m["name"])
	}
	return ""
}
