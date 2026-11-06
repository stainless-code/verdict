package config

import "github.com/stainless-code/verdict/internal/jq"

// DisplayFloorFor returns the effective display_min_severity for one finding
// path. A path the resolved review files matched carries that file's own
// default, so a scope clause outranks the repository default for its paths;
// a rename matches on either path. Unscoped paths and body findings fall back
// to the global default. Explicit flags and environment are already merged
// into the capture, so this reads the capture and nothing else.
func DisplayFloorFor(rs *RunState, path string) string {
	return floorFor(rs, path, displayFloorOf)
}

// PostFloorFor returns the effective post_min_severity for one finding path
// under the same precedence as DisplayFloorFor.
func PostFloorFor(rs *RunState, path string) string {
	return floorFor(rs, path, postFloorOf)
}

// BelowDisplayFloor reports whether one stored entry sits under the display
// floor applying to its own path: a legible severity under the floor rank is
// withheld, while a missing or unknown severity stays visible so the filter
// never hides what the vocabulary cannot place.
func BelowDisplayFloor(rs *RunState, entry any) bool {
	return underFloor(rs, entry, displayFloorOf)
}

// BelowPostFloor reports whether one stored entry sits under the post floor
// applying to its own path, under the same rules as BelowDisplayFloor. It
// decides the published payload only: every compliance and block gate keeps
// reading the full stored set.
func BelowPostFloor(rs *RunState, entry any) bool {
	return underFloor(rs, entry, postFloorOf)
}

func displayFloorOf(d *ResolvedDefaults) *string { return d.Values.DisplayMinSeverity }

func postFloorOf(d *ResolvedDefaults) *string { return d.Values.PostMinSeverity }

func floorFor(rs *RunState, path string, pick func(*ResolvedDefaults) *string) string {
	if rs == nil || rs.Resolved == nil {
		return ""
	}
	if path != "" {
		if f := reviewFileFor(rs.Resolved, path); f != nil {
			return orEmpty(pick(&f.Defaults))
		}
	}
	return orEmpty(pick(&rs.Resolved.Defaults))
}

// reviewFileFor finds the resolved review file a finding path belongs to, so a
// rename still answers for both its old and its new path.
func reviewFileFor(resolved *Resolved, path string) *FileResolution {
	for i := range resolved.Files {
		f := &resolved.Files[i]
		if f.Path == path || (f.OldPath != "" && f.OldPath == path) {
			return f
		}
	}
	return nil
}

func orEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// underFloor reports whether one entry sits below the floor for its own path:
// a legible severity under the floor rank is withheld, while a missing or
// unknown severity stays visible.
func underFloor(rs *RunState, entry any, pick func(*ResolvedDefaults) *string) bool {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	severity, _ := m.Get("severity")
	rank := SeverityRank(jq.StrOf(severity))
	if rank == 0 {
		return false
	}
	path, _ := m.Get("path")
	floor := floorFor(rs, jq.StrOf(path), pick)
	if floor == "" {
		return false
	}
	return rank < SeverityRank(floor)
}
