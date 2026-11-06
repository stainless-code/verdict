package config

import "github.com/stainless-code/verdict/internal/core"

// depthDeep is the profile depth requiring a recorded validation pass.
const depthDeep = "deep"

// PrivacyConflict is one reviewed path whose required content is withheld by
// the saved personal exclusions. It names the path, the requirement that
// needs it, the group, and the exclusion rule — never any withheld bytes.
type PrivacyConflict struct {
	Path       string `json:"path"`
	Group      string `json:"group,omitempty"`
	GroupLabel string `json:"group_label,omitempty"`
	Rule       string `json:"rule"`
	Deep       bool   `json:"deep,omitempty"`
}

// PrivacyConflicts reports the required review the captured exclusions make
// impossible: an actual reviewed path is withheld, and either an applicable
// block clause requires review of that path (categories never excuse missing
// content) or its plan group is deep. Checks such as head-current or
// no-live-threads without a content clause or deep group never conflict.
func PrivacyConflicts(rs *RunState) []PrivacyConflict {
	if rs == nil || rs.Resolved == nil {
		return nil
	}
	exclusions := rs.Resolved.Privacy.Exclusions
	if len(exclusions) == 0 {
		return nil
	}
	var out []PrivacyConflict
	for i := range rs.Resolved.Files {
		if conflict, ok := fileConflict(rs, &rs.Resolved.Files[i], exclusions); ok {
			out = append(out, conflict)
		}
	}
	return out
}

// fileConflict reports the one conflict a reviewed file states, if any.
func fileConflict(rs *RunState, file *FileResolution, exclusions []string) (PrivacyConflict, bool) {
	path, rule, withheld := withheldPath(file, exclusions)
	if !withheld {
		return PrivacyConflict{}, false
	}
	group := groupForPath(rs.Resolved.Plan, file.Path)
	deep := group != nil && group.Depth == depthDeep
	if !deep && !clauseRequiresPath(file, path) {
		return PrivacyConflict{}, false
	}
	conflict := PrivacyConflict{Path: path, Rule: rule, Deep: deep}
	if group != nil {
		conflict.Group, conflict.GroupLabel = group.ID, group.Label
	}
	return conflict, true
}

// withheldPath reports the reviewed path withheld by an exclusion and the
// rule that matched; a renamed file's old path is checked too.
func withheldPath(file *FileResolution, exclusions []string) (string, string, bool) {
	for _, path := range []string{file.Path, file.OldPath} {
		if path == "" {
			continue
		}
		if rule := matchingRule(exclusions, path); rule != "" {
			return path, rule, true
		}
	}
	return "", "", false
}

// matchingRule names the first exclusion glob covering path.
func matchingRule(exclusions []string, path string) string {
	for _, glob := range exclusions {
		if Match(glob, path) {
			return glob
		}
	}
	return ""
}

// clauseRequiresPath reports whether a block clause applicable to this file
// requires review of path: a clause naming no paths covers every path, and a
// matching clause path covers path itself (or the renamed old path).
func clauseRequiresPath(file *FileResolution, path string) bool {
	for i := range file.Requirements.Clauses {
		if clauseCoversPath(&file.Requirements.Clauses[i].Clause, file.OldPath, path) {
			return true
		}
	}
	return false
}

// clauseCoversPath reports whether one clause names path, or names no paths
// (covering every path), or names the renamed old path.
func clauseCoversPath(clause *core.BlockClause, oldPath, path string) bool {
	if len(clause.Paths) == 0 {
		return true
	}
	for _, glob := range clause.Paths {
		if Match(glob, path) || (oldPath != "" && Match(glob, oldPath)) {
			return true
		}
	}
	return false
}
