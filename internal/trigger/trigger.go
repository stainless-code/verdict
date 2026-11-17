// Package trigger evaluates the closed local trigger predicate set against
// PR facts captured at fetch time. The predicates come from the trusted base
// configuration; the facts come from the bundle's PR.json.
package trigger

import (
	"fmt"
	"regexp"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Facts are the PR inputs a trigger predicate can read.
type Facts struct {
	Author     string
	Paths      []string
	Labels     []string
	Title      string
	BaseBranch string
	Draft      bool
}

// Evaluate reports whether a PR runs the review and names the predicate that
// decided it: "matched" for a configured match, "no-trigger" when no predicate
// is set, or the failing predicate's key. Lists OR within one predicate and
// AND across predicates.
func Evaluate(pr Facts, predicates *core.ConfigTrigger) (bool, string) {
	if predicates == nil || empty(*predicates) {
		return true, "no-trigger"
	}
	if names := predicates.Authors; len(names) != 0 && !contains(names, pr.Author) {
		return false, "authors"
	}
	if globs := predicates.PathsInclude; len(globs) != 0 && !anyPath(globs, pr.Paths) {
		return false, "paths_include"
	}
	if globs := predicates.PathsExclude; len(globs) != 0 && anyPath(globs, pr.Paths) {
		return false, "paths_exclude"
	}
	if labels := predicates.Labels; len(labels) != 0 && !anyContains(labels, pr.Labels) {
		return false, "labels"
	}
	if pattern := predicates.TitleMatches; pattern != "" {
		matched, err := regexp.MatchString(pattern, pr.Title)
		if err != nil || !matched {
			return false, "title_matches"
		}
	}
	if branches := predicates.BaseBranches; len(branches) != 0 && !anyGlob(branches, pr.BaseBranch) {
		return false, "base_branches"
	}
	if predicates.Draft != nil && *predicates.Draft != pr.Draft {
		return false, "draft"
	}
	return true, "matched"
}

func empty(p core.ConfigTrigger) bool {
	return len(p.Authors)+len(p.PathsInclude)+len(p.PathsExclude)+len(p.Labels)+
		len(p.BaseBranches)+len(p.TitleMatches) == 0 && p.Draft == nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func anyContains(values, candidates []string) bool {
	for _, candidate := range candidates {
		if contains(values, candidate) {
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
		return Facts{}, fmt.Errorf("could not read %s/PR.json: %w", dir, err)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return Facts{}, fmt.Errorf("could not decode %s/PR.json: %w", dir, err)
	}
	record, ok := jq.AsMap(doc)
	if !ok {
		return Facts{}, fmt.Errorf("%s/PR.json is not an object", dir)
	}
	return Facts{
		Author:     readAuthor(dir, record),
		Paths:      stringsFrom(jq.Elements(record["files"]), "path"),
		Labels:     readLabels(record["labels"]),
		Title:      jq.StrOf(record["title"]),
		BaseBranch: firstString(record["baseRefName"], record["base_ref"]),
		Draft:      readDraft(record),
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
		if value, ok := entry.(string); ok && value != "" {
			out = append(out, value)
			continue
		}
		if m, ok := jq.AsMap(entry); ok {
			if value := jq.StrOf(m["name"]); value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}
