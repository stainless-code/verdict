package config

import (
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// ClauseProvenance pairs one blocking clause with every source that
// contributed it, in contribution order: the trusted base first, then
// matching scopes in file order. Identical clauses (same threshold,
// categories, and paths) normalize to one entry with all provenances.
type ClauseProvenance struct {
	Clause  core.BlockClause `json:"clause"`
	Sources []string         `json:"sources"`
}

// RequirementSet is the accumulated requirements for one file or group:
// the conjunction of every applicable clause, the union of required
// checks with per-check sources, and the OR of every require_bind.
type RequirementSet struct {
	CheckOrigin map[string][]string `json:"check_origin"`
	Clauses     []ClauseProvenance  `json:"clauses"`
	Checks      []string            `json:"checks"`
	BindSources []string            `json:"bind_sources"`
	RequireBind bool                `json:"require_bind"`
}

// FileInput is one review path with its pre-rename path when the diff
// renamed it.
type FileInput struct {
	Path    string
	OldPath string
}

// FileResolution is the effective configuration for one path: the merged
// defaults with origins, the accumulated requirements, the resolved
// profile, and the contributing scopes in match order. Clauses apply from
// both the new and the old path, each retaining provenance, so a rename
// can never evade a clause.
type FileResolution struct {
	Path         string           `json:"path"`
	OldPath      string           `json:"old_path,omitempty"`
	Profile      string           `json:"profile"`
	Scopes       []string         `json:"scopes"`
	Requirements RequirementSet   `json:"requirements"`
	Defaults     ResolvedDefaults `json:"defaults"`
}

// repoSource labels root-clause provenance with the trusted base.
func repoSource(baseSHA string) string {
	if strings.TrimSpace(baseSHA) == "" {
		return "repository configuration"
	}
	return "repo base " + baseSHA
}

// scopeSource labels scope-clause provenance, annotating old-path-only
// matches on renames.
func scopeSource(name, oldPath string, oldOnly bool) string {
	if oldOnly {
		return "scope " + name + " (old path " + oldPath + ")"
	}
	return "scope " + name
}

// RootSet returns the accumulated root requirements for baseSHA: the
// repository-level clauses, checks, and bind behind every file. Verbs
// persist it in the run bundle so compliance reads stored policy,
// never reconstructed inputs.
func RootSet(repo *core.Config, baseSHA string) RequirementSet {
	return rootRequirements(requirementsOf(repo), baseSHA)
}

// rootRequirements converts the shared requirements into a set with base
// provenance.
func rootRequirements(reqs *core.ConfigRequirements, baseSHA string) RequirementSet {
	var out RequirementSet
	if reqs == nil {
		return out
	}
	source := repoSource(baseSHA)
	for i := range reqs.BlockClauses {
		out.addClause(&reqs.BlockClauses[i], source)
	}
	for _, check := range reqs.RequireChecks {
		out.addCheck(check, source)
	}
	if reqs.RequireBind != nil && *reqs.RequireBind {
		out.RequireBind, out.BindSources = true, []string{source}
	}
	return out
}

// addCheck unions one required check, recording its source once.
func (s *RequirementSet) addCheck(check, source string) {
	if !slices.Contains(s.Checks, check) {
		s.Checks = append(s.Checks, check)
	}
	if s.CheckOrigin == nil {
		s.CheckOrigin = map[string][]string{}
	}
	if !slices.Contains(s.CheckOrigin[check], source) {
		s.CheckOrigin[check] = append(s.CheckOrigin[check], source)
	}
}

// addClause unions one blocking clause: identical clauses (same
// threshold, categories, and paths) normalize to one entry with both
// provenances recorded, so conjunction never double-counts.
func (s *RequirementSet) addClause(clause *core.BlockClause, source string) {
	for i := range s.Clauses {
		if sameClause(&s.Clauses[i].Clause, clause) {
			if !slices.Contains(s.Clauses[i].Sources, source) {
				s.Clauses[i].Sources = append(s.Clauses[i].Sources, source)
			}
			return
		}
	}
	s.Clauses = append(s.Clauses, ClauseProvenance{Clause: *clause, Sources: []string{source}})
}

// sameClause reports whether two clauses constrain identically: equal
// threshold with equal category and path sets. Names may differ; the
// first name wins and every provenance is kept.
func sameClause(left, right *core.BlockClause) bool {
	return left.Threshold == right.Threshold &&
		slices.Equal(slices.Sorted(slices.Values(left.Categories)), slices.Sorted(slices.Values(right.Categories))) &&
		slices.Equal(slices.Sorted(slices.Values(left.Paths)), slices.Sorted(slices.Values(right.Paths)))
}

// accumulate folds one scope's inline requirements into the set: clauses
// union with provenance, checks union with per-check sources, and
// require_bind accumulates by OR, where false adds no constraint.
func (s *RequirementSet) accumulate(reqs *core.ConfigRequirements, source string) {
	if reqs == nil {
		return
	}
	for i := range reqs.BlockClauses {
		s.addClause(&reqs.BlockClauses[i], source)
	}
	for _, check := range reqs.RequireChecks {
		s.addCheck(check, source)
	}
	if reqs.RequireBind != nil && *reqs.RequireBind && !slices.Contains(s.BindSources, source) {
		s.RequireBind = true
		s.BindSources = append(s.BindSources, source)
	}
}

// scopeMatch reports whether a scope applies to the new path, the old
// path, or both. Exclude controls only whether the scope applies to a
// path; it never withholds a path globally.
func scopeMatch(scope *core.ConfigScope, path, oldPath string) (matchNew, matchOld bool) {
	matchNew = pathMatchesScope(scope, path)
	if oldPath != "" && oldPath != path {
		matchOld = pathMatchesScope(scope, oldPath)
	}
	return matchNew, matchOld
}

// pathMatchesScope applies the include and exclude globs to one path.
func pathMatchesScope(scope *core.ConfigScope, path string) bool {
	included := false
	for _, glob := range scope.Include {
		if Match(glob, path) {
			included = true
			break
		}
	}
	if !included {
		return false
	}
	for _, glob := range scope.Exclude {
		if Match(glob, path) {
			return false
		}
	}
	return true
}

// matchingScopes returns the scopes matching either path, in file order,
// with the old-path-only flags parallel to them.
func matchingScopes(scopes []core.ConfigScope, file FileInput) ([]core.ConfigScope, []bool) {
	var matched []core.ConfigScope
	var oldOnly []bool
	for i := range scopes {
		matchNew, matchOld := scopeMatch(&scopes[i], file.Path, file.OldPath)
		if !matchNew && !matchOld {
			continue
		}
		matched = append(matched, scopes[i])
		oldOnly = append(oldOnly, !matchNew && matchOld)
	}
	return matched, oldOnly
}

// clauseApplies reports whether a clause constrains one of the file's
// paths. Absent paths mean all paths; otherwise either the new or the
// old path may satisfy it.
func clauseApplies(clause *core.BlockClause, file FileInput) bool {
	if len(clause.Paths) == 0 {
		return true
	}
	for _, glob := range clause.Paths {
		if Match(glob, file.Path) {
			return true
		}
		if file.OldPath != "" && Match(glob, file.OldPath) {
			return true
		}
	}
	return false
}
